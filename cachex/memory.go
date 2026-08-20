package cachex

import (
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/spf13/cast"
)

// ================================== 内存缓存 - 开始 ==================================

// MemoryStore - 内存缓存驱动（基于 ristretto v2：TinyLFU 准入 + SampledLFU 淘汰）
type MemoryStore struct {
	// ristretto 客户端（导出便于测试 Wait/Close 及高级用法直连异步写）
	Client *ristretto.Cache[string, any]
	// 配置
	Config MemoryConfig
	// 原子方法（Incr/SetNX）的分段锁：按 key 哈希分片，同键串行、异键并行
	locks shardLocks
}

// newMemoryStore - 内存缓存驱动工厂
func newMemoryStore(config Config) (Store, error) {
	conf := config.Memory
	client, err := ristretto.NewCache[string, any](&ristretto.Config[string, any]{
		NumCounters:        10 * conf.MaxEntries, // 官方建议计数器为条目数 10 倍
		MaxCost:            conf.MaxEntries,      // 配合 IgnoreInternalCost + cost 恒 1，语义 = 最大条目数
		BufferItems:        64,                   // 官方建议值
		Metrics:            conf.Metrics,
		IgnoreInternalCost: true, // 不叠加内部 storeItem 开销，否则实际容量远小于 MaxEntries
	})
	if err != nil {
		return nil, err
	}
	return &MemoryStore{Client: client, Config: conf}, nil
}

// Has - 判断缓存是否存在（ristretto Get 不返回过期项，过期视为不存在）
func (this *MemoryStore) Has(key string) (ok bool) {
	_, ok = this.Client.Get(key)
	return ok
}

// Get - 获取缓存（未命中或已过期返回 nil）
func (this *MemoryStore) Get(key string) (value any) {
	value, ok := this.Client.Get(key)
	if !ok {
		return nil
	}
	return value
}

// Set - 设置缓存（expired <= 0 表示永不过期；Wait 保证写后立即可读）
func (this *MemoryStore) Set(key string, value any, expired time.Duration) (ok bool) {
	// ristretto 负 ttl 是 no-op，归一到 0（永不过期）
	if expired < 0 {
		expired = 0
	}
	if !this.Client.SetWithTTL(key, value, 1, expired) {
		return false // 写缓冲已满被丢弃
	}
	this.Client.Wait()
	return true
}

// Delete - 删除缓存（ristretto Del 对存储即时生效，无需 Wait）
func (this *MemoryStore) Delete(key ...string) (ok bool) {
	for _, item := range key {
		this.Client.Del(item)
	}
	return true
}

// Clear - 清空缓存（实例级：每个 memory store 持有独立 ristretto 实例，无前缀隔离问题）
func (this *MemoryStore) Clear() (ok bool) {
	this.Client.Clear()
	return true
}

// Incr - 原子自增 1（分段锁内读-改-写串行；仅当自增结果为 1 时写入过期时间，固定窗口语义）
func (this *MemoryStore) Incr(key string, expired time.Duration) (count int64, err error) {
	lock := this.locks.lock(key)
	lock.Lock()
	defer lock.Unlock()

	count = 1
	ttl := expired
	if value, ok := this.Client.Get(key); ok {
		// 已存在：累加计数并保留原过期时间（GetTTL 返回 (0,true) 表示永不过期）
		count = cast.ToInt64(value) + 1
		if rest, found := this.Client.GetTTL(key); found {
			ttl = rest
		}
	}
	if ttl < 0 {
		ttl = 0
	}
	if !this.Client.SetWithTTL(key, count, 1, ttl) {
		return 0, fmt.Errorf("cachex: memory 驱动写入被丢弃")
	}
	// 为新键首写兜底：等准入结果对后续 Get 可见（已存在键的更新即时生效，此处只是快路径）
	this.Client.Wait()
	return count, nil
}

// SetNX - 仅当键不存在时设置（已存在不覆盖、不续期）
func (this *MemoryStore) SetNX(key string, value any, expired time.Duration) (ok bool, err error) {
	lock := this.locks.lock(key)
	lock.Lock()
	defer lock.Unlock()

	if _, ok = this.Client.Get(key); ok {
		return false, nil
	}
	if expired < 0 {
		expired = 0
	}
	if !this.Client.SetWithTTL(key, value, 1, expired) {
		return false, fmt.Errorf("cachex: memory 驱动写入被丢弃")
	}
	this.Client.Wait()
	return true, nil
}

// TTL - 剩余存活秒数（>0 有效；0 = 不存在或已过期；-1 = 存在但永不过期）
func (this *MemoryStore) TTL(key string) (seconds int64, err error) {
	ttl, ok := this.Client.GetTTL(key)
	if !ok {
		return 0, nil
	}
	if ttl <= 0 {
		return -1, nil // 存在但永不过期
	}
	// 向下取整与 redis 驱动一致；不足 1 秒会归 0（与"不存在"同值），属已知精度取舍
	return int64(ttl / time.Second), nil
}

// Close - 关闭缓存实例：停止 ristretto 后台 goroutine 与清理 ticker（幂等）
func (this *MemoryStore) Close() error {
	this.Client.Close()
	return nil
}

// 编译期接口校验
var _ Store = (*MemoryStore)(nil)

// 编译期接口校验：实现 io.Closer，门面热重载时统一关闭旧实例
var _ interface{ Close() error } = (*MemoryStore)(nil)

// ================================== 内存缓存 - 结束 ==================================
