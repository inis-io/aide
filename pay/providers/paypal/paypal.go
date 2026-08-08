// Package paypal 提供 PayPal 官方 Provider 适配。
package paypal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-pay/gopay"
	paypalv2 "github.com/go-pay/gopay/paypal"
	"github.com/go-pay/gopay/pkg/xhttp"

	"github.com/inis-io/aide/pay"
)

// Config - PayPal Provider 配置
type Config struct {
	// ClientID - PayPal 应用 Client ID
	ClientID string `json:"clientId"`
	// Secret - 内联 Client Secret
	Secret pay.SensitiveString `json:"secret"`
	// SecretRef - Client Secret 引用
	SecretRef pay.SecretRef `json:"secretRef"`
	// WebhookID - PayPal Webhook ID
	WebhookID string `json:"webhookId"`
}

// TradeOptions - PayPal 交易专属扩展
type TradeOptions struct {
	// ShippingPreference - 收货地址策略
	ShippingPreference string `json:"shippingPreference,omitempty"`
	// BrandName - PayPal 收银台品牌名
	BrandName string `json:"brandName,omitempty"`
}

// Provider - PayPal Provider 实例
type Provider struct {
	client      *paypalv2.Client
	config      Config
	options     pay.OpenOptions
	tokenExpiry time.Time
	mu          sync.Mutex
	closed      bool
}

// Register - 向指定实例 Registry 显式注册 PayPal 工厂
func Register(registry *pay.Registry) error {
	if registry == nil {
		return fmt.Errorf("%w：Registry 为 nil", pay.ErrInvalidConfig)
	}
	return registry.Register("paypal", Factory)
}

// Factory - 构造 PayPal Provider
func Factory(ctx context.Context, input pay.ConfigInput, options pay.OpenOptions) (pay.Provider, error) {
	if options.SchemaVersion != 1 {
		return nil, pay.ErrUnsupportedConfigVersion
	}
	config, err := decodeConfig(input)
	if err != nil {
		return nil, err
	}
	secret, err := resolveSecret(ctx, config.Secret, config.SecretRef, options)
	if err != nil {
		return nil, err
	}
	if config.ClientID == "" || secret == "" || config.WebhookID == "" {
		return nil, fmt.Errorf("%w：PayPal ClientID、Secret 与 WebhookID 均为必填", pay.ErrInvalidConfig)
	}
	httpClient := xhttp.NewClient()
	httpClient.HttpClient = options.Client
	httpClient.SetBodySize(2)
	client, err := paypalv2.NewClient(config.ClientID, secret, !options.Sandbox, paypalv2.WithHttpClient(httpClient), paypalv2.WithoutAutoRefreshToken())
	if err != nil {
		return nil, fmt.Errorf("%w：PayPal 客户端初始化失败", pay.ErrInvalidConfig)
	}
	return &Provider{client: client, config: config, options: options, tokenExpiry: options.Clock.Now().Add(time.Duration(client.ExpiresIn) * time.Second)}, nil
}

// Name - 返回 Provider 名称
func (this *Provider) Name() string { return "paypal" }

// Capabilities - 返回 PayPal 真实能力集合
func (this *Provider) Capabilities() []pay.Capability {
	return []pay.Capability{pay.CapTradeCreate, pay.CapTradeQuery, pay.CapTradeCapture, pay.CapWebhook}
}

// Close - 幂等清理客户端持有的 token 与敏感字符串
func (this *Provider) Close() error {
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.closed {
		return nil
	}
	this.closed = true
	this.client.AccessToken, this.client.Secret = "", ""
	this.config = Config{ClientID: this.config.ClientID, WebhookID: this.config.WebhookID}
	return nil
}

