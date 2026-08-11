package pay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Driver - Provider 的统一能力检查与调用门面
type Driver struct {
	provider     Provider
	capabilities map[Capability]struct{}
	options      OpenOptions
	closeOnce    sync.Once
	closeErr     error
}

// Name - 返回 Provider 名称
func (this *Driver) Name() string {
	if this == nil || this.provider == nil {
		return ""
	}
	return this.provider.Name()
}

// Supports - 判断 Provider 是否声明指定能力
func (this *Driver) Supports(capability Capability) bool {
	if this == nil {
		return false
	}
	_, ok := this.capabilities[capability]
	return ok
}

// Provider - 暴露底层 Provider 供高级扩展类型断言
func (this *Driver) Provider() Provider {
	if this == nil {
		return nil
	}
	return this.provider
}

// CreateTrade - 创建交易
func (this *Driver) CreateTrade(ctx context.Context, request TradeCreateRequest) (TradeResult, error) {
	provider, err := capabilityOf[TradeCreator](this, CapTradeCreate)
	if err != nil {
		return TradeResult{}, err
	}
	if strings.TrimSpace(request.OutTradeNo) == "" || strings.TrimSpace(request.Subject) == "" || !request.Amount.IsPositive() || request.Mode == "" {
		return TradeResult{}, fmt.Errorf("%w：交易号、主题、模式和正金额必填", ErrInvalidRequest)
	}
	if err := validateOwnExtensions(request.Extensions, this.Name()); err != nil {
		return TradeResult{}, err
	}
	var result TradeResult
	err = this.observe(ctx, "trade:create", request.OutTradeNo, func() error { var callErr error; result, callErr = provider.CreateTrade(ctx, request); return callErr })
	if err == nil && result.Action != nil {
		err = result.Action.Validate()
	}
	return result, err
}

// QueryTrade - 查询交易
func (this *Driver) QueryTrade(ctx context.Context, request TradeQueryRequest) (TradeResult, error) {
	provider, err := capabilityOf[TradeQuerier](this, CapTradeQuery)
	if err != nil {
		return TradeResult{}, err
	}
	if request.OutTradeNo == "" && request.GatewayTradeNo == "" {
		return TradeResult{}, fmt.Errorf("%w：至少提供一个交易号", ErrInvalidRequest)
	}
	var result TradeResult
	err = this.observe(ctx, "trade:query", request.OutTradeNo, func() error { var e error; result, e = provider.QueryTrade(ctx, request); return e })
	return result, err
}

// CaptureTrade - 捕获交易
func (this *Driver) CaptureTrade(ctx context.Context, request TradeCaptureRequest) (TradeResult, error) {
	provider, err := capabilityOf[TradeCapturer](this, CapTradeCapture)
	if err != nil {
		return TradeResult{}, err
	}
	if request.OutTradeNo == "" || request.GatewayTradeNo == "" || request.IdempotencyKey == "" || !request.Amount.IsPositive() {
		return TradeResult{}, fmt.Errorf("%w：Capture 参数不完整", ErrInvalidRequest)
	}
	var result TradeResult
	err = this.observe(ctx, "trade:capture", request.OutTradeNo, func() error { var e error; result, e = provider.CaptureTrade(ctx, request); return e })
	return result, err
}

// CloseTrade - 关闭交易
func (this *Driver) CloseTrade(ctx context.Context, request TradeCloseRequest) error {
	provider, err := capabilityOf[TradeCloser](this, CapTradeClose)
	if err != nil {
		return err
	}
	if request.OutTradeNo == "" && request.GatewayTradeNo == "" {
		return fmt.Errorf("%w：至少提供一个交易号", ErrInvalidRequest)
	}
	return this.observe(ctx, "trade:close", request.OutTradeNo, func() error { return provider.CloseTrade(ctx, request) })
}

// Refund - 发起退款
func (this *Driver) Refund(ctx context.Context, request RefundRequest) (RefundResult, error) {
	provider, err := capabilityOf[Refunder](this, CapRefund)
	if err != nil {
		return RefundResult{}, err
	}
	if request.OutTradeNo == "" || request.OutRefundNo == "" || !request.TotalAmount.IsPositive() || !request.RefundAmount.IsPositive() || !request.TotalAmount.SameCurrency(request.RefundAmount) || request.RefundAmount.Minor > request.TotalAmount.Minor {
		return RefundResult{}, fmt.Errorf("%w：退款参数非法", ErrInvalidRequest)
	}
	var result RefundResult
	err = this.observe(ctx, "refund:create", request.OutRefundNo, func() error { var e error; result, e = provider.Refund(ctx, request); return e })
	return result, err
}

