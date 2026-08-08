// Package taskx - 异步任务队列包：以接口模式封装 file、Redis 等可靠队列后端
package taskx

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Broker - 队列存储后端接口（唯一扩展点）
type Broker interface {
	// Enqueue - 原子写入新任务
	Enqueue(ctx context.Context, msg *Message) error
	// Claim - 原子认领一个待执行任务
	Claim(ctx context.Context, queues []string) (*Message, error)
	// Promote - 搬运到期任务并回收过期租约
	Promote(ctx context.Context) (int, error)
	// Ack - 确认任务执行成功
	Ack(ctx context.Context, msg *Message) error
	// Retry - 将任务转入重试状态
	Retry(ctx context.Context, msg *Message, cause error) error
	// Archive - 将任务转入死信状态
	Archive(ctx context.Context, msg *Message, cause error) error
	// Release - 将执行中任务归还待执行队列
	Release(ctx context.Context, msg *Message) error
	// Extend - 延长任务租约
	Extend(ctx context.Context, msg *Message, leaseUntil time.Time) error
	// Inspect - 检视任务状态
	Inspect(ctx context.Context, query InspectQuery) (*InspectResult, error)
	// Manage - 管理任务
	Manage(ctx context.Context, op ManageOp) error
	// Close - 释放后端资源
	Close() error
}

// Factory - 队列驱动工厂
type Factory func(config Config) (Broker, error)

var (
	// ErrTaskIdConflict - 确定性任务 ID 已存在
	ErrTaskIdConflict = errors.New("taskx: 任务 ID 冲突")
	// ErrDuplicateTask - 内容去重窗口内任务重复
	ErrDuplicateTask = errors.New("taskx: 任务重复")
	// ErrNotRunning - 引擎未运行
	ErrNotRunning = errors.New("taskx: 引擎未运行")
	// ErrAlreadyRunning - 引擎已经运行
	ErrAlreadyRunning = errors.New("taskx: 引擎已经运行")
	// ErrDriverNotReady - 驱动未就绪
	ErrDriverNotReady = errors.New("taskx: 驱动未就绪")
)

var registry = struct {
	sync.RWMutex
	items map[string]Factory
}{items: map[string]Factory{
	"file":  newFileBroker,
	"redis": newRedisBroker,
}}

// Register - 注册队列驱动
func Register(name string, factory Factory) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		panic("taskx: 驱动名称不能为空")
	}
	if factory == nil {
		panic("taskx: 驱动工厂不能为空")
	}
	registry.Lock()
	registry.items[name] = factory
	registry.Unlock()
}

func registered(name string) bool {
	registry.RLock()
	defer registry.RUnlock()
	_, ok := registry.items[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// Names - 返回已注册驱动名称列表
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()
	names := make([]string, 0, len(registry.items))
	for name := range registry.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func open(name string, config Config) (Broker, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	registry.RLock()
	factory, ok := registry.items[name]
	registry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("taskx: 未注册的驱动[%s]（可用: %s）", name, strings.Join(Names(), ", "))
	}
	return factory(config)
}

// New - 创建独立队列实例
func New(name string, config Config) (*Driver, error) {
	config.Engine = strings.ToLower(strings.TrimSpace(name))
	if !registered(config.Engine) {
		return nil, fmt.Errorf("taskx: 未注册的驱动[%s]（可用: %s）", name, strings.Join(Names(), ", "))
	}
	conf := normConfig(config)
	broker, err := open(config.Engine, conf)
	if err != nil {
		return nil, err
	}
	return NewDriver(broker, conf), nil
}

// Enqueue - 使用全局队列实例入队
func Enqueue(ctx context.Context, msg *Message, options ...Option) (string, error) {
	if currentQueue() == nil {
		return "", fmt.Errorf("%w: 全局队列未初始化", ErrDriverNotReady)
	}
	return Queue.EnqueueMessage(ctx, msg, options...)
}
