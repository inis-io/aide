package cachex

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

// fakeStore - 测试用驱动：内存 map 记录写入，不触发任何网络与磁盘请求
type fakeStore struct {
	// 写入记录：键 -> 值与过期时间
	items map[string]fakeItem
}

// fakeItem - 写入记录项
type fakeItem struct {
	// 缓存值
	value any
	// 过期时间
	expired time.Duration
}

// Has - 判断键是否存在
func (this *fakeStore) Has(key string) bool {
	_, ok := this.items[key]
	return ok
}

// Get - 获取键值
func (this *fakeStore) Get(key string) any {
	item, ok := this.items[key]
	if !ok {
		return nil
	}
	return item.value
}

// Set - 记录键值与过期时间
func (this *fakeStore) Set(key string, value any, expired time.Duration) bool {
	this.items[key] = fakeItem{value: value, expired: expired}
	return true
}

// Delete - 删除键
func (this *fakeStore) Delete(key ...string) bool {
	for _, item := range key {
		delete(this.items, item)
	}
	return true
}

// Clear - 清空全部记录
func (this *fakeStore) Clear() bool {
	this.items = map[string]fakeItem{}
	return true
}

// Incr - 原子自增 1（测试实现：仅首次写入记录过期时间）
func (this *fakeStore) Incr(key string, expired time.Duration) (int64, error) {
	item, ok := this.items[key]
	if !ok {
		this.items[key] = fakeItem{value: int64(1), expired: expired}
		return 1, nil
	}
	count := cast.ToInt64(item.value) + 1
	item.value = count
	this.items[key] = item
	return count, nil
}

// SetNX - 仅当键不存在时设置（测试实现）
func (this *fakeStore) SetNX(key string, value any, expired time.Duration) (bool, error) {
	if _, ok := this.items[key]; ok {
		return false, nil
	}
	this.items[key] = fakeItem{value: value, expired: expired}
	return true, nil
}

// TTL - 剩余存活秒数（测试实现：不存在返回 0，永不过期返回 -1）
func (this *fakeStore) TTL(key string) (int64, error) {
	item, ok := this.items[key]
	if !ok {
		return 0, nil
	}
	if item.expired <= 0 {
		return -1, nil
	}
	return int64(item.expired / time.Second), nil
}

// registerFake - 注册一个测试驱动并返回其实例（同名覆盖，互不影响）
func registerFake(name string) *fakeStore {
	fake := &fakeStore{items: map[string]fakeItem{}}
	Register(name, func(config Config) (Store, error) {
		return fake, nil
	})
	return fake
}

// TestRegisterAndNew - 验证注册表：内置驱动登记在册、列表有序、注册后能按名称创建、未注册名称报错且提示可用列表
func TestRegisterAndNew(t *testing.T) {

	registerFake("mock")

	names := Names()
	if !sort.StringsAreSorted(names) {
		t.Fatalf("Names() 应返回有序列表，实际: %v", names)
	}

	// 内置驱动应在变量初始化时登记，无需任何 init 调用
	for _, want := range []string{"file", "redis", "mock"} {
		if !contains(names, want) {
			t.Fatalf("驱动[%s]应出现在 Names() 中，实际: %v", want, names)
		}
	}

	if _, err := New("mock", Config{}); err != nil {
		t.Fatalf("按已注册名称创建驱动不应报错: %v", err)
	}

	// 未注册名称应报错，且错误中提示可用驱动列表
	if _, err := New("not-exists", Config{}); err == nil || !strings.Contains(err.Error(), "可用") {
		t.Fatalf("未注册的驱动名称应返回带可用列表的错误，实际: %v", err)
	}
}

// contains - 判断字符串切片是否包含目标值
func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

// TestNormConfig - 验证配置归一化：默认值补齐、未知引擎回退、Hash 生成
func TestNormConfig(t *testing.T) {

	conf := normConfig(Config{})

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"引擎默认值", conf.Engine, "file"},
		{"Redis主机默认值", conf.Redis.Host, "127.0.0.1"},
		{"Redis端口默认值", conf.Redis.Port, 6379},
		{"Redis过期默认值", conf.Redis.Expired, 7200},
		{"Redis前缀默认值", conf.Redis.Prefix, "AIDE"},
		{"文件根目录默认值", conf.File.Root, "./runtime/cache"},
		{"文件后缀默认值", conf.File.Suffix, "json"},
		{"文件过期默认值", conf.File.Expired, 7200},
		{"文件前缀默认值", conf.File.Prefix, "AIDE"},
	}
	for _, item := range cases {
		if item.got != item.want {
			t.Errorf("%s: 期望 %v，实际 %v", item.name, item.want, item.got)
		}
	}

	if conf.Hash == "" {
		t.Error("Hash 应自动生成")
	}

	// 未注册的引擎名称应回退到默认值
	conf = normConfig(Config{Engine: "unknown"})
	if conf.Engine != "file" {
		t.Errorf("未知引擎应回退默认值，实际: %s", conf.Engine)
	}
}

