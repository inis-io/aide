package types

// ============================= 桶 - 开始 =============================

// BucketInfo - 管理视角桶信息
type BucketInfo struct {
	// Name - 桶名（全局唯一）
	Name string `json:"name"`
	// Region - 区域
	Region string `json:"region"`
	// Acl - 预置 ACL（private/public-read/public-read-write）
	Acl string `json:"acl"`
	// Versioning - 版本控制（off/on/suspended）
	Versioning string `json:"versioning"`
	// Status - 状态（active/frozen）
	Status string `json:"status"`
	// StorageClass - 默认存储级别
	StorageClass string `json:"storageClass"`
	// Shared - 公共桶开关（true = 匿名读 + 登录用户限前缀写）
	Shared bool `json:"shared"`
	// SharePrefix - 非桶主可写前缀（规范化后以 / 结尾）
	SharePrefix string `json:"sharePrefix"`
	// ShareMaxObjects - 单个写作者在本桶的对象数上限（0 = 不限）
	ShareMaxObjects int64 `json:"shareMaxObjects"`
	// ShareMaxBytes - 单个写作者在本桶的累计字节上限（0 = 不限）
	ShareMaxBytes int64 `json:"shareMaxBytes"`
	// AllowExts - 上传扩展名白名单（逗号分隔、小写、带点；空 = 不限制）
	AllowExts string `json:"allowExts"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
}

// EmptyBucketRequest - 清空桶请求（全部对象软删 + 物理文件清理）
type EmptyBucketRequest struct {
	// Bucket - 桶名（必填）
	Bucket string `json:"bucket"`
}

// EmptyBucketResult - 清空桶结果
type EmptyBucketResult struct {
	// Removed - 清空的对象数
	Removed int64 `json:"removed"`
}

// ============================= 桶 - 结束 =============================
