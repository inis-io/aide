package storagex

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeStore - 测试用驱动：内存记录全部调用，不触发任何网络与磁盘请求
type fakeStore struct {
	// 存储根目录名
	root string
	// 访问域名
	domain string
	// Put 记录：key -> 内容
	puts map[string]string
	// List 预设返回
	entries []Entry
	// List 收到的 dir
	listDir string
	// MakeDir 记录
	dirs []string
	// Remove 记录
	removed []string
	// Move 记录：[源, 目标]
	moves [][2]string
}

// newFakeStore - 构造假驱动
func newFakeStore() *fakeStore {
	return &fakeStore{
		root:   "app",
		domain: "https://static.example.com",
		puts:   map[string]string{},
	}
}

// Root - 假实现，返回预设根
func (this *fakeStore) Root() string { return this.root }

// Domain - 假实现，返回预设域名
func (this *fakeStore) Domain() string { return this.domain }

// Put - 假实现，记录 key 与内容
func (this *fakeStore) Put(_ context.Context, key string, reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	this.puts[key] = string(data)
	return nil
}

// List - 假实现，记录 dir 并返回预设条目
func (this *fakeStore) List(_ context.Context, dir string, _ string, _ int) ([]Entry, string, error) {
	this.listDir = dir
	return this.entries, "", nil
}

// MakeDir - 假实现，记录目录
func (this *fakeStore) MakeDir(_ context.Context, dir string) error {
	this.dirs = append(this.dirs, dir)
	return nil
}

// Remove - 假实现，记录路径
func (this *fakeStore) Remove(_ context.Context, paths ...string) error {
	this.removed = append(this.removed, paths...)
	return nil
}

// Move - 假实现，记录源与目标
func (this *fakeStore) Move(_ context.Context, src, dst string) error {
	this.moves = append(this.moves, [2]string{src, dst})
	return nil
}

// registerFake - 注册返回固定假驱动的工厂，返回驱动本体便于断言
func registerFake(name string) *fakeStore {
	store := newFakeStore()
	Register(name, func(config Config) (Store, error) { return store, nil })
	return store
}

// onlyKey - 取假驱动中唯一一条 Put 记录的 key
func (this *fakeStore) onlyKey(t *testing.T) string {
	t.Helper()
	if len(this.puts) != 1 {
		t.Fatalf("Put 记录数应为 1，实际 %d：%v", len(this.puts), this.puts)
	}
	for key := range this.puts {
		return key
	}
	return ""
}

// TestRegisterAndNew - 验证注册表：非法注册 panic、按名创建、未注册报错、驱动列表有序
func TestRegisterAndNew(t *testing.T) {

	// 空名称注册应 panic
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("空名称注册应 panic")
			}
		}()
		Register(" ", func(config Config) (Store, error) { return nil, nil })
	}()

	// 空工厂注册应 panic
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("空工厂注册应 panic")
			}
		}()
		Register("nil-factory", nil)
	}()

	store := registerFake("mock")
	driver, err := New("mock", Config{})
	if err != nil {
		t.Fatalf("按名创建失败：%v", err)
	}
	if driver.Store() != store {
		t.Fatal("New 应使用注册的工厂构建驱动")
	}

	if _, err = New("not-exist", Config{}); err == nil || !strings.Contains(err.Error(), "not-exist") {
		t.Fatalf("未注册驱动应报错并提示名称，实际：%v", err)
	}

	// 内置驱动已登记且列表有序
	names := Names()
	for _, want := range []string{"cos", "local", "oss"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("内置驱动[%s]未登记：%v", want, names)
		}
	}
}

// TestNormConfig - 验证配置归一化：默认值补齐、未注册引擎回退 local、Hash 自动计算
func TestNormConfig(t *testing.T) {

	conf := normConfig(Config{})
	if conf.Engine != "local" {
		t.Fatalf("空引擎应回退 local，实际 %s", conf.Engine)
	}
	if conf.Local.Domain != "http://localhost:2000" || conf.Local.Root != "public/storage" {
		t.Fatalf("本地配置默认值不符：%+v", conf.Local)
	}
	if conf.OSS.Endpoint != "oss-cn-guangzhou.aliyuncs.com" || conf.OSS.Path != "AIDE" {
		t.Fatalf("OSS 配置默认值不符：%+v", conf.OSS)
	}
	if conf.COS.Region != "ap-guangzhou" || conf.COS.Path != "AIDE" {
		t.Fatalf("COS 配置默认值不符：%+v", conf.COS)
	}
	if conf.Hash == "" {
		t.Fatal("Hash 应自动计算")
	}

	if conf = normConfig(Config{Engine: " UNKNOWN "}); conf.Engine != "local" {
		t.Fatalf("未注册引擎应回退 local，实际 %s", conf.Engine)
	}
	if conf = normConfig(Config{Engine: " OSS "}); conf.Engine != "oss" {
		t.Fatalf("引擎名应清洗为小写，实际 %s", conf.Engine)
	}
}

