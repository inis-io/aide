package wechat

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/go-pay/gopay"
	wechatv3 "github.com/go-pay/gopay/wechat/v3"
	"github.com/spf13/cast"

	"github.com/inis-io/aide/pay"
)

type fakeSDK struct {
	t          *testing.T
	publicKeys map[string]*rsa.PublicKey
	billHash   string
}

func (this *fakeSDK) V3TransactionNative(_ context.Context, body gopay.BodyMap) (*wechatv3.NativeRsp, error) {
	this.expectAmount(body, 1001)
	return &wechatv3.NativeRsp{Response: &wechatv3.Native{CodeUrl: "weixin://pay"}}, nil
}
func (this *fakeSDK) V3TransactionH5(_ context.Context, body gopay.BodyMap) (*wechatv3.H5Rsp, error) {
	this.expectAmount(body, 1001)
	return &wechatv3.H5Rsp{Response: &wechatv3.H5Url{H5Url: "https://wx.example/h5"}}, nil
}
func (this *fakeSDK) V3TransactionQueryOrder(context.Context, wechatv3.OrderNoType, string) (*wechatv3.QueryOrderRsp, error) {
	return &wechatv3.QueryOrderRsp{Response: &wechatv3.QueryOrder{OutTradeNo: "T-1", TransactionId: "WX-1", TradeState: "SUCCESS", Amount: &wechatv3.Amount{Total: 1001, Currency: "CNY"}}}, nil
}
func (this *fakeSDK) V3TransactionCloseOrder(context.Context, string) (*wechatv3.EmptyRsp, error) {
	return &wechatv3.EmptyRsp{}, nil
}
func (this *fakeSDK) V3Refund(_ context.Context, body gopay.BodyMap) (*wechatv3.RefundRsp, error) {
	amount, _ := body["amount"].(gopay.BodyMap)
	if cast.ToInt64(amount["refund"]) != 101 {
		this.t.Fatalf("微信退款金额边界错误：%v", amount)
	}
	return &wechatv3.RefundRsp{Response: &wechatv3.RefundOrderResponse{RefundId: "WX-R-1", OutRefundNo: "R-1", Status: "PROCESSING", Amount: &wechatv3.RefundOrderAmount{Refund: 101, Currency: "CNY"}}}, nil
}
func (this *fakeSDK) V3RefundQuery(context.Context, string, gopay.BodyMap) (*wechatv3.RefundQueryRsp, error) {
	return &wechatv3.RefundQueryRsp{Response: &wechatv3.RefundQueryResponse{RefundId: "WX-R-1", OutRefundNo: "R-1", Status: "SUCCESS", Amount: &wechatv3.RefundOrderAmount{Refund: 101, Currency: "CNY"}}}, nil
}
func (this *fakeSDK) V3TransferBills(_ context.Context, body gopay.BodyMap) (*wechatv3.TransferBillsRsp, error) {
	if cast.ToInt64(body["transfer_amount"]) != 200 {
		this.t.Fatalf("微信转账金额边界错误：%v", body)
	}
	return &wechatv3.TransferBillsRsp{Response: &wechatv3.TransferBills{OutBillNo: "X-1", TransferBillNo: "WX-X-1", State: "ACCEPTED"}}, nil
}
func (this *fakeSDK) V3TransferBillsQuery(context.Context, string) (*wechatv3.TransferBillsQueryRsp, error) {
	return &wechatv3.TransferBillsQueryRsp{Response: &wechatv3.TransferBillsQuery{OutBillNo: "X-1", TransferBillNo: "WX-X-1", State: "SUCCESS", TransferAmount: 200}}, nil
}
func (this *fakeSDK) V3TransferBillsMerchantQuery(context.Context, string) (*wechatv3.TransferBillsMerchantQueryRsp, error) {
	return &wechatv3.TransferBillsMerchantQueryRsp{Response: &wechatv3.TransferBillsMerchantQuery{OutBillNo: "X-1", TransferBillNo: "WX-X-1", State: "SUCCESS", TransferAmount: 200}}, nil
}
func (this *fakeSDK) V3BillTradeBill(_ context.Context, body gopay.BodyMap) (*wechatv3.BillRsp, error) {
	if body.GetString("bill_type") != "ALL" && body.GetString("bill_type") != "SUCCESS" && body.GetString("bill_type") != "REFUND" {
		this.t.Fatalf("微信交易账单类型错误：%s", body.GetString("bill_type"))
	}
	return &wechatv3.BillRsp{Response: &wechatv3.TradeBill{HashType: "SHA1", HashValue: this.billHash, DownloadUrl: "https://wxbill.test/bill.tar.gz"}}, nil
}
func (this *fakeSDK) V3BillFundFlowBill(_ context.Context, body gopay.BodyMap) (*wechatv3.BillRsp, error) {
	if body.GetString("account_type") != "BASIC" && body.GetString("account_type") != "OPERATION" && body.GetString("account_type") != "FEES" {
		this.t.Fatalf("微信资金账单账户类型错误：%s", body.GetString("account_type"))
	}
	return &wechatv3.BillRsp{Response: &wechatv3.TradeBill{HashType: "SHA1", HashValue: this.billHash, DownloadUrl: "https://wxbill.test/fund.tar.gz"}}, nil
}
func (this *fakeSDK) V3BillDownLoadBill(context.Context, string) ([]byte, error) {
	return []byte("gzip-bill"), nil
}
func (this *fakeSDK) V3EncryptText(value string) (string, error) { return "encrypted:" + value, nil }
func (this *fakeSDK) WxPublicKeyMap() map[string]*rsa.PublicKey  { return this.publicKeys }
func (this *fakeSDK) expectAmount(body gopay.BodyMap, expected int64) {
	amount, _ := body["amount"].(gopay.BodyMap)
	if cast.ToInt64(amount["total"]) != expected {
		this.t.Fatalf("微信支付金额边界错误：%v", amount)
	}
}

