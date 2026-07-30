package facade

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	
	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/inis-io/aide/dto"
	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
	"github.com/tencentyun/cos-go-sdk-v5"
)

var StorageInst = &StorageClass{}

type StorageClass struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash      string            `json:"hash"`
	// 当前存储配置（由调用方注入）
	Config    dto.StorageConfig `json:"config"`
	// 是否已经注入过配置
	HasConfig bool              `json:"hasConfig"`
	// 读写锁，保护配置和Hash的并发访问
	Mutex     sync.RWMutex
}

func init() { StorageInst.Init() }

// normConfig 统一配置默认值，避免不同项目接入时行为不一致
func (this *StorageClass) normConfig(config dto.StorageConfig) dto.StorageConfig {

	config.Engine = strings.ToLower(strings.TrimSpace(config.Engine))
	switch config.Engine {
	case "oss", "cos", "local":
	default:
		config.Engine = "local"
	}

	if utils.Is.Empty(config.Local.Domain) {
		config.Local.Domain = "http://localhost:2000"
	}

	if utils.Is.Empty(config.OSS.Endpoint) {
		config.OSS.Endpoint = "oss-cn-guangzhou.aliyuncs.com"
	}
	if utils.Is.Empty(config.OSS.Path) {
		config.OSS.Path = "AIDE"
	}

	if utils.Is.Empty(config.COS.Region) {
		config.COS.Region = "ap-guangzhou"
	}
	if utils.Is.Empty(config.COS.Path) {
		config.COS.Path = "AIDE"
	}

	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}

	return config

}

// defaultConfig - 获取默认存储配置
func (this *StorageClass) defaultConfig() dto.StorageConfig {
	return StorageInst.normConfig(dto.StorageConfig{})
}

// useDefaultStorage - 使用默认配置激活存储
func (this *StorageClass) useDefaultStorage() {
	conf := StorageInst.defaultConfig()

	this.Mutex.Lock()
	this.Config = conf
	this.Hash = conf.Hash
	this.HasConfig = false
	this.Mutex.Unlock()

	StorageInst.setActiveStorage(conf)
}

// setActiveStorage - 按配置切换当前活动存储实现
func (this *StorageClass) setActiveStorage(config dto.StorageConfig) {

	conf := StorageInst.normConfig(config)

	this.Mutex.Lock()
	this.Config = conf
	this.Mutex.Unlock()

	Storage = StorageInst.newWithConfig(conf)

	LocalStorage = nil
	OSS = nil
	COS = nil

	switch impl := Storage.(type) {
	case *LocalStorageClass:
		LocalStorage = impl
	case *OssClass:
		OSS = impl
	case *CosClass:
		COS = impl
	}
}

// newWithConfig - 按配置创建新的存储实现
func (this *StorageClass) newWithConfig(config dto.StorageConfig) StorageAPI {
	conf := StorageInst.normConfig(config)

	switch conf.Engine {
	case "oss":
		item := &OssClass{Config: conf}
		item.Init()
		if item.Client != nil {
			return item
		}
	case "cos":
		item := &CosClass{Config: conf}
		item.Init()
		if item.Client != nil {
			return item
		}
	}

	return &LocalStorageClass{Config: conf}
}

