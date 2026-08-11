// Package wechat 提供微信支付 V3 官方 Provider 适配。
package wechat

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-pay/gopay"
	"github.com/go-pay/gopay/pkg/xhttp"
	wechatv3 "github.com/go-pay/gopay/wechat/v3"

	"github.com/inis-io/aide/pay"
)

// Config - 微信支付 V3 Provider 配置
type Config struct {
	// AppID - 微信应用 ID
	AppID string `json:"appId"`
	// MerchantID - 微信支付商户号
	MerchantID string `json:"merchantId"`
	// APIv3Key - 内联 APIv3Key
	APIv3Key pay.SensitiveString `json:"apiV3Key"`
	// APIv3KeyRef - APIv3Key 引用
	APIv3KeyRef pay.SecretRef `json:"apiV3KeyRef"`
	// SerialNo - 商户 API 证书序列号
	SerialNo string `json:"serialNo"`
	// PrivateKey - 内联商户 API 私钥
	PrivateKey pay.SensitiveString `json:"privateKey"`
	// PrivateKeyRef - 商户 API 私钥引用
	PrivateKeyRef pay.SecretRef `json:"privateKeyRef"`
	// PublicKeyID - 微信支付公钥 ID
	PublicKeyID string `json:"publicKeyId"`
	// PublicKey - 微信支付公钥内容
	PublicKey string `json:"publicKey"`
}

// TradeOptions - 微信支付交易专属扩展
type TradeOptions struct {
	// H5Type - H5 场景类型
	H5Type string `json:"h5Type,omitempty"`
}

// BillOptions - 微信账单专属扩展
type BillOptions struct {
	// TradeBillType - 交易账单类型：ALL / SUCCESS / REFUND，缺省 ALL
	TradeBillType string `json:"tradeBillType,omitempty"`
	// AccountType - 资金账单账户：BASIC / OPERATION / FEES，缺省 BASIC
	AccountType string `json:"accountType,omitempty"`
}

// Provider - 微信支付 V3 Provider 实例
type Provider struct {
	client   sdkClient
	config   Config
	apiV3Key string
	options  pay.OpenOptions
}

type sdkClient interface {
	V3TransactionNative(context.Context, gopay.BodyMap) (*wechatv3.NativeRsp, error)
	V3TransactionH5(context.Context, gopay.BodyMap) (*wechatv3.H5Rsp, error)
	V3TransactionQueryOrder(context.Context, wechatv3.OrderNoType, string) (*wechatv3.QueryOrderRsp, error)
	V3TransactionCloseOrder(context.Context, string) (*wechatv3.EmptyRsp, error)
	V3Refund(context.Context, gopay.BodyMap) (*wechatv3.RefundRsp, error)
	V3RefundQuery(context.Context, string, gopay.BodyMap) (*wechatv3.RefundQueryRsp, error)
	V3TransferBills(context.Context, gopay.BodyMap) (*wechatv3.TransferBillsRsp, error)
	V3TransferBillsQuery(context.Context, string) (*wechatv3.TransferBillsQueryRsp, error)
	V3TransferBillsMerchantQuery(context.Context, string) (*wechatv3.TransferBillsMerchantQueryRsp, error)
	V3BillTradeBill(context.Context, gopay.BodyMap) (*wechatv3.BillRsp, error)
	V3BillFundFlowBill(context.Context, gopay.BodyMap) (*wechatv3.BillRsp, error)
	V3BillDownLoadBill(context.Context, string) ([]byte, error)
	V3EncryptText(string) (string, error)
	WxPublicKeyMap() map[string]*rsa.PublicKey
}

// Register - 向指定实例 Registry 显式注册微信支付工厂
func Register(registry *pay.Registry) error {
	if registry == nil {
		return fmt.Errorf("%w：Registry 为 nil", pay.ErrInvalidConfig)
	}
	return registry.Register("wechat", Factory)
}

