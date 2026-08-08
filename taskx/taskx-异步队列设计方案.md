# taskx：aide 异步队列子包设计方案

> 目标：在 `github.com/inis-io/aide` 新增 `taskx` 子包，以 aide 既有的「接口模式」封装异步任务队列，
> 功能对齐 `github.com/hibiken/asynq`，并突破其「仅支持 Redis」的限制——内置 **file（本地文件缓存）** 与
> **redis** 双后端，第三方后端可经注册表扩展。
>
> 动机场景：licen-hub 回调通知推送（参考 `docs/plan/回调通知与事件推送设计方案.md`）需要可靠的任务派发，
> 但 licen-hub 的部署模型是单二进制、Redis 可选；pay 项目使用 hibiken/asynq 的商户通知链路存在
> 「入队失败即丢失、自入队重试崩溃断链、无补偿扫描」的已知缺口。
>
> **本方案当前仅为设计文档，未实施。** 实施时遵循 `aide/AGENTS.md` 的全部约定（中文注释、`this` 接收者、
> 分隔带、值语义链式、`Inst` 门面、标准库测试）。

## 1. 背景与目标

### 1.1 为什么做 taskx

1. **aide 子包家族缺队列成员**：`cachex`（缓存）、`storagex`（存储）、`pushx`（推送）、`logx`（日志）之后，异步队列是下一个通用基础能力；业务方（licen-hub、pay）目前各自为战（pay 直接用 hibiken/asynq，licen-hub 用 gocron 扫表）。
2. **hibiken/asynq 只支持 Redis**：licen-hub 单二进制私有化部署中 Redis 是可选件（`cachex` 在 Redis 不可达时降级文件缓存），硬依赖 Redis 的队列无法落地；需要「无 Redis 也能跑」的本地后端。
3. **pay 的 asynq 用法存在可靠性缺口**：入队在业务事务之外（Redis 挂了通知即丢失）、重试靠「自入队新任务」（两次入队之间进程崩溃则重试链断裂）、无补偿扫描兜底。taskx 的引擎语义（租约 + 到期搬运 + 崩溃回收）从机制上覆盖这些缺口。

### 1.2 设计目标

1. **功能对齐 asynq**：多队列优先级、并发消费、延迟/定时任务、重试退避、超时、去重、崩溃恢复、优雅退出、周期任务、检视管理——逐项对照见 §2；
2. **后端可插拔**：`Broker` 接口为唯一扩展点，内置 file / redis，第三方可注册（postgres、sqlite、甚至包装 hibiken/asynq 的适配器）；
3. **语义一致由构造保证**：单一引擎（Engine）编排全部队列语义，后端只实现存储原语，避免「两个引擎、两份语义」的漂移；
4. **零新直接依赖**：file 用 `afero`（已依赖）、redis 用 `go-redis/v9`（已依赖）、ID 用 `google/uuid`（已依赖）；
5. **完全复用 aide 门面约定**：与 `cachex.Cache` / `pushx.Push` 同构的使用体验（见 §4、§9）。

### 1.3 非目标

- 不实现 Group 任务聚合（hibiken 侧亦为实验特性，预留扩展位）；
- 不提供 Web 监控 UI（asynqmon 对应物）；只提供 `Inspect` Go API，由业务方自建页面（licen-hub 后台即用此 API 做「重推」）；
- 不做定时调度的完整 cron 表达式解析器（`@every` + `NextFunc` 钩子，cron 由适配器外接 robfig/cron，见 §10）；
- 不追求 Kafka/RabbitMQ 级吞吐；定位是「应用内可靠任务队列」（千级任务/分钟以内）。

## 2. hibiken/asynq 功能盘点与 taskx 对照

| asynq 能力 | taskx | 实现方式 / 差异说明 |
|---|---|---|
| `Client.Enqueue(task, opts...)` | ✅ | 链式 `Driver.New(...).Enqueue(ctx)` 与函数式 `taskx.Enqueue(ctx, msg, opts...)` 双入口 |
| 队列 `Queue(name)` | ✅ | 默认队列 `default` |
| 多队列权重优先级 `Queues map[string]int` | ✅ | 引擎按权重随机选取队列顺序（与 asynq 同策略） |
| 并发 `Concurrency` | ✅ | worker 协程池，默认 10 |
| 延迟/定时 `ProcessIn / ProcessAt` | ✅ | `In(d)` / `At(t)`；进入 scheduled 态，到期搬运 |
| 重试 `MaxRetry` + `RetryDelayFunc` | ✅ | 默认退避与 asynq 同风格（`attempts^4` 秒 + 随机抖动），可注入自定义函数（如 licen-hub 的定长退避表） |
| 超时 `Timeout` / 截止时间 `Deadline` | ✅ | 执行期 `context.WithTimeout`；`Deadline` 直接指定固定时间点（两者并存时取较早者） |
| 确定性 ID `TaskID` | ✅ | 重复 ID 返回 `ErrTaskIdConflict`（pay 的 `notify:{tradeNo}:r{n}:a{m}` 模式直接可用） |
| 内容去重 `Unique(ttl)` | ✅ | digest = SHA-256(type + payload)，窗口内重复返回 `ErrDuplicateTask` |
| 完成保留 `Retention` | ✅ | completed 态按保留期由清理线程删除；0 = 成功即删（asynq 默认行为） |
| 死信 archived（重试耗尽） | ✅ | 永久保留，支持检视/重跑/清空 |
| 租约 + 崩溃恢复 | ✅ | 认领即加租约，执行中心跳续租；租约过期任务被搬运线程回收重投（替代 Redis 过期键语义，file 后端同样成立） |
| 优雅退出 `Shutdown` | ✅ | 停止认领 → 等在途任务至 `ShutdownTimeout` → 超时取消 → 未完成任务 `Release` 立即回 pending（不坐等租约过期） |
| `Mux` 路由 / `HandlerFunc` | ✅ | 同构 API；未注册类型返回内部错误并重试（防配置遗漏静默丢任务） |
| 中间件 `Use` | ✅ | 与 asynq 相同的包裹式中间件 |
| `ErrorHandler` 钩子 | ✅ | 每次失败回调（告警/指标接入点） |
| 日志 `Logger` | ✅ | 定义窄 `Logger` 接口，`logx` 一行适配；缺省静默 |
| 周期任务 `Scheduler`（cron spec / `@every`） | ✅ 变体 | 内置 `@every` 与 `NextFunc` 钩子；cron 表达式经适配器外接（§10） |
| 检视 `Inspector`（列表/删除/重跑/归档） | ✅ 变体 | `Inspect` + `Manage` API；无 Web UI |
| `asynqmon` Web UI | ❌ | 业务方用 Inspect API 自建（licen-hub 后台页面） |
| Group 任务聚合 | ❌ 预留 | hibiken 亦为实验特性；Message 预留 `Group` 字段位 |
| Redis Cluster / Sentinel | ➖ | redis 驱动配置面预留 `Options` 注入点；单节点为主 |

