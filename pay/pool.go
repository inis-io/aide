package pay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// PoolOption - 多网关 Pool 构造选项
type PoolOption func(*poolOptions)

type poolOptions struct {
	maxEntries   int
	idleTTL      time.Duration
	drainTimeout time.Duration
	openOptions  []Option
	clock        Clock
}

// WithPoolMaxEntries - 设置 Pool 最大活动实例数
func WithPoolMaxEntries(maxEntries int) PoolOption {
	return func(options *poolOptions) { options.maxEntries = maxEntries }
}

// WithPoolIdleTTL - 设置无租约实例的空闲回收时间
func WithPoolIdleTTL(ttl time.Duration) PoolOption {
	return func(options *poolOptions) { options.idleTTL = ttl }
}

// WithPoolDrainTimeout - 设置关闭时等待在途租约的上限
func WithPoolDrainTimeout(timeout time.Duration) PoolOption {
	return func(options *poolOptions) { options.drainTimeout = timeout }
}

// WithPoolClock - 注入 Pool 的 TTL/LRU 测试时钟
func WithPoolClock(clock Clock) PoolOption {
	return func(options *poolOptions) {
		if clock != nil {
			options.clock = clock
		}
	}
}

// WithPoolOpenOptions - 设置所有动态 Provider 共用的构造选项
func WithPoolOpenOptions(options ...Option) PoolOption {
	return func(target *poolOptions) { target.openOptions = append(target.openOptions, options...) }
}

// PoolDrainError - Pool 关闭超时且仍有实例被租用
type PoolDrainError struct {
	// InUse - 仍被租用的实例数量
	InUse int
}

// Error - 返回未释放实例数量
func (this *PoolDrainError) Error() string {
	return fmt.Sprintf("pay: Pool 关闭超时，仍有 %d 个实例正在使用", this.InUse)
}

// Pool - 带租约、版本切换、TTL 和 LRU 的多网关实例池
type Pool struct {
	registry *Registry
	resolver Resolver
	options  poolOptions

	mu         sync.Mutex
	entries    map[string]*poolEntry
	retired    map[*poolEntry]struct{}
	closed     bool
	generation uint64
	signal     chan struct{}

	keyMu    sync.Mutex
	keyLocks map[string]*keyLock
	closeMu  sync.Mutex
}