// CreateTrade - 创建 PayPal 订单并返回结构化授权动作
func (this *Provider) CreateTrade(ctx context.Context, request pay.TradeCreateRequest) (pay.TradeResult, error) {
	if request.Mode != pay.TradeModeQR && request.Mode != pay.TradeModeWAP && request.Mode != pay.TradeModePC {
		return pay.TradeResult{}, fmt.Errorf("%w：PayPal 只支持 qr、wap 与 pc 模式", pay.ErrInvalidRequest)
	}
	var extension TradeOptions
	if err := pay.DecodeExtension(request.Extensions, this.Name(), &extension); err != nil {
		return pay.TradeResult{}, err
	}
	purchaseUnits := []map[string]any{{"reference_id": request.OutTradeNo, "description": request.Subject, "custom_id": request.OutTradeNo, "invoice_id": request.OutTradeNo, "amount": map[string]any{"currency_code": request.Amount.Currency.Code, "value": request.Amount.MajorString()}}}
	body := gopay.BodyMap{"intent": "CAPTURE", "purchase_units": purchaseUnits}
	if request.ReturnURL != "" || request.CancelURL != "" || extension.BrandName != "" || extension.ShippingPreference != "" {
		experience := gopay.BodyMap{"payment_method_preference": "IMMEDIATE_PAYMENT_REQUIRED", "user_action": "PAY_NOW"}
		setOptional(experience, "return_url", request.ReturnURL)
		setOptional(experience, "cancel_url", request.CancelURL)
		setOptional(experience, "brand_name", extension.BrandName)
		setOptional(experience, "shipping_preference", extension.ShippingPreference)
		body.Set("payment_source", gopay.BodyMap{"paypal": gopay.BodyMap{"experience_context": experience}})
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if err := this.ensureTokenLocked(); err != nil {
		return pay.TradeResult{}, err
	}
	if request.IdempotencyKey != "" {
		this.client.SetRequestHeader("PayPal-Request-Id", request.IdempotencyKey)
		defer this.client.ClearRequestHeader()
	}
	response, err := this.client.CreateOrder(ctx, body)
	if err != nil {
		return pay.TradeResult{}, gatewayError("trade:create", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrorResponse); err != nil {
		return pay.TradeResult{}, err
	}
	if response.Response == nil {
		return pay.TradeResult{}, invalidResponse("trade:create")
	}
	approval := approvalURL(response.Response.Links)
	if approval == "" {
		return pay.TradeResult{}, invalidResponse("trade:create")
	}
	action := &pay.PaymentAction{Kind: pay.ActionRedirect, Redirect: &pay.RedirectAction{URL: approval}}
	if request.Mode == pay.TradeModeQR {
		action = &pay.PaymentAction{Kind: pay.ActionQRCode, QRCode: &pay.QRCodeAction{Content: approval}}
	}
	return pay.TradeResult{OutTradeNo: request.OutTradeNo, GatewayTradeNo: response.Response.Id, Status: tradeStatus(response.Response.Status), GatewayStatus: response.Response.Status, ChargedAmount: request.Amount, Action: action, Raw: capture(this.options, response)}, nil
}

// QueryTrade - 查询 PayPal 订单
func (this *Provider) QueryTrade(ctx context.Context, request pay.TradeQueryRequest) (pay.TradeResult, error) {
	if request.GatewayTradeNo == "" {
		return pay.TradeResult{}, fmt.Errorf("%w：PayPal 查询必须提供网关交易号", pay.ErrInvalidRequest)
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if err := this.ensureTokenLocked(); err != nil {
		return pay.TradeResult{}, err
	}
	response, err := this.client.OrderDetail(ctx, request.GatewayTradeNo, nil)
	if err != nil {
		return pay.TradeResult{}, gatewayError("trade:query", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrorResponse); err != nil {
		return pay.TradeResult{}, err
	}
	if response.Response == nil {
		return pay.TradeResult{}, invalidResponse("trade:query")
	}
	outNo, amount := orderAmount(response.Response)
	return pay.TradeResult{OutTradeNo: outNo, GatewayTradeNo: response.Response.Id, Status: tradeStatus(response.Response.Status), GatewayStatus: response.Response.Status, ChargedAmount: amount, Raw: capture(this.options, response)}, nil
}

// CaptureTrade - 显式捕获已获买家批准的 PayPal 订单
func (this *Provider) CaptureTrade(ctx context.Context, request pay.TradeCaptureRequest) (pay.TradeResult, error) {
	this.mu.Lock()
	defer this.mu.Unlock()
	if err := this.ensureTokenLocked(); err != nil {
		return pay.TradeResult{}, err
	}
	this.client.SetRequestHeader("PayPal-Request-Id", request.IdempotencyKey)
	defer this.client.ClearRequestHeader()
	response, err := this.client.OrderCapture(ctx, request.GatewayTradeNo, nil)
	if err != nil {
		return pay.TradeResult{}, gatewayError("trade:capture", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrorResponse); err != nil {
		return pay.TradeResult{}, err
	}
	if response.Response == nil {
		return pay.TradeResult{}, invalidResponse("trade:capture")
	}
	outNo, amount := orderAmount(response.Response)
	if outNo == "" {
		outNo = request.OutTradeNo
	}
	if amount.Minor == 0 {
		amount = request.Amount
	}
	return pay.TradeResult{OutTradeNo: outNo, GatewayTradeNo: response.Response.Id, Status: tradeStatus(response.Response.Status), GatewayStatus: response.Response.Status, ChargedAmount: amount, Raw: capture(this.options, response)}, nil
}

// ParseNotify - 通过 PayPal 验签 API 验证并解析 Webhook，不执行 Capture
func (this *Provider) ParseNotify(ctx context.Context, request pay.NotifyRequest) (pay.NotifyEvent, error) {
	if request.Kind != pay.NotifyKindWebhook {
		return pay.NotifyEvent{}, pay.ErrUnsupportedCapability
	}
	if !strings.EqualFold(request.Method, http.MethodPost) {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	var webhook paypalv2.WebhookEvent
	if err := json.Unmarshal(request.Body, &webhook); err != nil || webhook.Id == "" || webhook.EventType == "" {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	transmissionTime := request.Headers.Get("Paypal-Transmission-Time")
	parsedTime, err := time.Parse(time.RFC3339, transmissionTime)
	if err != nil || absDuration(this.options.Clock.Now().Sub(parsedTime)) > this.options.NotifyClockSkew {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	certURL := request.Headers.Get("Paypal-Cert-Url")
	if !validCertURL(certURL) {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	body := gopay.BodyMap{"auth_algo": request.Headers.Get("Paypal-Auth-Algo"), "cert_url": certURL, "transmission_id": request.Headers.Get("Paypal-Transmission-Id"), "transmission_sig": request.Headers.Get("Paypal-Transmission-Sig"), "transmission_time": transmissionTime, "webhook_id": this.config.WebhookID, "webhook_event": json.RawMessage(request.Body)}
	this.mu.Lock()
	defer this.mu.Unlock()
	if err = this.ensureTokenLocked(); err != nil {
		return pay.NotifyEvent{}, err
	}
	verification, err := this.client.VerifyWebhookSignature(ctx, body)
	if err != nil || verification == nil || verification.VerificationStatus != "SUCCESS" {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	event, err := this.webhookEvent(webhook)
	if err != nil {
		return pay.NotifyEvent{}, err
	}
	event.VerifiedAt, event.VerificationKeyID = this.options.Clock.Now(), keyID(certURL)
	event.Raw = pay.CaptureRaw(this.options.RawCapture, request.Headers.Get("Content-Type"), request.Body)
	return event, nil
}

// NotifyResponse - 编码 PayPal Webhook ACK
func (this *Provider) NotifyResponse(kind pay.NotifyKind, decision pay.NotifyDecision) pay.NotifyResponse {
	status, message := http.StatusOK, "ok"
	if decision == pay.NotifyRetry {
		status, message = http.StatusInternalServerError, "retry"
	}
	if decision == pay.NotifyReject {
		status, message = http.StatusBadRequest, "reject"
	}
	body, _ := json.Marshal(map[string]string{"message": message})
	return pay.NotifyResponse{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: body}
}

func (this *Provider) webhookEvent(webhook paypalv2.WebhookEvent) (pay.NotifyEvent, error) {
	occurred := this.options.Clock.Now()
	if parsed, err := time.Parse(time.RFC3339, webhook.CreateTime); err == nil {
		occurred = parsed
	}
	base := pay.NotifyEvent{ID: webhook.Id, Provider: this.Name(), OccurredAt: occurred}
	if webhook.EventType == "CHECKOUT.ORDER.APPROVED" || webhook.EventType == "CHECKOUT.ORDER.VOIDED" {
		var order paypalv2.OrderDetail
		if err := json.Unmarshal(webhook.Resource, &order); err != nil {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
		outNo, amount := orderAmount(&order)
		status := tradeStatus(order.Status)
		eventType := pay.EventTradeApproved
		if webhook.EventType == "CHECKOUT.ORDER.VOIDED" {
			status, eventType = pay.TradeStatusClosed, pay.EventTradeClosed
		}
		base.Type, base.Trade = eventType, &pay.TradeEvent{OutTradeNo: outNo, GatewayTradeNo: order.Id, Status: status, GatewayStatus: order.Status, Amount: amount}
		return base, nil
	}
	var resource struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		CustomID  string `json:"custom_id"`
		InvoiceID string `json:"invoice_id"`
		Amount    struct {
			CurrencyCode string `json:"currency_code"`
			Value        string `json:"value"`
		} `json:"amount"`
		SupplementaryData struct {
			RelatedIDs struct {
				OrderID string `json:"order_id"`
			} `json:"related_ids"`
		} `json:"supplementary_data"`
	}
	if err := json.Unmarshal(webhook.Resource, &resource); err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	orderID := resource.SupplementaryData.RelatedIDs.OrderID
	if orderID == "" {
		orderID = resource.ID
	}
	amount, err := pay.ParseMoney(resource.Amount.Value, resource.Amount.CurrencyCode)
	if err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	switch webhook.EventType {
	case "PAYMENT.CAPTURE.COMPLETED":
		base.Type = pay.EventTradeSucceeded
		base.Trade = &pay.TradeEvent{OutTradeNo: firstNonEmpty(resource.CustomID, resource.InvoiceID), GatewayTradeNo: orderID, Status: pay.TradeStatusSucceeded, GatewayStatus: resource.Status, Amount: amount}
	case "PAYMENT.CAPTURE.DENIED", "PAYMENT.CAPTURE.REVERSED":
		base.Type = pay.EventTradeClosed
		base.Trade = &pay.TradeEvent{OutTradeNo: firstNonEmpty(resource.CustomID, resource.InvoiceID), GatewayTradeNo: orderID, Status: pay.TradeStatusFailed, GatewayStatus: resource.Status, Amount: amount}
	default:
		return pay.NotifyEvent{}, fmt.Errorf("%w：未支持的 PayPal 事件 %s", pay.ErrInvalidRequest, webhook.EventType)
	}
	return base, nil
}

func (this *Provider) ensureTokenLocked() error {
	if this.closed {
		return pay.ErrPoolClosed
	}
	if this.options.Clock.Now().Add(time.Minute).Before(this.tokenExpiry) {
		return nil
	}
	token, err := this.client.GetAccessToken()
	if err != nil {
		return gatewayError("token:refresh", err, pay.OutcomeUnknown)
	}
	this.tokenExpiry = this.options.Clock.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	return nil
}

func decodeConfig(input pay.ConfigInput) (Config, error) {
	if (input.Value == nil) == (len(input.Raw) == 0) {
		return Config{}, fmt.Errorf("%w：Value 与 Raw 必须二选一", pay.ErrInvalidConfig)
	}
	if input.Value != nil {
		config, ok := input.Value.(Config)
		if !ok {
			pointer, pointerOK := input.Value.(*Config)
			if !pointerOK || pointer == nil {
				return Config{}, fmt.Errorf("%w：PayPal 配置类型错误", pay.ErrInvalidConfig)
			}
			config = *pointer
		}
		return config, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(input.Raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w：PayPal 配置 JSON 非法", pay.ErrInvalidConfig)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Config{}, fmt.Errorf("%w：PayPal 配置包含多个 JSON 值", pay.ErrInvalidConfig)
	}
	return config, nil
}

func resolveSecret(ctx context.Context, inline pay.SensitiveString, ref pay.SecretRef, options pay.OpenOptions) (string, error) {
	if inline.Reveal() != "" && ref != "" {
		return "", fmt.Errorf("%w：敏感值与引用不能同时提供", pay.ErrInvalidConfig)
	}
	if ref == "" {
		return inline.Reveal(), nil
	}
	if options.SecretResolver == nil {
		return "", fmt.Errorf("%w：缺少 SecretResolver", pay.ErrInvalidConfig)
	}
	value, err := options.SecretResolver.ResolveSecret(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("%w：敏感值解析失败", pay.ErrInvalidConfig)
	}
	defer clear(value)
	return string(value), nil
}
func checkResponse(code int, response *paypalv2.ErrorResponse) error {
	if code == paypalv2.Success && response == nil {
		return nil
	}
	gateway := &pay.GatewayError{Provider: "paypal", Operation: "gateway", Outcome: pay.OutcomeKnownFailed, Cause: pay.ErrGatewayRejected}
	if response != nil {
		gateway.Code, gateway.Message = response.Name, response.Message
	}
	gateway.Retryable = code == 429 || code >= 500
	if gateway.Retryable {
		gateway.Cause = pay.ErrGatewayUnavailable
	}
	return gateway
}
func invalidResponse(operation string) error {
	return &pay.GatewayError{Provider: "paypal", Operation: operation, Message: "网关响应为空", Retryable: true, Outcome: pay.OutcomeUnknown, Cause: pay.ErrGatewayUnavailable}
}
func gatewayError(operation string, err error, outcome pay.Outcome) error {
	retryable := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
	cause := pay.ErrGatewayRejected
	if retryable {
		cause = pay.ErrGatewayUnavailable
	}
	return &pay.GatewayError{Provider: "paypal", Operation: operation, Message: "网关调用失败", Retryable: retryable, Outcome: outcome, Cause: cause}
}
func approvalURL(links []*paypalv2.Link) string {
	for _, link := range links {
		if link != nil && (link.Rel == "payer-action" || link.Rel == "approve") {
			return link.Href
		}
	}
	return ""
}
func orderAmount(order *paypalv2.OrderDetail) (string, pay.Money) {
	if order == nil || len(order.PurchaseUnits) == 0 || order.PurchaseUnits[0] == nil {
		return "", pay.Money{}
	}
	unit := order.PurchaseUnits[0]
	amount := pay.Money{}
	if unit.Amount != nil {
		amount, _ = pay.ParseMoney(unit.Amount.Value, unit.Amount.CurrencyCode)
	}
	return firstNonEmpty(unit.CustomId, unit.InvoiceId, unit.ReferenceId), amount
}
func tradeStatus(value string) pay.TradeStatus {
	switch value {
	case "COMPLETED":
		return pay.TradeStatusSucceeded
	case "APPROVED":
		return pay.TradeStatusProcessing
	case "CREATED", "SAVED", "PAYER_ACTION_REQUIRED":
		return pay.TradeStatusPending
	case "VOIDED":
		return pay.TradeStatusClosed
	default:
		return pay.TradeStatusUnknown
	}
}
func validCertURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "paypal.com" || strings.HasSuffix(host, ".paypal.com")
}
func setOptional(body gopay.BodyMap, key, value string) {
	if value != "" {
		body.Set(key, value)
	}
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func capture(options pay.OpenOptions, value any) *pay.RawPayload {
	body, _ := json.Marshal(value)
	return pay.CaptureRaw(options.RawCapture, "application/json", body)
}
func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func keyID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}
