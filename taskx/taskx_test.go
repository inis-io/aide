package taskx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/inis-io/aide/logx"
	"github.com/spf13/afero"
)

var _ Logger = (*logx.Logger)(nil)

func testConfig() Config {
	return normConfig(Config{
		Engine: "file", Concurrency: 2, Queues: map[string]int{"default": 1},
		PollInterval: 5 * time.Millisecond, LeaseTTL: 50 * time.Millisecond,
		ShutdownTimeout: 30 * time.Millisecond, JanitorInterval: time.Second,
		File:       FileConfig{Root: "queue"},
		RetryDelay: func(int, error) time.Duration { return 5 * time.Millisecond },
	})
}

func testFileDriver(t *testing.T) (*Driver, *fileBroker) {
	t.Helper()
	config := testConfig()
	broker, err := newFileBrokerWithFs(config, afero.NewMemMapFs())
	if err != nil {
		t.Fatalf("创建 file Broker 失败: %v", err)
	}
	driver := NewDriver(broker, config)
	return driver, broker.(*fileBroker)
}

// TestNormConfig - 验证 taskx 零配置默认值与非法引擎回退
func TestNormConfig(t *testing.T) {
	config := normConfig(Config{Engine: "not-found"})
	if config.Engine != "file" || config.Concurrency != 10 {
		t.Fatalf("默认配置不正确: %+v", config)
	}
	if config.Queues["default"] != 1 || config.PollInterval != time.Second || config.LeaseTTL != 30*time.Second {
		t.Fatalf("引擎默认值不正确: %+v", config)
	}
	if config.File.Root != "./runtime/queue" || config.Redis.Addr != "localhost:6379" || config.Redis.Prefix != "AIDE:TASKX:" {
		t.Fatalf("后端默认值不正确: %+v", config)
	}
	if config.Hash == "" || config.RetryDelay == nil {
		t.Fatal("配置指纹或默认退避函数未生成")
	}
}

// TestQueueFacadeOption - 验证 Queue 同时可作为函数式选项使用
func TestQueueFacadeOption(t *testing.T) {
	msg := NewMessage("test", nil)
	Queue("critical")(msg)
	if msg.Queue != "critical" {
		t.Fatalf("Queue 选项未生效: %s", msg.Queue)
	}
}

// TestDriverValueSemantics - 验证链式调用不会污染原实例与兄弟链
func TestDriverValueSemantics(t *testing.T) {
	driver, _ := testFileDriver(t)
	base := driver.New("demo", map[string]any{"id": 1})
	critical := base.Queue("critical").MaxRetry(8)
	low := base.Queue("low").MaxRetry(1)
	if base.msg.Queue != "" || critical.msg.Queue != "critical" || low.msg.Queue != "low" {
		t.Fatalf("链式值语义失效: base=%+v critical=%+v low=%+v", base.msg, critical.msg, low.msg)
	}
	if critical.msg.MaxRetry != 8 || low.msg.MaxRetry != 1 {
		t.Fatal("链式配置发生交叉污染")
	}
}

// TestRegistryAndFacadeReload - 验证扩展注册、独立实例与门面热重载保留路由
func TestRegistryAndFacadeReload(t *testing.T) {
	name := "memory-contract"
	Register(name, func(config Config) (Broker, error) {
		return newFileBrokerWithFs(config, afero.NewMemMapFs())
	})
	driver, err := New(name, Config{File: FileConfig{Root: "registry"}})
	if err != nil || driver == nil {
		t.Fatalf("注册驱动创建失败: driver=%+v err=%v", driver, err)
	}
	_ = driver.Close()
	found := false
	for _, item := range Names() {
		if item == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("注册表未返回扩展驱动: %v", Names())
	}

	savedInst, savedActive := Inst, activeQueue
	controller := &Controller{}
	Inst = controller
	t.Cleanup(func() {
		if activeQueue != nil && activeQueue != savedActive {
			_ = activeQueue.Close()
		}
		activeQueue = savedActive
		Inst = savedInst
	})
	controller.Init(Config{Engine: name, File: FileConfig{Root: "facade-a"}})
	Queue.Handle("reload", HandlerFunc(func(context.Context, *Message) error { return nil }))
	oldMux := activeQueue.mux
	controller.ReloadIfChanged(Config{Engine: name, File: FileConfig{Root: "facade-b"}})
	if activeQueue == nil || activeQueue.mux != oldMux {
		t.Fatal("热重载未保留 Handler/Middleware 路由器")
	}
	oldMux.mutex.RLock()
	handler := oldMux.handlers["reload"]
	oldMux.mutex.RUnlock()
	if handler == nil {
		t.Fatal("热重载后已注册 Handler 丢失")
	}

	broken := NewDriver(&brokerError{name: "broken", err: errors.New("初始化失败")}, testConfig())
	if _, err = broken.New("broken", nil).Enqueue(context.Background()); !errors.Is(err, ErrDriverNotReady) {
		t.Fatalf("brokerError 未保留哨兵错误: %v", err)
	}
}