// TestParseExpired - 验证过期时间解析：Duration、duration 字符串、数值按秒
func TestParseExpired(t *testing.T) {

	cases := []struct {
		name  string
		input any
		want  time.Duration
	}{
		{"Duration 类型", 5 * time.Second, 5 * time.Second},
		{"duration 字符串", "1m", time.Minute},
		{"数字字符串按秒", "60", 60 * time.Second},
		{"整数按秒", 60, 60 * time.Second},
		{"int64 按秒", int64(10), 10 * time.Second},
		{"非法字符串", "abc", 0},
	}
	for _, item := range cases {
		if got := parseExpired(item.input); got != item.want {
			t.Errorf("%s: 期望 %v，实际 %v", item.name, item.want, got)
		}
	}
}

// TestDriverChain - 验证链式实例：值语义上下文隔离、链式参数透传
func TestDriverChain(t *testing.T) {

	fake := registerFake("mock")

	base := NewDriver(fake, "TEST", time.Hour)
	child := base.Tag("a", "b").Key("k1").Expired(60)

	// 值语义：链式调用不影响原实例
	if len(base.tags) != 0 || len(base.keys) != 0 || base.expired != time.Hour {
		t.Fatal("链式调用不应修改原实例的上下文")
	}

	if !child.Set("x", "1") {
		t.Fatal("链式设置缓存不应失败")
	}

	// 过期时间应透传到底层驱动
	if item := fake.items[child.name("x")]; item.expired != time.Minute {
		t.Fatalf("链式过期时间应透传，实际: %v", item.expired)
	}

	// 底层驱动为 nil 时各操作应安全返回失败
	var empty Driver
	if empty.Set("x", "1") || empty.Has("x") || empty.Get("x") != nil || empty.Delete("x") || empty.Clear() {
		t.Fatal("底层驱动为空时应返回失败")
	}
}

// TestDriverCrud - 验证链式实例的增删查改与清空
func TestDriverCrud(t *testing.T) {

	fake := registerFake("mock")
	driver := NewDriver(fake, "TEST", time.Hour)

	if !driver.Set("foo", map[string]any{"a": 1}) {
		t.Fatal("Set 不应失败")
	}
	if !driver.Has("foo") {
		t.Fatal("Set 后 Has 应为 true")
	}
	if got := driver.Get("foo"); cast.ToInt(cast.ToStringMap(got)["a"]) != 1 {
		t.Fatalf("Get 应返回写入的值，实际: %+v", got)
	}

	driver.Delete("foo")
	if driver.Has("foo") {
		t.Fatal("Delete 后 Has 应为 false")
	}

	// Key 链式累积的键应随 Delete 一并删除
	driver.Set("a", 1)
	driver.Set("b", 2)
	driver.Key("a", "b").Delete()
	if driver.Has("a") || driver.Has("b") {
		t.Fatal("Key 累积的键应一并删除")
	}

	// Clear 应清空全部
	driver.Set("c", 3)
	driver.Clear()
	if driver.Has("c") {
		t.Fatal("Clear 后缓存应清空")
	}
}

// TestDriverTags - 验证标签簿记：成员累积（旧版 Redis 标签会被覆盖的回归）、按标签整组删除
func TestDriverTags(t *testing.T) {

	fake := registerFake("mock")
	driver := NewDriver(fake, "AIDE", time.Hour)

	driver.Tag("user").Set("k1", "v1")
	driver.Tag("user").Set("k2", "v2")

	// 标签成员列表应累积两个成员（旧版实现每次覆盖只剩最后一个）
	members := cast.ToStringSlice(fake.Get("AIDE-TAG-USER"))
	if len(members) != 2 || !contains(members, driver.name("k1")) || !contains(members, driver.name("k2")) {
		t.Fatalf("标签成员应累积，实际: %v", members)
	}

	// 同一成员重复写入不应重复簿记
	driver.Tag("user").Set("k1", "v1-again")
	if members = cast.ToStringSlice(fake.Get("AIDE-TAG-USER")); len(members) != 2 {
		t.Fatalf("同一成员不应重复簿记，实际: %v", members)
	}

	// 按标签删除：成员与标签列表一并删除
	driver.Tag("user").Delete()
	if driver.Has("k1") || driver.Has("k2") {
		t.Fatal("按标签删除后成员应全部移除")
	}
	if fake.Has("AIDE-TAG-USER") {
		t.Fatal("按标签删除后标签列表应移除")
	}
}

