package dto

type StorageConfig struct {
	// Engine - 存储驱动
	Engine string	 	`json:"engine" default:"local"`
	// Local - 本地存储配置
	Local  LocalStorageConfig `json:"local"`
	// OSS - 阿里OSS配置
	OSS    OSS 			`json:"oss"`
	// COS - 腾讯COS配置
	COS    COS 			`json:"cos"`
	// Hash - 计算配置是否发生变更
	Hash    string  	`json:"hash"`
}

// LocalStorageConfig - 本地存储配置
type LocalStorageConfig struct {
	// Domain - 本地存储域名
	Domain string `json:"domain" comment:"域名" validate:"omitempty,url" default:"http://localhost:2000"`
}

// OSS - 阿里OSS配置
type OSS struct {
	// AccessKeyId     - 阿里云AccessKey ID
	AccessKeyId     string `json:"access_key_id" comment:"AccessKey ID" validate:"required,alphaNum"`
	// AccessKeySecret - 阿里云AccessKey Secret
	AccessKeySecret string `json:"access_key_secret" comment:"AccessKey Secret" validate:"required,alphaNum"`
	// Endpoint        - OSS 外网 Endpoint
	Endpoint        string `json:"endpoint" comment:"endpoint" validate:"required,host" default:"oss-cn-guangzhou.aliyuncs.com"`
	// Bucket          - OSS Bucket - 存储桶名称
	Bucket          string `json:"bucket"   comment:"存储桶名称" validate:"required,alphaDash"`
	// Domain          - OSS 外网域名 - 用于访问 - 不填写则使用默认域名
	Domain          string `json:"domain"   comment:"外网域名" validate:"omitempty,url"`
	// Path            - OSS 存储目录
	Path            string `json:"path" comment:"存储目录" validate:"required" default:"AIDE"`
}

// COS - 腾讯COS配置
type COS struct {
	// AppId     - 腾讯云COS AppId
	AppId     string `json:"app_id" comment:"AppId" validate:"required,numeric"`
	// SecretId  - 腾讯云COS SecretId
	SecretId  string `json:"secret_id"  comment:"SecretId"  validate:"required,alphaNum"`
	// SecretKey - 腾讯云COS SecretKey
	SecretKey string `json:"secret_key" comment:"SecretKey" validate:"required,alphaNum"`
	// Bucket    - COS Bucket - 存储桶名称
	Bucket    string `json:"bucket" comment:"存储桶名称" validate:"required,alphaDash"`
	// Region    - COS 所在地区，如这里的 ap-guangzhou（广州）
	Region    string `json:"region" comment:"区域" validate:"required,alphaDash" default:"ap-guangzhou"`
	// Domain    - COS 外网域名 - 用于访问 - 不填写则使用默认域名
	Domain    string `json:"domain" comment:"外网域名" validate:"omitempty,url"`
	// Path      - COS 存储目录
	Path      string `json:"path"   comment:"存储目录" default:"AIDE"`
}