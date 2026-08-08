package pay

// Payee - 标准收款人
type Payee struct {
	// Account - 收款账号
	Account string `json:"account"`
	// Name - 收款人姓名
	Name string `json:"name"`
	// Type - 收款账号类型
	Type PayeeType `json:"type"`
}

// TransferRequest - 发起转账请求
type TransferRequest struct {
	_ noUnkeyedLiterals
	// OutTransferNo - 商户转账号
	OutTransferNo string `json:"outTransferNo"`
	// IdempotencyKey - 网关原生幂等键
	IdempotencyKey string `json:"idempotencyKey"`
	// Amount - 转账金额
	Amount Money `json:"amount"`
	// Payee - 收款人
	Payee Payee `json:"payee"`
	// Subject - 转账主题
	Subject string `json:"subject"`
	// NotifyURL - 转账异步通知地址
	NotifyURL string `json:"notifyUrl"`
	// Scene - Provider 转账场景
	Scene string `json:"scene"`
	// SceneReport - 转账场景报备信息
	SceneReport map[string]string `json:"sceneReport"`
	// Metadata - 仅供调用方本地关联的元数据
	Metadata map[string]string `json:"metadata"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTransferRequest - 创建基础转账请求
func NewTransferRequest(outTransferNo string, amount Money, payee Payee) TransferRequest {
	return TransferRequest{OutTransferNo: outTransferNo, Amount: amount, Payee: payee}
}

// TransferQueryRequest - 查询转账请求
type TransferQueryRequest struct {
	_ noUnkeyedLiterals
	// OutTransferNo - 商户转账号
	OutTransferNo string `json:"outTransferNo"`
	// GatewayTransferNo - 网关转账号
	GatewayTransferNo string `json:"gatewayTransferNo"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// NewTransferQueryRequest - 创建按商户转账号查询的请求
func NewTransferQueryRequest(outTransferNo string) TransferQueryRequest {
	return TransferQueryRequest{OutTransferNo: outTransferNo}
}

// TransferResult - 标准转账结果
type TransferResult struct {
	// OutTransferNo - 商户转账号
	OutTransferNo string `json:"outTransferNo"`
	// GatewayTransferNo - 网关转账号
	GatewayTransferNo string `json:"gatewayTransferNo"`
	// Status - 标准转账状态
	Status TransferStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// Amount - 转账金额
	Amount Money `json:"amount"`
	// Raw - 按捕获策略保留的原始响应
	Raw *RawPayload `json:"-"`
}

// TransferEvent - 转账通知资源
type TransferEvent struct {
	// OutTransferNo - 商户转账号
	OutTransferNo string `json:"outTransferNo"`
	// GatewayTransferNo - 网关转账号
	GatewayTransferNo string `json:"gatewayTransferNo"`
	// Status - 标准转账状态
	Status TransferStatus `json:"status"`
	// GatewayStatus - 网关原始状态
	GatewayStatus string `json:"gatewayStatus"`
	// Amount - 转账金额
	Amount Money `json:"amount"`
}