## 3. 总体架构

### 3.1 分层视图

```
┌──────────────────────────── 业务方 ────────────────────────────┐
│  链式入队：taskx.Queue.New("trade:notify", p).In(15s).Enqueue() │
│  注册消费：taskx.Queue.Handle("trade:notify", handler)          │
│  启动运行：go taskx.Queue.Run(ctx)                              │
└──────────────────────────────┬────────────────────────────────┘
                               ▼
┌──────────────────────────── 门面层（facade.go）─────────────────┐
│  Inst 控制器单例（Init / ReloadIfChanged，sync.RWMutex）          │
│  全局实例 Queue；初始化失败 brokerError 占位（操作返回原始错误）     │
└──────────────────────────────┬────────────────────────────────┘
                               ▼
┌──────────────────────────── Driver（driver.go）─────────────────┐
│  Broker 之上的链式包装（值语义副本）：New/Queue/In/At/MaxRetry/     │
│  Timeout/Deadline/Retention/Unique/TaskID/Enqueue                │
│  + Handle/Use（挂到内建 Mux）+ Run/Shutdown（控制引擎）            │
└──────────────────────────────┬────────────────────────────────┘
                               ▼
┌──────────────────────────── Engine（engine.go，唯一）────────────┐
│  worker 池（Concurrency）、队列权重选取、Mux 路由 + 中间件、        │
│  退避计算（RetryDelayFunc）、到期搬运（Promote）、租约心跳、        │
│  优雅退出（Release 在途）、清理线程（completed/locks 过期）         │
└───────────────┬──────────────────────────────┬─────────────────┘
                ▼                              ▼
┌──────────────────────┐          ┌──────────────────────────────┐
│ file 驱动（file.go） │          │ redis 驱动（redis.go）         │
│ afero 文件层：目录状态机│          │ go-redis/v9：LIST/ZSET/HASH   │
│ 原子 rename 认领/租约字段│          │ + Lua 原子脚本               │
└──────────────────────┘          └──────────────────────────────┘
        ▲ 实现同一个 Broker 接口；第三方在自己包 init() 里 Register
```

与家族同构对照：`Broker` ↔ `cachex.Store` / `pushx.Sender` / `storagex.Store`；`Driver` ↔ 各包链式包装；`Inst` + `Queue` ↔ 各包控制器 + 全局实例。唯一差异：taskx 的 Driver 之上多了一个**引擎**（因为队列有消费侧生命周期，其余子包只有调用侧）。

### 3.2 包文件布局

```
taskx/
├── taskx.go       # Broker 接口、registry、Register、New(name, config)、编译期接口校验
├── config.go      # Config / FileConfig / RedisConfig、normConfig（默认值见 §9.2）
├── facade.go      # Inst 控制器单例、全局实例 Queue、brokerError 占位
├── message.go     # Message、NewMessage、Option 函数集
├── driver.go      # Driver 链式包装（值语义）、Handle/Use、Run/Shutdown
├── engine.go      # 引擎：worker 池、搬运、心跳、优雅退出、退避与清理
├── mux.go         # Mux 路由 + 中间件链
├── file.go        # file 驱动（afero 文件层，§6）
├── redis.go       # redis 驱动（go-redis + Lua，§7）
├── scheduler.go   # 周期任务（§10）
├── inspect.go     # InspectQuery / InspectResult / ManageOp（§11）
└── taskx_test.go  # 契约测试套件 + 引擎单测（§12）
```

### 3.3 任务状态机

```
            In/At 选项                Claim（原子认领+加租约）
 入队 ──────► scheduled ──Promote 到期──► pending ──────────────► active
                                        ▲  ▲                      │
                                        │  │         ┌────────────┼─────────────┐
                                        │  │         ▼            ▼             ▼
                                        │  │      成功 Ack    失败且未超限    失败且超限
                                        │  │         │      Retry（attempts+1） Archive
                                        │  │         ▼            ▼             ▼
                                        │  │     completed    retry ──到期──┐ archived（死信）
                                        │  │    （Retention 到期清理）       │  （Manage 可重跑回 pending）
                                        │  └────────────────────────────────┘
                                        └────── 租约过期回收（崩溃/失联）◄── active
                                               优雅退出 Release ──► pending
```

## 4. 核心类型与公开 API 草案

### 4.1 Message 与入队选项

```go
// Message - 任务消息
type Message struct {
    Id        string          `json:"id"`         // 任务 ID（缺省自动生成 uuid；TaskID 选项指定则为确定性 ID）
    Type      string          `json:"type"`       // 任务类型名（Mux 路由键，如 "trade:notify"）
    Payload   json.RawMessage `json:"payload"`    // 任务载荷（JSON）
    Queue     string          `json:"queue"`      // 所属队列（缺省 default）
    Group     string          `json:"group"`      // 聚合组（预留，本期不实现聚合语义）
    MaxRetry  int             `json:"maxRetry"`   // 最大重试次数（0 = 失败即归档）
    Timeout   time.Duration   `json:"timeout"`    // 单次执行超时（0 = 不限制）
    Deadline  time.Time       `json:"deadline"`   // 执行截止时间点（零值 = 不限制；与 Timeout 并存取较早者）
    Retention time.Duration   `json:"retention"`  // 完成后保留时长（0 = 成功即删）
    UniqueTTL time.Duration   `json:"uniqueTtl"`  // 内容去重窗口（0 = 不去重）
    Attempts  int             `json:"attempts"`   // 已执行次数（引擎维护）
    ProcessAt time.Time       `json:"processAt"`  // 期望执行时间（In/At 选项写入）
    RetryAt   time.Time       `json:"retryAt"`    // 下次重试时间（引擎计算后写入）
    LastError string          `json:"lastError"`  // 最近一次失败原因
    CreatedAt time.Time       `json:"createdAt"`  // 入队时间
}
```

