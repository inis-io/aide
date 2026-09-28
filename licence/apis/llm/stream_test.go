package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseOKBody - 流式正常响应：注释行 + 多 data 帧 + 坏帧 + [DONE]（\r\n 与 \n 混用）
const sseOKBody = ": ping\r\n" +
	"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你\"},\"finish_reason\":null}]}\r\n" +
	"\r\n" +
	"event: message\n" +
	"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"好\"},\"finish_reason\":null}]}\n" +
	"data: {bad json 坏帧\n" +
	"data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n" +
	"data: [DONE]\n"

// TestStreamRecv - SSE 解析：注释/事件行忽略、坏帧跳过、[DONE] 转 io.EOF、usage 末帧可取
func TestStreamRecv(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("X-Request-Id", "req_stream")
		writer.Write([]byte(sseOKBody))
	}))
	defer server.Close()

	stream, err := NewClient(server.URL, "sk-abc").ChatStream(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream 失败：%v", err)
	}
	if stream.RequestId() != "req_stream" {
		t.Fatalf("Stream 应回显 RequestId，实际：%q", stream.RequestId())
	}
	var text strings.Builder
	var usage *Usage
	finish := ""
	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("Recv 意外错误：%v", recvErr)
		}
		for _, choice := range chunk.Choices {
			text.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if text.String() != "你好" {
		t.Fatalf("增量拼接不符：%q", text.String())
	}
	if finish != "stop" {
		t.Fatalf("finish_reason 应为 stop，实际：%q", finish)
	}
	if usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("usage 末帧应可取，实际：%+v", usage)
	}
	// 终结后再 Recv 恒 io.EOF；Close 幂等
	if _, recvErr := stream.Recv(); !errors.Is(recvErr, io.EOF) {
		t.Fatalf("终结后 Recv 应为 io.EOF，实际：%v", recvErr)
	}
	if err = stream.Close(); err != nil {
		t.Fatalf("Close 应幂等无错：%v", err)
	}
}

// TestStreamRequestBody - 流式请求体强制 "stream":true（调用方 Extra 偷渡 stream:false 被覆盖）
func TestStreamRequestBody(t *testing.T) {

	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		gotBody = readAll(t, req)
		writer.Write([]byte("data: [DONE]\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "sk-abc")
	stream, err := client.ChatStream(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Extra:    map[string]any{"stream": false},
	})
	if err != nil {
		t.Fatalf("ChatStream 失败：%v", err)
	}
	defer stream.Close()
	if !strings.Contains(gotBody, `"stream":true`) {
		t.Fatalf("请求体应强制 stream:true，实际：%s", gotBody)
	}
}

// TestStreamUnexpectedEOF - 未见 [DONE] 的连接截断 → io.ErrUnexpectedEOF
func TestStreamUnexpectedEOF(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.Write([]byte("data: {\"id\":\"c1\",\"choices\":[]}\n"))
		// 不写 [DONE] 直接结束
	}))
	defer server.Close()

	stream, err := NewClient(server.URL, "sk-abc").ChatStream(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream 失败：%v", err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatalf("首帧应正常：%v", err)
	}
	if _, err = stream.Recv(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("截断应为 io.ErrUnexpectedEOF，实际：%v", err)
	}
}

// TestStreamContextCancel - ctx 取消即断流（Recv 返回非 EOF 错误）
func TestStreamContextCancel(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.Write([]byte("data: {\"id\":\"c1\",\"choices\":[]}\n"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-req.Context().Done() // 挂起直到客户端断开
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewClient(server.URL, "sk-abc").ChatStream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ChatStream 失败：%v", err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatalf("首帧应正常：%v", err)
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err = stream.Recv()
		if err == nil {
			if time.Now().After(deadline) {
				t.Fatalf("ctx 取消后 Recv 迟迟不返回错误")
			}
			continue
		}
		if errors.Is(err, io.EOF) {
			t.Fatalf("ctx 取消不应伪装成正常收尾")
		}
		break
	}
}

// TestStreamErrorBeforeHeaders - 建连阶段的非 2xx（如余额不足）走统一错误归一，不返回 Stream
func TestStreamErrorBeforeHeaders(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.WriteHeader(http.StatusPaymentRequired)
		writer.Write([]byte(`{"error":{"message":"余额不足","type":"invalid_request_error","code":"insufficient_balance"}}`))
	}))
	defer server.Close()

	stream, err := NewClient(server.URL, "sk-abc").ChatStream(context.Background(), ChatRequest{Model: "m"})
	if stream != nil {
		t.Fatalf("失败路径不得返回 Stream")
	}
	llmErr, ok := err.(*Error)
	if !ok || llmErr.Code != ErrorCodeInsufficientBalance || llmErr.HTTPStatus != 402 {
		t.Fatalf("错误映射不符：%+v", err)
	}
}