type poolEntry struct {
	key           string
	driver        *Driver
	name          string
	version       string
	schemaVersion uint16
	sandbox       bool
	configDigest  string
	leases        int
	retiring      bool
	lastUsed      time.Time
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

// Lease - 在途请求持有的 Provider 租约
type Lease interface {
	Driver() *Driver
	Release()
}

type poolLease struct {
	pool  *Pool
	entry *poolEntry
	once  sync.Once
}

// Driver - 返回租约保护的 Driver
func (this *poolLease) Driver() *Driver {
	if this == nil || this.entry == nil {
		return nil
	}
	return this.entry.driver
}

// Release - 幂等释放租约
func (this *poolLease) Release() {
	if this == nil {
		return
	}
	this.once.Do(func() { this.pool.release(this.entry) })
}

// NewPool - 创建多网关 Provider Pool
func NewPool(registry *Registry, resolver Resolver, options ...PoolOption) *Pool {
	settings := poolOptions{maxEntries: 128, idleTTL: 15 * time.Minute, drainTimeout: 30 * time.Second, clock: systemClock{}}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if settings.maxEntries <= 0 {
		settings.maxEntries = 128
	}
	if settings.idleTTL <= 0 {
		settings.idleTTL = 15 * time.Minute
	}
	if settings.drainTimeout <= 0 {
		settings.drainTimeout = 30 * time.Second
	}
	return &Pool{registry: registry, resolver: resolver, options: settings, entries: make(map[string]*poolEntry), retired: make(map[*poolEntry]struct{}), signal: make(chan struct{}, 1), keyLocks: make(map[string]*keyLock)}
}

// Acquire - 解析网关配置并获取租约
func (this *Pool) Acquire(ctx context.Context, key string) (Lease, error) {
	if this == nil || this.registry == nil || this.resolver == nil {
		return nil, fmt.Errorf("%w：Pool 依赖不完整", ErrInvalidConfig)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w：网关 key 为空", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	lock := this.acquireKeyLock(key)
	defer this.releaseKeyLock(key, lock)

	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		return nil, ErrPoolClosed
	}
	generation := this.generation
	toClose := this.pruneExpiredLocked(this.options.clock.Now())
	this.mu.Unlock()
	closeDrivers(toClose)

	definition, err := this.resolver.Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	if definition.SchemaVersion == 0 {
		definition.SchemaVersion = 1
	}
	if normalizeProviderName(definition.Name) == "" || strings.TrimSpace(definition.Version) == "" || len(definition.Config) == 0 {
		return nil, fmt.Errorf("%w：Resolver 定义缺少名称、版本或配置", ErrInvalidConfig)
	}
	digestBytes := sha256.Sum256(definition.Config)
	digest := hex.EncodeToString(digestBytes[:])

	this.mu.Lock()
	if current := this.entries[key]; this.generation == generation && current != nil && sameDefinition(current, definition, digest) && !current.retiring {
		current.leases++
		current.lastUsed = this.options.clock.Now()
		this.mu.Unlock()
		return &poolLease{pool: this, entry: current}, nil
	}
	this.mu.Unlock()

	openOptions := append([]Option(nil), this.options.openOptions...)
	openOptions = append(openOptions, WithSandbox(definition.Sandbox), WithSchemaVersion(definition.SchemaVersion))
	driver, err := this.registry.OpenRaw(ctx, definition.Name, definition.Config, openOptions...)
	if err != nil {
		return nil, err
	}
	entry := &poolEntry{key: key, driver: driver, name: normalizeProviderName(definition.Name), version: definition.Version, schemaVersion: definition.SchemaVersion, sandbox: definition.Sandbox, configDigest: digest, leases: 1, lastUsed: this.options.clock.Now()}

	var closing []*Driver
	this.mu.Lock()
	if this.closed {
		this.mu.Unlock()
		_ = driver.Close()
		return nil, ErrPoolClosed
	}
	if this.generation != generation {
		this.mu.Unlock()
		_ = driver.Close()
		return nil, fmt.Errorf("%w：Pool 在构造期间已失效", ErrGatewayUnavailable)
	}
	if _, exists := this.entries[key]; !exists && len(this.entries) >= this.options.maxEntries {
		evicted := this.evictLRULocked(key)
		if evicted == nil {
			this.mu.Unlock()
			_ = driver.Close()
			return nil, fmt.Errorf("%w：Pool 容量已满且所有实例均在使用", ErrGatewayUnavailable)
		}
		closing = append(closing, evicted)
	}
	if old := this.entries[key]; old != nil {
		old.retiring = true
		this.retired[old] = struct{}{}
		if old.leases == 0 {
			delete(this.retired, old)
			closing = append(closing, old.driver)
		}
	}
	this.entries[key] = entry
	this.mu.Unlock()
	closeDrivers(closing)
	return &poolLease{pool: this, entry: entry}, nil
}

// Invalidate - 使指定 key 的活动实例停止接受新租约
func (this *Pool) Invalidate(keys ...string) {
	if this == nil {
		return
	}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		lock := this.acquireKeyLock(key)
		var closing []*Driver
		this.mu.Lock()
		if entry := this.entries[key]; entry != nil {
			delete(this.entries, key)
			entry.retiring = true
			this.retired[entry] = struct{}{}
			if entry.leases == 0 {
				delete(this.retired, entry)
				closing = append(closing, entry.driver)
			}
		}
		this.mu.Unlock()
		this.releaseKeyLock(key, lock)
		closeDrivers(closing)
	}
}