// Factory - 构造微信支付 Provider
func Factory(ctx context.Context, input pay.ConfigInput, options pay.OpenOptions) (pay.Provider, error) {
	if options.SchemaVersion != 1 {
		return nil, pay.ErrUnsupportedConfigVersion
	}
	if options.Sandbox {
		return nil, fmt.Errorf("%w：微信支付 V3 不提供沙箱端点", pay.ErrInvalidConfig)
	}
	config, err := decodeConfig(input)
	if err != nil {
		return nil, err
	}
	apiV3Key, err := resolveSecret(ctx, config.APIv3Key, config.APIv3KeyRef, options)
	if err != nil {
		return nil, err
	}
	privateKey, err := resolveSecret(ctx, config.PrivateKey, config.PrivateKeyRef, options)
	if err != nil {
		return nil, err
	}
	if config.AppID == "" || config.MerchantID == "" || apiV3Key == "" || config.SerialNo == "" || privateKey == "" || config.PublicKeyID == "" || config.PublicKey == "" {
		return nil, fmt.Errorf("%w：微信 AppID、商户号、APIv3Key、证书序列号、私钥和平台公钥均为必填", pay.ErrInvalidConfig)
	}
	client, err := wechatv3.NewClientV3(config.MerchantID, config.SerialNo, apiV3Key, privateKey)
	if err != nil {
		return nil, fmt.Errorf("%w：微信客户端初始化失败", pay.ErrInvalidConfig)
	}
	if err = client.AutoVerifySignByPublicKey([]byte(config.PublicKey), config.PublicKeyID); err != nil {
		return nil, fmt.Errorf("%w：微信支付公钥无效", pay.ErrInvalidConfig)
	}
	httpClient := xhttp.NewClient()
	httpClient.HttpClient = options.Client
	httpClient.SetBodySize(2)
	client.SetHttpClient(httpClient)
	return &Provider{client: client, config: config, apiV3Key: apiV3Key, options: options}, nil
}

// Name - 返回 Provider 名称
func (this *Provider) Name() string { return "wechat" }

// Capabilities - 返回微信支付真实能力集合
func (this *Provider) Capabilities() []pay.Capability {
	return []pay.Capability{pay.CapTradeCreate, pay.CapTradeQuery, pay.CapTradeClose, pay.CapRefund, pay.CapRefundQuery, pay.CapTransfer, pay.CapTransferQuery, pay.CapNotifyTrade, pay.CapNotifyRefund, pay.CapNotifyTransfer, pay.CapBill}
}

// Close - 关闭 Provider；公钥模式没有后台证书刷新任务
func (this *Provider) Close() error { return nil }

// CreateTrade - 创建 Native 或 H5 微信支付交易
func (this *Provider) CreateTrade(ctx context.Context, request pay.TradeCreateRequest) (pay.TradeResult, error) {
	if request.Amount.Currency.Code != "CNY" {
		return pay.TradeResult{}, fmt.Errorf("%w：微信境内支付只支持 CNY", pay.ErrInvalidRequest)
	}
	var extension TradeOptions
	if err := pay.DecodeExtension(request.Extensions, this.Name(), &extension); err != nil {
		return pay.TradeResult{}, err
	}
	body := gopay.BodyMap{"appid": this.config.AppID, "mchid": this.config.MerchantID, "description": request.Subject, "out_trade_no": request.OutTradeNo, "notify_url": request.NotifyURL, "amount": gopay.BodyMap{"total": request.Amount.Minor, "currency": "CNY"}}
	result := pay.TradeResult{OutTradeNo: request.OutTradeNo, Status: pay.TradeStatusPending, ChargedAmount: request.Amount}
	switch request.Mode {
	case pay.TradeModeQR:
		response, err := this.client.V3TransactionNative(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeUnknown)
		}
		if err = checkResponse(response.Code, response.ErrResponse); err != nil {
			return result, err
		}
		if response.Response == nil {
			return result, invalidResponse("trade:create")
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionQRCode, QRCode: &pay.QRCodeAction{Content: response.Response.CodeUrl}}
	case pay.TradeModeWAP:
		if request.ClientIP == "" {
			return result, fmt.Errorf("%w：微信 H5 支付缺少 ClientIP", pay.ErrInvalidRequest)
		}
		h5Type := extension.H5Type
		if h5Type == "" {
			h5Type = "Wap"
		}
		body.Set("scene_info", gopay.BodyMap{"payer_client_ip": request.ClientIP, "h5_info": gopay.BodyMap{"type": h5Type}})
		response, err := this.client.V3TransactionH5(ctx, body)
		if err != nil {
			return result, gatewayError("trade:create", err, pay.OutcomeUnknown)
		}
		if err = checkResponse(response.Code, response.ErrResponse); err != nil {
			return result, err
		}
		if response.Response == nil {
			return result, invalidResponse("trade:create")
		}
		result.Action = &pay.PaymentAction{Kind: pay.ActionRedirect, Redirect: &pay.RedirectAction{URL: response.Response.H5Url}}
	default:
		return result, fmt.Errorf("%w：微信支付只支持 qr 与 wap 模式", pay.ErrInvalidRequest)
	}
	return result, nil
}

