# cachex/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `cachex` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 cachex 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/cachex`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）与 `../dto`。
- 定位：**缓存能力**，以接口模式封装文件 / 内存 / Redis 缓存，注册表 + 链式调用，可扩展后端。

## 目录结构

```
├── cachex.go      # Store 接口、registry 注册表（变量初始化登记）、Driver 链式包装
├── config.go      # Config 配置结构、normConfig 默认值补齐、defaultContext 分段上下文
├── facade.go      # 全局门面：Inst 控制器单例 + Cache 全局实例（storeError 占位）
├── file.go        # 文件驱动（afero，临时文件 + Rename 原子写入，可注入 MemMapFs 测试）
├── memory.go      # 内存驱动（ristretto v2，TinyLFU 准入 + SampledLFU 淘汰，分段锁原子方法）
├── layered.go     # 分层驱动（L1 内存 + L2 文件，cache-aside：读回源回灌、写权威层后失效 L1）
├── redis.go       # Redis 驱动（go-redis，Clear 按前缀扫描，前缀空回退 FlushDB）
└── cachex_test.go # 注册表、链式、配置归一化、标签簿记、文件/内存/分层驱动实测、热重载测试
```

## 核心约定

- **`Store` 接口是唯一扩展点**：`Has` / `Get` / `Set(key, value, expired)` / `Delete` / `Clear` 五个读写方法，外加 `Incr(key, expired)` / `SetNX(key, value, expired)` / `TTL(key)` 三个原子方法（计数、占位、存活查询，支撑安全限流等场景）。内置驱动在 `cachex.go` 的 `registry` 变量初始化时登记；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名覆盖。新增内置驱动：新建文件实现 `Store`，并在 `registry` 登记一行。
- **`Store` 契约**：键由 Driver 层命名（`前缀-MD5前16位(key)`），驱动按原名持久化；`Set` 的 value 须可 JSON 序列化，**`expired <= 0` 表示永不过期**；`Get` 未命中或已过期返回 `nil`；读写方法以 `bool` 表示成功与否；**原子方法返回 `error` 暴露后端故障**（fail-closed 调用方依赖该错误，不得静默吞掉）；`Incr` 仅在自增结果为 1 时写入过期时间（固定窗口语义，redis 用 Lua 保证原子，file / memory 用分段锁 `shardLocks` 保证同键串行、异键并行）；`TTL` 约定 `>0` 有效、`0` 不存在或已过期、`-1` 永不过期（file 以"一百年时间戳"哨兵还原 -1）。
- **`Driver` 链式包装**（`Tag` / `Key` / `Expired` / `Has` / `Get` / `Set` / `Delete` / `Clear` / `Incr` / `SetNX` / `TTL`），**值语义**：每次链式调用返回副本。**标签簿记统一收敛在 Driver 层**（成员列表永不过期；读-改-写带键控锁），后端不感知标签；`Incr`/`SetNX` 不参与标签簿记。
- **配置自包含**：`cachex.Config`（含 `file` / `memory` / `redis` 三组内置驱动配置，外部扩展配置放 `Config.Options`）；`normConfig()` 补齐默认值（引擎未注册回退 `file`，默认前缀 `AIDE`、默认过期 7200 秒）；`defaultContext()` 按引擎取对应分段的前缀与过期时间。
- **内存驱动**（`memory.go`，ristretto v2）：写路径统一 `Wait()` 对齐 file/redis 的同步可见性；`MaxCost` 语义 = 最大条目数（`IgnoreInternalCost` + cost 恒 1）；`Incr`/`SetNX` 用分段锁（`shardLocks`，256 分片按键哈希取锁）与 `GetTTL` 保留原过期；值不经 JSON 往返（类型保留、引用共享）；`Clear` 为实例级清空；实现 `io.Closer`，门面热重载自动关闭旧实例（独立实例由调用方 Close）。
- **分层驱动**（`layered.go`，L1 = `memory` + L2 = `file`）：**cache-aside 模式**——写先落 L2（权威，失败即整体失败）再失效 L1，读 L1 未命中回源 L2 并带剩余 TTL 回灌（ristretto 无法枚举键，只能懒加载回源，不做启动预热）；不变式 L1 是 L2 的保守子集（回灌 TTL 秒级向下取整，L1 先于 L2 过期）；`Incr`/`SetNX` 委托 L2 保证重启后计数连续；`Clear` 继承 file 语义（清空整个 Root 目录）；实现 `io.Closer`（关闭 L1）。
- **全局门面**：控制器单例 `cachex.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `cachex.Cache`。驱动初始化失败时全局位用 `storeError` 占位，所有操作返回失败。

## 安全约定

- Redis 凭据通过 `cachex.Config` 在运行时注入，仓库中不得硬编码任何真实凭据。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，**禁止联网**（Redis 用 miniredis）；文件驱动用 `afero.NewMemMapFs` 实测。
- 现有覆盖：注册表、链式实例、配置归一化、过期解析、标签簿记、标签并发簿记回归、文件驱动实测、memory 驱动实测（读写/类型保留/TTL 三态/过期/Close 幂等）、分层驱动实测（回源回灌/写失效一致性/重启恢复/计数连续）、file 与 memory 分段锁并发回归（同键串行、异键不错串）、控制器热重载（含关闭旧 memory/layered 实例）、原子方法（Driver 透传与 nil 驱动报错、file 固定窗口/过期保留/并发自增、redis 经 miniredis 实测 Lua 自增/SetNX/TTL）、storeError 原子方法错误透传。

## 构建与测试命令

```bash
cd cachex && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本（如 `+0.1.0`、`+1.0.0`），否则每次发 tag 一律在当前版本末位 `+0.0.1`，不自行跨版本。

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