> 序列化说明：对外 API 用 `time.Duration / time.Time`，落盘/落库编码统一为毫秒 int64
>（`timeoutMs / deadlineMs / processAtMs...`），编码细节由各自驱动收敛，不泄漏到业务侧。

```go
// NewMessage - 创建任务消息
func NewMessage(taskType string, payload any) *Message  // payload 非 RawMessage 时自动 JSON 编码

// Option - 入队选项（函数式入口用）
type Option func(*Message)
func ProcessIn(d time.Duration) Option   // 延迟入队
func ProcessAt(t time.Time) Option       // 定时入队
func Queue(name string) Option
func MaxRetry(n int) Option
func Timeout(d time.Duration) Option
func Deadline(t time.Time) Option
func Retention(d time.Duration) Option
func Unique(ttl time.Duration) Option    // 内容去重（type+payload 摘要）
func TaskID(id string) Option            // 确定性 ID（防重）

// 函数式入队
func Enqueue(ctx context.Context, msg *Message, opts ...Option) (string, error)
```

### 4.2 链式入队（Driver，值语义）

```go
// 与 pushx/cachex 链式一致：每次调用返回副本，天然隔离上下文
id, err := taskx.Queue.New("trade:notify", payload).
    Queue("critical").               // 缺省 default
    In(15 * time.Second).            // 延迟；At(t) 为定时
    MaxRetry(8).
    Timeout(30 * time.Second).
    Unique(time.Hour).
    TaskID("notify:TRD-2026-1:r1:a1").
    Enqueue(ctx)
```

### 4.3 消费侧

```go
// Handler - 任务处理器
type Handler interface {
    ProcessTask(ctx context.Context, msg *Message) error
}

// HandlerFunc - 函数适配器
type HandlerFunc func(ctx context.Context, msg *Message) error

// Middleware - 中间件（与 asynq 同构的包裹式）
type Middleware func(next Handler) Handler

// 注册与启动（门面全局实例）
taskx.Queue.Use(recoveryMiddleware, loggingMiddleware)
taskx.Queue.Handle("trade:notify", taskx.HandlerFunc(func(ctx context.Context, m *taskx.Message) error {
    // 业务处理；返回 error 即触发重试（引擎按 RetryDelay 计算下次时间）
    return nil
}))
go taskx.Queue.Run(ctx)          // 阻塞运行引擎（worker 池 + 搬运 + 清理）
// 退出：taskx.Queue.Shutdown()   // 优雅退出（Stop accepting → 等待 → Release 在途）
```

约定：

- `Run` 幂等（重复调用返回错误或忽略，实施时定）、`Shutdown` 后可再次 `Run`（热重载场景）；
- handler 返回 `nil` → `Ack`；返回 `error` → `ErrorHandler` 钩子 + 重试/归档判定；**panic 由引擎 recover 并按失败处理**（同时回传错误钩子，等价 asynq 行为）；
- `ctx` 携带 Timeout/Deadline 取消语义，handler 应响应取消；忽略取消导致超时的任务按失败计。

### 4.4 错误约定

```go
var (
    ErrTaskIdConflict = errors.New("taskx: 任务 ID 冲突")   // TaskID 已存在
    ErrDuplicateTask  = errors.New("taskx: 任务重复")        // Unique 窗口内内容重复
    ErrNotRunning     = errors.New("taskx: 引擎未运行")
    ErrDriverNotReady = errors.New("taskx: 驱动未就绪")      // brokerError 占位统一包装原始错误
)
```

## 5. Broker 接口契约

`Broker` 是 taskx 的唯一扩展点（对应 `cachex.Store` 的家族地位）。引擎编排全部队列语义，Broker 只提供**原子存储原语**——这是「双后端语义一致」的结构性保证。

```go
// Broker - 队列存储后端接口（唯一扩展点）
type Broker interface {
    // Enqueue - 原子写入新任务（含 TaskID/Unique 唯一性检查；冲突返回 ErrTaskIdConflict/ErrDuplicateTask）
    // msg.ProcessAt 在未来 → 进 scheduled，否则进 pending
    Enqueue(ctx context.Context, msg *Message) error

    // Claim - 按给定顺序尝试从各队列 pending 原子认领一个任务：置为 active 并加租约（leaseUntil = now + LeaseTTL）
    // 全部队列为空返回 (nil, nil)；认领必须原子——并发/多副本下同一条任务只能被一个调用方拿走
    Claim(ctx context.Context, queues []string) (*Message, error)

    // Promote - 一轮到期搬运（引擎每 PollInterval 调用一次）：
    //   1) scheduled 中 ProcessAt 到期 → pending
    //   2) retry 中 RetryAt 到期 → pending
    //   3) active 中租约过期（崩溃/失联回收）→ pending
    // 返回搬运条数；该过程可批量，但必须对单条任务原子（搬一半崩溃不得丢任务）
    Promote(ctx context.Context) (int, error)

    // Ack - 执行成功：Retention > 0 → 转 completed（记录完成时间），否则直接删除；同时释放唯一锁
    Ack(ctx context.Context, msg *Message) error

    // Retry - 执行失败且未超限：attempts+1、记录 LastError、按 msg.RetryAt（引擎已算好）转 retry；同时释放唯一锁
    Retry(ctx context.Context, msg *Message, cause error) error

    // Archive - 执行失败且已达上限：转 archived（死信，永久保留）；同时释放唯一锁
    Archive(ctx context.Context, msg *Message, cause error) error

    // Release - 优雅退出时将在途任务立即归还 pending（清租约；attempts 不变）
    Release(ctx context.Context, msg *Message) error

    // Extend - 续租（执行中心跳，引擎每 LeaseTTL/2 调用一次）
    Extend(ctx context.Context, msg *Message, leaseUntil time.Time) error

    // Inspect - 检视（§11）：计数与分页列表
    Inspect(ctx context.Context, query InspectQuery) (*InspectResult, error)

    // Manage - 管理操作（§11）：重跑（archived/retry/scheduled → pending）、删除、清空
    Manage(ctx context.Context, op ManageOp) error

    // Close - 释放资源（文件句柄/连接池）
    Close() error
}
```

