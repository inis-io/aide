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
	for _, want := range []string{"file", "memory", "redis", "layered", "mock"} {
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
		{"内存最大条目默认值", conf.Memory.MaxEntries, int64(10000)},
		{"内存过期默认值", conf.Memory.Expired, 7200},
		{"内存前缀默认值", conf.Memory.Prefix, "AIDE"},
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

	// defaultContext：按引擎取对应分段的前缀与过期时间
	ctx := defaultContext("memory", conf)
	if ctx.prefix != "AIDE" || ctx.expired != 7200*time.Second {
		t.Errorf("memory 段应取默认前缀与过期，实际: %+v", ctx)
	}
	custom := normConfig(Config{Memory: MemoryConfig{Prefix: "MEM", Expired: 60}})
	ctx = defaultContext("memory", custom)
	if ctx.prefix != "MEM" || ctx.expired != time.Minute {
		t.Errorf("memory 段应取自定义前缀与过期，实际: %+v", ctx)
	}
	// layered 复用 Memory 段（两层共用 Driver 命名的同一键）
	ctx = defaultContext("layered", custom)
	if ctx.prefix != "MEM" || ctx.expired != time.Minute {
		t.Errorf("layered 段应复用 memory 段配置，实际: %+v", ctx)
	}
	ctx = defaultContext("unknown", conf)
	if ctx.prefix != conf.File.Prefix || ctx.expired != time.Duration(conf.File.Expired)*time.Second {
		t.Errorf("未知引擎应回退 file 段，实际: %+v", ctx)
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

// newMemory - 构建内存驱动测试实例（自动注册清理时关闭，避免测试进程泄漏 ristretto goroutine）
func newMemory(t *testing.T, conf MemoryConfig) *MemoryStore {
	t.Helper()
	// 工厂契约要求配置已归一化（与 setActive/New 的调用路径一致）
	store, err := newMemoryStore(normConfig(Config{Memory: conf}))
	if err != nil {
		t.Fatalf("构建内存驱动失败: %v", err)
	}
	t.Cleanup(func() { _ = store.(*MemoryStore).Close() })
	return store.(*MemoryStore)
}

// TestMemoryStore - 验证内存驱动：读写、类型保留、TTL 三态、过期、删除清空、Close 幂等与关闭后行为
func TestMemoryStore(t *testing.T) {

	store := newMemory(t, MemoryConfig{})

	// 永不过期读写
	if !store.Set("k", "v", 0) {
		t.Fatal("Set 不应失败")
	}
	if !store.Has("k") || store.Get("k") != "v" {
		t.Fatal("Set 后应能读到写入的值")
	}

	// 类型保留：内存驱动不经 JSON 往返，int64 不应变成 float64
	store.Set("n", int64(42), 0)
	if got, ok := store.Get("n").(int64); !ok || got != 42 {
		t.Fatalf("内存驱动应保留原始类型，实际: %+v", store.Get("n"))
	}

	// TTL 三态：永不过期 -1、带窗口 >0、不存在 0
	if seconds, _ := store.TTL("k"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}
	store.Set("e", "v", 2*time.Second)
	if seconds, _ := store.TTL("e"); seconds <= 0 || seconds > 2 {
		t.Fatalf("带窗口的键 TTL 应在 (0,2] 秒内，实际: %d", seconds)
	}
	if seconds, _ := store.TTL("none"); seconds != 0 {
		t.Fatalf("不存在的键 TTL 应为 0，实际: %d", seconds)
	}

	// 过期后视为不存在（短 TTL + 睡眠，Get 惰性判定过期）
	store.Set("x", "v", 100*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if store.Has("x") || store.Get("x") != nil {
		t.Fatal("过期缓存应视为不存在")
	}

	// 删除与清空
	store.Delete("k")
	if store.Has("k") {
		t.Fatal("Delete 后缓存应移除")
	}
	store.Set("c", 1, 0)
	if !store.Clear() || store.Has("c") {
		t.Fatal("Clear 后缓存应清空")
	}

	// Close 幂等：连续两次不报错；关闭后读写全部失败
	if err := store.Close(); err != nil {
		t.Fatalf("首次 Close 不应报错: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等: %v", err)
	}
	if store.Set("a", "b", 0) || store.Has("a") || store.Get("a") != nil {
		t.Fatal("关闭后所有读写应返回失败")
	}
}

// TestMemoryStoreAtomic - 验证内存驱动原子方法：固定窗口自增、过期保留、SetNX 不覆盖不续期、TTL 映射、过期重计
func TestMemoryStoreAtomic(t *testing.T) {

	store := newMemory(t, MemoryConfig{})

	// 固定窗口：首次自增写入过期时间，后续自增保留原时间戳
	if count, _ := store.Incr("c", 10*time.Minute); count != 1 {
		t.Fatalf("首次自增应为 1，实际: %d", count)
	}
	if count, _ := store.Incr("c", time.Hour); count != 2 {
		t.Fatalf("第二次自增应为 2，实际: %d", count)
	}
	if seconds, _ := store.TTL("c"); seconds < 598 || seconds > 600 {
		t.Fatalf("后续自增不应改写过期时间，TTL 应接近 600 秒，实际: %d", seconds)
	}

	// 永不过期返回 -1
	if count, _ := store.Incr("f", 0); count != 1 {
		t.Fatalf("永不过期自增应为 1，实际: %d", count)
	}
	if seconds, _ := store.TTL("f"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}

	// SetNX：已存在不覆盖、不续期，不存在则写入
	store.Set("k", "v", 10*time.Minute)
	if ok, _ := store.SetNX("k", "v2", time.Second); ok {
		t.Fatal("键已存在时 SetNX 应返回 false")
	}
	if store.Get("k") != "v" {
		t.Fatalf("SetNX 不应覆盖已有值，实际: %+v", store.Get("k"))
	}
	if seconds, _ := store.TTL("k"); seconds < 598 || seconds > 600 {
		t.Fatalf("SetNX 不应续期，TTL 应接近 600 秒，实际: %d", seconds)
	}
	if ok, _ := store.SetNX("n", 1, time.Minute); !ok {
		t.Fatal("键不存在时 SetNX 应返回 true")
	}

	// 窗口过期后从 1 重新计数
	if count, _ := store.Incr("e", time.Second); count != 1 {
		t.Fatalf("过期窗口自增应为 1，实际: %d", count)
	}
	time.Sleep(2100 * time.Millisecond)
	if count, _ := store.Incr("e", 10*time.Minute); count != 1 {
		t.Fatalf("窗口过期后应重新从 1 计数，实际: %d", count)
	}
}

// TestMemoryStoreIncrConcurrent - 验证内存驱动并发自增：同键计数准确（分段锁同键串行）、异键互不错串
func TestMemoryStoreIncrConcurrent(t *testing.T) {

	store := newMemory(t, MemoryConfig{})

	// 同键并发：50 goroutine × 4 次，最终计数应精确
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
	if count, _ := store.Incr("c", 0); count != goroutines*each+1 {
		t.Fatalf("同键并发自增计数应准确为 %d，实际: %d", goroutines*each+1, count)
	}

	// 异键并发：30 goroutine 各自增 a × 5、b × 7，两个键的计数互不错串
	var group2 sync.WaitGroup
	for i := 0; i < 30; i++ {
		group2.Add(2)
		go func() {
			defer group2.Done()
			for j := 0; j < 5; j++ {
				_, _ = store.Incr("a", time.Minute)
			}
		}()
		go func() {
			defer group2.Done()
			for j := 0; j < 7; j++ {
				_, _ = store.Incr("b", time.Minute)
			}
		}()
	}
	group2.Wait()
	if count, _ := store.Incr("a", 0); count != 30*5+1 {
		t.Fatalf("键 a 计数应准确为 %d，实际: %d", 30*5+1, count)
	}
	if count, _ := store.Incr("b", 0); count != 30*7+1 {
		t.Fatalf("键 b 计数应准确为 %d，实际: %d", 30*7+1, count)
	}
}

// TestFileStoreIncrConcurrentStripes - 验证文件驱动分段锁：异键并发自增互不错串（分段锁回归测试）
func TestFileStoreIncrConcurrentStripes(t *testing.T) {

	store := &FileStore{Fs: afero.NewMemMapFs(), Config: FileConfig{Root: "cache", Suffix: "json"}}

	var group sync.WaitGroup
	for i := 0; i < 30; i++ {
		group.Add(2)
		go func() {
			defer group.Done()
			for j := 0; j < 5; j++ {
				if _, err := store.Incr("a", time.Minute); err != nil {
					t.Errorf("键 a 并发自增失败: %v", err)
				}
			}
		}()
		go func() {
			defer group.Done()
			for j := 0; j < 7; j++ {
				if _, err := store.Incr("b", time.Minute); err != nil {
					t.Errorf("键 b 并发自增失败: %v", err)
				}
			}
		}()
	}
	group.Wait()

	if count, _ := store.Incr("a", 0); count != 30*5+1 {
		t.Fatalf("键 a 计数应准确为 %d，实际: %d", 30*5+1, count)
	}
	if count, _ := store.Incr("b", 0); count != 30*7+1 {
		t.Fatalf("键 b 计数应准确为 %d，实际: %d", 30*7+1, count)
	}
}

// TestControllerReloadClosesOldStore - 验证门面热重载关闭旧 memory 实例（防 ristretto goroutine 泄漏）
func TestControllerReloadClosesOldStore(t *testing.T) {

	inst := &Controller{}
	inst.Init(Config{Engine: "memory", Memory: MemoryConfig{MaxEntries: 1000}})

	old, ok := Cache.Store().(*MemoryStore)
	if !ok {
		t.Fatal("全局实例底层应为 memory 驱动")
	}
	if !Cache.Set("k", "v") {
		t.Fatal("memory 引擎应可正常读写")
	}

	// 热重载到 file 引擎（临时目录，避免污染仓库）
	inst.ReloadIfChanged(Config{Engine: "file", File: FileConfig{Root: t.TempDir()}})
	if !Cache.Set("k2", "v2") {
		t.Fatal("重载后新实例应可正常读写")
	}

	// 旧 memory 实例应已由门面关闭（ristretto 关闭后 Set 返回 false）
	if old.Set("k3", "v3", 0) {
		t.Fatal("旧 memory 实例应已被关闭")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}

// newLayered - 构建分层驱动测试实例（L2 注入内存文件系统；清理时关闭 L1）
func newLayered(t *testing.T, fs afero.Fs) *LayeredStore {
	t.Helper()
	conf := normConfig(Config{File: FileConfig{Root: "cache", Suffix: "json"}})
	l1, err := newMemoryStore(conf)
	if err != nil {
		t.Fatalf("构建内存层失败: %v", err)
	}
	l2 := &FileStore{Fs: fs, Config: conf.File}
	store := &LayeredStore{L1: l1, L2: l2}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestLayeredStore - 验证分层驱动：读写、回源回灌、写失效一致性、过期、删除清空双层生效
func TestLayeredStore(t *testing.T) {

	store := newLayered(t, afero.NewMemMapFs())

	// Set 后 Get 命中（写已失效 L1，首次读走 L2 回源）
	if !store.Set("k", "v", 0) {
		t.Fatal("Set 不应失败")
	}
	if !store.Has("k") || store.Get("k") != "v" {
		t.Fatal("Set 后应能读到写入的值")
	}
	// 回源后应回灌 L1（再次读不触发磁盘）
	if !store.L1.Has("k") {
		t.Fatal("Get 回源后应回灌 L1")
	}

	// 写失效一致性：Set 新值后读必为新值，无 L1 残留
	store.Set("k", "A", 0)
	if got := store.Get("k"); got != "A" {
		t.Fatalf("写后失效不应残留旧值，实际: %+v", got)
	}

	// 过期语义（L2 为秒级时间戳，1 秒 TTL + 睡眠越过秒边界；先回灌 L1 再验证两层同时失效）
	store.Set("e", "v", time.Second)
	if store.Get("e") != "v" {
		t.Fatal("窗口内应可读")
	}
	time.Sleep(2100 * time.Millisecond)
	if store.Has("e") || store.Get("e") != nil {
		t.Fatal("过期缓存应视为不存在")
	}

	// 删除：回灌后的键删除后两层均无残留（file 层 JSON 往返为 float64，用 cast 比较）
	store.Set("d", 1, 0)
	if cast.ToInt64(store.Get("d")) != 1 {
		t.Fatal("读以触发回灌")
	}
	store.Delete("d")
	if store.Has("d") || store.L1.Has("d") {
		t.Fatal("Delete 后两层均应移除")
	}

	// Clear：两层同时清空
	store.Set("c", 1, 0)
	if !store.Clear() || store.Has("c") || store.L1.Has("c") {
		t.Fatal("Clear 后两层均应清空")
	}
}

// TestLayeredRestartPersist - 验证分层驱动重启恢复：同一文件系统 + 全新内存层，数据与计数连续
func TestLayeredRestartPersist(t *testing.T) {

	fs := afero.NewMemMapFs()
	store := newLayered(t, fs)

	store.Set("ticket", "T-1", 10*time.Minute)
	store.Set("forever", "F", 0)
	if count, _ := store.Incr("counter", 10*time.Minute); count != 1 {
		t.Fatalf("首次自增应为 1，实际: %d", count)
	}
	if count, _ := store.Incr("counter", 10*time.Minute); count != 2 {
		t.Fatalf("第二次自增应为 2，实际: %d", count)
	}
	store.Close() // 模拟进程结束：L1 释放，L2 落盘数据仍在

	// 重启：同一文件系统（磁盘），全新内存层
	store2 := newLayered(t, fs)
	if got := store2.Get("ticket"); got != "T-1" {
		t.Fatalf("重启后应能从文件层恢复 ticket，实际: %+v", got)
	}
	if got := store2.Get("forever"); got != "F" {
		t.Fatalf("重启后应能恢复永不过期键，实际: %+v", got)
	}
	if seconds, _ := store2.TTL("forever"); seconds != -1 {
		t.Fatalf("重启后永不过期键 TTL 应为 -1，实际: %d", seconds)
	}
	// 计数连续：文件层权威
	if count, _ := store2.Incr("counter", 10*time.Minute); count != 3 {
		t.Fatalf("重启后计数应连续为 3，实际: %d", count)
	}
}

// TestLayeredAtomic - 验证分层驱动原子方法：SetNX 语义、TTL 三态
func TestLayeredAtomic(t *testing.T) {

	store := newLayered(t, afero.NewMemMapFs())

	// SetNX：已存在不覆盖，不存在则写入
	store.Set("k", "v", 10*time.Minute)
	if ok, _ := store.SetNX("k", "v2", time.Second); ok {
		t.Fatal("键已存在时 SetNX 应返回 false")
	}
	if store.Get("k") != "v" {
		t.Fatalf("SetNX 不应覆盖已有值，实际: %+v", store.Get("k"))
	}
	if ok, _ := store.SetNX("n", 1, time.Minute); !ok {
		t.Fatal("键不存在时 SetNX 应返回 true")
	}

	// TTL 三态：永不过期 -1、带窗口 >0、不存在 0
	store.Set("f", "v", 0)
	if seconds, _ := store.TTL("f"); seconds != -1 {
		t.Fatalf("永不过期的键 TTL 应为 -1，实际: %d", seconds)
	}
	store.Set("e", "v", 2*time.Second)
	if seconds, _ := store.TTL("e"); seconds <= 0 || seconds > 2 {
		t.Fatalf("带窗口的键 TTL 应在 (0,2] 秒内，实际: %d", seconds)
	}
	if seconds, _ := store.TTL("none"); seconds != 0 {
		t.Fatalf("不存在的键 TTL 应为 0，实际: %d", seconds)
	}
}

// TestLayeredIncrConcurrent - 验证分层驱动并发自增：同键计数准确（L2 分段锁生效）、异键互不错串
func TestLayeredIncrConcurrent(t *testing.T) {

	store := newLayered(t, afero.NewMemMapFs())

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
	if count, _ := store.Incr("c", 0); count != goroutines*each+1 {
		t.Fatalf("同键并发自增计数应准确为 %d，实际: %d", goroutines*each+1, count)
	}

	// 异键并发：互不错串
	var group2 sync.WaitGroup
	for i := 0; i < 30; i++ {
		group2.Add(2)
		go func() {
			defer group2.Done()
			for j := 0; j < 5; j++ {
				_, _ = store.Incr("a", time.Minute)
			}
		}()
		go func() {
			defer group2.Done()
			for j := 0; j < 7; j++ {
				_, _ = store.Incr("b", time.Minute)
			}
		}()
	}
	group2.Wait()
	if count, _ := store.Incr("a", 0); count != 30*5+1 {
		t.Fatalf("键 a 计数应准确为 %d，实际: %d", 30*5+1, count)
	}
	if count, _ := store.Incr("b", 0); count != 30*7+1 {
		t.Fatalf("键 b 计数应准确为 %d，实际: %d", 30*7+1, count)
	}
}

// TestLayeredReloadClosesOldStore - 验证门面热重载关闭旧 layered 实例（复用 io.Closer 断言机制）
func TestLayeredReloadClosesOldStore(t *testing.T) {

	inst := &Controller{}
	inst.Init(Config{Engine: "layered", File: FileConfig{Root: t.TempDir()}})

	old, ok := Cache.Store().(*LayeredStore)
	if !ok {
		t.Fatal("全局实例底层应为 layered 驱动")
	}
	if !Cache.Set("k", "v") {
		t.Fatal("layered 引擎应可正常读写")
	}

	// 热重载到 memory 引擎
	inst.ReloadIfChanged(Config{Engine: "memory", Memory: MemoryConfig{MaxEntries: 1000}})
	if !Cache.Set("k2", "v2") {
		t.Fatal("重载后新实例应可正常读写")
	}

	// 旧 layered 实例的 L1 应已由门面关闭（ristretto 关闭后 Set 返回 false）
	if old.L1.Set("k3", "v3", 0) {
		t.Fatal("旧实例的 L1 应已被关闭")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}
