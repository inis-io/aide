package taskx

import (
	"context"
	"fmt"
	"time"
)

// Driver - Broker 之上的值语义链式队列实例
type Driver struct {
	broker Broker
	config Config
	mux    *Mux
	engine *Engine
	msg    *Message
}

// NewDriver - 用 Broker 创建链式队列实例
func NewDriver(broker Broker, config Config) *Driver {
	config = normConfig(config)
	mux := NewMux()
	driver := &Driver{broker: broker, config: config, mux: mux}
	driver.engine = newEngine(broker, mux, config)
	return driver
}

// Broker - 取出底层队列后端
func (this Driver) Broker() Broker { return this.broker }

// New - 创建一条链式任务消息
func (this Driver) New(taskType string, payload any) Driver {
	this.msg = NewMessage(taskType, payload)
	return this
}

func (this Driver) mutate(option Option) Driver {
	if this.msg == nil {
		this.msg = NewMessage("", nil)
	} else {
		this.msg = cloneMessage(this.msg)
	}
	option(this.msg)
	return this
}

// Queue - 设置任务队列
func (this Driver) Queue(name string) Driver { return this.mutate(queueOption(name)) }

// In - 设置延迟执行时间
func (this Driver) In(duration time.Duration) Driver { return this.mutate(ProcessIn(duration)) }

// At - 设置定时执行时间
func (this Driver) At(at time.Time) Driver { return this.mutate(ProcessAt(at)) }

// MaxRetry - 设置最大重试次数
func (this Driver) MaxRetry(count int) Driver { return this.mutate(MaxRetry(count)) }

// Timeout - 设置单次执行超时
func (this Driver) Timeout(duration time.Duration) Driver { return this.mutate(Timeout(duration)) }

// Deadline - 设置执行截止时间点
func (this Driver) Deadline(at time.Time) Driver { return this.mutate(Deadline(at)) }

// Retention - 设置完成保留时间
func (this Driver) Retention(duration time.Duration) Driver { return this.mutate(Retention(duration)) }

// Unique - 设置内容去重窗口
func (this Driver) Unique(ttl time.Duration) Driver { return this.mutate(Unique(ttl)) }

// TaskID - 设置确定性任务 ID
func (this Driver) TaskID(id string) Driver { return this.mutate(TaskID(id)) }

// Enqueue - 将链式消息入队
func (this Driver) Enqueue(ctx context.Context) (string, error) {
	return this.EnqueueMessage(ctx, this.msg)
}

// EnqueueMessage - 将消息应用选项后入队
func (this Driver) EnqueueMessage(ctx context.Context, msg *Message, options ...Option) (string, error) {
	if this.broker == nil {
		return "", fmt.Errorf("%w: Broker 为空", ErrDriverNotReady)
	}
	if msg == nil {
		return "", errorsNew("任务消息不能为空")
	}
	msg = cloneMessage(msg)
	for _, option := range options {
		if option != nil {
			option(msg)
		}
	}
	if err := prepareMessage(msg); err != nil {
		return "", err
	}
	if err := this.broker.Enqueue(ctx, msg); err != nil {
		return "", err
	}
	return msg.Id, nil
}

func errorsNew(message string) error { return fmt.Errorf("taskx: %s", message) }

// Handle - 注册任务处理器
func (this Driver) Handle(taskType string, handler Handler) { this.mux.Handle(taskType, handler) }

// Use - 注册任务中间件
func (this Driver) Use(middlewares ...Middleware) { this.mux.Use(middlewares...) }

// Run - 阻塞运行消费引擎
func (this Driver) Run(ctx context.Context) error { return this.engine.Run(ctx) }

// Shutdown - 优雅停止消费引擎
func (this Driver) Shutdown() error { return this.engine.Shutdown() }

// Inspect - 检视任务
func (this Driver) Inspect(ctx context.Context, query InspectQuery) (*InspectResult, error) {
	return this.broker.Inspect(ctx, query)
}

// Manage - 管理任务
func (this Driver) Manage(ctx context.Context, op ManageOp) error {
	return this.broker.Manage(ctx, op)
}

// Close - 停止引擎并关闭后端
func (this Driver) Close() error {
	_ = this.engine.Shutdown()
	return this.broker.Close()
}
