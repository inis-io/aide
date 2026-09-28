package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chatOKBody - 非流式成功响应（OpenAI 格式 + usage 分项）
const chatOKBody = `{
	"id":"chatcmpl-1","object":"chat.completion","created":1735689600,"model":"deepseek-chat",
	"choices":[{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":10,"completion_tokens":8,"total_tokens":18,
		"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":2}}
}`

// TestChatSuccess - 非流式成功：请求体不含 stream、响应 typed 解析、RequestId 取自响应头
func TestChatSuccess(t *testing.T) {

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" || req.Method != http.MethodPost {
			t.Fatalf("路径/方法不符：%s %s", req.Method, req.URL.Path)
		}
		gotBody = readAll(t, req)
		writer.Header().Set("X-Request-Id", "req_1")
		writer.Write([]byte(chatOKBody))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/v1", "sk-abc")
	resp, err := client.Chat(context.Background(), ChatRequest{
		Model:    "deepseek-chat",
		Messages: []Message{{Role: "user", Content: "你好"}},
	})
	if err != nil {
		t.Fatalf("Chat 失败：%v", err)
	}
	if strings.Contains(gotBody, "stream") {
		t.Fatalf("非流式请求体不得携带 stream 字段：%s", gotBody)
	}
	if resp.RequestId != "req_1" {
		t.Fatalf("RequestId 应取自 X-Request-Id 响应头，实际：%q", resp.RequestId)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "你好" || resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices 解析不符：%+v", resp.Choices)
	}
	if resp.Usage == nil || resp.Usage.PromptTokens != 10 || resp.Usage.TotalTokens != 18 ||
		resp.Usage.PromptTokensDetails == nil || resp.Usage.PromptTokensDetails.CachedTokens != 4 ||
		resp.Usage.CompletionTokensDetails == nil || resp.Usage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Fatalf("usage 分项解析不符：%+v", resp.Usage)
	}
}

// TestChatRequestExtraMerge - Extra 合并：扩展字段并入顶层、typed 优先防覆盖、stream 键被 typed 冲突集过滤
func TestChatRequestExtraMerge(t *testing.T) {

	temperature := 0.7
	raw, err := json.Marshal(ChatRequest{
		Model:       "deepseek-chat",
		Messages:    []Message{{Role: "user", Content: "hi"}},
		MaxTokens:   64,
		Temperature: &temperature,
		Extra: map[string]any{
			"reasoning_effort": "high",
			"model":            "evil-model", // typed 优先：不得覆盖
			"temperature":      9.9,          // typed 优先：不得覆盖
			"stream":           true,         // typed 冲突集：过滤
			"max_tokens":       1,            // typed 优先：不得覆盖
		},
	})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("序列化结果非 JSON 对象：%v", err)
	}
	var model string
	_ = json.Unmarshal(top["model"], &model)
	if model != "deepseek-chat" {
		t.Fatalf("Extra 不得覆盖 typed model，实际：%s", model)
	}
	var temp float64
	_ = json.Unmarshal(top["temperature"], &temp)
	if temp != 0.7 {
		t.Fatalf("Extra 不得覆盖 typed temperature，实际：%v", temp)
	}
	if _, has := top["stream"]; has {
		t.Fatalf("Extra 不得偷渡 stream 键：%s", raw)
	}
	var effort string
	_ = json.Unmarshal(top["reasoning_effort"], &effort)
	if effort != "high" {
		t.Fatalf("Extra 扩展字段应并入顶层，实际：%s", raw)
	}
	var maxTokens int
	_ = json.Unmarshal(top["max_tokens"], &maxTokens)
	if maxTokens != 64 {
		t.Fatalf("Extra 不得覆盖 typed max_tokens，实际：%d", maxTokens)
	}
}

