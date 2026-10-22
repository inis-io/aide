package openpay

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
)

// ============================= 客户端 - 开始 =============================

const (
	// defaultTimeout - 单次调用的整体超时
	defaultTimeout = 10 * time.Second
	// defaultUserAgent - 默认请求 User-Agent
	defaultUserAgent = "openpay-go/0.1.0"
)

// Client - inis-pay 开放平台商户客户端
// 通过 NewClient 构造；零值不可用。构造后无内部可变状态，可并发使用；
// 使用完毕应 defer client.Close() 释放 gRPC 连接。
type Client struct {
	// endpoint - 平台地址（如 https://pay.example.com、http://127.0.0.1:2000）
	endpoint string
	// mchId - 商户号（形如 M1000001）
	mchId string
	// secret - 商户密钥（仅参与本地签名，不持久化、不写日志、不进错误文案）
	secret string
	// signer - 签名器（默认 md5Signer，两条链路共用）
	signer Signer
	// transport - 传输链路（默认 gRPC，WithTransportHTTP 切换）
	transport Transport
	// timeout - 单次调用整体超时
	timeout time.Duration
	// userAgent - 请求 User-Agent
	userAgent string
	// transportHTTP - 是否使用 HTTP 链路（默认 false → gRPC）
	transportHTTP bool
	// httpClient - HTTP 链路自定义客户端（nil 时按 timeout 内置）
	httpClient *http.Client
	// dialOpts - gRPC 拨号选项（TLS 凭证等，WithGRPCDialOpts 累积）
	dialOpts []grpc.DialOption
}

// Option - Client 构造选项（直接作用于接收者）
type Option func(*Client)

// WithEndpoint - 平台地址（必填，无默认）；gRPC 链路自动剥离 scheme 与路径作为拨号目标
/**
 * @param addr string - 平台地址，如 https://pay.example.com
 */
func WithEndpoint(addr string) Option {

	return func(this *Client) {
		this.endpoint = addr
	}
}

// WithTransportHTTP - 切换为 HTTP 链路（默认 gRPC）
func WithTransportHTTP() Option {

	return func(this *Client) {
		this.transportHTTP = true
	}
}

// WithGRPCDialOpts - 透传 gRPC 拨号选项（TLS 凭证、拦截器、保活等）
// 未传入任何选项时使用 plaintext（insecure）拨号，对齐平台 HTTP/gRPC 同端口分流现状；
// 只要传入选项，TLS 凭证由调用方自行负责（如 grpc.WithTransportCredentials(credentials.NewTLS(...))）。
/**
 * @param opts ...grpc.DialOption - 拨号选项
 */
func WithGRPCDialOpts(opts ...grpc.DialOption) Option {

	return func(this *Client) {
		this.dialOpts = append(this.dialOpts, opts...)
	}
}

// WithHTTPClient - 注入自定义 HTTP 客户端（连接池/代理/重试/熔断交给它）
// 注入后 WithTimeout 不再约束 HTTP 客户端，但仍作为单次调用的上下文超时。
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

// WithTimeout - 单次调用的整体超时（默认 10 秒）
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

// WithUserAgent - 请求 User-Agent（默认 openpay-go/0.1.0）
/**
 * @param userAgent string - User-Agent 字面量
 */
func WithUserAgent(userAgent string) Option {

	return func(this *Client) {
		if strings.TrimSpace(userAgent) != "" {
			this.userAgent = strings.TrimSpace(userAgent)
		}
	}
}

// WithSigner - 注入自定义签名器（演进 seam，默认 MD5）
// 平台签名协议升级时按此注入新实现，业务代码零改动。
/**
 * @param signer Signer - 签名器实现（nil 忽略）
 */
func WithSigner(signer Signer) Option {

	return func(this *Client) {
		if signer != nil {
			this.signer = signer
		}
	}
}

// NewClient - 创建商户客户端
// 只完成参数校验与链路装配（gRPC 为惰性连接），不发起任何业务请求。
/**
 * @param mchId string - 商户号
 * @param secret string - 商户密钥（运行时注入，勿硬编码）
 * @param opts ...Option - 构造选项（WithEndpoint 必填）
 * @return *Client - 客户端实例
 * @return error - 参数缺失或链路装配失败（包装 ErrInvalidRequest / ErrTransport）
 * @example：
 * 	client, err := openpay.NewClient(mchId, secret,
 * 	    openpay.WithEndpoint("https://pay.example.com"),
 * 	    openpay.WithTransportHTTP(),
 * 	)
 * 	if err != nil { return err }
 * 	defer client.Close()
 */
func NewClient(mchId, secret string, opts ...Option) (*Client, error) {

	this := &Client{
		mchId:     strings.TrimSpace(mchId),
		secret:    secret,
		signer:    &md5Signer{},
		timeout:   defaultTimeout,
		userAgent: defaultUserAgent,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(this)
		}
	}

	this.endpoint = strings.TrimRight(strings.TrimSpace(this.endpoint), "/")

	if this.endpoint == "" {
		return nil, fmt.Errorf("%w：endpoint 不能为空（使用 WithEndpoint 指定平台地址）", ErrInvalidRequest)
	}
	if this.mchId == "" {
		return nil, fmt.Errorf("%w：mchId 不能为空", ErrInvalidRequest)
	}
	if this.secret == "" {
		return nil, fmt.Errorf("%w：secret 不能为空", ErrInvalidRequest)
	}

	if this.transportHTTP {
		this.transport = newHTTPTransport(this)
		return this, nil
	}

	transport, err := newGRPCTransport(this)
	if err != nil {
		return nil, err
	}
	this.transport = transport

	return this, nil
}

// Close - 释放底层连接（可重复调用）
func (this *Client) Close() error {

	if this == nil || this.transport == nil {
		return nil
	}

	return this.transport.Close()
}

// invoke - 统一调用入口：补齐上下文与整体超时后转交传输链路
func (this *Client) invoke(ctx context.Context, op Operation, req, resp any) error {

	if this == nil || this.transport == nil {
		return fmt.Errorf("%w：客户端未初始化，请使用 NewClient 构造", ErrInvalidRequest)
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if this.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, this.timeout)
		defer cancel()
	}

	return this.transport.Invoke(ctx, op, req, resp)
}

// ============================= 客户端 - 结束 =============================