// TestBrokerDirectEnqueue - 验证直接使用 Broker 时仍会归一化消息与确定性 ID
func TestBrokerDirectEnqueue(t *testing.T) {
	_, broker := testFileDriver(t)
	msg := NewMessage("direct", nil)
	if err := broker.Enqueue(context.Background(), msg); err != nil || msg.Id == "" || msg.Queue != "default" {
		t.Fatalf("Broker 直接入队未归一化消息: msg=%+v err=%v", msg, err)
	}
	first := NewMessage("direct-id", nil)
	first.Id = "direct-id-1"
	if err := broker.Enqueue(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := NewMessage("direct-id", nil)
	second.Id = "direct-id-1"
	if err := broker.Enqueue(context.Background(), second); !errors.Is(err, ErrTaskIdConflict) {
		t.Fatalf("Broker 直接入队未校验 TaskID: %v", err)
	}
}

// TestFileBrokerContract - 使用内存文件系统验证 file Broker 的完整状态机契约
func TestFileBrokerContract(t *testing.T) {
	driver, broker := testFileDriver(t)
	ctx := context.Background()

	id, err := driver.New("contract", map[string]any{"value": 1}).TaskID("contract-1").Retention(15 * time.Millisecond).Enqueue(ctx)
	if err != nil || id != "contract-1" {
		t.Fatalf("确定性任务入队失败: id=%s err=%v", id, err)
	}
	if _, err = driver.New("contract", map[string]any{"value": 2}).TaskID("contract-1").Enqueue(ctx); !errors.Is(err, ErrTaskIdConflict) {
		t.Fatalf("重复 TaskID 未返回哨兵错误: %v", err)
	}

	claimed, err := broker.Claim(ctx, []string{"default"})
	if err != nil || claimed == nil || claimed.Id != id {
		t.Fatalf("认领任务失败: msg=%+v err=%v", claimed, err)
	}
	claimed.RetryAt = time.Now().Add(-time.Millisecond)
	if err = broker.Retry(ctx, claimed, errors.New("第一次失败")); err != nil {
		t.Fatalf("任务转重试失败: %v", err)
	}
	retry, err := broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateRetry})
	if err != nil || retry.Total != 1 || retry.Tasks[0].Attempts != 1 {
		t.Fatalf("重试状态不正确: result=%+v err=%v", retry, err)
	}
	if moved, promoteErr := broker.Promote(ctx); promoteErr != nil || moved != 1 {
		t.Fatalf("重试任务搬运失败: moved=%d err=%v", moved, promoteErr)
	}
	claimed, _ = broker.Claim(ctx, []string{"default"})
	if claimed == nil || claimed.Attempts != 1 {
		t.Fatalf("重试任务再次认领失败: %+v", claimed)
	}
	lease := time.Now().Add(time.Second)
	if err = broker.Extend(ctx, claimed, lease); err != nil {
		t.Fatalf("任务续租失败: %v", err)
	}
	if moved, _ := broker.Promote(ctx); moved != 0 {
		t.Fatalf("未过期租约被错误回收: %d", moved)
	}
	if err = broker.Release(ctx, claimed); err != nil {
		t.Fatalf("任务归还失败: %v", err)
	}
	claimed, _ = broker.Claim(ctx, []string{"default"})
	if claimed == nil {
		t.Fatal("归还后的任务未能重新认领")
	}
	if err = broker.Archive(ctx, claimed, errors.New("重试耗尽")); err != nil {
		t.Fatalf("任务归档失败: %v", err)
	}
	archived, _ := broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateArchived})
	if archived.Total != 1 || archived.Tasks[0].LastError != "重试耗尽" {
		t.Fatalf("死信状态不正确: %+v", archived)
	}
	if err = broker.Manage(ctx, ManageOp{Action: "run", Queue: "default", State: stateArchived, Id: id}); err != nil {
		t.Fatalf("死信重跑失败: %v", err)
	}
	claimed, _ = broker.Claim(ctx, []string{"default"})
	if claimed == nil || claimed.Attempts != 0 || claimed.LastError != "" {
		t.Fatalf("重跑任务未重置执行上下文: %+v", claimed)
	}
	if err = broker.Ack(ctx, claimed); err != nil {
		t.Fatalf("任务确认失败: %v", err)
	}
	completed, _ := broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateCompleted})
	if completed.Total != 1 {
		t.Fatalf("完成任务未保留: %+v", completed)
	}
	time.Sleep(20 * time.Millisecond)
	_, _ = broker.Promote(ctx)
	completed, _ = broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateCompleted})
	if completed.Total != 0 {
		t.Fatalf("完成任务未按 Retention 清理: %+v", completed)
	}
}

