// Package alipay 提供支付宝官方 Provider 适配。
package alipay

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
	"time"

	"github.com/go-pay/gopay"
	legacy "github.com/go-pay/gopay/alipay"
	alipayv3 "github.com/go-pay/gopay/alipay/v3"
	"github.com/go-pay/gopay/pkg/xhttp"

	"github.com/inis-io/aide/pay"
)

// Config - 支付宝 Provider 配置
type Config struct {
	// AppID - 支付宝应用 ID
	AppID string `json:"appId"`
	// PrivateKey - 内联应用私钥
	PrivateKey pay.SensitiveString `json:"privateKey"`
	// PrivateKeyRef - 应用私钥引用
	PrivateKeyRef pay.SecretRef `json:"privateKeyRef"`
	// AppPublicCert - 应用公钥证书内容
	AppPublicCert string `json:"appPublicCert"`
	// AlipayRootCert - 支付宝根证书内容
	AlipayRootCert string `json:"alipayRootCert"`
	// AlipayPublicCert - 支付宝公钥证书内容
	AlipayPublicCert string `json:"alipayPublicCert"`
}

// TradeOptions - 支付宝交易专属扩展
type TradeOptions struct {
	// TimeoutExpress - 交易超时时间
	TimeoutExpress string `json:"timeoutExpress,omitempty"`
	// StoreID - 支付宝门店 ID
	StoreID string `json:"storeId,omitempty"`
}

// RefundQueryOptions - 支付宝退款查询所需的原交易标识
type RefundQueryOptions struct {
	// OutTradeNo - 原商户交易号
	OutTradeNo string `json:"outTradeNo,omitempty"`
	// GatewayTradeNo - 原支付宝交易号
	GatewayTradeNo string `json:"gatewayTradeNo,omitempty"`
}

// Provider - 支付宝 Provider 实例
type Provider struct {
	client  sdkClient
	config  Config
	options pay.OpenOptions
}

type sdkClient interface {
	TradePrecreate(context.Context, gopay.BodyMap) (*alipayv3.TradePrecreateRsp, error)
	TradeWapPay(context.Context, gopay.BodyMap) (string, error)
	TradePagePay(context.Context, gopay.BodyMap) (string, error)
	TradePay(context.Context, gopay.BodyMap) (*alipayv3.TradePayRsp, error)
	TradeCreate(context.Context, gopay.BodyMap) (*alipayv3.TradeCreateRsp, error)
	TradeAppPay(context.Context, gopay.BodyMap) (string, error)
	TradeQuery(context.Context, gopay.BodyMap) (*alipayv3.TradeQueryRsp, error)
	TradeClose(context.Context, gopay.BodyMap) (*alipayv3.TradeCloseRsp, error)
	TradeRefund(context.Context, gopay.BodyMap) (*alipayv3.TradeRefundRsp, error)
	TradeFastPayRefundQuery(context.Context, gopay.BodyMap) (*alipayv3.TradeFastPayRefundQueryRsp, error)
	FundTransUniTransfer(context.Context, gopay.BodyMap) (*alipayv3.FundTransUniTransferRsp, error)
	FundTransCommonQuery(context.Context, gopay.BodyMap) (*alipayv3.FundTransCommonQueryRsp, error)
}

// Register - 向指定实例 Registry 显式注册支付宝工厂
func Register(registry *pay.Registry) error {
	if registry == nil {
		return fmt.Errorf("%w：Registry 为 nil", pay.ErrInvalidConfig)
	}
	return registry.Register("alipay", Factory)
}

// Factory - 构造支付宝 Provider
func Factory(ctx context.Context, input pay.ConfigInput, options pay.OpenOptions) (pay.Provider, error) {
	if options.SchemaVersion != 1 {
		return nil, pay.ErrUnsupportedConfigVersion
	}
	config, err := decodeConfig(input)
	if err != nil {
		return nil, err
	}
	privateKey, err := resolveSecret(ctx, config.PrivateKey, config.PrivateKeyRef, options)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.AppID) == "" || privateKey == "" || config.AppPublicCert == "" || config.AlipayRootCert == "" || config.AlipayPublicCert == "" {
		return nil, fmt.Errorf("%w：支付宝 AppID、私钥与三份证书均为必填", pay.ErrInvalidConfig)
	}
	client, err := alipayv3.NewClientV3(config.AppID, privateKey, !options.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("%w：支付宝客户端初始化失败", pay.ErrInvalidConfig)
	}
	if err = client.SetCert([]byte(config.AppPublicCert), []byte(config.AlipayRootCert), []byte(config.AlipayPublicCert)); err != nil {
		return nil, fmt.Errorf("%w：支付宝证书无效", pay.ErrInvalidConfig)
	}
	httpClient := xhttp.NewClient()
	httpClient.HttpClient = options.Client
	httpClient.SetBodySize(2)
	client.SetHttpClient(httpClient)
	return &Provider{client: client, config: config, options: options}, nil
}