### 5.1 契约细则

1. **原子性底线**：`Enqueue`（含唯一锁占位）、`Claim`、`Promote` 的单任务搬运、`Ack/Retry/Archive/Release` 必须原子或崩溃安全——任何时刻一个任务在存储中**有且仅有一个归属状态**；驱动实现允许「状态文件冗余但可判别」（如 file 驱动的临时文件），不允许「两个 worker 同时认为自己认领成功」。
2. **唯一锁语义**：`UniqueTTL > 0` 时以 `SHA-256(type+payload)` 为锁键；`TaskID` 指定时以 `task:<id>` 为锁键；锁随 `Ack/Retry/Archive` 释放（TaskID 锁在终态前一直持有，保证同一业务键整条重试链只有一个任务），也可随 TTL 自然过期兜底。**TaskID 锁的 TTL = UniqueTTL 与队列生命周期无关时，建议实现为「显式释放 + 24h 兜底过期」**（防崩溃留锁）。
3. **租约语义**：`Claim` 写入 `leaseUntil = now + LeaseTTL`；`Extend` 顺延；`Promote` 回收过期者。Broker 不主动判断「任务是否卡死」，只做时间比较。
4. **幂等容忍**：引擎保证按状态机调用，但驱动应对「对非 active 任务 Ack」「对已删除任务 Extend」等时序边缘返回 nil（幂等成功）而非报错——崩溃恢复路径上这类调用不可避免。
5. **并发假设**：引擎单实例内并发调用各方法；redis 后端还需容忍**多进程**并发；file 后端文档建议单进程（§6.4）。
6. **错误分类**：唯一性冲突返回 §4.4 的哨兵错误（调用方据此静默或降级）；其余错误原样上抛，引擎记录 `LastError` 并进入重试。

### 5.2 注册与扩展

```go
// registry - 内置驱动登记表（变量初始化时登记，不依赖文件 init 顺序）
var registry = map[string]Factory{
    "file":  newFileBroker,
    "redis": newRedisBroker,
}

// Factory - 驱动工厂
type Factory func(config Config) (Broker, error)

// Register - 注册外部驱动（同名覆盖先注册者；外部包在 init() 中调用）
func Register(name string, factory Factory)

// New - 创建独立实例（与门面全局位互不影响）
func New(name string, config Config) (*Driver, error)
```

## 6. file 驱动设计（本地文件缓存后端）

### 6.1 设计基座：cachex/file.go 的文件缓存范式

需求指定以 `cachex/file.go`（文件缓存）为参考实现本地后端。file 驱动**完整镜像其工程范式**，但不直接复用 `cachex` 包或 `cachex.Store` 接口，原因：

| 队列需要的原语 | cachex.Store 现状 |
|---|---|
| 认领：抢占式「原子移动」（多 worker 抢同一任务，只有一个成功） | 仅 `Has/Get/Set/Delete/Clear` 五方法，无 rename/move 语义；`Get` + `Delete` 两步无法原子化，会重复认领 |
| 唯一锁：排他创建（存在即失败） | `Set` 为覆盖写，无 `O_EXCL` 语义；且 Driver 层键被哈希化为 `前缀-MD5前16位(key)`，锁键不可直接判读 |
| 状态机：六状态目录 + 保序列举（FIFO） | 键哈希化后无目录与顺序语义；过期模型是「缓存 TTL 命中即失效」，而队列需要「租约可续期、过期回收重投」与「完成后保留 Retention」两套时间语义 |
| 任务体原样存取 | 缓存值被包装为 `{expired, value}` JSON，多一层拆装 |

因此 file 驱动在 taskx 内建 afero 文件层，逐项镜像 `cachex/file.go` 的既有做法：

- **afero 文件系统抽象**：默认 `afero.NewOsFs()`，测试注入 `afero.NewMemMapFs()` 全程不落盘；
- **临时文件 + Rename 原子写入**：防写入中途崩溃留下半个任务文件；
- **Windows 兼容**：Rename 不允许覆盖已存在文件时，删除目标后重试；
- **根目录约定**：`Root` 默认落在 `runtime/` 下（queue 与 cachex 默认的 `runtime/cache` 并列），运行时产物不入库。

### 6.2 目录状态机

```
runtime/queue/                              # FileConfig.Root（默认 runtime/queue）
└── <queue>/                                # 每个队列一个目录（default、critical...）
    ├── pending/     <millis13位>-<seq6位>-<id>.json   # 待执行（文件名保序 ≈ FIFO）
    ├── active/      <id>.json                         # 执行中（含 leaseUntilMs）
    ├── scheduled/   <id>.json                         # 延迟/定时（含 processAtMs）
    ├── retry/       <id>.json                         # 等待重试（含 retryAtMs、attempts、lastError）
    ├── completed/   <id>.json                         # 已完成（含 completedAtMs；Retention 到期清理）
    ├── archived/    <id>.json                         # 死信（永久保留，Manage 可重跑/删除）
    └── locks/       <digest16>.lock                   # 唯一锁占位文件（内容 = 过期毫秒时间戳）
```

任务文件内容 = `Message` 的 JSON（时间字段编码为毫秒 int64）。文件即事实源，无额外索引；单队列千级任务内 `ReadDir` 开销可忽略（见 §8.4 量级边界）。

### 6.3 原子操作映射

