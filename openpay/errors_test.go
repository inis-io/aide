package openpay

import (
	"errors"
	"testing"
)

// TestAPIError - 业务错误结构与 nil 接收者安全
func TestAPIError(t *testing.T) {

	apiErr := &APIError{Code: 400, Msg: "签名验证失败！", Raw: []byte(`{"code":400}`)}
	if got := apiErr.Error(); got != "openpay: 平台业务错误（code=400）：签名验证失败！" {
		t.Fatalf("错误文案不符合预期：%s", got)
	}

	var nilError *APIError
	if got := nilError.Error(); got == "" {
		t.Fatalf("nil 接收者不应返回空文案")
	}

	// errors.As 可提取（商户统一判定范式）
	var target *APIError
	if !errors.As(error(apiErr), &target) || target.Code != 400 {
		t.Fatalf("errors.As 提取失败")
	}
}

// TestSentinelErrors - 哨兵错误文案与独立性
func TestSentinelErrors(t *testing.T) {

	if ErrInvalidRequest.Error() != "openpay: 请求参数无效" {
		t.Fatalf("ErrInvalidRequest 文案不符合预期：%s", ErrInvalidRequest)
	}
	if ErrInvalidSign.Error() != "openpay: 签名验证失败" {
		t.Fatalf("ErrInvalidSign 文案不符合预期：%s", ErrInvalidSign)
	}
	if ErrTransport.Error() != "openpay: 传输层调用失败" {
		t.Fatalf("ErrTransport 文案不符合预期：%s", ErrTransport)
	}

	if errors.Is(ErrInvalidRequest, ErrInvalidSign) || errors.Is(ErrInvalidSign, ErrTransport) {
		t.Fatalf("哨兵错误应彼此独立")
	}
}

// TestDecodeEnvelopeNilData - 成功但无 data 时保留零值
func TestDecodeEnvelopeNilData(t *testing.T) {

	resp := &Trade{}
	if err := decodeEnvelope([]byte(`{"code":200,"msg":"OK","data":null}`), resp); err != nil {
		t.Fatalf("data 为 null 时不应报错：%v", err)
	}
	if resp.TradeNo != "" {
		t.Fatalf("data 为 null 时应保留零值")
	}

	if err := decodeEnvelope([]byte(`{"code":200,"msg":"OK"}`), resp); err != nil {
		t.Fatalf("缺 data 时不应报错：%v", err)
	}
}

// TestDecodeEnvelopeAPIError - code!=200 归一为 *APIError 且保留原始报文
func TestDecodeEnvelopeAPIError(t *testing.T) {

	raw := []byte(`{"code":409,"msg":"订单已支付！","data":null}`)

	var apiErr *APIError
	if !errors.As(decodeEnvelope(raw, &Trade{}), &apiErr) {
		t.Fatalf("应返回 *APIError")
	}
	if apiErr.Code != 409 || apiErr.Msg != "订单已支付！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
	if string(apiErr.Raw) != string(raw) {
		t.Fatalf("原始报文未保留")
	}
}

// TestDecodeEnvelopeInvalidJSON - 非法 JSON 返回普通错误（协议违例，不是业务错误）
func TestDecodeEnvelopeInvalidJSON(t *testing.T) {

	err := decodeEnvelope([]byte("<html>502 Bad Gateway</html>"), &Trade{})
	if err == nil {
		t.Fatalf("非法 JSON 应返回错误")
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("非法 JSON 不应返回 *APIError")
	}
	if errors.Is(err, ErrTransport) {
		t.Fatalf("非法 JSON 按设计应返回原始错误而非 ErrTransport")
	}
}
