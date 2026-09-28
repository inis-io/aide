package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewClientNormalize - 构造归一：baseURL 末尾 / 裁剪、空 baseURL/空 apiKey 构造期 panic
func TestNewClientNormalize(t *testing.T) {

	client := NewClient("https://hub.example.com/v1/", "sk-x")
	if client.baseURL != "https://hub.example.com/v1" {
		t.Fatalf("baseURL 末尾 / 应被裁剪，实际：%s", client.baseURL)
	}
	if client.httpClient == nil || client.httpClient.Timeout != defaultTimeout {
		t.Fatalf("缺省客户端应带默认超时 %v", defaultTimeout)
	}
	for _, pair := range [][2]string{{"", "sk-x"}, {"https://hub.example.com/v1", ""}, {"  ", "sk-x"}} {
		func(baseURL, apiKey string) {
			defer func() {
				if recover() == nil {
					t.Fatalf("baseURL=%q apiKey=%q 应 panic", baseURL, apiKey)
				}
			}()
			NewClient(baseURL, apiKey)
		}(pair[0], pair[1])
	}
}

// TestClientHeaders - 请求头：强制 Bearer + JSON 头，幂等键单次调用优先于客户端默认，自定义头不得覆盖凭证
func TestClientHeaders(t *testing.T) {

	var gotAuth, gotRequestId, gotContentType, gotCustom string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotRequestId = req.Header.Get("X-Request-Id")
		gotContentType = req.Header.Get("Content-Type")
		gotCustom = req.Header.Get("X-Custom")
		writer.Header().Set("X-Request-Id", "req_echo")
		writer.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/v1", "sk-abc",
		WithRequestID("req_default"), WithHeader("X-Custom", "1"), WithHeader("Authorization", "Bearer evil"))
	if _, err := client.Models(context.Background()); err != nil {
		t.Fatalf("Models 调用失败：%v", err)
	}
	if gotAuth != "Bearer sk-abc" {
		t.Fatalf("Authorization 应强制为 sk-key（WithHeader 不可覆盖），实际：%s", gotAuth)
	}
	if gotRequestId != "req_default" {
		t.Fatalf("应携带客户端默认幂等键，实际：%s", gotRequestId)
	}
	if gotCustom != "1" {
		t.Fatalf("自定义头应透传，实际：%s", gotCustom)
	}
	// GET 无 body 不带 Content-Type；POST 带 body 必须有
	if gotContentType != "" {
		t.Fatalf("GET 不应带 Content-Type，实际：%s", gotContentType)
	}

	server2 := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		gotRequestId = req.Header.Get("X-Request-Id")
		gotContentType = req.Header.Get("Content-Type")
		writer.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[]}`))
	}))
	defer server2.Close()
	client2 := NewClient(server2.URL, "sk-abc", WithRequestID("req_default"))
	if _, err := client2.Chat(context.Background(), ChatRequest{Model: "m"}, "req_once"); err != nil {
		t.Fatalf("Chat 调用失败：%v", err)
	}
	if gotRequestId != "req_once" {
		t.Fatalf("单次调用幂等键应优先于默认值，实际：%s", gotRequestId)
	}
	if gotContentType != "application/json" {
		t.Fatalf("POST 应带 JSON Content-Type，实际：%s", gotContentType)
	}
}

// TestWithTimeoutAndHTTPClient - WithTimeout 只作用于内置客户端；注入自定义客户端后失效
func TestWithTimeoutAndHTTPClient(t *testing.T) {

	client := NewClient("https://hub.example.com/v1", "sk-x", WithTimeout(5*time.Second))
	if client.httpClient.Timeout != 5*time.Second {
		t.Fatalf("WithTimeout 应生效，实际：%v", client.httpClient.Timeout)
	}
	custom := &http.Client{Timeout: 9 * time.Second}
	client = NewClient("https://hub.example.com/v1", "sk-x", WithHTTPClient(custom), WithTimeout(5*time.Second))
	if client.httpClient != custom {
		t.Fatalf("注入自定义客户端后不应被替换")
	}
	if custom.Timeout != 9*time.Second {
		t.Fatalf("自定义客户端超时不得被改写，实际：%v", custom.Timeout)
	}
}

// readAll - 读取请求体原文（测试助手）
func readAll(t *testing.T, req *http.Request) string {

	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("请求体读取失败：%v", err)
	}
	return strings.TrimSpace(string(body))
}