// setConfig - 注入存储配置
func (this *StorageClass) setConfig(config dto.StorageConfig) *StorageClass {
	this.Mutex.Lock()
	defer this.Mutex.Unlock()

	this.Config = StorageInst.normConfig(config)
	this.HasConfig = true
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载存储
func (this *StorageClass) ReloadIfChanged(config ...dto.StorageConfig) {

	if len(config) > 0 {
		this.setConfig(config[0])
	}

	this.Mutex.RLock()
	hasConfig := this.HasConfig
	hash := this.Hash
	confHash := this.Config.Hash
	this.Mutex.RUnlock()

	if !hasConfig {
		return
	}

	// hash 变化，说明配置有更新
	if hash != confHash {
		this.Init()
	}
}

// Init 初始化
func (this *StorageClass) Init(config ...dto.StorageConfig) {

	if len(config) > 0 {
		this.setConfig(config[0])
	}

	this.Mutex.RLock()
	hasConfig := this.HasConfig
	current := this.Config
	this.Mutex.RUnlock()

	if !hasConfig {
		StorageInst.useDefaultStorage()
		return
	}

	conf := StorageInst.normConfig(current)

	this.Mutex.Lock()
	this.Config = conf
	this.Hash = conf.Hash
	this.Mutex.Unlock()

	StorageInst.setActiveStorage(conf)

}

// Storage - Storage实例
/**
 * @return StorageAPI
 * @example：
 * storage := facade.Storage.Upload(facade.Storage.Path() + suffix, bytes)
 */
var Storage StorageAPI
var OSS  *OssClass
var COS  *CosClass
var LocalStorage *LocalStorageClass


// StorageResp - 存储响应
type StorageResp struct {
	Error  error
	Path   string
	Domain string
	Name   string
}

// StorageParams - 存储参数
type StorageParams struct {
	// Dir - 存储目录
	Dir string
	// Name - 存储文件名
	Name string
	// Ext - 存储文件后缀
	Ext string
}

// StorageAPI 定义了存储操作的接口。
type StorageAPI interface {
	// Upload 上传文件
	/**
	 * @param reader io.Reader - 读取器
	 * @returns StorageAPI - 存储接口
	 */
	Upload(reader io.Reader) *StorageResp

	// Dir 设置存储的目录
	/**
	 * @param dir string - 目录
	 * @returns StorageAPI - 存储接口
	 */
	Dir(dir string) StorageAPI

	// Name 设置存储文件的名称
	/**
	 * @param name string - 名称
	 * @returns StorageAPI - 存储接口
	 */
	Name(name string) StorageAPI

	// Ext 设置存储文件的后缀
	/**
	 * @param ext string - 后缀
	 * @returns StorageAPI - 存储接口
	 */
	Ext(ext string) StorageAPI

	// List 列出目录内容（目录在前，文件在后）
	/**
	 * @param params StorageListParams - 列目录参数
	 * @returns StorageListResp - 列目录响应
	 */
	List(params StorageListParams) *StorageListResp

	// MakeDir 创建目录
	/**
	 * @param dir string - 目录（公开路径，如 /storage/media）
	 * @returns StorageResp - 存储响应
	 */
	MakeDir(dir string) *StorageResp

	// Remove 删除文件或目录（目录递归删除）
	/**
	 * @param paths ...string - 公开路径列表
	 * @returns StorageResp - 存储响应
	 */
	Remove(paths ...string) *StorageResp

	// Move 移动或重命名文件/目录
	/**
	 * @param src string - 源公开路径
	 * @param dst string - 目标公开路径
	 * @returns StorageResp - 存储响应
	 */
	Move(src, dst string) *StorageResp

	// NewStorage - 使用传入配置创建新的存储实例
	NewStorage(config dto.StorageConfig) StorageAPI
}

// cleanDir - 标准化目录，确保目录以 / 结尾
func (this *StorageClass) cleanDir(dir string) string {
	if !utils.Is.Empty(dir) && !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}

// cleanExt - 标准化后缀，确保以 . 开头
func (this *StorageClass) cleanExt(ext string) string {
	if !utils.Is.Empty(ext) && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return ext
}

// fileNameFromPath - 提取路径中的文件名
func (this *StorageClass) fileNameFromPath(path string) string {
	name := pathpkg.Base(strings.TrimSpace(path))
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// =================================== 本地存储存储 - 开始 ===================================

// LocalStorageClass 本地存储
type LocalStorageClass struct {
	// 配置
	Config dto.StorageConfig
	// 参数
	Params StorageParams
}

// clone - 克隆本地存储实例（共享配置，隔离链式参数）
func (this *LocalStorageClass) clone() *LocalStorageClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Upload - 上传文件
func (this *LocalStorageClass) Upload(reader io.Reader) (response *StorageResp) {

	response = &StorageResp{}

	path := this.Path()
	item := utils.File().Save(reader, path)

	if item.Error != nil {
		response.Error = item.Error
		return
	}

	// 去除前面的 public
	response.Path   = strings.Replace(path, "public", "", 1)
	response.Domain = this.Config.Local.Domain
	
	response.Name = StorageInst.fileNameFromPath(path)
	
	return
}

// Path - 本地存储位置 - 生成文件路径
func (this *LocalStorageClass) Path() (path string) {

	// 生成文件名 - 年月日+毫秒时间戳
	name := cast.ToString(time.Now().UnixNano() / 1e6)
	// 生成年月日目录 - 如：2023-04/10
	dir  := time.Now().Format("2006-01/02/")

	// 自定义目录
	if !utils.Is.Empty(this.Params.Dir) {
		dir = this.Params.Dir
	}
	// 自定义文件名
	if !utils.Is.Empty(this.Params.Name) {
		name = this.Params.Name
	}

	// 得到文件路径 - 但是可能还存在重复的 /
	path = strings.Join([]string{"public", "storage", dir}, "/")
	// 替换重复的 / - 重新生成文件路径
	path = strings.Join(cast.ToStringSlice(utils.ArrayEmpty(strings.Split(path, "/"))), "/")
	// 如果不是以 / 结尾
	if !strings.HasSuffix(path, "/") { path += "/" }

	return path + name + this.Params.Ext
}

// Dir - 本地存储位置 - 生成文件目录
func (this *LocalStorageClass) Dir(dir string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Dir = StorageInst.cleanDir(dir)
	return item
}

// Name - 本地存储位置 - 生成文件名
func (this *LocalStorageClass) Name(name string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Name = name
	return item
}

// Ext - 本地存储位置 - 生成文件后缀
func (this *LocalStorageClass) Ext(ext string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Ext = StorageInst.cleanExt(ext)
	return item
}

// NewStorage - 使用传入配置创建存储实例
func (this *LocalStorageClass) NewStorage(config dto.StorageConfig) StorageAPI {
	return StorageInst.newWithConfig(config)
}

// ================================== 阿里云对象存储 - 开始 ==================================

// OssClass 阿里云对象存储
type OssClass struct {
	// OSS客户端
	Client *oss.Client
	// 配置
	Config dto.StorageConfig
	// 参数
	Params StorageParams
}

// clone - 克隆 OSS 存储实例（共享客户端，隔离链式参数）
func (this *OssClass) clone() *OssClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Init 初始化 阿里云对象存储
func (this *OssClass) Init() {
	this.Config = StorageInst.normConfig(this.Config)

	client, err := oss.New(this.Config.OSS.Endpoint, this.Config.OSS.AccessKeyId, this.Config.OSS.AccessKeySecret)

	if err != nil {
		return
	}

	this.Client = client
}

// Bucket - 获取Bucket（存储桶）
func (this *OssClass) Bucket() *oss.Bucket {
	if this.Client == nil {
		return nil
	}

	exist, err := this.Client.IsBucketExist(this.Config.OSS.Bucket)

	if err != nil {
		return nil
	}

	if !exist {
		// 创建存储空间。
		err = this.Client.CreateBucket(this.Config.OSS.Bucket)
		if err != nil {
			return nil
		}
	}

	bucket, err := this.Client.Bucket(this.Config.OSS.Bucket)
	if err != nil {
		return nil
	}

	return bucket
}

// Upload - 上传文件
func (this *OssClass) Upload(reader io.Reader) (response *StorageResp) {

	response = &StorageResp{}

	path   := this.Path()
	bucket := this.Bucket()
	if bucket == nil {
		response.Error = fmt.Errorf("OSS Bucket 获取失败")
		return
	}
	if err := bucket.PutObject(path, reader); err != nil {
		response.Error = err
		return
	}

	if utils.Is.Empty(this.Config.OSS.Domain) {
		response.Domain = "https://" + this.Config.OSS.Bucket + "." + this.Config.OSS.Endpoint
	} else {
		response.Domain = this.Config.OSS.Domain
	}

	response.Path = "/" + path
	
	response.Name = StorageInst.fileNameFromPath(path)
	
	return
}

// Path - OSS存储位置 - 生成文件路径
func (this *OssClass) Path() (path string) {

	// 生成文件名 - 年月日+毫秒时间戳
	name := cast.ToString(time.Now().UnixNano() / 1e6)
	// 存储根目录
	root := this.Config.OSS.Path
	// 生成年月日目录 - 如：2023-04/10
	dir  := time.Now().Format("2006-01/02/")

	// 自定义目录
	if !utils.Is.Empty(this.Params.Dir) {
		dir = this.Params.Dir
	}
	// 自定义文件名
	if !utils.Is.Empty(this.Params.Name) {
		name = this.Params.Name
	}

	// 得到文件路径 - 但是可能还存在重复的 /
	path = strings.Join([]string{root, dir}, "/")
	// 替换重复的 / - 重新生成文件路径
	path = strings.Join(cast.ToStringSlice(utils.ArrayEmpty(strings.Split(path, "/"))), "/")
	// 如果不是以 / 结尾
	if !strings.HasSuffix(path, "/") { path += "/" }

	return path + name + this.Params.Ext
}

// Dir - 本地存储位置 - 生成文件目录
func (this *OssClass) Dir(dir string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Dir = StorageInst.cleanDir(dir)
	return item
}

// Name - 本地存储位置 - 生成文件名
func (this *OssClass) Name(name string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Name = name
	return item
}

// Ext - 本地存储位置 - 生成文件后缀
func (this *OssClass) Ext(ext string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Ext = StorageInst.cleanExt(ext)
	return item
}

// NewStorage - 使用传入配置创建存储实例
func (this *OssClass) NewStorage(config dto.StorageConfig) StorageAPI {
	return StorageInst.newWithConfig(config)
}

// ================================== 腾讯云对象存储 - 开始 ==================================

// CosClass 腾讯云对象存储
type CosClass struct {
	// COS客户端
	Client *cos.Client
	// 配置
	Config dto.StorageConfig
	// 参数
	Params StorageParams
}

// clone - 克隆 COS 存储实例（共享客户端，隔离链式参数）
func (this *CosClass) clone() *CosClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Init 初始化 腾讯云对象存储
func (this *CosClass) Init() {
	this.Config = StorageInst.normConfig(this.Config)

	cosUrl, err := url.Parse(fmt.Sprintf("https://%s-%s.cos.%s.myqcloud.com", this.Config.COS.Bucket, this.Config.COS.AppId, this.Config.COS.Region))
	if err != nil {
		return
	}

	this.Client = cos.NewClient(&cos.BaseURL{
		BucketURL: cosUrl,
	}, &http.Client{
		// 设置超时时间
		Timeout: 100 * time.Second,
		Transport: &cos.AuthorizationTransport{
			SecretID:  this.Config.COS.SecretId,
			SecretKey: this.Config.COS.SecretKey,
		},
	})
}

// Object - 获取Object（对象存储）
func (this *CosClass) Object() *cos.ObjectService {
	if this.Client == nil {
		return nil
	}

	// 查询存储桶
	exist, err := this.Client.Bucket.IsExist(context.Background())

	if err != nil {
		return nil
	}

	if !exist {
		// 创建存储桶 - 默认公共读私有写
		_, err = this.Client.Bucket.Put(context.Background(), &cos.BucketPutOptions{
			XCosACL: "public-read",
		})
		if err != nil {
			return nil
		}
	}

	return this.Client.Object
}

// Upload - 上传文件
func (this *CosClass) Upload(reader io.Reader) (response *StorageResp) {

	response = &StorageResp{}

	path := this.Path()
	object := this.Object()
	if object == nil {
		response.Error = fmt.Errorf("COS Object 获取失败")
		return
	}

	_, err := object.Put(context.Background(), path, reader, nil)
	if err != nil {
		response.Error = err
		return
	}

	if utils.Is.Empty(this.Config.COS.Domain) {
		response.Domain = fmt.Sprintf("https://%s-%s.cos.%s.myqcloud.com", this.Config.COS.Bucket, this.Config.COS.AppId, this.Config.COS.Region)
	} else {
		response.Domain = this.Config.COS.Domain
	}

	response.Path = "/" + path
	
	response.Name = StorageInst.fileNameFromPath(path)

	return
}

// Path - COS存储位置 - 生成文件路径
func (this *CosClass) Path() (path string) {

	// 生成文件名 - 年月日+毫秒时间戳
	name := cast.ToString(time.Now().UnixNano() / 1e6)
	// 存储根目录
	root := this.Config.COS.Path
	// 生成年月日目录 - 如：2023-04/10
	dir  := time.Now().Format("2006-01/02/")

	// 自定义目录
	if !utils.Is.Empty(this.Params.Dir) {
		dir = this.Params.Dir
	}
	// 自定义文件名
	if !utils.Is.Empty(this.Params.Name) {
		name = this.Params.Name
	}

	// 得到文件路径 - 但是可能还存在重复的 /
	path = strings.Join([]string{root, dir}, "/")
	// 替换重复的 / - 重新生成文件路径
	path = strings.Join(cast.ToStringSlice(utils.ArrayEmpty(strings.Split(path, "/"))), "/")
	// 如果不是以 / 结尾
	if !strings.HasSuffix(path, "/") { path += "/" }

	return path + name + this.Params.Ext
}

// Dir - 本地存储位置 - 生成文件目录
func (this *CosClass) Dir(dir string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Dir = StorageInst.cleanDir(dir)
	return item
}

// Name - 本地存储位置 - 生成文件名
func (this *CosClass) Name(name string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Name = name
	return item
}

// Ext - 本地存储位置 - 生成文件后缀
func (this *CosClass) Ext(ext string) StorageAPI {
	item := this.clone()
	if item == nil {
		return this
	}
	item.Params.Ext = StorageInst.cleanExt(ext)
	return item
}

// NewStorage - 使用传入配置创建存储实例
func (this *CosClass) NewStorage(config dto.StorageConfig) StorageAPI {
	return StorageInst.newWithConfig(config)
}

// ================================== 存储文件管理 - 开始 ==================================

// StorageEntry - 存储条目（文件或目录）
type StorageEntry struct {
	// Name - 条目名称
	Name string `json:"name"`
	// Path - 公开路径（与 Upload 返回的 Path 一致，带前导 /）
	Path string `json:"path"`
	// Url - 访问地址（仅文件有效）
	Url string `json:"url"`
	// Size - 文件大小（字节）
	Size int64 `json:"size"`
	// IsDir - 是否目录
	IsDir bool `json:"isDir"`
	// ModTime - 修改时间（毫秒时间戳）
	ModTime int64 `json:"modTime"`
}

// StorageListParams - 列目录参数
type StorageListParams struct {
	// Dir - 目标目录（公开路径，空串表示存储根）
	Dir string `json:"dir"`
	// Marker - 分页标记（上一页响应的 NextMarker）
	Marker string `json:"marker"`
	// Limit - 每页数量（默认 100，上限 1000）
	Limit int `json:"limit"`
	// Prefix - 名称前缀过滤（可选）
	Prefix string `json:"prefix"`
}

// StorageListResp - 列目录响应
type StorageListResp struct {
	Error error `json:"-"`
	// Root - 存储根公开路径（如 /storage、/AIDE）
	Root string `json:"root"`
	// List - 条目列表（目录在前，文件在后）
	List []StorageEntry `json:"list"`
	// NextMarker - 下一页分页标记，空串表示没有更多
	NextMarker string `json:"nextMarker"`
}

// splitPublicPath - 将公开路径转换为相对存储根的路径
// root 为引擎存储根（local 固定 storage，oss/cos 为配置的 Path），返回空串表示存储根本身
func (this *StorageClass) splitPublicPath(root, path string) (string, bool) {
	root = strings.Trim(strings.TrimSpace(root), "/")
	path = strings.Trim(strings.TrimSpace(strings.ReplaceAll(path, "\\", "/")), "/")
	if root == "" {
		return "", false
	}
	if path == "" || path == root {
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
func (this *StorageClass) joinPublicPath(root, rel string) string {
	root = strings.Trim(strings.TrimSpace(root), "/")
	rel = strings.Trim(strings.TrimSpace(rel), "/")
	if rel == "" {
		return "/" + root
	}
	return "/" + root + "/" + rel
}

// normListLimit - 标准化列目录每页数量
func (this *StorageClass) normListLimit(limit int) int {
	if limit <= 0 || limit > 1000 {
		return 100
	}
	return limit
}

// ================================== 本地存储文件管理 - 开始 ==================================

// localManageRoot - 本地存储文件管理根目录（相对 public）
const localManageRoot = "storage"

// localFsPath - 相对路径转本地文件系统路径
func (this *LocalStorageClass) localFsPath(rel string) string {
	return filepath.Join("public", localManageRoot, rel)
}

// List - 列出目录内容
func (this *LocalStorageClass) List(params StorageListParams) (response *StorageListResp) {

	response = &StorageListResp{Root: "/" + localManageRoot, List: []StorageEntry{}}

	rel, ok := StorageInst.splitPublicPath(localManageRoot, params.Dir)
	if !ok {
		response.Error = fmt.Errorf("目录越出存储根")
		return
	}

	entries, err := os.ReadDir(this.localFsPath(rel))
	if err != nil {
		// 目录不存在视为空目录（新用户尚未上传过文件的场景）
		if os.IsNotExist(err) {
			return
		}
		response.Error = err
		return
	}

	// 收集条目 - 目录与文件分开，便于目录前置
	var dirs, files []StorageEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// 名称前缀过滤
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(entry.Name(), params.Prefix) {
			continue
		}
		item := StorageEntry{
			Name:    entry.Name(),
			Path:    StorageInst.joinPublicPath(localManageRoot, strings.Trim(strings.Join([]string{rel, entry.Name()}, "/"), "/")),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime().UnixMilli(),
		}
		if entry.IsDir() {
			dirs = append(dirs, item)
			continue
		}
		item.Size = info.Size()
		item.Url = this.Config.Local.Domain + item.Path
		files = append(files, item)
	}

	// 目录按名称排序，文件按修改时间倒序
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })
	all := append(dirs, files...)

	// 内存分页 - Marker 为偏移量
	offset := cast.ToInt(params.Marker)
	if offset < 0 {
		offset = 0
	}
	if offset > len(all) {
		offset = len(all)
	}
	limit := StorageInst.normListLimit(params.Limit)
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	response.List = all[offset:end]
	if end < len(all) {
		response.NextMarker = cast.ToString(end)
	}
	return
}

// MakeDir - 创建目录
func (this *LocalStorageClass) MakeDir(dir string) (response *StorageResp) {

	response = &StorageResp{}

	rel, ok := StorageInst.splitPublicPath(localManageRoot, dir)
	if !ok || rel == "" {
		response.Error = fmt.Errorf("目录路径不合法")
		return
	}
	if err := os.MkdirAll(this.localFsPath(rel), 0755); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(localManageRoot, rel)
	response.Name = pathpkg.Base(rel)
	return
}

// Remove - 删除文件或目录（目录递归删除）
func (this *LocalStorageClass) Remove(paths ...string) (response *StorageResp) {

	response = &StorageResp{}

	for _, item := range paths {
		rel, ok := StorageInst.splitPublicPath(localManageRoot, item)
		if !ok || rel == "" {
			response.Error = fmt.Errorf("路径不合法或越出存储根：%s", item)
			return
		}
		if err := os.RemoveAll(this.localFsPath(rel)); err != nil {
			response.Error = err
			return
		}
	}
	return
}

// Move - 移动或重命名文件/目录
func (this *LocalStorageClass) Move(src, dst string) (response *StorageResp) {

	response = &StorageResp{}

	srcRel, ok := StorageInst.splitPublicPath(localManageRoot, src)
	if !ok || srcRel == "" {
		response.Error = fmt.Errorf("源路径不合法或越出存储根")
		return
	}
	dstRel, ok := StorageInst.splitPublicPath(localManageRoot, dst)
	if !ok || dstRel == "" {
		response.Error = fmt.Errorf("目标路径不合法或越出存储根")
		return
	}
	// 禁止移动到自身内部
	if dstRel == srcRel || strings.HasPrefix(dstRel, srcRel+"/") {
		response.Error = fmt.Errorf("不能移动到自身内部")
		return
	}
	// 目标父目录不存在则创建
	if err := os.MkdirAll(filepath.Dir(this.localFsPath(dstRel)), 0755); err != nil {
		response.Error = err
		return
	}
	if err := os.Rename(this.localFsPath(srcRel), this.localFsPath(dstRel)); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(localManageRoot, dstRel)
	response.Name = pathpkg.Base(dstRel)
	return
}

// ================================== 阿里云对象存储文件管理 - 开始 ==================================

// manageBucket - 获取 Bucket（文件管理场景，不触发自动创建存储空间）
func (this *OssClass) manageBucket() (*oss.Bucket, error) {
	if this.Client == nil {
		return nil, fmt.Errorf("OSS Client 未初始化")
	}
	return this.Client.Bucket(this.Config.OSS.Bucket)
}

// manageRoot - OSS 存储根目录
func (this *OssClass) manageRoot() string {
	return strings.Trim(this.Config.OSS.Path, "/")
}

// manageKey - 相对路径转 OSS 对象 Key
func (this *OssClass) manageKey(rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return this.manageRoot()
	}
	return this.manageRoot() + "/" + rel
}