| Broker 方法 | file 实现 |
|---|---|
| `Enqueue` | 唯一锁先行：`locks/<digest>.lock` 用 `O_CREATE\|O_EXCL` 排他创建（已存在 → 读内容判过期：未过期返回冲突错误，已过期删除重建）；随后任务 JSON 写 `pending/<seq>.json.tmp` → Rename 正式名（cachex 同款原子写）；`ProcessAt` 在未来则落 `scheduled/`。**入队失败（写文件出错）时已占的锁立即回滚删除**。 |
| `Claim` | 按队列顺序：`ReadDir(pending)` 文件名升序取第一个，`Rename(pending/f → active/<id>.json)` 前把 `leaseUntilMs` 写进任务内容（重写 temp+rename 到 active，同盘原子）；Rename 失败（已被抢走/目录为空）→ 尝试下一个。**同盘 rename 是天然的原子认领原语**：两个并发 Claim 只有一个能成功。 |
| `Promote` | 扫描 `scheduled`/`retry`：读文件判 `processAtMs/retryAtMs <= now` → Rename 到 `pending/`（重写为保序文件名）；扫描 `active`：`leaseUntilMs < now` → Rename 回 `pending/`（崩溃回收）；顺带懒清理 `locks/` 过期占位文件与 `completed/` 超保留期文件（亦可独立清理线程）。 |
| `Ack` | `Retention > 0`：写入 `completedAtMs` → Rename 到 `completed/`；否则 `Remove(active/<id>.json)`；删唯一锁文件。 |
| `Retry` | `attempts+1`、写 `lastError/retryAtMs` → Rename 到 `retry/`；删唯一锁。 |
| `Archive` | Rename 到 `archived/`；删唯一锁。 |
| `Release` | 清租约字段 → Rename 回 `pending/`。 |
| `Extend` | 重写 `active/<id>.json` 的 `leaseUntilMs`（temp+rename；文件已不存在则幂等返回 nil）。 |
| `Inspect` | 各目录 `ReadDir` 计数；列表模式按需读取文件内容（Marker 为偏移量的内存分页）。 |
| `Manage` | 重跑 = 源目录 Rename → `pending/`；删除 = `Remove`；清空 = `RemoveAll(目录)` 后重建。 |
| `Close` | 无句柄持有（每次操作即用即开），幂等 nil。 |

崩溃安全分析：任何时刻任务文件只属于一个状态目录；「写 temp + rename」保证不出现半个文件；唯一锁即使因崩溃残留，也有 TTL 过期 + Promote 懒清理兜底。`SyncWrites=true` 时在 rename 前对文件 `Sync()`（目录项落盘由文件系统保证尽力而为），换取更强的断电持久性，默认关闭。

### 6.4 使用边界

- **推荐单进程**（licen-hub 单二进制、桌面工具、边缘节点场景）；多进程共享同一目录「尽力而为」——rename 认领与 O_EXCL 锁在同一文件系统上跨进程有效，但**租约字段写在任务文件内，回收判断依赖各进程时钟一致**（NTP 同步前提下可用），NFS/SMB 等网络文件系统不提供保证。需要多副本时请用 redis 驱动。
- Windows 与 Linux 的 rename 语义差异按 cachex 既有兼容方案处理（覆盖场景删目标重试；队列流转中目标路径设计上不预存在，正常不会触发）。
- 单队列任务量建议 **≤ 数千条**（目录列举与逐文件读取的线性开销）；超过请用 redis 驱动（§8.4）。

## 7. redis 驱动设计

### 7.1 为什么不包装 hibiken/asynq

| 考量 | 包装 hibiken/asynq | go-redis 自研（本方案） |
|---|---|---|
| 直接依赖 | 新增 hibiken/asynq 及其依赖树 | **零新增**（`go-redis/v9` 已是 aide 直接依赖，cachex 在用） |
| 引擎数量 | 两个引擎（hibiken 引擎 + taskx 引擎），双后端语义靠测试对齐，漂移风险长期存在 | **单引擎**，Broker 只是存储原语，语义一致由构造保证 |
| 配置面 | hibiken 的 `RedisClientOpt/Config` 与 aide `Config/normConfig` 门面不同构，要做有损映射 | 与 `cachex.RedisConfig` 同构，业务方零学习成本 |
| 语义映射 | hibiken 的租约/归档/保留语义与 §5 契约存在边角差异（如 Retention 粒度、归档去重锁释放时机） | 按 §5 契约精确实现 |
| 成熟度的价值 | hibiken 引擎经生产验证——但它验证的是「引擎逻辑」，本方案引擎逻辑唯一且自测 | 存储层（LIST/ZSET/Lua）是成熟原语的直接组合 |

registry 是开放的：确有需要时，任何业务方都可以在自己仓库写「包装 hibiken/asynq 的适配 Broker」经 `Register("asynq", factory)` 接入（文档 §15 给出该扩展的要点提示），taskx 不内置。

### 7.2 key 结构

所有 key 带统一前缀（默认 `AIDE:TASKX:`，可配），并用 hash tag `{...}` 包裹队列段，保证 Redis Cluster 下同队列 key 落同一 slot（Lua 脚本要求）：

```
AIDE:TASKX:{<queue>}:pending     LIST      # 待执行（LPUSH 入队；认领从 RPOP 端出）
AIDE:TASKX:{<queue>}:active      HASH      # 执行中：field=<id>，value=任务 JSON（含 leaseUntilMs）
AIDE:TASKX:{<queue>}:lease       ZSET      # 租约索引：member=<id>，score=leaseUntilMs（回收扫描用）
AIDE:TASKX:{<queue>}:scheduled   ZSET      # 延迟/定时：member=<id>，score=processAtMs
AIDE:TASKX:{<queue>}:retry       ZSET      # 等待重试：member=<id>，score=retryAtMs
AIDE:TASKX:{<queue>}:msgs        HASH      # 任务体仓库：field=<id>，value=任务 JSON（pending/scheduled/retry/completed/archived 态的载荷统一存这里，active 态冗余一份便于租约更新）
AIDE:TASKX:{<queue>}:completed   ZSET      # 已完成：score=completedAtMs（Retention 到期 ZREMRANGEBYSCORE 清理）
AIDE:TASKX:{<queue>}:archived    ZSET      # 死信：score=archivedAtMs（永久，Manage 管理）
AIDE:TASKX:lock:<digest16>       STRING    # 唯一锁：SET NX PX（值=任务 ID，TTL=UniqueTTL；TaskID 锁为显式释放+兜底 TTL）
```

### 7.3 原子性：Lua 脚本

每个 Broker 方法对应一段 Lua（在 Redis 内原子执行），要点：

