package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ============================= 流式类型 =============================

// ChunkChoice - 流式补全选项（delta 增量语义）
type ChunkChoice struct {
	// Index - 选项序号
	Index int `json:"index"`
	// Delta - 增量消息（首帧常只带 role，后续帧带 content 片段）
	Delta Message `json:"delta"`
	// FinishReason - 结束原因（仅末帧非空）
	FinishReason string `json:"finish_reason"`
}

// ChatChunk - 流式补全帧（OpenAI chat.completion.chunk）
type ChatChunk struct {
	// Id - 上游补全 id
	Id string `json:"id"`
	// Object - 对象类型（chat.completion.chunk）
	Object string `json:"object"`
	// Created - 创建时间（秒级时间戳）
	Created int64 `json:"created"`
	// Model - 实际出参模型
	Model string `json:"model"`
	// Choices - 增量选项
	Choices []ChunkChoice `json:"choices"`
	// Usage - token 用量（仅末帧可能携带，取决于上游与 stream_options.include_usage；缺失时服务端按字符估算兜底计费）
	Usage *Usage `json:"usage,omitempty"`
}

// ============================= Stream（SSE 迭代器） =============================

// Stream - 流式响应迭代器。Recv 逐帧返回增量；[DONE] 收尾转 io.EOF；
// 帧内 JSON 解析失败跳过坏帧不中断（usage 末帧缺失由服务端估算兜底）；
// ctx 取消即断开底层连接（断流已产出部分照扣，见设计文档 §3.4）。
// 使用完毕必须 Close（Recv 到 io.EOF/出错时已自动 Close）。
type Stream struct {
	// body - 底层响应体
	body io.ReadCloser
	// reader - 行读取器
	reader *bufio.Reader
	// requestId - X-Request-Id 响应头回显（调用级幂等键，售后对账用）
	requestId string
	// done - 流已终结（[DONE] / 出错 / Close）
	done bool
}

// newStream - 装配流式迭代器（仅 ChatStream 成功路径调用）
func newStream(resp *http.Response) *Stream {

	return &Stream{
		body:      resp.Body,
		reader:    bufio.NewReaderSize(resp.Body, 64*1024),
		requestId: resp.Header.Get("X-Request-Id"),
	}
}

// RequestId - X-Request-Id 响应头回显（调用级幂等键）
func (this *Stream) RequestId() string {

	return this.requestId
}

// Recv - 读取下一帧。流正常结束返回 io.EOF；连接在未见 [DONE] 时断开返回
// io.ErrUnexpectedEOF；ctx 取消/网络故障返回原始错误。
/**
 * @return ChatChunk - 增量帧
 * @return error - io.EOF = 正常收尾；io.ErrUnexpectedEOF = 上游截断；其余为传输错误
 * @example：
 * 	stream, err := client.ChatStream(ctx, req)
 * 	if err != nil { … }
 * 	defer stream.Close()
 * 	for {
 * 		chunk, err := stream.Recv()
 * 		if errors.Is(err, io.EOF) { break }
 * 		if err != nil { … }
 * 		fmt.Print(chunk.Choices[0].Delta.Content)
 * 	}
 */
func (this *Stream) Recv() (ChatChunk, error) {

	for {
		if this.done {
			return ChatChunk{}, io.EOF
		}
		line, readErr := this.reader.ReadString('\n')
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if payload, isData := strings.CutPrefix(trimmed, "data:"); isData {
				payload = strings.TrimSpace(payload)
				if payload == "[DONE]" {
					this.done = true
					_ = this.Close()
					return ChatChunk{}, io.EOF
				}
				var chunk ChatChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
					return chunk, nil
				}
				// 坏帧跳过不中断流
			}
			// 非 data 行（SSE 注释 `:`、event:/id:/retry: 字段）一律忽略
		}
		if readErr != nil {
			this.done = true
			_ = this.Close()
			if readErr == io.EOF {
				// 未见 [DONE] 的截断（上游异常断流）
				return ChatChunk{}, io.ErrUnexpectedEOF
			}
			return ChatChunk{}, readErr
		}
	}
}

// Close - 关闭底层连接（幂等；Recv 终结后无需再调）
func (this *Stream) Close() error {

	if this.done && this.body == nil {
		return nil
	}
	this.done = true
	body := this.body
	this.body = nil
	if body == nil {
		return nil
	}
	return body.Close()
}

// ============================= ChatStream（流式） =============================

// ChatStream - 对话补全（流式，强制 stream=true）。返回的 Stream 用完必须 Close。
/**
 * @param ctx context.Context - 调用上下文（取消即断流，已产出部分照扣）
 * @param input ChatRequest - 对话请求（Stream 字段被强制为 true）
 * @param requestId ...string - 可选调用级幂等键
 * @return *Stream - 流式迭代器
 * @return error - 建连阶段失败为 *Error（同 Chat）或网络错误
 */
func (this *Client) ChatStream(ctx context.Context, input ChatRequest, requestId ...string) (*Stream, error) {

	input.Stream = true // 语义标记（json:"-" 不进序列化，流式标志由下方显式注入）
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	// 显式并入 "stream":true（Extra 中的 stream 键已被 typed 冲突集过滤，方法有最终决定权）
	body, err = injectStreamFlag(body)
	if err != nil {
		return nil, err
	}
	resp, err := this.do(ctx, http.MethodPost, "/chat/completions", body, variadicRequestId(requestId))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp)
	}
	return newStream(resp), nil
}

// injectStreamFlag - 顶层并入 "stream":true（MarshalJSON 之后的注入点）
func injectStreamFlag(body []byte) ([]byte, error) {

	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, err
	}
	top["stream"] = json.RawMessage("true")
	return json.Marshal(top)
}
