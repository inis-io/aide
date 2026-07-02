package facade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
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
