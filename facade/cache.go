package facade

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	
	"github.com/inis-io/aide/dto"
	"github.com/inis-io/aide/utils"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

// CacheInst - 缓存配置控制器实例
var CacheInst = &CacheClass{}

// init - 包初始化时启用默认缓存（文件缓存）
func init() { CacheInst.useDefaultCache() }

// CacheClass - 缓存配置控制器
type CacheClass struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash      string          `json:"hash"`
	// 当前缓存配置（由调用方注入）
	Config    dto.CacheConfig `json:"config"`
	// 是否已经注入过配置
	HasConfig bool            `json:"hasConfig"`
}

// normConfig 统一配置默认值，避免不同项目接入时行为不一致
func (this *CacheClass) normConfig(config dto.CacheConfig) dto.CacheConfig {
	
	config.Engine = strings.ToLower(strings.TrimSpace(config.Engine))
	if utils.Is.Empty(config.Engine) {
		config.Engine = "file"
	}
	if config.Engine != "file" && config.Engine != "redis" {
		config.Engine = "file"
	}
	
	if utils.Is.Empty(config.Redis.Host) {
		config.Redis.Host = "127.0.0.1"
	}
	if config.Redis.Port <= 0 {
		config.Redis.Port = 6379
	}
	if config.Redis.Expired <= 0 {
		config.Redis.Expired = 7200
	}
	if utils.Is.Empty(config.Redis.Prefix) {
		config.Redis.Prefix = "AIDE"
	}
	
	if utils.Is.Empty(config.File.Root) {
		config.File.Root = "./runtime/cache"
	}
	if utils.Is.Empty(config.File.Suffix) {
		config.File.Suffix = "json"
	}
	if config.File.Expired <= 0 {
		config.File.Expired = 7200
	}
	if utils.Is.Empty(config.File.Prefix) {
		config.File.Prefix = "AIDE"
	}
	
	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}
	
	return config
}

// defaultConfig - 获取默认缓存配置
func (this *CacheClass) defaultConfig() dto.CacheConfig {
	return CacheInst.normConfig(dto.CacheConfig{})
}

// useDefaultCache - 使用默认配置激活缓存
func (this *CacheClass) useDefaultCache() {
	conf := CacheInst.defaultConfig()
	CacheInst.setActiveCache(conf)
}

// setActiveCache - 按配置切换当前活动缓存实现
func (this *CacheClass) setActiveCache(config dto.CacheConfig) {
	
	conf := CacheInst.normConfig(config)

	switch conf.Engine {
	case "redis":
		Redis = &RedisClass{}
		Redis.Init(conf.Redis)
		FileCache = nil
		Cache = Redis
	default:
		FileCache = &FileClass{}
		FileCache.Init(conf.File)
		Redis = nil
		Cache = FileCache
	}
}

