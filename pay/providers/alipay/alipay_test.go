package alipay

import (
	"context"
	"errors"
	"testing"

	"github.com/go-pay/gopay"
	alipayv3 "github.com/go-pay/gopay/alipay/v3"

	"github.com/inis-io/aide/pay"
)

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
