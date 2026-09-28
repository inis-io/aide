package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ============================= 错误码（唯一事实源：licen-hub llm-runtime.go writeServiceError 映射表） =============================

// 错误码常量：与服务端 OpenAI 原生错误体 `{"error":{message,type,code}}` 的 code 逐字一致
// （码表见 licen-hub docs/plan/apis/10 §3.3）。上游渠道 4xx 业务错误原样透传时，
// code/type 可能取不到（非平台格式），此时原文摘录在 Error.RawError。
const (
	// ErrorCodeInvalidApiKey - key 缺失/错误/已吊销/已过期（HTTP 401）
	ErrorCodeInvalidApiKey = "invalid_api_key"
	// ErrorCodeInvalidRequest - 请求体非法（非 JSON、缺 model 等；HTTP 400）
	ErrorCodeInvalidRequest = "invalid_request_error"
	// ErrorCodeModelNotFound - 模型不存在/已下架/不在 key 白名单（HTTP 404）
	ErrorCodeModelNotFound = "model_not_found"
	// ErrorCodeModelNotPriced - 模型未定价，平台 fail-closed（HTTP 403）
	ErrorCodeModelNotPriced = "model_not_priced"
	// ErrorCodeForbidden - 无订阅/免费额度且无按量定价，或越权（HTTP 403）
	ErrorCodeForbidden = "forbidden"
	// ErrorCodeInsufficientBalance - 钱包余额不足，预扣失败（HTTP 402；充值/调额后重试）
	ErrorCodeInsufficientBalance = "insufficient_balance"
	// ErrorCodeQuotaExceeded - 订阅周期 token 额度耗尽或月度消费限额触顶（HTTP 429）
	ErrorCodeQuotaExceeded = "quota_exceeded"
	// ErrorCodeRateLimited - 并发/QPS 闸门（HTTP 429；按调用方自身节奏退避重试）
	ErrorCodeRateLimited = "rate_limited"
	// ErrorCodeModelUnavailable - 该模型暂无可用渠道（HTTP 503）
	ErrorCodeModelUnavailable = "model_unavailable"
	// ErrorCodeUpstreamError - 全部渠道候选失败（HTTP 503；可稍后重试）
	ErrorCodeUpstreamError = "upstream_error"
	// ErrorCodeServerError - 平台故障（HTTP 500）
	ErrorCodeServerError = "server_error"
)

// rawErrorLimit - 非平台格式错误体的原文摘录上限（字符数）
const rawErrorLimit = 512

// ============================= typed 错误 =============================

// Error - LLM 网关调用错误（非 2xx 一律归一为本类型，errors.As 断言）。
type Error struct {
	// HTTPStatus - 原始 HTTP 状态码
	HTTPStatus int
	// Type - error.type（authentication_error / invalid_request_error / rate_limit_exceeded / forbidden / api_error）
	Type string
	// Code - error.code（取值见 ErrorCode* 常量）
	Code string
	// Message - 提示文本
	Message string
	// RequestId - X-Request-Id 响应头回显（售后对账凭它定位调用明细）
	RequestId string
	// RawError - 上游 4xx 透传体非平台错误格式时的原文摘录（截断 512 字符；平台格式为空）
	RawError string
}

// Error - 实现 error 接口（nil 接收者安全，避免日志格式化时 panic）
func (this *Error) Error() string {

	if this == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("llm: HTTP %d", this.HTTPStatus))
	if this.Code != "" {
		builder.WriteString(" " + this.Code)
	}
	if this.Message != "" {
		builder.WriteString("：" + this.Message)
	}
	if this.RequestId != "" {
		builder.WriteString("（request id: " + this.RequestId + "）")
	}
	return builder.String()
}

// IsRetryable - 是否值得重试：仅 upstream_error / server_error / model_unavailable 为 true。
// quota_exceeded / insufficient_balance 重试无意义；rate_limited 由调用方按自身业务节奏退避；
// 本包不做自动重试（服务端已做渠道 failover）。
func (this *Error) IsRetryable() bool {

	if this == nil {
		return false
	}
	switch this.Code {
	case ErrorCodeUpstreamError, ErrorCodeServerError, ErrorCodeModelUnavailable:
		return true
	}
	return false
}

// ============================= 错误归一 =============================

// errorEnvelope - 平台错误信封（OpenAI 原生格式，与服务端 writeError 逐字对应）
type errorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// parseError - 非 2xx 响应归一为 *Error：平台格式取 error 三件套；非平台格式
// （上游 4xx 透传体、网关 HTML 错误页等）保留原文摘录到 RawError。调用后响应体已关闭。
func parseError(resp *http.Response) error {

	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	result := &Error{HTTPStatus: resp.StatusCode, RequestId: resp.Header.Get("X-Request-Id")}
	var envelope errorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil &&
		(envelope.Error.Message != "" || envelope.Error.Code != "" || envelope.Error.Type != "") {
		result.Message = envelope.Error.Message
		result.Type = envelope.Error.Type
		result.Code = envelope.Error.Code
		return result
	}
	result.RawError = truncateText(string(body), rawErrorLimit)
	result.Message = fmt.Sprintf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	return result
}

// truncateText - 按字符截断（rune 安全，不超 limit 个字符）
func truncateText(text string, limit int) string {

	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
