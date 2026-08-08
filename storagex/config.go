package storagex

import (
	"strings"

	"github.com/inis-io/aide/utils"
)

// Config - 存储服务配置（由调用方传入）
type Config struct {
	// Hash - 计算配置是否发生变更（可选，不传会自动计算）
	Hash string `json:"hash"`
	// Engine - 引擎（local / oss / cos / 自定义注册名）
	Engine string `json:"engine"`
	// Local - 本地存储配置
	Local LocalConfig `json:"local"`
	// OSS - 阿里云对象存储配置
	OSS OSSConfig `json:"oss"`
	// COS - 腾讯云对象存储配置
	COS COSConfig `json:"cos"`
	// Options - 扩展驱动的自定义配置（key 为驱动名），供外部注册的驱动读取
	Options map[string]map[string]any `json:"options"`
}

// LocalConfig - 本地存储配置
type LocalConfig struct {
	// Domain - 访问域名（拼接文件 Url）
	Domain string `json:"domain" comment:"访问域名" default:"http://localhost:2000"`
	// Root - 本地存储根目录（公开根取目录最后一段，如 public/storage → /storage）
	Root string `json:"root" comment:"本地存储根目录" default:"public/storage"`
}

// OSSConfig - 阿里云对象存储配置
type OSSConfig struct {
	// AccessKeyId - 阿里云 AccessKey ID
	AccessKeyId string `json:"access_key_id" comment:"AccessKey ID"`
	// AccessKeySecret - 阿里云 AccessKey Secret
	AccessKeySecret string `json:"access_key_secret" comment:"AccessKey Secret"`
	// Endpoint - OSS 外网 Endpoint
	Endpoint string `json:"endpoint" comment:"Endpoint" default:"oss-cn-guangzhou.aliyuncs.com"`
	// Bucket - 存储桶名称（需预先创建，SDK 不自动建桶）
	Bucket string `json:"bucket" comment:"存储桶名称"`
	// Domain - 访问域名（不填则用 存储桶外网默认域名）
	Domain string `json:"domain" comment:"访问域名"`
	// Path - 存储根目录
	Path string `json:"path" comment:"存储根目录" default:"AIDE"`
}

// COSConfig - 腾讯云对象存储配置
type COSConfig struct {
	// AppId - 腾讯云 AppId
	AppId string `json:"app_id" comment:"AppId"`
	// SecretId - 腾讯云 SecretId
	SecretId string `json:"secret_id" comment:"SecretId"`
	// SecretKey - 腾讯云 SecretKey
	SecretKey string `json:"secret_key" comment:"SecretKey"`
	// Bucket - 存储桶名称（需预先创建，SDK 不自动建桶）
	Bucket string `json:"bucket" comment:"存储桶名称"`
	// Region - 存储桶所在地域，如 ap-guangzhou（广州）
	Region string `json:"region" comment:"地域" default:"ap-guangzhou"`
	// Domain - 访问域名（不填则用 存储桶默认域名）
	Domain string `json:"domain" comment:"访问域名"`
	// Path - 存储根目录
	Path string `json:"path" comment:"存储根目录" default:"AIDE"`
}

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func normConfig(config Config) Config {

	config.Engine = strings.ToLower(strings.TrimSpace(config.Engine))
	if !registered(config.Engine) {
		config.Engine = "local"
	}

	if utils.Is.Empty(config.Local.Domain) {
		config.Local.Domain = "http://localhost:2000"
	}
	if utils.Is.Empty(config.Local.Root) {
		config.Local.Root = "public/storage"
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
