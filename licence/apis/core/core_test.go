package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// ============================= 信封解析（唯一解析点） =============================

// TestParseEnvelope - 运行时信封 {code,msg,data,detail} 的四种形态：
// 成功（数字 0）/ 成功（缺 code）/ 业务失败（字符串业务码 + detail）/ 非法形态（数字非 0）
func TestParseEnvelope(t *testing.T) {

	cases := []struct {
		name       string
		raw        string
		wantData   string
		wantCode   string
		wantStatus int
		wantErr    bool
	}{
		{name: "成功-code为0", raw: `{"code":0,"msg":"ok","data":{"result":{"ip":"1.1.1.1"}}}`, wantData: `{"result":{"ip":"1.1.1.1"}}`},
		{name: "成功-缺code", raw: `{"msg":"ok","data":{"records":[]}}`, wantData: `{"records":[]}`},
		{
			name: "业务失败-额度耗尽", raw: `{"code":"QUOTA_EXCEEDED","msg":"订阅额度已耗尽","detail":{"scope":"month","resetAt":"2026-10-01T00:00:00+08:00"}}`,
			wantCode: "QUOTA_EXCEEDED", wantStatus: http.StatusForbidden,
		},
		{name: "业务失败-模糊404认证", raw: `{"code":"NOT_FOUND","msg":"许可证或实例信息无效"}`, wantCode: "NOT_FOUND", wantStatus: http.StatusNotFound},
		{name: "非法形态-数字非0", raw: `{"code":200,"msg":"ok"}`, wantErr: true},
		{name: "非法形态-非JSON", raw: `<html>502 Bad Gateway</html>`, wantErr: true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			data, err := ParseEnvelope([]byte(item.raw))
			if item.wantErr {
				if err == nil {
					t.Fatalf("非法信封应返回错误，实际 data=%s", string(data))
				}
				var apiErr *Error
				if errors.As(err, &apiErr) {
					t.Fatalf("信封形态错误不得伪装成业务码：%+v", apiErr)
				}
				return
			}
			if item.wantCode == "" {
				if err != nil {
					t.Fatalf("成功信封不应报错：%v", err)
				}
				if string(data) != item.wantData {
					t.Fatalf("data 原文不符：%s（期望 %s）", string(data), item.wantData)
				}
				return
			}
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("业务失败应返回 *apis.Error，实际 %v", err)
			}
			if apiErr.Code != item.wantCode || apiErr.Message == "" || apiErr.HTTPStatus != item.wantStatus {
				t.Fatalf("业务错误字段不符：%+v（期望 code=%s status=%d）", apiErr, item.wantCode, item.wantStatus)
			}
		})
	}
}

// TestParseEnvelopeDetail - 失败信封 detail 原样进入 Error.Detail（键名与平台口径一致）
func TestParseEnvelopeDetail(t *testing.T) {

	_, err := ParseEnvelope([]byte(`{"code":"RATE_LIMITED","msg":"速率超限","detail":{"retryAfterMs":2000}}`))
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *apis.Error，实际 %v", err)
	}
	if fmt.Sprint(apiErr.Detail["retryAfterMs"]) != "2000" {
		t.Fatalf("detail.retryAfterMs 不符：%+v", apiErr.Detail)
	}
	if !strings.Contains(apiErr.Error(), "RATE_LIMITED") || !strings.Contains(apiErr.Error(), "速率超限") {
		t.Fatalf("Error() 文本应含业务码与提示：%s", apiErr.Error())
	}
}

// TestHTTPStatusByCode - 业务码 → HTTP 等价状态码映射表（licen-hub 04 §2.4 单一事实源）：
// 17 码逐码断言 + 未登记码回落 500，防止 SDK 侧映射与契约漂移
func TestHTTPStatusByCode(t *testing.T) {

	cases := map[string]int{
		ErrorCodeInvalidArgument:     http.StatusBadRequest,
		ErrorCodeUnauthorized:        http.StatusUnauthorized,
		ErrorCodeInsufficientBalance: http.StatusPaymentRequired,
		ErrorCodeForbidden:           http.StatusForbidden,
		ErrorCodeNoEntitlement:       http.StatusForbidden,
		ErrorCodeQuotaExceeded:       http.StatusForbidden,
		ErrorCodeAccountFrozen:       http.StatusForbidden,
		ErrorCodeIPNotFound:          http.StatusNotFound,
		ErrorCodeCapabilityNotFound:  http.StatusNotFound,
		ErrorCodeNotFound:            http.StatusNotFound,
		ErrorCodeConflict:            http.StatusConflict,
		ErrorCodeInvalidState:        http.StatusConflict,
		ErrorCodeRateLimited:         http.StatusTooManyRequests,
		ErrorCodeConcurrencyLimited:  http.StatusTooManyRequests,
		ErrorCodeSpendLimitExceeded:  http.StatusTooManyRequests,
		ErrorCodeUpstreamError:       http.StatusBadGateway,
		ErrorCodeInternal:            http.StatusInternalServerError,
	}
	if len(cases) != 17 {
		t.Fatalf("契约业务码应为 17 码，当前断言表 %d 码", len(cases))
	}
	for code, want := range cases {
		if got := HTTPStatusByCode(code); got != want {
			t.Errorf("HTTPStatusByCode(%s) = %d，期望 %d", code, got, want)
		}
	}
	if got := HTTPStatusByCode("NOT_REGISTERED_CODE"); got != http.StatusInternalServerError {
		t.Errorf("未登记业务码应回落 500，实际 %d", got)
	}
}

// ============================= 幂等键 =============================

// TestResolveRequestID - 幂等键求解：显式值优先（去空白），为空生成 req_ + 32 位无横线 UUID v4
func TestResolveRequestID(t *testing.T) {

	if got := ResolveRequestID(" req_custom "); got != "req_custom" {
		t.Fatalf("显式幂等键应去空白后原样使用，实际 %q", got)
	}
	if got := ResolveRequestID("", "req_second"); got != "req_second" {
		t.Fatalf("空值应跳过取下一个候选，实际 %q", got)
	}
	pattern := regexp.MustCompile(`^req_[0-9a-f]{32}$`)
	first := ResolveRequestID()
	if !pattern.MatchString(first) {
		t.Fatalf("自动生成的幂等键应为 req_ + 32 位无横线 UUID，实际 %q", first)
	}
	// 版本位/变体位：UUID v4 形态（与服务端兜底同构）
	// hex 编码下 byte 6 → 下标 4+12=16（版本位 '4'），byte 8 → 下标 4+16=20（变体位 8/9/a/b）
	if first[16] != '4' || !strings.ContainsAny(first[20:21], "89ab") {
		t.Fatalf("自动生成的幂等键应符合 UUID v4 位形态，实际 %q", first)
	}
	if second := ResolveRequestID(); second == first {
		t.Fatalf("两次生成不应相同：%q", second)
	}
}

// TestReceiptFieldAlignment - 计量回执 8 字段与 04 §2.3 信封 data.receipt 逐字对齐
func TestReceiptFieldAlignment(t *testing.T) {

	encoded, err := json.Marshal(Receipt{})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 8 {
		t.Fatalf("Receipt 应为 8 字段，实际 %d：%s", len(fields), string(encoded))
	}
}
