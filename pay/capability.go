package pay

// Capability - Provider 能力标识
type Capability string

const (
	CapTradeCreate    Capability = "trade:create"
	CapTradeQuery     Capability = "trade:query"
	CapTradeCapture   Capability = "trade:capture"
	CapTradeClose     Capability = "trade:close"
	CapRefund         Capability = "refund:create"
	CapRefundQuery    Capability = "refund:query"
	CapTransfer       Capability = "transfer:create"
	CapTransferQuery  Capability = "transfer:query"
	CapNotifyTrade    Capability = "notify:trade"
	CapNotifyRefund   Capability = "notify:refund"
	CapNotifyTransfer Capability = "notify:transfer"
	CapWebhook        Capability = "notify:webhook"
	CapBill           Capability = "bill:fetch"
)

// BillType - 账单类型
type BillType string

const (
	BillTypeTrade    BillType = "trade"     // 交易账单
	BillTypeFundFlow BillType = "fund-flow" // 资金账单（账户维度资金变动）
)

// TradeMode - 支付交易模式
type TradeMode string

const (
	TradeModeQR         TradeMode = "qr"
	TradeModeWAP        TradeMode = "wap"
	TradeModePC         TradeMode = "pc"
	TradeModeBarcode    TradeMode = "barcode"
	TradeModeBusinessQR TradeMode = "business-qr"
	TradeModeApp        TradeMode = "app"
)

// Status - 通用状态常量的底层类型
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusClosed     Status = "closed"
	StatusUnknown    Status = "unknown"
)

// TradeStatus - 交易状态
type TradeStatus string

// RefundStatus - 退款状态
type RefundStatus string

// TransferStatus - 转账状态
type TransferStatus string

const (
	TradeStatusPending    TradeStatus = "pending"
	TradeStatusProcessing TradeStatus = "processing"
	TradeStatusSucceeded  TradeStatus = "succeeded"
	TradeStatusFailed     TradeStatus = "failed"
	TradeStatusClosed     TradeStatus = "closed"
	TradeStatusUnknown    TradeStatus = "unknown"

	RefundStatusPending    RefundStatus = "pending"
	RefundStatusProcessing RefundStatus = "processing"
	RefundStatusSucceeded  RefundStatus = "succeeded"
	RefundStatusFailed     RefundStatus = "failed"
	RefundStatusClosed     RefundStatus = "closed"
	RefundStatusUnknown    RefundStatus = "unknown"

	TransferStatusPending    TransferStatus = "pending"
	TransferStatusProcessing TransferStatus = "processing"
	TransferStatusSucceeded  TransferStatus = "succeeded"
	TransferStatusFailed     TransferStatus = "failed"
	TransferStatusClosed     TransferStatus = "closed"
	TransferStatusUnknown    TransferStatus = "unknown"
)

// PayeeType - 收款人账号类型
type PayeeType string

const (
	PayeeTypeLoginID PayeeType = "login-id"
	PayeeTypeOpenID  PayeeType = "openid"
	PayeeTypeUserID  PayeeType = "user-id"
	PayeeTypeEmail   PayeeType = "email"
)

// NotifyKind - 网关通知种类
type NotifyKind string

const (
	NotifyKindTrade    NotifyKind = "trade"
	NotifyKindRefund   NotifyKind = "refund"
	NotifyKindTransfer NotifyKind = "transfer"
	NotifyKindWebhook  NotifyKind = "webhook"
)

// EventType - 通知事件类型
type EventType string

const (
	EventTradePending       EventType = "trade.pending"
	EventTradeApproved      EventType = "trade.approved"
	EventTradeSucceeded     EventType = "trade.succeeded"
	EventTradeClosed        EventType = "trade.closed"
	EventRefundProcessing   EventType = "refund.processing"
	EventRefundSucceeded    EventType = "refund.succeeded"
	EventRefundFailed       EventType = "refund.failed"
	EventTransferProcessing EventType = "transfer.processing"
	EventTransferSucceeded  EventType = "transfer.succeeded"
	EventTransferFailed     EventType = "transfer.failed"
)

// NotifyDecision - 业务处理后的通知应答决策
type NotifyDecision string

const (
	NotifyAccept NotifyDecision = "accept"
	NotifyRetry  NotifyDecision = "retry"
	NotifyReject NotifyDecision = "reject"
)

// Outcome - 资金操作结果确定性
type Outcome string

const (
	OutcomeKnownFailed    Outcome = "known-failed"
	OutcomeKnownSucceeded Outcome = "known-succeeded"
	OutcomeUnknown        Outcome = "unknown"
)