// TestDriverChain - 验证链式调用值语义：每次调用返回副本，互不影响
func TestDriverChain(t *testing.T) {

	driver := NewDriver(newFakeStore())

	base := driver.Dir("media")
	if driver.dir != "" {
		t.Fatal("链式调用不应修改原实例")
	}

	item := base.Name("a").Ext("txt")
	if base.name != "" || base.ext != "" {
		t.Fatal("链式调用不应修改中间副本")
	}
	if item.dir != "media/" || item.name != "a" || item.ext != ".txt" {
		t.Fatalf("链式参数不符：dir=%q name=%q ext=%q", item.dir, item.name, item.ext)
	}

	// Ext 自动补前导 .，Dir 自动清理并补尾部 /
	if NewDriver(newFakeStore()).Ext("png").ext != ".png" {
		t.Fatal("Ext 应补前导 .")
	}
	if NewDriver(newFakeStore()).Dir(`a\b`).dir != "a/b/" {
		t.Fatal("Dir 应统一分隔符并补尾部 /")
	}
}

// TestDriverPut - 验证上传：缺省按日期目录 + 时间戳命名，响应组装完整
func TestDriverPut(t *testing.T) {

	store := newFakeStore()
	resp := NewDriver(store).Ext("txt").Put(strings.NewReader("hello"))
	if resp.Error != nil {
		t.Fatalf("上传失败：%v", resp.Error)
	}

	key := store.onlyKey(t)
	// 缺省目录为当天 年-月/日
	if !strings.HasPrefix(key, time.Now().Format("2006-01/02/")) || !strings.HasSuffix(key, ".txt") {
		t.Fatalf("缺省 key 应为 日期目录/时间戳.txt，实际 %s", key)
	}
	if store.puts[key] != "hello" {
		t.Fatalf("上传内容不符：%q", store.puts[key])
	}

	if resp.Path != "/app/"+key {
		t.Fatalf("公开路径不符：%s", resp.Path)
	}
	if resp.Domain != "https://static.example.com" || resp.Url != "https://static.example.com/app/"+key {
		t.Fatalf("访问地址不符：domain=%s url=%s", resp.Domain, resp.Url)
	}
	if resp.Name != key[strings.LastIndex(key, "/")+1:] {
		t.Fatalf("文件名不符：%s", resp.Name)
	}

	// 空驱动与空内容应报错
	if NewDriver(nil).Put(strings.NewReader("x")).Error == nil {
		t.Fatal("驱动为 nil 应报错")
	}
	if NewDriver(newFakeStore()).Put(nil).Error == nil {
		t.Fatal("内容为 nil 应报错")
	}
}

// TestDriverPutNaming - 验证自定义命名：Dir/Name/Ext 生效，文件名目录成分被清理，目录越界回退缺省
func TestDriverPutNaming(t *testing.T) {

	store := newFakeStore()
	resp := NewDriver(store).Dir("media/avatar").Name("user/1").Ext("png").Put(strings.NewReader("x"))
	if resp.Error != nil {
		t.Fatalf("上传失败：%v", resp.Error)
	}
	if key := store.onlyKey(t); key != "media/avatar/1.png" {
		t.Fatalf("key 应为 media/avatar/1.png，实际 %s", key)
	}

	// 目录越界（..）按非法处理，回退缺省日期目录
	store = newFakeStore()
	if resp = NewDriver(store).Dir("../escape").Put(strings.NewReader("x")); resp.Error != nil {
		t.Fatalf("上传失败：%v", resp.Error)
	}
	if key := store.onlyKey(t); !strings.HasPrefix(key, time.Now().Format("2006-01/02/")) {
		t.Fatalf("越界目录应回退缺省日期目录，实际 %s", key)
	}
}

