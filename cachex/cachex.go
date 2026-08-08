// Package cachex - 缓存包：以接口模式封装文件、Redis 等缓存能力
//
// 设计要点：
//   - Store 是唯一扩展点：新后端只需实现 Has/Get/Set/Delete/Clear 五个方法
//   - 内置驱动在注册表变量初始化时登记（不依赖文件 init 顺序）；外部驱动在自己包内
//     通过 init() + Register 注册，同名注册会覆盖先注册者（可借此替换内置实现）
//   - 扩展驱动的自定义配置通过 Config.Options 传入（key 为驱动名）
//   - Driver 在 Store 之上提供链式调用（值语义，天然隔离上下文，无需 clone），
//     键命名（前缀 + Hash）与标签簿记统一收敛在 Driver 层，后端不感知
//   - Inst + Cache 提供与 facade 层一致的全局单例入口
package cachex

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 接口与注册表 - 开始 ==================================

// Store - 缓存驱动接口：文件、Redis 及未来接入的后端统一实现该接口
//
// 约定：键由 Driver 层命名（前缀 + Hash），驱动按原名持久化；
// Set 的 value 须可 JSON 序列化，expired <= 0 表示永不过期；
// 返回值 bool 仅表示操作是否成功，Get 未命中返回 nil。
type Store interface {
	// Has - 判断缓存是否存在（过期视为不存在）
	Has(key string) (ok bool)
	// Get - 获取缓存（未命中或已过期返回 nil）
	Get(key string) (value any)
	// Set - 设置缓存（expired <= 0 表示永不过期）
	Set(key string, value any, expired time.Duration) (ok bool)
	// Delete - 删除缓存
	Delete(key ...string) (ok bool)
	// Clear - 清空缓存
	Clear() (ok bool)
}

// Factory - 驱动工厂：按配置构建驱动实例（传入的 Config 已归一化）
type Factory func(config Config) (Store, error)

// registry - 驱动注册表（读写锁保护并发注册与查找）
// 内置驱动在变量初始化时登记，保证包初始化期间即可用；外部驱动通过 Register 注册
var registry = struct {
	sync.RWMutex
	items map[string]Factory
}{items: map[string]Factory{
	"file":  newFileStore,
	"redis": newRedisStore,
}}

// Register - 注册缓存驱动
/**
 * @param name    string  - 驱动名称（不区分大小写，同名后注册覆盖先注册）
 * @param factory Factory - 驱动工厂
 * @example：
 * 	func init() { cachex.Register("memory", newMemoryStore) }
 */
func Register(name string, factory Factory) {
	name = strings.ToLower(strings.TrimSpace(name))
	if utils.Is.Empty(name) {
		panic("cachex: 驱动名称不能为空")
	}
	if factory == nil {
		panic("cachex: 驱动[" + name + "]工厂不能为空")
	}
	registry.Lock()
	registry.items[name] = factory
	registry.Unlock()
}

// registered - 驱动是否已注册
func registered(name string) bool {
	registry.RLock()
	defer registry.RUnlock()
	_, ok := registry.items[name]
	return ok
}

// Names - 已注册的驱动名称列表（有序）
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

// open - 按名称构建驱动实例（内部使用，不补齐默认值）
func open(name string, config Config) (Store, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	registry.RLock()
	factory, ok := registry.items[name]
	registry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("cachex: 未注册的驱动[%s]（可用: %s）", name, strings.Join(Names(), ", "))
	}
	return factory(config)
}

// New - 按驱动名称与配置创建链式缓存实例
/**
 * @param name   string - 驱动名称（file / redis / 自定义注册名）
 * @param config Config - 缓存配置
 * @example：
 * 	driver, err := cachex.New("redis", cachex.Config{...})
 * 	ok := driver.Expired(5 * time.Minute).Set("code", "123456")
 */
func New(name string, config Config) (Driver, error) {
	conf := normConfig(config)
	store, err := open(name, conf)
	if err != nil {
		return Driver{}, err
	}
	ctx := defaultContext(name, conf)
	return NewDriver(store, ctx.prefix, ctx.expired), nil
}

// ================================== 接口与注册表 - 结束 ==================================

// ================================== 链式缓存实例 - 开始 ==================================

// Driver - 链式缓存实例：在 Store 之上提供链式上下文（值语义，每次调用返回副本）
type Driver struct {
	// 底层缓存驱动
	store Store
	// 键名前缀
	prefix string
	// 默认过期时间
	expired time.Duration
	// 累积的键名（Delete 时一并删除）
	keys []string
	// 累积的标签（Set 时簿记成员，Delete 时按标签删除）
	tags []string
}

// NewDriver - 用 Store 包装出链式缓存实例
/**
 * @param store   Store         - 底层缓存驱动
 * @param prefix  string        - 键名前缀
 * @param expired time.Duration - 默认过期时间（<= 0 表示永不过期）
 */
func NewDriver(store Store, prefix string, expired time.Duration) Driver {
	return Driver{store: store, prefix: prefix, expired: expired}
}

// Store - 取出底层缓存驱动
func (this Driver) Store() Store {
	return this.store
}

// Tag - 标签（Set 时把键簿记到标签下，Delete 时按标签整组删除）
/**
 * @example：
 * 	cachex.Cache.Tag("user").Set("name", "张三")
 * 	cachex.Cache.Tag("user").Delete() // 删除该标签下所有缓存
 */
func (this Driver) Tag(tag ...string) Driver {
	tags := make([]string, 0, len(this.tags)+len(tag))
	tags = append(tags, this.tags...)
	tags = append(tags, tag...)
	this.tags = cast.ToStringSlice(utils.ArrayUnique(tags))
	return this
}