// TestFileBrokerUniqueAndScheduled - 验证内容去重、延迟任务与锁所有权
func TestFileBrokerUniqueAndScheduled(t *testing.T) {
	driver, broker := testFileDriver(t)
	ctx := context.Background()
	first, err := driver.New("unique", map[string]any{"id": 1}).Unique(time.Hour).Enqueue(ctx)
	if err != nil {
		t.Fatalf("唯一任务入队失败: %v", err)
	}
	if _, err = driver.New("unique", map[string]any{"id": 1}).Unique(time.Hour).Enqueue(ctx); !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("内容去重未生效: %v", err)
	}
	msg, _ := broker.Claim(ctx, []string{"default"})
	msg.RetryAt = time.Now().Add(-time.Millisecond)
	if err = broker.Retry(ctx, msg, errors.New("失败")); err != nil {
		t.Fatal(err)
	}
	second, err := driver.New("unique", map[string]any{"id": 1}).Unique(time.Hour).Enqueue(ctx)
	if err != nil || second == first {
		t.Fatalf("Retry 释放 Unique 锁后仍无法入队: first=%s second=%s err=%v", first, second, err)
	}
	// 旧任务后续状态迁移不能误删新任务持有的同内容锁。
	if _, err = driver.New("unique", map[string]any{"id": 1}).Unique(time.Hour).Enqueue(ctx); !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("新任务的锁所有权被旧任务破坏: %v", err)
	}

	delayedID, err := driver.New("delayed", nil).At(time.Now().Add(15 * time.Millisecond)).Enqueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateScheduled})
	if result.Total != 1 || result.Tasks[0].Id != delayedID {
		t.Fatalf("延迟任务未进入 scheduled: %+v", result)
	}
	time.Sleep(20 * time.Millisecond)
	if moved, err := broker.Promote(ctx); err != nil || moved < 2 {
		t.Fatalf("到期任务搬运失败: moved=%d err=%v", moved, err)
	}
}

// TestFileBrokerConcurrentClaim - 验证并发认领的排他性
func TestFileBrokerConcurrentClaim(t *testing.T) {
	driver, broker := testFileDriver(t)
	if _, err := driver.New("concurrent", nil).Enqueue(context.Background()); err != nil {
		t.Fatal(err)
	}
	var claimed atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			msg, _ := broker.Claim(context.Background(), []string{"default"})
			if msg != nil {
				claimed.Add(1)
			}
		}()
	}
	wait.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("同一任务被认领 %d 次", claimed.Load())
	}
}