// manageDomain - OSS 访问域名（未配置则使用默认域名）
func (this *OssClass) manageDomain() string {
	if !utils.Is.Empty(this.Config.OSS.Domain) {
		return strings.TrimSuffix(this.Config.OSS.Domain, "/")
	}
	return "https://" + this.Config.OSS.Bucket + "." + this.Config.OSS.Endpoint
}

// List - 列出目录内容
func (this *OssClass) List(params StorageListParams) (response *StorageListResp) {

	response = &StorageListResp{Root: StorageInst.joinPublicPath(this.manageRoot(), ""), List: []StorageEntry{}}

	rel, ok := StorageInst.splitPublicPath(this.manageRoot(), params.Dir)
	if !ok {
		response.Error = fmt.Errorf("目录越出存储根")
		return
	}

	bucket, err := this.manageBucket()
	if err != nil {
		response.Error = err
		return
	}

	// 目录前缀 - 如 AIDE/media/
	prefix := this.manageKey(rel) + "/"
	options := []oss.Option{
		oss.Prefix(prefix),
		oss.Delimiter("/"),
		oss.MaxKeys(StorageInst.normListLimit(params.Limit)),
	}
	if !utils.Is.Empty(params.Marker) {
		options = append(options, oss.Marker(params.Marker))
	}

	result, err := bucket.ListObjects(options...)
	if err != nil {
		response.Error = err
		return
	}

	// 目录 - CommonPrefixes 形如 AIDE/media/users/
	for _, item := range result.CommonPrefixes {
		name := pathpkg.Base(strings.TrimSuffix(item, "/"))
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(name, params.Prefix) {
			continue
		}
		response.List = append(response.List, StorageEntry{
			Name:  name,
			Path:  "/" + strings.TrimSuffix(item, "/"),
			IsDir: true,
		})
	}
	// 文件 - 跳过目录占位对象
	for _, item := range result.Objects {
		if item.Key == prefix || strings.HasSuffix(item.Key, "/") {
			continue
		}
		name := pathpkg.Base(item.Key)
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(name, params.Prefix) {
			continue
		}
		response.List = append(response.List, StorageEntry{
			Name:    name,
			Path:    "/" + item.Key,
			Url:     this.manageDomain() + "/" + item.Key,
			Size:    item.Size,
			ModTime: item.LastModified.UnixMilli(),
		})
	}

	if result.IsTruncated {
		response.NextMarker = result.NextMarker
	}
	return
}

