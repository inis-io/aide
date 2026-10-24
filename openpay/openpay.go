// Package openpay - inis-pay 开放聚合支付平台的商户 SDK。
//
// 商户通过本包调用平台开放能力：统一下单、商户查单、收银取链接、转账发起/查询、
// 商户通道列表与凭据验证，并在自有服务端处理平台的支付结果异步通知（Webhook）。
//
// 商户只需 import 本包：所有请求/响应模型、签名器、通知解析与错误模型都以
// 类型别名或函数转发的方式从这里导出，内部实现拆分在以下子包：
//
//   - openpay/types：公开数据模型与错误模型（无依赖）
//   - openpay/sign：公开签名器（Signer 接口 + MD5 实现）
//   - openpay/notify：公开 Webhook 报文解析与验签
//   - openpay/internal/transport：内部传输链路（HTTP / gRPC），商户不直接使用
//
// # 双链路
//
// 默认 gRPC（/openpay.merchant.v1.MerchantService/*）；构造时传 WithTransportHTTP()
// 切换到 HTTP 开放端点（/api/trade/unity 等）。业务方法签名、签名器、默认值注入与
// 错误模型两条链路完全一致，业务代码无需感知链路差异：
//
//	client, err := openpay.NewClient(mchId, secret, openpay.WithEndpoint("https://pay.example.com"))
//	// 或：openpay.NewClient(mchId, secret, openpay.WithEndpoint(...), openpay.WithTransportHTTP())
//
// # 签名
//
// 参数集（HTTP 为 JSON 展开的 map，gRPC 为 protojson 展开的 map）→ 剔除 sign 键 →
// 剔除空值 → 键字典序拼 k1=v1&k2=v2 → 追加 &secret=<商户密钥> → MD5 十六进制大写。
// 与平台服务端 utils.Md5.GenSign 逐字节等价（含 float64 的 FormatFloat(v,'f',-1,64)
// 语义与「只剔除字符串形式为空的值」语义）；实现为无副作用纯函数，Client 可并发使用。
//
// # 安全
//
// secret 由调用方运行时注入（环境变量/配置中心），本包不持久化、不写日志、
// 不出现在任何错误文案中。生产环境 HTTP 链路必须走 HTTPS；
// gRPC 链路默认 plaintext（对齐平台同端口分流），TLS 通过 WithGRPCDialOpts 注入凭证。
package openpay

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/inis-io/aide/openpay/internal/transport"
	"github.com/inis-io/aide/openpay/notify"
	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"
)

// ============================= 公开面导出 - 开始 =============================
// 下列别名与转发把分区实现的公开面统一到 openpay 命名空间：商户只 import 本包即可，
// 各子包路径属实现细节（sign/notify 公开是因其类型出现在方法签名上）。

// Signer - 请求签名器（别名 openpay/sign.Signer；需自定义签名协议时配合 WithSigner 使用）
type Signer = sign.Signer

// Trade - 订单（别名 openpay/types.Trade；平台内部字段不下发）
type Trade = types.Trade

// Gateway - 支付网关（商户可见字段子集）
type Gateway = types.Gateway

// MerchantBrief - 商户展示信息（限制字段）
type MerchantBrief = types.MerchantBrief

// TradeCreateRequest - 统一下单请求
type TradeCreateRequest = types.TradeCreateRequest

// TradeCreateResult - 统一下单结果
type TradeCreateResult = types.TradeCreateResult

// TradeQueryRequest - 商户查单请求
type TradeQueryRequest = types.TradeQueryRequest

// CheckoutRequest - 收银请求（取支付链接）
type CheckoutRequest = types.CheckoutRequest

// CheckoutResult - 收银结果（支付链接在 Trade.Url）
type CheckoutResult = types.CheckoutResult

// Trans - 转账单（仅商户可见字段）
type Trans = types.Trans

// TransferRequest - 转账发起请求
type TransferRequest = types.TransferRequest

// TransferResult - 转账发起结果
type TransferResult = types.TransferResult

// TransferQueryRequest - 转账查询请求
type TransferQueryRequest = types.TransferQueryRequest

// Channel - 商户可用的支付/转账通道
type Channel = types.Channel

// NotifyPayload - 支付结果异步通知报文
type NotifyPayload = types.NotifyPayload

