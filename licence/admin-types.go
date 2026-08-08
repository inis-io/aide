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
