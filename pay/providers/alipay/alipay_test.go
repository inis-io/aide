package alipay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-pay/gopay"
	alipayv3 "github.com/go-pay/gopay/alipay/v3"
	"github.com/spf13/cast"

	"github.com/inis-io/aide/pay"
)

type fixedClock struct{ now time.Time }

func (this fixedClock) Now() time.Time { return this.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (this roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}

type fakeSDK struct {
	t *testing.T
	// billType / billDate - DataBillDownloadUrlQuery 期望的账单入参，billType 为空时按 trade 校验，billDate 为空不校验
	billType string
	billDate string
}

func (this *fakeSDK) TradePrecreate(_ context.Context, body gopay.BodyMap) (*alipayv3.TradePrecreateRsp, error) {
	this.expectMoney(body, "10.01")
	return &alipayv3.TradePrecreateRsp{StatusCode: 200, OutTradeNo: body.GetString("out_trade_no"), QrCode: "qr-content"}, nil
}
func (this *fakeSDK) TradeWapPay(_ context.Context, body gopay.BodyMap) (string, error) {
	this.expectMoney(body, "10.01")
	return "https://pay.example/wap", nil
}
func (this *fakeSDK) TradePagePay(_ context.Context, body gopay.BodyMap) (string, error) {
	this.expectMoney(body, "10.01")
	return "https://pay.example/pc", nil
}
func (this *fakeSDK) TradePay(_ context.Context, body gopay.BodyMap) (*alipayv3.TradePayRsp, error) {
	this.expectMoney(body, "10.01")
	return &alipayv3.TradePayRsp{StatusCode: 200, TradeNo: "ALI-1", OutTradeNo: "T-1", TotalAmount: "10.01"}, nil
}
func (this *fakeSDK) TradeCreate(_ context.Context, body gopay.BodyMap) (*alipayv3.TradeCreateRsp, error) {
	return &alipayv3.TradeCreateRsp{StatusCode: 200, TradeNo: "ALI-2", OutTradeNo: body.GetString("out_trade_no")}, nil
}
func (this *fakeSDK) TradeAppPay(_ context.Context, _ gopay.BodyMap) (string, error) {
	return "signed-order", nil
}
func (this *fakeSDK) TradeQuery(_ context.Context, _ gopay.BodyMap) (*alipayv3.TradeQueryRsp, error) {
	return &alipayv3.TradeQueryRsp{StatusCode: 200, TradeNo: "ALI-1", OutTradeNo: "T-1", TradeStatus: "TRADE_SUCCESS", TotalAmount: "10.01"}, nil
}
func (this *fakeSDK) TradeClose(_ context.Context, _ gopay.BodyMap) (*alipayv3.TradeCloseRsp, error) {
	return &alipayv3.TradeCloseRsp{StatusCode: 200}, nil
}
func (this *fakeSDK) TradeRefund(_ context.Context, body gopay.BodyMap) (*alipayv3.TradeRefundRsp, error) {
	this.expectField(body, "refund_amount", "1.01")
	return &alipayv3.TradeRefundRsp{StatusCode: 200, TradeNo: "ALI-1", OutTradeNo: "T-1", FundChange: "Y", RefundFee: "1.01"}, nil
}
func (this *fakeSDK) TradeFastPayRefundQuery(_ context.Context, _ gopay.BodyMap) (*alipayv3.TradeFastPayRefundQueryRsp, error) {
	return &alipayv3.TradeFastPayRefundQueryRsp{StatusCode: 200, TradeNo: "ALI-1", OutTradeNo: "T-1", OutRequestNo: "R-1", RefundAmount: "1.01", RefundStatus: "REFUND_SUCCESS"}, nil
}
func (this *fakeSDK) FundTransUniTransfer(_ context.Context, body gopay.BodyMap) (*alipayv3.FundTransUniTransferRsp, error) {
	this.expectField(body, "trans_amount", "2.00")
	return &alipayv3.FundTransUniTransferRsp{StatusCode: 200, OutBizNo: "X-1", OrderId: "ALI-X-1", Status: "SUCCESS"}, nil
}
func (this *fakeSDK) FundTransCommonQuery(_ context.Context, _ gopay.BodyMap) (*alipayv3.FundTransCommonQueryRsp, error) {
	return &alipayv3.FundTransCommonQueryRsp{StatusCode: 200, OutBizNo: "X-1", OrderId: "ALI-X-1", TransAmount: "2.00", Status: "SUCCESS"}, nil
}
func (this *fakeSDK) DataBillDownloadUrlQuery(_ context.Context, body gopay.BodyMap) (*alipayv3.DataBillDownloadUrlQueryRsp, error) {
	billType := this.billType
	if billType == "" {
		billType = "trade"
	}
	this.expectField(body, "bill_type", billType)
	if this.billDate != "" {
		this.expectField(body, "bill_date", this.billDate)
	}
	return &alipayv3.DataBillDownloadUrlQueryRsp{StatusCode: 200, BillDownloadUrl: "https://download.alipay.test/bill.zip", BillFileCode: "BILL-1"}, nil
}
func (this *fakeSDK) expectMoney(body gopay.BodyMap, expected string) {
	this.expectField(body, "total_amount", expected)
}
func (this *fakeSDK) expectField(body gopay.BodyMap, key, expected string) {
	if body.GetString(key) != expected {
		this.t.Fatalf("%s 金额边界错误：%s", key, body.GetString(key))
	}
}

// TestRegisterAndStrictConfig - 验证显式注册与动态配置未知字段拒绝
func TestRegisterAndStrictConfig(t *testing.T) {
	registry := pay.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := Register(registry); !errors.Is(err, pay.ErrDuplicateProvider) {
		t.Fatalf("重复注册应失败：%v", err)
	}
	if _, err := registry.OpenRaw(context.Background(), "alipay", []byte(`{"unknown":true}`)); !errors.Is(err, pay.ErrInvalidConfig) {
		t.Fatalf("未知配置字段应拒绝：%v", err)
	}
}

// TestStatusMappings - 验证支付宝网关状态映射保持资源语义
func TestStatusMappings(t *testing.T) {
	if tradeStatus("TRADE_SUCCESS") != pay.TradeStatusSucceeded || tradeStatus("NEW_VALUE") != pay.TradeStatusUnknown {
		t.Fatal("交易状态映射错误")
	}
	if refundStatus("REFUND_SUCCESS") != pay.RefundStatusSucceeded || transferStatus("DEALING") != pay.TransferStatusProcessing {
		t.Fatal("退款或转账状态映射错误")
	}
}

// TestReasonMapping - 验证错误码到标准分类的映射与网关错误 Reason 填充
func TestReasonMapping(t *testing.T) {
	cases := map[string]pay.Reason{
		"ACQ.TRADE_NOT_EXIST":            pay.ReasonOrderNotFound,
		"ACQ.TRADE_HAS_SUCCESS":          pay.ReasonDuplicateRequest,
		"ACQ.REFUND_AMT_NOT_EQUAL_TOTAL": pay.ReasonAmountMismatch,
		"UNKNOWN_CODE":                   pay.ReasonNone,
	}
	for code, expected := range cases {
		if reasonFor(code) != expected {
			t.Fatalf("错误码 %s 映射错误：%s", code, reasonFor(code))
		}
	}
	err := checkAlipayResponse(200, alipayv3.ErrResponse{Code: "ACQ.TRADE_NOT_EXIST", Message: "订单不存在"})
	var gateway *pay.GatewayError
	if !errors.As(err, &gateway) || gateway.Reason != pay.ReasonOrderNotFound {
		t.Fatalf("checkAlipayResponse 未填充 Reason：%v", err)
	}
	if pay.ReasonOf(checkAlipayResponse(200, alipayv3.ErrResponse{Code: "NOPE"})) != pay.ReasonNone {
		t.Fatal("未知码应返回 ReasonNone")
	}
}

// TestOfflineOperations - 用 fake SDK 验证支付宝主要资金操作与整数金额边界
func TestOfflineOperations(t *testing.T) {
	provider := &Provider{client: &fakeSDK{t: t}, options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}}}
	create := pay.NewTradeCreateRequest("T-1", pay.TradeModeQR, "商品", pay.NewMoneyMinor(1001, "CNY"))
	create.NotifyURL = "https://merchant.test/notify"
	result, err := provider.CreateTrade(context.Background(), create)
	if err != nil || result.Action == nil || result.Action.QRCode == nil {
		t.Fatalf("QR 下单失败：%+v %v", result, err)
	}
	for _, mode := range []pay.TradeMode{pay.TradeModeWAP, pay.TradeModePC, pay.TradeModeApp} {
		create.Mode = mode
		result, err = provider.CreateTrade(context.Background(), create)
		if err != nil || result.Action == nil {
			t.Fatalf("模式 %s 下单失败：%v", mode, err)
		}
	}
	create.Mode, create.AuthCode = pay.TradeModeBarcode, "auth"
	result, err = provider.CreateTrade(context.Background(), create)
	if err != nil || result.Status != pay.TradeStatusSucceeded {
		t.Fatalf("条码支付失败：%+v %v", result, err)
	}
	query, err := provider.QueryTrade(context.Background(), pay.NewTradeQueryRequest("T-1"))
	if err != nil || query.Status != pay.TradeStatusSucceeded || query.ChargedAmount.Minor != 1001 {
		t.Fatalf("查单失败：%+v %v", query, err)
	}
	refund := pay.NewRefundRequest("T-1", "R-1", pay.NewMoneyMinor(1001, "CNY"), pay.NewMoneyMinor(101, "CNY"))
	refundResult, err := provider.Refund(context.Background(), refund)
	if err != nil || refundResult.Status != pay.RefundStatusSucceeded {
		t.Fatalf("退款失败：%+v %v", refundResult, err)
	}
	extensions, _ := pay.SetExtension(nil, "alipay", RefundQueryOptions{OutTradeNo: "T-1"})
	refundQuery := pay.NewRefundQueryRequest("R-1")
	refundQuery.Extensions = extensions
	refundResult, err = provider.QueryRefund(context.Background(), refundQuery)
	if err != nil || refundResult.Amount.Minor != 101 {
		t.Fatalf("退款查询失败：%+v %v", refundResult, err)
	}
	transfer := pay.NewTransferRequest("X-1", pay.NewMoneyMinor(200, "CNY"), pay.Payee{Account: "user@example.com", Type: pay.PayeeTypeLoginID})
	transfer.IdempotencyKey, transfer.Subject = "idem", "转账"
	transferResult, err := provider.Transfer(context.Background(), transfer)
	if err != nil || transferResult.Status != pay.TransferStatusSucceeded {
		t.Fatalf("转账失败：%+v %v", transferResult, err)
	}
	if err = provider.CloseTrade(context.Background(), pay.NewTradeCloseRequest("T-1")); err != nil {
		t.Fatalf("关单失败：%v", err)
	}
}