// QueryTrade - 查询微信支付交易
func (this *Provider) QueryTrade(ctx context.Context, request pay.TradeQueryRequest) (pay.TradeResult, error) {
	typeID, orderNo := wechatv3.OutTradeNo, request.OutTradeNo
	if request.GatewayTradeNo != "" {
		typeID, orderNo = wechatv3.TransactionId, request.GatewayTradeNo
	}
	response, err := this.client.V3TransactionQueryOrder(ctx, typeID, orderNo)
	if err != nil {
		return pay.TradeResult{}, gatewayError("trade:query", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.TradeResult{}, err
	}
	if response.Response == nil {
		return pay.TradeResult{}, invalidResponse("trade:query")
	}
	amount := pay.NewMoneyMinor(0, "CNY")
	if response.Response.Amount != nil {
		amount = pay.NewMoneyMinor(int64(response.Response.Amount.Total), defaultCurrency(response.Response.Amount.Currency))
	}
	return pay.TradeResult{OutTradeNo: response.Response.OutTradeNo, GatewayTradeNo: response.Response.TransactionId, Status: tradeStatus(response.Response.TradeState), GatewayStatus: response.Response.TradeState, ChargedAmount: amount, Raw: capture(this.options, response)}, nil
}

// CloseTrade - 关闭微信支付交易
func (this *Provider) CloseTrade(ctx context.Context, request pay.TradeCloseRequest) error {
	if request.OutTradeNo == "" {
		return fmt.Errorf("%w：微信关单必须提供商户交易号", pay.ErrInvalidRequest)
	}
	response, err := this.client.V3TransactionCloseOrder(ctx, request.OutTradeNo)
	if err != nil {
		return gatewayError("trade:close", err, pay.OutcomeUnknown)
	}
	return checkResponse(response.Code, response.ErrResponse)
}

// Refund - 发起微信退款
func (this *Provider) Refund(ctx context.Context, request pay.RefundRequest) (pay.RefundResult, error) {
	if request.TotalAmount.Currency.Code != "CNY" {
		return pay.RefundResult{}, fmt.Errorf("%w：微信退款只支持 CNY", pay.ErrInvalidRequest)
	}
	body := gopay.BodyMap{"out_refund_no": request.OutRefundNo, "reason": request.Reason, "notify_url": request.NotifyURL, "amount": gopay.BodyMap{"total": request.TotalAmount.Minor, "refund": request.RefundAmount.Minor, "currency": "CNY"}}
	if request.GatewayTradeNo != "" {
		body.Set("transaction_id", request.GatewayTradeNo)
	} else {
		body.Set("out_trade_no", request.OutTradeNo)
	}
	response, err := this.client.V3Refund(ctx, body)
	if err != nil {
		return pay.RefundResult{}, gatewayError("refund:create", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.RefundResult{}, err
	}
	if response.Response == nil {
		return pay.RefundResult{}, invalidResponse("refund:create")
	}
	return pay.RefundResult{OutRefundNo: response.Response.OutRefundNo, GatewayRefundNo: response.Response.RefundId, Status: refundStatus(response.Response.Status), GatewayStatus: response.Response.Status, Amount: request.RefundAmount, Raw: capture(this.options, response)}, nil
}

// QueryRefund - 查询微信退款
func (this *Provider) QueryRefund(ctx context.Context, request pay.RefundQueryRequest) (pay.RefundResult, error) {
	if request.OutRefundNo == "" {
		return pay.RefundResult{}, fmt.Errorf("%w：微信退款查询必须提供商户退款号", pay.ErrInvalidRequest)
	}
	response, err := this.client.V3RefundQuery(ctx, request.OutRefundNo, nil)
	if err != nil {
		return pay.RefundResult{}, gatewayError("refund:query", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.RefundResult{}, err
	}
	if response.Response == nil {
		return pay.RefundResult{}, invalidResponse("refund:query")
	}
	amount := pay.NewMoneyMinor(0, "CNY")
	if response.Response.Amount != nil {
		amount = pay.NewMoneyMinor(int64(response.Response.Amount.Refund), defaultCurrency(response.Response.Amount.Currency))
	}
	return pay.RefundResult{OutRefundNo: response.Response.OutRefundNo, GatewayRefundNo: response.Response.RefundId, Status: refundStatus(response.Response.Status), GatewayStatus: response.Response.Status, Amount: amount, Raw: capture(this.options, response)}, nil
}

// Transfer - 发起微信商家转账
func (this *Provider) Transfer(ctx context.Context, request pay.TransferRequest) (pay.TransferResult, error) {
	if request.Amount.Currency.Code != "CNY" || request.Payee.Type != pay.PayeeTypeOpenID {
		return pay.TransferResult{}, fmt.Errorf("%w：微信转账要求 CNY 与 OpenID 收款人", pay.ErrInvalidRequest)
	}
	remark := request.Subject
	if len([]rune(remark)) > 32 {
		remark = string([]rune(remark)[:32])
	}
	body := gopay.BodyMap{"appid": this.config.AppID, "out_bill_no": request.OutTransferNo, "transfer_scene_id": request.Scene, "openid": request.Payee.Account, "transfer_amount": request.Amount.Minor, "transfer_remark": remark, "notify_url": request.NotifyURL}
	if request.Scene == "" {
		body.Set("transfer_scene_id", "1000")
	}
	if len(request.SceneReport) > 0 {
		reports := make([]map[string]string, 0, len(request.SceneReport))
		for key, value := range request.SceneReport {
			reports = append(reports, map[string]string{"info_type": key, "info_content": value})
		}
		body.Set("transfer_scene_report_infos", reports)
	}
	if request.Payee.Name != "" {
		encrypted, err := this.client.V3EncryptText(request.Payee.Name)
		if err != nil {
			return pay.TransferResult{}, fmt.Errorf("%w：微信收款人姓名加密失败", pay.ErrInvalidRequest)
		}
		body.Set("user_name", encrypted)
	}
	response, err := this.client.V3TransferBills(ctx, body)
	if err != nil {
		return pay.TransferResult{}, gatewayError("transfer:create", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.TransferResult{}, err
	}
	if response.Response == nil {
		return pay.TransferResult{}, invalidResponse("transfer:create")
	}
	return pay.TransferResult{OutTransferNo: response.Response.OutBillNo, GatewayTransferNo: response.Response.TransferBillNo, Status: transferStatus(response.Response.State), GatewayStatus: response.Response.State, Amount: request.Amount, Raw: capture(this.options, response)}, nil
}

// QueryTransfer - 查询微信商家转账
func (this *Provider) QueryTransfer(ctx context.Context, request pay.TransferQueryRequest) (pay.TransferResult, error) {
	if request.GatewayTransferNo != "" {
		response, err := this.client.V3TransferBillsQuery(ctx, request.GatewayTransferNo)
		if err != nil {
			return pay.TransferResult{}, gatewayError("transfer:query", err, pay.OutcomeUnknown)
		}
		if err = checkResponse(response.Code, response.ErrResponse); err != nil {
			return pay.TransferResult{}, err
		}
		if response.Response == nil {
			return pay.TransferResult{}, invalidResponse("transfer:query")
		}
		return pay.TransferResult{OutTransferNo: response.Response.OutBillNo, GatewayTransferNo: response.Response.TransferBillNo, Status: transferStatus(response.Response.State), GatewayStatus: response.Response.State, Amount: pay.NewMoneyMinor(int64(response.Response.TransferAmount), "CNY"), Raw: capture(this.options, response)}, nil
	}
	response, err := this.client.V3TransferBillsMerchantQuery(ctx, request.OutTransferNo)
	if err != nil {
		return pay.TransferResult{}, gatewayError("transfer:query", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.TransferResult{}, err
	}
	if response.Response == nil {
		return pay.TransferResult{}, invalidResponse("transfer:query")
	}
	return pay.TransferResult{OutTransferNo: response.Response.OutBillNo, GatewayTransferNo: response.Response.TransferBillNo, Status: transferStatus(response.Response.State), GatewayStatus: response.Response.State, Amount: pay.NewMoneyMinor(int64(response.Response.TransferAmount), "CNY"), Raw: capture(this.options, response)}, nil
}

// FetchBill - 获取并代下载微信对账单（微信下载地址必须带签名 GET，建议总是 Fetch=true）
func (this *Provider) FetchBill(ctx context.Context, request pay.BillRequest) (pay.BillResult, error) {
	if len(request.Date) == len("2006-01") {
		return pay.BillResult{}, fmt.Errorf("%w：微信不支持月账单", pay.ErrInvalidRequest)
	}
	var extension BillOptions
	if err := pay.DecodeExtension(request.Extensions, this.Name(), &extension); err != nil {
		return pay.BillResult{}, err
	}
	body := gopay.BodyMap{"bill_date": request.Date, "tar_type": "GZIP"}
	var response *wechatv3.BillRsp
	var err error
	switch request.Type {
	case pay.BillTypeFundFlow:
		if extension.AccountType == "" {
			extension.AccountType = "BASIC"
		}
		body.Set("account_type", extension.AccountType)
		response, err = this.client.V3BillFundFlowBill(ctx, body)
	case pay.BillTypeTrade, "":
		if extension.TradeBillType == "" {
			extension.TradeBillType = "ALL"
		}
		body.Set("bill_type", extension.TradeBillType)
		response, err = this.client.V3BillTradeBill(ctx, body)
	default:
		return pay.BillResult{}, fmt.Errorf("%w：未知账单类型 %s", pay.ErrInvalidRequest, request.Type)
	}
	if err != nil {
		return pay.BillResult{}, gatewayError("bill:fetch", err, pay.OutcomeUnknown)
	}
	if err = checkResponse(response.Code, response.ErrResponse); err != nil {
		return pay.BillResult{}, err
	}
	if response.Response == nil {
		return pay.BillResult{}, invalidResponse("bill:fetch")
	}
	result := pay.BillResult{DownloadURL: response.Response.DownloadUrl, HashType: response.Response.HashType, HashValue: response.Response.HashValue, Raw: capture(this.options, response)}
	if !request.Fetch {
		return result, nil
	}
	content, err := this.client.V3BillDownLoadBill(ctx, result.DownloadURL)
	if err != nil {
		return pay.BillResult{}, gatewayError("bill:fetch", err, pay.OutcomeUnknown)
	}
	limit := this.options.BillMaxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	if int64(len(content)) > limit {
		result.Content, result.Truncated = content[:limit], true
		return result, nil
	}
	result.Content = content
	if strings.EqualFold(result.HashType, "SHA1") && result.HashValue != "" {
		digest := sha1.Sum(content)
		if !strings.EqualFold(hex.EncodeToString(digest[:]), result.HashValue) {
			return pay.BillResult{}, &pay.GatewayError{Provider: this.Name(), Operation: "bill:fetch", Message: "账单摘要校验失败", Retryable: true, Outcome: pay.OutcomeUnknown, Cause: pay.ErrGatewayRejected}
		}
	}
	return result, nil
}

// ParseNotify - 严格验签、时间校验并解密微信通知
func (this *Provider) ParseNotify(ctx context.Context, request pay.NotifyRequest) (pay.NotifyEvent, error) {
	if !strings.EqualFold(request.Method, http.MethodPost) {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, "https://notify.invalid", bytes.NewReader(request.Body))
	if err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	httpRequest.Header = request.Headers.Clone()
	notify, err := wechatv3.V3ParseNotify(httpRequest)
	if err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	if err = notify.VerifySignByPKMap(this.client.WxPublicKeyMap()); err != nil {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	timestamp, err := strconv.ParseInt(notify.SignInfo.HeaderTimestamp, 10, 64)
	if err != nil || absDuration(this.options.Clock.Now().Sub(time.Unix(timestamp, 0))) > this.options.NotifyClockSkew {
		return pay.NotifyEvent{}, pay.ErrVerifyFailed
	}
	base := pay.NotifyEvent{ID: notify.Id, Provider: this.Name(), OccurredAt: parseTime(notify.CreateTime, this.options.Clock.Now()), VerifiedAt: this.options.Clock.Now(), VerificationKeyID: notify.SignInfo.HeaderSerial, Raw: pay.CaptureRaw(this.options.RawCapture, request.Headers.Get("Content-Type"), request.Body)}
	if base.ID == "" {
		digest := sha256.Sum256(request.Body)
		base.ID = hex.EncodeToString(digest[:])
	}
	switch request.Kind {
	case pay.NotifyKindTrade:
		result, e := notify.DecryptPayCipherText(this.apiV3Key)
		if e != nil || result.Mchid != this.config.MerchantID || result.Appid != this.config.AppID || result.Amount == nil {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
		base.Type = tradeEventType(result.TradeState)
		base.Trade = &pay.TradeEvent{OutTradeNo: result.OutTradeNo, GatewayTradeNo: result.TransactionId, Status: tradeStatus(result.TradeState), GatewayStatus: result.TradeState, Amount: pay.NewMoneyMinor(int64(result.Amount.Total), defaultCurrency(result.Amount.Currency))}
	case pay.NotifyKindRefund:
		result, e := notify.DecryptRefundCipherText(this.apiV3Key)
		if e != nil || result.Mchid != this.config.MerchantID || result.Amount == nil {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
		base.Type = refundEventType(result.RefundStatus)
		base.Refund = &pay.RefundEvent{OutTradeNo: result.OutTradeNo, OutRefundNo: result.OutRefundNo, GatewayRefundNo: result.RefundId, Status: refundStatus(result.RefundStatus), GatewayStatus: result.RefundStatus, Amount: pay.NewMoneyMinor(int64(result.Amount.Refund), "CNY")}
	case pay.NotifyKindTransfer:
		result, e := notify.DecryptTransferBillsNotifyCipherText(this.apiV3Key)
		if e != nil || result.MchId != this.config.MerchantID {
			return pay.NotifyEvent{}, pay.ErrVerifyFailed
		}
		base.Type = transferEventType(result.State)
		base.Transfer = &pay.TransferEvent{OutTransferNo: result.OutBillNo, GatewayTransferNo: result.TransferBillNo, Status: transferStatus(result.State), GatewayStatus: result.State, Amount: pay.NewMoneyMinor(int64(result.TransferAmount), "CNY")}
	default:
		return pay.NotifyEvent{}, pay.ErrUnsupportedCapability
	}
	return base, nil
}

// NotifyResponse - 编码微信支付 V3 通知 ACK
func (this *Provider) NotifyResponse(kind pay.NotifyKind, decision pay.NotifyDecision) pay.NotifyResponse {
	status, code, message := http.StatusOK, "SUCCESS", "成功"
	if decision == pay.NotifyRetry {
		status, code, message = http.StatusInternalServerError, "FAIL", "处理失败，请重试"
	}
	if decision == pay.NotifyReject {
		status, code, message = http.StatusBadRequest, "FAIL", "非法通知"
	}
	body, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return pay.NotifyResponse{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}, Body: body}
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
				return Config{}, fmt.Errorf("%w：微信配置类型错误", pay.ErrInvalidConfig)
			}
			config = *pointer
		}
		return config, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(input.Raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("%w：微信配置 JSON 非法", pay.ErrInvalidConfig)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Config{}, fmt.Errorf("%w：微信配置包含多个 JSON 值", pay.ErrInvalidConfig)
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

// wechatReasonMap - 微信支付错误码到标准分类的映射表
// 出处：ORDER_NOT_EXIST / PARAM_ERROR / INVALID_REQUEST 见微信商户平台错误码文档；
// 268892183（订单或退款金额不一致）、268448746（单笔订单退款频率限制）见官方退款错误码。
// RULE_LIMIT 属业务规则限制而非频率限制，不映射为 rate-limited，留空待联调归类。
var wechatReasonMap = map[string]pay.Reason{
	"ORDER_NOT_EXIST":    pay.ReasonOrderNotFound,
	"PARAM_ERROR":        pay.ReasonInvalidRequest,
	"INVALID_REQUEST":    pay.ReasonInvalidRequest,
	"268892183":          pay.ReasonAmountMismatch,
	"268448746":          pay.ReasonRateLimited,
}

// reasonFor - 将微信原始错误码映射为标准分类；未知码返回 ReasonNone
func reasonFor(code string) pay.Reason {
	return wechatReasonMap[code]
}

func checkResponse(code int, response wechatv3.ErrResponse) error {
	if code == wechatv3.Success && response.Code == "" {
		return nil
	}
	return &pay.GatewayError{Provider: "wechat", Operation: "gateway", Code: response.Code, Message: response.Message, Reason: reasonFor(response.Code), Retryable: code == 429 || code >= 500, Outcome: pay.OutcomeKnownFailed, Cause: pay.ErrGatewayRejected}
}
func invalidResponse(operation string) error {
	return &pay.GatewayError{Provider: "wechat", Operation: operation, Message: "网关响应为空", Retryable: true, Outcome: pay.OutcomeUnknown, Cause: pay.ErrGatewayUnavailable}
}
func gatewayError(operation string, err error, outcome pay.Outcome) error {
	retryable := errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
	cause := pay.ErrGatewayRejected
	if retryable {
		cause = pay.ErrGatewayUnavailable
	}
	return &pay.GatewayError{Provider: "wechat", Operation: operation, Message: "网关调用失败", Retryable: retryable, Outcome: outcome, Cause: cause}
}
func defaultCurrency(value string) string {
	if value == "" {
		return "CNY"
	}
	return value
}
func tradeStatus(value string) pay.TradeStatus {
	switch value {
	case "SUCCESS":
		return pay.TradeStatusSucceeded
	case "NOTPAY":
		return pay.TradeStatusPending
	case "USERPAYING":
		return pay.TradeStatusProcessing
	case "CLOSED", "REVOKED":
		return pay.TradeStatusClosed
	case "PAYERROR":
		return pay.TradeStatusFailed
	default:
		return pay.TradeStatusUnknown
	}
}
func refundStatus(value string) pay.RefundStatus {
	switch value {
	case "SUCCESS":
		return pay.RefundStatusSucceeded
	case "PROCESSING":
		return pay.RefundStatusProcessing
	case "CLOSED":
		return pay.RefundStatusClosed
	case "ABNORMAL":
		return pay.RefundStatusFailed
	default:
		return pay.RefundStatusUnknown
	}
}
func transferStatus(value string) pay.TransferStatus {
	switch value {
	case "SUCCESS":
		return pay.TransferStatusSucceeded
	case "WAIT_USER_CONFIRM", "ACCEPTED", "PROCESSING":
		return pay.TransferStatusProcessing
	case "CANCELLED", "CANCELING":
		return pay.TransferStatusClosed
	case "FAIL":
		return pay.TransferStatusFailed
	default:
		return pay.TransferStatusUnknown
	}
}
func tradeEventType(value string) pay.EventType {
	if value == "SUCCESS" {
		return pay.EventTradeSucceeded
	}
	if value == "CLOSED" || value == "REVOKED" {
		return pay.EventTradeClosed
	}
	return pay.EventTradePending
}
func refundEventType(value string) pay.EventType {
	if value == "SUCCESS" {
		return pay.EventRefundSucceeded
	}
	if value == "ABNORMAL" || value == "CLOSED" {
		return pay.EventRefundFailed
	}
	return pay.EventRefundProcessing
}
func transferEventType(value string) pay.EventType {
	if value == "SUCCESS" {
		return pay.EventTransferSucceeded
	}
	if value == "WAIT_USER_CONFIRM" || value == "ACCEPTED" || value == "PROCESSING" {
		return pay.EventTransferProcessing
	}
	return pay.EventTransferFailed
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
func parseTime(value string, fallback time.Time) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return fallback
	}
	return parsed
}