// TestDriverList - 验证列目录：公开路径换算、Prefix 页内过滤、Path/Url 组装、越界报错
func TestDriverList(t *testing.T) {

	store := newFakeStore()
	store.entries = []Entry{
		{Name: "docs", IsDir: true},
		{Name: "a.txt", Size: 3, ModTime: 123},
		{Name: "b.txt", Size: 5, ModTime: 456},
	}

	driver := NewDriver(store)
	resp := driver.List(ListParams{Dir: "/app/media", Prefix: "a"})
	if resp.Error != nil {
		t.Fatalf("列目录失败：%v", resp.Error)
	}

	if store.listDir != "media" {
		t.Fatalf("驱动应收到相对路径 media，实际 %q", store.listDir)
	}
	if resp.Root != "/app" {
		t.Fatalf("根公开路径应为 /app，实际 %s", resp.Root)
	}
	// Prefix 页内过滤：docs 与 b.txt 被滤除
	if len(resp.List) != 1 || resp.List[0].Name != "a.txt" {
		t.Fatalf("Prefix 过滤结果不符：%+v", resp.List)
	}
	item := resp.List[0]
	if item.Path != "/app/media/a.txt" || item.Url != "https://static.example.com/app/media/a.txt" {
		t.Fatalf("条目路径组装不符：%+v", item)
	}

	// 目录条目不组装 Url
	resp = driver.List(ListParams{Dir: "/app/media"})
	if len(resp.List) != 3 || resp.List[0].Url != "" {
		t.Fatalf("目录条目不应有 Url：%+v", resp.List)
	}

	// 越界目录报错
	if driver.List(ListParams{Dir: "/other/x"}).Error == nil {
		t.Fatal("前缀不符的目录应报错")
	}
	if driver.List(ListParams{Dir: "/app/../../etc"}).Error == nil {
		t.Fatal(".. 越界目录应报错")
	}

	// 根目录与空目录等价
	if resp = driver.List(ListParams{Dir: "/app"}); resp.Error != nil || store.listDir != "" {
		t.Fatalf("存储根本身应映射为空相对路径，dir=%q err=%v", store.listDir, resp.Error)
	}

	if driver.Root() != "/app" {
		t.Fatalf("Root 应为 /app，实际 %s", driver.Root())
	}
}

// TestDriverManage - 验证目录管理：路径校验后透传相对路径，Move 禁止移入自身内部
func TestDriverManage(t *testing.T) {

	store := newFakeStore()
	driver := NewDriver(store)

	if resp := driver.MakeDir("/app/media"); resp.Error != nil {
		t.Fatalf("创建目录失败：%v", resp.Error)
	}
	if len(store.dirs) != 1 || store.dirs[0] != "media" {
		t.Fatalf("MakeDir 应透传相对路径 media，实际 %v", store.dirs)
	}
	if driver.MakeDir("/etc").Error == nil {
		t.Fatal("越界目录应报错")
	}
	if driver.MakeDir("/app").Error == nil {
		t.Fatal("存储根本身不允许作为创建目标")
	}

	if resp := driver.Remove("/app/a.txt", "/app/dir"); resp.Error != nil {
		t.Fatalf("删除失败：%v", resp.Error)
	}
	if len(store.removed) != 2 || store.removed[0] != "a.txt" || store.removed[1] != "dir" {
		t.Fatalf("Remove 应透传相对路径，实际 %v", store.removed)
	}
	if driver.Remove("/app").Error == nil {
		t.Fatal("不允许删除存储根本身")
	}
	if driver.Remove("/other/a").Error == nil {
		t.Fatal("越界路径应报错")
	}

	if resp := driver.Move("/app/a", "/app/b"); resp.Error != nil {
		t.Fatalf("移动失败：%v", resp.Error)
	}
	if len(store.moves) != 1 || store.moves[0] != [2]string{"a", "b"} {
		t.Fatalf("Move 应透传相对路径，实际 %v", store.moves)
	}
	if driver.Move("/app/a", "/app/a/c").Error == nil {
		t.Fatal("移动到自身内部应报错")
	}
	if driver.Move("/app/a", "/app/a").Error == nil {
		t.Fatal("移动到自身应报错")
	}
}

