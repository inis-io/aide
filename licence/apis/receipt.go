package apis

// Receipt - 计量回执：每次商城调用的计费凭证（客户侧对账依据，跨 HTTP/gRPC 字段一致）。
// 对应响应信封 data.receipt；字段与 licen-hub API 商城套件 04 文档 §2.3、06 文档 §4 对齐。
type Receipt struct {
	// RequestId - 请求唯一编号（幂等键，对账与排查用）
	RequestId string `json:"requestId"`
	// ChargeMode - 计费模式：free_quota / trial / subscription_quota / metered_balance
	ChargeMode string `json:"chargeMode"`
	// CacheHit - 是否命中缓存（命中折扣由本标记 + Amount 实收体现，不新增 ChargeMode 枚举）
	CacheHit bool `json:"cacheHit"`
	// Quantity - 本次计费数量
	Quantity int64 `json:"quantity"`
	// Amount - 实收金额（分；免费、体验与订阅额度内为 0）
	Amount int64 `json:"amount"`
	// QuotaRemaining - 订阅额度余量（次；无订阅额度语义时为 0）
	QuotaRemaining int64 `json:"quotaRemaining"`
	// BalanceAfter - 扣费后余额账户余额（分；metered_balance 模式返回）
	BalanceAfter int64 `json:"balanceAfter"`
	// ServerTime - 服务端时间（毫秒时间戳，供校时）
	ServerTime int64 `json:"serverTime"`
}
