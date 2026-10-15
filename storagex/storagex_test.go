package storagex

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cast"
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

// TestSignedURLOSS - 验证 OSS 预签名 URL：本地计算签名（不联网），断言 URL 形态与过期秒数
func TestSignedURLOSS(t *testing.T) {

	factory, err := newOssStore(Config{OSS: OSSConfig{
		AccessKeyId: "test-access-key", AccessKeySecret: "test-secret", Bucket: "smoke-bucket", Endpoint: "oss-cn-guangzhou.aliyuncs.com", Path: "AIDE",
	}})
	if err != nil {
		t.Fatalf("构造 OSS 驱动失败：%v", err)
	}
	store, ok := factory.(SignedURLer)
	if !ok {
		t.Fatal("OSS 驱动应实现 SignedURLer 可选接口")
	}

	signed, err := store.SignedURL(context.Background(), "artifacts/app.bin", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL 失败：%v", err)
	}
	// 形态：{scheme}://{bucket}.{endpoint}/{Root}/{key}?{签名 query}（scheme 由 SDK 决定）
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("预签名 URL 解析失败：%v", err)
	}
	if parsed.Host != "smoke-bucket.oss-cn-guangzhou.aliyuncs.com" {
		t.Fatalf("预签名 URL host 不符：%s", signed)
	}
	if parsed.Path != "/AIDE/artifacts/app.bin" {
		t.Fatalf("预签名 URL 路径不符：%s", signed)
	}
	query := parsed.Query()
	if query.Get("Signature") == "" || query.Get("Expires") == "" {
		t.Fatalf("预签名 URL 缺少签名参数：%s", signed)
	}

	// 无效客户端应报错
	broken := &OssStore{Config: OSSConfig{Bucket: "b"}}
	if _, err := broken.SignedURL(context.Background(), "k", time.Minute); err == nil {
		t.Fatal("未初始化客户端应报错")
	}
}

// TestSignedURLCOS - 验证 COS 预签名 URL：本地计算签名（不联网），断言 URL 形态
func TestSignedURLCOS(t *testing.T) {

	factory, err := newCosStore(Config{COS: COSConfig{
		AppId: "1250000000", SecretId: "test-secret-id", SecretKey: "test-secret-key", Bucket: "smoke-bucket", Region: "ap-guangzhou", Path: "AIDE",
	}})
	if err != nil {
		t.Fatalf("构造 COS 驱动失败：%v", err)
	}
	store, ok := factory.(SignedURLer)
	if !ok {
		t.Fatal("COS 驱动应实现 SignedURLer 可选接口")
	}

	signed, err := store.SignedURL(context.Background(), "artifacts/app.bin", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL 失败：%v", err)
	}
	// 形态：https://{bucket}-{appid}.cos.{region}.myqcloud.com/{Root}/{key}?{签名 query}
	if !strings.HasPrefix(signed, "https://smoke-bucket-1250000000.cos.ap-guangzhou.myqcloud.com/AIDE/artifacts/app.bin?") {
		t.Fatalf("预签名 URL 形态不符：%s", signed)
	}
	if !strings.Contains(signed, "q-signature=") {
		t.Fatalf("预签名 URL 缺少 COS 签名参数：%s", signed)
	}
}