// TestRedisBrokerContract - 使用 miniredis 验证 Redis Lua 状态迁移与检视管理契约
func TestRedisBrokerContract(t *testing.T) {
	server := miniredis.RunT(t)
	config := testConfig()
	config.Engine = "redis"
	config.Redis.Addr = server.Addr()
	config.Redis.Prefix = "TEST:TASKX:"
	broker, err := newRedisBroker(config)
	if err != nil {
		t.Fatalf("创建 Redis Broker 失败: %v", err)
	}
	defer broker.Close()
	driver := NewDriver(broker, config)
	ctx := context.Background()

	id, err := driver.New("redis-contract", map[string]int{"id": 1}).TaskID("redis-1").MaxRetry(1).Enqueue(ctx)
	if err != nil || id != "redis-1" {
		t.Fatalf("Redis 入队失败: id=%s err=%v", id, err)
	}
	if _, err = driver.New("redis-contract", nil).TaskID("redis-1").Enqueue(ctx); !errors.Is(err, ErrTaskIdConflict) {
		t.Fatalf("Redis TaskID 冲突未生效: %v", err)
	}
	msg, err := broker.Claim(ctx, []string{"default"})
	if err != nil || msg == nil {
		t.Fatalf("Redis 认领失败: msg=%+v err=%v", msg, err)
	}
	msg.RetryAt = time.Now().Add(-time.Millisecond)
	if err = broker.Retry(ctx, msg, errors.New("临时失败")); err != nil {
		t.Fatalf("Redis Retry 失败: %v", err)
	}
	if moved, promoteErr := broker.Promote(ctx); promoteErr != nil || moved != 1 {
		t.Fatalf("Redis Promote 失败: moved=%d err=%v", moved, promoteErr)
	}
	msg, _ = broker.Claim(ctx, []string{"default"})
	if msg == nil || msg.Attempts != 1 || msg.LastError != "临时失败" {
		t.Fatalf("Redis 重试消息不正确: %+v", msg)
	}
	if err = broker.Archive(ctx, msg, errors.New("最终失败")); err != nil {
		t.Fatalf("Redis Archive 失败: %v", err)
	}
	archived, err := broker.Inspect(ctx, InspectQuery{Queue: "default", State: stateArchived})
	if err != nil || archived.Total != 1 || archived.Tasks[0].LastError != "最终失败" {
		t.Fatalf("Redis archived 检视失败: result=%+v err=%v", archived, err)
	}
	if err = broker.Manage(ctx, ManageOp{Action: "run", Queue: "default", State: stateArchived, Id: id}); err != nil {
		t.Fatalf("Redis 死信重跑失败: %v", err)
	}
	msg, _ = broker.Claim(ctx, []string{"default"})
	if msg == nil || msg.Attempts != 0 || msg.LastError != "" {
		t.Fatalf("Redis 重跑上下文未重置: %+v", msg)
	}
	if err = broker.Ack(ctx, msg); err != nil {
		t.Fatalf("Redis Ack 失败: %v", err)
	}

	if _, err = driver.New("redis-unique", map[string]int{"id": 2}).Unique(time.Hour).Enqueue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = driver.New("redis-unique", map[string]int{"id": 2}).Unique(time.Hour).Enqueue(ctx); !errors.Is(err, ErrDuplicateTask) {
		t.Fatalf("Redis Unique 未生效: %v", err)
	}
	delayed, err := driver.New("redis-delayed", nil).At(time.Now().Add(-time.Millisecond)).Enqueue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claimed, _ := broker.Claim(ctx, []string{"default"})
	if claimed == nil {
		t.Fatal("Redis 即时任务未进入 pending")
	}
	if claimed.Id != delayed {
		// pending 中可能先认领前面的 unique 任务，再认领当前任务。
		_ = broker.Ack(ctx, claimed)
		claimed, _ = broker.Claim(ctx, []string{"default"})
	}
	if claimed == nil || claimed.Id != delayed {
		t.Fatalf("Redis pending 顺序异常: %+v", claimed)
	}
	_ = broker.Ack(ctx, claimed)
}

// TestEngineRetryAndPanic - 验证引擎自动重试、失败钩子与 panic 归档
func TestEngineRetryAndPanic(t *testing.T) {
	driver, _ := testFileDriver(t)
	var attempts atomic.Int32
	var failures atomic.Int32
	driver.config.ErrorHandler = func(context.Context, *Message, error) { failures.Add(1) }
	driver.engine.config.ErrorHandler = driver.config.ErrorHandler
	done := make(chan struct{})
	driver.Handle("retry", HandlerFunc(func(context.Context, *Message) error {
		if attempts.Add(1) == 1 {
			return errors.New("临时失败")
		}
		close(done)
		return nil
	}))
	if _, err := driver.New("retry", nil).MaxRetry(1).Enqueue(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- driver.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("重试任务未在预期时间内完成")
	}
	cancel()
	if err := <-runDone; err != nil {
		t.Fatalf("引擎退出失败: %v", err)
	}
	if attempts.Load() != 2 || failures.Load() != 1 {
		t.Fatalf("重试或错误钩子次数不正确: attempts=%d failures=%d", attempts.Load(), failures.Load())
	}

	panicDriver, broker := testFileDriver(t)
	panicSeen := make(chan error, 1)
	panicDriver.config.ErrorHandler = func(_ context.Context, _ *Message, err error) { panicSeen <- err }
	panicDriver.engine.config.ErrorHandler = panicDriver.config.ErrorHandler
	panicDriver.Handle("panic", HandlerFunc(func(context.Context, *Message) error { panic("boom") }))
	_, _ = panicDriver.New("panic", nil).Enqueue(context.Background())
	panicCtx, panicCancel := context.WithCancel(context.Background())
	panicDone := make(chan error, 1)
	go func() { panicDone <- panicDriver.Run(panicCtx) }()
	select {
	case err := <-panicSeen:
		if err == nil {
			t.Fatal("panic 未转换为错误")
		}
	case <-time.After(time.Second):
		t.Fatal("panic 失败钩子未触发")
	}
	panicCancel()
	<-panicDone
	archived, _ := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: stateArchived})
	if archived.Total != 1 {
		t.Fatalf("panic 任务未归档: %+v", archived)
	}
}

