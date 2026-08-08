package taskx

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Inst - 异步队列控制器单例
var Inst = &Controller{}

var activeQueue *Driver

// queueFacade - 同时提供函数式队列选项与全局活动实例门面的可调用类型
type queueFacade func(name string) Option

// Queue - 全局异步队列门面；既可调用 Queue(name) 构造选项，也可调用 Queue.New 等方法
var Queue queueFacade = queueOption

func currentQueue() *Driver {
	Inst.Mutex.RLock()
	defer Inst.Mutex.RUnlock()
	return activeQueue
}

// New - 创建一条链式任务消息
func (this queueFacade) New(taskType string, payload any) Driver {
	return currentQueue().New(taskType, payload)
}

// Broker - 取出当前底层队列后端
func (this queueFacade) Broker() Broker { return currentQueue().Broker() }

// Driver - 取出当前全局活动队列实例（供 Scheduler 等需要实例指针的组件使用）
func (this queueFacade) Driver() *Driver { return currentQueue() }

// EnqueueMessage - 使用当前活动实例将消息入队
func (this queueFacade) EnqueueMessage(ctx context.Context, msg *Message, options ...Option) (string, error) {
	return currentQueue().EnqueueMessage(ctx, msg, options...)
}

// Handle - 在当前活动实例注册任务处理器
func (this queueFacade) Handle(taskType string, handler Handler) {
	currentQueue().Handle(taskType, handler)
}

// Use - 在当前活动实例注册中间件
func (this queueFacade) Use(middlewares ...Middleware) { currentQueue().Use(middlewares...) }

// Run - 运行当前活动实例
func (this queueFacade) Run(ctx context.Context) error { return currentQueue().Run(ctx) }

// Shutdown - 停止当前活动实例
func (this queueFacade) Shutdown() error { return currentQueue().Shutdown() }

// Inspect - 检视当前活动实例的任务
func (this queueFacade) Inspect(ctx context.Context, query InspectQuery) (*InspectResult, error) {
	return currentQueue().Inspect(ctx, query)
}

// Manage - 管理当前活动实例的任务
func (this queueFacade) Manage(ctx context.Context, op ManageOp) error {
	return currentQueue().Manage(ctx, op)
}

// Controller - 管理配置注入与全局活动实例热重载
type Controller struct {
	// Hash - 当前配置指纹
	Hash string `json:"hash"`
	// Config - 当前队列配置
	Config Config `json:"config"`
	// HasConfig - 是否已注入配置
	HasConfig bool `json:"hasConfig"`
	// Mutex - 配置与实例读写锁
	Mutex sync.RWMutex
}

func init() { Inst.Init() }

func (this *Controller) useDefault() {
	conf := normConfig(Config{})
	this.Mutex.Lock()
	this.Hash = conf.Hash
	this.HasConfig = false
	this.Mutex.Unlock()
	this.setActive(conf)
}

func (this *Controller) setActive(config Config) {
	conf := normConfig(config)
	broker, err := open(conf.Engine, conf)
	if err != nil {
		broker = &brokerError{name: conf.Engine, err: err}
	}
	next := NewDriver(broker, conf)

	this.Mutex.RLock()
	previous := activeQueue
	this.Mutex.RUnlock()
	if previous != nil {
		// Handler 与 Middleware 属于业务注册信息，热重载后继续复用。
		next.mux = previous.mux
		next.engine = newEngine(broker, next.mux, conf)
		_ = previous.Close()
	}

	this.Mutex.Lock()
	this.Config = conf
	activeQueue = next
	this.Mutex.Unlock()
}

func (this *Controller) setConfig(config Config) {
	this.Mutex.Lock()
	this.Config = normConfig(config)
	this.HasConfig = true
	this.Mutex.Unlock()
}

// ReloadIfChanged - 配置变化时热重载队列
func (this *Controller) ReloadIfChanged(config ...Config) {
	if len(config) > 0 {
		this.setConfig(config[0])
	}
	this.Mutex.RLock()
	hasConfig := this.HasConfig
	changed := this.Hash != this.Config.Hash
	this.Mutex.RUnlock()
	if hasConfig && changed {
		this.Init()
	}
}

// Init - 初始化异步队列
func (this *Controller) Init(config ...Config) {
	if len(config) > 0 {
		this.setConfig(config[0])
	}
	this.Mutex.RLock()
	hasConfig := this.HasConfig
	this.Mutex.RUnlock()
	if !hasConfig {
		this.useDefault()
		return
	}
	this.Mutex.Lock()
	this.Config = normConfig(this.Config)
	this.Hash = this.Config.Hash
	conf := this.Config
	this.Mutex.Unlock()
	this.setActive(conf)
}

type brokerError struct {
	name string
	err  error
}

func (this *brokerError) wrap() error {
	return fmt.Errorf("%w: 驱动[%s]: %w", ErrDriverNotReady, this.name, this.err)
}

func (this *brokerError) Enqueue(context.Context, *Message) error           { return this.wrap() }
func (this *brokerError) Claim(context.Context, []string) (*Message, error) { return nil, this.wrap() }
func (this *brokerError) Promote(context.Context) (int, error)              { return 0, this.wrap() }
func (this *brokerError) Ack(context.Context, *Message) error               { return this.wrap() }
func (this *brokerError) Retry(context.Context, *Message, error) error      { return this.wrap() }
func (this *brokerError) Archive(context.Context, *Message, error) error    { return this.wrap() }
func (this *brokerError) Release(context.Context, *Message) error           { return this.wrap() }
func (this *brokerError) Extend(context.Context, *Message, time.Time) error { return this.wrap() }
func (this *brokerError) Inspect(context.Context, InspectQuery) (*InspectResult, error) {
	return nil, this.wrap()
}
func (this *brokerError) Manage(context.Context, ManageOp) error { return this.wrap() }
func (this *brokerError) Close() error                           { return nil }

var _ Broker = (*brokerError)(nil)
