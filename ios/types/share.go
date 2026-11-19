package types

// ============================= 分享 - 开始 =============================

// Share - 分享链接（令牌制，支持永久与吊销）
type Share struct {
	// Id - 分享 ID
	Id int64 `json:"id"`
	// Bucket - 桶名
	Bucket string `json:"bucket"`
	// Key - 对象键
	Key string `json:"key"`
	// Token - 访问令牌（完整链接 = 平台地址 + /share/{token}）
	Token string `json:"token"`
	// ExpireAt - 过期时间（毫秒，0 = 永久）
	ExpireAt int64 `json:"expireAt"`
	// Visits - 累计访问次数
	Visits int64 `json:"visits"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ShareCreateRequest - 签发分享链接请求
type ShareCreateRequest struct {
	// Bucket - 桶名（必填）
	Bucket string `json:"bucket"`
	// Key - 对象键（必填，文件夹不支持分享）
	Key string `json:"key"`
	// Expires - 有效期（秒，默认 0 = 永久）
	Expires int64 `json:"expires"`
}

// Normalize - 注入服务端默认值（签名前调用，两条链路共用）
// 与服务端「先按 default 标签注入默认值再验签」的顺序一致：expires 默认 0（永久）。
func (this *ShareCreateRequest) Normalize() {

	if this.Expires < 0 {
		this.Expires = 0
	}
}

// ShareCreateResult - 签发分享链接结果
type ShareCreateResult struct {
	// Share - 分享行
	Share Share `json:"share"`
	// Reused - 是否命中 60s 幂等去重窗口（复用既有活跃分享）
	Reused bool `json:"reused"`
}

// ShareListRequest - 对象分享列表请求
type ShareListRequest struct {
	// Bucket - 桶名（必填）
	Bucket string `json:"bucket"`
	// Key - 对象键（必填）
	Key string `json:"key"`
}

// ShareRevokeRequest - 吊销分享请求（软删即失效）
type ShareRevokeRequest struct {
	// Id - 分享 ID（必填）
	Id int64 `json:"id"`
}

// ============================= 分享 - 结束 =============================