- **Enqueue**：唯一锁 `SET NX PX` → 失败按锁类型返回冲突；成功则 `HSET msgs` +（到期判断）`LPUSH pending` 或 `ZADD scheduled`；锁占位失败回滚 `DEL`。
- **Claim**：按脚本入参队列顺序依次 `RPOP pending` → 命中即 `HSET active`（重写 leaseUntilMs）+ `ZADD lease`，返回任务 JSON；全程单脚本原子，多副本安全。
- **Promote**：对每队列：`ZRANGEBYSCORE scheduled/retry 0 now` 逐条 `ZREM`（抢得到才搬，防并发重复）→ `LPUSH pending`；`ZRANGEBYSCORE lease 0 now` → 取回 `HDEL active` 成功者的任务体重投 pending + `ZREM lease`。
- **Ack / Retry / Archive / Release / Extend**：均为「校验当前归属（active 中存在）→ 原子迁移 + 锁释放/租约更新」的单脚本；归属校验失败幂等返回成功（§5.1-4）。
- **清理**：`ZREMRANGEBYSCORE completed 0 (now-retention)` + 同步 `HDEL msgs`；死信不自动清。

脚本遵循 Redis Cluster 约束：单脚本只触碰同 hash tag（同队列）的 key；跨队列操作按队列逐个脚本执行。

### 7.4 连接与配置

- 配置与 `cachex.RedisConfig` 同构（`Addr/Username/Password/DB/PoolSize/...`），`normConfig` 兜底默认值；
- **建议业务方使用独立逻辑库**（如 cache 用 DB0、taskx 用 DB1，pay 的 `facade.Async` 即此惯例），避免 keyspace 混杂；
- Redis 不可达时驱动工厂直接返回错误 → 门面 `brokerError` 占位（**不做静默降级到 file**——后端语义不同，静默换后端会造成任务「看得见时有时无」，比明确报错更危险；业务方若需降级，自行在配置层决策）；
- 引擎采用统一轮询模型（Claim 空转间隔 = PollInterval/2）；redis 专属的 `BLPOP` 阻塞认领作为后续优化项预留（不影响契约）。

## 8. 可靠性与一致性语义

### 8.1 投递保证：at-least-once

认领即加租约、成功才 Ack；崩溃 → 租约过期 → 回收重投。因此**同一任务可能被执行多次**，业务幂等责任在 handler：

- 优先用 `TaskID(业务唯一键)` 防重（入队期即拦截重复，pay 的 `notify:{tradeNo}:r{n}:a{m}` 模式）；
- 或 handler 内部以业务键做幂等（唯一索引、状态前置校验）；
- 文档示例与 licen-hub 回调通知的 `deliveryNo` 幂等键直接对应。

### 8.2 崩溃恢复矩阵

| 故障点 | file 驱动 | redis 驱动 |
|---|---|---|
| 入队写一半崩溃 | temp 文件残留（不会被认领，Promote 顺手清理）；唯一锁残留 → TTL 过期 | Lua 原子，无中间态；锁残留 → TTL 过期 |
| 认领后执行中崩溃 | 租约过期 → Promote 回收重投 | 同左（lease ZSET 扫描回收） |
| Ack 途中崩溃 | 任务在 active → 租约过期重投（at-least-once 代价，幂等兜住） | 同左 |
| 进程退出（优雅） | 在途任务 Release 立即回 pending | 同左 |
| 机器断电 | 已 rename 的文件落盘（SyncWrites 更稳）；启动恢复扫描接管 | Redis 持久化策略决定（AOF/RDB），与 asynq 相同 |

启动时引擎固定先跑一轮 `Promote`（恢复扫描），再进入正常循环。

### 8.3 多进程/多副本

- file：单进程推荐（§6.4）；
- redis：天然多副本——Lua 原子认领保证一条任务只被一个 worker 拿走；权重队列选取在各副本独立进行，无需协调。

### 8.4 量级边界（诚实声明）

| 维度 | file | redis |
|---|---|---|
| 单队列积压 | 千级以内（目录列举线性开销） | 十万级（LIST/ZSET 成熟结构） |
| 吞吐 | 百级任务/秒（文件 IO 上限，SyncWrites 开启会显著下降） | 千级任务/秒（轮询模型；BLPOP 优化后更高） |
| 定时精度 | ±(PollInterval + 文件扫描耗时) | ±PollInterval |

定位即「应用内可靠队列」，不替代专业 MQ；超出边界请接外部 Broker。

## 9. 配置与全局门面

### 9.1 Config 结构

```go
// Config - taskx 配置（自包含在包内；外部扩展驱动的自定义配置放 Options）
type Config struct {
    Engine          string                                   // 驱动名：file / redis / 外部注册名；未注册回退 file
    Concurrency     int                                      // worker 协程数
    Queues          map[string]int                           // 队列 → 权重（优先级按权重随机选取，同 asynq）
    PollInterval    time.Duration                            // 到期搬运 / 空转轮询周期
    LeaseTTL        time.Duration                            // 租约时长（执行中心跳按 LeaseTTL/2 续租）
    ShutdownTimeout time.Duration                            // 优雅退出等待在途任务的上限
    JanitorInterval time.Duration                            // completed/locks 清理周期
    RetryDelay      func(attempts int, err error) time.Duration // 退避函数（nil = 默认 attempts^4 秒 + 抖动）
    ErrorHandler    func(ctx context.Context, msg *Message, err error) // 失败钩子（告警/指标）
    Logger          Logger                                   // 窄日志接口（logx 一行适配；nil = 静默）
    File            FileConfig                               // file 驱动配置
    Redis           RedisConfig                              // redis 驱动配置
    Options         map[string]any                           // 外部扩展驱动的自定义配置（key 为驱动名）
}

// FileConfig - file 驱动配置
type FileConfig struct {
    Root        string // 队列根目录
    SyncWrites  bool   // 写入后 fsync（更抗断电，性能换持久性）
}

// RedisConfig - redis 驱动配置（与 cachex.RedisConfig 同构）
type RedisConfig struct {
    Addr     string
    Username string
    Password string
    DB       int    // 建议与 cache 分库（pay 惯例 DB+1）
    Prefix   string // key 前缀
    PoolSize int
}

// Logger - 窄日志接口（logx.Logger 直接适配）
type Logger interface {
    Debug(msg string, fields map[string]any)
    Info(msg string, fields map[string]any)
    Warn(msg string, fields map[string]any)
    Error(msg string, fields map[string]any)
}
```