// setConfig - 注入缓存配置
func (this *CacheClass) setConfig(config dto.CacheConfig) *CacheClass {
	this.Config = CacheInst.normConfig(config)
	this.HasConfig = true
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载缓存
func (this *CacheClass) ReloadIfChanged(config ...dto.CacheConfig) {
	
	if len(config) > 0 { this.setConfig(config[0]) }
	
	if !this.HasConfig { return }
	
	// hash 变化，说明配置有更新
	if this.Hash != this.Config.Hash { this.Init() }
}

// Init 初始化
func (this *CacheClass) Init(config ...dto.CacheConfig) {
	
	if len(config) > 0 {
		this.setConfig(config[0])
	}
	
	if !this.HasConfig {
		CacheInst.useDefaultCache()
		return
	}
	
	this.Config = CacheInst.normConfig(this.Config)
	
	// 记录配置 Hash 值
	this.Hash = this.Config.Hash
	CacheInst.setActiveCache(this.Config)
}

// Cache - Cache实例
/**
 * @return CacheAPI
 * @example：
 * cache := facade.Cache.Expire(5 * time.Minute).Set("test", "这是测试")
 */
var Cache CacheAPI

// Redis - 当前激活的 Redis 缓存实例（当 Engine=redis 时可用）
var Redis *RedisClass

// FileCache - 当前激活的文件缓存实例（当 Engine=file 时可用）
var FileCache *FileClass

// CacheAPI - 统一缓存能力接口
type CacheAPI interface {
	// Tag
	/**
	 * @name 标签
	 * @param tag 标签
	 * @return CacheAPI
	 */
	Tag(tag ...string) CacheAPI
	// Tags
	/**
	 * @name 标签
	 * @param tag 标签
	 * @return CacheAPI
	 */
	Tags(tag []string) CacheAPI
	// Key
	/**
	 * @name 键
	 * @param key 键
	 * @return CacheAPI
	 */
	Key(key ...string) CacheAPI
	// Keys
	/**
	 * @name 键集
	 * @param key 键集
	 * @return CacheAPI
	 */
	Keys(key []string) CacheAPI
	// Has
	/**
	 * @name 判断缓存是否存在
	 * @param key 缓存的key
	 * @return bool
	 */
	Has(key string) (ok bool)
	// Get
	/**
	 * @name 获取缓存
	 * @param key 缓存的key
	 * @return any 缓存值
	 */
	Get(key string) (value any)
	// Set
	/**
	 * @name 设置缓存
	 * @param key 缓存的key
	 * @param value 缓存的值
	 * @param expired （可选）过期时间
	 * @return bool
	 */
	Set(key string, value any) (ok bool)
	// Delete
	/**
	 * @name 删除缓存
	 * @param key 缓存的key
	 * @return bool
	 */
	Delete(key ...string) (ok bool)
	// Clear
	/**
	 * @name 清空缓存
	 * @return bool
	 */
	Clear() (ok bool)
	// Expired
	/**
	 * @name 设置缓存过期时间
	 * @param second 过期时间
	 * @param second 支持 time.Duration 类型，字符串类型（如 "5s", "1m"）或数值类型（按秒计算）
	 * @return CacheAPI
	 */
	Expired(second any) CacheAPI
	// NewCache - 新建缓存
	NewCache(config dto.CacheConfig) CacheAPI
}

// CacheBody - 链式调用上下文
type CacheBody struct {
	// 键集 - \/:*?"<>|
	Keys []string
	// 标签
	Tags []string
	// 前缀
	Prefix string
	// 过期时间
	Expired time.Duration
}

// cloneStrings - 克隆字符串切片，避免共享底层数组
func (this *CacheClass) cloneStrings(values []string) []string {
	
	if len(values) == 0 { return nil }

	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

// cloneBody - 克隆缓存上下文，避免链式调用串状态
func (this *CacheClass) cloneBody(body CacheBody) CacheBody {
	body.Keys = CacheInst.cloneStrings(body.Keys)
	body.Tags = CacheInst.cloneStrings(body.Tags)
	return body
}

// ==================== Redis 缓存 ====================

// RedisClass - Redis缓存
type RedisClass struct {
	Client *redis.Client
	Body   CacheBody
	Config dto.CacheRedisConfig
}

// clone - 克隆 Redis 缓存实例（共享 client，隔离链式上下文）
func (this *RedisClass) clone() *RedisClass {
	
	if this == nil { return nil }

	clone := *this
	clone.Body = CacheInst.cloneBody(this.Body)
	return &clone
}

// NewCache - 按配置创建新的缓存实例
func (this *RedisClass) NewCache(config dto.CacheConfig) CacheAPI {
	
	conf := CacheInst.normConfig(config)
	
	switch strings.ToLower(conf.Engine) {
	case "file":
		cache := &FileClass{}
		cache.Init(conf.File)
		return cache
	default:
		cache := &RedisClass{}
		cache.Init(conf.Redis)
		return cache
	}
}

// Init - 初始化 Redis 缓存
func (this *RedisClass) Init(config dto.CacheRedisConfig) {
	
	this.Config = config
	
	prefix := this.Config.Prefix
	if !utils.Is.Empty(prefix) {
		this.Body.Prefix = prefix
	}
	this.Client = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%v", this.Config.Host, this.Config.Port),
		DB:       this.Config.Database,
		Password: this.Config.Password,
		// 明确禁用维护通知
		// 这可防止客户端发送“CLIENT MAINT_NOTIFICATIONS ON”
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: maintnotifications.ModeDisabled,
		},
	})
	this.Body.Expired = time.Duration(this.Config.Expired) * time.Second
}