// TestRegisterStrictConfigAndSandbox - 验证显式注册、未知字段与不支持的沙箱配置
func TestRegisterStrictConfigAndSandbox(t *testing.T) {
	registry := pay.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.OpenRaw(context.Background(), "wechat", []byte(`{"unknown":true}`)); !errors.Is(err, pay.ErrInvalidConfig) {
		t.Fatalf("未知配置字段应拒绝：%v", err)
	}
	config := Config{AppID: "app", MerchantID: "mch", APIv3Key: pay.NewSensitiveString("key"), SerialNo: "sn", PrivateKey: pay.NewSensitiveString("private"), PublicKeyID: "pub", PublicKey: "public"}
	if _, err := registry.New(context.Background(), "wechat", config, pay.WithSandbox(true)); !errors.Is(err, pay.ErrInvalidConfig) {
		t.Fatalf("V3 沙箱应明确拒绝：%v", err)
	}
}

// TestStatusMappings - 验证微信网关状态映射保持未知值
func TestStatusMappings(t *testing.T) {
	if tradeStatus("SUCCESS") != pay.TradeStatusSucceeded || tradeStatus("FUTURE") != pay.TradeStatusUnknown {
		t.Fatal("交易状态映射错误")
	}
	if refundStatus("PROCESSING") != pay.RefundStatusProcessing || transferStatus("CANCELLED") != pay.TransferStatusClosed {
		t.Fatal("退款或转账状态映射错误")
	}
}

