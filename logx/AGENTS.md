# logx/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `logx` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 logx 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/logx`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）与 `../dto`。
- 定位：**结构化日志**——zap + lumberjack，按级别分文件精确收录，单例 + 独立实例。

## 目录结构

```
├── logx.go        # Config、normConfig、Logger（Info/Warn/Error/Debug/With/Zap/Sync/Close）、全局门面
├── logx_test.go   # 配置归一化、级别文件精确收录、最低级别阈值、Disable、With 派生、caller、热重载测试
└── README.md      # 接入教程
```

## 核心约定

- **单 Logger + Tee Core**：`debug` / `info` / `warn` / `error` 四个级别文件**精确匹配收录**（error 档含 panic/fatal），日志不重复写盘，落盘结构 `根目录/日期/级别.log`；lumberjack 首次写入才创建文件，import 无副作用。
- **`Logger` 方法**：`Info` / `Warn` / `Error` / `Debug`（`msg` 为空回退级别名，字段传 `map[string]any`，多表按序合并覆盖、输出按键排序）；`With(map)` 派生带固定字段的子实例（原实例不受影响）；`Error` 自动附带堆栈；`Zap()` 暴露底层实例；`Sync()` 刷缓冲、`Close()` 刷缓冲并释放 lumberjack 文件句柄（**With 派生实例不持有句柄**，由原实例统一关闭）。
- **配置**：`logx.Config` 零值即可用（`Disable` 零值 false 即启用）；`normConfig()` 补齐默认值（`Root=runtime/logs`、`Level=debug`、`Size=10MB`、`Age=7天`、`Backups=20`）；`Console=true` 时彩色文本同步输出到 stdout；`Level` 控制最低写入级别；`Disable=true` 构建 Nop Logger 零开销。
- **caller 定位**：`zap.AddCaller` + `AddCallerSkip(2)`，指向业务调用方。
- **全局门面**：控制器单例 `logx.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `logx.Log`；热重载原子替换后 `Close()` 旧实例释放句柄。`logx.New(config)` 创建独立实例。

## 安全约定

- 输出含个人信息的内容到日志前应使用 `utils.Mask` 脱敏。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，临时目录真实落盘验证，**禁止联网**。
- 现有覆盖：配置归一化、级别文件精确收录、最低级别阈值、Disable、With 派生、caller 定位、控制器热重载。

## 构建与测试命令

```bash
cd logx && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本（如 `+0.1.0`、`+1.0.0`），否则每次发 tag 一律在当前版本末位 `+0.0.1`，不自行跨版本。

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