// Name - 返回 Provider 名称
func (this *Provider) Name() string { return "alipay" }

// Capabilities - 返回支付宝真实能力集合
func (this *Provider) Capabilities() []pay.Capability {
	return []pay.Capability{pay.CapTradeCreate, pay.CapTradeQuery, pay.CapTradeClose, pay.CapRefund, pay.CapRefundQuery, pay.CapTransfer, pay.CapTransferQuery, pay.CapNotifyTrade}
}

// Close - 关闭 Provider；支付宝 SDK 无常驻资源
func (this *Provider) Close() error { return nil }

// CreateTrade - 创建支付宝交易
func (this *Provider) CreateTrade(ctx context.Context, request pay.TradeCreateRequest) (pay.TradeResult, error) {
	var extension TradeOptions
	if err := pay.DecodeExtension(request.Extensions, this.Name(), &extension); err != nil {
		return pay.TradeResult{}, err
	}
	body := gopay.BodyMap{"subject": request.Subject, "out_trade_no": request.OutTradeNo, "total_amount": request.Amount.MajorString()}
	setOptional(body, "notify_url", request.NotifyURL)
	setOptional(body, "return_url", request.ReturnURL)
	setOptional(body, "timeout_express", extension.TimeoutExpress)
	setOptional(body, "store_id", extension.StoreID)
	result := pay.TradeResult{OutTradeNo: request.OutTradeNo, Status: pay.TradeStatusPending, ChargedAmount: request.Amount}
	switch request.Mode {
	case pay.TradeModeQR:
		response, err := this.client.TradePrecreate(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeUnknown)
		}
		if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
			return result, err
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionQRCode, QRCode: &pay.QRCodeAction{Content: response.QrCode}}
	case pay.TradeModeWAP:
		body.Set("product_code", "QUICK_WAP_WAY")
		redirect, err := this.client.TradeWapPay(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeKnownFailed)
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionRedirect, Redirect: &pay.RedirectAction{URL: redirect}}
	case pay.TradeModePC:
		body.Set("product_code", "FAST_INSTANT_TRADE_PAY")
		redirect, err := this.client.TradePagePay(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeKnownFailed)
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionRedirect, Redirect: &pay.RedirectAction{URL: redirect}}
	case pay.TradeModeBarcode:
		if request.AuthCode == "" {
			return result, fmt.Errorf("%w：支付宝条码支付缺少 AuthCode", pay.ErrInvalidRequest)
		}
		body.Set("scene", "bar_code")
		body.Set("auth_code", request.AuthCode)
		body.Set("product_code", "FACE_TO_FACE_PAYMENT")
		response, err := this.client.TradePay(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeUnknown)
		}
		if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
			return result, err
		}
		result.GatewayTradeNo, result.GatewayStatus, result.Status = response.TradeNo, "TRADE_SUCCESS", pay.TradeStatusSucceeded
	case pay.TradeModeBusinessQR:
		if request.BuyerID == "" {
			return result, fmt.Errorf("%w：支付宝经营码支付缺少 BuyerID", pay.ErrInvalidRequest)
		}
		body.Set("buyer_id", request.BuyerID)
		body.Set("product_code", "FACE_TO_FACE_PAYMENT")
		response, err := this.client.TradeCreate(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeUnknown)
		}
		if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
			return result, err
		}
		result.GatewayTradeNo = response.TradeNo
	case pay.TradeModeApp:
		body.Set("product_code", "QUICK_MSECURITY_PAY")
		order, err := this.client.TradeAppPay(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeKnownFailed)
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionSDK, SDK: &pay.SDKAction{Parameters: map[string]string{"orderString": order}}}
	default:
		return result, fmt.Errorf("%w：支付宝不支持模式 %s", pay.ErrInvalidRequest, request.Mode)
	}
	return result, nil
}

