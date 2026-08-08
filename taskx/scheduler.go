package taskx

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Entry - 周期任务条目
type Entry struct {
	// Type - 任务类型名
	Type string
	// Payload - 任务载荷
	Payload any
	// Every - 固定触发间隔
	Every time.Duration
	// NextFunc - 自定义下次触发计算函数
	NextFunc func(now time.Time) time.Time
	// Options - 入队选项
	Options []Option
	// TaskID - 确定性任务 ID
	TaskID string
}

type schedulerEntry struct {
	entry    Entry
	next     time.Time
	disabled bool
}

// Scheduler - 周期任务调度器
type Scheduler struct {
	driver  *Driver
	mutex   sync.Mutex
	items   []schedulerEntry
	running bool
	cancel  context.CancelFunc
}

// NewScheduler - 创建周期任务调度器
func NewScheduler(driver *Driver) *Scheduler {
	return &Scheduler{driver: driver}
}

// Register - 注册周期任务
func (this *Scheduler) Register(entry Entry) error {
	if this.driver == nil {
		return errors.New("taskx: Scheduler 队列实例不能为空")
	}
	if entry.Type == "" {
		return errors.New("taskx: Scheduler 任务类型不能为空")
	}
	if (entry.Every > 0) == (entry.NextFunc != nil) {
		return errors.New("taskx: Scheduler 的 Every 与 NextFunc 必须且只能设置一个")
	}
	next := nextEntryTime(entry, time.Now())
	if next.IsZero() {
		return errors.New("taskx: Scheduler 下次触发时间无效")
	}
	this.mutex.Lock()
	this.items = append(this.items, schedulerEntry{entry: entry, next: next})
	this.mutex.Unlock()
	return nil
}

// Run - 阻塞运行周期任务调度器
func (this *Scheduler) Run(ctx context.Context) error {
	this.mutex.Lock()
	if this.running {
		this.mutex.Unlock()
		return errors.New("taskx: Scheduler 已经运行")
	}
	runCtx, cancel := context.WithCancel(ctx)
	this.running = true
	this.cancel = cancel
	this.mutex.Unlock()
	defer func() {
		this.mutex.Lock()
		this.running = false
		this.cancel = nil
		this.mutex.Unlock()
	}()

	for {
		delay := this.untilNext()
		timer := time.NewTimer(delay)
		select {
		case <-runCtx.Done():
			timer.Stop()
			return nil
		case now := <-timer.C:
			this.fire(runCtx, now)
		}
	}
}

// Stop - 停止周期任务调度器
func (this *Scheduler) Stop() {
	this.mutex.Lock()
	if this.cancel != nil {
		this.cancel()
	}
	this.mutex.Unlock()
}

func (this *Scheduler) untilNext() time.Duration {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	var next time.Time
	for _, item := range this.items {
		if item.disabled {
			continue
		}
		if next.IsZero() || item.next.Before(next) {
			next = item.next
		}
	}
	if next.IsZero() {
		return time.Second
	}
	delay := time.Until(next)
	if delay < 0 {
		return 0
	}
	return delay
}

func (this *Scheduler) fire(ctx context.Context, now time.Time) {
	this.mutex.Lock()
	due := make([]Entry, 0)
	for index := range this.items {
		if this.items[index].disabled || this.items[index].next.After(now) {
			continue
		}
		due = append(due, this.items[index].entry)
		this.items[index].next = nextEntryTime(this.items[index].entry, now)
		this.items[index].disabled = this.items[index].next.IsZero()
	}
	this.mutex.Unlock()
	for _, entry := range due {
		options := append([]Option(nil), entry.Options...)
		if entry.TaskID != "" {
			options = append(options, TaskID(entry.TaskID))
		}
		_, err := this.driver.EnqueueMessage(ctx, NewMessage(entry.Type, entry.Payload), options...)
		if err != nil && !errors.Is(err, ErrTaskIdConflict) && !errors.Is(err, ErrDuplicateTask) {
			if this.driver.config.Logger != nil {
				this.driver.config.Logger.Error("周期任务入队失败", map[string]any{"taskType": entry.Type, "error": err.Error()})
			}
		}
	}
}

func nextEntryTime(entry Entry, now time.Time) time.Time {
	if entry.Every > 0 {
		return now.Add(entry.Every)
	}
	if entry.NextFunc == nil {
		return time.Time{}
	}
	next := entry.NextFunc(now)
	if !next.After(now) {
		return time.Time{}
	}
	return next
}