// collectKeys - 收集指定路径对应的全部对象 Key（文件返回自身，目录返回前缀下全部对象）
func (this *OssClass) collectKeys(bucket *oss.Bucket, key string) (keys []string, err error) {

	// 目录场景 - 前缀下全部对象
	marker := ""
	for {
		result, item := bucket.ListObjects(oss.Prefix(key+"/"), oss.Marker(marker), oss.MaxKeys(1000))
		if item != nil {
			return nil, item
		}
		for _, object := range result.Objects {
			keys = append(keys, object.Key)
		}
		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}
	if len(keys) > 0 {
		return keys, nil
	}

	// 单文件场景
	exist, item := bucket.IsObjectExist(key)
	if item != nil {
		return nil, item
	}
	if exist {
		keys = append(keys, key)
	}
	return keys, nil
}

// deleteKeys - 分批删除对象（单次最多 1000 个）
func (this *OssClass) deleteKeys(bucket *oss.Bucket, keys []string) error {
	for len(keys) > 0 {
		batch := keys
		if len(batch) > 1000 {
			batch = keys[:1000]
		}
		if _, err := bucket.DeleteObjects(batch, oss.DeleteObjectsQuiet(true)); err != nil {
			return err
		}
		keys = keys[len(batch):]
	}
	return nil
}

