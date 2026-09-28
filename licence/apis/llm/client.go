package llm

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"
)

// ============================= Client =============================

// defaultTimeout - 缺省整体超时（长流式输出场景用 WithTimeout 调大）
const defaultTimeout = 120 * time.Second

// Client - LLM 网关客户端（sk-key Bearer 认证，OpenAI 兼容协议）。
// 通过 NewClient 构造；零值不可用。
type Client struct {
	// baseURL - 网关地址（已归一：末尾 `/` 裁剪；须含 /v1 前缀，本包不自动补）
	baseURL string
	// apiKey - 商城 API 密钥（sk- 前缀）
	apiKey string
	// httpClient - 调用方注入的 HTTP 客户端（nil = 使用内置默认客户端）
	httpClient *http.Client
	// timeout - 内置默认客户端的整体超时（注入 httpClient 后不生效）
	timeout time.Duration
	// header - 自定义追加头（WithHeader 累积；Authorization / Content-Type / X-Request-Id 由本包强制覆盖）
	header http.Header
	// requestId - 默认调用级幂等键（WithRequestID；空 = 不携带，由服务端生成 req_ 前缀键并回显响应头）
	requestId string
}

// Option - Client 构造选项
type Option func(*Client)

// WithHTTPClient - 注入自定义 HTTP 客户端（连接池/代理/重试/熔断交给它）。
// 注入后 WithTimeout 不再生效（超时由注入的客户端自行控制）。
/**
 * @param client *http.Client - 自定义 HTTP 客户端（nil 忽略）
 */
func WithHTTPClient(client *http.Client) Option {

	return func(this *Client) {
		if client != nil {
			this.httpClient = client
		}
	}
}

// WithTimeout - 内置默认客户端的整体超时（默认 120 秒；长流式输出场景可调大）。
// 仅在未注入自定义 http.Client 时生效。
/**
 * @param timeout time.Duration - 超时时间（<= 0 忽略）
 */
func WithTimeout(timeout time.Duration) Option {

	return func(this *Client) {
		if timeout > 0 {
			this.timeout = timeout
		}
	}
}

// WithHeader - 追加自定义请求头（可多次调用；同名后者覆盖前者）。
// Authorization / Content-Type / X-Request-Id 由本包管理，此处设置会被强制覆盖。
/**
 * @param key string - 头名
 * @param value string - 头值
 */
func WithHeader(key, value string) Option {

	return func(this *Client) {
		this.header.Set(key, value)
	}
}

// WithRequestID - 默认调用级幂等键（重试必须复用同一值；单次调用可用方法的
// requestId 参数覆盖）。缺省不携带，服务端会生成 req_ 前缀键并回显 X-Request-Id 响应头。
/**
 * @param requestId string - 幂等键
 */
func WithRequestID(requestId string) Option {

	return func(this *Client) {
		this.requestId = strings.TrimSpace(requestId)
	}
}

// NewClient - 创建 LLM 网关客户端。
/**
 * @param baseURL string - 网关地址（须含 /v1 前缀，如 https://hub.example.com/v1；末尾 / 自动裁剪）
 * @param apiKey string - 商城 API 密钥（sk- 前缀，平台后台「我的密钥」页签发）
 * @param opts ...Option - 可选配置
 * @return *Client - 网关客户端
 * @example：
 * 	client := llm.NewClient("https://hub.example.com/v1", "sk-xxxx")
 * 	resp, err := client.Chat(ctx, llm.ChatRequest{Model: "deepseek-chat", Messages: …})
 */
func NewClient(baseURL, apiKey string, opts ...Option) *Client {

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		panic("llm: baseURL 不能为空（须含 /v1 前缀，如 https://hub.example.com/v1）")
	}
	if apiKey == "" {
		panic("llm: apiKey 不能为空（商城 sk- 前缀密钥）")
	}
	this := &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		timeout: defaultTimeout,
		header:  http.Header{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(this)
		}
	}
	if this.httpClient == nil {
		this.httpClient = &http.Client{Timeout: this.timeout}
	}
	return this
}

// ============================= 内部请求助手 =============================

// do - 发起一次 HTTP 请求：强制 Bearer 认证与 JSON 头，叠加自定义头与幂等键。
// 响应体归属调用方（成功路径由调用方 Close；错误归一经 parseError 关闭）。
func (this *Client) do(ctx context.Context, method, path string, body []byte, requestId string) (*http.Response, error) {

	req, err := http.NewRequestWithContext(ctx, method, this.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// 自定义头先铺底，认证/协议头强制覆盖（防 WithHeader 误伤凭证链路）
	for key, values := range this.header {
		req.Header[key] = values
	}
	req.Header.Set("Authorization", "Bearer "+this.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if id := firstNonEmpty(requestId, this.requestId); id != "" {
		req.Header.Set("X-Request-Id", id)
	}
	return this.httpClient.Do(req)
}

// firstNonEmpty - 取第一个去空白后的非空串（单次调用幂等键优先于客户端默认值）
func firstNonEmpty(values ...string) string {

	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// variadicRequestId - 变参幂等键归一（方法签名 requestId ...string 的统一取值点）
func variadicRequestId(requestId []string) string {

	return firstNonEmpty(requestId...)
}