// Key - 键
func (this *RedisClass) Key(key ...string) CacheAPI {
	cache := this.clone()
	var keys []string
	// 把多个标签转换为数组
	for _, value := range key {
		keys = append(keys, cache.Name(value))
	}
	// 合并之前的标签
	if len(cache.Body.Keys) > 0 {
		keys = append(keys, cache.Body.Keys...)
	}
	// 去重
	cache.Body.Keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	return cache
}

// Keys - 键集
func (this *RedisClass) Keys(key []string) CacheAPI {
	cache := this.clone()
	var keys []string
	// 把多个标签转换为数组
	for _, value := range key {
		keys = append(keys, cache.Name(value))
	}
	// 合并之前的标签
	if len(cache.Body.Keys) > 0 {
		keys = append(keys, cache.Body.Keys...)
	}
	// 去重
	cache.Body.Keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	return cache
}

// Tag - 标签
func (this *RedisClass) Tag(tag ...string) CacheAPI {
	cache := this.clone()
	var tags []string
	// 把多个标签转换为数组
	for _, value := range tag {
		tags = append(tags, strings.ToUpper(fmt.Sprintf("tag-%v", value)))
	}
	// 合并之前的标签
	if len(cache.Body.Tags) > 0 {
		tags = append(tags, cache.Body.Tags...)
	}
	// 去重
	cache.Body.Tags = cast.ToStringSlice(utils.ArrayUnique(tags))
	return cache
}

// Tags - 标签
func (this *RedisClass) Tags(tag []string) CacheAPI {
	cache := this.clone()
	var tags []string
	// 把多个标签转换为数组
	for _, value := range tag {
		tags = append(tags, strings.ToUpper(fmt.Sprintf("tag-%v", value)))
	}
	// 合并之前的标签
	if len(cache.Body.Tags) > 0 {
		tags = append(tags, cache.Body.Tags...)
	}
	// 去重
	cache.Body.Tags = cast.ToStringSlice(utils.ArrayUnique(tags))
	return cache
}

// Expired - 过期时间
func (this *RedisClass) Expired(second any) CacheAPI {
	
	cache := this.clone()
	
	// 判断 second 是否为 time.Duration 类型或可解析为 duration 字符串
	switch v := second.(type) {
	case time.Duration:
		cache.Body.Expired = v
	case string:
		// 尝试解析为 duration 字符串（例如 `5s`, `1m`）
		if d, err := time.ParseDuration(v); err == nil {
			cache.Body.Expired = d
		} else if i := cast.ToInt64(v); i > 0 {
			cache.Body.Expired = time.Duration(i) * time.Second
		} else {
			cache.Body.Expired = cast.ToDuration(v)
		}
	default:
		// 对于数值类型，按秒处理；对于其他类型，尽量转换为 duration
		if cast.ToInt64(second) > 0 {
			cache.Body.Expired = time.Duration(cast.ToInt64(second)) * time.Second
		} else {
			cache.Body.Expired = cast.ToDuration(second)
		}
	}
	
	return cache
}

// Has - 判断缓存是否存在
func (this *RedisClass) Has(key string) (ok bool) {
	
	if utils.Is.Empty(key) { return false }
	
	ctx := context.Background()
	
	result, err := this.Client.Exists(ctx, this.Name(key)).Result()
	return utils.Ternary[bool](err != nil, false, result == 1)
}

// Get - 获取缓存
func (this *RedisClass) Get(key string) (value any) {
	
	if utils.Is.Empty(key) { return false }
	
	ctx := context.Background()
	
	result, err := this.Client.Get(ctx, this.Name(key)).Result()
	
	return utils.Ternary[any](err != nil, nil, utils.Json.Decode(result))
}

