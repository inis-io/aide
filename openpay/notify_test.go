package openpay

import (
	"errors"
	"strings"
	"testing"
)

// ============================= Webhook 测试 - 开始 =============================

// TestParseNotifyGolden - Webhook 报文验签通过并正确解析（签名值为服务端实测黄金向量）
func TestParseNotifyGolden(t *testing.T) {

	payload, err := ParseNotify([]byte(goldenNotifyBody), testSecret)
	if err != nil {
		t.Fatalf("验签失败：%v", err)
	}

	if payload.TradeNo != "202608151200000001" || payload.OutTradeNo != "OUT20260815001" {
		t.Fatalf("单号解析失败：%+v", payload)
	}
	if payload.GatewayTradeNo != "ALIPAY2026081500001" || payload.MchId != testMchId {
		t.Fatalf("网关单号/商户号解析失败：%+v", payload)
	}
	if payload.Amount != 12.3 || payload.Currency != "CNY" {
		t.Fatalf("金额/货币解析失败：%+v", payload)
	}
	if payload.Scene != "alipay" || payload.Way != "url" || payload.Status != "paid" {
		t.Fatalf("场景/方式/状态解析失败：%+v", payload)
	}
	if payload.CreateAt != 1755244800000 || payload.UpdateAt != 1755244860000 {
		t.Fatalf("毫秒时间戳解析失败：%+v", payload)
	}
	if payload.Sign != goldenNotifySign {
		t.Fatalf("签名解析失败：%s", payload.Sign)
	}

	if !VerifyNotify([]byte(goldenNotifyBody), testSecret) {
		t.Fatalf("VerifyNotify 应返回 true")
	}
}

// TestParseNotifyTampered - 篡改任一字段后验签失败
func TestParseNotifyTampered(t *testing.T) {

	cases := map[string]string{
		"篡改金额":  strings.Replace(goldenNotifyBody, `"amount":12.3`, `"amount":13.3`, 1),
		"篡改状态":  strings.Replace(goldenNotifyBody, `"status":"paid"`, `"status":"fail"`, 1),
		"篡改时间戳": strings.Replace(goldenNotifyBody, `"createAt":1755244800000`, `"createAt":1755244800001`, 1),
		"追加字段":  strings.Replace(goldenNotifyBody, `{"tradeNo"`, `{"extra":"x","tradeNo"`, 1),
	}

	for name, body := range cases {
		if body == goldenNotifyBody {
			t.Fatalf("%s：测试数据未发生变化", name)
		}
		if _, err := ParseNotify([]byte(body), testSecret); !errors.Is(err, ErrInvalidSign) {
			t.Fatalf("%s：应返回 ErrInvalidSign，实际：%v", name, err)
		}
		if VerifyNotify([]byte(body), testSecret) {
			t.Fatalf("%s：VerifyNotify 应返回 false", name)
		}
	}
}

// TestParseNotifyMissingSign - 缺少 sign 字段直接判失败（无跳过开关）
func TestParseNotifyMissingSign(t *testing.T) {

	body := strings.Replace(goldenNotifyBody, `,"sign":"`+goldenNotifySign+`"`, "", 1)

	_, err := ParseNotify([]byte(body), testSecret)
	if !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("应返回 ErrInvalidSign，实际：%v", err)
	}
	if !strings.Contains(err.Error(), "缺少 sign") {
		t.Fatalf("错误文案不符合预期：%v", err)
	}
}

// TestParseNotifyInvalidJSON - 非法报文返回 ErrInvalidRequest
func TestParseNotifyInvalidJSON(t *testing.T) {

	if _, err := ParseNotify([]byte("not-json"), testSecret); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("应返回 ErrInvalidRequest，实际：%v", err)
	}
	if VerifyNotify([]byte("not-json"), testSecret) {
		t.Fatalf("非法报文 VerifyNotify 应返回 false")
	}
}

// TestParseNotifyWrongSecret - 错误密钥验签失败
func TestParseNotifyWrongSecret(t *testing.T) {

	if _, err := ParseNotify([]byte(goldenNotifyBody), testSecret+"-wrong"); !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("应返回 ErrInvalidSign，实际：%v", err)
	}
}

// ============================= Webhook 测试 - 结束 =============================
