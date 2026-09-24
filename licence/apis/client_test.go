package apis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// ============================= 假宿主 =============================

// fakeDoer - 记录调用的假宿主（断言请求构造、幂等键透传与错误归一）
type fakeDoer struct {
	// calls - 调用次数
	calls int
	// method/path/body/requestId - 最近一次调用的请求参数
	method    string
	path      string
	body      []byte
	requestId string
	// response - 罐头响应（业务信封 JSON 原文）
	response []byte
	// err - 非空时直接返回该错误（模拟网络/传输失败）
	err error
}

func (this *fakeDoer) Do(ctx context.Context, method string, path string, body []byte, requestId string) ([]byte, error) {

	this.calls++
	this.method, this.path, this.body, this.requestId = method, path, body, requestId
	if this.err != nil {
		return nil, this.err
	}
	return this.response, nil
}

// TestNewAssemblesDoer - New 仅装配宿主调用能力，不发起网络请求
func TestNewAssemblesDoer(t *testing.T) {

	doer := &fakeDoer{}
	client := New(doer)
	if client == nil || client.doer != doer {
		t.Fatalf("New 应原样装配 doer")
	}
	if doer.calls != 0 {
		t.Fatalf("New 不得发起调用，实际 %d 次", doer.calls)
	}
}

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
			data, err := parseEnvelope([]byte(item.raw))
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

// TestParseEnvelopeDetail - 失败信封 detail 原样进入 apis.Error.Detail（键名与平台口径一致）
func TestParseEnvelopeDetail(t *testing.T) {

	_, err := parseEnvelope([]byte(`{"code":"RATE_LIMITED","msg":"速率超限","detail":{"retryAfterMs":2000}}`))
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

	if got := resolveRequestID(" req_custom "); got != "req_custom" {
		t.Fatalf("显式幂等键应去空白后原样使用，实际 %q", got)
	}
	if got := resolveRequestID("", "req_second"); got != "req_second" {
		t.Fatalf("空值应跳过取下一个候选，实际 %q", got)
	}
	pattern := regexp.MustCompile(`^req_[0-9a-f]{32}$`)
	first := resolveRequestID()
	if !pattern.MatchString(first) {
		t.Fatalf("自动生成的幂等键应为 req_ + 32 位无横线 UUID，实际 %q", first)
	}
	// 版本位/变体位：UUID v4 形态（与服务端兜底同构）
	// hex 编码下 byte 6 → 下标 4+12=16（版本位 '4'），byte 8 → 下标 4+16=20（变体位 8/9/a/b）
	if first[16] != '4' || !strings.ContainsAny(first[20:21], "89ab") {
		t.Fatalf("自动生成的幂等键应符合 UUID v4 位形态，实际 %q", first)
	}
	if second := resolveRequestID(); second == first {
		t.Fatalf("两次生成不应相同：%q", second)
	}
}

// TestDoNetworkErrorPassthrough - 网络/传输错误原样返回：不伪装业务码（errors.Is 可穿透）
func TestDoNetworkErrorPassthrough(t *testing.T) {

	networkErr := errors.New("dial tcp 127.0.0.1:443: connect: connection refused")
	doer := &fakeDoer{err: networkErr}
	client := New(doer)
	_, _, err := client.IPLocate(context.Background(), "203.0.113.10")
	if !errors.Is(err, networkErr) {
		t.Fatalf("网络错误应原样返回，实际 %v", err)
	}
	var apiErr *Error
	if errors.As(err, &apiErr) {
		t.Fatalf("网络错误不得伪装成业务码：%+v", apiErr)
	}
}

// TestClientResponseShapes - 成功信封 data 缺 result/receipt 时的解析容错：
// 契约内字段缺失不 panic，零值返回（客户按零值判空）
func TestClientResponseShapes(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{}}`)}
	client := New(doer)
	loc, receipt, err := client.IPLocate(context.Background(), "203.0.113.10")
	if err != nil {
		t.Fatalf("空 data 应正常返回：%v", err)
	}
	if loc != (IPLocateResult{}) || receipt != (Receipt{}) {
		t.Fatalf("空 data 应返回零值结果：%+v %+v", loc, receipt)
	}
	if !json.Valid(doer.body) {
		t.Fatalf("请求体应为合法 JSON：%s", string(doer.body))
	}
}