// QueryRefund - 查询退款
func (this *Driver) QueryRefund(ctx context.Context, request RefundQueryRequest) (RefundResult, error) {
	provider, err := capabilityOf[RefundQuerier](this, CapRefundQuery)
	if err != nil {
		return RefundResult{}, err
	}
	if request.OutRefundNo == "" && request.GatewayRefundNo == "" {
		return RefundResult{}, fmt.Errorf("%w：至少提供一个退款号", ErrInvalidRequest)
	}
	var result RefundResult
	err = this.observe(ctx, "refund:query", request.OutRefundNo, func() error { var e error; result, e = provider.QueryRefund(ctx, request); return e })
	return result, err
}

// Transfer - 发起转账
func (this *Driver) Transfer(ctx context.Context, request TransferRequest) (TransferResult, error) {
	provider, err := capabilityOf[Transferer](this, CapTransfer)
	if err != nil {
		return TransferResult{}, err
	}
	if request.OutTransferNo == "" || request.IdempotencyKey == "" || !request.Amount.IsPositive() || request.Payee.Account == "" || request.Payee.Type == "" {
		return TransferResult{}, fmt.Errorf("%w：转账参数非法", ErrInvalidRequest)
	}
	var result TransferResult
	err = this.observe(ctx, "transfer:create", request.OutTransferNo, func() error { var e error; result, e = provider.Transfer(ctx, request); return e })
	return result, err
}

// QueryTransfer - 查询转账
func (this *Driver) QueryTransfer(ctx context.Context, request TransferQueryRequest) (TransferResult, error) {
	provider, err := capabilityOf[TransferQuerier](this, CapTransferQuery)
	if err != nil {
		return TransferResult{}, err
	}
	if request.OutTransferNo == "" && request.GatewayTransferNo == "" {
		return TransferResult{}, fmt.Errorf("%w：至少提供一个转账号", ErrInvalidRequest)
	}
	var result TransferResult
	err = this.observe(ctx, "transfer:query", request.OutTransferNo, func() error { var e error; result, e = provider.QueryTransfer(ctx, request); return e })
	return result, err
}

// FetchBill - 获取并可选代下载账单
func (this *Driver) FetchBill(ctx context.Context, request BillRequest) (BillResult, error) {
	provider, err := capabilityOf[Biller](this, CapBill)
	if err != nil {
		return BillResult{}, err
	}
	if !validBillDate(request.Date) {
		return BillResult{}, fmt.Errorf("%w：账单日期必须为 yyyy-MM-dd 或 yyyy-MM", ErrInvalidRequest)
	}
	if request.Type == "" {
		request.Type = BillTypeTrade
	}
	if request.Type != BillTypeTrade && request.Type != BillTypeFundFlow {
		return BillResult{}, fmt.Errorf("%w：未知账单类型 %s", ErrInvalidRequest, request.Type)
	}
	if err := validateOwnExtensions(request.Extensions, this.Name()); err != nil {
		return BillResult{}, err
	}
	var result BillResult
	err = this.observe(ctx, "bill:fetch", request.Date, func() error { var e error; result, e = provider.FetchBill(ctx, request); return e })
	return result, err
}

// validBillDate - 校验账单日期：日账单 yyyy-MM-dd 或月账单 yyyy-MM，且必须是合法日期
func validBillDate(value string) bool {
	layout := "2006-01-02"
	if len(value) == len("2006-01") {
		layout = "2006-01"
	} else if len(value) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse(layout, value)
	return err == nil
}

// ParseNotify - 验签并解析通知
func (this *Driver) ParseNotify(ctx context.Context, request NotifyRequest) (NotifyEvent, error) {
	capability := notifyCapability(request.Kind)
	provider, err := capabilityOf[NotifyParser](this, capability)
	if err != nil {
		return NotifyEvent{}, err
	}
	if int64(len(request.Body)) > this.options.NotifyMaxBody {
		return NotifyEvent{}, fmt.Errorf("%w：通知体超过上限", ErrVerifyFailed)
	}
	var event NotifyEvent
	err = this.observe(ctx, "notify:parse", "", func() error { var e error; event, e = provider.ParseNotify(ctx, request); return e })
	if err == nil && !event.Valid() {
		return NotifyEvent{}, fmt.Errorf("%w：Provider 返回无效事件", ErrInvalidProvider)
	}
	if err == nil {
		event.DedupeKey = deriveDedupeKey(event)
	}
	return event, err
}