// QueryTrade - 查询支付宝交易
func (this *Provider) QueryTrade(ctx context.Context, request pay.TradeQueryRequest) (pay.TradeResult, error) {
	body := make(gopay.BodyMap)
	setTradeID(body, request.OutTradeNo, request.GatewayTradeNo)
	response, err := this.client.TradeQuery(ctx, body)
	if err != nil {
		return pay.TradeResult{}, gatewayError("trade:query", err, pay.OutcomeUnknown)
	}
	if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
		return pay.TradeResult{}, err
	}
	amount, _ := pay.ParseMoney(response.TotalAmount, "CNY")
	return pay.TradeResult{OutTradeNo: response.OutTradeNo, GatewayTradeNo: response.TradeNo, Status: tradeStatus(response.TradeStatus), GatewayStatus: response.TradeStatus, ChargedAmount: amount, Raw: capture(this.options, response)}, nil
}

// CloseTrade - 关闭支付宝交易
func (this *Provider) CloseTrade(ctx context.Context, request pay.TradeCloseRequest) error {
	body := make(gopay.BodyMap)
	setTradeID(body, request.OutTradeNo, request.GatewayTradeNo)
	response, err := this.client.TradeClose(ctx, body)
	if err != nil {
		return gatewayError("trade:close", err, pay.OutcomeUnknown)
	}
	if response.ErrResponse.Code == "ACQ.TRADE_NOT_EXIST" {
		return nil
	}
	return checkAlipayResponse(response.StatusCode, response.ErrResponse)
}

// Refund - 发起支付宝退款
func (this *Provider) Refund(ctx context.Context, request pay.RefundRequest) (pay.RefundResult, error) {
	body := gopay.BodyMap{"out_request_no": request.OutRefundNo, "refund_amount": request.RefundAmount.MajorString()}
	setTradeID(body, request.OutTradeNo, request.GatewayTradeNo)
	setOptional(body, "refund_reason", request.Reason)
	response, err := this.client.TradeRefund(ctx, body)
	if err != nil {
		return pay.RefundResult{}, gatewayError("refund:create", err, pay.OutcomeUnknown)
	}
	if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
		return pay.RefundResult{}, err
	}
	status := pay.RefundStatusProcessing
	if response.FundChange == "Y" {
		status = pay.RefundStatusSucceeded
	}
	return pay.RefundResult{OutRefundNo: request.OutRefundNo, GatewayRefundNo: response.TradeNo, Status: status, GatewayStatus: response.FundChange, Amount: request.RefundAmount, Raw: capture(this.options, response)}, nil
}

// QueryRefund - 查询支付宝退款
func (this *Provider) QueryRefund(ctx context.Context, request pay.RefundQueryRequest) (pay.RefundResult, error) {
	if request.OutRefundNo == "" {
		return pay.RefundResult{}, fmt.Errorf("%w：支付宝退款查询必须提供商户退款号", pay.ErrInvalidRequest)
	}
	var extension RefundQueryOptions
	if err := pay.DecodeExtension(request.Extensions, this.Name(), &extension); err != nil {
		return pay.RefundResult{}, err
	}
	body := gopay.BodyMap{"out_request_no": request.OutRefundNo}
	setTradeID(body, extension.OutTradeNo, extension.GatewayTradeNo)
	if extension.OutTradeNo == "" && extension.GatewayTradeNo == "" {
		return pay.RefundResult{}, fmt.Errorf("%w：支付宝退款查询扩展缺少原交易号", pay.ErrInvalidRequest)
	}
	response, err := this.client.TradeFastPayRefundQuery(ctx, body)
	if err != nil {
		return pay.RefundResult{}, gatewayError("refund:query", err, pay.OutcomeUnknown)
	}
	if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
		return pay.RefundResult{}, err
	}
	amount, _ := pay.ParseMoney(response.RefundAmount, "CNY")
	return pay.RefundResult{OutRefundNo: response.OutRequestNo, GatewayRefundNo: response.TradeNo, Status: refundStatus(response.RefundStatus), GatewayStatus: response.RefundStatus, Amount: amount, Raw: capture(this.options, response)}, nil
}