// Set - 设置缓存
func (this *RedisClass) Set(key string, value any) (ok bool) {
	
	cache := this.clone()
	if cache == nil || utils.Is.Empty(key) { return false }
	
	ctx := context.Background()
	
	// 设置缓存
	err := cache.Client.Set(ctx, cache.Name(key), utils.Json.Encode(value), cache.Body.Expired).Err()
	
	// 设置标签
	cache.SetTags(key)
	// 重置配置
	cache.Reset()
	
	return utils.Ternary[bool](err != nil, false, true)
}

// Delete - 删除缓存
func (this *RedisClass) Delete(key ...string) (ok bool) {
	
	cache := this.clone()
	if cache == nil { return false }
	
	var err error
	ctx := context.Background()
	
	// // 根据标签删除缓存
	// if len(this.Body.Tags) > 0 {
	// 	for _, tag := range this.Body.Tags {
	// 		// 获取标签下的所有成员
	// 		members, _ := this.Client.SMembers(ctx, tag).Result()
	// 		for _, member := range members {
	// 			// 删除成员
	// 			err = this.Client.Del(ctx, member).Err()
	// 			if err != nil { return false }
	// 		}
	// 		// 删除标签
	// 		err = this.Client.Del(ctx, tag).Err()
	// 		// 清空标签
	// 		this.Body.Tags = []string{}
	// 	}
	// }
	
	if cloned, ok := cache.Keys(key).(*RedisClass); ok {
		cache = cloned
	}
	
	// 根据键集删除缓存
	_ = cache.Client.Del(ctx, cache.Body.Keys...).Err()
	
	// 根据标签删除缓存
	cache.DelTags()
	// 重置配置
	cache.Reset()
	
	return utils.Ternary[bool](err != nil, false, true)
}

// Clear - 清空缓存
func (this *RedisClass) Clear() (ok bool) {
	
	ctx := context.Background()
	err := this.Client.FlushDB(ctx).Err()
	return utils.Ternary[bool](err != nil, false, true)
}

// Name - 缓存名称规则
func (this *RedisClass) Name(key any) string {
	return fmt.Sprintf("%s-%s", this.Body.Prefix, utils.Hash.Sum32(key))
	// return utils.Hash.Sum32(fmt.Sprintf("%vkey=%v", this.Body.Prefix, key))
}

// Reset - 重置配置 - 辅助方法
func (this *RedisClass) Reset() {
	this.Body.Keys    = []string{}
	this.Body.Tags    = []string{}
	this.Body.Expired = time.Duration(this.Config.Expired) * time.Second
}

// SetTags - 设置标签 - 辅助方法
func (this *RedisClass) SetTags(key string) {
	
	ctx := context.Background()
	
	for _, tag := range this.Body.Tags {
		
		var keys []string
		
		// 文件名
		name := fmt.Sprintf("%v-%v", this.Body.Prefix, tag)
		
		// 不存在标签
		if !this.Has(name) {
			// 添加新成员
			keys = append(keys, this.Name(key))
			// 设置缓存
			_ = this.Client.Set(ctx, name, utils.Json.Encode(keys), 0).Err()
			continue
		}
		
		// 获取标签下的所有成员
		read, _ := this.Client.Get(ctx, name).Result()
		keys = cast.ToStringSlice(utils.Json.Decode(read))
		
		// 添加新成员
		keys = append(keys, this.Name(key))
		// 去重
		keys = cast.ToStringSlice(utils.ArrayUnique(keys))
		// 写入文件
		_ = this.Client.Set(ctx, name, utils.Json.Encode(keys), 0).Err()
	}
}

// DelTags - 删除标签
func (this *RedisClass) DelTags() {
	
	ctx := context.Background()
	
	for _, tag := range this.Body.Tags {
		
		// 文件名
		name := fmt.Sprintf("%v-%v", this.Body.Prefix, tag)
		
		ok, _ := this.Client.Exists(ctx, name).Result()
		// 不存在标签
		if ok != 1 {
			continue
		}
		
		// 获取标签下的所有成员
		read, _ := this.Client.Get(ctx, name).Result()
		keys := cast.ToStringSlice(utils.Json.Decode(read))
		// 删除标签下的所有成员
		_ = this.Client.Del(ctx, keys...).Err()
		// 删除标签文件
		_ = this.Client.Del(ctx, name).Err()
	}
}