// TestLocalStore - 验证本地驱动：基于临时目录的真实落盘全流程
func TestLocalStore(t *testing.T) {

	root := filepath.Join(t.TempDir(), "storage")
	store := &LocalStore{Config: LocalConfig{Domain: "http://localhost:2000", Root: root}}
	if store.Root() != "storage" {
		t.Fatalf("公开根应为 storage，实际 %s", store.Root())
	}

	driver := NewDriver(store)

	// 上传并读回
	resp := driver.Dir("media").Name("hello").Ext("txt").Put(strings.NewReader("world"))
	if resp.Error != nil {
		t.Fatalf("上传失败：%v", resp.Error)
	}
	if resp.Path != "/storage/media/hello.txt" || resp.Url != "http://localhost:2000/storage/media/hello.txt" {
		t.Fatalf("响应组装不符：%+v", resp)
	}
	data, err := os.ReadFile(filepath.Join(root, "media", "hello.txt"))
	if err != nil || string(data) != "world" {
		t.Fatalf("落盘内容不符：data=%q err=%v", data, err)
	}

	// 列目录
	list := driver.List(ListParams{Dir: "/storage/media"})
	if list.Error != nil || len(list.List) != 1 || list.List[0].Name != "hello.txt" || list.List[0].Size != 5 {
		t.Fatalf("列目录不符：%+v", list)
	}

	// 不存在的目录按空目录处理
	if list = driver.List(ListParams{Dir: "/storage/none"}); list.Error != nil || len(list.List) != 0 {
		t.Fatalf("不存在目录应返回空列表：%+v", list)
	}

	// 创建目录
	if resp = driver.MakeDir("/storage/sub"); resp.Error != nil {
		t.Fatalf("创建目录失败：%v", resp.Error)
	}
	if info, err := os.Stat(filepath.Join(root, "sub")); err != nil || !info.IsDir() {
		t.Fatal("目录未创建")
	}

	// 移动（跨目录，目标父目录自动创建）
	if resp = driver.Move("/storage/media/hello.txt", "/storage/sub/deep/hi.txt"); resp.Error != nil {
		t.Fatalf("移动失败：%v", resp.Error)
	}
	data, err = os.ReadFile(filepath.Join(root, "sub", "deep", "hi.txt"))
	if err != nil || string(data) != "world" {
		t.Fatalf("移动后内容不符：data=%q err=%v", data, err)
	}
	if _, err = os.Stat(filepath.Join(root, "media", "hello.txt")); !os.IsNotExist(err) {
		t.Fatal("源文件应已移走")
	}

	// 删除（目录递归）
	if resp = driver.Remove("/storage/sub"); resp.Error != nil {
		t.Fatalf("删除失败：%v", resp.Error)
	}
	if _, err = os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Fatal("目录应已删除")
	}
}

// TestControllerInitAndReload - 验证控制器：默认配置、注入配置、Hash 变化触发热重载
func TestControllerInitAndReload(t *testing.T) {

	mock := registerFake("mock")

	inst := &Controller{}
	// 未注入配置时使用默认本地驱动
	inst.Init()
	if _, ok := Storage.Store().(*LocalStore); !ok {
		t.Fatalf("默认驱动应为本地存储，实际 %T", Storage.Store())
	}

	// 注入配置后切换到假驱动
	inst.Init(Config{Engine: "mock"})
	resp := Storage.Dir("a").Ext("txt").Put(strings.NewReader("v"))
	if resp.Error != nil || len(mock.puts) != 1 {
		t.Fatalf("注入配置后应使用假驱动：%+v", resp)
	}

	// 配置未变化时不重载
	before := Storage
	inst.ReloadIfChanged()
	if Storage.Store() != before.Store() {
		t.Fatal("配置未变化不应重载")
	}

	// 配置变化后重载到新驱动
	other := registerFake("mock2")
	inst.ReloadIfChanged(Config{Engine: "mock2"})
	if resp = Storage.Put(strings.NewReader("x")); resp.Error != nil || len(other.puts) != 1 {
		t.Fatal("配置变化后应重载到新注册的驱动")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}

// TestStoreError - 验证驱动初始化失败的占位实现：所有操作返回原始初始化错误
func TestStoreError(t *testing.T) {

	Register("broken", func(config Config) (Store, error) {
		return nil, errors.New("连接失败")
	})

	inst := &Controller{}
	inst.Init(Config{Engine: "broken"})

	if resp := Storage.Put(strings.NewReader("x")); resp.Error == nil || !strings.Contains(resp.Error.Error(), "连接失败") {
		t.Fatalf("Put 应返回初始化错误，实际 %v", resp.Error)
	}
	if resp := Storage.List(ListParams{}); resp.Error == nil {
		t.Fatal("List 应返回初始化错误")
	}
	if Storage.MakeDir("/x").Error == nil || Storage.Remove("/x").Error == nil || Storage.Move("/x", "/y").Error == nil {
		t.Fatal("管理操作应返回初始化错误")
	}

	inst.HasConfig = false
	inst.useDefault()
}