// TestEngineShutdownRelease - 验证退出超时会取消并归还在途任务
func TestEngineShutdownRelease(t *testing.T) {
	driver, broker := testFileDriver(t)
	started := make(chan struct{})
	driver.Handle("blocking", HandlerFunc(func(context.Context, *Message) error {
		close(started)
		time.Sleep(200 * time.Millisecond)
		return nil
	}))
	_, _ = driver.New("blocking", nil).Enqueue(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- driver.Run(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("阻塞任务未启动")
	}
	if err := driver.Shutdown(); err != nil {
		t.Fatalf("优雅退出失败: %v", err)
	}
	<-runDone
	pending, _ := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: statePending})
	if pending.Total != 1 {
		t.Fatalf("在途任务未归还 pending: %+v", pending)
	}
}

// TestSchedulerEvery - 验证固定间隔周期任务会重复入队并可停止
func TestSchedulerEvery(t *testing.T) {
	driver, broker := testFileDriver(t)
	scheduler := NewScheduler(driver)
	if err := scheduler.Register(Entry{Type: "tick", Payload: map[string]int{"v": 1}, Every: 10 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, _ := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: statePending})
		if result.Total >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	scheduler.Stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result, _ := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: statePending})
	if result.Total < 2 {
		t.Fatalf("周期任务触发次数不足: %+v", result)
	}
	if err := scheduler.Register(Entry{Type: "bad", Every: time.Second, NextFunc: func(time.Time) time.Time { return time.Now() }}); err == nil {
		t.Fatal("Every 与 NextFunc 同时设置时应拒绝")
	}
}

// TestInspectAndManageValidation - 验证检视分页与管理操作边界
func TestInspectAndManageValidation(t *testing.T) {
	driver, broker := testFileDriver(t)
	for index := 0; index < 3; index++ {
		_, _ = driver.New("page", map[string]int{"index": index}).Enqueue(context.Background())
	}
	page, err := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: statePending, Page: 2, Size: 2})
	if err != nil || page.Total != 3 || len(page.Tasks) != 1 {
		t.Fatalf("检视分页错误: result=%+v err=%v", page, err)
	}
	if err = broker.Manage(context.Background(), ManageOp{Action: "purge", Queue: "default", State: statePending}); err == nil {
		t.Fatal("清空 pending 应被拒绝")
	}
	if _, err = broker.Inspect(context.Background(), InspectQuery{State: statePending}); err == nil {
		t.Fatal("列表模式缺少队列时应报错")
	}
	_ = fmt.Sprintf("%v", driver)
}

// TestEngineArchiveHook - 验证归档（死信）钩子仅在归档成功后触发一次
func TestEngineArchiveHook(t *testing.T) {
	driver, broker := testFileDriver(t)
	type archiveRecord struct {
		msg   *Message
		cause error
	}
	archived := make(chan archiveRecord, 4)
	var failures atomic.Int32
	driver.config.ErrorHandler = func(context.Context, *Message, error) { failures.Add(1) }
	driver.engine.config.ErrorHandler = driver.config.ErrorHandler
	driver.config.ArchiveHandler = func(_ context.Context, msg *Message, cause error) {
		archived <- archiveRecord{msg: msg, cause: cause}
	}
	driver.engine.config.ArchiveHandler = driver.config.ArchiveHandler
	driver.Handle("dead", HandlerFunc(func(context.Context, *Message) error { return errors.New("始终失败") }))
	driver.Handle("ok", HandlerFunc(func(context.Context, *Message) error { return nil }))
	id, err := driver.New("dead", nil).MaxRetry(0).Enqueue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = driver.New("ok", nil).Enqueue(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- driver.Run(ctx) }()
	select {
	case record := <-archived:
		if record.msg.Id != id || record.cause == nil || record.cause.Error() != "始终失败" {
			t.Fatalf("归档钩子内容不正确: msg=%+v cause=%v", record.msg, record.cause)
		}
	case <-time.After(time.Second):
		t.Fatal("归档钩子未在预期时间内触发")
	}
	cancel()
	if err = <-runDone; err != nil {
		t.Fatalf("引擎退出失败: %v", err)
	}
	if len(archived) != 0 {
		t.Fatal("成功任务不应触发归档钩子")
	}
	if failures.Load() != 1 {
		t.Fatalf("失败钩子次数不正确: %d", failures.Load())
	}
	result, _ := broker.Inspect(context.Background(), InspectQuery{Queue: "default", State: stateArchived})
	if result.Total != 1 {
		t.Fatalf("任务未归档: %+v", result)
	}
}
