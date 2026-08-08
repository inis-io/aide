package taskx

import (
	"context"
	"math/rand"
	"sync"
	"time"
)

type activeTask struct {
	msg      *Message
	cancel   context.CancelFunc
	released bool
}

// Engine - 统一编排消费、重试、租约与优雅退出的任务引擎
type Engine struct {
	broker Broker
	mux    *Mux
	config Config

	mutex      sync.Mutex
	running    bool
	shutting   bool
	stopClaim  context.CancelFunc
	done       chan struct{}
	workers    sync.WaitGroup
	background sync.WaitGroup
	active     map[string]*activeTask
}

func newEngine(broker Broker, mux *Mux, config Config) *Engine {
	return &Engine{broker: broker, mux: mux, config: config, active: make(map[string]*activeTask)}
}

// Run - 阻塞运行任务引擎
func (this *Engine) Run(ctx context.Context) error {
	this.mutex.Lock()
	if this.running {
		this.mutex.Unlock()
		return ErrAlreadyRunning
	}
	claimCtx, cancel := context.WithCancel(context.Background())
	this.running = true
	this.shutting = false
	this.stopClaim = cancel
	this.done = make(chan struct{})
	this.active = make(map[string]*activeTask)
	done := this.done
	this.mutex.Unlock()

	if _, err := this.broker.Promote(claimCtx); err != nil {
		this.logError("启动恢复扫描失败", err, nil)
	}

	this.background.Add(1)
	go this.promoteLoop(claimCtx)
	for index := 0; index < this.config.Concurrency; index++ {
		this.workers.Add(1)
		go this.worker(claimCtx)
	}

	select {
	case <-ctx.Done():
		_ = this.Shutdown()
	case <-done:
	}
	return nil
}

func (this *Engine) worker(ctx context.Context) {
	defer this.workers.Done()
	idle := this.config.PollInterval / 2
	if idle <= 0 {
		idle = 100 * time.Millisecond
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msg, err := this.broker.Claim(ctx, weightedQueueOrder(this.config.Queues))
		if err != nil {
			if ctx.Err() == nil {
				this.logError("认领任务失败", err, nil)
			}
			if !waitContext(ctx, idle) {
				return
			}
			continue
		}
		if msg == nil {
			if !waitContext(ctx, idle) {
				return
			}
			continue
		}
		this.process(msg)
	}
}

func (this *Engine) process(msg *Message) {
	processCtx, cancel := taskContext(msg)
	entry := &activeTask{msg: msg, cancel: cancel}
	this.mutex.Lock()
	this.active[msg.Id] = entry
	this.mutex.Unlock()

	heartbeatDone := make(chan struct{})
	go this.heartbeat(processCtx, msg, heartbeatDone)
	err := this.callHandler(processCtx, msg)
	cancel()
	<-heartbeatDone

	this.mutex.Lock()
	current := this.active[msg.Id]
	delete(this.active, msg.Id)
	released := current == nil || current.released
	this.mutex.Unlock()
	if released {
		return
	}
	if err == nil {
		if ackErr := this.broker.Ack(context.Background(), msg); ackErr != nil {
			this.logError("确认任务失败", ackErr, msg)
		}
		return
	}
	if this.config.ErrorHandler != nil {
		this.config.ErrorHandler(context.Background(), cloneMessage(msg), err)
	}
	if msg.Attempts < msg.MaxRetry {
		msg.RetryAt = time.Now().Add(this.config.RetryDelay(msg.Attempts+1, err))
		if retryErr := this.broker.Retry(context.Background(), msg, err); retryErr != nil {
			this.logError("任务转入重试失败", retryErr, msg)
		}
		return
	}
	if archiveErr := this.broker.Archive(context.Background(), msg, err); archiveErr != nil {
		this.logError("任务归档失败", archiveErr, msg)
	}
}

func (this *Engine) callHandler(ctx context.Context, msg *Message) error {
	result := make(chan error, 1)
	// Handler 使用消息副本，避免超时后不响应取消的处理器与引擎状态迁移并发修改同一对象。
	handlerMsg := cloneMessage(msg)
	go func() { result <- this.mux.process(ctx, handlerMsg) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (this *Engine) heartbeat(ctx context.Context, msg *Message, done chan<- struct{}) {
	defer close(done)
	interval := this.config.LeaseTTL / 2
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := this.broker.Extend(context.Background(), msg, time.Now().Add(this.config.LeaseTTL)); err != nil {
				this.logError("任务续租失败", err, msg)
			}
		}
	}
}

func (this *Engine) promoteLoop(ctx context.Context) {
	defer this.background.Done()
	ticker := time.NewTicker(this.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := this.broker.Promote(ctx); err != nil {
				this.logError("搬运到期任务失败", err, nil)
			}
		}
	}
}

// Shutdown - 优雅停止任务引擎
func (this *Engine) Shutdown() error {
	this.mutex.Lock()
	if !this.running {
		this.mutex.Unlock()
		return ErrNotRunning
	}
	if this.shutting {
		done := this.done
		this.mutex.Unlock()
		<-done
		return nil
	}
	this.shutting = true
	this.stopClaim()
	done := this.done
	this.mutex.Unlock()

	finished := make(chan struct{})
	go func() {
		this.background.Wait()
		this.workers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(this.config.ShutdownTimeout):
		this.releaseActive()
		<-finished
	}

	this.mutex.Lock()
	this.running = false
	this.shutting = false
	close(done)
	this.mutex.Unlock()
	return nil
}

func (this *Engine) releaseActive() {
	this.mutex.Lock()
	items := make([]*activeTask, 0, len(this.active))
	for _, item := range this.active {
		item.released = true
		item.cancel()
		items = append(items, item)
	}
	this.mutex.Unlock()
	for _, item := range items {
		if err := this.broker.Release(context.Background(), item.msg); err != nil {
			this.logError("归还在途任务失败", err, item.msg)
		}
	}
}

func (this *Engine) logError(message string, err error, msg *Message) {
	if this.config.Logger == nil {
		return
	}
	fields := map[string]any{"error": err.Error()}
	if msg != nil {
		fields["taskId"] = msg.Id
		fields["taskType"] = msg.Type
		fields["queue"] = msg.Queue
	}
	this.config.Logger.Error(message, fields)
}

func taskContext(msg *Message) (context.Context, context.CancelFunc) {
	deadline := msg.Deadline
	if msg.Timeout > 0 {
		timeoutAt := time.Now().Add(msg.Timeout)
		if deadline.IsZero() || timeoutAt.Before(deadline) {
			deadline = timeoutAt
		}
	}
	if !deadline.IsZero() {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithCancel(context.Background())
}

func weightedQueueOrder(queues map[string]int) []string {
	weights := make(map[string]int, len(queues))
	for name, weight := range queues {
		if weight > 0 {
			weights[name] = weight
		}
	}
	result := make([]string, 0, len(weights))
	for len(weights) > 0 {
		total := 0
		for _, weight := range weights {
			total += weight
		}
		pick := rand.Intn(total)
		for name, weight := range weights {
			if pick < weight {
				result = append(result, name)
				delete(weights, name)
				break
			}
			pick -= weight
		}
	}
	return result
}

func defaultRetryDelay(attempts int, _ error) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	seconds := attempts * attempts * attempts * attempts
	return time.Duration(seconds)*time.Second + time.Duration(rand.Intn(31))*time.Second
}

func waitContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
