package pay

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mutableResolver struct {
	mu         sync.RWMutex
	definition Definition
}

func (this *mutableResolver) Resolve(context.Context, string) (Definition, error) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	result := this.definition
	result.Config = append(json.RawMessage(nil), result.Config...)
	return result, nil
}
func (this *mutableResolver) set(definition Definition) {
	this.mu.Lock()
	this.definition = definition
	this.mu.Unlock()
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (this *fakeClock) Now() time.Time { this.mu.Lock(); defer this.mu.Unlock(); return this.now }
func (this *fakeClock) add(duration time.Duration) {
	this.mu.Lock()
	this.now = this.now.Add(duration)
	this.mu.Unlock()
}

// TestPoolConcurrentSingleBuildAndRotation - 验证并发单建、版本轮换与租约延迟关闭
func TestPoolConcurrentSingleBuildAndRotation(t *testing.T) {
	registry := NewRegistry()
	var builds atomic.Int32
	var mu sync.Mutex
	providers := make([]*testProvider, 0, 2)
	if err := registry.Register("demo", func(context.Context, ConfigInput, OpenOptions) (Provider, error) {
		builds.Add(1)
		provider := &testProvider{name: "demo"}
		mu.Lock()
		providers = append(providers, provider)
		mu.Unlock()
		return provider, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolver := &mutableResolver{definition: Definition{Name: "demo", Config: json.RawMessage(`{"id":1}`), Version: "v1", SchemaVersion: 1}}
	pool := NewPool(registry, resolver)
	var wg sync.WaitGroup
	leases := make([]Lease, 24)
	wg.Add(len(leases))
	for index := range leases {
		go func(index int) {
			defer wg.Done()
			lease, err := pool.Acquire(context.Background(), "gateway:1")
			if err != nil {
				t.Errorf("Acquire 失败：%v", err)
				return
			}
			leases[index] = lease
		}(index)
	}
	wg.Wait()
	if builds.Load() != 1 {
		t.Fatalf("并发首次加载应只构造一次，实际 %d", builds.Load())
	}
	for _, lease := range leases[1:] {
		lease.Release()
	}
	resolver.set(Definition{Name: "demo", Config: json.RawMessage(`{"id":2}`), Version: "v2", SchemaVersion: 1})
	newLease, err := pool.Acquire(context.Background(), "gateway:1")
	if err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 2 {
		t.Fatalf("版本变化应构造新实例，实际 %d", builds.Load())
	}
	if providers[0].closed.Load() != 0 {
		t.Fatal("旧实例仍有租约时不能提前关闭")
	}
	leases[0].Release()
	if providers[0].closed.Load() != 1 {
		t.Fatal("最后一个旧租约释放后应关闭旧实例")
	}
	newLease.Release()
	if err = pool.Close(); err != nil {
		t.Fatal(err)
	}
	if providers[1].closed.Load() != 1 {
		t.Fatal("Pool Close 应关闭活动实例")
	}
	if _, err = pool.Acquire(context.Background(), "gateway:1"); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("关闭后不得新建租约：%v", err)
	}
}

// TestPoolTTLAndDrainTimeout - 验证空闲 TTL 回收及关闭 Drain 超时可恢复
func TestPoolTTLAndDrainTimeout(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	registry := NewRegistry()
	var builds atomic.Int32
	_ = registry.Register("demo", func(context.Context, ConfigInput, OpenOptions) (Provider, error) {
		builds.Add(1)
		return &testProvider{name: "demo"}, nil
	})
	resolver := ResolverFunc(func(context.Context, string) (Definition, error) {
		return Definition{Name: "demo", Config: json.RawMessage(`{}`), Version: "v1", SchemaVersion: 1}, nil
	})
	pool := NewPool(registry, resolver, WithPoolIdleTTL(time.Minute), WithPoolClock(clock), WithPoolDrainTimeout(20*time.Millisecond))
	lease, err := pool.Acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	clock.add(2 * time.Minute)
	second, err := pool.Acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	if builds.Load() != 2 {
		t.Fatalf("TTL 后应重建实例：%d", builds.Load())
	}
	if err = pool.Close(); err == nil {
		t.Fatal("仍有租约时应在 DrainTimeout 后返回错误")
	} else {
		var drain *PoolDrainError
		if !errors.As(err, &drain) || drain.InUse != 1 {
			t.Fatalf("Drain 错误不正确：%v", err)
		}
	}
	second.Release()
	if err = pool.Close(); err != nil {
		t.Fatalf("租约释放后再次 Close 应成功：%v", err)
	}
}

// TestPoolLRUAndInvalidate - 验证容量 LRU 回收与显式失效后重建
func TestPoolLRUAndInvalidate(t *testing.T) {
	clock := &fakeClock{now: time.Unix(200, 0)}
	registry := NewRegistry()
	var builds atomic.Int32
	_ = registry.Register("demo", func(context.Context, ConfigInput, OpenOptions) (Provider, error) {
		builds.Add(1)
		return &testProvider{name: "demo"}, nil
	})
	resolver := ResolverFunc(func(context.Context, string) (Definition, error) {
		return Definition{Name: "demo", Config: json.RawMessage(`{}`), Version: "v1", SchemaVersion: 1}, nil
	})
	pool := NewPool(registry, resolver, WithPoolMaxEntries(2), WithPoolClock(clock))
	defer pool.Close()
	for _, key := range []string{"a", "b", "c"} {
		lease, err := pool.Acquire(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		lease.Release()
		clock.add(time.Second)
	}
	if builds.Load() != 3 {
		t.Fatalf("初始构造数不符：%d", builds.Load())
	}
	lease, err := pool.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if builds.Load() != 4 {
		t.Fatalf("最旧的 a 应被 LRU 回收并重建：%d", builds.Load())
	}
	pool.Invalidate("a")
	lease, err = pool.Acquire(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if builds.Load() != 5 {
		t.Fatalf("Invalidate 后应重建：%d", builds.Load())
	}
}