// TestFileStore - 验证文件驱动：基于内存文件系统的真实读写、过期语义、落盘命名
func TestFileStore(t *testing.T) {

	store := &FileStore{Fs: afero.NewMemMapFs(), Config: FileConfig{Root: "cache", Suffix: "json"}}

	// expired <= 0 表示永不过期
	if !store.Set("k", "v", 0) {
		t.Fatal("Set 不应失败")
	}
	if !store.Has("k") || store.Get("k") != "v" {
		t.Fatal("Set 后应能读到写入的值")
	}

	// 落盘文件应追加后缀（与历史格式一致）
	if exist, _ := afero.Exists(store.Fs, "cache/k.json"); !exist {
		t.Fatal("落盘文件应为 键名.后缀 格式")
	}

	// 对象值应 JSON 往返
	store.Set("obj", map[string]any{"a": 1}, time.Hour)
	if got := store.Get("obj"); cast.ToInt(cast.ToStringMap(got)["a"]) != 1 {
		t.Fatalf("对象值应 JSON 往返，实际: %+v", got)
	}

	// 过期后视为不存在（落盘为秒级时间戳，等待需越过秒边界）
	store.Set("e", "v", time.Second)
	time.Sleep(2100 * time.Millisecond)
	if store.Has("e") || store.Get("e") != nil {
		t.Fatal("过期缓存应视为不存在")
	}

	// 删除与清空
	store.Delete("k")
	if store.Has("k") {
		t.Fatal("Delete 后缓存应移除")
	}
	if !store.Clear() || store.Has("obj") {
		t.Fatal("Clear 后缓存应清空")
	}
	if exist, _ := afero.Exists(store.Fs, "cache"); !exist {
		t.Fatal("Clear 后应重建根目录")
	}
}

// TestControllerInitAndReload - 验证控制器：配置注入后全局实例生效，Hash 变化时热重载
func TestControllerInitAndReload(t *testing.T) {

	fake := registerFake("mock")

	inst := &Controller{}
	inst.Init(Config{Engine: "mock"})

	if !inst.HasConfig {
		t.Fatal("注入配置后 HasConfig 应为 true")
	}

	// 全局链式实例应能读写
	if !Cache.Set("foo", "bar") || Cache.Get("foo") != "bar" {
		t.Fatal("全局实例应路由到已注册驱动")
	}
	if !fake.Has(Cache.name("foo")) {
		t.Fatal("全局实例底层应为已注册驱动")
	}

	// Hash 未变化时不重载，变化后才重载
	hashBefore := inst.Hash
	inst.ReloadIfChanged()
	if inst.Hash != hashBefore {
		t.Fatal("配置未变化时不应重载")
	}

	other := registerFake("mock2")
	inst.ReloadIfChanged(Config{Engine: "mock2"})
	if !Cache.Set("x", "y") || !other.Has(Cache.name("x")) {
		t.Fatal("配置变化后应重载到新注册的驱动")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}

// TestStoreError - 验证驱动初始化失败的占位实现：所有操作返回失败
func TestStoreError(t *testing.T) {

	Register("broken", func(config Config) (Store, error) {
		return nil, errors.New("连接失败")
	})

	inst := &Controller{}
	inst.Init(Config{Engine: "broken"})

	if Cache.Set("x", "y") || Cache.Has("x") || Cache.Get("x") != nil || Cache.Delete("x") || Cache.Clear() {
		t.Fatal("初始化失败的驱动应所有操作返回失败")
	}

	// 原子方法应返回初始化错误（fail-closed 场景依赖该错误感知后端故障）
	if _, err := Cache.Incr("x"); err == nil {
		t.Fatal("初始化失败的驱动 Incr 应返回错误")
	}
	if _, err := Cache.SetNX("x", 1); err == nil {
		t.Fatal("初始化失败的驱动 SetNX 应返回错误")
	}
	if _, err := Cache.TTL("x"); err == nil {
		t.Fatal("初始化失败的驱动 TTL 应返回错误")
	}

	inst.HasConfig = false
	inst.useDefault()
}

// TestDriverTagsConcurrent - 验证并发下同标签簿记不丢成员（键控锁回归测试）
func TestDriverTagsConcurrent(t *testing.T) {

	store := &FileStore{Fs: afero.NewMemMapFs(), Config: FileConfig{Root: "cache", Suffix: "json"}}
	driver := NewDriver(store, "AIDE", 7200*time.Second)

	const total = 20
	var group sync.WaitGroup
	for i := 0; i < total; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			if !driver.Tag("user").Set(fmt.Sprintf("k%d", i), i) {
				t.Errorf("并发写入 k%d 失败", i)
			}
		}(i)
	}
	group.Wait()

	// 标签成员列表应完整记录全部 20 个键
	members := cast.ToStringSlice(store.Get("AIDE-TAG-USER"))
	if len(members) != total {
		t.Fatalf("标签成员数应为 %d，实际 %d：%v", total, len(members), members)
	}

	// 按标签删除后成员键与标签列表应一并清除
	if !driver.Tag("user").Delete() {
		t.Fatal("按标签删除失败")
	}
	if store.Get("AIDE-TAG-USER") != nil || store.Has(driver.name("k0")) {
		t.Fatal("按标签删除后成员与标签列表应已清除")
	}
}


