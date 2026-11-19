package types

// ============================= 凭据 - 开始 =============================

// AccessKeyInfo - 密钥自检信息（Secret 永不下发）
type AccessKeyInfo struct {
	// Name - 备注名
	Name string `json:"name"`
	// Status - 状态（active/disabled）
	Status string `json:"status"`
	// Scope - 授权范围（"*"/{"buckets":[],"mode":"ro|rw"} JSON）
	Scope string `json:"scope"`
	// LastUsedAt - 最近使用时间（毫秒）
	LastUsedAt int64 `json:"lastUsedAt"`
	// ExpireAt - 到期时间（毫秒，0 = 永久）
	ExpireAt int64 `json:"expireAt"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ============================= 凭据 - 结束 =============================