// ============================ 文件缓存 ============================

// FileClass - 文件缓存实现
type FileClass struct {
	// 文件客户端
	Fs     afero.Fs
	// 缓存参数
	Body   CacheBody
	// 当前配置
	Config dto.CacheFileConfig
	// 存储目录
	Root   string
	// 文件后缀
	Suffix string
}

// clone - 克隆文件缓存实例（共享文件系统对象，隔离链式上下文）
func (this *FileClass) clone() *FileClass {
	
	if this == nil { return nil }

	clone := *this
	clone.Body = CacheInst.cloneBody(this.Body)
	return &clone
}

// NewCache - 按配置创建新的缓存实例
func (this *FileClass) NewCache(config dto.CacheConfig) CacheAPI {
	
	conf := CacheInst.normConfig(config)
	
	switch strings.ToLower(conf.Engine) {
	case "redis":
		cache := &RedisClass{}
		cache.Init(conf.Redis)
		return cache
	default:
		cache := &FileClass{}
		cache.Init(conf.File)
		return cache
	}
}

// FileCacheResp - 文件缓存结构
type FileCacheResp struct {
	// 过期时间戳
	Expired int64 `json:"expired"`
	// 缓存值
	Value any `json:"value"`
}

// Init 初始化 文件缓存
func (this *FileClass) Init(config dto.CacheFileConfig) {
	
	this.Config = config
	
	this.Root   = this.Config.Root
	// 设置文件后缀
	this.Suffix = this.Config.Suffix
	
	prefix := this.Config.Prefix
	if !utils.Is.Empty(prefix) {
		this.Body.Prefix = fmt.Sprintf("%v", prefix)
	}
	
	this.Fs = afero.NewOsFs()
	this.Body.Expired = time.Duration(this.Config.Expired) * time.Second
}

// Key - 键
func (this *FileClass) Key(key ...string) CacheAPI {
	cache := this.clone()
	var keys []string
	// 把多个标签转换为数组
	for _, value := range key {
		keys = append(keys, cache.Name(value))
	}
	// 合并之前的标签
	if len(cache.Body.Keys) > 0 {
		keys = append(keys, cache.Body.Keys...)
	}
	// 去重
	cache.Body.Keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	return cache
}

// Keys - 键集
func (this *FileClass) Keys(key []string) CacheAPI {
	cache := this.clone()
	var keys []string
	// 把多个标签转换为数组
	for _, value := range key {
		keys = append(keys, cache.Name(value))
	}
	// 合并之前的标签
	if len(cache.Body.Keys) > 0 {
		keys = append(keys, cache.Body.Keys...)
	}
	// 去重
	cache.Body.Keys = cast.ToStringSlice(utils.ArrayUnique(keys))
	return cache
}

// Tag - 标签
func (this *FileClass) Tag(tag ...string) CacheAPI {
	cache := this.clone()
	var tags []string
	// 把多个标签转换为数组
	for _, value := range tag {
		tags = append(tags, strings.ToUpper(fmt.Sprintf("tag-%v", value)))
	}
	// 合并之前的标签
	if len(cache.Body.Tags) > 0 {
		tags = append(tags, cache.Body.Tags...)
	}
	// 去重
	cache.Body.Tags = cast.ToStringSlice(utils.ArrayUnique(tags))
	return cache
}

// Tags - 标签
func (this *FileClass) Tags(tag []string) CacheAPI {
	cache := this.clone()
	var tags []string
	// 把多个标签转换为数组
	for _, value := range tag {
		tags = append(tags, strings.ToUpper(fmt.Sprintf("tag-%v", value)))
	}
	// 合并之前的标签
	if len(cache.Body.Tags) > 0 {
		tags = append(tags, cache.Body.Tags...)
	}
	// 去重
	cache.Body.Tags = cast.ToStringSlice(utils.ArrayUnique(tags))
	return cache
}