// TestSignedURLLocalNotSupported - 验证 local 驱动不实现 SignedURLer（走鉴权下载代理）
func TestSignedURLLocalNotSupported(t *testing.T) {

	store, err := newLocalStore(Config{Local: LocalConfig{Root: "public/storage", Domain: "http://localhost:2000"}})
	if err != nil {
		t.Fatalf("构造 local 驱动失败：%v", err)
	}
	if _, ok := store.(SignedURLer); ok {
		t.Fatal("local 驱动不应实现 SignedURLer（本地文件走鉴权下载代理）")
	}
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

// fakeMultipartStore - 测试用全能力假驱动：在 fakeStore 之上实现 Multiparter / Getter / SignedURLer，记录换算后的相对 key
type fakeMultipartStore struct {
	*fakeStore
	// 分片族方法收到的相对 key 调用记录
	keys []string
	// Get 收到的相对 key
	getKey string
	// SignedURL 收到的相对 key
	signKey string
}

// InitMultipart - 假实现，记录 key 并返回固定 uploadId
func (this *fakeMultipartStore) InitMultipart(_ context.Context, key string) (string, error) {
	this.keys = append(this.keys, key)
	return "fake-upload-id", nil
}

// UploadPart - 假实现，记录 key
func (this *fakeMultipartStore) UploadPart(_ context.Context, key, _ string, index int, _ io.Reader, size int64) (Part, error) {
	this.keys = append(this.keys, key)
	return Part{Index: index, ETag: "fake-etag", Size: size}, nil
}

// ListParts - 假实现，记录 key
func (this *fakeMultipartStore) ListParts(_ context.Context, key, _ string) ([]Part, error) {
	this.keys = append(this.keys, key)
	return []Part{{Index: 1, ETag: "fake-etag", Size: 1}}, nil
}

// CompleteMultipart - 假实现，记录 key
func (this *fakeMultipartStore) CompleteMultipart(_ context.Context, key, _ string, _ []Part) error {
	this.keys = append(this.keys, key)
	return nil
}

// AbortMultipart - 假实现，记录 key
func (this *fakeMultipartStore) AbortMultipart(_ context.Context, key, _ string) error {
	this.keys = append(this.keys, key)
	return nil
}

// Get - 假实现，记录 key
func (this *fakeMultipartStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	this.getKey = key
	return io.NopCloser(strings.NewReader("ok")), nil
}

// SignedURL - 假实现，记录 key
func (this *fakeMultipartStore) SignedURL(_ context.Context, key string, _ time.Duration) (string, error) {
	this.signKey = key
	return "https://signed.example.com/" + key, nil
}

// TestDriverMultipart - 验证 Driver 分片族方法：公开路径换算为相对 key、.. 穿越拒绝、驱动不支持时报错含能力名
func TestDriverMultipart(t *testing.T) {

	full := &fakeMultipartStore{fakeStore: newFakeStore()}
	driver := NewDriver(full)

	uploadId, err := driver.InitMultipart("/app/big/a.zip")
	if err != nil || uploadId != "fake-upload-id" {
		t.Fatalf("InitMultipart 失败：uploadId=%s err=%v", uploadId, err)
	}
	if len(full.keys) != 1 || full.keys[0] != "big/a.zip" {
		t.Fatalf("应换算为相对 key big/a.zip，实际 %v", full.keys)
	}
	if _, err = driver.UploadPart("/app/big/a.zip", uploadId, 2, strings.NewReader("xy"), 2); err != nil {
		t.Fatalf("UploadPart 失败：%v", err)
	}
	if _, err = driver.ListParts("/app/big/a.zip", uploadId); err != nil {
		t.Fatalf("ListParts 失败：%v", err)
	}
	if err = driver.CompleteMultipart("/app/big/a.zip", uploadId, []Part{{Index: 1, ETag: "fake-etag", Size: 1}}); err != nil {
		t.Fatalf("CompleteMultipart 失败：%v", err)
	}
	if err = driver.AbortMultipart("/app/big/a.zip", uploadId); err != nil {
		t.Fatalf("AbortMultipart 失败：%v", err)
	}
	// 五次调用都应收到同一个相对 key
	for _, key := range full.keys {
		if key != "big/a.zip" {
			t.Fatalf("分片族方法应收到相对 key big/a.zip，实际 %v", full.keys)
		}
	}

	// Get 与 SignedURL 公开路径换算
	reader, err := driver.Get("/app/big/a.zip")
	if err != nil {
		t.Fatalf("Get 失败：%v", err)
	}
	_ = reader.Close()
	if full.getKey != "big/a.zip" {
		t.Fatalf("Get 应收到相对 key big/a.zip，实际 %s", full.getKey)
	}
	if _, err = driver.SignedURL("/app/big/a.zip", time.Minute); err != nil {
		t.Fatalf("SignedURL 失败：%v", err)
	}
	if full.signKey != "big/a.zip" {
		t.Fatalf("SignedURL 应收到相对 key big/a.zip，实际 %s", full.signKey)
	}

	// 穿越与越界拒绝
	if _, err = driver.InitMultipart("/app/../escape.zip"); err == nil {
		t.Fatal(".. 穿越应报错")
	}
	if _, err = driver.InitMultipart("/other/a.zip"); err == nil {
		t.Fatal("越出存储根应报错")
	}
	if _, err = driver.Get("/app/../../etc/passwd"); err == nil {
		t.Fatal("Get 穿越应报错")
	}
	if _, err = driver.SignedURL("/app", time.Minute); err == nil {
		t.Fatal("存储根本身不允许签发")
	}

	// 不支持可选能力的驱动：报错应含能力名
	plain := NewDriver(newFakeStore())
	if _, err = plain.InitMultipart("/app/a.zip"); err == nil || !strings.Contains(err.Error(), "Multiparter") {
		t.Fatalf("不支持分片应报错并提示 Multiparter，实际 %v", err)
	}
	if _, err = plain.Get("/app/a.zip"); err == nil || !strings.Contains(err.Error(), "Getter") {
		t.Fatalf("不支持读取应报错并提示 Getter，实际 %v", err)
	}
	if _, err = plain.SignedURL("/app/a.zip", time.Minute); err == nil || !strings.Contains(err.Error(), "SignedURLer") {
		t.Fatalf("不支持签名应报错并提示 SignedURLer，实际 %v", err)
	}
}

// TestDriverSignedURLKey - 验证 Driver.SignedURL 口径：公开路径换算为相对 key，云端签名 URL 不双前缀（本地计算签名，不联网）
func TestDriverSignedURLKey(t *testing.T) {

	factory, err := newOssStore(Config{OSS: OSSConfig{
		AccessKeyId: "test-access-key", AccessKeySecret: "test-secret", Bucket: "smoke-bucket", Endpoint: "oss-cn-guangzhou.aliyuncs.com", Path: "AIDE",
	}})
	if err != nil {
		t.Fatalf("构造 OSS 驱动失败：%v", err)
	}
	signed, err := NewDriver(factory).SignedURL("/AIDE/artifacts/app.bin", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL 失败：%v", err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("预签名 URL 解析失败：%v", err)
	}
	if parsed.Path != "/AIDE/artifacts/app.bin" {
		t.Fatalf("签名 URL 双前缀或路径不符：%s", signed)
	}
}

// TestLocalMultipart - 验证本地驱动分片全生命周期：Init → 乱序上传 → ListParts 升序 → Complete 逐字节一致且临时区清空 → Abort 清理
func TestLocalMultipart(t *testing.T) {

	root := filepath.Join(t.TempDir(), "storage")
	store := &LocalStore{Config: LocalConfig{Domain: "http://localhost:2000", Root: root}}
	driver := NewDriver(store)

	publicPath := "/storage/2026-10/big.bin"
	key := "2026-10/big.bin"
	content := []string{"hello-", "storagex-", "multipart"}

	// 初始化会话：临时区目录与 meta.json 落地
	uploadId, err := driver.InitMultipart(publicPath)
	if err != nil {
		t.Fatalf("InitMultipart 失败：%v", err)
	}
	dir := filepath.Join(root, ".multipart", uploadId)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("临时区目录未创建：%v", err)
	}
	meta, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil || !strings.Contains(string(meta), key) {
		t.Fatalf("meta.json 应记录 key：%q err=%v", meta, err)
	}

	// 乱序上传 3 片（3 → 1 → 2），回执 ETag 为 size 摘要
	for _, index := range []int{3, 1, 2} {
		part, err := driver.UploadPart(publicPath, uploadId, index, strings.NewReader(content[index-1]), int64(len(content[index-1])))
		if err != nil {
			t.Fatalf("UploadPart %d 失败：%v", index, err)
		}
		if part.Size != int64(len(content[index-1])) || part.ETag != "size:"+cast.ToString(part.Size) {
			t.Fatalf("分片回执不符：%+v", part)
		}
	}

	// 同 index 幂等覆盖（重传安全）
	if _, err = driver.UploadPart(publicPath, uploadId, 2, strings.NewReader(content[1]), int64(len(content[1]))); err != nil {
		t.Fatalf("同 index 重传失败：%v", err)
	}

	// ListParts 按序号升序
	parts, err := driver.ListParts(publicPath, uploadId)
	if err != nil || len(parts) != 3 {
		t.Fatalf("ListParts 不符：%+v err=%v", parts, err)
	}
	for i, part := range parts {
		if part.Index != i+1 || part.Size != int64(len(content[i])) {
			t.Fatalf("分片顺序或大小不符：%+v", parts)
		}
	}

	// 声明字节数与实际不符应拒绝
	if _, err = driver.UploadPart(publicPath, uploadId, 4, strings.NewReader("x"), 5); err == nil {
		t.Fatal("字节数不符应报错")
	}

	// Complete：拼接结果逐字节一致，临时区清空
	if err = driver.CompleteMultipart(publicPath, uploadId, parts); err != nil {
		t.Fatalf("CompleteMultipart 失败：%v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "2026-10", "big.bin"))
	if err != nil || string(data) != strings.Join(content, "") {
		t.Fatalf("合并内容不符：data=%q err=%v", data, err)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("Complete 后临时区应已清空")
	}

	// Get 回读一致
	reader, err := driver.Get(publicPath)
	if err != nil {
		t.Fatalf("Get 失败：%v", err)
	}
	back, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(back) != strings.Join(content, "") {
		t.Fatalf("Get 回读不符：%q err=%v", back, err)
	}

	// Abort：临时区清理
	uploadId2, err := driver.InitMultipart(publicPath)
	if err != nil {
		t.Fatalf("再次 InitMultipart 失败：%v", err)
	}
	if _, err = driver.UploadPart(publicPath, uploadId2, 1, strings.NewReader("abort"), 5); err != nil {
		t.Fatalf("UploadPart 失败：%v", err)
	}
	if err = driver.AbortMultipart(publicPath, uploadId2); err != nil {
		t.Fatalf("AbortMultipart 失败：%v", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".multipart", uploadId2)); !os.IsNotExist(err) {
		t.Fatal("Abort 后临时区应已清空")
	}

	// 已清理会话继续上传应报错
	if _, err = driver.UploadPart(publicPath, uploadId2, 1, strings.NewReader("x"), 1); err == nil {
		t.Fatal("会话已清理后继续上传应报错")
	}
}

// TestLocalPutAtomic - 验证 Put 原子写：同名覆盖内容正确、无临时文件残留；List 不暴露 .multipart 临时区
func TestLocalPutAtomic(t *testing.T) {

	root := filepath.Join(t.TempDir(), "storage")
	store := &LocalStore{Config: LocalConfig{Domain: "http://localhost:2000", Root: root}}
	driver := NewDriver(store)

	if resp := driver.Dir("a").Name("f").Ext("txt").Put(strings.NewReader("v1")); resp.Error != nil {
		t.Fatalf("首次上传失败：%v", resp.Error)
	}
	if resp := driver.Dir("a").Name("f").Ext("txt").Put(strings.NewReader("v2-longer-content")); resp.Error != nil {
		t.Fatalf("覆盖上传失败：%v", resp.Error)
	}
	data, err := os.ReadFile(filepath.Join(root, "a", "f.txt"))
	if err != nil || string(data) != "v2-longer-content" {
		t.Fatalf("覆盖后内容不符：%q err=%v", data, err)
	}

	// 无 .tmp 临时文件残留
	entries, err := os.ReadDir(filepath.Join(root, "a"))
	if err != nil {
		t.Fatalf("读目录失败：%v", err)
	}
	for _, item := range entries {
		if strings.Contains(item.Name(), ".tmp-") {
			t.Fatalf("存在临时文件残留：%s", item.Name())
		}
	}

	// List 不暴露 .multipart 临时区
	uploadId, err := driver.InitMultipart("/storage/a/f.txt")
	if err != nil {
		t.Fatalf("InitMultipart 失败：%v", err)
	}
	if _, err = driver.UploadPart("/storage/a/f.txt", uploadId, 1, strings.NewReader("x"), 1); err != nil {
		t.Fatalf("UploadPart 失败：%v", err)
	}
	list := driver.List(ListParams{Dir: "/storage"})
	if list.Error != nil {
		t.Fatalf("List 失败：%v", list.Error)
	}
	for _, entry := range list.List {
		if entry.Name == ".multipart" {
			t.Fatal("List 不应暴露 .multipart 临时区")
		}
	}
}

// TestBuiltinOptionalInterfaces - 验证三内置驱动实现 Multiparter / Getter 可选接口（OSS/COS 仅断言不调用，不联网）
func TestBuiltinOptionalInterfaces(t *testing.T) {

	local, err := newLocalStore(Config{Local: LocalConfig{Root: "public/storage", Domain: "http://localhost:2000"}})
	if err != nil {
		t.Fatalf("构造 local 驱动失败：%v", err)
	}
	if _, ok := local.(Multiparter); !ok {
		t.Fatal("local 驱动应实现 Multiparter")
	}
	if _, ok := local.(Getter); !ok {
		t.Fatal("local 驱动应实现 Getter")
	}

	ossStore, err := newOssStore(Config{OSS: OSSConfig{
		AccessKeyId: "test-access-key", AccessKeySecret: "test-secret", Bucket: "smoke-bucket", Endpoint: "oss-cn-guangzhou.aliyuncs.com", Path: "AIDE",
	}})
	if err != nil {
		t.Fatalf("构造 OSS 驱动失败：%v", err)
	}
	if _, ok := ossStore.(Multiparter); !ok {
		t.Fatal("OSS 驱动应实现 Multiparter")
	}
	if _, ok := ossStore.(Getter); !ok {
		t.Fatal("OSS 驱动应实现 Getter")
	}

	cosStore, err := newCosStore(Config{COS: COSConfig{
		AppId: "1250000000", SecretId: "test-secret-id", SecretKey: "test-secret-key", Bucket: "smoke-bucket", Region: "ap-guangzhou", Path: "AIDE",
	}})
	if err != nil {
		t.Fatalf("构造 COS 驱动失败：%v", err)
	}
	if _, ok := cosStore.(Multiparter); !ok {
		t.Fatal("COS 驱动应实现 Multiparter")
	}
	if _, ok := cosStore.(Getter); !ok {
		t.Fatal("COS 驱动应实现 Getter")
	}
}
