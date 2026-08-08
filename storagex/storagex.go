// Package storagex - 存储包：以接口模式封装本地磁盘、对象存储等文件存储能力
//
// 设计要点：
//   - Store 是唯一扩展点：新后端只需实现 Root/Domain/Put/List/MakeDir/Remove/Move 七个方法
//   - 内置驱动在注册表变量初始化时登记（不依赖文件 init 顺序）；外部驱动在自己包内
//     通过 init() + Register 注册，同名注册会覆盖先注册者（可借此替换内置实现）
//   - 扩展驱动的自定义配置通过 Config.Options 传入（key 为驱动名）
//   - Driver 在 Store 之上提供链式调用（值语义，天然隔离上下文，无需 clone）；
//     对象命名（日期目录 + 时间戳文件名）、公开路径换算与路径穿越防护统一收敛在 Driver 层，
//     驱动只接收相对存储根的安全相对路径，不感知链式参数
//   - Inst + Storage 提供与 facade 层一致的全局单例入口
package storagex

import (
	"context"
	"fmt"
	"io"
	pathpkg "path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 接口与注册表 - 开始 ==================================

// Store - 存储驱动接口：本地磁盘、对象存储及未来接入的后端统一实现该接口
//
// 约定：所有方法的 key / dir / src / dst 均为「相对存储根」的路径（不含根目录名），
// 由 Driver 层完成公开路径换算与路径穿越校验后传入，驱动按原名直接使用即可；
// ctx 为链式 Ctx 注入的上下文（默认 context.Background()）。
type Store interface {
	// Root - 存储根目录名（不带斜杠，如 storage、AIDE），用于公开路径换算
	Root() (root string)
	// Domain - 访问域名（不带尾部斜杠，用于拼接文件 Url）
	Domain() (domain string)
	// Put - 上传对象（key 为相对存储根的路径）
	Put(ctx context.Context, key string, reader io.Reader) (err error)
	// List - 列出目录内容（dir 空串表示存储根；limit 已归一化；返回条目只需填 Name/IsDir/Size/ModTime）
	List(ctx context.Context, dir string, marker string, limit int) (entries []Entry, nextMarker string, err error)
	// MakeDir - 创建目录（对象存储为目录占位对象）
	MakeDir(ctx context.Context, dir string) (err error)
	// Remove - 删除文件或目录（目录递归删除）
	Remove(ctx context.Context, paths ...string) (err error)
	// Move - 移动或重命名（「禁止移动到自身内部」已由 Driver 层校验）
	Move(ctx context.Context, src, dst string) (err error)
}

// Factory - 驱动工厂：按配置构建驱动实例（传入的 Config 已归一化）
type Factory func(config Config) (Store, error)

// registry - 驱动注册表（读写锁保护并发注册与查找）
// 内置驱动在变量初始化时登记，保证包初始化期间即可用；外部驱动通过 Register 注册
var registry = struct {
	sync.RWMutex
	items map[string]Factory
}{items: map[string]Factory{
	"local": newLocalStore,
	"oss":   newOssStore,
	"cos":   newCosStore,
}}

// Register - 注册存储驱动
/**
 * @param name    string  - 驱动名称（不区分大小写，同名后注册覆盖先注册）
 * @param factory Factory - 驱动工厂
 * @example：
 * 	func init() { storagex.Register("qiniu", newQiniuStore) }
 */
func Register(name string, factory Factory) {
	name = strings.ToLower(strings.TrimSpace(name))
	if utils.Is.Empty(name) {
		panic("storagex: 驱动名称不能为空")
	}
	if factory == nil {
		panic("storagex: 驱动[" + name + "]工厂不能为空")
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
		return nil, fmt.Errorf("storagex: 未注册的驱动[%s]（可用: %s）", name, strings.Join(Names(), ", "))
	}
	return factory(config)
}

// New - 按驱动名称与配置创建链式存储实例
/**
 * @param name   string - 驱动名称（local / oss / cos / 自定义注册名）
 * @param config Config - 存储配置
 * @example：
 * 	driver, err := storagex.New("oss", storagex.Config{...})
 * 	resp := driver.Dir("media").Name("cover").Ext("jpg").Put(reader)
 */
func New(name string, config Config) (Driver, error) {
	conf := normConfig(config)
	store, err := open(name, conf)
	if err != nil {
		return Driver{}, err
	}
	return NewDriver(store), nil
}

// ================================== 接口与注册表 - 结束 ==================================

// ================================== 响应与条目 - 开始 ==================================

// Resp - 存储操作响应
type Resp struct {
	// 操作错误（nil 表示成功）
	Error error `json:"-"`
	// 公开路径（带前导 /，如 /storage/2026-08/08/a.jpg）
	Path string `json:"path"`
	// 完整访问地址（Domain + Path，仅文件有效）
	Url string `json:"url"`
	// 访问域名
	Domain string `json:"domain"`
	// 文件或目录名称
	Name string `json:"name"`
}

// Entry - 存储条目（文件或目录）
type Entry struct {
	// 条目名称
	Name string `json:"name"`
	// 公开路径（带前导 /）
	Path string `json:"path"`
	// 访问地址（仅文件有效）
	Url string `json:"url"`
	// 文件大小（字节）
	Size int64 `json:"size"`
	// 是否目录
	IsDir bool `json:"isDir"`
	// 修改时间（毫秒时间戳）
	ModTime int64 `json:"modTime"`
}

// ListParams - 列目录参数
type ListParams struct {
	// 目标目录（公开路径，空串表示存储根）
	Dir string `json:"dir"`
	// 分页标记（上一页响应的 NextMarker）
	Marker string `json:"marker"`
	// 每页数量（默认 100，上限 1000）
	Limit int `json:"limit"`
	// 名称前缀过滤（页内过滤，云端分页场景命中数量可能少于 Limit）
	Prefix string `json:"prefix"`
}

// ListResp - 列目录响应
type ListResp struct {
	// 操作错误（nil 表示成功）
	Error error `json:"-"`
	// 存储根公开路径（如 /storage、/AIDE）
	Root string `json:"root"`
	// 条目列表（目录在前，文件在后）
	List []Entry `json:"list"`
	// 下一页分页标记，空串表示没有更多
	NextMarker string `json:"nextMarker"`
}

// ================================== 响应与条目 - 结束 ==================================

// ================================== 链式存储实例 - 开始 ==================================

// Driver - 链式存储实例：在 Store 之上提供链式上下文（值语义，每次调用返回副本）
type Driver struct {
	// 底层存储驱动
	store Store
	// 请求上下文（nil 时按 context.Background() 处理）
	ctx context.Context
	// 上传目录（cleanDir 清理后，带尾部 /）
	dir string
	// 上传文件名（已去目录成分）
	name string
	// 上传文件后缀（带前导 .）
	ext string
}

// NewDriver - 用 Store 包装出链式存储实例
func NewDriver(store Store) Driver {
	return Driver{store: store}
}

// Store - 取出底层存储驱动（供类型断言访问驱动特有方法）
func (this Driver) Store() Store {
	return this.store
}

// Ctx - 请求上下文（控制云存储操作的取消与超时）
func (this Driver) Ctx(ctx context.Context) Driver {
	this.ctx = ctx
	return this
}

// Dir - 上传目录（相对存储根，自动清理 .. 与多余分隔符）
/**
 * @example：
 * 	storagex.Storage.Dir("media/2026").Name("cover").Ext("jpg").Put(reader)
 */
func (this Driver) Dir(dir string) Driver {
	this.dir = cleanDir(dir)
	return this
}

// Name - 上传文件名（自动去除目录成分，为空时按时间戳生成）
func (this Driver) Name(name string) Driver {
	this.name = fileNameFromPath(name)
	return this
}

// Ext - 上传文件后缀（自动补前导 .）
func (this Driver) Ext(ext string) Driver {
	this.ext = cleanExt(ext)
	return this
}

// context - 取请求上下文（未设置时回退 Background）
func (this Driver) context() context.Context {
	if this.ctx == nil {
		return context.Background()
	}
	return this.ctx
}

// Put - 上传文件（目录默认按 年-月/日 生成，文件名默认按毫秒时间戳生成）
/**
 * @param reader io.Reader - 文件内容读取器
 * @example：
 * 	resp := storagex.Storage.Dir("media").Ext(".png").Put(file)
 * 	if resp.Error == nil { fmt.Println(resp.Url) }
 */
func (this Driver) Put(reader io.Reader) (response *Resp) {

	response = &Resp{}
	if this.store == nil {
		response.Error = fmt.Errorf("storagex: 存储驱动未初始化")
		return
	}
	if reader == nil {
		response.Error = fmt.Errorf("storagex: 文件内容不能为空")
		return
	}

	// 目录 - 缺省按 年-月/日 归档，如 2026-08/08/
	dir := this.dir
	if utils.Is.Empty(dir) {
		dir = time.Now().Format("2006-01/02/")
	}

	// 文件名 - 缺省为毫秒时间戳 + 随机后缀（防同毫秒并发撞名）
	name := this.name
	if utils.Is.Empty(name) {
		name = cast.ToString(time.Now().UnixMilli()) + "-" + utils.Rand.String(6)
	}

	key := dir + name + this.ext
	if err := this.store.Put(this.context(), key, reader); err != nil {
		response.Error = err
		return
	}

	response.Path = joinPublicPath(this.store.Root(), key)
	response.Domain = strings.TrimSuffix(this.store.Domain(), "/")
	response.Url = response.Domain + response.Path
	response.Name = pathpkg.Base(key)
	return
}

// List - 列出目录内容（目录在前，文件在后）
func (this Driver) List(params ListParams) (response *ListResp) {

	response = &ListResp{List: []Entry{}}
	if this.store == nil {
		response.Error = fmt.Errorf("storagex: 存储驱动未初始化")
		return
	}

	root := this.store.Root()
	response.Root = joinPublicPath(root, "")

	rel, ok := splitPublicPath(root, params.Dir)
	if !ok {
		response.Error = fmt.Errorf("storagex: 目录越出存储根")
		return
	}

	entries, nextMarker, err := this.store.List(this.context(), rel, params.Marker, normListLimit(params.Limit))
	if err != nil {
		response.Error = err
		return
	}

	// 组装公开路径与访问地址，名称前缀页内过滤
	domain := strings.TrimSuffix(this.store.Domain(), "/")
	for _, entry := range entries {
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(entry.Name, params.Prefix) {
			continue
		}
		entry.Path = joinPublicPath(root, strings.Trim(strings.Join([]string{rel, entry.Name}, "/"), "/"))
		if !entry.IsDir {
			entry.Url = domain + entry.Path
		}
		response.List = append(response.List, entry)
	}
	response.NextMarker = nextMarker
	return
}

// MakeDir - 创建目录
/**
 * @param dir string - 公开路径（如 /storage/media）
 */
func (this Driver) MakeDir(dir string) (response *Resp) {

	response = &Resp{}
	if this.store == nil {
		response.Error = fmt.Errorf("storagex: 存储驱动未初始化")
		return
	}

	root := this.store.Root()
	rel, ok := splitPublicPath(root, dir)
	if !ok || utils.Is.Empty(rel) {
		response.Error = fmt.Errorf("storagex: 目录路径不合法")
		return
	}

	if err := this.store.MakeDir(this.context(), rel); err != nil {
		response.Error = err
		return
	}

	response.Path = joinPublicPath(root, rel)
	response.Name = pathpkg.Base(rel)
	return
}

// Remove - 删除文件或目录（目录递归删除）
/**
 * @param paths ...string - 公开路径列表
 */
func (this Driver) Remove(paths ...string) (response *Resp) {

	response = &Resp{}
	if this.store == nil {
		response.Error = fmt.Errorf("storagex: 存储驱动未初始化")
		return
	}

	root := this.store.Root()
	rels := make([]string, 0, len(paths))
	for _, item := range paths {
		rel, ok := splitPublicPath(root, item)
		if !ok || utils.Is.Empty(rel) {
			response.Error = fmt.Errorf("storagex: 路径不合法或越出存储根：%s", item)
			return
		}
		rels = append(rels, rel)
	}

	response.Error = this.store.Remove(this.context(), rels...)
	return
}

// Move - 移动或重命名文件/目录
/**
 * @param src string - 源公开路径
 * @param dst string - 目标公开路径
 */
func (this Driver) Move(src, dst string) (response *Resp) {

	response = &Resp{}
	if this.store == nil {
		response.Error = fmt.Errorf("storagex: 存储驱动未初始化")
		return
	}

	root := this.store.Root()
	srcRel, ok := splitPublicPath(root, src)
	if !ok || utils.Is.Empty(srcRel) {
		response.Error = fmt.Errorf("storagex: 源路径不合法或越出存储根")
		return
	}
	dstRel, ok := splitPublicPath(root, dst)
	if !ok || utils.Is.Empty(dstRel) {
		response.Error = fmt.Errorf("storagex: 目标路径不合法或越出存储根")
		return
	}
	// 禁止移动到自身内部
	if dstRel == srcRel || strings.HasPrefix(dstRel, srcRel+"/") {
		response.Error = fmt.Errorf("storagex: 不能移动到自身内部")
		return
	}

	if err := this.store.Move(this.context(), srcRel, dstRel); err != nil {
		response.Error = err
		return
	}

	response.Path = joinPublicPath(root, dstRel)
	response.Name = pathpkg.Base(dstRel)
	return
}

// Root - 存储根公开路径（如 /storage、/AIDE）
func (this Driver) Root() string {
	if this.store == nil {
		return ""
	}
	return joinPublicPath(this.store.Root(), "")
}

// ================================== 链式存储实例 - 结束 ==================================

// ================================== 路径工具 - 开始 ==================================

// cleanDir - 标准化目录：清理多余分隔符并确保以 / 结尾；显式拒绝 .. 段
// （path.Clean 会把越界 .. 静默吸收成根内路径，掩盖调用方错误，故先行拦截）
func cleanDir(dir string) string {
	if utils.Is.Empty(dir) {
		return ""
	}
	dir = strings.ReplaceAll(dir, "\\", "/")
	for _, seg := range strings.Split(dir, "/") {
		if seg == ".." {
			return ""
		}
	}
	dir = strings.TrimPrefix(pathpkg.Clean("/"+dir), "/")
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}

// cleanExt - 标准化后缀，确保以 . 开头
func cleanExt(ext string) string {
	if !utils.Is.Empty(ext) && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// fileNameFromPath - 提取路径中的文件名（去除目录成分）
func fileNameFromPath(path string) string {
	name := pathpkg.Base(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// splitPublicPath - 将公开路径转换为相对存储根的路径
// root 为存储根目录名（storage、AIDE），返回空串表示存储根本身，false 表示路径非法或越界
func splitPublicPath(root, path string) (string, bool) {
	root = strings.Trim(strings.TrimSpace(root), "/")
	path = strings.Trim(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "/")
	if utils.Is.Empty(root) {
		return "", false
	}
	if utils.Is.Empty(path) || path == root {
		return "", true
	}
	if !strings.HasPrefix(path, root+"/") {
		return "", false
	}
	rel := pathpkg.Clean(strings.TrimPrefix(path, root+"/"))
	// 拒绝越界字符
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return rel, true
}

// joinPublicPath - 拼接公开路径（带前导 /）
func joinPublicPath(root, rel string) string {
	root = strings.Trim(strings.TrimSpace(root), "/")
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if utils.Is.Empty(rel) {
		return "/" + root
	}
	return "/" + root + "/" + rel
}

// normListLimit - 标准化列目录每页数量
func normListLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 100
	}
	return limit
}

// ================================== 路径工具 - 结束 ==================================

// storeError - 初始化失败的驱动占位：所有操作返回原始初始化错误
type storeError struct {
	// 驱动名称
	name string
	// 初始化错误
	err error
}

// Root - 占位实现，返回空串
func (this storeError) Root() string { return "" }

// Domain - 占位实现，返回空串
func (this storeError) Domain() string { return "" }

// Put - 占位实现，返回初始化错误
func (this storeError) Put(context.Context, string, io.Reader) error { return this.err }

// List - 占位实现，返回初始化错误
func (this storeError) List(context.Context, string, string, int) ([]Entry, string, error) {
	return nil, "", this.err
}

// MakeDir - 占位实现，返回初始化错误
func (this storeError) MakeDir(context.Context, string) error { return this.err }

// Remove - 占位实现，返回初始化错误
func (this storeError) Remove(context.Context, ...string) error { return this.err }

// Move - 占位实现，返回初始化错误
func (this storeError) Move(context.Context, string, string) error { return this.err }

// 编译期接口校验
var _ Store = storeError{}
