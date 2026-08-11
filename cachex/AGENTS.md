# cachex/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `cachex` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 cachex 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/cachex`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）与 `../dto`。
- 定位：**缓存能力**，以接口模式封装文件 / Redis 缓存，注册表 + 链式调用，可扩展后端。

## 目录结构

```
├── cachex.go      # Store 接口、registry 注册表（变量初始化登记）、Driver 链式包装
├── config.go      # Config 配置结构、normConfig 默认值补齐、defaultContext 分段上下文
├── facade.go      # 全局门面：Inst 控制器单例 + Cache 全局实例（storeError 占位）
├── file.go        # 文件驱动（afero，临时文件 + Rename 原子写入，可注入 MemMapFs 测试）
├── redis.go       # Redis 驱动（go-redis，Clear 按前缀扫描，前缀空回退 FlushDB）
└── cachex_test.go # 注册表、链式、配置归一化、标签簿记、文件驱动实测、热重载测试
```

## 核心约定

- **`Store` 接口是唯一扩展点**：只含 `Has` / `Get` / `Set(key, value, expired)` / `Delete` / `Clear` 五个方法。内置驱动在 `cachex.go` 的 `registry` 变量初始化时登记；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名覆盖。新增内置驱动：新建文件实现 `Store`，并在 `registry` 登记一行。
- **`Store` 契约**：键由 Driver 层命名（`前缀-MD5前16位(key)`），驱动按原名持久化；`Set` 的 value 须可 JSON 序列化，**`expired <= 0` 表示永不过期**；`Get` 未命中或已过期返回 `nil`。
- **`Driver` 链式包装**（`Tag` / `Key` / `Expired` / `Has` / `Get` / `Set` / `Delete` / `Clear`），**值语义**：每次链式调用返回副本。**标签簿记统一收敛在 Driver 层**（成员列表永不过期；读-改-写带键控锁），后端不感知标签。
- **配置自包含**：`cachex.Config`（含 `file` / `redis` 两组内置驱动配置，外部扩展配置放 `Config.Options`）；`normConfig()` 补齐默认值（引擎未注册回退 `file`，默认前缀 `AIDE`、默认过期 7200 秒）；`defaultContext()` 按引擎取对应分段的前缀与过期时间。
- **全局门面**：控制器单例 `cachex.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `cachex.Cache`。驱动初始化失败时全局位用 `storeError` 占位，所有操作返回失败。

## 安全约定

- Redis 凭据通过 `cachex.Config` 在运行时注入，仓库中不得硬编码任何真实凭据。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，**禁止联网**（Redis 用 miniredis）；文件驱动用 `afero.NewMemMapFs` 实测。
- 现有覆盖：注册表、链式实例、配置归一化、过期解析、标签簿记、标签并发簿记回归、文件驱动实测、控制器热重载。

## 构建与测试命令

```bash
cd cachex && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
