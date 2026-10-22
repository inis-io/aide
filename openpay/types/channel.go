package types

// ============================= 通道 - 开始 =============================

// Channel - 商户可用的支付/转账通道
type Channel struct {
	// Id - 通道ID
	Id int32 `json:"id"`
	// Code - 通道编码（下单/转账时的 channelCode）
	Code string `json:"code"`
	// Name - 通道名称
	Name string `json:"name"`
	// Currency - 通道支持币种
	Currency string `json:"currency"`
	// FeeRate - 通道费率（如 0.0020 表示 0.20%）
	FeeRate float64 `json:"feeRate"`
	// Status - 通道状态（normal 可用）
	Status string `json:"status"`
	// Remark - 备注
	Remark string `json:"remark"`
	// Gateways - 通道支持的网关
	Gateways []Gateway `json:"gateways"`
}

// ============================= 通道 - 结束 =============================
