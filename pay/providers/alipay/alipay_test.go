package alipay

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-pay/gopay"
	alipayv3 "github.com/go-pay/gopay/alipay/v3"

	"github.com/inis-io/aide/pay"
)

type fixedClock struct{ now time.Time }

func (this fixedClock) Now() time.Time { return this.now }

type fakeSDK struct{ t *testing.T }

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
