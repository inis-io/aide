package pushx

import (
	"strings"

	"github.com/inis-io/aide/utils"
)

// Config - 消息推送服务配置（由调用方传入）
type Config struct {
	// Engine - 驱动
	Engine EngineConfig `json:"engine"`
	// Email - 邮件服务配置
	Email EmailConfig `json:"email"`
	// AliYun 阿里云短信服务配置
	AliYun AliYunConfig `json:"aliyun"`
	// Tencent 腾讯云短信服务配置
	Tencent TencentConfig `json:"tencent"`
	// Smsbao 短信宝短信服务配置
	Smsbao SmsbaoConfig `json:"smsbao"`
	// Options - 扩展驱动的自定义配置（key 为驱动名），供外部注册的驱动读取
	Options map[string]map[string]any `json:"options"`
	// Hash - 计算配置是否发生变更
	Hash string `json:"hash"`
}

// EngineConfig - 推送引擎配置
type EngineConfig struct {
	// Email - 邮件
	Email string `json:"email" default:"email"`
	// SMS   - 短信
	SMS string `json:"sms"   default:"aliyun"`
}

// EmailConfig - 邮件服务配置
type EmailConfig struct {
	// Host      - 邮件服务器地址
	Host string `json:"host" comment:"服务地址" validate:"required,host" default:"smtp.qq.com"`
	// Port      - 邮件服务端口
	Port int `json:"port" comment:"端口号" validate:"numeric" default:"465"`
	// Account   - 邮件账号
	Account string `json:"account"  comment:"账号"  validate:"required"`
	// Password  - 服务密码 - 不是邮箱密码
	Password string `json:"password" comment:"密码"  validate:"required"`
	// Nickname  - 邮件昵称
	Nickname string `json:"nickname" comment:"昵称"  validate:"max=64" default:"邮件昵称"`
	// Subject   - 邮件主题
	Subject string `json:"subject"  comment:"主题"  validate:"max=64" default:"邮件主题"`
}

// AliYunConfig - 阿里云短信服务配置
type AliYunConfig struct {
	// AccessKeyId     - 阿里云AccessKey ID
	AccessKeyId string `json:"access_key_id" comment:"AccessKey ID" validate:"required,alphaNum"`
	// AccessKeySecret - 阿里云AccessKey Secret
	AccessKeySecret string `json:"access_key_secret" comment:"AccessKey Secret" validate:"required,alphaNum"`
	// Endpoint        - 阿里云短信服务endpoint
	Endpoint string `json:"endpoint"    comment:"endpoint" validate:"required,host" default:"dysmsapi.aliyuncs.com"`
	// SignName        - 短信签名
	SignName string `json:"sign_name"   comment:"短信签名" validate:"required"`
	// VerifyCode      - 验证码模板
	VerifyCode string `json:"verify_code" comment:"验证码模板" validate:"required,alphaDash"`
}

// TencentConfig - 腾讯云短信服务配置
type TencentConfig struct {
	// SecretId        - 腾讯云SecretId
	SecretId string `json:"secret_id"   comment:"Secret ID"  validate:"required,alphaNum"`
	// SecretKey       - 腾讯云SecretKey
	SecretKey string `json:"secret_key"  comment:"Secret Key" validate:"required,alphaNum"`
	// Endpoint        - 腾讯云短信服务endpoint
	Endpoint string `json:"endpoint"    comment:"endpoint"   validate:"required,host" default:"sms.tencentcloudapi.com"`
	// SmsSdkAppId     - 腾讯云短信服务appid
	SmsSdkAppId string `json:"sms_sdk_app_id" comment:"短信服务AppID" validate:"required,numeric"`
	// SignName        - 短信签名
	SignName string `json:"sign_name"   comment:"短信签名" validate:"required"`
	// VerifyCode      - 验证码模板id
	VerifyCode string `json:"verify_code" comment:"验证码模板" validate:"required,numeric"`
	// Region          - 区域
	Region string `json:"region" comment:"区域" validate:"required,alphaDash" default:"ap-guangzhou"`
}

// SmsbaoConfig - 短信宝短信服务配置
type SmsbaoConfig struct {
	// Account   - 短信宝账号
	Account string `json:"account"   comment:"短信宝账号" validate:"required,alphaNum"`
	// ApiKey    - API密钥
	ApiKey string `json:"api_key"   comment:"API 密钥"  validate:"required,alphaNum"`
	// SignName  - 短信签名
	SignName string `json:"sign_name" comment:"短信签名" validate:"required"`
	// BaseUrl   - 接口地址
	BaseUrl string `json:"base_url"  comment:"接口地址" validate:"url" default:"https://api.smsbao.com"`
}

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func normConfig(config Config) Config {

	config.Engine.Email = strings.ToLower(strings.TrimSpace(config.Engine.Email))
	if !registered(config.Engine.Email) {
		config.Engine.Email = "email"
	}

	config.Engine.SMS = strings.ToLower(strings.TrimSpace(config.Engine.SMS))
	if !registered(config.Engine.SMS) {
		config.Engine.SMS = "aliyun"
	}

	if utils.Is.Empty(config.Email.Host) {
		config.Email.Host = "smtp.qq.com"
	}
	if config.Email.Port <= 0 {
		config.Email.Port = 465
	}
	if utils.Is.Empty(config.Email.Nickname) {
		config.Email.Nickname = "邮件昵称"
	}
	if utils.Is.Empty(config.Email.Subject) {
		config.Email.Subject = "邮件主题"
	}

	if utils.Is.Empty(config.AliYun.Endpoint) {
		config.AliYun.Endpoint = "dysmsapi.aliyuncs.com"
	}
	if utils.Is.Empty(config.Tencent.Endpoint) {
		config.Tencent.Endpoint = "sms.tencentcloudapi.com"
	}
	if utils.Is.Empty(config.Tencent.Region) {
		config.Tencent.Region = "ap-guangzhou"
	}
	if utils.Is.Empty(config.Smsbao.BaseUrl) {
		config.Smsbao.BaseUrl = "https://api.smsbao.com"
	}

	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}

	return config
}