### 9.2 normConfig 默认值

| 字段 | 默认值 | 说明 |
|---|---|---|
| `Engine` | `file` | 未注册名字一律回退（与 cachex 回退 file 同款） |
| `Concurrency` | `10` | |
| `Queues` | `{"default": 1}` | |
| `PollInterval` | `1s` | file/redis 统一 |
| `LeaseTTL` | `30s` | 与 asynq 租约同量级 |
| `ShutdownTimeout` | `30s` | |
| `JanitorInterval` | `5min` | |
| `RetryDelay` | `attempts^4` 秒 + ≤30s 随机抖动 | 与 asynq 默认同风格；licen-hub 定长退避表经注入实现 |
| `File.Root` | `runtime/queue` | 与 cachex 默认 `runtime/cache` 并列 |
| `File.SyncWrites` | `false` | |
| `Redis.Addr` | `localhost:6379` | |
| `Redis.Prefix` | `AIDE:TASKX:` | |

### 9.3 全局门面（与家族同构）

```go
// 包 init()：以默认配置初始化全局位（零配置可用，file 后端开箱即用）
var Inst  *Controller  // 控制器单例：Init(Config) / ReloadIfChanged(Config)（sync.RWMutex 保护）
var Queue *Driver      // 全局活动实例
```

- `ReloadIfChanged` 依据配置 Hash 判断；变更时**先 Shutdown 旧引擎（在途任务按 §3.3 归还）→ 关闭旧 Broker → 原子替换全局实例**（logx 热重载同款时序）；
- 驱动初始化失败时全局位用 `brokerError` 占位，`Enqueue/Run` 等操作返回原始初始化错误（`storeError/senderError` 同款）；
- `taskx.New("redis", config)` 创建独立实例（多队列隔离、测试场景）。

## 10. 周期任务 Scheduler

对应 asynq 的 `Scheduler`（周期入队器），零新依赖实现：

```go
// Scheduler - 周期任务调度器（独立于引擎运行，复用同一 Broker 入队）
type Scheduler struct { ... }

// Entry - 周期任务条目
type Entry struct {
    Type    string                              // 任务类型名
    Payload any                                  // 载荷
    Every   time.Duration                       // 固定间隔（>0 时生效；与 NextFunc 二选一）
    NextFunc func(now time.Time) time.Time       // 自定义下次触发计算（cron 表达式经此接入）
    Options []Option                             // 入队选项（Queue/MaxRetry/Unique...）
    TaskID  string                              // 确定性 ID 模板（配合 Unique 防重复入队）
}

func NewScheduler(driver *Driver) *Scheduler
func (this *Scheduler) Register(entry Entry) error
func (this *Scheduler) Run(ctx context.Context)   // 阻塞；到点即计算下次触发并入队
func (this *Scheduler) Stop()
```

设计要点：

1. **只内置 `@every`（`Every` 字段）与 `NextFunc` 钩子**，不自研 cron 解析器（保持零新依赖；解析器的边界坑——时区、秒级字段、L/W/# 语法——与收益不成比例）；
2. **cron 表达式经适配器外接**：业务方引入 `robfig/cron` 后用十行代码桥接（文档给出完整示例）：

```go
parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
sched, _ := parser.Parse("0 3 * * *")
scheduler.Register(taskx.Entry{
    Type:     "report:daily",
    Payload:  nil,
    NextFunc: func(now time.Time) time.Time { return sched.Next(now) },
    Options:  []taskx.Option{taskx.Queue("low"), taskx.Unique(time.Hour)},
})
```

3. **多副本防重**：同一时刻多个 Scheduler 副本可能同时到点——依靠入队侧的 `Unique(窗口)` 或确定性 `TaskID`（如 `report:daily:20260808`）天然去重，Broker 的唯一锁即协调点，无需选主；
4. 错过补偿：进程停摆期间的触发**不补跑**（与 asynq Scheduler 一致）；需要补跑的业务（如日终对账）在 handler 内自行判断上次成功水位。

## 11. 检视与管理 Inspect

对应 asynq 的 `Inspector`（无 Web UI），为后台页面（licen-hub 投递记录/重推）与运维工具提供 API：

```go
// InspectQuery - 检视查询
type InspectQuery struct {
    Queue string // 队列名（空 = 全部队列，仅计数模式允许）
    State string // pending / active / scheduled / retry / completed / archived（空 = 计数模式）
    Page  int    // 列表模式页码（1 起）
    Size  int    // 列表模式页大小（默认 20，上限 100）
}

// InspectResult - 检视结果
type InspectResult struct {
    Counts map[string]map[string]int // 计数模式：queue → state → 条数
    Tasks  []Message                 // 列表模式：当前页任务
    Total  int                       // 列表模式：总数
}

// ManageOp - 管理操作
type ManageOp struct {
    Action string // run（重跑：scheduled/retry/archived → pending）/ delete（删除单条）/ purge（清空某状态）
    Queue  string
    State  string // run/delete 时为任务当前状态；purge 时为目标状态
    Id     string // run/delete 必填；purge 忽略
}

// 使用
res, err := taskx.Queue.Inspect(ctx, taskx.InspectQuery{Queue: "critical", State: "archived", Page: 1})
err := taskx.Queue.Manage(ctx, taskx.ManageOp{Action: "run", Queue: "critical", State: "archived", Id: "..."})
```

约束：`run` 仅允许从 `scheduled/retry/archived` 发起（pending/active 重跑无意义）；`purge` 仅允许对 `completed/archived`；全部操作经 Broker 原子迁移，执行中的任务不可被 Manage 触碰。licen-hub 后台「投递记录 → 重推」按钮的最终落点即 `Manage{Action: "run"}`。

## 12. 测试策略

遵循 aide 约定：同包 `*_test.go`、标准库 `testing`、无第三方断言、**禁止联网测试**。