// deriveDedupeKey - 派生业务幂等去重键：业务单号（缺省退化为网关单号）+ "|" + 标准事件类型
func deriveDedupeKey(event NotifyEvent) string {
	no := ""
	if event.Trade != nil {
		no = firstNonEmpty(event.Trade.OutTradeNo, event.Trade.GatewayTradeNo)
	}
	if event.Refund != nil {
		no = firstNonEmpty(event.Refund.OutRefundNo, event.Refund.GatewayRefundNo)
	}
	if event.Transfer != nil {
		no = firstNonEmpty(event.Transfer.OutTransferNo, event.Transfer.GatewayTransferNo)
	}
	if no == "" {
		return event.ID + "|" + string(event.Type)
	}
	return no + "|" + string(event.Type)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// NotifyResponse - 将业务决策编码为网关 ACK
func (this *Driver) NotifyResponse(kind NotifyKind, decision NotifyDecision) NotifyResponse {
	provider, err := capabilityOf[NotifyParser](this, notifyCapability(kind))
	if err != nil {
		return NotifyResponse{StatusCode: 500, Body: []byte("fail")}
	}
	return provider.NotifyResponse(kind, decision)
}

// Close - 幂等关闭底层 Provider
func (this *Driver) Close() error {
	if this == nil || this.provider == nil {
		return nil
	}
	this.closeOnce.Do(func() { this.closeErr = this.provider.Close() })
	return this.closeErr
}

func capabilityOf[T any](driver *Driver, capability Capability) (T, error) {
	var zero T
	if driver == nil || !driver.Supports(capability) {
		return zero, fmt.Errorf("%w：%s", ErrUnsupportedCapability, capability)
	}
	provider, ok := any(driver.provider).(T)
	if !ok {
		return zero, fmt.Errorf("%w：%s", ErrInvalidProvider, capability)
	}
	return provider, nil
}

func notifyCapability(kind NotifyKind) Capability {
	switch kind {
	case NotifyKindTrade:
		return CapNotifyTrade
	case NotifyKindRefund:
		return CapNotifyRefund
	case NotifyKindTransfer:
		return CapNotifyTransfer
	default:
		return CapWebhook
	}
}

func validateOwnExtensions(extensions Extensions, provider string) error {
	for namespace := range extensions {
		if normalizeProviderName(namespace) != normalizeProviderName(provider) {
			return fmt.Errorf("%w：Provider %s 不能读取 %s 扩展", ErrInvalidRequest, provider, namespace)
		}
	}
	return nil
}

func (this *Driver) observe(ctx context.Context, operation, outNo string, call func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	start := this.options.Clock.Now()
	hash := outNoHash(outNo)
	if this.options.Observer != nil {
		this.options.Observer.Observe(ctx, Observation{Phase: "start", Provider: this.Name(), Operation: operation, OutNoHash: hash})
	}
	err := call()
	duration := this.options.Clock.Now().Sub(start)
	record := Observation{Phase: "end", Provider: this.Name(), Operation: operation, OutNoHash: hash, Duration: duration}
	var gatewayError *GatewayError
	if errors.As(err, &gatewayError) {
		record.Code, record.Message, record.Reason, record.Outcome, record.Retryable = gatewayError.Code, gatewayError.Message, gatewayError.Reason, gatewayError.Outcome, gatewayError.Retryable
	}
	if this.options.Observer != nil {
		this.options.Observer.Observe(ctx, record)
	}
	if this.options.Logger != nil {
		this.options.Logger.Log(ctx, LogRecord{Level: levelForError(err), Provider: record.Provider, Operation: operation, OutNoHash: hash, Code: record.Code, Message: record.Message, Reason: record.Reason, Outcome: record.Outcome, Retryable: record.Retryable, Duration: duration})
	}
	return err
}

func levelForError(err error) string {
	if err != nil {
		return "error"
	}
	return "info"
}
