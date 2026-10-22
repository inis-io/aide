package types

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