// Key - 键（Delete 时一并删除）
func (this Driver) Key(key ...string) Driver {
	keys := make([]string, 0, len(this.keys)+len(key))
	keys = append(keys, this.keys...)
	for _, item := range key {
		keys = append(keys, this.name(item))
	}
	this.keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	return this
}

// Expired - 过期时间（支持 time.Duration、duration 字符串（如 "5s", "1m"）、数值（按秒））
func (this Driver) Expired(second any) Driver {
	this.expired = parseExpired(second)
	return this
}

// Has - 判断缓存是否存在
func (this Driver) Has(key string) (ok bool) {
	if this.store == nil || utils.Is.Empty(key) {
		return false
	}
	return this.store.Has(this.name(key))
}

// Get - 获取缓存
func (this Driver) Get(key string) (value any) {
	if this.store == nil || utils.Is.Empty(key) {
		return nil
	}
	return this.store.Get(this.name(key))
}

// Set - 设置缓存
func (this Driver) Set(key string, value any) (ok bool) {
	if this.store == nil || utils.Is.Empty(key) {
		return false
	}
	name := this.name(key)
	if !this.store.Set(name, value, this.expired) {
		return false
	}
	// 簿记标签成员
	this.setTags(name)
	return true
}

// Delete - 删除缓存（链式累积的键、传入的键、各标签下的成员一并删除）
func (this Driver) Delete(key ...string) (ok bool) {
	if this.store == nil {
		return false
	}

	keys := make([]string, 0, len(this.keys)+len(key))
	keys = append(keys, this.keys...)
	for _, item := range key {
		if !utils.Is.Empty(item) {
			keys = append(keys, this.name(item))
		}
	}

	// 根据标签删除成员与标签列表
	this.delTags()

	keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	if len(keys) == 0 {
		return true
	}
	return this.store.Delete(keys...)
}

// Clear - 清空缓存
func (this Driver) Clear() (ok bool) {
	if this.store == nil {
		return false
	}
	return this.store.Clear()
}

// name - 缓存键命名规则：前缀 + key 的 MD5 前 16 位（64 位，碰撞概率远低于旧的 32 位哈希）
// 注意：与 Sum32 时代的旧键不兼容，旧键随默认过期时间自然淘汰
func (this Driver) name(key string) string {
	return fmt.Sprintf("%s-%s", this.prefix, utils.Hash.Token(key, 16))
}

// tagName - 标签列表键命名规则：前缀-TAG-大写标签名（与历史数据保持兼容）
func (this Driver) tagName(tag string) string {
	return fmt.Sprintf("%s-TAG-%s", this.prefix, strings.ToUpper(tag))
}

// tagLocks - 标签簿记键控锁：同一标签名的累积簿记串行化，避免并发下读-改-写丢失成员
var tagLocks sync.Map // map[string]*sync.Mutex

// tagLock - 取标签簿记键名对应的锁
func tagLock(name string) *sync.Mutex {
	lock, _ := tagLocks.LoadOrStore(name, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// setTags - 把成员键写入各标签的成员列表（标签列表永不过期）
func (this Driver) setTags(member string) {
	for _, tag := range this.tags {
		name := this.tagName(tag)
		lock := tagLock(name)
		lock.Lock()
		var keys []string
		if existing := this.store.Get(name); existing != nil {
			keys = cast.ToStringSlice(existing)
		}
		keys = append(keys, member)
		keys = cast.ToStringSlice(utils.ArrayUnique(keys))
		this.store.Set(name, keys, 0)
		lock.Unlock()
	}
}

// delTags - 删除各标签下的全部成员键与标签列表
func (this Driver) delTags() {
	for _, tag := range this.tags {
		name := this.tagName(tag)
		lock := tagLock(name)
		lock.Lock()
		existing := this.store.Get(name)
		if existing == nil {
			lock.Unlock()
			continue
		}
		if keys := cast.ToStringSlice(existing); len(keys) > 0 {
			this.store.Delete(keys...)
		}
		this.store.Delete(name)
		lock.Unlock()
	}
}

// ================================== 链式缓存实例 - 结束 ==================================

// parseExpired - 解析过期时间：支持 time.Duration、duration 字符串（如 "5s", "1m"）、数值（按秒）
func parseExpired(value any) time.Duration {
	switch item := value.(type) {
	case time.Duration:
		return item
	case string:
		// 尝试解析为 duration 字符串（例如 `5s`, `1m`）
		if d, err := time.ParseDuration(item); err == nil {
			return d
		}
		if i := cast.ToInt64(item); i > 0 {
			return time.Duration(i) * time.Second
		}
		return cast.ToDuration(item)
	default:
		// 对于数值类型，按秒处理；对于其他类型，尽量转换为 duration
		if cast.ToInt64(value) > 0 {
			return time.Duration(cast.ToInt64(value)) * time.Second
		}
		return cast.ToDuration(value)
	}
}

// storeError - 初始化失败的驱动占位：所有操作返回失败
type storeError struct {
	// 驱动名称
	name string
	// 初始化错误
	err error
}

// Has - 占位实现，返回 false
func (this storeError) Has(string) bool { return false }

// Get - 占位实现，返回 nil
func (this storeError) Get(string) any { return nil }

// Set - 占位实现，返回 false
func (this storeError) Set(string, any, time.Duration) bool { return false }

// Delete - 占位实现，返回 false
func (this storeError) Delete(...string) bool { return false }

// Clear - 占位实现，返回 false
func (this storeError) Clear() bool { return false }

// 编译期接口校验
var _ Store = storeError{}