// MakeDir - 创建目录
func (this *OssClass) MakeDir(dir string) (response *StorageResp) {

	response = &StorageResp{}

	rel, ok := StorageInst.splitPublicPath(this.manageRoot(), dir)
	if !ok || rel == "" {
		response.Error = fmt.Errorf("目录路径不合法")
		return
	}

	bucket, err := this.manageBucket()
	if err != nil {
		response.Error = err
		return
	}
	// 目录占位对象 - 以 / 结尾的空对象
	if err = bucket.PutObject(this.manageKey(rel)+"/", bytes.NewReader([]byte{})); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(this.manageRoot(), rel)
	response.Name = pathpkg.Base(rel)
	return
}

// Remove - 删除文件或目录（目录递归删除）
func (this *OssClass) Remove(paths ...string) (response *StorageResp) {

	response = &StorageResp{}

	bucket, err := this.manageBucket()
	if err != nil {
		response.Error = err
		return
	}

	for _, item := range paths {
		rel, ok := StorageInst.splitPublicPath(this.manageRoot(), item)
		if !ok || rel == "" {
			response.Error = fmt.Errorf("路径不合法或越出存储根：%s", item)
			return
		}
		keys, err := this.collectKeys(bucket, this.manageKey(rel))
		if err != nil {
			response.Error = err
			return
		}
		if err = this.deleteKeys(bucket, keys); err != nil {
			response.Error = err
			return
		}
	}
	return
}

