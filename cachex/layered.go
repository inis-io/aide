package cachex

import (
	"time"
)

// ================================== 分层缓存 - 开始 ==================================

// LayeredStore - 分层缓存驱动：L1 内存（读快）+ L2 文件（重启不丢），cache-aside 模式
// 不变式：L1 是 L2 的保守子集——L1 条目只能来自 L2 回灌，且回灌 TTL 秒级向下取整，
// L1 只会比 L2 更早过期，读路径无脏读窗口（同进程内）。
type LayeredStore struct {
	// L1 内存层（*MemoryStore）
	L1 Store
	// L2 文件层（*FileStore，权威层）
	L2 Store
}

// newLayeredStore - 分层缓存驱动工厂（配置复用 Config.Memory / Config.File 两段，已归一化）
func newLayeredStore(config Config) (Store, error) {
	l1, err := newMemoryStore(config)
	if err != nil {
		return nil, err
	}
	l2, err := newFileStore(config) // file 工厂当前不失败，保留 error 对齐签名
	if err != nil {
		return nil, err
	}
	return &LayeredStore{L1: l1, L2: l2}, nil
}

// Has - 判断缓存是否存在（L1 是 L2 的保守子集，任一层存在即存在）
func (this *LayeredStore) Has(key string) (ok bool) {
	return this.L1.Has(key) || this.L2.Has(key)
}

// Get - 获取缓存（L1 未命中回源 L2，命中则带剩余 TTL 回灌 L1；回灌失败可容忍，下次读再回源）
func (this *LayeredStore) Get(key string) (value any) {
	if value = this.L1.Get(key); value != nil {
		return value
	}
	if value = this.L2.Get(key); value == nil {
		return nil
	}
	// 回灌：TTL -1 → 0（永不过期）；秒级向下取整保证 L1 先于 L2 过期
	ttl, _ := this.L2.TTL(key)
	if ttl < 0 {
		ttl = 0
	}
	this.L1.Set(key, value, time.Duration(ttl)*time.Second)
	return value
}

// Set - 设置缓存（L2 权威写入，成功后失效 L1；L2 失败则整体失败且不动 L1，保住"返回 true 必已持久化"）
func (this *LayeredStore) Set(key string, value any, expired time.Duration) (ok bool) {
	if !this.L2.Set(key, value, expired) {
		return false
	}
	this.L1.Delete(key)
	return true
}

// Delete - 删除缓存（权威层先动，避免 L2 残留被回灌复活）
func (this *LayeredStore) Delete(key ...string) (ok bool) {
	if !this.L2.Delete(key...) {
		return false
	}
	this.L1.Delete(key...)
	return true
}

// Clear - 清空缓存（继承 file 语义：清空整个文件根目录）
func (this *LayeredStore) Clear() (ok bool) {
	if !this.L2.Clear() {
		return false
	}
	this.L1.Clear()
	return true
}

// Incr - 原子自增 1（委托 L2 保证重启后计数连续，成功后失效 L1；计数器不可靠双写，失效即正确）
func (this *LayeredStore) Incr(key string, expired time.Duration) (count int64, err error) {
	count, err = this.L2.Incr(key, expired)
	if err != nil {
		return 0, err
	}
	this.L1.Delete(key)
	return count, nil
}

// SetNX - 仅当键不存在时设置（委托 L2，写入成功后失效 L1；已存在或出错原样返回）
func (this *LayeredStore) SetNX(key string, value any, expired time.Duration) (ok bool, err error) {
	ok, err = this.L2.SetNX(key, value, expired)
	if err != nil || !ok {
		return ok, err
	}
	this.L1.Delete(key)
	return true, nil
}

// TTL - 剩余存活秒数（热键先查 L1，未命中再查 L2）
func (this *LayeredStore) TTL(key string) (seconds int64, err error) {
	if seconds, err = this.L1.TTL(key); err != nil || seconds != 0 {
		return seconds, err
	}
	return this.L2.TTL(key)
}

// Close - 关闭分层实例：释放 L1 的 ristretto 后台 goroutine（L2 无 Close 概念）
func (this *LayeredStore) Close() error {
	if closer, ok := this.L1.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// 编译期接口校验
var _ Store = (*LayeredStore)(nil)

// 编译期接口校验：实现 io.Closer，门面热重载时统一关闭旧实例
var _ interface{ Close() error } = (*LayeredStore)(nil)

// ================================== 分层缓存 - 结束 ==================================