// Transfer - 发起支付宝单笔转账
func (this *Provider) Transfer(ctx context.Context, request pay.TransferRequest) (pay.TransferResult, error) {
	identityType := "ALIPAY_LOGON_ID"
	if request.Payee.Type == pay.PayeeTypeUserID {
		identityType = "ALIPAY_USER_ID"
	}
	payee := gopay.BodyMap{"identity": request.Payee.Account, "identity_type": identityType}
	setOptional(payee, "name", request.Payee.Name)
	body := gopay.BodyMap{"out_biz_no": request.OutTransferNo, "trans_amount": request.Amount.MajorString(), "product_code": "TRANS_ACCOUNT_NO_PWD", "biz_scene": "DIRECT_TRANSFER", "order_title": request.Subject, "payee_info": payee}
	response, err := this.client.FundTransUniTransfer(ctx, body)
	if err != nil {
		return pay.TransferResult{}, gatewayError("transfer:create", err, pay.OutcomeUnknown)
	}
	if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
		return pay.TransferResult{}, err
	}
	return pay.TransferResult{OutTransferNo: request.OutTransferNo, GatewayTransferNo: response.OrderId, Status: transferStatus(response.Status), GatewayStatus: response.Status, Amount: request.Amount, Raw: capture(this.options, response)}, nil
}

// QueryTransfer - 查询支付宝转账
func (this *Provider) QueryTransfer(ctx context.Context, request pay.TransferQueryRequest) (pay.TransferResult, error) {
	body := gopay.BodyMap{"out_biz_no": request.OutTransferNo, "product_code": "TRANS_ACCOUNT_NO_PAY", "biz_scene": "DIRECT_TRANSFER"}
	response, err := this.client.FundTransCommonQuery(ctx, body)
	if err != nil {
		return pay.TransferResult{}, gatewayError("transfer:query", err, pay.OutcomeUnknown)
	}
	if err = checkAlipayResponse(response.StatusCode, response.ErrResponse); err != nil {
		return pay.TransferResult{}, err
	}
	amount, _ := pay.ParseMoney(response.TransAmount, "CNY")
	return pay.TransferResult{OutTransferNo: response.OutBizNo, GatewayTransferNo: response.OrderId, Status: transferStatus(response.Status), GatewayStatus: response.Status, Amount: amount, Raw: capture(this.options, response)}, nil
}