// TestFetchBill - 验证账单类型映射、月账单限制、代下载与截断语义
func TestFetchBill(t *testing.T) {
	provider := &Provider{
		client: &fakeSDK{t: t},
		options: pay.OpenOptions{
			RawCapture:    pay.RawCapturePolicy{Mode: pay.RawCaptureNone},
			Client:        &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF })},
			BillMaxBytes:  64,
		},
	}
	result, err := provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01"})
	if err != nil || result.DownloadURL != "https://download.alipay.test/bill.zip" || result.FileName != "BILL-1" {
		t.Fatalf("日账单申请失败：%+v %v", result, err)
	}
	if result.Content != nil {
		t.Fatal("Fetch=false 不应代下载")
	}
	if _, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08", Type: pay.BillTypeFundFlow}); !errors.Is(err, pay.ErrInvalidRequest) {
		t.Fatalf("月账单仅限交易类型：%v", err)
	}
}

// TestFetchBillDownload - 验证代下载命中截断与普通下载语义
func TestFetchBillDownload(t *testing.T) {
	provider := &Provider{
		client: &fakeSDK{t: t},
		options: pay.OpenOptions{
			RawCapture:   pay.RawCapturePolicy{Mode: pay.RawCaptureNone},
			Client:       &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), Request: request}, nil })},
			BillMaxBytes: 64,
		},
	}
	result, err := provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Fetch: true})
	if err != nil || !result.Truncated || len(result.Content) != 64 {
		t.Fatalf("超上限应截断：%+v %v", result, err)
	}
	small := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) { return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("zip-data")), Request: request}, nil })}
	provider.options.Client = small
	result, err = provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Fetch: true})
	if err != nil || result.Truncated || string(result.Content) != "zip-data" {
		t.Fatalf("未超限应完整返回：%+v %v", result, err)
	}
}

