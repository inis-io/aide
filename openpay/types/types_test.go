package types

import (
	"errors"
	"testing"
)

// TestTradeCreateRequestNormalize - 下单默认值注入（服务端 default 标签等价）
func TestTradeCreateRequestNormalize(t *testing.T) {

	request := &TradeCreateRequest{Amount: 12.30, ChannelCode: "ALIPAY_PC"}
	request.Normalize()

	if request.Currency != "CNY" {
		t.Fatalf("默认货币未注入：%q", request.Currency)
	}
	if request.Expired != 900 {
		t.Fatalf("默认有效期未注入：%d", request.Expired)
	}

	// 显式值不被覆盖
	request = &TradeCreateRequest{Currency: "USD", Expired: 1800}
	request.Normalize()
	if request.Currency != "USD" || request.Expired != 1800 {
		t.Fatalf("显式值被覆盖：%+v", request)
	}

	// 负值回退默认
	request = &TradeCreateRequest{Expired: -1}
	request.Normalize()
	if request.Expired != 900 {
		t.Fatalf("负值有效期未回退默认：%d", request.Expired)
	}
}

// TestCheckoutRequestNormalize - 收银默认值注入（genUrl 恒为 true）
func TestCheckoutRequestNormalize(t *testing.T) {

	request := &CheckoutRequest{TradeNo: "202608151200000001"}
	request.Normalize()

	if request.Scene != "alipay" || request.Way != "auto" || !request.GenUrl {
		t.Fatalf("收银默认值未注入：%+v", request)
	}

	request = &CheckoutRequest{TradeNo: "202608151200000001", Scene: "wechat", Way: "scan", GenUrl: false}
	request.Normalize()
	if request.Scene != "wechat" || request.Way != "scan" || !request.GenUrl {
		t.Fatalf("收银默认值处理不符合预期：%+v", request)
	}
}

// TestTransferRequestNormalize - 转账默认值注入
func TestTransferRequestNormalize(t *testing.T) {

	request := &TransferRequest{ChannelCode: "ALIPAY_TRANS", Amount: 88.80}
	request.Normalize()

	if request.Currency != "CNY" || request.PayeeType != "ALIPAY_LOGON_ID" {
		t.Fatalf("转账默认值未注入：%+v", request)
	}
}

// TestTradeCreateResultUnmarshalData - HTTP 链路 row → Trade 映射
func TestTradeCreateResultUnmarshalData(t *testing.T) {

	result := &TradeCreateResult{}
	raw := []byte(`{"row":{"tradeNo":"T1","amount":12.3,"status":"created","expireAt":1755245700000},` +
		`"gateways":[{"id":1,"name":"支付宝","scene":"alipay","costRate":0.006}],` +
		`"merchant":{"mchId":"M1000001","name":"演示商户"}}`)

	if err := result.UnmarshalData(raw); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if result.Trade.TradeNo != "T1" || result.Trade.ExpireAt != 1755245700000 {
		t.Fatalf("订单行映射失败：%+v", result.Trade)
	}
	if len(result.Gateways) != 1 || result.Gateways[0].Scene != "alipay" {
		t.Fatalf("网关映射失败：%+v", result.Gateways)
	}
	if result.Merchant.MchId != "M1000001" {
		t.Fatalf("商户映射失败：%+v", result.Merchant)
	}

	// 非法 JSON 归一为 ErrTransport
	if err := result.UnmarshalData([]byte(`{`)); !errors.Is(err, ErrTransport) {
		t.Fatalf("非法 JSON 应包装 ErrTransport，实际：%v", err)
	}
}

// TestTransferResultUnmarshalData - HTTP 链路 row → Trans 映射
func TestTransferResultUnmarshalData(t *testing.T) {

	result := &TransferResult{}
	raw := []byte(`{"row":{"tradeNo":"TT1","payeeAccount":"payee@example.com","status":"pending"},` +
		`"merchant":{"mchId":"M1000001"}}`)

	if err := result.UnmarshalData(raw); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if result.Trans.TradeNo != "TT1" || result.Trans.PayeeAccount != "payee@example.com" {
		t.Fatalf("转账单映射失败：%+v", result.Trans)
	}
	if result.Merchant.MchId != "M1000001" {
		t.Fatalf("商户映射失败：%+v", result.Merchant)
	}

	if err := result.UnmarshalData([]byte(`[]`)); !errors.Is(err, ErrTransport) {
		t.Fatalf("结构不匹配应包装 ErrTransport，实际：%v", err)
	}
}