// Move - 移动或重命名文件/目录
func (this *OssClass) Move(src, dst string) (response *StorageResp) {

	response = &StorageResp{}

	srcRel, ok := StorageInst.splitPublicPath(this.manageRoot(), src)
	if !ok || srcRel == "" {
		response.Error = fmt.Errorf("源路径不合法或越出存储根")
		return
	}
	dstRel, ok := StorageInst.splitPublicPath(this.manageRoot(), dst)
	if !ok || dstRel == "" {
		response.Error = fmt.Errorf("目标路径不合法或越出存储根")
		return
	}
	// 禁止移动到自身内部
	if dstRel == srcRel || strings.HasPrefix(dstRel, srcRel+"/") {
		response.Error = fmt.Errorf("不能移动到自身内部")
		return
	}

	bucket, err := this.manageBucket()
	if err != nil {
		response.Error = err
		return
	}

	srcKey := this.manageKey(srcRel)
	dstKey := this.manageKey(dstRel)
	keys, err := this.collectKeys(bucket, srcKey)
	if err != nil {
		response.Error = err
		return
	}
	if len(keys) == 0 {
		response.Error = fmt.Errorf("源路径不存在")
		return
	}

	// 逐对象复制到新路径
	for _, key := range keys {
		if _, err = bucket.CopyObject(key, dstKey+strings.TrimPrefix(key, srcKey)); err != nil {
			response.Error = err
			return
		}
	}
	// 复制成功后删除源对象
	if err = this.deleteKeys(bucket, keys); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(this.manageRoot(), dstRel)
	response.Name = pathpkg.Base(dstRel)
	return
}

