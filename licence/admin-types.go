package licence

import "encoding/json"

// 本文件 DTO 与平台逐一对齐：
//   - 输出结构对齐 licen-hub/backend/app/models/basic 各模型的 json tag；
//   - 输入结构对齐 licen-hub/backend/app/types 各请求结构体的 json tag；
//   - 时间戳除特别注明外均为毫秒（平台 autoCreateTime:milli）。

// ============================= 登录态 =============================

// Token - 登录令牌（平台 types.TokenResp）
type Token struct {
	// No - 会话编号（内嵌 JWT，关联登录会话表）
	No string `json:"no"`
	// Value - JWT 令牌值（请求时放入 Authorization: Bearer <value>）
	Value string `json:"value"`
	// Expired - 过期时间（毫秒时间戳；平台注释误标为秒，实际按 UnixMilli 签发）
	Expired int64 `json:"expired"`
}

// User - 登录用户信息（平台 models/basic.Users 的常用字段子集）
type User struct {
	// Id - 用户ID
	Id int `json:"id"`
	// UserNo - 对外用户编号（许可证载荷引用，格式 USR-%06d）
	UserNo string `json:"userNo"`
	// Account - 账号
	Account string `json:"account"`
	// Nickname - 昵称
	Nickname string `json:"nickname"`
	// Email - 邮箱
	Email string `json:"email"`
	// Phone - 手机号
	Phone string `json:"phone"`
	// Avatar - 头像
	Avatar string `json:"avatar"`
	// Source - 注册来源
	Source string `json:"source"`
	// Status - 账号状态（normal=正常）
	Status string `json:"status"`
	// UserType - 用户类型（platform=平台人员 / member=注册用户）
	UserType string `json:"userType"`
	// QualificationStatus - 资格状态（none/pending/approved/rejected/revoked）
	QualificationStatus string `json:"qualificationStatus"`
	// ProjectQuota - 个人项目配额（0=跟随系统默认）
	ProjectQuota int `json:"projectQuota"`
	// QualifiedAt - 最近一次资格审核通过时间（毫秒）
	QualifiedAt int64 `json:"qualifiedAt"`
	// SignInAt - 登录时间（毫秒）
	SignInAt int64 `json:"signInAt"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// SignInResult - 登录/校验令牌响应数据（data 内 {user,token,auth}）
type SignInResult struct {
	// User - 登录用户
	User User `json:"user"`
	// Token - 登录令牌
	Token Token `json:"token"`
	// Auth - 权限快照（角色/权限码/数据范围等，平台 types.UsersAuth，结构随版本演进，保留原文）
	Auth json.RawMessage `json:"auth"`
}

// ============================= 资格审核 =============================

// QualificationApplication - 资格申请（平台 models/basic.QualificationApplication）
type QualificationApplication struct {
	// Id - 申请ID
	Id int `json:"id"`
	// ApplyNo - 申请编号（QLF-{年}-{序号}）
	ApplyNo string `json:"applyNo"`
	// UserId - 申请人用户ID
	UserId int `json:"userId"`
	// Reason - 申请说明（用途、项目方向）
	Reason string `json:"reason"`
	// Contact - 申请人补充联系方式
	Contact string `json:"contact"`
	// Status - 状态（pending/approved/rejected/revoked/cancelled）
	Status string `json:"status"`
	// ReviewNote - 审批意见（拒绝/撤销时必填）
	ReviewNote string `json:"reviewNote"`
	// ReviewerId - 审批人用户ID
	ReviewerId int `json:"reviewerId"`
	// ReviewedAt - 审批时间（毫秒）
	ReviewedAt int64 `json:"reviewedAt"`
	// Version - 乐观锁版本
	Version int64 `json:"version"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// QualificationCurrent - 我的资格状态（GET /api/qualification/current 的 data）
type QualificationCurrent struct {
	// QualificationStatus - 资格状态（none/pending/approved/rejected/revoked）
	QualificationStatus string `json:"qualificationStatus"`
	// ProjectQuota - 有效配额（个人配额 > 0 时取个人配额，否则取系统默认）
	ProjectQuota int `json:"projectQuota"`
	// DefaultQuota - 系统默认项目配额
	DefaultQuota int `json:"defaultQuota"`
	// LatestApplication - 最近一条申请（任意状态；无申请时为 null）
	LatestApplication *QualificationApplication `json:"latestApplication"`
}

// QualificationApplyInput - 提交资格申请参数（平台 types.QualificationApply）
type QualificationApplyInput struct {
	// Reason - 申请说明（必填，最长 512）
	Reason string `json:"reason"`
	// Contact - 补充联系方式（必填，最长 128）
	Contact string `json:"contact"`
}

// QualificationFindParams - 资格申请查询参数（mine/find/rows 共用；status/userId 仅管理队列生效）
type QualificationFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Status - 申请状态筛选
	Status string `json:"status,omitempty"`
	// UserId - 申请人用户ID筛选（管理员队列生效）
	UserId int `json:"userId,omitempty"`
}

// QualificationReviewInput - 审批资格申请参数（平台 types.QualificationReview，需 qualification.review 权限）
type QualificationReviewInput struct {
	// Id - 申请ID（必填）
	Id int `json:"id"`
	// Action - 审批动作（approve/reject，必填）
	Action string `json:"action"`
	// ReviewNote - 审批意见（reject 时必填）
	ReviewNote string `json:"reviewNote,omitempty"`
	// ProjectQuota - 个人项目配额（approve 时可选，>=1 生效，0=不调整）
	ProjectQuota int `json:"projectQuota,omitempty"`
}

// ============================= 项目 =============================

