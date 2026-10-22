package notify

import (
	"errors"
	"strings"
	"testing"

	"github.com/inis-io/aide/openpay/types"
)

// ============================= 黄金报文 - 开始 =============================
//
// 签名值由平台服务端实现 utils.Md5.GenSign 对该报文实测生成（商户密钥同 sign 包向量），
// 覆盖「金额 12.3 + 毫秒时间戳 1755244800000 不用科学计数法」两处易错点。
const (
	// goldenSecret - 向量专用密钥（禁止使用真实密钥）
	goldenSecret = "test-secret-0123456789abcdef"
	// goldenSign - 报文签名（服务端实测值）
	goldenSign = "6AF54267F9FE7682EE98ACACCB42E517"
	// goldenBody - 与 goldenSign 对应的原始报文
	goldenBody = `{"tradeNo":"202608151200000001","outTradeNo":"OUT20260815001","gatewayTradeNo":"ALIPAY2026081500001","mchId":"M1000001","amount":12.3,"currency":"CNY","scene":"alipay","way":"url","status":"paid","createAt":1755244800000,"updateAt":1755244860000,"sign":"6AF54267F9FE7682EE98ACACCB42E517"}`
)

// ============================= 黄金报文 - 结束 =============================

// TestParseNotifyGolden - 报文验签通过并正确解析
func TestParseNotifyGolden(t *testing.T) {

	payload, err := ParseNotify([]byte(goldenBody), goldenSecret)
	if err != nil {
		t.Fatalf("验签失败：%v", err)
	}

	if payload.TradeNo != "202608151200000001" || payload.OutTradeNo != "OUT20260815001" {
		t.Fatalf("单号解析失败：%+v", payload)
	}
	if payload.GatewayTradeNo != "ALIPAY2026081500001" || payload.MchId != "M1000001" {
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
	if payload.Sign != goldenSign {
		t.Fatalf("签名解析失败：%s", payload.Sign)
	}

	if !VerifyNotify([]byte(goldenBody), goldenSecret) {
		t.Fatalf("VerifyNotify 应返回 true")
	}
}

// TestParseNotifyTampered - 篡改任一字段后验签失败
func TestParseNotifyTampered(t *testing.T) {

	cases := map[string]string{
		"篡改金额":  strings.Replace(goldenBody, `"amount":12.3`, `"amount":13.3`, 1),
		"篡改状态":  strings.Replace(goldenBody, `"status":"paid"`, `"status":"fail"`, 1),
		"篡改时间戳": strings.Replace(goldenBody, `"createAt":1755244800000`, `"createAt":1755244800001`, 1),
		"追加字段":  strings.Replace(goldenBody, `{"tradeNo"`, `{"extra":"x","tradeNo"`, 1),
	}

	for name, body := range cases {
		if body == goldenBody {
			t.Fatalf("%s：测试数据未发生变化", name)
		}
		if _, err := ParseNotify([]byte(body), goldenSecret); !errors.Is(err, types.ErrInvalidSign) {
			t.Fatalf("%s：应返回 ErrInvalidSign，实际：%v", name, err)
		}
		if VerifyNotify([]byte(body), goldenSecret) {
			t.Fatalf("%s：VerifyNotify 应返回 false", name)
		}
	}
}

// TestParseNotifyMissingSign - 缺少 sign 字段直接判失败（无跳过开关）
func TestParseNotifyMissingSign(t *testing.T) {

	body := strings.Replace(goldenBody, `,"sign":"`+goldenSign+`"`, "", 1)

	_, err := ParseNotify([]byte(body), goldenSecret)
	if !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("应返回 ErrInvalidSign，实际：%v", err)
	}
	if !strings.Contains(err.Error(), "缺少 sign") {
		t.Fatalf("错误文案不符合预期：%v", err)
	}
}

// TestParseNotifyInvalidJSON - 非法报文返回 ErrInvalidRequest
func TestParseNotifyInvalidJSON(t *testing.T) {

	if _, err := ParseNotify([]byte("not-json"), goldenSecret); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("应返回 ErrInvalidRequest，实际：%v", err)
	}
	if VerifyNotify([]byte("not-json"), goldenSecret) {
		t.Fatalf("非法报文 VerifyNotify 应返回 false")
	}
}

// TestParseNotifyWrongSecret - 错误密钥验签失败
func TestParseNotifyWrongSecret(t *testing.T) {

	if _, err := ParseNotify([]byte(goldenBody), goldenSecret+"-wrong"); !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("应返回 ErrInvalidSign，实际：%v", err)
	}
}