// SetTags - 设置标签
func (this *FileClass) SetTags(key string) {
	for _, tag := range this.Body.Tags {
		
		var value []byte
		var keys  []string
		
		// 文件名
		name := fmt.Sprintf("%v-%v.%s", this.Body.Prefix, tag, this.Suffix)
		// 存储路径
		dest := fmt.Sprintf("%s/%s", this.Root, name)
		
		// 不存在标签
		if !this.Exist(dest) {
			// 添加新成员
			keys = append(keys, this.Name(key))
			value = []byte(utils.Json.Encode(keys))
			_ = this.Write(dest, value)
			continue
		}
		
		// 获取标签下的所有成员
		read, _ := this.Read(dest)
		keys  = cast.ToStringSlice(utils.Json.Decode(read))
		
		// 添加新成员
		keys  = append(keys, this.Name(key))
		// 去重
		keys  = cast.ToStringSlice(utils.ArrayUnique(keys))
		// 重新设置
		value = []byte(utils.Json.Encode(keys))
		// 写入文件
		_ = this.Write(dest, value)
	}
}

// DelTags - 删除标签
func (this *FileClass) DelTags() {
	for _, tag := range this.Body.Tags {
		
		// 文件名
		name := fmt.Sprintf("%v-%v.%s", this.Body.Prefix, tag, this.Suffix)
		// 存储路径
		dest := fmt.Sprintf("%s/%s", this.Root, name)
		
		// 不存在标签
		if !this.Exist(dest) {
			continue
		}
		
		// 获取标签下的所有成员
		read, _ := this.Read(dest)
		keys := cast.ToStringSlice(utils.Json.Decode(read))
		// 删除标签下的所有成员
		for _, key := range keys {
			_ = this.DeleteFile(fmt.Sprintf("%s/%s", this.Root, key))
		}
		// 删除标签文件
		_ = this.DeleteFile(dest)
	}
}

// Expired - 过期时间
func (this *FileClass) Expired(second any) CacheAPI {
	cache := this.clone()
	
	// 判断 second 是否为 time.Duration 类型或可解析为 duration 字符串
	switch v := second.(type) {
	case time.Duration:
		cache.Body.Expired = v
	case string:
		// 尝试解析为 duration 字符串（例如 `5s`, `1m`）
		if d, err := time.ParseDuration(v); err == nil {
			cache.Body.Expired = d
		} else if i := cast.ToInt64(v); i > 0 {
			cache.Body.Expired = time.Duration(i) * time.Second
		} else {
			cache.Body.Expired = cast.ToDuration(v)
		}
	default:
		// 对于数值类型，按秒处理；对于其他类型，尽量转换为 duration
		if cast.ToInt64(second) > 0 {
			cache.Body.Expired = time.Duration(cast.ToInt64(second)) * time.Second
		} else {
			// 一百年
			cache.Body.Expired = cast.ToDuration(100*365*24*60*60) * time.Second
		}
	}
	
	return cache
}

// Has - 判断缓存是否存在
func (this *FileClass) Has(key string) (ok bool) {
	
	// 如果 key 为空，直接返回 false
	if utils.Is.Empty(key) {
		return false
	}
	// 检查缓存是否存在
	if !this.Exist(this.Dest(key)) {
		return false
	}
	
	// 读取文件内容
	data, err := this.Read(this.Dest(key))
	if err != nil {
		return false
	}
	
	var row FileCacheResp
	
	_ = utils.Json.Unmarshal(data, &row)
	
	// 检查缓存是否过期
	if row.Expired < time.Now().Unix() {
		// 删除过期缓存
		_ = this.DeleteFile(this.Dest(key))
		return false
	}
	
	return true
}

// Get - 获取缓存
func (this *FileClass) Get(key string) (value any) {
	
	if utils.Is.Empty(key) {
		return nil
	}
	
	// 获取缓存内容
	if !this.Has(key) {
		return nil
	}
	
	// 读取文件内容
	data, err := this.Read(this.Dest(key))
	if err != nil {
		return nil
	}
	
	var row FileCacheResp
	
	err = utils.Json.Unmarshal(data, &row)
	if err != nil {
		return nil
	}
	
	// 检查缓存是否过期
	if row.Expired < time.Now().Unix() {
		// 删除过期缓存
		_ = this.DeleteFile(this.Dest(key))
		return nil
	}
	
	// 返回缓存值
	return row.Value
}

