# taskx/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `taskx` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 taskx 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/taskx`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）、`../dto` 与 `../logx`。
- 定位：**异步队列**——统一 Engine + file / Redis 双 Broker，支持重试、租约、死信、周期任务与检视管理。

## 目录结构

```
├── taskx.go       # Broker 接口、Driver 链式入队与消费生命周期、Engine、包级 Queue 门面
├── config.go      # Config 配置结构、normConfig 默认值补齐、全局门面（依赖 utils）
├── engine.go      # Engine：worker、加权队列、重试退避、超时、租约续期与回收、优雅退出
├── file.go        # file Broker（afero 六状态目录、O_EXCL 锁、原子 Rename）
├── redis.go       # redis Broker（LIST/ZSET/HASH + Lua 原子迁移）
├── inspect.go     # Inspect / Manage：计数、分页、重跑、删除与终态清空
├── scheduler.go   # Scheduler：只内置 Every 与 NextFunc，不自行解析 cron
├── taskx_test.go  # 双 Broker 契约、并发排他认领、租约、去重、重试与 panic、死信钩子、优雅退出、Scheduler、Inspect/Manage
└── README.md      # 接入教程
```

## 核心约定

- **`Broker` 是唯一扩展点**，内置 `file` / `redis`，只提供原子存储原语；worker、加权队列、重试退避、超时、租约续期与回收、优雅退出均由唯一 `Engine` 编排，**禁止在后端复制消费语义**。
- **`Driver` 链式**：入队（`New` / `Queue` / `In` / `At` / `MaxRetry` / `Timeout` / `Deadline` / `Retention` / `Unique` / `TaskID` / `Enqueue`）与消费生命周期（`Handle` / `Use` / `Run` / `Shutdown`）。包级 `Queue` 是可调用函数门面，同时支持 `taskx.Queue("low")` 选项和 `taskx.Queue.New(...)` 链式入口。
- **投递保证 at-least-once**：认领即加租约，成功才 Ack，租约过期自动重投；Handler 必须业务幂等。TaskID 冲突返回 `ErrTaskIdConflict`，内容去重冲突返回 `ErrDuplicateTask`。失败钩子 `ErrorHandler` 每次失败触发（含重试中的失败），死信钩子 `ArchiveHandler` 仅在归档成功后触发一次。
- **file 后端**：afero 六状态目录、`O_EXCL` 锁与原子 Rename，默认根目录 `./runtime/queue`，推荐单进程且单队列千级以内；**redis 后端**：LIST/ZSET/HASH + Lua 原子迁移，建议独立逻辑库，连接失败不静默降级。
- **`Inspect` / `Manage`**：计数、分页、重跑、删除与终态清空；**`Scheduler`** 只内置 `Every` 与 `NextFunc`，不自行解析 cron。
- **全局门面**：`taskx.Inst` + `taskx.Queue`，热重载保留已注册 Handler/Middleware，并在替换前优雅关闭旧引擎。

## 安全约定

- Redis 凭据通过 `taskx.Config` 运行时注入，仓库中不得硬编码任何真实凭据。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，Redis 用 miniredis，**禁止联网**。
- 现有覆盖：file/redis Broker 契约、并发排他认领、状态搬运、租约、去重、引擎重试与 panic、死信归档钩子、优雅退出、Scheduler、Inspect/Manage。

## 构建与测试命令

```bash
cd taskx && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本（如 `+0.1.0`、`+1.0.0`），否则每次发 tag 一律在当前版本末位 `+0.0.1`，不自行跨版本。

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
