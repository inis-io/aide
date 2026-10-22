package types

// ============================= Webhook 报文 - 开始 =============================

// NotifyPayload - 支付结果异步通知报文
// 平台以 POST {NotifyUrl} 投递（Content-Type: application/json），报文字段如下，
// sign 与请求签名同一算法（剔除 sign 与空值 → 字典序拼串 → &secret=<商户密钥> → MD5 大写）。
type NotifyPayload struct {
	// TradeNo - 平台订单号（商户侧幂等去重键）
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户订单号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 支付网关订单号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// MchId - 商户号
	MchId string `json:"mchId"`
	// Amount - 金额（元）
	Amount float64 `json:"amount"`
	// Currency - 货币
	Currency string `json:"currency"`
	// Scene - 支付场景
	Scene string `json:"scene"`
	// Way - 支付方式
	Way string `json:"way"`
	// Status - 订单状态（paid 表示支付成功）
	Status string `json:"status"`
	// CreateAt - 订单创建时间（毫秒时间戳）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 订单更新时间（毫秒时间戳）
	UpdateAt int64 `json:"updateAt"`
	// Sign - 报文签名（参与验签后即可丢弃）
	Sign string `json:"sign"`
}

// ============================= Webhook 报文 - 结束 =============================
