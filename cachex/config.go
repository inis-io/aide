package cachex

import (
	"strings"
	"time"

	"github.com/inis-io/aide/utils"
)

// Config - 缓存服务配置（由调用方传入）
type Config struct {
	// Hash - 计算配置是否发生变更（可选，不传会自动计算）
	Hash string `json:"hash"`
	// Engine - 引擎（file / redis / 自定义注册名）
	Engine string `json:"engine"`
	// Redis - Redis 配置
	Redis RedisConfig `json:"redis"`
	// File - 文件缓存配置
	File FileConfig `json:"file"`
	// Options - 扩展驱动的自定义配置（key 为驱动名），供外部注册的驱动读取
	Options map[string]map[string]any `json:"options"`
}

// RedisConfig - Redis 配置
type RedisConfig struct {
	// Host     - 主机地址 - 不限制 host，因为 docker 的地址不正常
	Host string `json:"host" comment:"主机地址" default:"localhost"`
	// Port     - 端口号
	Port int `json:"port" comment:"端口号" validate:"numeric" default:"6379"`
	// Password - 密码
	Password string `json:"password" comment:"密码"`
	// Expired  - 过期时间（秒）
	Expired int `json:"expired"  comment:"过期时间" validate:"numeric" default:"7200"`
	// Prefix   - 前缀
	Prefix string `json:"prefix"   comment:"前缀" validate:"alphaDash,max=12" default:"AIDE"`
	// Database - 数据库
	Database int `json:"database" comment:"数据库索引" validate:"numeric"`
}

// FileConfig - 文件缓存配置
type FileConfig struct {
	// Expired - 过期时间（秒）
	Expired int `json:"expired" comment:"过期时间" validate:"numeric" default:"7200"`
	// Prefix  - 前缀
	Prefix string `json:"prefix"  comment:"前缀" validate:"alphaDash,max=12" default:"AIDE"`
	// Root    - 文件缓存根目录
	Root string `json:"root"    comment:"文件缓存根目录" default:"./runtime/cache"`
	// Suffix  - 文件后缀
	Suffix string `json:"suffix"  comment:"文件后缀" default:"json"`
}

// driverContext - 链式实例的默认上下文
type driverContext struct {
	// 键名前缀
	prefix string
	// 默认过期时间
	expired time.Duration
}

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func normConfig(config Config) Config {

	config.Engine = strings.ToLower(strings.TrimSpace(config.Engine))
	if !registered(config.Engine) {
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

// defaultContext - 按引擎取对应分段的默认前缀与过期时间（扩展引擎回退 file 段）
func defaultContext(name string, config Config) driverContext {
	if strings.ToLower(strings.TrimSpace(name)) == "redis" {
		return driverContext{
			prefix:  config.Redis.Prefix,
			expired: time.Duration(config.Redis.Expired) * time.Second,
		}
	}
	return driverContext{
		prefix:  config.File.Prefix,
		expired: time.Duration(config.File.Expired) * time.Second,
	}
}
