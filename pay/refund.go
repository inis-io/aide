package pay

// RefundRequest - 发起退款请求
type RefundRequest struct {
	_ noUnkeyedLiterals
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// OutRefundNo - 商户退款号
	OutRefundNo string `json:"outRefundNo"`
	// IdempotencyKey - 网关原生幂等键
	IdempotencyKey string `json:"idempotencyKey"`
	// TotalAmount - 原交易总金额
	TotalAmount Money `json:"totalAmount"`
	// RefundAmount - 本次退款金额
	RefundAmount Money `json:"refundAmount"`
	// Reason - 退款原因
	Reason string `json:"reason"`
	// NotifyURL - 退款异步通知地址
	NotifyURL string `json:"notifyUrl"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewRefundRequest - 创建基础退款请求
func NewRefundRequest(outTradeNo, outRefundNo string, totalAmount, refundAmount Money) RefundRequest {
	return RefundRequest{OutTradeNo: outTradeNo, OutRefundNo: outRefundNo, TotalAmount: totalAmount, RefundAmount: refundAmount}
}

// RefundQueryRequest - 查询退款请求
type RefundQueryRequest struct {
	_ noUnkeyedLiterals
	// OutRefundNo - 商户退款号
	OutRefundNo string `json:"outRefundNo"`
	// GatewayRefundNo - 网关退款号
	GatewayRefundNo string `json:"gatewayRefundNo"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewRefundQueryRequest - 创建按商户退款号查询的请求
func NewRefundQueryRequest(outRefundNo string) RefundQueryRequest {
	return RefundQueryRequest{OutRefundNo: outRefundNo}
}

// RefundResult - 标准退款结果
type RefundResult struct {
	// OutRefundNo - 商户退款号
	OutRefundNo string `json:"outRefundNo"`
	// GatewayRefundNo - 网关退款号
	GatewayRefundNo string `json:"gatewayRefundNo"`
	// Status - 标准退款状态
	Status RefundStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// Amount - 退款金额
	Amount Money `json:"amount"`
	// Raw - 按捕获策略保留的原始响应
	Raw *RawPayload `json:"-"`
}

// RefundEvent - 退款通知资源
type RefundEvent struct {
	// OutTradeNo - 关联商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// OutRefundNo - 商户退款号
	OutRefundNo string `json:"outRefundNo"`
	// GatewayRefundNo - 网关退款号
	GatewayRefundNo string `json:"gatewayRefundNo"`
	// Status - 标准退款状态
	Status RefundStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// Amount - 退款金额
	Amount Money `json:"amount"`
}