// TestDriverIncrSetNxTtl - 验证链式实例的原子方法：自增序列、过期时间透传、占位不覆盖、存活查询、空驱动返回错误
func TestDriverIncrSetNxTtl(t *testing.T) {

	fake := registerFake("mock")
	driver := NewDriver(fake, "TEST", time.Minute)

	// 自增序列与过期时间透传（仅首次写入记录）
	for want := int64(1); want <= 3; want++ {
		count, err := driver.Incr("counter")
		if err != nil || count != want {
			t.Fatalf("自增应为 %d，实际: %d, err=%v", want, count, err)
		}
	}
	if item := fake.items[driver.name("counter")]; item.expired != time.Minute {
		t.Fatalf("链式过期时间应透传到 Incr，实际: %v", item.expired)
	}

	// SetNX：已存在不覆盖，不存在则写入
	if ok, _ := driver.SetNX("counter", 99); ok {
		t.Fatal("键已存在时 SetNX 应返回 false")
	}
	if got := cast.ToInt64(fake.items[driver.name("counter")].value); got != 3 {
		t.Fatalf("SetNX 不应覆盖已有值，实际: %d", got)
	}
	if ok, _ := driver.SetNX("fresh", 1); !ok {
		t.Fatal("键不存在时 SetNX 应返回 true")
	}

	// TTL：有窗口返回剩余秒，永不过期返回 -1，不存在返回 0
	if seconds, _ := driver.TTL("counter"); seconds != 60 {
		t.Fatalf("TTL 应返回窗口秒数，实际: %d", seconds)
	}
	forever := NewDriver(fake, "TEST", 0)
	if _, err := forever.SetNX("forever", 1); err != nil {
		t.Fatal(err)
	}
	if seconds, _ := forever.TTL("forever"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}
	if seconds, _ := driver.TTL("missing"); seconds != 0 {
		t.Fatalf("不存在的键 TTL 应为 0，实际: %d", seconds)
	}

	// 底层驱动为 nil 时应返回错误
	var empty Driver
	if _, err := empty.Incr("x"); err == nil {
		t.Fatal("底层驱动为空时 Incr 应返回错误")
	}
	if _, err := empty.SetNX("x", 1); err == nil {
		t.Fatal("底层驱动为空时 SetNX 应返回错误")
	}
	if _, err := empty.TTL("x"); err == nil {
		t.Fatal("底层驱动为空时 TTL 应返回错误")
	}
}