// TestChatErrorMapping - 错误映射：平台错误体归一为 *Error（code/type/requestId 全取到）
func TestChatErrorMapping(t *testing.T) {

	cases := []struct {
		status    int
		body      string
		wantCode  string
		wantType  string
		retryable bool
	}{
		{401, `{"error":{"message":"Incorrect API key provided.","type":"authentication_error","code":"invalid_api_key"}}`, ErrorCodeInvalidApiKey, "authentication_error", false},
		{402, `{"error":{"message":"余额不足","type":"invalid_request_error","code":"insufficient_balance"}}`, ErrorCodeInsufficientBalance, "invalid_request_error", false},
		{404, `{"error":{"message":"模型不存在","type":"invalid_request_error","code":"model_not_found"}}`, ErrorCodeModelNotFound, "invalid_request_error", false},
		{403, `{"error":{"message":"模型未定价","type":"invalid_request_error","code":"model_not_priced"}}`, ErrorCodeModelNotPriced, "invalid_request_error", false},
		{403, `{"error":{"message":"无可用权益","type":"forbidden","code":"forbidden"}}`, ErrorCodeForbidden, "forbidden", false},
		{429, `{"error":{"message":"额度耗尽","type":"invalid_request_error","code":"quota_exceeded"}}`, ErrorCodeQuotaExceeded, "invalid_request_error", false},
		{429, `{"error":{"message":"并发超限","type":"rate_limit_exceeded","code":"rate_limited"}}`, ErrorCodeRateLimited, "rate_limit_exceeded", false},
		{503, `{"error":{"message":"暂无可用渠道","type":"api_error","code":"model_unavailable"}}`, ErrorCodeModelUnavailable, "api_error", true},
		{503, `{"error":{"message":"全部候选失败","type":"api_error","code":"upstream_error"}}`, ErrorCodeUpstreamError, "api_error", true},
		{500, `{"error":{"message":"服务端故障","type":"api_error","code":"server_error"}}`, ErrorCodeServerError, "api_error", true},
		{400, `{"error":{"message":"缺 model","type":"invalid_request_error","code":"invalid_request_error"}}`, ErrorCodeInvalidRequest, "invalid_request_error", false},
	}
	for _, item := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
			writer.Header().Set("X-Request-Id", "req_err")
			writer.WriteHeader(item.status)
			writer.Write([]byte(item.body))
		}))
		_, err := NewClient(server.URL, "sk-abc").Chat(context.Background(), ChatRequest{Model: "m"})
		server.Close()
		if err == nil {
			t.Fatalf("HTTP %d 应返回错误", item.status)
		}
		llmErr, ok := err.(*Error)
		if !ok {
			t.Fatalf("HTTP %d 错误应为 *Error，实际 %T", item.status, err)
		}
		if llmErr.HTTPStatus != item.status || llmErr.Code != item.wantCode || llmErr.Type != item.wantType {
			t.Fatalf("HTTP %d 映射不符：%+v", item.status, llmErr)
		}
		if llmErr.RequestId != "req_err" {
			t.Fatalf("HTTP %d 应回显 RequestId，实际：%q", item.status, llmErr.RequestId)
		}
		if llmErr.IsRetryable() != item.retryable {
			t.Fatalf("HTTP %d（%s）IsRetryable 应为 %v", item.status, item.wantCode, item.retryable)
		}
		if llmErr.RawError != "" {
			t.Fatalf("平台格式错误体不应填 RawError：%q", llmErr.RawError)
		}
		if !strings.Contains(llmErr.Error(), item.wantCode) {
			t.Fatalf("Error() 文本应含 code：%s", llmErr.Error())
		}
	}
}

// TestChatErrorRawBody - 非平台格式错误体（上游 4xx 透传/HTML 错误页）：RawError 保留摘录并截断 512 字符
func TestChatErrorRawBody(t *testing.T) {

	longBody := strings.Repeat("x", 600)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		writer.Write([]byte(longBody))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "sk-abc").Chat(context.Background(), ChatRequest{Model: "m"})
	llmErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("应为 *Error，实际 %T", err)
	}
	if llmErr.Code != "" || llmErr.Type != "" {
		t.Fatalf("非平台格式不应有 code/type：%+v", llmErr)
	}
	if len([]rune(llmErr.RawError)) != 512 {
		t.Fatalf("RawError 应截断 512 字符，实际：%d", len([]rune(llmErr.RawError)))
	}
	if !strings.Contains(llmErr.Message, "400") {
		t.Fatalf("Message 应含 HTTP 状态，实际：%q", llmErr.Message)
	}

	// 上游厂商 JSON 但非平台 error 三件套形态 → 同样走 RawError
	server2 := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		writer.Write([]byte(`{"error_msg":"content filter","error_code":123}`))
	}))
	defer server2.Close()
	_, err = NewClient(server2.URL, "sk-abc").Chat(context.Background(), ChatRequest{Model: "m"})
	llmErr, ok = err.(*Error)
	if !ok || !strings.Contains(llmErr.RawError, "error_msg") {
		t.Fatalf("上游透传体应保留 RawError 摘录：%+v", err)
	}
}

// TestNilErrorSafety - nil *Error 的 Error()/IsRetryable() 不 panic
func TestNilErrorSafety(t *testing.T) {

	var err *Error
	if err.Error() != "" || err.IsRetryable() {
		t.Fatalf("nil Error 应安全返回零值")
	}
}