// ================================== 腾讯云对象存储文件管理 - 开始 ==================================

// manageRoot - COS 存储根目录
func (this *CosClass) manageRoot() string {
	return strings.Trim(this.Config.COS.Path, "/")
}

// manageKey - 相对路径转 COS 对象 Key
func (this *CosClass) manageKey(rel string) string {
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return this.manageRoot()
	}
	return this.manageRoot() + "/" + rel
}

// manageDomain - COS 访问域名（未配置则使用默认域名）
func (this *CosClass) manageDomain() string {
	if !utils.Is.Empty(this.Config.COS.Domain) {
		return strings.TrimSuffix(this.Config.COS.Domain, "/")
	}
	return fmt.Sprintf("https://%s-%s.cos.%s.myqcloud.com", this.Config.COS.Bucket, this.Config.COS.AppId, this.Config.COS.Region)
}

// manageClient - 获取 COS 客户端（文件管理场景，不触发自动创建存储桶）
func (this *CosClass) manageClient() (*cos.Client, error) {
	if this.Client == nil {
		return nil, fmt.Errorf("COS Client 未初始化")
	}
	return this.Client, nil
}

// List - 列出目录内容
func (this *CosClass) List(params StorageListParams) (response *StorageListResp) {

	response = &StorageListResp{Root: StorageInst.joinPublicPath(this.manageRoot(), ""), List: []StorageEntry{}}

	rel, ok := StorageInst.splitPublicPath(this.manageRoot(), params.Dir)
	if !ok {
		response.Error = fmt.Errorf("目录越出存储根")
		return
	}

	client, err := this.manageClient()
	if err != nil {
		response.Error = err
		return
	}

	// 目录前缀 - 如 AIDE/media/
	prefix := this.manageKey(rel) + "/"
	result, _, err := client.Bucket.Get(context.Background(), &cos.BucketGetOptions{
		Prefix:    prefix,
		Delimiter: "/",
		Marker:    params.Marker,
		MaxKeys:   StorageInst.normListLimit(params.Limit),
	})
	if err != nil {
		response.Error = err
		return
	}

	// 目录 - CommonPrefixes 形如 AIDE/media/users/
	for _, item := range result.CommonPrefixes {
		name := pathpkg.Base(strings.TrimSuffix(item, "/"))
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(name, params.Prefix) {
			continue
		}
		response.List = append(response.List, StorageEntry{
			Name:  name,
			Path:  "/" + strings.TrimSuffix(item, "/"),
			IsDir: true,
		})
	}
	// 文件 - 跳过目录占位对象
	for _, item := range result.Contents {
		if item.Key == prefix || strings.HasSuffix(item.Key, "/") {
			continue
		}
		name := pathpkg.Base(item.Key)
		if !utils.Is.Empty(params.Prefix) && !strings.HasPrefix(name, params.Prefix) {
			continue
		}
		// 修改时间 - RFC3339 格式
		modTime, _ := time.Parse(time.RFC3339, item.LastModified)
		response.List = append(response.List, StorageEntry{
			Name:    name,
			Path:    "/" + item.Key,
			Url:     this.manageDomain() + "/" + item.Key,
			Size:    item.Size,
			ModTime: modTime.UnixMilli(),
		})
	}

	if result.IsTruncated {
		response.NextMarker = result.NextMarker
	}
	return
}