// TestFetchBillTypeMapping - 验证 bill_type/bill_date 映射：trade（含月账单）与 signcustomer 原样上送
func TestFetchBillTypeMapping(t *testing.T) {
	provider := &Provider{
		client:  &fakeSDK{t: t, billType: "trade", billDate: "2026-08"},
		options: pay.OpenOptions{RawCapture: pay.RawCapturePolicy{Mode: pay.RawCaptureNone}, BillMaxBytes: 64},
	}
	if _, err := provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08"}); err != nil {
		t.Fatalf("月交易账单应合法：%v", err)
	}
	provider.client = &fakeSDK{t: t, billType: "signcustomer", billDate: "2026-08-01"}
	if _, err := provider.FetchBill(context.Background(), pay.BillRequest{Date: "2026-08-01", Type: pay.BillTypeFundFlow}); err != nil {
		t.Fatalf("资金账单映射失败：%v", err)
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

func (this *orderNotFoundSDK) TradeQuery(_ context.Context, _ gopay.BodyMap) (*alipayv3.TradeQueryRsp, error) {
	return &alipayv3.TradeQueryRsp{StatusCode: 200, ErrResponse: alipayv3.ErrResponse{Code: "ACQ.TRADE_NOT_EXIST", Message: "交易不存在"}}, nil
}

// TestParseNotifyBodyWithFundBillList - 回归：携带 fund_bill_list / voucher_detail_list
// 的真实 TRADE_SUCCESS 回调必须被接受。支付宝将这两个字段以 URL 编码的 JSON 字符串下发，
// 若反序列化到 gopay legacy.NotifyRequest（字段为切片）会报错，导致成功回调被拒。
func TestParseNotifyBodyWithFundBillList(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	provider := &Provider{
		config: Config{AppID: "2026100000000000"},
		options: pay.OpenOptions{
			Clock:            fixedClock{now},
			NotifyClockSkew:  5 * time.Minute,
			RawCapture:       pay.RawCapturePolicy{Mode: pay.RawCaptureNone},
		},
	}
	body := gopay.BodyMap{
		"app_id":              "2026100000000000",
		"out_trade_no":        "T-1001",
		"trade_no":            "2026081122000000000000000001",
		"trade_status":        "TRADE_SUCCESS",
		"total_amount":        "10.01",
		"notify_id":           "R-1001",
		"notify_time":         "2026-08-11 12:00:05",
		"gmt_payment":         "2026-08-11 12:00:05",
		"fund_bill_list":      `[{"amount":"10.01","fundChannel":"ALIPAYACCOUNT"}]`,
		"voucher_detail_list": `[{"amount":"1.00","merchantContribute":"1.00"}]`,
		"sign":                "upstream-verified",
	}
	request := pay.NotifyRequest{
		Kind:   pay.NotifyKindTrade,
		Method: http.MethodPost,
		Body:   []byte("app_id=2026100000000000&trade_status=TRADE_SUCCESS&fund_bill_list=%5B%7B%22amount%22%3A%2210.01%22%7D%5D"),
	}
	event, err := provider.parseNotifyBody(request, body)
	if err != nil {
		t.Fatalf("携带 fund_bill_list 的成功回调必须被接受：%v", err)
	}
	if event.Type != pay.EventTradeSucceeded || event.Trade == nil {
		t.Fatalf("事件类型不符：%+v", event)
	}
	if event.Trade.OutTradeNo != "T-1001" || event.Trade.GatewayTradeNo != "2026081122000000000000000001" || event.Trade.Amount.Minor != 1001 {
		t.Fatalf("交易事件不符：%+v", event.Trade)
	}
	// 支付宝 notify_id 每次推送都不同，但同一业务号 + 事件类型的去重键必须稳定
	body["notify_id"] = "R-2002"
	repeated, err := provider.parseNotifyBody(request, body)
	if err != nil || repeated.ID == event.ID {
		t.Fatalf("重复通知解析异常：%+v %v", repeated, err)
	}
	if repeated.Trade.OutTradeNo != event.Trade.OutTradeNo || repeated.Type != event.Type {
		t.Fatalf("重复通知业务字段应一致：%+v vs %+v", repeated, event)
	}
}

// TestParseNotifyBodyRejectsBadFields - 缺少必要字段或金额非法的回调必须被拒绝
func TestParseNotifyBodyRejectsBadFields(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	provider := &Provider{
		config: Config{AppID: "2026100000000000"},
		options: pay.OpenOptions{
			Clock:           fixedClock{now},
			NotifyClockSkew: 5 * time.Minute,
			RawCapture:      pay.RawCapturePolicy{Mode: pay.RawCaptureNone},
		},
	}
	base := gopay.BodyMap{
		"app_id":        "2026100000000000",
		"out_trade_no":  "T-1001",
		"trade_no":      "2026081122000000000000000001",
		"trade_status":  "TRADE_SUCCESS",
		"total_amount":  "10.01",
		"notify_time":   "2026-08-11 12:00:05",
		"gmt_payment":   "2026-08-11 12:00:05",
		"fund_bill_list": `[{"amount":"10.01"}]`,
	}
	request := pay.NotifyRequest{Kind: pay.NotifyKindTrade, Method: http.MethodPost}

	cases := []struct {
		name   string
		mutate func(gopay.BodyMap)
	}{
		{name: "app_id 不匹配", mutate: func(b gopay.BodyMap) { b["app_id"] = "other" }},
		{name: "缺少 out_trade_no", mutate: func(b gopay.BodyMap) { b["out_trade_no"] = "" }},
		{name: "缺少 trade_status", mutate: func(b gopay.BodyMap) { b["trade_status"] = "" }},
		{name: "金额非法", mutate: func(b gopay.BodyMap) { b["total_amount"] = "not-a-number" }},
		{name: "通知时间超差", mutate: func(b gopay.BodyMap) { b["notify_time"] = "2026-08-11 11:00:00" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := gopay.BodyMap{}
			for k, v := range base {
				body[k] = v
			}
			tc.mutate(body)
			if _, err := provider.parseNotifyBody(request, body); !errors.Is(err, pay.ErrVerifyFailed) {
				t.Fatalf("非法通知必须拒绝：%v", err)
			}
		})
	}
}

// TestParseNotifyDedupeKey - 用真实 RSA2 签名 fixture 走完整验签链路，
// 验证 Driver 为支付宝通知统一派生 DedupeKey，且同一载荷重复投递键相同
func TestParseNotifyDedupeKey(t *testing.T) {
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// 自签证书充当支付宝公钥证书：验签只提取证书中的 RSA 公钥
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "alipay-test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour)}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	provider := &Provider{
		config: Config{AppID: "2026100000000000", AlipayPublicCert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))},
		options: pay.OpenOptions{
			Clock:           fixedClock{now},
			NotifyClockSkew: 5 * time.Minute,
			RawCapture:      pay.RawCapturePolicy{Mode: pay.RawCaptureNone},
		},
	}
	registry := pay.NewRegistry()
	_ = registry.Register("alipay", func(context.Context, pay.ConfigInput, pay.OpenOptions) (pay.Provider, error) { return provider, nil })
	driver, err := registry.New(context.Background(), "alipay", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	params := gopay.BodyMap{
		"app_id":       "2026100000000000",
		"out_trade_no": "T-1001",
		"trade_no":     "2026081122000000000000000001",
		"trade_status": "TRADE_SUCCESS",
		"total_amount": "10.01",
		"notify_id":    "R-1001",
		"notify_time":  "2026-08-11 12:00:05",
		"gmt_payment":  "2026-08-11 12:00:05",
	}
	digest := sha256.Sum256([]byte(params.EncodeAliPaySignParams()))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{}
	for key, value := range params {
		values.Set(key, cast.ToString(value))
	}
	values.Set("sign", base64.StdEncoding.EncodeToString(signature))
	values.Set("sign_type", "RSA2")
	request := pay.NotifyRequest{Kind: pay.NotifyKindTrade, Method: http.MethodPost, Headers: http.Header{}, Body: []byte(values.Encode())}
	event, err := driver.ParseNotify(context.Background(), request)
	if err != nil {
		t.Fatalf("真实签名的通知应通过验签：%v", err)
	}
	if event.DedupeKey != "T-1001|trade.succeeded" {
		t.Fatalf("Driver 未派生去重键：%s", event.DedupeKey)
	}
	again, err := driver.ParseNotify(context.Background(), request)
	if err != nil || again.DedupeKey != event.DedupeKey {
		t.Fatalf("同一载荷重复解析键应相同：%s vs %s", again.DedupeKey, event.DedupeKey)
	}
}