// TestReasonMapping - 验证微信错误码到标准分类的映射与网关错误 Reason 填充
func TestReasonMapping(t *testing.T) {
	cases := map[string]pay.Reason{
		"ORDER_NOT_EXIST": pay.ReasonOrderNotFound,
		"PARAM_ERROR":     pay.ReasonInvalidRequest,
		"INVALID_REQUEST": pay.ReasonInvalidRequest,
		"268892183":       pay.ReasonAmountMismatch,
		"268448746":       pay.ReasonRateLimited,
		"RULE_LIMIT":      pay.ReasonNone,
		"UNKNOWN":         pay.ReasonNone,
	}
	for code, expected := range cases {
		if reasonFor(code) != expected {
			t.Fatalf("错误码 %s 映射错误：%s", code, reasonFor(code))
		}
	}
	err := checkResponse(404, wechatv3.ErrResponse{Code: "ORDER_NOT_EXIST", Message: "订单不存在"})
	var gateway *pay.GatewayError
	if !errors.As(err, &gateway) || gateway.Reason != pay.ReasonOrderNotFound {
		t.Fatalf("checkResponse 未填充 Reason：%v", err)
	}
	if pay.ReasonOf(checkResponse(400, wechatv3.ErrResponse{Code: "SOMETHING_ELSE"})) != pay.ReasonNone {
		t.Fatal("未知码应返回 ReasonNone")
	}
}

// TestOfflineOperations - 用 fake SDK 验证微信主要资金操作与分制金额边界
func TestOfflineOperations(t *testing.T) {
	provider := &Provider{client: &fakeSDK{t: t}, config: Config{AppID: "app", MerchantID: "mch"}, options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}}}
	create := pay.NewTradeCreateRequest("T-1", pay.TradeModeQR, "商品", pay.NewMoneyMinor(1001, "CNY"))
	create.NotifyURL = "https://merchant.test/notify"
	result, err := provider.CreateTrade(context.Background(), create)
	if err != nil || result.Action == nil || result.Action.QRCode == nil {
		t.Fatalf("Native 下单失败：%+v %v", result, err)
	}
	create.Mode, create.ClientIP = pay.TradeModeWAP, "127.0.0.1"
	result, err = provider.CreateTrade(context.Background(), create)
	if err != nil || result.Action.Redirect == nil {
		t.Fatalf("H5 下单失败：%+v %v", result, err)
	}
	query, err := provider.QueryTrade(context.Background(), pay.NewTradeQueryRequest("T-1"))
	if err != nil || query.Status != pay.TradeStatusSucceeded || query.ChargedAmount.Minor != 1001 {
		t.Fatalf("查单失败：%+v %v", query, err)
	}
	refund := pay.NewRefundRequest("T-1", "R-1", pay.NewMoneyMinor(1001, "CNY"), pay.NewMoneyMinor(101, "CNY"))
	refundResult, err := provider.Refund(context.Background(), refund)
	if err != nil || refundResult.Status != pay.RefundStatusProcessing {
		t.Fatalf("退款失败：%+v %v", refundResult, err)
	}
	refundResult, err = provider.QueryRefund(context.Background(), pay.NewRefundQueryRequest("R-1"))
	if err != nil || refundResult.Status != pay.RefundStatusSucceeded {
		t.Fatalf("退款查询失败：%+v %v", refundResult, err)
	}
	transfer := pay.NewTransferRequest("X-1", pay.NewMoneyMinor(200, "CNY"), pay.Payee{Account: "openid", Name: "兔子", Type: pay.PayeeTypeOpenID})
	transfer.IdempotencyKey, transfer.Subject = "idem", "转账"
	transferResult, err := provider.Transfer(context.Background(), transfer)
	if err != nil || transferResult.Status != pay.TransferStatusProcessing {
		t.Fatalf("转账失败：%+v %v", transferResult, err)
	}
	transferResult, err = provider.QueryTransfer(context.Background(), pay.NewTransferQueryRequest("X-1"))
	if err != nil || transferResult.Status != pay.TransferStatusSucceeded {
		t.Fatalf("转账查询失败：%+v %v", transferResult, err)
	}
	if err = provider.CloseTrade(context.Background(), pay.NewTradeCloseRequest("T-1")); err != nil {
		t.Fatalf("关单失败：%v", err)
	}
}