// ParseNotify - 验签并解析支付宝交易通知
func (this *Provider) ParseNotify(ctx context.Context, request pay.NotifyRequest) (pay.NotifyEvent, error) {
	if request.Kind != pay.NotifyKindTrade {
		return pay.NotifyEvent{}, pay.ErrUnsupportedCapability
	}
	if !strings.EqualFold(request.Method, http.MethodPost) {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	values := request.Query
	if len(request.Body) > 0 {
		parsed, err := url.ParseQuery(string(request.Body))
		if err != nil {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
		values = parsed
	}
	body, err := legacy.ParseNotifyByURLValues(values)
	if err != nil {
		return pay.NotifyEvent{}, fmt.Errorf("%w：支付宝通知解析失败", pay.ErrVerifyFailed)
	}
	ok, err := legacy.VerifySignWithCert([]byte(this.config.AlipayPublicCert), body)
	if err != nil || !ok {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	var notify legacy.NotifyRequest
	if err = body.Unmarshal(&notify); err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	if notify.AppId != this.config.AppID || notify.OutTradeNo == "" || notify.TradeStatus == "" {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	if notify.NotifyTime != "" {
		parsed, parseErr := time.ParseInLocation("2006-01-02 15:04:05", notify.NotifyTime, time.FixedZone("CST", 8*3600))
		if parseErr != nil || absDuration(this.options.Clock.Now().Sub(parsed)) > this.options.NotifyClockSkew {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
	}
	amount, err := pay.ParseMoney(notify.TotalAmount, "CNY")
	if err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	eventID := notify.NotifyId
	if eventID == "" {
		eventID = stableID(this.Name(), string(request.Kind), notify.OutTradeNo, notify.TradeNo, string(request.Body))
	}
	occurred := this.options.Clock.Now()
	if notify.GmtPayment != "" {
		if parsed, e := time.ParseInLocation("2006-01-02 15:04:05", notify.GmtPayment, time.FixedZone("CST", 8*3600)); e == nil {
			occurred = parsed
		}
	}
	return pay.NotifyEvent{ID: eventID, Type: eventType(notify.TradeStatus), Provider: this.Name(), Trade: &pay.TradeEvent{OutTradeNo: notify.OutTradeNo, GatewayTradeNo: notify.TradeNo, Status: tradeStatus(notify.TradeStatus), GatewayStatus: notify.TradeStatus, Amount: amount}, OccurredAt: occurred, VerifiedAt: this.options.Clock.Now(), VerificationKeyID: stableID(this.config.AlipayPublicCert)[:16], Raw: pay.CaptureRaw(this.options.RawCapture, request.Headers.Get("Content-Type"), request.Body)}, nil
}

// NotifyResponse - 编码支付宝通知 ACK
func (this *Provider) NotifyResponse(kind pay.NotifyKind, decision pay.NotifyDecision) pay.NotifyResponse {
	body := []byte("fail")
	if decision == pay.NotifyAccept {
		body = []byte("success")
	}
	return pay.NotifyResponse{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}}, Body: body}
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
				return Config{}, fmt.Errorf("%w：支付宝配置类型错误", pay.ErrInvalidConfig)
			}
			config = *pointer
		}
		return config, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(input.Raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w：支付宝配置 JSON 非法", pay.ErrInvalidConfig)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Config{}, fmt.Errorf("%w：支付宝配置包含多个 JSON 值", pay.ErrInvalidConfig)
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

func checkAlipayResponse(status int, response alipayv3.ErrResponse) error {
	if status >= 200 && status < 300 && response.Code == "" {
		return nil
	}
	return &pay.GatewayError{Provider: "alipay", Operation: "gateway", Code: response.Code, Message: response.Message, Retryable: status == 429 || status >= 500, Outcome: pay.OutcomeKnownFailed, Cause: pay.ErrGatewayRejected}
}

func gatewayError(operation string, err error, outcome pay.Outcome) error {
	retryable := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
	cause := pay.ErrGatewayRejected
	if retryable {
		cause = pay.ErrGatewayUnavailable
	}
	return &pay.GatewayError{Provider: "alipay", Operation: operation, Message: "网关调用失败", Retryable: retryable, Outcome: outcome, Cause: cause}
}

func setOptional(body gopay.BodyMap, key, value string) {
	if strings.TrimSpace(value) != "" {
		body.Set(key, value)
	}
}
func setTradeID(body gopay.BodyMap, outNo, gatewayNo string) {
	if gatewayNo != "" {
		body.Set("trade_no", gatewayNo)
	} else {
		body.Set("out_trade_no", outNo)
	}
}
func tradeStatus(value string) pay.TradeStatus {
	switch value {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		return pay.TradeStatusSucceeded
	case "WAIT_BUYER_PAY":
		return pay.TradeStatusPending
	case "TRADE_CLOSED":
		return pay.TradeStatusClosed
	default:
		return pay.TradeStatusUnknown
	}
}
func refundStatus(value string) pay.RefundStatus {
	switch value {
	case "REFUND_SUCCESS", "SUCCESS":
		return pay.RefundStatusSucceeded
	case "REFUND_PROCESSING", "PROCESSING":
		return pay.RefundStatusProcessing
	case "REFUND_FAIL", "FAIL":
		return pay.RefundStatusFailed
	default:
		return pay.RefundStatusUnknown
	}
}
func transferStatus(value string) pay.TransferStatus {
	switch value {
	case "SUCCESS":
		return pay.TransferStatusSucceeded
	case "DEALING":
		return pay.TransferStatusProcessing
	case "REFUND":
		return pay.TransferStatusClosed
	case "FAIL":
		return pay.TransferStatusFailed
	default:
		return pay.TransferStatusUnknown
	}
}
func eventType(value string) pay.EventType {
	if value == "TRADE_SUCCESS" || value == "TRADE_FINISHED" {
		return pay.EventTradeSucceeded
	}
	if value == "TRADE_CLOSED" {
		return pay.EventTradeClosed
	}
	return pay.EventTradePending
}
func capture(options pay.OpenOptions, value any) *pay.RawPayload {
	body, _ := json.Marshal(value)
	return pay.CaptureRaw(options.RawCapture, "application/json", body)
}
func stableID(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}
func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