// Set - 设置缓存
func (this *FileClass) Set(key string, value any) (ok bool) {
	cache := this.clone()
	
	if cache == nil || utils.Is.Empty(key) { return false }
	
	// 创建存储目录
	_ = os.MkdirAll(cache.Root, 0755)
	
	// 过期时间戳
	expired := time.Now().Add(cache.Body.Expired).Unix()
	
	data := utils.Json.Encode(map[string]any{
		"expired": expired,
		"value":   value,
	})
	
	if err := cache.Write(cache.Dest(key), []byte(data)); err != nil {
		return false
	}
	
	// 设置标签
	cache.SetTags(key)
	// 重置配置
	cache.Reset()
	
	return true
}

// Delete - 删除缓存
func (this *FileClass) Delete(key ...string) (ok bool) {
	cache := this.clone()
	if cache == nil {
		return false
	}
	
	if cloned, ok := cache.Keys(key).(*FileClass); ok {
		cache = cloned
	}
	
	// 删除缓存
	for _, value := range cache.Body.Keys {
		_ = cache.DeleteFile(fmt.Sprintf("%s/%s", cache.Root, value))
	}
	
	// 根据标签删除缓存
	cache.DelTags()
	// 重置配置
	cache.Reset()
	
	return true
}

// Clear - 清空缓存
func (this *FileClass) Clear() (ok bool) {
	err := this.DeleteFile(this.Root)
	if err != nil {
		return false
	}
	// 创建目录
	_ = os.MkdirAll(this.Root, 0755)
	return true
}

// Name - 缓存名称规则 - 辅助方法
func (this *FileClass) Name(key string) string {
	return fmt.Sprintf("%s-%s.%s", this.Body.Prefix, utils.Hash.Sum32(key), this.Suffix)
}

// Dest - 缓存目录 - 辅助方法
func (this *FileClass) Dest(key string) string {
	return fmt.Sprintf("%s/%s", this.Root, this.Name(key))
}

// Reset - 重置配置 - 辅助方法
func (this *FileClass) Reset() {
	this.Body.Keys = []string{}
	this.Body.Tags = []string{}
	this.Body.Expired = time.Duration(this.Config.Expired) * time.Second
}

// Exist 检查文件或目录是否存在 - 辅助方法
func (this *FileClass) Exist(dest string) (ok bool) {
	ok, _ = afero.Exists(this.Fs, dest)
	return ok
}

// Chmod - 修改文件或目录权限 - 辅助方法
func (this *FileClass) Chmod(dest string, mode int64) {
	_, _ = afero.Exists(this.Fs, dest)
	_ = this.Fs.Chmod(dest, os.FileMode(mode))
	return
}

// CreateFile - 创建文件并写入内容 - 辅助方法
func (this *FileClass) Write(dest string, content []byte) (err error) {
	
	// 以写入模式打开文件（若不存在则创建，存在则覆盖）
	file, err := this.Fs.Create(dest)
	
	if err != nil {
		return fmt.Errorf("创建文件失败: %w", err)
	}
	// 延迟关闭文件
	defer func(file afero.File) { err = file.Close() }(file)
	
	// 写入内容到文件
	_, err = file.Write(content)
	if err != nil {
		return fmt.Errorf("写入文件内容失败: %w", err)
	}
	
	// 设置文件权限为 755
	err = this.Fs.Chmod(dest, 0755)
	
	return nil
}

// Read - 读取文件内容 - 辅助方法
func (this *FileClass) Read(dest string) (content []byte, err error) {
	
	// 打开指定文件
	file, err := this.Fs.Open(dest)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer func(file afero.File) { err = file.Close() }(file)
	
	// 获取文件信息
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("获取文件信息失败: %w", err)
	}
	
	// 读取文件内容
	content = make([]byte, info.Size())
	_, err  = file.Read(content)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("读取文件内容失败: %w", err)
	}
	
	return content, nil
}

// DeleteFile - 删除文件或目录 - 辅助方法
func (this *FileClass) DeleteFile(dest string) error {
	return this.Fs.RemoveAll(dest)
}