// TestFetchBill - 验证账单类型映射、月账单拒绝、代下载与 SHA-1 校验
func TestFetchBill(t *testing.T) {
	digest := sha1.Sum([]byte("gzip-bill"))
	provider := &Provider{client: &fakeSDK{t: t, billHash: hex.EncodeToString(digest[:])}, options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}, BillMaxBytes: 1 << 20}}
	result, err := provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01"})
	if err != nil || result.DownloadURL == "" || result.HashType != "SHA1" {
		t.Fatalf("交易账单申请失败：%+v %v", result, err)
	}
	result, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Type: pay.BillTypeFundFlow})
	if err != nil || result.DownloadURL == "" {
		t.Fatalf("资金账单申请失败：%+v %v", result, err)
	}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08"}); !errors.Is(err, pay.ErrInvalidRequest) {
		t.Fatalf("微信月账单应拒绝：%v", err)
	}
	result, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Fetch: true})
	if err != nil || result.Truncated || string(result.Content) != "gzip-bill" {
		t.Fatalf("代下载与 SHA-1 校验失败：%+v %v", result, err)
	}
	provider.client = &fakeSDK{t: t, billHash: "deadbeef"}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Fetch: true}); !errors.Is(err, pay.ErrGatewayRejected) {
		t.Fatalf("摘要不符应拒绝：%v", err)
	}
	provider.options.BillMaxBytes = 4
	result, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Fetch: true})
	if err != nil || !result.Truncated || len(result.Content) != 4 {
		t.Fatalf("截断时应跳过摘要校验：%+v %v", result, err)
	}
}

// TestFetchBillExtensions - 验证扩展的显式值透传与未知账单类型拒绝
func TestFetchBillExtensions(t *testing.T) {
	provider := &Provider{client: &fakeSDK{t: t, billHash: "any"}, options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}}}
	extensions, err := pay.SetExtension(nil, "wechat", BillOptions{TradeBillType: "REFUND", AccountType: "OPERATION"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Type: pay.BillTypeFundFlow, Extensions: extensions}); err != nil {
		t.Fatalf("资金账单扩展失败：%v", err)
	}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Type: pay.BillTypeFundFlow, Extensions: extensions}); err != nil {
		t.Fatalf("交易账单扩展失败：%v", err)
	}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Type: "unknown"}); !errors.Is(err, pay.ErrInvalidRequest) {
		t.Fatalf("未知账单类型应拒绝：%v", err)
	}
}

// TestQueryTradeOrderNotFound - 查无此单统一识别为 order-not-found
func TestQueryTradeOrderNotFound(t *testing.T) {
	provider := &Provider{client: &orderNotFoundSDK{&fakeSDK{t: t}}, options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}}}
	_, err := provider.QueryTrade(context.Background(), pay.NewTradeQueryRequest("T-404"))
	if pay.ReasonOf(err) != pay.ReasonOrderNotFound {
		t.Fatalf("查无此单应统一识别：%v", err)
	}
}

type orderNotFoundSDK struct{ *fakeSDK }

func (this *orderNotFoundSDK) V3TransactionQueryOrder(context.Context, wechatv3.OrderNoType, string) (*wechatv3.QueryOrderRsp, error) {
	return &wechatv3.QueryOrderRsp{Code: 404, ErrResponse: wechatv3.ErrResponse{Code: "ORDER_NOT_EXIST", Message: "订单不存在"}}, nil
}

