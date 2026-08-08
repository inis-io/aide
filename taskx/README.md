# taskx 异步任务队列

`taskx` 以统一的 `Broker` 接口封装可靠异步任务队列，内置 `file` 与 `redis` 两个后端。消费语义由同一个 Engine 编排，两种后端均支持并发消费、延迟任务、自动重试、执行超时、去重、租约恢复、死信、周期任务以及检视管理。

## 快速开始

```go
package main

import (
	"context"
	"time"

	"github.com/inis-io/aide/taskx"
)

func main() {
	taskx.Inst.Init(taskx.Config{
		Engine:      "file",
		Concurrency: 10,
		Queues: map[string]int{
			"critical": 5,
			"default":  1,
		},
	})

	taskx.Queue.Handle("trade:notify", taskx.HandlerFunc(func(ctx context.Context, msg *taskx.Message) error {
		// 返回 error 时由 taskx 自动重试；处理器必须保证业务幂等。
		return nil
	}))

	_, _ = taskx.Queue.New("trade:notify", map[string]any{"tradeNo": "T-1"}).
		Queue("critical").
		MaxRetry(8).
		Timeout(30 * time.Second).
		TaskID("notify:T-1").
		Enqueue(context.Background())

	_ = taskx.Queue.Run(context.Background())
}
```

函数式入队与链式入队可以混用。`Queue` 是可调用门面，因此既支持 `taskx.Queue("critical")` 选项，也支持 `taskx.Queue.New(...)` 链式入口：

```go
msg := taskx.NewMessage("report:daily", nil)
id, err := taskx.Enqueue(context.Background(), msg,
	taskx.Queue("low"),
	taskx.ProcessIn(time.Minute),
	taskx.MaxRetry(3),
	taskx.Unique(time.Hour),
)
_, _ = id, err
```

## 后端配置

file 后端零配置可用，默认目录为 `./runtime/queue`：

```go
taskx.Inst.Init(taskx.Config{
	Engine: "file",
	File: taskx.FileConfig{
		Root:       "./runtime/queue",
		SyncWrites: true,
	},
})
```

file 适合单二进制、桌面工具和边缘节点，推荐单进程使用；单队列积压超过数千条或需要多副本时，应改用 redis：

```go
taskx.Inst.Init(taskx.Config{
	Engine: "redis",
	Redis: taskx.RedisConfig{
		Addr:     "127.0.0.1:6379",
		DB:       1,
		Prefix:   "AIDE:TASKX:",
		PoolSize: 20,
	},
})
```

Redis 建议与缓存使用不同逻辑库。连接不可用会直接返回初始化错误，不会静默切换到 file，以免任务落入不同后端形成不可见分叉。

## 可靠性语义

taskx 提供 at-least-once 投递：任务认领后持有租约，成功后才 Ack；进程崩溃或 worker 失联时，租约到期任务会自动回到 pending。因此同一任务可能执行多次，Handler 必须以业务唯一键保证幂等。

- `TaskID(id)`：确定性业务 ID，重复入队返回 `ErrTaskIdConflict`。
- `Unique(ttl)`：按任务类型与载荷计算内容摘要，窗口内重复返回 `ErrDuplicateTask`。
- `MaxRetry(n)`：最大重试次数，耗尽后进入 archived 死信。
- `Timeout` 与 `Deadline` 同时存在时取更早者。
- `Retention(0)` 表示成功即删；大于零时进入 completed 并在到期后清理。

## 周期任务

Scheduler 内置固定间隔与自定义下次触发函数，不解析 cron 表达式：

实际使用时把独立实例或全局活动实例对应的 `*taskx.Driver` 传给 `NewScheduler`：

```go
scheduler := taskx.NewScheduler(taskx.Queue.Driver())
_ = scheduler.Register(taskx.Entry{
	Type:    "report:daily",
	Payload: nil,
	Every:   24 * time.Hour,
	Options: []taskx.Option{taskx.Queue("low"), taskx.Unique(time.Hour)},
})
go scheduler.Run(context.Background())
defer scheduler.Stop()
```

独立队列可使用 `driver, _ := taskx.New("file", taskx.Config{})` 创建，再把 `driver` 传给 `NewScheduler`。

完整 cron 可由业务方使用现有解析器生成 `NextFunc`。多副本 Scheduler 应配置 `Unique` 或确定性 `TaskID` 来协调重复触发。

## 检视与管理

```go
result, err := taskx.Queue.Inspect(context.Background(), taskx.InspectQuery{
	Queue: "critical",
	State: "archived",
	Page:  1,
	Size:  20,
})

err = taskx.Queue.Manage(context.Background(), taskx.ManageOp{
	Action: "run",
	Queue:  "critical",
	State:  "archived",
	Id:     result.Tasks[0].Id,
})
```

`run` 只允许 scheduled、retry、archived；`purge` 只允许 completed、archived；执行中的 active 任务不允许通过 Manage 删除。

## 扩展 Broker

`Broker` 是唯一扩展点。外部包实现接口后，在自己的 `init()` 中注册即可；同名注册会覆盖已有驱动：

```go
func init() {
	taskx.Register("postgres", newPostgresBroker)
}
```

扩展后端必须保证 Enqueue、Claim、Promote 与状态迁移的单任务原子性，并容忍对已迁移或已删除任务的幂等操作。
