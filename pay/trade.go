package pay

// TradeCreateRequest - 创建交易请求
type TradeCreateRequest struct {
	_ noUnkeyedLiterals
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// IdempotencyKey - 网关原生幂等键
	IdempotencyKey string `json:"idempotencyKey"`
	// Mode - 支付交互模式
	Mode TradeMode `json:"mode"`
	// Subject - 交易主题
	Subject string `json:"subject"`
	// Amount - 扣款金额
	Amount Money `json:"amount"`
	// NotifyURL - 异步通知地址
	NotifyURL string `json:"notifyUrl"`
	// ReturnURL - 支付完成返回地址
	ReturnURL string `json:"returnUrl"`
	// CancelURL - 支付取消返回地址
	CancelURL string `json:"cancelUrl"`
	// ClientIP - 付款客户端 IP
	ClientIP string `json:"clientIp"`
	// AuthCode - 条码支付授权码
	AuthCode string `json:"authCode"`
	// BuyerID - 网关买家标识
	BuyerID string `json:"buyerId"`
	// Metadata - 仅供调用方本地关联的元数据
	Metadata map[string]string `json:"metadata"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTradeCreateRequest - 创建基础交易请求
func NewTradeCreateRequest(outTradeNo string, mode TradeMode, subject string, amount Money) TradeCreateRequest {
	return TradeCreateRequest{OutTradeNo: outTradeNo, Mode: mode, Subject: subject, Amount: amount}
}

// TradeQueryRequest - 查询交易请求
type TradeQueryRequest struct {
	_ noUnkeyedLiterals
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTradeQueryRequest - 创建按商户交易号查询的请求
func NewTradeQueryRequest(outTradeNo string) TradeQueryRequest {
	return TradeQueryRequest{OutTradeNo: outTradeNo}
}

// TradeCaptureRequest - 捕获交易请求
type TradeCaptureRequest struct {
	_ noUnkeyedLiterals
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// IdempotencyKey - Capture 幂等键
	IdempotencyKey string `json:"idempotencyKey"`
	// Amount - 捕获金额
	Amount Money `json:"amount"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTradeCaptureRequest - 创建交易捕获请求
func NewTradeCaptureRequest(outTradeNo, gatewayTradeNo, idempotencyKey string, amount Money) TradeCaptureRequest {
	return TradeCaptureRequest{OutTradeNo: outTradeNo, GatewayTradeNo: gatewayTradeNo, IdempotencyKey: idempotencyKey, Amount: amount}
}

// TradeCloseRequest - 关闭交易请求
type TradeCloseRequest struct {
	_ noUnkeyedLiterals
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTradeCloseRequest - 创建按商户交易号关单的请求
func NewTradeCloseRequest(outTradeNo string) TradeCloseRequest {
	return TradeCloseRequest{OutTradeNo: outTradeNo}
}

// TradeResult - 标准交易结果
type TradeResult struct {
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// GatewayCaptureNo - 网关捕获号（仅 PayPal 等先授权后捕获的通道有值；退款以此号为目标）
	GatewayCaptureNo string `json:"gatewayCaptureNo"`
	// Status - 标准交易状态
	Status TradeStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// ChargedAmount - 实际扣款金额
	ChargedAmount Money `json:"chargedAmount"`
	// Action - 后续支付动作
	Action *PaymentAction `json:"action,omitempty"`
	// Raw - 按捕获策略保留的原始响应
	Raw *RawPayload `json:"-"`
}

// TradeEvent - 交易通知资源
type TradeEvent struct {
	// OutTradeNo - 商户交易号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 网关交易号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// GatewayCaptureNo - 网关捕获号（仅 PayPal 等先授权后捕获的通道有值；退款以此号为目标）
	GatewayCaptureNo string `json:"gatewayCaptureNo"`
	// Status - 标准交易状态
	Status TradeStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// Amount - 通知交易金额
	Amount Money `json:"amount"`
}