1. **Broker 契约测试套件**（核心）：一套 `runBrokerContract(t, factory func(t *testing.T) Broker)` 覆盖 §5 全部契约——入队/冲突（TaskID/Unique）、认领排他（并发 Claim 仅一个成功）、Promote 三态搬运与租约回收、Ack/Retry/Archive/Release 状态流转、Extend 幂等、Inspect/Manage。**file 与 redis 驱动跑同一套套件**——这是「双后端语义一致」的验收线。
2. **file 驱动**：注入 `afero.NewMemMapFs()`，全程不落盘（cachex 同款手法）；额外覆盖 Windows rename 兼容分支（用行为可注入的假 Fs 模拟覆盖报错）。
3. **redis 驱动**：两条路径供实施时决策——
   - **首选**：引入 `github.com/alicebob/miniredis/v2` 作为**仅测试范围**依赖跑契约套件（go.mod 会出现该依赖，需团队认可）；
   - **零依赖备选**：用接口级假 Broker 只测引擎，redis Lua 脚本以静态审查 + 注释形式保证正确性，契约套件对 redis 标记跳过（风险：Lua 错误只能到集成环境暴露）。
   推荐前者（test-only 依赖不进入业务二进制），实施时拍板。
4. **引擎单测**（假 Broker）：worker 池并发上限、权重选取分布、退避计算（默认函数与注入函数）、panic recover、ErrorHandler 触发、优雅退出时序（停止认领→等待→Release）、热重载替换。
5. **门面测试**：注册表（登记/覆盖/New 独立实例）、normConfig 默认值、Init/ReloadIfChanged、brokerError 占位（与 cachex/pushx 测试同构）。
6. **Scheduler 测试**：`Every` 触发节奏、`NextFunc` 适配、多副本防重（唯一锁拦截）。

## 13. 与既有仓库的接入关系（仅说明，本轮不改代码）

### 13.1 licen-hub（回调通知推送器演进）

《回调通知与事件推送设计方案》中「gocron 每 15s 扫表 + 乐观锁认领」的派发器，可在 taskx 落地后演进为：

- `callback_deliveries` 表仍是**事实源**（业务留存与后台页面不变）；
- 落库后向 taskx 入队一条 `callback:deliver` 任务（`TaskID = deliveryNo` 天然防重）；handler 执行发送并按 §5 应答契约回写投递行；失败由 taskx 重试（`RetryDelay` 注入定长退避表）；
- 私有化单二进制用 file 驱动，平台侧集群部署切 redis 驱动，**业务代码零改动**（只换 `Config.Engine`）；
- 后台「重推」按钮 = `Manage{Action: "run"}` 或直接再次入队同 `TaskID` 任务。

### 13.2 pay（facade.Async 平移）

pay 的 `backend/app/facade/async.go`（hibiken/asynq 封装）可整体平移到 taskx redis 驱动：

- `Async.Handle(type, handler)` → `Queue.Handle`；`Async.Enqueue(type, payload, opts...)` → 链式/函数式入队；
- 自入队重试模式可直接保留（`ProcessIn` + 确定性 `TaskID` 即 pay 原模式），也可简化为 taskx 内建重试（MaxRetry + RetryDelay 注入 `5轮×8次` 表）——平移时逐任务评估，不在本方案范围；
- Redis DB+1 隔离惯例由 `RedisConfig.DB` 直接表达；热重载由 `Inst.ReloadIfChanged` 接管（替代 pay 手写 Reload）。

## 14. 里程碑与验收清单

### 14.1 里程碑

| 里程碑 | 内容 | 产出 |
|---|---|---|
| M1（MVP） | 核心类型 + 引擎（worker 池/搬运/心跳/优雅退出/退避）+ file 驱动 + 契约测试套件（file 侧）+ 门面 | `taskx` 包可用，licen-hub 单二进制场景可接入 |
| M2 | redis 驱动（key 结构 + Lua）+ 契约套件 redis 侧 + Inspect/Manage + Scheduler | 双后端齐备，pay 平移路径打通 |
| M3（增强） | SyncWrites 基准与调优、redis BLPOP 阻塞认领优化、licen-hub/pay 实际接入、Group 聚合评估 | 生产打磨 |

### 14.2 验收清单

1. `go build ./... && go vet ./... && go test ./...` 全绿（含 licence 模块不受影响的回归）；
2. Broker 契约套件对 file（MemMapFs）全过；redis 侧按 §12.3 决策执行；
3. 功能对照表（§2）逐项可演示：延迟任务按点触发、重试按退避节奏、超时取消生效、TaskID/Unique 拦截、崩溃回收（执行中 kill 进程，重启后任务重投）、优雅退出不丢在途任务；
4. 热重载：改配置 `ReloadIfChanged` 后旧任务不丢、新配置生效；
5. 零新直接依赖（M2 后 `go.mod` diff 仅可能出现 miniredis 的 test-only 条目）；
6. aide/AGENTS.md 增补 taskx 包约定一节（实施时同步），README 家族清单更新。

## 15. 已考量并否决的方案

| 方案 | 否决理由 |
|---|---|
| 包装 hibiken/asynq 作为唯一 redis 路径 | 双引擎语义漂移风险长期存在；新增依赖树；其配置面与 aide 门面不同构；§7.1 有完整对照。保留为外部注册扩展（registry 开放），不内置。 |
| 直接复用 `cachex` 包实现 file 驱动 | `cachex.Store` 五方法缺少原子认领原语（无 rename/move，Get+Delete 两步无法原子化）；键哈希化命名、标签簿记与 TTL 过期模型服务于缓存语义，表达不了队列状态机。改为镜像 `cachex/file.go` 工程范式（§6.1）。 |
| 引入 robfig/cron 作为直接依赖 | 违反零新依赖目标；`NextFunc` 钩子 + 十行适配器即可外接（§10）。 |
| 自研完整 cron 表达式解析器 | 时区与扩展语法（L/W/#、秒级字段）边界复杂，测试成本高，收益抵不上风险；适配器路线已覆盖需求。 |
| file 驱动内嵌 SQLite（单文件数据库） | SQLite 确实是本地队列的优质载体（事务 + 索引），但 aide 当前无 SQLite 驱动依赖（cgo/纯 Go 实现二选一都是重决策）；列为未来可选的**外部注册 Broker**（业务方可自行实现 `sqlite` 驱动），不内置。 |
| redis 认领用 BLPOP 阻塞模型作为初版 | 与 file 的轮询模型分叉，破坏单引擎假设；初版统一轮询（语义完全一致、实现最简），BLPOP 列为 M3 纯性能优化，契约不变。 |
