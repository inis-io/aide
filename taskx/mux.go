package taskx

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
)

// Handler - 任务处理器
type Handler interface {
	// ProcessTask - 处理任务
	ProcessTask(ctx context.Context, msg *Message) error
}

// HandlerFunc - 任务处理函数适配器
type HandlerFunc func(ctx context.Context, msg *Message) error

// ProcessTask - 调用任务处理函数
func (this HandlerFunc) ProcessTask(ctx context.Context, msg *Message) error {
	return this(ctx, msg)
}

// Middleware - 任务中间件
type Middleware func(next Handler) Handler

// Mux - 任务类型路由器
type Mux struct {
	mutex       sync.RWMutex
	handlers    map[string]Handler
	middlewares []Middleware
}

// NewMux - 创建任务路由器
func NewMux() *Mux {
	return &Mux{handlers: make(map[string]Handler)}
}

// Handle - 注册任务处理器
func (this *Mux) Handle(taskType string, handler Handler) {
	taskType = strings.TrimSpace(taskType)
	if taskType == "" {
		panic("taskx: 任务类型不能为空")
	}
	if handler == nil {
		panic("taskx: 任务处理器不能为空")
	}
	this.mutex.Lock()
	this.handlers[taskType] = handler
	this.mutex.Unlock()
}

// Use - 注册任务中间件
func (this *Mux) Use(middlewares ...Middleware) {
	this.mutex.Lock()
	for _, middleware := range middlewares {
		if middleware != nil {
			this.middlewares = append(this.middlewares, middleware)
		}
	}
	this.mutex.Unlock()
}

func (this *Mux) process(ctx context.Context, msg *Message) (err error) {
	this.mutex.RLock()
	handler := this.handlers[msg.Type]
	middlewares := append([]Middleware(nil), this.middlewares...)
	this.mutex.RUnlock()
	if handler == nil {
		return fmt.Errorf("taskx: 未注册任务类型[%s]的处理器", msg.Type)
	}
	for index := len(middlewares) - 1; index >= 0; index-- {
		handler = middlewares[index](handler)
	}
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("taskx: 任务处理器 panic: %v\n%s", cause, debug.Stack())
		}
	}()
	return handler.ProcessTask(ctx, msg)
}