// Project - 项目（平台 models/basic.Project）
type Project struct {
	// Id - 项目ID
	Id int `json:"id"`
	// ProjectNo - 项目编号
	ProjectNo string `json:"projectNo"`
	// ProjectName - 项目名称
	ProjectName string `json:"projectName"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// ProjectType - 项目类型
	ProjectType string `json:"projectType"`
	// TechStack - 技术栈
	TechStack string `json:"techStack"`
	// DeliveryMode - 交付方式
	DeliveryMode string `json:"deliveryMode"`
	// RepositoryUrl - 代码仓库地址
	RepositoryUrl string `json:"repositoryUrl"`
	// DefaultBranch - 默认分支
	DefaultBranch string `json:"defaultBranch"`
	// CurrentVersion - 当前版本
	CurrentVersion string `json:"currentVersion"`
	// Status - 状态
	Status string `json:"status"`
	// AcceptanceDate - 验收日期（毫秒）
	AcceptanceDate int64 `json:"acceptanceDate"`
	// LicenseMode - 授权模式
	LicenseMode string `json:"licenseMode"`
	// ExpirePolicy - 到期策略
	ExpirePolicy string `json:"expirePolicy"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ProjectInput - 项目新增/修改参数（平台 types.Project；Create 时 Id 留空，Update 时 Id 必填）
// 归属用户由平台按登录态强制写入，请求体中的 userId 无效，故本结构不提供该字段。
type ProjectInput struct {
	// Id - 项目ID（Update 必填）
	Id int `json:"id,omitempty"`
	// ProjectNo - 项目编号（留空由平台自动生成）
	ProjectNo string `json:"projectNo,omitempty"`
	// ProjectName - 项目名称（Create 必填，最长 128）
	ProjectName string `json:"projectName,omitempty"`
	// ProjectType - 项目类型
	ProjectType string `json:"projectType,omitempty"`
	// TechStack - 技术栈
	TechStack string `json:"techStack,omitempty"`
	// DeliveryMode - 交付方式
	DeliveryMode string `json:"deliveryMode,omitempty"`
	// RepositoryUrl - 代码仓库地址
	RepositoryUrl string `json:"repositoryUrl,omitempty"`
	// DefaultBranch - 默认分支
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// CurrentVersion - 当前版本
	CurrentVersion string `json:"currentVersion,omitempty"`
	// Status - 状态
	Status string `json:"status,omitempty"`
	// AcceptanceDate - 验收日期（毫秒）
	AcceptanceDate int64 `json:"acceptanceDate,omitempty"`
	// LicenseMode - 授权模式
	LicenseMode string `json:"licenseMode,omitempty"`
	// ExpirePolicy - 到期策略
	ExpirePolicy string `json:"expirePolicy,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// ProjectFindParams - 项目查询参数（平台 types.ProjectFind + 控制器 In/Like/Between 清单）
type ProjectFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "create_at desc"）
	Order string `json:"order,omitempty"`
	// ProjectName - 项目名称（模糊）
	ProjectName string `json:"projectName,omitempty"`
	// UserId - 归属用户ID（IN）
	UserId []int `json:"userId,omitempty"`
	// ProjectType - 项目类型（IN）
	ProjectType []string `json:"projectType,omitempty"`
	// DeliveryMode - 交付方式（IN）
	DeliveryMode []string `json:"deliveryMode,omitempty"`
	// Status - 状态（IN）
	Status []string `json:"status,omitempty"`
	// LicenseMode - 授权模式（IN）
	LicenseMode []string `json:"licenseMode,omitempty"`
	// CreateTime - 创建时间区间（毫秒 [起,止]，0=开口；Between）
	CreateTime []int64 `json:"createTime,omitempty"`
	// UpdateTime - 更新时间区间（毫秒，Between）
	UpdateTime []int64 `json:"updateTime,omitempty"`
	// AcceptanceDate - 验收日期区间（毫秒，Between）
	AcceptanceDate []int64 `json:"acceptanceDate,omitempty"`
	// OnlyTrashed - 仅回收站数据（仅管理员生效）
	OnlyTrashed bool `json:"onlyTrashed,omitempty"`
	// WithTrashed - 包含回收站数据（仅管理员生效）
	WithTrashed bool `json:"withTrashed,omitempty"`
}

// ============================= 部署实例 =============================

// DeploymentInstance - 部署实例（平台 models/basic.DeploymentInstance）
type DeploymentInstance struct {
	// Id - 实例ID
	Id int `json:"id"`
	// InstanceNo - 实例编号
	InstanceNo string `json:"instanceNo"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// Environment - 环境
	Environment string `json:"environment"`
	// DeploymentType - 部署类型
	DeploymentType string `json:"deploymentType"`
	// Domain - 访问域名
	Domain string `json:"domain"`
	// ServerFingerprint - 服务器指纹（平台只存加盐哈希）
	ServerFingerprint string `json:"serverFingerprint"`
	// CurrentVersion - 实际运行版本
	CurrentVersion string `json:"currentVersion"`
	// LastSeenAt - 最近心跳时间（毫秒）
	LastSeenAt int64 `json:"lastSeenAt"`
	// LicenseStatus - 当前授权状态
	LicenseStatus string `json:"licenseStatus"`
	// NetworkMode - 网络模式
	NetworkMode string `json:"networkMode"`
	// IsBillable - 是否计费
	IsBillable bool `json:"isBillable"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// InstanceInput - 部署实例新增/修改参数（平台 types.DeploymentInstance；Create 时 Id 留空，Update 时 Id 必填）
// ServerFingerprint 提交原文，平台加盐哈希后存储（不回存原文）。
type InstanceInput struct {
	// Id - 实例ID（Update 必填）
	Id int `json:"id,omitempty"`
	// InstanceNo - 实例编号（留空由平台自动生成）
	InstanceNo string `json:"instanceNo,omitempty"`
	// ProjectId - 项目ID（Create 必填）
	ProjectId int `json:"projectId,omitempty"`
	// Environment - 环境
	Environment string `json:"environment,omitempty"`
	// DeploymentType - 部署类型
	DeploymentType string `json:"deploymentType,omitempty"`
	// Domain - 访问域名
	Domain string `json:"domain,omitempty"`
	// ServerFingerprint - 服务器指纹（原文，平台加盐哈希存储）
	ServerFingerprint string `json:"serverFingerprint,omitempty"`
	// CurrentVersion - 实际运行版本
	CurrentVersion string `json:"currentVersion,omitempty"`
	// LastSeenAt - 最近心跳时间（毫秒）
	LastSeenAt int64 `json:"lastSeenAt,omitempty"`
	// LicenseStatus - 当前授权状态
	LicenseStatus string `json:"licenseStatus,omitempty"`
	// NetworkMode - 网络模式
	NetworkMode string `json:"networkMode,omitempty"`
	// IsBillable - 是否计费（需配合 IsBillableSet=true 才生效；否则平台按环境取默认）
	IsBillable bool `json:"isBillable,omitempty"`
	// IsBillableSet - 是否显式设置计费标记
	IsBillableSet bool `json:"isBillableSet,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// InstanceFindParams - 部署实例查询参数（平台 types.DeploymentInstanceFind + 控制器 In/Like/Between 清单）
type InstanceFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "create_at desc"）
	Order string `json:"order,omitempty"`
	// InstanceNo - 实例编号（模糊）
	InstanceNo string `json:"instanceNo,omitempty"`
	// ProjectId - 项目ID（IN）
	ProjectId []int `json:"projectId,omitempty"`
	// UserId - 归属用户ID（IN）
	UserId []int `json:"userId,omitempty"`
	// Environment - 环境（IN）
	Environment []string `json:"environment,omitempty"`
	// DeploymentType - 部署类型（IN）
	DeploymentType []string `json:"deploymentType,omitempty"`
	// Domain - 访问域名（模糊）
	Domain string `json:"domain,omitempty"`
	// NetworkMode - 网络模式（IN）
	NetworkMode []string `json:"networkMode,omitempty"`
	// IsBillable - 是否计费（IN）
	IsBillable []bool `json:"isBillable,omitempty"`
	// CreateTime - 创建时间区间（毫秒，Between）
	CreateTime []int64 `json:"createTime,omitempty"`
	// UpdateTime - 更新时间区间（毫秒，Between）
	UpdateTime []int64 `json:"updateTime,omitempty"`
	// LastSeenAt - 最近心跳时间区间（毫秒，Between）
	LastSeenAt []int64 `json:"lastSeenAt,omitempty"`
	// OnlyTrashed - 仅回收站数据（仅管理员生效）
	OnlyTrashed bool `json:"onlyTrashed,omitempty"`
	// WithTrashed - 包含回收站数据（仅管理员生效）
	WithTrashed bool `json:"withTrashed,omitempty"`
}

// ============================= 许可证与授权申请 =============================

// License - 许可证（平台 models/basic.License）
type License struct {
	// Id - 许可证ID
	Id int `json:"id"`
	// LicenseNo - 许可证编号（LIC-{年}-%06d）
	LicenseNo string `json:"licenseNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId"`
	// InstanceId - 部署实例ID
	InstanceId int `json:"instanceId"`
	// Environment - 环境
	Environment string `json:"environment"`
	// LicenseType - 许可证类型
	LicenseType string `json:"licenseType"`
	// Status - 状态（active/suspended/revoked）
	Status string `json:"status"`
	// ValidFrom - 生效时间（毫秒）
	ValidFrom int64 `json:"validFrom"`
	// ValidUntil - 到期时间（毫秒，0=永久）
	ValidUntil int64 `json:"validUntil"`
	// MaintenanceUntil - 维保到期时间（毫秒，0=不限制）
	MaintenanceUntil int64 `json:"maintenanceUntil"`
	// UpgradeUntil - 升级权到期时间（毫秒，0=不限制）
	UpgradeUntil int64 `json:"upgradeUntil"`
	// GraceDays - 宽限期（天）
	GraceDays int `json:"graceDays"`
	// VersionRange - 允许的版本范围
	VersionRange string `json:"versionRange"`
	// Payload - 签发载荷快照（JSON 原文，可用 aide/licence 包 Parse 解析）
	Payload string `json:"payload"`
	// Signature - 载荷签名（hex）
	Signature string `json:"signature"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// LicenseApplication - 授权申请（平台 models/basic.LicenseApplication）
// 状态机：pending → issued（审批通过自动签发）/ rejected / cancelled（申请人撤回）
type LicenseApplication struct {
	// Id - 申请ID
	Id int `json:"id"`
	// ApplyNo - 申请编号
	ApplyNo string `json:"applyNo"`
	// UserId - 申请人用户ID
	UserId int `json:"userId"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId"`
	// InstanceId - 部署实例ID
	InstanceId int `json:"instanceId"`
	// LicenseType - 申请的许可证类型
	LicenseType string `json:"licenseType"`
	// Environment - 环境
	Environment string `json:"environment"`
	// Reason - 申请说明
	Reason string `json:"reason"`
	// RequestPayload - 申请人期望的权益请求（JSON 原文）
	RequestPayload string `json:"requestPayload"`
	// Status - 状态（pending/issued/rejected/cancelled）
	Status string `json:"status"`
	// ReviewNote - 审批意见
	ReviewNote string `json:"reviewNote"`
	// ReviewerId - 审批人用户ID
	ReviewerId int `json:"reviewerId"`
	// ReviewedAt - 审批时间（毫秒）
	ReviewedAt int64 `json:"reviewedAt"`
	// LicenseId - 签发回填的许可证ID
	LicenseId int `json:"licenseId"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// LicenseHistory - 许可证变更历史（平台 models/basic.LicenseHistory，只增不改）
type LicenseHistory struct {
	// Id - 历史ID
	Id int `json:"id"`
	// LicenseId - 许可证ID
	LicenseId int `json:"licenseId"`
	// Action - 变更动作（issue/renew/suspend/revoke/reissue）
	Action string `json:"action"`
	// BeforePayload - 变更前快照（JSON 原文）
	BeforePayload string `json:"beforePayload"`
	// AfterPayload - 变更后快照（JSON 原文）
	AfterPayload string `json:"afterPayload"`
	// OperatorId - 操作人用户ID
	OperatorId int `json:"operatorId"`
	// Reason - 变更原因
	Reason string `json:"reason"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// Activation - 许可证在线激活记录（平台 models/basic.Activation）
type Activation struct {
	// Id - 激活记录ID
	Id int `json:"id"`
	// ActivationNo - 激活编号
	ActivationNo string `json:"activationNo"`
	// LicenseId - 许可证ID
	LicenseId int `json:"licenseId"`
	// InstanceId - 部署实例ID
	InstanceId int `json:"instanceId"`
	// FingerprintHash - 服务器指纹哈希
	FingerprintHash string `json:"fingerprintHash"`
	// Challenge - 挑战值
	Challenge string `json:"challenge"`
	// TokenHash - 激活令牌哈希（SHA-256，平台不存原文）
	TokenHash string `json:"tokenHash"`
	// ClientPublicKey - 客户端请求验签公钥（Ed25519 hex）
	ClientPublicKey string `json:"clientPublicKey"`
	// Status - 状态（active/grace/expired）
	Status string `json:"status"`
	// ActivatedAt - 激活时间（毫秒）
	ActivatedAt int64 `json:"activatedAt"`
	// LastRefreshAt - 最近刷新时间（毫秒）
	LastRefreshAt int64 `json:"lastRefreshAt"`
	// ExpiresAt - 过期时间（毫秒）
	ExpiresAt int64 `json:"expiresAt"`
	// LastClientTime - 最近客户端上报时间（毫秒）
	LastClientTime int64 `json:"lastClientTime"`
	// LicenseVersion - 激活时的许可证乐观锁版本
	LicenseVersion int `json:"licenseVersion"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
}

// LicensePayloadView - 签发载荷视图（GET /api/licenses/take-payload 的 data）
type LicensePayloadView struct {
	// LicenseNo - 许可证编号
	LicenseNo string `json:"licenseNo"`
	// Payload - 签发载荷快照（JSON 原文，可用 aide/licence 包 Parse 解析并验签）
	Payload string `json:"payload"`
	// Signature - 载荷签名（hex）
	Signature string `json:"signature"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
}

// LicenseApplyInput - 提交授权申请参数（平台 types.LicenseApply；归属用户取登录态）
type LicenseApplyInput struct {
	// ProjectId - 项目ID（必填，须归本人所有）
	ProjectId int `json:"projectId"`
	// InstanceId - 部署实例ID（可选，须属于该项目）
	InstanceId int `json:"instanceId,omitempty"`
	// LicenseType - 许可证类型（必填）
	LicenseType string `json:"licenseType"`
	// Environment - 环境（必填）
	Environment string `json:"environment"`
	// Reason - 申请说明（必填，最长 512）
	Reason string `json:"reason"`
	// RequestPayload - 期望的权益/额度/期限（自由 JSON，审批人可调整后签发）
	RequestPayload map[string]any `json:"requestPayload,omitempty"`
}

// LicenseBindingInput - 签发载荷中的绑定信息（平台 types.LicenseBindingInput）
type LicenseBindingInput struct {
	// Type - 绑定类型
	Type string `json:"type"`
	// Value - 绑定值
	Value string `json:"value"`
}

// LicenseIssuePayload - 签发载荷参数（平台 types.LicenseIssuePayload，审批通过/续期/重新签发共用）
// 四个期限为毫秒时间戳，0 = 不限制（ValidUntil 为 0 即永久授权）。
type LicenseIssuePayload struct {
	// LicenseType - 许可证类型
	LicenseType string `json:"licenseType,omitempty"`
	// Environment - 环境
	Environment string `json:"environment,omitempty"`
	// InstanceId - 部署实例ID
	InstanceId int `json:"instanceId,omitempty"`
	// ValidFrom - 生效时间（毫秒）
	ValidFrom int64 `json:"validFrom,omitempty"`
	// ValidUntil - 到期时间（毫秒，0=永久）
	ValidUntil int64 `json:"validUntil,omitempty"`
	// MaintenanceUntil - 维保到期时间（毫秒，0=不限制）
	MaintenanceUntil int64 `json:"maintenanceUntil,omitempty"`
	// UpgradeUntil - 升级权到期时间（毫秒，0=不限制）
	UpgradeUntil int64 `json:"upgradeUntil,omitempty"`
	// GraceDays - 宽限期（天）
	GraceDays int `json:"graceDays,omitempty"`
	// VersionRange - 允许的版本范围
	VersionRange string `json:"versionRange,omitempty"`
	// Features - 功能权益
	Features map[string]bool `json:"features,omitempty"`
	// Limits - 额度限制
	Limits map[string]int64 `json:"limits,omitempty"`
	// Binding - 绑定信息
	Binding *LicenseBindingInput `json:"binding,omitempty"`
}

// LicenseReviewInput - 审批授权申请参数（平台 types.LicenseReview，需 license.review 权限）
type LicenseReviewInput struct {
	// Id - 申请ID（必填）
	Id int `json:"id"`
	// Action - 审批动作（approve/reject，必填；approve 自动签发许可证）
	Action string `json:"action"`
	// ReviewNote - 审批意见（reject 时必填）
	ReviewNote string `json:"reviewNote,omitempty"`
	// IssuePayload - 签发参数（approve 时可选；为空时从申请的 requestPayload 回退解析）
	IssuePayload *LicenseIssuePayload `json:"issuePayload,omitempty"`
}

// LicenseActionInput - 许可证运维操作参数（平台 types.LicenseAction，renew/suspend/revoke/reissue 共用）
type LicenseActionInput struct {
	// Id - 许可证ID（必填）
	Id int `json:"id"`
	// Reason - 操作原因（suspend/revoke 必填，renew/reissue 可选）
	Reason string `json:"reason,omitempty"`
	// ApproverId - 预留审批人字段
	ApproverId int `json:"approverId,omitempty"`
	// ValidUntil - 新到期时间（毫秒，renew 生效，0=保留原值）
	ValidUntil int64 `json:"validUntil,omitempty"`
	// MaintenanceUntil - 新维保到期（毫秒，renew 生效，0=保留原值）
	MaintenanceUntil int64 `json:"maintenanceUntil,omitempty"`
	// UpgradeUntil - 新升级权到期（毫秒，renew 生效，0=保留原值）
	UpgradeUntil int64 `json:"upgradeUntil,omitempty"`
	// IssuePayload - 覆盖签发参数（reissue 生效；为空字段沿用现载荷）
	IssuePayload *LicenseIssuePayload `json:"issuePayload,omitempty"`
}

// LicenseFindParams - 许可证查询参数（平台 types.LicenseFind；userId 仅审批视角生效）
type LicenseFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Status - 许可证状态
	Status string `json:"status,omitempty"`
	// UserId - 归属用户ID（审批视角生效）
	UserId int `json:"userId,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
}

// LicenseApplicationFindParams - 授权申请查询参数（平台 types.LicenseApplicationFind；userId 仅审批视角生效）
type LicenseApplicationFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Status - 申请状态
	Status string `json:"status,omitempty"`
	// UserId - 申请人用户ID（审批视角生效）
	UserId int `json:"userId,omitempty"`
}

// LicenseHistoryFindParams - 许可证变更历史分页参数（平台 types.LicenseHistoryFind）
type LicenseHistoryFindParams struct {
	// LicenseId - 许可证ID（必填）
	LicenseId int `json:"licenseId"`
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
}

// ActivationFindParams - 激活记录查询参数（审批视角支持 licenseId/status 筛选）
type ActivationFindParams struct {
	// LicenseId - 许可证ID（审批视角生效）
	LicenseId int `json:"licenseId,omitempty"`
	// Status - 状态（审批视角生效）
	Status string `json:"status,omitempty"`
}

// ============================= 签名密钥 =============================

// SigningKeyPublic - 公钥导出结果（GET /api/signing-keys/public 的 data）
type SigningKeyPublic struct {
	// Purpose - 密钥用途（license/release）
	Purpose string `json:"purpose"`
	// KeyVersion - 密钥版本
	KeyVersion string `json:"keyVersion"`
	// Algorithm - 签名算法（Ed25519）
	Algorithm string `json:"algorithm"`
	// PublicKey - 公钥（hex）
	PublicKey string `json:"publicKey"`
}

// ============================= 项目版本 =============================

// ProjectVersion - 项目版本（平台 models/basic.ProjectVersion）
type ProjectVersion struct {
	// Id - 版本ID
	Id int `json:"id"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId"`
	// Version - 语义版本号
	Version string `json:"version"`
	// BuildNumber - 构建号
	BuildNumber string `json:"buildNumber"`
	// GitRepo - 源码仓库
	GitRepo string `json:"gitRepo"`
	// GitBranch - 源码分支
	GitBranch string `json:"gitBranch"`
	// GitTag - 源码标签
	GitTag string `json:"gitTag"`
	// GitCommit - 提交哈希
	GitCommit string `json:"gitCommit"`
	// PipelineNo - 流水线编号
	PipelineNo string `json:"pipelineNo"`
	// SourceVersionRange - 可升级来源版本范围
	SourceVersionRange string `json:"sourceVersionRange"`
	// MinUpgradeVersion - 最低可升级版本
	MinUpgradeVersion string `json:"minUpgradeVersion"`
	// OsArch - 平台架构（如 linux/amd64）
	OsArch string `json:"osArch"`
	// MigrationVersion - 数据库迁移版本
	MigrationVersion string `json:"migrationVersion"`
	// ConfigChanges - 配置变更说明
	ConfigChanges string `json:"configChanges"`
	// NeedDowntime - 是否需要停机
	NeedDowntime bool `json:"needDowntime"`
	// EstimatedDuration - 预计升级时长（分钟）
	EstimatedDuration int `json:"estimatedDuration"`
	// RollbackPlan - 回滚方案
	RollbackPlan string `json:"rollbackPlan"`
	// TestReport - 测试报告
	TestReport string `json:"testReport"`
	// Status - 状态（draft/testing/released/archived）
	Status string `json:"status"`
	// ReleasedAt - 发布时间（毫秒）
	ReleasedAt int64 `json:"releasedAt"`
	// SupportUntil - 停止支持时间（毫秒）
	SupportUntil int64 `json:"supportUntil"`
	// GrayMode - 灰度模式（空=全量/whitelist/percent）
	GrayMode string `json:"grayMode"`
	// GrayInstances - 灰度实例白名单（实例ID）
	GrayInstances []int `json:"grayInstances"`
	// GrayPercent - 灰度百分比（0-100）
	GrayPercent int `json:"grayPercent"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// VersionInput - 版本新增/修改参数（平台 types.ProjectVersion；Create 时 Id 留空，Update 时 Id 必填）
// 新建仅允许 draft/testing 状态，发布与归档必须走 Release/Archive 专用接口；
// released/archived 版本仅 remark/supportUntil 允许修改。
type VersionInput struct {
	// Id - 版本ID（Update 必填）
	Id int `json:"id,omitempty"`
	// ProjectId - 项目ID（Create 必填；Update 时必须与现值一致，项目不可改）
	ProjectId int `json:"projectId,omitempty"`
	// Version - 语义版本号（必填）
	Version string `json:"version,omitempty"`
	// BuildNumber - 构建号（必填）
	BuildNumber string `json:"buildNumber,omitempty"`
	// GitRepo - 源码仓库（留回落为项目仓库地址）
	GitRepo string `json:"gitRepo,omitempty"`
	// GitBranch - 源码分支
	GitBranch string `json:"gitBranch,omitempty"`
	// GitTag - 源码标签
	GitTag string `json:"gitTag,omitempty"`
	// GitCommit - 提交哈希
	GitCommit string `json:"gitCommit,omitempty"`
	// PipelineNo - 流水线编号
	PipelineNo string `json:"pipelineNo,omitempty"`
	// SourceVersionRange - 可升级来源版本范围
	SourceVersionRange string `json:"sourceVersionRange,omitempty"`
	// MinUpgradeVersion - 最低可升级版本
	MinUpgradeVersion string `json:"minUpgradeVersion,omitempty"`
	// OsArch - 平台架构
	OsArch string `json:"osArch,omitempty"`
	// MigrationVersion - 数据库迁移版本
	MigrationVersion string `json:"migrationVersion,omitempty"`
	// ConfigChanges - 配置变更说明
	ConfigChanges string `json:"configChanges,omitempty"`
	// NeedDowntime - 是否需要停机
	NeedDowntime bool `json:"needDowntime,omitempty"`
	// EstimatedDuration - 预计升级时长（分钟）
	EstimatedDuration int `json:"estimatedDuration,omitempty"`
	// RollbackPlan - 回滚方案
	RollbackPlan string `json:"rollbackPlan,omitempty"`
	// TestReport - 测试报告
	TestReport string `json:"testReport,omitempty"`
	// Status - 状态（仅允许 draft/testing）
	Status string `json:"status,omitempty"`
	// SupportUntil - 停止支持时间（毫秒）
	SupportUntil int64 `json:"supportUntil,omitempty"`
	// GrayMode - 灰度模式（空=全量/whitelist/percent）
	GrayMode string `json:"grayMode,omitempty"`
	// GrayInstances - 灰度实例白名单（实例ID）
	GrayInstances []int `json:"grayInstances,omitempty"`
	// GrayPercent - 灰度百分比（0-100）
	GrayPercent int `json:"grayPercent,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// VersionFindParams - 版本查询参数（平台 types.ProjectVersionFind + 控制器 In/Like/Between 清单）
type VersionFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "create_at desc"）
	Order string `json:"order,omitempty"`
	// ProjectId - 项目ID（IN）
	ProjectId []int `json:"projectId,omitempty"`
	// Version - 语义版本号（模糊）
	Version string `json:"version,omitempty"`
	// BuildNumber - 构建号（模糊）
	BuildNumber string `json:"buildNumber,omitempty"`
	// Status - 状态（IN）
	Status []string `json:"status,omitempty"`
	// OsArch - 平台架构（IN）
	OsArch []string `json:"osArch,omitempty"`
	// PipelineNo - 流水线编号（模糊）
	PipelineNo string `json:"pipelineNo,omitempty"`
	// ReleasedAt - 发布时间区间（毫秒，Between）
	ReleasedAt []int64 `json:"releasedAt,omitempty"`
	// SupportUntil - 停止支持时间区间（毫秒，Between）
	SupportUntil []int64 `json:"supportUntil,omitempty"`
	// CreateTime - 创建时间区间（毫秒，Between）
	CreateTime []int64 `json:"createTime,omitempty"`
	// UpdateTime - 更新时间区间（毫秒，Between）
	UpdateTime []int64 `json:"updateTime,omitempty"`
	// OnlyTrashed - 仅回收站数据（仅管理员生效）
	OnlyTrashed bool `json:"onlyTrashed,omitempty"`
	// WithTrashed - 包含回收站数据（仅管理员生效）
	WithTrashed bool `json:"withTrashed,omitempty"`
}

// ============================= 项目发布物 =============================

// ProjectArtifact - 项目发布物（平台 models/basic.ProjectArtifact）
type ProjectArtifact struct {
	// Id - 发布物ID
	Id int `json:"id"`
	// ArtifactNo - 发布物编号（ART-{年}-%06d）
	ArtifactNo string `json:"artifactNo"`
	// VersionId - 版本ID
	VersionId int `json:"versionId"`
	// FileName - 原始文件名
	FileName string `json:"fileName"`
	// Size - 文件大小（字节）
	Size int64 `json:"size"`
	// StorageDriver - 存储驱动
	StorageDriver string `json:"storageDriver"`
	// StoragePath - 存储路径
	StoragePath string `json:"storagePath"`
	// Url - 访问地址
	Url string `json:"url"`
	// Sha256 - SHA-256 摘要（服务端按上传字节计算）
	Sha256 string `json:"sha256"`
	// Signature - 发布物签名（hex，release-key 对 artifactNo/version/sha256 签名）
	Signature string `json:"signature"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
	// ArtifactType - 发布物类型（full/incr 等）
	ArtifactType string `json:"artifactType"`
	// SourceVersion - 增量包来源版本
	SourceVersion string `json:"sourceVersion"`
	// TargetVersion - 增量包目标版本
	TargetVersion string `json:"targetVersion"`
	// OsArch - 平台架构
	OsArch string `json:"osArch"`
	// ScanStatus - 病毒扫描状态
	ScanStatus string `json:"scanStatus"`
	// IsLocked - 是否已锁定（签名即锁定，锁定后禁止删除）
	IsLocked bool `json:"isLocked"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ArtifactUploadInput - 发布物上传附加参数（平台 types.ProjectArtifactUpload；文件字段固定为 file）
type ArtifactUploadInput struct {
	// VersionId - 版本ID（必填，版本须未发布/未归档且项目可写）
	VersionId int `json:"versionId"`
	// ArtifactType - 发布物类型（默认 full）
	ArtifactType string `json:"artifactType,omitempty"`
	// SourceVersion - 增量包来源版本
	SourceVersion string `json:"sourceVersion,omitempty"`
	// TargetVersion - 增量包目标版本
	TargetVersion string `json:"targetVersion,omitempty"`
	// OsArch - 平台架构
	OsArch string `json:"osArch,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// ArtifactUpdateInput - 发布物元数据更新参数（平台 types.ProjectArtifactUpdate；已锁定记录仅放行扫描状态）
type ArtifactUpdateInput struct {
	// Id - 发布物ID（必填）
	Id int `json:"id"`
	// ScanStatus - 病毒扫描状态
	ScanStatus string `json:"scanStatus,omitempty"`
	// Remark - 备注（已锁定记录不生效）
	Remark string `json:"remark,omitempty"`
}

// ArtifactVerifyResult - 发布物验签结果（POST /api/project-artifacts/verify 的 data）
type ArtifactVerifyResult struct {
	// Id - 发布物ID
	Id int `json:"id"`
	// ArtifactNo - 发布物编号
	ArtifactNo string `json:"artifactNo"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
	// Sha256 - 库内登记的 SHA-256 摘要
	Sha256 string `json:"sha256"`
	// RecomputedSha256 - 上传文件重算的 SHA-256（未上传文件时为空）
	RecomputedSha256 string `json:"recomputedSha256"`
	// HashMatch - 重算摘要与库内摘要是否一致
	HashMatch bool `json:"hashMatch"`
	// SignatureValid - 签名验签是否通过
	SignatureValid bool `json:"signatureValid"`
	// Valid - 综合结论（HashMatch && SignatureValid）
	Valid bool `json:"valid"`
}

// ArtifactFindParams - 发布物查询参数（平台 types.ProjectArtifactFind + 控制器 In/Like/Between 清单）
type ArtifactFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "create_at desc"）
	Order string `json:"order,omitempty"`
	// VersionId - 版本ID（IN）
	VersionId []int `json:"versionId,omitempty"`
	// ArtifactNo - 发布物编号（模糊）
	ArtifactNo string `json:"artifactNo,omitempty"`
	// FileName - 文件名（模糊）
	FileName string `json:"fileName,omitempty"`
	// Sha256 - SHA-256 摘要（模糊）
	Sha256 string `json:"sha256,omitempty"`
	// ArtifactType - 发布物类型（IN）
	ArtifactType []string `json:"artifactType,omitempty"`
	// ScanStatus - 病毒扫描状态（IN）
	ScanStatus []string `json:"scanStatus,omitempty"`
	// KeyVersion - 签名密钥版本（IN）
	KeyVersion []string `json:"keyVersion,omitempty"`
	// OsArch - 平台架构（IN）
	OsArch []string `json:"osArch,omitempty"`
	// IsLocked - 是否已锁定（IN）
	IsLocked []bool `json:"isLocked,omitempty"`
	// CreateTime - 创建时间区间（毫秒，Between）
	CreateTime []int64 `json:"createTime,omitempty"`
	// UpdateTime - 更新时间区间（毫秒，Between）
	UpdateTime []int64 `json:"updateTime,omitempty"`
	// OnlyTrashed - 仅回收站数据（仅管理员生效）
	OnlyTrashed bool `json:"onlyTrashed,omitempty"`
	// WithTrashed - 包含回收站数据（仅管理员生效）
	WithTrashed bool `json:"withTrashed,omitempty"`
}

// ============================= 通用结果 =============================

// IdResult - 单 ID 结果（create/update/release 等接口的 data）
type IdResult struct {
	// Id - 记录ID
	Id int `json:"id"`
}

// IdsResult - 批量 ID 结果（remove/delete/restore/clear 等接口的 data）
type IdsResult struct {
	// Ids - 受影响的记录ID列表
	Ids []int `json:"ids"`
}

// ApplyResult - 申请提交结果（资格/授权申请 apply 接口的 data）
type ApplyResult struct {
	// Id - 申请ID
	Id int `json:"id"`
	// ApplyNo - 申请编号
	ApplyNo string `json:"applyNo"`
}

// ReviewResult - 审批结果（授权申请 review 接口的 data；approve 时返回签发的许可证信息）
type ReviewResult struct {
	// Id - 申请ID（approve 时为签发的许可证ID）
	Id int `json:"id"`
	// Action - 审批动作
	Action string `json:"action"`
	// LicenseNo - 签发的许可证编号（仅 approve 返回）
	LicenseNo string `json:"licenseNo"`
}

// LicenseNoResult - 许可证编号结果（renew/reissue 接口的 data）
type LicenseNoResult struct {
	// Id - 许可证ID
	Id int `json:"id"`
	// LicenseNo - 许可证编号
	LicenseNo string `json:"licenseNo"`
}

// StatusResult - 状态流转结果（suspend/revoke 接口的 data）
type StatusResult struct {
	// Id - 许可证ID
	Id int `json:"id"`
	// Status - 目标状态
	Status string `json:"status"`
}

// ReleaseResult - 版本发布结果（release 接口的 data）
type ReleaseResult struct {
	// Id - 版本ID
	Id int `json:"id"`
	// Version - 语义版本号
	Version string `json:"version"`
}

// ============================= SaaS 菜单清单 =============================

// SaasMenuManifest - SaaS 应用菜单清单（平台 models/basic.SaasMenuManifest）
// 项目级版本化清单：projectId + version 联合唯一；每个项目同一时刻仅一条 published。
type SaasMenuManifest struct {
	// Id - 清单ID
	Id int `json:"id"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// UserId - 归属用户ID（冗余自项目）
	UserId int `json:"userId"`
	// Version - 清单版本号（项目内递增）
	Version int `json:"version"`
	// Manifest - 清单原文（JSON 字符串，结构见平台 types.SaasManifest）
	Manifest string `json:"manifest"`
	// Status - 状态（draft/published/archived）
	Status string `json:"status"`
	// PublishedAt - 发布时间（毫秒）
	PublishedAt int64 `json:"publishedAt"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// SaasMenuFindParams - 菜单清单分页查询参数（平台 types.SaasMenuManifestFind）
type SaasMenuFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// Status - 清单状态（draft/published/archived）
	Status string `json:"status,omitempty"`
}

// SaasMenuSaveInput - 保存菜单清单草稿参数（平台 types.SaasMenuManifestSave）
// Id=0 新建递增版本草稿；否则更新既有 draft 行（published/archived 不可改）。
type SaasMenuSaveInput struct {
	// Id - 清单ID（0=新建版本草稿）
	Id int `json:"id,omitempty"`
	// ProjectId - 项目ID（必填）
	ProjectId int `json:"projectId"`
	// Manifest - 清单原文（JSON，草稿允许半成品，发布时才做完整结构校验）
	Manifest string `json:"manifest"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// SaasMenuSaveResult - 清单保存/发布结果（save/publish 接口的 data）
type SaasMenuSaveResult struct {
	// Id - 清单ID
	Id int `json:"id"`
	// Version - 清单版本号
	Version int `json:"version"`
}

// ============================= SaaS 功能字典 =============================

// SaasFeatureDict - 项目功能字典（平台 models/basic.SaasFeatureDict）
// 功能编码项目内唯一；disabled 后套餐不可再引用，存量已签发信封不受影响。
type SaasFeatureDict struct {
	// Id - 字典ID
	Id int `json:"id"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// UserId - 归属用户ID（冗余自项目）
	UserId int `json:"userId"`
	// FeatureCode - 功能编码（登记后不可改）
	FeatureCode string `json:"featureCode"`
	// FeatureName - 功能名称
	FeatureName string `json:"featureName"`
	// Description - 功能说明
	Description string `json:"description"`
	// Status - 状态（enabled/disabled）
	Status string `json:"status"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// SaasFeatureFindParams - 功能字典分页查询参数（平台 types.SaasFeatureFind）
type SaasFeatureFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// Status - 状态（enabled/disabled）
	Status string `json:"status,omitempty"`
}

// SaasFeatureSaveInput - 功能字典登记/修改参数（平台 types.SaasFeatureSave）
// Id=0 登记；修改时 featureCode 必须与现值一致（登记后不可改，仅名称/说明可改）。
type SaasFeatureSaveInput struct {
	// Id - 字典ID（0=登记）
	Id int `json:"id,omitempty"`
	// ProjectId - 项目ID（必填）
	ProjectId int `json:"projectId"`
	// FeatureCode - 功能编码（必填，小写字母/数字开头，段间可含 . _ -）
	FeatureCode string `json:"featureCode"`
	// FeatureName - 功能名称（必填）
	FeatureName string `json:"featureName"`
	// Description - 功能说明
	Description string `json:"description,omitempty"`
}

// ============================= SaaS 套餐 =============================

// SaasPlan - 套餐模板（平台 models/basic.SaasPlan）
// features/limits/menuCodes 在模型层为 JSON 文本，读取时按原文返回（可自行 unmarshal）。
type SaasPlan struct {
	// Id - 套餐ID
	Id int `json:"id"`
	// PlanNo - 套餐编号（PLN-{年}-%06d）
	PlanNo string `json:"planNo"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// PlanCode - 套餐编码（签发入载荷，创建后不可改）
	PlanCode string `json:"planCode"`
	// PlanName - 套餐名称
	PlanName string `json:"planName"`
	// Description - 套餐描述
	Description string `json:"description"`
	// Features - 功能权益（JSON map[string]bool 原文）
	Features string `json:"features"`
	// Limits - 额度（JSON map[string]int64 原文）
	Limits string `json:"limits"`
	// MenuCodes - 菜单编码数组（JSON 原文，当前 published 清单 code 子集）
	MenuCodes string `json:"menuCodes"`
	// ManifestVersion - 选单依据的清单版本（溯源用）
	ManifestVersion int `json:"manifestVersion"`
	// Status - 状态（draft/enabled/disabled；仅 enabled 可被订阅）
	Status string `json:"status"`
	// Sort - 排序
	Sort int `json:"sort"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// SaasPlanFindParams - 套餐分页查询参数（平台 types.SaasPlanFind）
type SaasPlanFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// Status - 套餐状态（draft/enabled/disabled）
	Status string `json:"status,omitempty"`
}

// SaasPlanSaveInput - 套餐新建/修改参数（平台 types.SaasPlanSave）
// Create 时 Id 留空；Update 时 Id 必填且 planCode 须与现值一致；
// 保存即做功能字典引用与菜单子集校验（menuCodes 须为当前 published 清单 code 子集）。
type SaasPlanSaveInput struct {
	// Id - 套餐ID（Update 必填，Create 留空）
	Id int `json:"id,omitempty"`
	// ProjectId - 项目ID（必填）
	ProjectId int `json:"projectId"`
	// PlanCode - 套餐编码（必填，创建后不可改）
	PlanCode string `json:"planCode"`
	// PlanName - 套餐名称（必填）
	PlanName string `json:"planName"`
	// Description - 套餐描述
	Description string `json:"description,omitempty"`
	// Features - 功能权益（每个 key 须命中功能字典 enabled）
	Features map[string]bool `json:"features,omitempty"`
	// Limits - 额度
	Limits map[string]int64 `json:"limits,omitempty"`
	// MenuCodes - 菜单编码数组（当前 published 清单 code 子集）
	MenuCodes []string `json:"menuCodes,omitempty"`
	// Sort - 排序
	Sort int `json:"sort,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// PlanNoResult - 套餐新建结果（create 接口的 data）
type PlanNoResult struct {
	// Id - 套餐ID
	Id int `json:"id"`
	// PlanNo - 套餐编号
	PlanNo string `json:"planNo"`
}

// ============================= SaaS 租户 =============================

// SaasTenant - SaaS 租户（平台 models/basic.SaasTenant）
// 主状态机：pending → active ↔ suspended → revoked（不可逆）；
// 时间派生态由 validUntil + graceDays 调用时即时判定，不入库。
type SaasTenant struct {
	// Id - 租户ID
	Id int `json:"id"`
	// TenantNo - 租户编号（TEN-{年}-%06d）
	TenantNo string `json:"tenantNo"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// TenantCode - 租户编码（SaaS 应用上送，项目内唯一）
	TenantCode string `json:"tenantCode"`
	// TenantName - 租户名称（非权益字段，直改生效）
	TenantName string `json:"tenantName"`
	// Contact - 联系人信息（JSON 原文：name/phone/email，仅备案）
	Contact string `json:"contact"`
	// SubscriptionType - 订阅类型（trial/official）
	SubscriptionType string `json:"subscriptionType"`
	// PlanId - 当前订阅套餐ID
	PlanId int `json:"planId"`
	// Overrides - 个性化覆盖（JSON 原文，增量合并到套餐基线）
	Overrides string `json:"overrides"`
	// Environment - 环境
	Environment string `json:"environment"`
	// ValidFrom - 生效时间（毫秒）
	ValidFrom int64 `json:"validFrom"`
	// ValidUntil - 到期时间（毫秒，0=永久）
	ValidUntil int64 `json:"validUntil"`
	// GraceDays - 宽限期（天）
	GraceDays int `json:"graceDays"`
	// VersionRange - 允许的 SaaS 应用版本范围
	VersionRange string `json:"versionRange"`
	// Status - 状态（pending/active/suspended/revoked）
	Status string `json:"status"`
	// Payload - 最近签发的载荷快照（JSON 原文，仅归属人/平台可见）
	Payload string `json:"payload"`
	// Signature - 载荷签名（hex）
	Signature string `json:"signature"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// SaasTenantFindParams - 租户分页查询参数（平台 types.SaasTenantFind；userId 仅平台视角生效）
type SaasTenantFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// UserId - 归属用户ID（仅平台视角筛选生效）
	UserId int `json:"userId,omitempty"`
	// Status - 租户状态（pending/active/suspended/revoked）
	Status string `json:"status,omitempty"`
	// Environment - 环境（dev/test/staging/production/dr）
	Environment string `json:"environment,omitempty"`
}

// SaasTenantPayloadView - 租户授权原文视图（take-payload 接口的 data）
type SaasTenantPayloadView struct {
	// TenantNo - 租户编号
	TenantNo string `json:"tenantNo"`
	// Payload - 载荷快照（JSON 原文，可用本包 TenantPayload 解析并验签）
	Payload string `json:"payload"`
	// Signature - 载荷签名（hex）
	Signature string `json:"signature"`
	// KeyVersion - 签名密钥版本
	KeyVersion string `json:"keyVersion"`
}

// SaasOverrideMenus - 个性化覆盖的菜单增删结构（平台 types.SaasOverrideMenus）
type SaasOverrideMenus struct {
	// Add - 新增菜单编码
	Add []string `json:"add,omitempty"`
	// Remove - 移除菜单编码
	Remove []string `json:"remove,omitempty"`
}

// SaasOverrides - 租户个性化覆盖（套餐基线的增量，平台 types.SaasOverrides）
type SaasOverrides struct {
	// Features - 功能开关覆盖（true 加购 / false 裁剪，key 须命中功能字典）
	Features map[string]bool `json:"features,omitempty"`
	// Limits - 额度覆盖
	Limits map[string]int64 `json:"limits,omitempty"`
	// Menus - 菜单增删（在套餐 menuCodes 基础上）
	Menus SaasOverrideMenus `json:"menus,omitempty"`
}

// SaasTenantContact - 租户联系人（仅备案，非权益字段，平台 types.SaasTenantContact）
type SaasTenantContact struct {
	// Name - 联系人姓名
	Name string `json:"name,omitempty"`
	// Phone - 联系电话
	Phone string `json:"phone,omitempty"`
	// Email - 联系邮箱
	Email string `json:"email,omitempty"`
}

// SaasTenantSubscribeInput - 租户开通申请参数（平台 types.SaasTenantSubscribe）
// member 走申请单（命中自动过单则即时生效）；platform 直通生效不产生申请单。
type SaasTenantSubscribeInput struct {
	// ProjectId - 项目ID（必填，delivery_mode=saas 且归当前用户可写范围）
	ProjectId int `json:"projectId"`
	// PlanId - 套餐ID（必填，须属于该项目且 enabled）
	PlanId int `json:"planId"`
	// TenantCode - 租户编码（必填，项目内唯一）
	TenantCode string `json:"tenantCode"`
	// TenantName - 租户名称（必填）
	TenantName string `json:"tenantName"`
	// Contact - 联系人信息（可选）
	Contact *SaasTenantContact `json:"contact,omitempty"`
	// SubscriptionType - 订阅类型（trial/official，必填）
	SubscriptionType string `json:"subscriptionType"`
	// Overrides - 个性化覆盖（可选）
	Overrides *SaasOverrides `json:"overrides,omitempty"`
	// Environment - 环境（dev/test/staging/production/dr，必填）
	Environment string `json:"environment"`
	// ValidFrom - 生效时间（毫秒，0=立即）
	ValidFrom int64 `json:"validFrom,omitempty"`
	// ValidUntil - 到期时间（毫秒，0=永久）
	ValidUntil int64 `json:"validUntil,omitempty"`
	// GraceDays - 宽限期（天）
	GraceDays int `json:"graceDays,omitempty"`
	// VersionRange - 允许的版本范围
	VersionRange string `json:"versionRange,omitempty"`
	// Reason - 申请说明（必填）
	Reason string `json:"reason"`
}

// SaasTenantSubscribeResult - 租户开通结果（subscribe 接口的 data）
// 三种形态：member 待审 {id=申请单ID, applyNo, tenantId}；命中自动过单 {id=租户ID, tenantNo, applyNo, autoApproved=true}；
// platform 直通 {id=租户ID, tenantNo}。
type SaasTenantSubscribeResult struct {
	// Id - 记录ID（待审为申请单ID，生效为租户ID）
	Id int `json:"id"`
	// TenantId - 新建的 pending 租户ID（member 待审时返回）
	TenantId int `json:"tenantId"`
	// TenantNo - 租户编号（生效时返回）
	TenantNo string `json:"tenantNo"`
	// ApplyNo - 申请编号（member 路径返回）
	ApplyNo string `json:"applyNo"`
	// AutoApproved - 是否命中自动过单即时生效
	AutoApproved bool `json:"autoApproved"`
}

// SaasTenantChangeInput - 租户权益变更申请参数（平台 types.SaasTenantChange）
// 仅 active 租户可发起（一律人工审批）；pending 租户为驳回后重新提审；platform 直通生效。
type SaasTenantChangeInput struct {
	// TenantId - 租户ID（必填）
	TenantId int `json:"tenantId"`
	// PlanId - 新套餐ID（必填，须属于该项目且 enabled）
	PlanId int `json:"planId"`
	// SubscriptionType - 订阅类型（trial/official，必填）
	SubscriptionType string `json:"subscriptionType"`
	// Overrides - 个性化覆盖（可选）
	Overrides *SaasOverrides `json:"overrides,omitempty"`
	// Environment - 环境（必填）
	Environment string `json:"environment"`
	// ValidFrom - 生效时间（毫秒，0=立即）
	ValidFrom int64 `json:"validFrom,omitempty"`
	// ValidUntil - 到期时间（毫秒，0=永久）
	ValidUntil int64 `json:"validUntil,omitempty"`
	// GraceDays - 宽限期（天）
	GraceDays int `json:"graceDays,omitempty"`
	// VersionRange - 允许的版本范围
	VersionRange string `json:"versionRange,omitempty"`
	// Reason - 申请说明（必填）
	Reason string `json:"reason"`
}

// SaasTenantChangeResult - 租户变更结果（change 接口的 data）
// member 待审 {id=申请单ID, applyNo}；platform 直通 {id=租户ID, tenantNo}；重新提审命中自动过单时同 subscribe。
type SaasTenantChangeResult struct {
	// Id - 记录ID（待审为申请单ID，生效为租户ID）
	Id int `json:"id"`
	// TenantNo - 租户编号（生效时返回）
	TenantNo string `json:"tenantNo"`
	// ApplyNo - 申请编号（member 路径返回）
	ApplyNo string `json:"applyNo"`
	// AutoApproved - 是否命中自动过单即时生效（重新提审场景）
	AutoApproved bool `json:"autoApproved"`
}

// SaasTenantInfoUpdateInput - 租户非权益字段直改参数（平台 types.SaasTenantInfoUpdate，即时生效 + 审计）
type SaasTenantInfoUpdateInput struct {
	// Id - 租户ID（必填）
	Id int `json:"id"`
	// TenantName - 租户名称（必填）
	TenantName string `json:"tenantName"`
	// Contact - 联系人信息
	Contact *SaasTenantContact `json:"contact,omitempty"`
}

// SaasTenantActionInput - 租户状态机操作参数（平台 types.SaasTenantAction，suspend/resume/revoke 共用）
type SaasTenantActionInput struct {
	// Id - 租户ID（必填）
	Id int `json:"id"`
	// Reason - 操作原因（必填）
	Reason string `json:"reason"`
}

// SaasTenantReissueInput - 租户重签参数（平台 types.SaasTenantReissue）
// 平台纠错通道：以现载荷为基础按入参覆盖重签，空值沿用现载荷；直通不产生申请单。
type SaasTenantReissueInput struct {
	// Id - 租户ID（必填）
	Id int `json:"id"`
	// Reason - 操作原因
	Reason string `json:"reason,omitempty"`
	// Environment - 环境（非空覆盖）
	Environment string `json:"environment,omitempty"`
	// ValidFrom - 生效时间（毫秒，0=保留原值）
	ValidFrom int64 `json:"validFrom,omitempty"`
	// ValidUntil - 到期时间（毫秒，0=保留原值）
	ValidUntil int64 `json:"validUntil,omitempty"`
	// GraceDays - 宽限期（天，0=保留原值）
	GraceDays int `json:"graceDays,omitempty"`
	// VersionRange - 允许的版本范围（非空覆盖）
	VersionRange string `json:"versionRange,omitempty"`
	// Features - 功能权益（非空覆盖）
	Features map[string]bool `json:"features,omitempty"`
	// Limits - 额度（非空覆盖）
	Limits map[string]int64 `json:"limits,omitempty"`
	// MenuCodes - 菜单编码（非空覆盖）
	MenuCodes []string `json:"menuCodes,omitempty"`
}

// SaasTenantNoResult - 租户编号结果（reissue/直通开通等接口的 data）
type SaasTenantNoResult struct {
	// Id - 租户ID
	Id int `json:"id"`
	// TenantNo - 租户编号
	TenantNo string `json:"tenantNo"`
}

// ============================= SaaS 租户申请单 / 审批 =============================

// SaasTenantApplication - 租户开通/变更申请单（平台 models/basic.SaasTenantApplication）
// 状态机：pending → approved / rejected / cancelled；同一租户同时只允许一条 pending。
type SaasTenantApplication struct {
	// Id - 申请单ID
	Id int `json:"id"`
	// ApplyNo - 申请编号（SCA-{年}-%06d）
	ApplyNo string `json:"applyNo"`
	// BizType - 业务类型（subscribe/change）
	BizType string `json:"bizType"`
	// TenantId - 目标租户ID
	TenantId int `json:"tenantId"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// UserId - 申请人用户ID
	UserId int `json:"userId"`
	// RequestPayload - 权益提案快照（JSON 原文：套餐快照+overrides+期限/环境）
	RequestPayload string `json:"requestPayload"`
	// MergedPreview - 合并后权益预览（JSON 原文：features/limits/menuCodes 拍平）
	MergedPreview string `json:"mergedPreview"`
	// Reason - 申请说明
	Reason string `json:"reason"`
	// Status - 状态（pending/approved/rejected/cancelled）
	Status string `json:"status"`
	// ReviewNote - 审批意见（自动过单记录命中的护栏规则）
	ReviewNote string `json:"reviewNote"`
	// ReviewerId - 审批人用户ID（自动过单为 0=系统）
	ReviewerId int `json:"reviewerId"`
	// ReviewedAt - 审批时间（毫秒）
	ReviewedAt int64 `json:"reviewedAt"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// SaasTenantApplicationFindParams - 租户申请单分页查询参数（平台 types.SaasTenantApplicationFind；
// 我的申请与审批队列共用，userId 仅审批视角生效）
type SaasTenantApplicationFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// UserId - 申请人用户ID（审批视角筛选）
	UserId int `json:"userId,omitempty"`
	// BizType - 业务类型（subscribe/change）
	BizType string `json:"bizType,omitempty"`
	// Status - 申请状态（pending/approved/rejected/cancelled）
	Status string `json:"status,omitempty"`
}

// SaasReviewInput - 租户申请审批参数（平台 types.SaasReviewAction，需 saas.review 权限）
type SaasReviewInput struct {
	// Id - 申请单ID（必填）
	Id int `json:"id"`
	// Action - 审批动作（approve/reject，必填；approve 单事务生效并签发）
	Action string `json:"action"`
	// ReviewNote - 审批意见（reject 时必填）
	ReviewNote string `json:"reviewNote,omitempty"`
}

// SaasReviewResult - 租户申请审批结果（review 接口的 data）
type SaasReviewResult struct {
	// Id - 申请单ID（approve 时为目标租户ID）
	Id int `json:"id"`
	// TenantNo - 生效租户编号（仅 approve 返回）
	TenantNo string `json:"tenantNo"`
	// Action - 审批动作
	Action string `json:"action"`
}

// ============================= SaaS 租户用量与留痕 =============================

// SaasTenantUsageRow - 租户用量历史行（usage/find 接口 data 内行，含租户编号/编码冗余）
type SaasTenantUsageRow struct {
	// Id - 用量记录ID
	Id int `json:"id"`
	// TenantId - 租户ID
	TenantId int `json:"tenantId"`
	// ProjectId - 所属项目ID
	ProjectId int `json:"projectId"`
	// TenantNo - 租户编号
	TenantNo string `json:"tenantNo"`
	// TenantCode - 租户编码
	TenantCode string `json:"tenantCode"`
	// LimitKey - 额度项（与载荷 limits key 对齐）
	LimitKey string `json:"limitKey"`
	// LimitValue - 上报用量值
	LimitValue int64 `json:"limitValue"`
	// ReportedAt - 上报时间（毫秒）
	ReportedAt int64 `json:"reportedAt"`
	// HourBucket - 小时水位（reportedAt 截断到整点）
	HourBucket int64 `json:"hourBucket"`
}

// SaasTenantUsageFindParams - 租户用量历史分页查询参数（平台 types.SaasTenantUsageFind）
type SaasTenantUsageFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// TenantId - 租户ID
	TenantId int `json:"tenantId,omitempty"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId,omitempty"`
	// LimitKey - 额度项
	LimitKey string `json:"limitKey,omitempty"`
	// StartTime - 上报时间起（毫秒）
	StartTime int64 `json:"startTime,omitempty"`
	// EndTime - 上报时间止（毫秒）
	EndTime int64 `json:"endTime,omitempty"`
}

// SaasTenantUsageItem - 用量水位项（额度项取载荷 limits 与已上报 key 的并集，nil 表示无该维度）
type SaasTenantUsageItem struct {
	// LimitKey - 额度项
	LimitKey string `json:"limitKey"`
	// Limit - 载荷 limits 上限（nil=载荷未定义该项）
	Limit *int64 `json:"limit"`
	// Value - 最新上报值（nil=未上报过）
	Value *int64 `json:"value"`
	// ReportedAt - 最新上报时间（毫秒，nil=未上报过）
	ReportedAt *int64 `json:"reportedAt"`
}

// SaasTenantUsageSummary - 租户用量水位（usage/summary 接口的 data）
type SaasTenantUsageSummary struct {
	// TenantId - 租户ID
	TenantId int `json:"tenantId"`
	// TenantNo - 租户编号
	TenantNo string `json:"tenantNo"`
	// Items - 用量水位项列表
	Items []SaasTenantUsageItem `json:"items"`
}

// SaasTenantBatchRenewInput - 租户批量续期参数（平台 types.SaasTenantBatchRenew）
// 仅 active/suspended 可续；member 逐租户生成 change 申请单走审批；platform 直通生效重签。
type SaasTenantBatchRenewInput struct {
	// Ids - 租户ID列表（必填，须全部处于写数据范围内，否则整体拒绝）
	Ids []int `json:"ids"`
	// ValidUntil - 新到期时间（毫秒，必填且须晚于当前时间）
	ValidUntil int64 `json:"validUntil"`
	// Reason - 续期说明
	Reason string `json:"reason,omitempty"`
}

// SaasTenantBatchRenewItem - 批量续期单租户结果
type SaasTenantBatchRenewItem struct {
	// TenantId - 租户ID
	TenantId int `json:"tenantId"`
	// TenantNo - 租户编号
	TenantNo string `json:"tenantNo"`
	// Result - 处理结果（applied/submitted/skipped/failed）
	Result string `json:"result"`
	// Message - 结果说明
	Message string `json:"message"`
	// ApplyNo - 续期申请编号（member 提交审批时返回）
	ApplyNo string `json:"applyNo"`
}

// SaasTenantBatchRenewSummary - 批量续期汇总
type SaasTenantBatchRenewSummary struct {
	// Applied - 直通生效数
	Applied int `json:"applied"`
	// Submitted - 已提交审批数
	Submitted int `json:"submitted"`
	// Skipped - 跳过数
	Skipped int `json:"skipped"`
	// Failed - 失败数
	Failed int `json:"failed"`
}

// SaasTenantBatchRenewResult - 批量续期结果（batch-renew 接口的 data）
type SaasTenantBatchRenewResult struct {
	// Results - 逐租户结果
	Results []SaasTenantBatchRenewItem `json:"results"`
	// Summary - 汇总
	Summary SaasTenantBatchRenewSummary `json:"summary"`
}

// SaasTenantHistoryExport - 租户留痕导出结果（history/export 接口的 data，CSV 文本包裹在 JSON 内）
type SaasTenantHistoryExport struct {
	// FileName - 建议文件名
	FileName string `json:"fileName"`
	// Content - CSV 内容（带 BOM）
	Content string `json:"content"`
}

// ============================= 项目功能模块 =============================

// ProjectModule - 项目功能模块（平台 models/basic.ProjectModule）
type ProjectModule struct {
	// Id - 模块ID
	Id int `json:"id"`
	// ProjectId - 项目ID
	ProjectId int `json:"projectId"`
	// ModuleCode - 模块编码（项目内唯一）
	ModuleCode string `json:"moduleCode"`
	// ModuleName - 模块名称
	ModuleName string `json:"moduleName"`
	// ParentCode - 父模块编码
	ParentCode string `json:"parentCode"`
	// Sort - 排序
	Sort int `json:"sort"`
	// Description - 描述
	Description string `json:"description"`
	// Uid - 操作人用户ID
	Uid int `json:"uid"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ProjectModuleInput - 项目功能模块新增/修改参数（平台 types.ProjectModule；
// Create 时 Id 留空，Update 时 Id 必填；moduleCode 项目内唯一）
type ProjectModuleInput struct {
	// Id - 模块ID（Update 必填）
	Id int `json:"id,omitempty"`
	// ProjectId - 项目ID（必填）
	ProjectId int `json:"projectId,omitempty"`
	// ModuleCode - 模块编码（必填）
	ModuleCode string `json:"moduleCode,omitempty"`
	// ModuleName - 模块名称（必填）
	ModuleName string `json:"moduleName,omitempty"`
	// ParentCode - 父模块编码
	ParentCode string `json:"parentCode,omitempty"`
	// Sort - 排序
	Sort int `json:"sort,omitempty"`
	// Description - 描述
	Description string `json:"description,omitempty"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// ProjectModuleFindParams - 项目功能模块查询参数（平台 types.ProjectModuleFind + 控制器 In/Like/Between 清单）
type ProjectModuleFindParams struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "sort asc, create_at desc"）
	Order string `json:"order,omitempty"`
	// ProjectId - 项目ID（IN）
	ProjectId []int `json:"projectId,omitempty"`
	// ModuleCode - 模块编码（模糊）
	ModuleCode string `json:"moduleCode,omitempty"`
	// ModuleName - 模块名称（模糊）
	ModuleName string `json:"moduleName,omitempty"`
	// ParentCode - 父模块编码（IN）
	ParentCode []string `json:"parentCode,omitempty"`
	// Description - 描述（模糊）
	Description string `json:"description,omitempty"`
	// CreateTime - 创建时间区间（毫秒 [起,止]，0=开口；Between）
	CreateTime []int64 `json:"createTime,omitempty"`
	// UpdateTime - 更新时间区间（毫秒，Between）
	UpdateTime []int64 `json:"updateTime,omitempty"`
	// OnlyTrashed - 仅回收站数据（仅管理员生效）
	OnlyTrashed bool `json:"onlyTrashed,omitempty"`
	// WithTrashed - 包含回收站数据（仅管理员生效）
	WithTrashed bool `json:"withTrashed,omitempty"`
}