// APIError - 平台业务错误（Code 为业务码，Msg 为中文文案，Raw 为原始响应体）
type APIError = types.APIError

// 哨兵错误 - 商户用 errors.Is 判定错误类别，链路差异不外溢
var (
	// ErrInvalidRequest - 请求参数无效（本地校验或链路适配失败，未发出/未按预期发出请求）
	ErrInvalidRequest = types.ErrInvalidRequest
	// ErrInvalidSign - 签名验证失败（本地验签不通过，或平台以 Unauthenticated 拒绝）
	ErrInvalidSign = types.ErrInvalidSign
	// ErrTransport - 传输层调用失败（网络错误、连接断开、RPC 状态非业务失败）
	ErrTransport = types.ErrTransport
)

// 函数转发 - 与 openpay/notify 子包同一实现，方便商户只 import 本包
var (
	// ParseNotify - 解析并验签 Webhook 报文（验签失败返回 ErrInvalidSign）
	ParseNotify = notify.ParseNotify
	// VerifyNotify - 仅验签（报文非法或验签失败均为 false）
	VerifyNotify = notify.VerifyNotify
)

// ============================= 公开面导出 - 结束 =============================

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
	// signer - 签名器（默认 MD5，两条链路共用）
	signer Signer
	// transport - 传输链路（默认 gRPC，WithTransportHTTP 切换）
	transport transport.Transport
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
		signer:    sign.New(),
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

	config := transport.Config{
		Endpoint:   this.endpoint,
		MchId:      this.mchId,
		Secret:     this.secret,
		Signer:     this.signer,
		Timeout:    this.timeout,
		UserAgent:  this.userAgent,
		HTTPClient: this.httpClient,
		DialOpts:   this.dialOpts,
	}

	if this.transportHTTP {
		this.transport = transport.NewHTTP(config)
		return this, nil
	}

	link, err := transport.NewGRPC(config)
	if err != nil {
		return nil, err
	}
	this.transport = link

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
func (this *Client) invoke(ctx context.Context, op transport.Operation, req, resp any) error {

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

// ============================= 交易 - 开始 =============================

// CreateTrade - 统一下单
// 只落库返回订单行，不含支付链接；取链接请再调 GetCheckout。
/**
 * @param ctx context.Context - 调用上下文（超时由客户端整体超时兜底）
 * @param req *TradeCreateRequest - 下单请求
 * @return *TradeCreateResult - 订单行 + 可用网关 + 商户信息
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	res, err := client.CreateTrade(ctx, &openpay.TradeCreateRequest{
 * 	    Amount: 12.30, ChannelCode: "ALIPAY_PC", OutTradeNo: "OUT20260815001",
 * 	})
 */
func (this *Client) CreateTrade(ctx context.Context, req *TradeCreateRequest) (*TradeCreateResult, error) {

	if req == nil {
		return nil, fmt.Errorf("%w：下单请求不能为空", ErrInvalidRequest)
	}
	if req.ChannelCode == "" {
		return nil, fmt.Errorf("%w：channelCode 不能为空（取 ListChannels 返回的通道编码）", ErrInvalidRequest)
	}
	if req.Amount < 0.01 {
		return nil, fmt.Errorf("%w：amount 必须大于等于 0.01 元", ErrInvalidRequest)
	}

	result := &TradeCreateResult{}
	if err := this.invoke(ctx, transport.OpTradeCreate, req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// QueryTrade - 商户查单
/**
 * @param ctx context.Context - 调用上下文
 * @param req *TradeQueryRequest - 查单请求（TradeNo 与 OutTradeNo 至少其一）
 * @return *Trade - 订单行
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) QueryTrade(ctx context.Context, req *TradeQueryRequest) (*Trade, error) {

	if req == nil || (req.TradeNo == "" && req.OutTradeNo == "") {
		return nil, fmt.Errorf("%w：tradeNo 与 outTradeNo 至少提供一个", ErrInvalidRequest)
	}

	trade := &Trade{}
	if err := this.invoke(ctx, transport.OpTradeQuery, req, trade); err != nil {
		return nil, err
	}

	return trade, nil
}

// GetCheckout - 收银/取支付链接
// scene/way 不同则生成的链接不同；同一订单可多次调用获取最新链接。
/**
 * @param ctx context.Context - 调用上下文
 * @param req *CheckoutRequest - 收银请求
 * @return *CheckoutResult - 订单行（含支付链接）+ 网关 + 商户信息
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) GetCheckout(ctx context.Context, req *CheckoutRequest) (*CheckoutResult, error) {

	if req == nil || req.TradeNo == "" {
		return nil, fmt.Errorf("%w：tradeNo 不能为空", ErrInvalidRequest)
	}

	result := &CheckoutResult{}
	if err := this.invoke(ctx, transport.OpCheckout, req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// ============================= 交易 - 结束 =============================

// ============================= 转账 - 开始 =============================

// SendTransfer - 转账发起
// 平台可能返回「待审核」的业务结果：此时 Trans.Status 为待审核态但业务码仍为 200。
/**
 * @param ctx context.Context - 调用上下文
 * @param req *TransferRequest - 转账请求
 * @return *TransferResult - 转账单行 + 商户信息
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) SendTransfer(ctx context.Context, req *TransferRequest) (*TransferResult, error) {

	if req == nil {
		return nil, fmt.Errorf("%w：转账请求不能为空", ErrInvalidRequest)
	}
	if req.ChannelCode == "" {
		return nil, fmt.Errorf("%w：channelCode 不能为空（取 ListChannels 返回的通道编码）", ErrInvalidRequest)
	}
	if req.PayeeAccount == "" {
		return nil, fmt.Errorf("%w：payeeAccount 不能为空", ErrInvalidRequest)
	}
	if req.Amount < 0.01 {
		return nil, fmt.Errorf("%w：amount 必须大于等于 0.01 元", ErrInvalidRequest)
	}

	result := &TransferResult{}
	if err := this.invoke(ctx, transport.OpTransferSend, req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// QueryTransfer - 转账查询
/**
 * @param ctx context.Context - 调用上下文
 * @param req *TransferQueryRequest - 查询请求（TradeNo 与 OutTradeNo 至少其一）
 * @return *Trans - 转账单行
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) QueryTransfer(ctx context.Context, req *TransferQueryRequest) (*Trans, error) {

	if req == nil || (req.TradeNo == "" && req.OutTradeNo == "") {
		return nil, fmt.Errorf("%w：tradeNo 与 outTradeNo 至少提供一个", ErrInvalidRequest)
	}

	trans := &Trans{}
	if err := this.invoke(ctx, transport.OpTransferQuery, req, trans); err != nil {
		return nil, err
	}

	return trans, nil
}

// ============================= 转账 - 结束 =============================

// ============================= 通道 - 开始 =============================

// ListChannels - 查询商户可用通道列表
// 通道编码（Channel.Code）即下单/转账请求的 channelCode。
/**
 * @param ctx context.Context - 调用上下文
 * @return []Channel - 通道列表（无可用通道时为空切片）
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	channels, err := client.ListChannels(ctx)
 * 	for _, channel := range channels { fmt.Println(channel.Code) }
 */
func (this *Client) ListChannels(ctx context.Context) ([]Channel, error) {

	channels := &[]Channel{}
	if err := this.invoke(ctx, transport.OpChannelList, nil, channels); err != nil {
		return nil, err
	}

	return *channels, nil
}

// ============================= 通道 - 结束 =============================

// ============================= 凭据 - 开始 =============================

// VerifyCredentials - 验证商户凭据（mchId + secret）是否有效
// 以 ListChannels（唯一只读、无资金副作用的签名端点）为探针：err == nil 即凭据有效，
// 并返回商户可用通道列表（不关心通道时可忽略返回值）。
// 错误语义：ErrInvalidSign / *APIError（如商户不存在、签名验证失败）= 凭据无效；
// ErrTransport = 平台暂不可达，本次无法判定凭据有效性。
/**
 * @param ctx context.Context - 调用上下文
 * @return []Channel - 商户可用通道列表
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	if _, err := client.VerifyCredentials(ctx); err != nil {
 * 	    log.Fatal("商户凭据无效或平台不可达：", err)
 * 	}
 */
func (this *Client) VerifyCredentials(ctx context.Context) ([]Channel, error) {

	return this.ListChannels(ctx)
}

// ============================= 凭据 - 结束 =============================
