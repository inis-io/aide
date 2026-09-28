package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ============================= 请求类型 =============================

// Message - 对话消息（OpenAI 兼容）。ToolCalls/ToolCallID/Name 为工具回合可选字段，
// 原样透传不建模（工具调用回合的结构以上游厂商文档为准）。
type Message struct {
	// Role - 角色（system / user / assistant / tool）
	Role string `json:"role"`
	// Content - 文本内容（多模态消息请用 Extra 自行组装 messages）
	Content string `json:"content"`
	// Name - 可选参与者名称
	Name string `json:"name,omitempty"`
	// ToolCallID - tool 角色消息对应的工具调用 id
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls - assistant 消息的工具调用列表（原样透传，不建模）
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// ChatRequest - 对话补全请求（OpenAI 兼容）。
// Extra 用于厂商扩展字段（reasoning_effort、stream_options 等），序列化时并入顶层；
// 键与 typed 字段冲突时 typed 优先（防覆盖 model 等关键字段）。
type ChatRequest struct {
	// Model - 模型 id（先 GET /v1/models 对清单；未上架/未定价会被服务端拒绝）
	Model string `json:"model"`
	// Messages - 对话消息列表
	Messages []Message `json:"messages"`
	// MaxTokens - 最大输出 token（0 = 不限制）
	MaxTokens int `json:"max_tokens,omitempty"`
	// Temperature - 采样温度（nil = 上游默认）
	Temperature *float64 `json:"temperature,omitempty"`
	// Tools - 工具定义（原样透传，不建模）
	Tools json.RawMessage `json:"tools,omitempty"`
	// Stream - 是否流式（由调用方法强制：Chat 恒 false，ChatStream 恒 true；手动设置无效）
	Stream bool `json:"-"`
	// Extra - 厂商扩展字段（序列化时并入顶层，typed 字段优先）
	Extra map[string]any `json:"-"`
}

// chatTypedFields - ChatRequest typed 字段的 JSON 键全集（Extra 合并冲突判定的唯一依据）。
// stream 虽为 json:"-" 也在其中：Extra 永不可设置 stream，流式与否由 Chat/ChatStream 方法强制。
var chatTypedFields = map[string]struct{}{
	"model": {}, "messages": {}, "max_tokens": {}, "temperature": {}, "tools": {}, "stream": {},
}

// MarshalJSON - 定制序列化：typed 字段 + Extra 合并（Extra 键与 typed 字段冲突时 typed 优先）。
func (this ChatRequest) MarshalJSON() ([]byte, error) {

	type chatRequestAlias ChatRequest
	raw, err := json.Marshal(chatRequestAlias(this))
	if err != nil {
		return nil, err
	}
	if len(this.Extra) == 0 {
		return raw, nil
	}
	merged := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	for key, value := range this.Extra {
		if _, typed := chatTypedFields[key]; typed {
			continue
		}
		item, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("llm: Extra 字段 %q 序列化失败：%w", key, err)
		}
		merged[key] = item
	}
	return json.Marshal(merged)
}

// ============================= 响应类型 =============================

// Usage - token 用量（分项与平台计费口径一致：缓存命中/推理分项缺失时服务端分别按输入价/输出价计）
type Usage struct {
	// PromptTokens - 输入 token 数
	PromptTokens int `json:"prompt_tokens"`
	// CompletionTokens - 输出 token 数
	CompletionTokens int `json:"completion_tokens"`
	// TotalTokens - 总 token 数
	TotalTokens int `json:"total_tokens"`
	// PromptTokensDetails - 输入分项（缓存命中等；上游未返回为 nil）
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details,omitempty"`
	// CompletionTokensDetails - 输出分项（推理 token 等；上游未返回为 nil）
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details,omitempty"`
}

// Choice - 非流式补全选项
type Choice struct {
	// Index - 选项序号
	Index int `json:"index"`
	// Message - assistant 回复消息
	Message Message `json:"message"`
	// FinishReason - 结束原因（stop / length / tool_calls …；可能为空）
	FinishReason string `json:"finish_reason"`
}

// ChatResponse - 非流式对话补全响应（上游响应体原样透传后的 typed 视图）
type ChatResponse struct {
	// Id - 上游补全 id
	Id string `json:"id"`
	// Object - 对象类型（chat.completion）
	Object string `json:"object"`
	// Created - 创建时间（秒级时间戳）
	Created int64 `json:"created"`
	// Model - 实际出参模型
	Model string `json:"model"`
	// Choices - 回复选项
	Choices []Choice `json:"choices"`
	// Usage - token 用量（上游缺失时为 nil，服务端按字符估算兜底计费）
	Usage *Usage `json:"usage,omitempty"`
	// RequestId - X-Request-Id 响应头回显（调用级幂等键，售后对账用；非上游响应体字段）
	RequestId string `json:"-"`
}

// ============================= Chat（非流式） =============================

// Chat - 对话补全（非流式，强制 stream=false）。
/**
 * @param ctx context.Context - 调用上下文
 * @param input ChatRequest - 对话请求（Extra 可携带厂商扩展字段）
 * @param requestId ...string - 可选调用级幂等键（缺省用客户端默认值，再缺省不携带由服务端生成）
 * @return *ChatResponse - 补全响应（RequestId 取自 X-Request-Id 响应头）
 * @return error - 非 2xx 为 *Error（errors.As 断言，IsRetryable 判定是否值得重试）
 * @example：
 * 	resp, err := client.Chat(ctx, llm.ChatRequest{
 * 		Model: "deepseek-chat",
 * 		Messages: []llm.Message{{Role: "user", Content: "你好"}},
 * 	})
 * 	if err == nil && len(resp.Choices) > 0 {
 * 		fmt.Println(resp.Choices[0].Message.Content)
 * 	}
 */
func (this *Client) Chat(ctx context.Context, input ChatRequest, requestId ...string) (*ChatResponse, error) {

	input.Stream = false
	body, err := json.Marshal(input)
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
	defer resp.Body.Close()
	var result ChatResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("llm: 响应解析失败：%w", err)
	}
	result.RequestId = resp.Header.Get("X-Request-Id")
	return &result, nil
}