// TestFileStoreAtomic - 验证文件驱动的原子方法：固定窗口自增、过期时间保留、SetNX、TTL 映射、过期重计
func TestFileStoreAtomic(t *testing.T) {

	store := &FileStore{Fs: afero.NewMemMapFs(), Config: FileConfig{Root: "cache", Suffix: "json"}}

	// 固定窗口：首次自增写入过期时间，后续自增保留原时间戳
	if count, _ := store.Incr("c", 10*time.Minute); count != 1 {
		t.Fatalf("首次自增应为 1，实际: %d", count)
	}
	row, _ := store.read(store.dest("c"))
	if count, _ := store.Incr("c", time.Hour); count != 2 {
		t.Fatalf("第二次自增应为 2，实际: %d", count)
	}
	row2, _ := store.read(store.dest("c"))
	if row.Expired != row2.Expired {
		t.Fatalf("后续自增不应改写过期时间戳: %d → %d", row.Expired, row2.Expired)
	}

	// TTL：窗口内返回剩余秒（秒级精度，容差 2 秒），永不过期返回 -1，不存在返回 0
	if seconds, _ := store.TTL("c"); seconds < 598 || seconds > 600 {
		t.Fatalf("TTL 应接近 600 秒，实际: %d", seconds)
	}
	store.Set("f", "v", 0)
	if seconds, _ := store.TTL("f"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}
	if seconds, _ := store.TTL("none"); seconds != 0 {
		t.Fatalf("不存在的键 TTL 应为 0，实际: %d", seconds)
	}

	// SetNX：已存在不覆盖，不存在则写入
	if ok, _ := store.SetNX("c", 99, time.Minute); ok {
		t.Fatal("键已存在时 SetNX 应返回 false")
	}
	if count, _ := store.Incr("c", 0); count != 3 {
		t.Fatalf("SetNX 不应覆盖已有值，实际计数: %d", count)
	}
	if ok, _ := store.SetNX("n", 1, time.Minute); !ok {
		t.Fatal("键不存在时 SetNX 应返回 true")
	}

	// 窗口过期后从 1 重新计数（落盘为秒级时间戳，等待需越过秒边界）
	if _, err := store.Incr("e", time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	if count, _ := store.Incr("e", 10*time.Minute); count != 1 {
		t.Fatalf("窗口过期后应重新从 1 计数，实际: %d", count)
	}
}

// TestFileStoreIncrConcurrent - 验证并发自增计数准确（互斥锁回归测试）
func TestFileStoreIncrConcurrent(t *testing.T) {

	store := &FileStore{Fs: afero.NewMemMapFs(), Config: FileConfig{Root: "cache", Suffix: "json"}}

	const goroutines = 50
	const each = 4
	var group sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < each; j++ {
				if _, err := store.Incr("c", 10*time.Minute); err != nil {
					t.Errorf("并发自增失败: %v", err)
				}
			}
		}()
	}
	group.Wait()

	count, _ := store.Incr("c", 0)
	if want := int64(goroutines*each + 1); count != want {
		t.Fatalf("并发自增计数应准确为 %d，实际: %d", want, count)
	}
}

// TestRedisStoreAtomic - 验证 Redis 驱动的原子方法（miniredis 进程内实例，不触网；Incr 走 Lua 脚本路径）
func TestRedisStoreAtomic(t *testing.T) {

	server := miniredis.RunT(t)

	store := &RedisStore{
		Client: redis.NewClient(&redis.Options{Addr: server.Addr()}),
		Config: RedisConfig{Host: "127.0.0.1", Port: cast.ToInt(server.Port())},
	}

	// 自增序列（Lua 脚本原子执行）
	for want := int64(1); want <= 3; want++ {
		count, err := store.Incr("c", 10*time.Minute)
		if err != nil || count != want {
			t.Fatalf("自增应为 %d，实际: %d, err=%v", want, count, err)
		}
	}
	// 首次自增已写入过期时间
	if seconds, _ := store.TTL("c"); seconds < 598 || seconds > 600 {
		t.Fatalf("TTL 应接近 600 秒，实际: %d", seconds)
	}
	// 永不过期返回 -1
	if _, err := store.Incr("f", 0); err != nil {
		t.Fatal(err)
	}
	if seconds, _ := store.TTL("f"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}
	// 不存在返回 0
	if seconds, _ := store.TTL("none"); seconds != 0 {
		t.Fatalf("不存在的键 TTL 应为 0，实际: %d", seconds)
	}

	// SetNX：已存在不覆盖、不续期，不存在则写入
	if ok, _ := store.SetNX("c", 99, time.Hour); ok {
		t.Fatal("键已存在时 SetNX 应返回 false")
	}
	if got := cast.ToInt64(store.Get("c")); got != 3 {
		t.Fatalf("SetNX 不应覆盖已有值，实际: %d", got)
	}
	if ok, _ := store.SetNX("n", 1, 10*time.Minute); !ok {
		t.Fatal("键不存在时 SetNX 应返回 true")
	}
}