// TestOfflineSignedNotify - 用本地 RSA 与 AES-GCM fixture 验证微信通知验签、时间窗和解密顺序
func TestOfflineSignedNotify(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	apiKey := "0123456789abcdef0123456789abcdef"
	plaintext := []byte(`{"appid":"app","mchid":"mch","out_trade_no":"T-1","transaction_id":"WX-1","trade_state":"SUCCESS","success_time":"2026-08-08T12:00:00Z","amount":{"total":1001,"currency":"CNY"}}`)
	nonce, associated := "123456789012", "transaction"
	ciphertext := encryptFixture(t, []byte(apiKey), []byte(nonce), []byte(associated), plaintext)
	body, err := json.Marshal(map[string]any{
		"id": "EV-1", "create_time": "2026-08-08T12:00:00Z", "event_type": "TRANSACTION.SUCCESS", "resource_type": "encrypt-resource",
		"resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": base64.StdEncoding.EncodeToString(ciphertext), "associated_data": associated, "nonce": nonce},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp, headerNonce := strconv.FormatInt(now.Unix(), 10), "notify-nonce"
	signed := timestamp + "\n" + headerNonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{
		"Wechatpay-Timestamp": []string{timestamp}, "Wechatpay-Nonce": []string{headerNonce},
		"Wechatpay-Signature": []string{base64.StdEncoding.EncodeToString(signature)}, "Wechatpay-Serial": []string{"PUB-1"},
		"Content-Type": []string{"application/json"},
	}
	provider := &Provider{
		client: &fakeSDK{t: t, publicKeys: map[string]*rsa.PublicKey{"PUB-1": &privateKey.PublicKey}},
		config: Config{AppID: "app", MerchantID: "mch"}, apiV3Key: apiKey,
		options: pay.OpenOptions{Clock: fixedClock{now}, NotifyClockSkew: 5 * time.Minute, RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}},
	}
	request := pay.NotifyRequest{Kind: pay.NotifyKindTrade, Method: http.MethodPost, Headers: headers, Body: body}
	event, err := provider.ParseNotify(context.Background(), request)
	if err != nil {
		t.Fatalf("签名通知解析失败：%v", err)
	}
	if event.ID != "EV-1" || event.VerificationKeyID != "PUB-1" || event.Trade == nil || event.Trade.Amount.Minor != 1001 {
		t.Fatalf("通知事件不符：%+v", event)
	}
	privatePEM, publicPEM := pemFixture(t, privateKey)
	registry := pay.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	driver, err := registry.New(context.Background(), "wechat", Config{AppID: "app", MerchantID: "mch", APIv3Key: pay.NewSensitiveString(apiKey), SerialNo: "sn", PrivateKey: pay.NewSensitiveString(privatePEM), PublicKeyID: "PUB-1", PublicKey: publicPEM}, pay.WithClock(fixedClock{now}), pay.WithNotifyLimits(1<<20, 5*time.Minute))
	if err != nil {
		t.Fatalf("构造 Driver 失败：%v", err)
	}
	defer driver.Close()
	driverEvent, err := driver.ParseNotify(context.Background(), request)
	if err != nil || driverEvent.DedupeKey != "T-1|trade.succeeded" {
		t.Fatalf("Driver 派生去重键错误：%+v %v", driverEvent, err)
	}
	repeated, err := driver.ParseNotify(context.Background(), request)
	if err != nil || repeated.DedupeKey != driverEvent.DedupeKey {
		t.Fatalf("同一载荷重复解析键应相同：%s vs %s", repeated.DedupeKey, driverEvent.DedupeKey)
	}
	headers.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString([]byte("invalid")))
	if _, err = provider.ParseNotify(context.Background(), request); !errors.Is(err, pay.ErrVerifyFailed) {
		t.Fatalf("错误签名必须拒绝：%v", err)
	}
}

func pemFixture(t *testing.T, key *rsa.PrivateKey) (string, string) {
	t.Helper()
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	return string(privatePEM), string(publicPEM)
}

type fixedClock struct{ now time.Time }

func (this fixedClock) Now() time.Time { return this.now }

func encryptFixture(t *testing.T, key, nonce, associated, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return gcm.Seal(nil, nonce, plaintext, associated)
}