// collectKeys - 收集指定路径对应的全部对象 Key（文件返回自身，目录返回前缀下全部对象）
func (this *CosClass) collectKeys(client *cos.Client, key string) (keys []string, err error) {

	// 目录场景 - 前缀下全部对象
	marker := ""
	for {
		result, _, item := client.Bucket.Get(context.Background(), &cos.BucketGetOptions{
			Prefix:  key + "/",
			Marker:  marker,
			MaxKeys: 1000,
		})
		if item != nil {
			return nil, item
		}
		for _, object := range result.Contents {
			keys = append(keys, object.Key)
		}
		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}
	if len(keys) > 0 {
		return keys, nil
	}

	// 单文件场景
	exist, item := client.Object.IsExist(context.Background(), key)
	if item != nil {
		return nil, item
	}
	if exist {
		keys = append(keys, key)
	}
	return keys, nil
}

// deleteKeys - 分批删除对象（单次最多 1000 个）
func (this *CosClass) deleteKeys(client *cos.Client, keys []string) error {
	for len(keys) > 0 {
		batch := keys
		if len(batch) > 1000 {
			batch = keys[:1000]
		}
		objects := make([]cos.Object, len(batch))
		for i, key := range batch {
			objects[i] = cos.Object{Key: key}
		}
		if _, _, err := client.Object.DeleteMulti(context.Background(), &cos.ObjectDeleteMultiOptions{
			Objects: objects,
			Quiet:   true,
		}); err != nil {
			return err
		}
		keys = keys[len(batch):]
	}
	return nil
}

// MakeDir - 创建目录
func (this *CosClass) MakeDir(dir string) (response *StorageResp) {

	response = &StorageResp{}

	rel, ok := StorageInst.splitPublicPath(this.manageRoot(), dir)
	if !ok || rel == "" {
		response.Error = fmt.Errorf("目录路径不合法")
		return
	}

	client, err := this.manageClient()
	if err != nil {
		response.Error = err
		return
	}
	// 目录占位对象 - 以 / 结尾的空对象
	if _, err = client.Object.Put(context.Background(), this.manageKey(rel)+"/", strings.NewReader(""), nil); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(this.manageRoot(), rel)
	response.Name = pathpkg.Base(rel)
	return
}

// Remove - 删除文件或目录（目录递归删除）
func (this *CosClass) Remove(paths ...string) (response *StorageResp) {

	response = &StorageResp{}

	client, err := this.manageClient()
	if err != nil {
		response.Error = err
		return
	}

	for _, item := range paths {
		rel, ok := StorageInst.splitPublicPath(this.manageRoot(), item)
		if !ok || rel == "" {
			response.Error = fmt.Errorf("路径不合法或越出存储根：%s", item)
			return
		}
		keys, err := this.collectKeys(client, this.manageKey(rel))
		if err != nil {
			response.Error = err
			return
		}
		if err = this.deleteKeys(client, keys); err != nil {
			response.Error = err
			return
		}
	}
	return
}

// Move - 移动或重命名文件/目录
func (this *CosClass) Move(src, dst string) (response *StorageResp) {

	response = &StorageResp{}

	srcRel, ok := StorageInst.splitPublicPath(this.manageRoot(), src)
	if !ok || srcRel == "" {
		response.Error = fmt.Errorf("源路径不合法或越出存储根")
		return
	}
	dstRel, ok := StorageInst.splitPublicPath(this.manageRoot(), dst)
	if !ok || dstRel == "" {
		response.Error = fmt.Errorf("目标路径不合法或越出存储根")
		return
	}
	// 禁止移动到自身内部
	if dstRel == srcRel || strings.HasPrefix(dstRel, srcRel+"/") {
		response.Error = fmt.Errorf("不能移动到自身内部")
		return
	}

	client, err := this.manageClient()
	if err != nil {
		response.Error = err
		return
	}

	srcKey := this.manageKey(srcRel)
	dstKey := this.manageKey(dstRel)
	keys, err := this.collectKeys(client, srcKey)
	if err != nil {
		response.Error = err
		return
	}
	if len(keys) == 0 {
		response.Error = fmt.Errorf("源路径不存在")
		return
	}

	// 逐对象复制到新路径
	for _, key := range keys {
		// 复制源地址 - Key 需分段 URL 编码
		source := fmt.Sprintf("%s-%s.cos.%s.myqcloud.com/%s", this.Config.COS.Bucket, this.Config.COS.AppId, this.Config.COS.Region, encodeUriKey(key))
		if _, _, err = client.Object.Copy(context.Background(), dstKey+strings.TrimPrefix(key, srcKey), source, nil); err != nil {
			response.Error = err
			return
		}
	}
	// 复制成功后删除源对象
	if err = this.deleteKeys(client, keys); err != nil {
		response.Error = err
		return
	}

	response.Path = StorageInst.joinPublicPath(this.manageRoot(), dstRel)
	response.Name = pathpkg.Base(dstRel)
	return
}

// encodeUriKey - 对象 Key 分段 URL 编码（保留 / 分隔符）
func encodeUriKey(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