// Clear - 失效所有活动实例，在途实例于租约释放后关闭
func (this *Pool) Clear() {
	if this == nil {
		return
	}
	var closing []*Driver
	this.mu.Lock()
	this.generation++
	for key, entry := range this.entries {
		delete(this.entries, key)
		entry.retiring = true
		this.retired[entry] = struct{}{}
		if entry.leases == 0 {
			delete(this.retired, entry)
			closing = append(closing, entry.driver)
		}
	}
	this.mu.Unlock()
	closeDrivers(closing)
}

// Close - 禁止新租约并等待在途实例释放；可重复调用
func (this *Pool) Close() error {
	if this == nil {
		return nil
	}
	this.closeMu.Lock()
	defer this.closeMu.Unlock()
	var closing []*Driver
	this.mu.Lock()
	this.closed = true
	this.generation++
	for key, entry := range this.entries {
		delete(this.entries, key)
		entry.retiring = true
		this.retired[entry] = struct{}{}
		if entry.leases == 0 {
			delete(this.retired, entry)
			closing = append(closing, entry.driver)
		}
	}
	this.mu.Unlock()
	closeDrivers(closing)
	deadline := time.NewTimer(this.options.drainTimeout)
	defer deadline.Stop()
	for {
		this.mu.Lock()
		inUse := len(this.retired)
		this.mu.Unlock()
		if inUse == 0 {
			return nil
		}
		select {
		case <-this.signal:
		case <-deadline.C:
			return &PoolDrainError{InUse: inUse}
		}
	}
}

func (this *Pool) release(entry *poolEntry) {
	var driver *Driver
	this.mu.Lock()
	if entry.leases > 0 {
		entry.leases--
	}
	entry.lastUsed = this.options.clock.Now()
	if entry.retiring && entry.leases == 0 {
		delete(this.retired, entry)
		driver = entry.driver
	}
	this.mu.Unlock()
	if driver != nil {
		_ = driver.Close()
	}
	select {
	case this.signal <- struct{}{}:
	default:
	}
}

func (this *Pool) acquireKeyLock(key string) *keyLock {
	this.keyMu.Lock()
	lock := this.keyLocks[key]
	if lock == nil {
		lock = &keyLock{}
		this.keyLocks[key] = lock
	}
	lock.refs++
	this.keyMu.Unlock()
	lock.mu.Lock()
	return lock
}

func (this *Pool) releaseKeyLock(key string, lock *keyLock) {
	lock.mu.Unlock()
	this.keyMu.Lock()
	lock.refs--
	if lock.refs == 0 {
		delete(this.keyLocks, key)
	}
	this.keyMu.Unlock()
}

func (this *Pool) pruneExpiredLocked(now time.Time) []*Driver {
	var closing []*Driver
	for key, entry := range this.entries {
		if entry.leases == 0 && now.Sub(entry.lastUsed) >= this.options.idleTTL {
			delete(this.entries, key)
			entry.retiring = true
			closing = append(closing, entry.driver)
		}
	}
	return closing
}

func (this *Pool) evictLRULocked(exclude string) *Driver {
	candidates := make([]*poolEntry, 0, len(this.entries))
	for key, entry := range this.entries {
		if key != exclude && entry.leases == 0 {
			candidates = append(candidates, entry)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].lastUsed.Before(candidates[j].lastUsed) })
	victim := candidates[0]
	delete(this.entries, victim.key)
	victim.retiring = true
	return victim.driver
}

func sameDefinition(entry *poolEntry, definition Definition, digest string) bool {
	return entry.name == normalizeProviderName(definition.Name) && entry.version == definition.Version && entry.schemaVersion == definition.SchemaVersion && entry.sandbox == definition.Sandbox && entry.configDigest == digest
}

func closeDrivers(drivers []*Driver) {
	for _, driver := range drivers {
		if driver != nil {
			_ = driver.Close()
		}
	}
}
