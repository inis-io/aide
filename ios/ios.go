// Package ios - Inis Object Storage（inis 对象存储）开放平台的 Go SDK。
//
// 接入方通过本包调用平台开放能力，覆盖两个平面：
//
//   - 数据面（S3 协议子集）：桶增删查、对象上传（单 PUT / 自动分片并发）/下载（Range）/
//     元数据/删除/浏览、批量与拷贝、断点续传、桶配置族、STS、预签名链接、事件通知配置。
//     直连平台 S3 网关（默认与控制面同源，合并模式单端口部署），SigV4 签名，全程流式。
//   - 控制面（平台开放接口）：凭据自检、管理视角桶列表、清空桶、分享链接、回收站、
//     用量与配额视图。HTTP / gRPC 双链路对称（默认 gRPC，WithTransportHTTP 切换）。
//
// 一对 AccessKey + Secret 同时是两个平面的凭据：数据面作 SigV4 密钥，
// 控制面作 MD5 GenSign 同源签名密钥（与平台服务端 utils.Md5.GenSign 逐字节等价）。
//
// 接入方只需 import 本包：所有请求/响应模型、签名器与错误模型都以类型别名或
// 函数转发的方式从这里导出，内部实现拆分在以下子包：
//
//   - ios/types：公开数据模型与错误模型（零依赖）
//   - ios/sign：公开签名器（Signer 接口 + MD5 实现）
//   - ios/internal/transport：内部控制面传输链路（HTTP / gRPC）
//   - ios/internal/s3：内部数据面轻客户端（SigV4 + XML 子集，纯标准库）
//
// # 双平面地址
//
// 控制面地址经 WithEndpoint 指定（可空，缺省回退 DefaultEndpoint = 官方平台
// https://ios.inis.cn）；数据面网关默认与控制面同源（合并模式单端口部署为零配置
// 默认形态）；数据面独立端口部署时用 WithS3Endpoint 显式覆盖：
//
//	client, err := ios.NewClient(accessKey, secret,
//	    ios.WithEndpoint("https://storage.example.com"),
//	    ios.WithS3Endpoint("https://storage.example.com:9000"),
//	)
//	defer client.Close()
//
// # 超时
//
// WithTimeout（默认 10 秒）只约束控制面单次调用；数据面是流式链路（大对象上传下载），
// 不套整体超时，取消与截止由调用方 ctx 全权控制。
//
// # 安全
//
// secret 由调用方运行时注入（环境变量/配置中心），本包不持久化、不写日志、
// 不出现在任何错误文案中。生产环境两条链路都必须走 HTTPS；
// gRPC 链路默认 plaintext（对齐平台 HTTP/gRPC 同端口分流），TLS 经 WithGRPCDialOpts 注入。
package ios

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/inis-io/aide/ios/internal/s3"
	"github.com/inis-io/aide/ios/internal/transport"
	"github.com/inis-io/aide/ios/notify"
	"github.com/inis-io/aide/ios/sign"
	"github.com/inis-io/aide/ios/types"
)

// ============================= 公开面导出 - 开始 =============================
// 下列别名与转发把分区实现的公开面统一到 ios 命名空间：接入方只 import 本包即可，
// 各子包路径属实现细节（sign 公开是因其类型出现在方法签名上）。

// Signer - 控制面请求签名器（别名 ios/sign.Signer；需自定义签名协议时配合 WithSigner 使用）
type Signer = sign.Signer

// ---------- 控制面模型（别名 ios/types.*） ----------

// AccessKeyInfo - 密钥自检信息（Secret 永不下发）
type AccessKeyInfo = types.AccessKeyInfo

// BucketInfo - 管理视角桶信息
type BucketInfo = types.BucketInfo

// EmptyBucketResult - 清空桶结果
type EmptyBucketResult = types.EmptyBucketResult

// Share - 分享链接（令牌制，支持永久与吊销）
type Share = types.Share

// ShareCreateRequest - 签发分享链接请求
type ShareCreateRequest = types.ShareCreateRequest

// ShareCreateResult - 签发分享链接结果
type ShareCreateResult = types.ShareCreateResult

// ShareListRequest - 对象分享列表请求
type ShareListRequest = types.ShareListRequest

// RecycleEntry - 回收站条目
type RecycleEntry = types.RecycleEntry

// RecycleListRequest - 回收站列表请求
type RecycleListRequest = types.RecycleListRequest

// RecycleListResult - 回收站列表结果
type RecycleListResult = types.RecycleListResult

// RecycleRestoreResult - 回收站还原结果
type RecycleRestoreResult = types.RecycleRestoreResult

// RecyclePurgeResult - 回收站释放结果
type RecyclePurgeResult = types.RecyclePurgeResult

// QuotaView - 有效配额（0 = 不限）
type QuotaView = types.QuotaView

// UsageOverview - 用量总览
type UsageOverview = types.UsageOverview

// BucketUsage - 单桶用量
type BucketUsage = types.BucketUsage

// EgressPoint - 流量趋势点
type EgressPoint = types.EgressPoint

// APIError - 平台业务错误（Code 为业务码，Msg 为中文文案，Raw 为原始响应体）
type APIError = types.APIError

// ---------- 数据面模型（别名 ios/internal/s3.*） ----------

// BucketRow - S3 视角桶行
type BucketRow = s3.BucketRow

// PutRequest - 简单上传输入（单请求流式）
type PutRequest = s3.PutInput

// PutResult - 上传结果
type PutResult = s3.PutResult

// UploadRequest - 高级上传输入（按阈值自动单 PUT / 分片编排）
type UploadRequest = s3.UploadInput

// CreateMultipartUploadRequest - 分片会话初始化输入（CreateMultipartUploadEx 使用；支持 Meta / CacheControl）
type CreateMultipartUploadRequest = s3.CreateInput

// GetOptions - 下载选项（Range 字节区间）
type GetOptions = s3.GetOptions

// GetResult - 下载结果（Body 由调用方关闭）
type GetResult = s3.GetResult

// ObjectMeta - 对象元数据
type ObjectMeta = s3.ObjectMeta

// ObjectEntry - 列表条目
type ObjectEntry = s3.ObjectEntry

// ListRequest - 列举输入（ListObjectsV2 同语义）
type ListRequest = s3.ListInput

// ListResult - 列举结果
type ListResult = s3.ListResult

// Part - 已传分片记录
type Part = s3.Part

// S3Error - S3 网关返回的 XML 错误体（数据面业务错误）
type S3Error = s3.S3Error

// DeleteObjectsResult - 批量删除结果（Deleted 与 Errors 互补）
type DeleteObjectsResult = s3.DeleteObjectsResult

// DeleteObjectsError - 批量删除单键失败条目
type DeleteObjectsError = s3.DeleteObjectsError

// CopyResult - 拷贝结果
type CopyResult = s3.CopyResult

// MultipartUpload - 进行中的分片会话
type MultipartUpload = s3.MultipartUpload

// CORSRule - 跨域规则
type CORSRule = s3.CORSRule

// LifecycleRule - 生命周期规则
type LifecycleRule = s3.LifecycleRule

// SessionCredentials - STS 临时凭证（仅数据面可用）
type SessionCredentials = s3.SessionCredentials

// NotificationRule - 事件通知规则（Topic = Webhook 目标 URL；Secret = 投递验签密钥，GET 回读可见）
type NotificationRule = s3.NotificationRule

// 版本控制状态常量（PutBucketVersioning 仅收这两个值）
const (
	// VersioningEnabled - 开启版本控制
	VersioningEnabled = s3.VersioningEnabled
	// VersioningSuspended - 暂停版本控制（开启后不可回 off，只可暂停）
	VersioningSuspended = s3.VersioningSuspended
)

// 事件名常量（PutBucketNotification 订阅；平台当前仅支持这两类通配）
const (
	// EventObjectCreated - 对象创建事件（上传 / 拷贝 / 分片完成触发）
	EventObjectCreated = s3.EventObjectCreated
	// EventObjectRemoved - 对象删除事件（软删进回收站触发）
	EventObjectRemoved = s3.EventObjectRemoved
)

// ---------- 事件通知模型（别名 ios/notify.*） ----------

// Event - 验签后的结构化事件报文（Name/Delivery 取自投递头，Delivery 为幂等键）
type Event = notify.Event

// Record - 单条事件记录（S3 风格）
type Record = notify.Record

// RecordS3 - S3 事件主体
type RecordS3 = notify.RecordS3

// RecordBucket - 事件桶信息
type RecordBucket = notify.RecordBucket

// RecordObject - 事件对象信息
type RecordObject = notify.RecordObject

// ---------- 错误与助手 ----------

// 哨兵错误 - 接入方用 errors.Is 判定错误类别，平面与链路差异不外溢
var (
	// ErrInvalidRequest - 请求参数无效（本地校验或链路适配失败，未发出/未按预期发出请求）
	ErrInvalidRequest = types.ErrInvalidRequest
	// ErrInvalidSign - 签名验证失败（本地验签不通过，或平台以 Unauthenticated 拒绝）
	ErrInvalidSign = types.ErrInvalidSign
	// ErrTransport - 传输层调用失败（网络错误、连接断开、RPC 状态非业务失败）
	ErrTransport = types.ErrTransport
)

// 函数转发 - 与子包同一实现，方便接入方只 import 本包
var (
	// IsCode - 判定错误是否为指定 S3 错误码（如 IsCode(err, "NoSuchKey")）
	IsCode = s3.IsCode
	// IsNotFound - 判定错误是否为资源不存在（NoSuchKey / NoSuchBucket / 404）
	IsNotFound = s3.IsNotFound
	// JoinIds - 将 ID 列表编码为契约字符串（"1,2,3"）
	JoinIds = types.JoinIds
	// ParseIds - 解析契约字符串为 ID 列表
	ParseIds = types.ParseIds
	// ParseEvent - 事件通知报文解析 + 验签（先验签后解析；验签失败一律 ErrInvalidSign）
	ParseEvent = notify.ParseEvent
	// VerifyEvent - 事件通知仅验签（constant-time 比对；secret 或签名缺失一律 false）
	VerifyEvent = notify.VerifyEvent
)

// ============================= 公开面导出 - 结束 =============================

// ============================= 客户端 - 开始 =============================

const (
	// DefaultEndpoint - 默认控制面平台地址（官方平台；WithEndpoint 未指定时使用）
	DefaultEndpoint = "https://ios.inis.cn"
	// defaultTimeout - 控制面单次调用的整体超时
	defaultTimeout = 10 * time.Second
	// defaultUserAgent - 默认请求 User-Agent
	defaultUserAgent = "ios-go/0.1.0"
	// defaultRegion - 数据面默认区域（与平台网关配置一致）
	defaultRegion = "us-east-1"
	// defaultPartSize - 默认分片大小（8MB；平台约束单片 5MB 起）
	defaultPartSize int64 = 8 << 20
	// defaultConcurrency - 默认分片并发数
	defaultConcurrency = 4
)

// Client - Inis Object Storage 开放平台客户端
// 通过 NewClient 构造；零值不可用。构造后无内部可变状态，可并发使用；
// 使用完毕应 defer client.Close() 释放控制面 gRPC 连接。
type Client struct {
	// endpoint - 控制面平台地址（如 https://storage.example.com）
	endpoint string
	// s3Endpoint - 数据面 S3 网关地址（缺省与控制面同源；独立数据面用 WithS3Endpoint 覆盖）
	s3Endpoint string
	// region - 数据面区域（默认 us-east-1）
	region string
	// accessKey - AccessKey（双平面共用）
	accessKey string
	// secret - SecretKey（仅参与本地签名，不持久化、不写日志、不进错误文案）
	secret string
	// signer - 控制面签名器（默认 MD5）
	signer Signer
	// transport - 控制面传输链路（默认 gRPC，WithTransportHTTP 切换）
	transport transport.Transport
	// s3Client - 数据面轻客户端
	s3Client *s3.Client
	// timeout - 控制面单次调用整体超时（数据面流式不套用）
	timeout time.Duration
	// userAgent - 请求 User-Agent
	userAgent string
	// transportHTTP - 控制面是否使用 HTTP 链路（默认 false → gRPC）
	transportHTTP bool
	// httpClient - 控制面 HTTP 链路自定义客户端（nil 时按 timeout 内置）
	httpClient *http.Client
	// dialOpts - gRPC 拨号选项（TLS 凭证等，WithGRPCDialOpts 累积）
	dialOpts []grpc.DialOption
	// sessionToken - STS 会话令牌（仅数据面；空 = 永久凭证）
	sessionToken string
	// partSize - 高级上传分片大小（默认 8MB）
	partSize int64
	// concurrency - 高级上传分片并发数（默认 4）
	concurrency int
}

// Option - Client 构造选项（直接作用于接收者）
type Option func(*Client)

// WithEndpoint - 控制面平台地址（可空，缺省回退 DefaultEndpoint = 官方平台 https://ios.inis.cn）
/**
 * @param addr string - 平台地址，如 https://storage.example.com
 */
func WithEndpoint(addr string) Option {

	return func(this *Client) {
		this.endpoint = addr
	}
}

// WithS3Endpoint - 数据面 S3 网关地址
// 缺省与控制面同源（合并模式单端口）；数据面独立端口部署时显式指定。
/**
 * @param addr string - 网关地址，如 https://storage.example.com:9000
 */
func WithS3Endpoint(addr string) Option {

	return func(this *Client) {
		this.s3Endpoint = addr
	}
}

// WithRegion - 数据面区域（默认 us-east-1，与平台网关配置一致）
/**
 * @param region string - 区域名
 */
func WithRegion(region string) Option {

	return func(this *Client) {
		if strings.TrimSpace(region) != "" {
			this.region = strings.TrimSpace(region)
		}
	}
}

// WithTransportHTTP - 控制面切换为 HTTP 链路（默认 gRPC）
func WithTransportHTTP() Option {

	return func(this *Client) {
		this.transportHTTP = true
	}
}

// WithGRPCDialOpts - 透传 gRPC 拨号选项（TLS 凭证、拦截器、保活等）
// 未传入任何选项时使用 plaintext（insecure）拨号，对齐平台 HTTP/gRPC 同端口分流现状；
// 只要传入选项，TLS 凭证由调用方自行负责。
/**
 * @param opts ...grpc.DialOption - 拨号选项
 */
func WithGRPCDialOpts(opts ...grpc.DialOption) Option {

	return func(this *Client) {
		this.dialOpts = append(this.dialOpts, opts...)
	}
}

// WithHTTPClient - 注入控制面 HTTP 链路的自定义客户端（连接池/代理/重试交给它）
// 注入后 WithTimeout 不再约束 HTTP 客户端，但仍作为单次调用的上下文超时；
// 数据面流式链路不受影响（始终使用无整体超时的独立客户端）。
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

// WithTimeout - 控制面单次调用的整体超时（默认 10 秒；数据面流式不套用）
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

// WithUserAgent - 请求 User-Agent（默认 ios-go/0.1.0）
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

// WithSigner - 注入自定义控制面签名器（演进 seam，默认 MD5）
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

// WithSessionToken - 注入 STS 会话令牌（仅数据面）
// 数据面按 S3 规范携带 x-amz-security-token；控制面始终以父 AK 签名，不携带令牌。
/**
 * @param token string - 会话令牌
 */
func WithSessionToken(token string) Option {

	return func(this *Client) {
		this.sessionToken = strings.TrimSpace(token)
	}
}

// WithPartSize - 高级上传分片大小（默认 8MB；平台约束单片 5MB 起，自动上调保证片数 ≤10000）
/**
 * @param size int64 - 分片字节数（<= 0 忽略）
 */
func WithPartSize(size int64) Option {

	return func(this *Client) {
		if size > 0 {
			this.partSize = size
		}
	}
}

// WithConcurrency - 高级上传分片并发数（默认 4）
/**
 * @param n int - 并发数（<= 0 忽略）
 */
func WithConcurrency(n int) Option {

	return func(this *Client) {
		if n > 0 {
			this.concurrency = n
		}
	}
}

// NewClient - 创建开放平台客户端
// 只完成参数校验与双平面链路装配（gRPC 为惰性连接），不发起任何业务请求。
/**
 * @param accessKey string - AccessKey
 * @param secret string - SecretKey（运行时注入，勿硬编码）
 * @param opts ...Option - 构造选项（WithEndpoint 可空，缺省 DefaultEndpoint）
 * @return *Client - 客户端实例
 * @return error - 参数缺失或链路装配失败（包装 ErrInvalidRequest / ErrTransport）
 * @example：
 * 	client, err := ios.NewClient(accessKey, secret,
 * 	    ios.WithEndpoint("https://storage.example.com"),
 * 	    ios.WithTransportHTTP(),
 * 	)
 * 	if err != nil { return err }
 * 	defer client.Close()
 */
func NewClient(accessKey, secret string, opts ...Option) (*Client, error) {

	this := &Client{
		accessKey:   strings.TrimSpace(accessKey),
		secret:      secret,
		signer:      sign.New(),
		region:      defaultRegion,
		timeout:     defaultTimeout,
		userAgent:   defaultUserAgent,
		partSize:    defaultPartSize,
		concurrency: defaultConcurrency,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(this)
		}
	}

	this.endpoint = strings.TrimRight(strings.TrimSpace(this.endpoint), "/")
	this.s3Endpoint = strings.TrimRight(strings.TrimSpace(this.s3Endpoint), "/")

	if this.endpoint == "" {
		this.endpoint = DefaultEndpoint
	}
	if this.accessKey == "" {
		return nil, fmt.Errorf("%w：accessKey 不能为空", ErrInvalidRequest)
	}
	if this.secret == "" {
		return nil, fmt.Errorf("%w：secret 不能为空", ErrInvalidRequest)
	}

	// 数据面网关缺省与控制面同源（合并模式单端口；独立数据面用 WithS3Endpoint 覆盖）
	if this.s3Endpoint == "" {
		this.s3Endpoint = deriveS3Endpoint(this.endpoint)
		if this.s3Endpoint == "" {
			return nil, fmt.Errorf("%w：无法从 endpoint %q 派生数据面网关地址，请使用 WithS3Endpoint 显式指定", ErrInvalidRequest, this.endpoint)
		}
	}

	config := transport.Config{
		Endpoint:   this.endpoint,
		AccessKey:  this.accessKey,
		Secret:     this.secret,
		Signer:     this.signer,
		Timeout:    this.timeout,
		UserAgent:  this.userAgent,
		HTTPClient: this.httpClient,
		DialOpts:   this.dialOpts,
	}

	if this.transportHTTP {
		this.transport = transport.NewHTTP(config)
	} else {
		link, err := transport.NewGRPC(config)
		if err != nil {
			return nil, err
		}
		this.transport = link
	}

	s3Client, err := s3.New(s3.Config{
		Endpoint:     this.s3Endpoint,
		Region:       this.region,
		AccessKey:    this.accessKey,
		Secret:       this.secret,
		SessionToken: this.sessionToken,
		UserAgent:    this.userAgent,
	})
	if err != nil {
		return nil, err
	}
	this.s3Client = s3Client

	return this, nil
}

// deriveS3Endpoint - 数据面网关地址派生（与控制面同源同端口：合并模式 [s3] merge 单端口部署
// 为零配置默认形态；独立 :9000 数据面部署用 WithS3Endpoint 显式指定）。
// 仅保留 scheme://host[:port]，丢弃 path（S3 路由从根路径起算）。
func deriveS3Endpoint(endpoint string) string {

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return ""
	}

	return parsed.Scheme + "://" + parsed.Host
}

// Close - 释放底层连接（可重复调用；数据面无持有资源）
func (this *Client) Close() error {

	if this == nil || this.transport == nil {
		return nil
	}

	return this.transport.Close()
}

// invoke - 控制面统一调用入口：补齐上下文与整体超时后转交传输链路
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

// data - 数据面统一入口（流式链路不套整体超时，ctx 全权由调用方控制）
func (this *Client) data() (*s3.Client, error) {

	if this == nil || this.s3Client == nil {
		return nil, fmt.Errorf("%w：客户端未初始化，请使用 NewClient 构造", ErrInvalidRequest)
	}

	return this.s3Client, nil
}

// ============================= 客户端 - 结束 =============================

// ============================= 控制面：凭据 - 开始 =============================

// VerifyCredentials - 凭据验证 / 密钥自检（err == nil 即凭据有效）
// 返回密钥的自检信息（scope/到期/最近使用），供接入方核对授权范围与到期时间。
// 错误语义：ErrInvalidSign / *APIError = 凭据无效；ErrTransport = 平台暂不可达。
/**
 * @param ctx context.Context - 调用上下文（超时由控制面整体超时兜底）
 * @return *AccessKeyInfo - 密钥自检信息
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	info, err := client.VerifyCredentials(ctx)
 * 	if err != nil { log.Fatal("凭据无效或平台不可达：", err) }
 * 	fmt.Println(info.Scope, info.ExpireAt)
 */
func (this *Client) VerifyCredentials(ctx context.Context) (*AccessKeyInfo, error) {

	info := &AccessKeyInfo{}
	if err := this.invoke(ctx, transport.OpAccessKeyVerify, nil, info); err != nil {
		return nil, err
	}

	return info, nil
}

// ============================= 控制面：凭据 - 结束 =============================

// ============================= 控制面：桶 - 开始 =============================

// ListBucketInfos - 管理视角桶列表（含 acl/versioning/status/存储级别，供核对与展示）
// 与数据面 ListBuckets（S3 视角）字段集不同，按需取用。
/**
 * @param ctx context.Context - 调用上下文
 * @return []BucketInfo - 桶列表
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) ListBucketInfos(ctx context.Context) ([]BucketInfo, error) {

	buckets := &[]BucketInfo{}
	if err := this.invoke(ctx, transport.OpBucketInfoList, nil, buckets); err != nil {
		return nil, err
	}

	return *buckets, nil
}

// EmptyBucket - 清空桶（全部对象软删进回收站 + 物理文件清理，不删桶本身）
// 写操作：要求凭据 scope 为读写且覆盖目标桶；回收站条目仍可还原。
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return *EmptyBucketResult - 清空的对象数
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) EmptyBucket(ctx context.Context, bucket string) (*EmptyBucketResult, error) {

	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	result := &EmptyBucketResult{}
	if err := this.invoke(ctx, transport.OpBucketEmpty, &types.EmptyBucketRequest{Bucket: bucket}, result); err != nil {
		return nil, err
	}

	return result, nil
}

// ============================= 控制面：桶 - 结束 =============================

// ============================= 控制面：分享 - 开始 =============================

// CreateShare - 签发分享链接（令牌制）
// 同一对象的活跃分享在 60 秒幂等去重窗口内复用（Reused=true）。
/**
 * @param ctx context.Context - 调用上下文
 * @param req *ShareCreateRequest - 签发请求（Expires 秒，0 = 永久；负数收敛为 0）
 * @return *ShareCreateResult - 分享行（完整链接 = 平台地址 + /share/{Token}）
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	res, err := client.CreateShare(ctx, &ios.ShareCreateRequest{
 * 	    Bucket: "photos", Key: "a/b.jpg", Expires: 3600,
 * 	})
 */
func (this *Client) CreateShare(ctx context.Context, req *ShareCreateRequest) (*ShareCreateResult, error) {

	if req == nil {
		return nil, fmt.Errorf("%w：签发分享请求不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Key) == "" {
		return nil, fmt.Errorf("%w：key 不能为空（文件夹不支持分享）", ErrInvalidRequest)
	}

	result := &ShareCreateResult{}
	if err := this.invoke(ctx, transport.OpShareCreate, req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// ListShares - 对象分享列表（含已过期项，供审计与吊销前核对）
/**
 * @param ctx context.Context - 调用上下文
 * @param req *ShareListRequest - 列表请求（bucket 与 key 必填）
 * @return []Share - 分享列表
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) ListShares(ctx context.Context, req *ShareListRequest) ([]Share, error) {

	if req == nil {
		return nil, fmt.Errorf("%w：分享列表请求不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Bucket) == "" || strings.TrimSpace(req.Key) == "" {
		return nil, fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	shares := &[]Share{}
	if err := this.invoke(ctx, transport.OpShareList, req, shares); err != nil {
		return nil, err
	}

	return *shares, nil
}

// RevokeShare - 吊销分享（软删即失效，已签发链接立即不可访问）
/**
 * @param ctx context.Context - 调用上下文
 * @param id int64 - 分享 ID（取 ListShares / CreateShare 返回的 Share.Id）
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) RevokeShare(ctx context.Context, id int64) error {

	if id <= 0 {
		return fmt.Errorf("%w：id 必须大于 0", ErrInvalidRequest)
	}

	return this.invoke(ctx, transport.OpShareRevoke, &types.ShareRevokeRequest{Id: id}, nil)
}

// ============================= 控制面：分享 - 结束 =============================

// ============================= 控制面：回收站 - 开始 =============================

// ListRecycle - 回收站列表（分页 + key 模糊；page 默认 1，limit 默认 20、服务端封顶 200）
/**
 * @param ctx context.Context - 调用上下文
 * @param req *RecycleListRequest - 列表请求（nil 等价零值：全部桶第一页）
 * @return *RecycleListResult - 条目 + 总条数
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) ListRecycle(ctx context.Context, req *RecycleListRequest) (*RecycleListResult, error) {

	if req == nil {
		req = &RecycleListRequest{}
	}

	result := &RecycleListResult{}
	if err := this.invoke(ctx, transport.OpRecycleList, req, result); err != nil {
		return nil, err
	}

	return result, nil
}

// RestoreObjects - 回收站还原（按对象行 ID 批量还原）
// 回收期间被重新上传占用的 key 会跳过并进入 Conflicts，不报错。
/**
 * @param ctx context.Context - 调用上下文
 * @param ids []int64 - 对象行 ID 列表（取 ListRecycle 返回的 RecycleEntry.Id）
 * @return *RecycleRestoreResult - 已还原数量 + 冲突跳过的 key 列表
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) RestoreObjects(ctx context.Context, ids []int64) (*RecycleRestoreResult, error) {

	if len(ids) == 0 {
		return nil, fmt.Errorf("%w：ids 不能为空", ErrInvalidRequest)
	}

	result := &RecycleRestoreResult{}
	if err := this.invoke(ctx, transport.OpRecycleRestore, &types.RecycleRestoreRequest{Ids: ids}, result); err != nil {
		return nil, err
	}

	return result, nil
}

// PurgeObjects - 回收站指定释放（硬删元数据 + 物理清理，不可恢复）
/**
 * @param ctx context.Context - 调用上下文
 * @param ids []int64 - 对象行 ID 列表
 * @return *RecyclePurgeResult - 已释放数量
 * @return error - ErrInvalidRequest / ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) PurgeObjects(ctx context.Context, ids []int64) (*RecyclePurgeResult, error) {

	if len(ids) == 0 {
		return nil, fmt.Errorf("%w：ids 不能为空", ErrInvalidRequest)
	}

	result := &RecyclePurgeResult{}
	if err := this.invoke(ctx, transport.OpRecyclePurge, &types.RecyclePurgeRequest{Ids: ids}, result); err != nil {
		return nil, err
	}

	return result, nil
}

// ClearRecycle - 回收站全部释放（不可恢复；bucket 为空 = 全部桶）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名（空 = 全部桶）
 * @return *RecyclePurgeResult - 已释放数量
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) ClearRecycle(ctx context.Context, bucket string) (*RecyclePurgeResult, error) {

	result := &RecyclePurgeResult{}
	if err := this.invoke(ctx, transport.OpRecycleClear, &types.RecycleClearRequest{Bucket: bucket}, result); err != nil {
		return nil, err
	}

	return result, nil
}

// ============================= 控制面：回收站 - 结束 =============================

// ============================= 控制面：用量 - 开始 =============================

// GetUsageOverview - 用量总览（存储量/对象数/桶数/当月出口流量 + 有效配额视图）
/**
 * @param ctx context.Context - 调用上下文
 * @return *UsageOverview - 总览（Quota 各维 0 = 不限）
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) GetUsageOverview(ctx context.Context) (*UsageOverview, error) {

	overview := &UsageOverview{}
	if err := this.invoke(ctx, transport.OpUsageOverview, nil, overview); err != nil {
		return nil, err
	}

	return overview, nil
}

// ListBucketUsage - 按桶用量列表（已软删的桶仍展示计量）
/**
 * @param ctx context.Context - 调用上下文
 * @return []BucketUsage - 各桶存储量/对象数/当月出口流量
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) ListBucketUsage(ctx context.Context) ([]BucketUsage, error) {

	rows := &[]BucketUsage{}
	if err := this.invoke(ctx, transport.OpUsageBucketList, nil, rows); err != nil {
		return nil, err
	}

	return *rows, nil
}

// GetEgressTrend - 出口流量按日序列（近 30 天，日期升序）
/**
 * @param ctx context.Context - 调用上下文
 * @return []EgressPoint - 趋势点（Date = YYYY-MM-DD）
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 */
func (this *Client) GetEgressTrend(ctx context.Context) ([]EgressPoint, error) {

	points := &[]EgressPoint{}
	if err := this.invoke(ctx, transport.OpUsageEgressTrend, nil, points); err != nil {
		return nil, err
	}

	return *points, nil
}

// ============================= 控制面：用量 - 结束 =============================

// ============================= 数据面：桶 - 开始 =============================

// CreateBucket - 创建桶（S3 PutBucket；桶名全局唯一）
/**
 * @param ctx context.Context - 调用上下文（数据面不套整体超时）
 * @param bucket string - 桶名
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) CreateBucket(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.CreateBucket(ctx, bucket)
}

// DeleteBucket - 删除桶（S3 DeleteBucket；桶必须为空，清桶用 EmptyBucket）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteBucket(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.DeleteBucket(ctx, bucket)
}

// HeadBucket - 探测桶存在与访问权（S3 HeadBucket）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return error - nil 即存在且可访问；否则 ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) HeadBucket(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.HeadBucket(ctx, bucket)
}

// ListBuckets - 列出当前 AK 用户的桶（S3 视角：名称 + 创建时间）
// 管理视角（acl/versioning/status 等）请用控制面 ListBucketInfos。
/**
 * @param ctx context.Context - 调用上下文
 * @return []BucketRow - 桶行列表
 * @return error - ErrTransport / *S3Error
 */
func (this *Client) ListBuckets(ctx context.Context) ([]BucketRow, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}

	return client.ListBuckets(ctx)
}

// ============================= 数据面：桶 - 结束 =============================

// ============================= 数据面：对象 - 开始 =============================

// PutObject - 简单上传（单请求流式直通，默认流式 MD5 与响应 ETag 回核）
// 默认开启秒传探测：Body 实现 io.Seeker 时先本地算全量 SHA-256 探测，命中零字节上行；
// 未命中/旧网关/网络失败自动回退正常上传。InstantUpload 显式 false 可关闭，OnInstantHit 可感知命中。
// 大对象请用 Upload（按阈值自动分片并发）；Meta（如 {"name": "摸鱼"}）与 CacheControl 随请求头写入。
/**
 * @param ctx context.Context - 调用上下文（流式不套整体超时）
 * @param req *PutRequest - 上传输入（Body 不整读内存，调用方负责关闭）
 * @return *PutResult - ETag / 实际写入字节数 / 版本号（秒传命中时 Size 为本地内容长度）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 * @example：
 * 	file, _ := os.Open("a.jpg")
 * 	defer file.Close()
 * 	res, err := client.PutObject(ctx, &ios.PutRequest{
 * 	    Bucket: "photos", Key: "a.jpg", Body: file, Mime: "image/jpeg",
 * 	})
 */
func (this *Client) PutObject(ctx context.Context, req *PutRequest) (*PutResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w：上传请求不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Bucket) == "" || strings.TrimSpace(req.Key) == "" {
		return nil, fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}
	if req.Body == nil {
		return nil, fmt.Errorf("%w：上传内容流不能为空", ErrInvalidRequest)
	}

	return client.PutObject(ctx, *req)
}

// Upload - 高级上传：Size ≤ PartSize 走单 PUT，否则分片编排（顺序读片 + 有界并发 + 失败自动 Abort）
// 常驻内存 ≤ PartSize × Concurrency；分片大小自动上调保证片数 ≤10000。
/**
 * @param ctx context.Context - 调用上下文（流式不套整体超时）
 * @param req *UploadRequest - 上传输入（Size 必填；PartSize/Concurrency 为 0 时取客户端级配置）
 * @return *PutResult - ETag（分片形态 md5(各片MD5拼接)-{N}）/ 总字节数 / 版本号
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) Upload(ctx context.Context, req *UploadRequest) (*PutResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w：上传请求不能为空", ErrInvalidRequest)
	}

	value := *req
	if value.PartSize <= 0 {
		value.PartSize = this.partSize
	}
	if value.Concurrency <= 0 {
		value.Concurrency = this.concurrency
	}

	return client.Upload(ctx, value)
}

// GetObject - 下载（流式回写，支持 Range 字节区间）
/**
 * @param ctx context.Context - 调用上下文（流式不套整体超时）
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param options *GetOptions - 下载选项（nil = 全量）
 * @return *GetResult - 内容流（调用方负责关闭 Body）+ 元数据
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error（NoSuchKey）
 */
func (this *Client) GetObject(ctx context.Context, bucket, key string, options *GetOptions) (*GetResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.GetObject(ctx, bucket, key, options)
}

// HeadObject - 取对象元数据（S3 HeadObject）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @return *ObjectMeta - 元数据（ETag/大小/Mime/用户元数据/版本号）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error（404）
 */
func (this *Client) HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.HeadObject(ctx, bucket, key)
}

// DeleteObject - 删除对象（S3 DeleteObject；软删进回收站，还原/释放走控制面回收站族）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteObject(ctx context.Context, bucket, key string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.DeleteObject(ctx, bucket, key)
}

// ListObjects - 浏览对象（ListObjectsV2 同语义：prefix/delimiter/token 分页）
/**
 * @param ctx context.Context - 调用上下文
 * @param req *ListRequest - 列举输入（Bucket 必填；MaxKeys 0 = 服务端默认）
 * @return *ListResult - 条目 + 公共前缀 + 翻页令牌
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) ListObjects(ctx context.Context, req *ListRequest) (*ListResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w：列举请求不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.ListObjects(ctx, *req)
}

// ============================= 数据面：对象 - 结束 =============================

// ============================= 数据面：预签名 - 开始 =============================

// PresignGetObject - 预签名下载链接（本地自签，零网络往返）
// 有效期 <= 0 默认 1 小时，硬上限 7 天；带 STS 会话时自动附加 X-Amz-Security-Token。
/**
 * @param ctx context.Context - 调用上下文（本地操作，不发起请求）
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param expires time.Duration - 有效期
 * @return string - 预签名 URL（GET 方法直接可访问）
 * @return error - ErrInvalidRequest
 */
func (this *Client) PresignGetObject(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.PresignGet(ctx, bucket, key, expires)
}

// PresignGetObjectWith - 预签名下载链接（叠加额外查询参数并一并签入；本地自签，零网络往返）
// 典型用法：把网关图片处理串签入（extraQuery{"x-inis-process": "image/resize,w_32,q_40"}），
// 私有桶的 blur-up 双 URL（模糊小图 + 原图）必须走本方法，事后拼参数会破坏签名。
/**
 * @param ctx context.Context - 调用上下文（本地操作，不发起请求）
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param expires time.Duration - 有效期（<= 0 默认 1 小时，硬上限 7 天）
 * @param extraQuery map[string]string - 额外查询参数（参与签名；空 = 与 PresignGetObject 等价）
 * @return string - 预签名 URL（GET 方法直接可访问）
 * @return error - ErrInvalidRequest
 */
func (this *Client) PresignGetObjectWith(ctx context.Context, bucket, key string, expires time.Duration, extraQuery map[string]string) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.PresignGetWith(ctx, bucket, key, expires, extraQuery)
}

// ImageProcessURL - 图片实时处理链接拼装（纯本地，零网络往返）：
// s3Endpoint/{bucket}/{key}?x-inis-process=<process>，适用 public-read 公开桶的 blur-up 渐进加载
// （先加载模糊小图 URL，再替换原图 URL）；私有桶请改用 PresignGetObjectWith 把处理串签入。
/**
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param process string - 处理串（image/resize,w_32,q_40 形态）
 * @return string - 可直接 GET 的处理链接
 * @return error - ErrInvalidRequest
 */
func (this *Client) ImageProcessURL(bucket, key, process string) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(process) == "" {
		return "", fmt.Errorf("%w：bucket、key 与 process 不能为空", ErrInvalidRequest)
	}

	return client.ImageProcessURL(bucket, key, process), nil
}

// PresignPutObject - 预签名上传链接（本地自签，零网络往返；PUT 方法使用）
/**
 * @param ctx context.Context - 调用上下文（本地操作，不发起请求）
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param mime string - Content-Type（空 = 不约束）
 * @param expires time.Duration - 有效期（<= 0 默认 1 小时，硬上限 7 天）
 * @return string - 预签名 URL
 * @return error - ErrInvalidRequest
 */
func (this *Client) PresignPutObject(ctx context.Context, bucket, key, mime string, expires time.Duration) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.PresignPut(ctx, bucket, key, mime, expires)
}

// ============================= 数据面：预签名 - 结束 =============================

// ============================= 数据面：批量与拷贝 - 开始 =============================

// DeleteObjects - 批量删除（超过 1000 键自动分批；单键失败不阻断，逐键回传 Errors）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param keys []string - 删除键列表（软删进回收站，还原/释放走控制面回收站族）
 * @return *DeleteObjectsResult - 成功键列表 + 失败键原因
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteObjects(ctx context.Context, bucket string, keys []string) (*DeleteObjectsResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w：删除键列表不能为空", ErrInvalidRequest)
	}

	return client.DeleteObjects(ctx, bucket, keys)
}

// CopyObject - 服务端拷贝（零流量回传；源与目标须同在该 AK 授权范围内）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 目标桶名
 * @param key string - 目标对象键
 * @param sourceBucket string - 源桶名
 * @param sourceKey string - 源对象键
 * @return *CopyResult - 目标 ETag / 最后修改时间 / 版本号
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error（NoSuchKey / AccessDenied）
 */
func (this *Client) CopyObject(ctx context.Context, bucket, key, sourceBucket, sourceKey string) (*CopyResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" ||
		strings.TrimSpace(sourceBucket) == "" || strings.TrimSpace(sourceKey) == "" {
		return nil, fmt.Errorf("%w：目标 bucket/key 与源 bucket/key 均不能为空", ErrInvalidRequest)
	}

	return client.CopyObject(ctx, bucket, key, sourceBucket, sourceKey)
}

// ============================= 数据面：批量与拷贝 - 结束 =============================

// ============================= 数据面：断点续传 - 开始 =============================

// ResumeUpload - 断点续传：发现同桶同键的进行中分片会话时复用该会话，已传分片本地跳过、只补传缺失分片；
// 无进行中会话时等价 Upload。
// 调用方必须从头提供完整内容流；已传分片信任网关记录（大小对齐布局即采纳）。
// 布局不一致（partSize 变化/键被他人占用会话）返回 ErrInvalidRequest，可先 AbortMultipartUpload 再重传。
/**
 * @param ctx context.Context - 调用上下文（流式不套整体超时）
 * @param req *UploadRequest - 上传输入（语义同 Upload；PartSize/Concurrency 为 0 时取客户端级配置）
 * @return *PutResult - ETag / 总字节数 / 版本号
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) ResumeUpload(ctx context.Context, req *UploadRequest) (*PutResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("%w：上传请求不能为空", ErrInvalidRequest)
	}

	value := *req
	if value.PartSize <= 0 {
		value.PartSize = this.partSize
	}
	if value.Concurrency <= 0 {
		value.Concurrency = this.concurrency
	}

	return client.ResumeUpload(ctx, value)
}

// ListMultipartUploads - 列出进行中的分片会话（续传发现 / 遗留治理）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param prefix string - 键前缀过滤（空 = 全桶）
 * @return []MultipartUpload - 会话列表（Key / UploadId / Initiated）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]MultipartUpload, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.ListMultipartUploads(ctx, bucket, prefix)
}

// AbortMultipartUpload - 中止分片会话（幂等；清理遗留会话，已传分片随之释放）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param uploadId string - 分片会话 ID（取自 ListMultipartUploads）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) AbortMultipartUpload(ctx context.Context, bucket, key, uploadId string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(uploadId) == "" {
		return fmt.Errorf("%w：bucket、key 与 uploadId 均不能为空", ErrInvalidRequest)
	}

	return client.AbortMultipartUpload(ctx, bucket, key, uploadId)
}

// ============================= 数据面：断点续传 - 结束 =============================

// ============================= 数据面：分片原语 - 开始 =============================

// CreateMultipartUpload - 初始化分片会话，返回 uploadId（低层原语；高层编排见 Upload / ResumeUpload）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param mime string - Content-Type（空 = 网关缺省）
 * @return string - 分片会话 ID
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) CreateMultipartUpload(ctx context.Context, bucket, key, mime string) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.CreateMultipartUpload(ctx, bucket, key, mime)
}

// CreateMultipartUploadEx - 初始化分片会话，返回 uploadId（支持用户元数据与 Cache-Control；低层原语）
/**
 * @param ctx context.Context - 调用上下文
 * @param req *CreateMultipartUploadRequest - 会话初始化输入（Bucket/Key 必填；Meta/CacheControl 可选）
 * @return string - 分片会话 ID
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 * @example：
 * 	uploadId, err := client.CreateMultipartUploadEx(ctx, &ios.CreateMultipartUploadRequest{
 * 	    Bucket: "emoji", Key: "community/big.gif", Mime: "image/gif",
 * 	    Meta: map[string]string{"name": "大图"}, CacheControl: "public, max-age=2592000",
 * 	})
 */
func (this *Client) CreateMultipartUploadEx(ctx context.Context, req *CreateMultipartUploadRequest) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if req == nil {
		return "", fmt.Errorf("%w：分片初始化请求不能为空", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Bucket) == "" || strings.TrimSpace(req.Key) == "" {
		return "", fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}

	return client.CreateMultipartUploadEx(ctx, *req)
}

// UploadPart - 上传单个分片（流式 + MD5 回核 ETag）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param uploadId string - 分片会话 ID（取自 CreateMultipartUpload）
 * @param partNumber int - 分片序号（1-10000）
 * @param body io.Reader - 分片内容流
 * @return *Part - 已传分片记录（PartNumber / ETag / Size）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) UploadPart(ctx context.Context, bucket, key, uploadId string, partNumber int, body io.Reader) (*Part, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(uploadId) == "" {
		return nil, fmt.Errorf("%w：bucket、key 与 uploadId 均不能为空", ErrInvalidRequest)
	}
	if partNumber < 1 || partNumber > 10000 {
		return nil, fmt.Errorf("%w：partNumber 必须在 1-10000 之间", ErrInvalidRequest)
	}
	if body == nil {
		return nil, fmt.Errorf("%w：分片内容流不能为空", ErrInvalidRequest)
	}

	return client.UploadPart(ctx, bucket, key, uploadId, partNumber, body)
}

// ListParts - 查询会话已传分片（自动翻页取全量，按序号升序）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param uploadId string - 分片会话 ID
 * @return []Part - 已传分片列表
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) ListParts(ctx context.Context, bucket, key, uploadId string) ([]Part, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(uploadId) == "" {
		return nil, fmt.Errorf("%w：bucket、key 与 uploadId 均不能为空", ErrInvalidRequest)
	}

	return client.ListParts(ctx, bucket, key, uploadId)
}

// CompleteMultipartUpload - 完成分片上传（清单需按序号升序携带；Size 为清单各分片字节数之和）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param uploadId string - 分片会话 ID
 * @param parts []Part - 分片清单（至少一条；ETag 取自 UploadPart / ListParts 回值）
 * @return *PutResult - 完成结果（ETag / Size / VersionId）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadId string, parts []Part) (*PutResult, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" || strings.TrimSpace(uploadId) == "" {
		return nil, fmt.Errorf("%w：bucket、key 与 uploadId 均不能为空", ErrInvalidRequest)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w：分片清单不能为空", ErrInvalidRequest)
	}

	return client.CompleteMultipartUpload(ctx, bucket, key, uploadId, parts)
}

// ============================= 数据面：分片原语 - 结束 =============================

// ============================= 数据面：桶配置 - 开始 =============================

// GetBucketCors - 取桶 CORS 配置（未配置返回 *S3Error{Code: NoSuchCORSConfiguration}，可用 IsCode 判定）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return []CORSRule - 规则列表
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) GetBucketCors(ctx context.Context, bucket string) ([]CORSRule, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.GetBucketCors(ctx, bucket)
}

// PutBucketCors - 写桶 CORS 配置（整体覆盖语义；规则数 1-100）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param rules []CORSRule - 规则列表（每条至少一个来源与一个方法）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) PutBucketCors(ctx context.Context, bucket string, rules []CORSRule) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.PutBucketCors(ctx, bucket, rules)
}

// DeleteBucketCors - 删桶 CORS 配置
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteBucketCors(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.DeleteBucketCors(ctx, bucket)
}

// GetBucketVersioning - 取桶版本控制状态（"" = 未开启 / Enabled / Suspended）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return string - 状态（未开启返回空串，S3 语义不返回 404）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) GetBucketVersioning(ctx context.Context, bucket string) (string, error) {

	client, err := this.data()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(bucket) == "" {
		return "", fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.GetBucketVersioning(ctx, bucket)
}

// PutBucketVersioning - 写桶版本控制状态（仅收 VersioningEnabled / VersioningSuspended；开启后不可回 off）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param status string - 状态（VersioningEnabled / VersioningSuspended）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) PutBucketVersioning(ctx context.Context, bucket, status string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.PutBucketVersioning(ctx, bucket, status)
}

// GetBucketLifecycle - 取桶生命周期配置（未配置返回 *S3Error{Code: NoSuchLifecycleConfiguration}）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return []LifecycleRule - 规则列表
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) GetBucketLifecycle(ctx context.Context, bucket string) ([]LifecycleRule, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.GetBucketLifecycle(ctx, bucket)
}

// PutBucketLifecycle - 写桶生命周期配置（整体覆盖语义；规则数 1-1000，每条至少一个动作）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param rules []LifecycleRule - 规则列表
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) PutBucketLifecycle(ctx context.Context, bucket string, rules []LifecycleRule) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.PutBucketLifecycle(ctx, bucket, rules)
}

// DeleteBucketLifecycle - 删桶生命周期配置
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteBucketLifecycle(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.DeleteBucketLifecycle(ctx, bucket)
}

// ============================= 数据面：桶配置 - 结束 =============================

// ============================= 数据面：事件通知 - 开始 =============================

// GetBucketNotification - 取桶事件通知配置（未配置返回空列表，AWS 语义不 404）
// 返回规则含服务端生成的 Id 与 Secret（限属主签名端点），Secret 即接收方验签密钥。
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return []NotificationRule - 规则列表（空 = 未配置）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) GetBucketNotification(ctx context.Context, bucket string) ([]NotificationRule, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.GetBucketNotification(ctx, bucket)
}

// PutBucketNotification - 写桶事件通知配置（整组替换语义；规则数 0-100，空列表 = 清除）
// Secret 留空由服务端生成，写入后经 GetBucketNotification 回读可见（配置到接收方验签）。
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @param rules []NotificationRule - 规则列表（Topic 须为 http(s) 地址；Events 取 EventObjectCreated / EventObjectRemoved）
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 * @example：
 * 	err := client.PutBucketNotification(ctx, "photos", []ios.NotificationRule{{
 * 	    Topic:  "https://hooks.example.com/ios",
 * 	    Events: []string{ios.EventObjectCreated, ios.EventObjectRemoved},
 * 	}})
 */
func (this *Client) PutBucketNotification(ctx context.Context, bucket string, rules []NotificationRule) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.PutBucketNotification(ctx, bucket, rules)
}

// DeleteBucketNotification - 清除桶事件通知配置（PUT 空配置语义，AWS 无独立 Delete 操作）
/**
 * @param ctx context.Context - 调用上下文
 * @param bucket string - 桶名
 * @return error - ErrInvalidRequest / ErrTransport / *S3Error
 */
func (this *Client) DeleteBucketNotification(ctx context.Context, bucket string) error {

	client, err := this.data()
	if err != nil {
		return err
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	return client.DeleteBucketNotification(ctx, bucket)
}

// ============================= 数据面：事件通知 - 结束 =============================

// ============================= 数据面：STS - 开始 =============================

// GetSessionToken - 以永久凭证签发 STS 临时凭证（默认 1 小时，钳制 15 分钟 - 12 小时）
// 临时凭证仅数据面可用：用返回的三元组 + WithSessionToken 构造新客户端（控制面签名只收永久凭证）。
/**
 * @param ctx context.Context - 调用上下文
 * @param duration time.Duration - 有效期（<= 0 默认 1 小时；钳制 [15 分钟, 12 小时]）
 * @return *SessionCredentials - 临时凭证三元组 + 过期时间
 * @return error - ErrInvalidRequest（临时凭证再签发）/ ErrTransport / *S3Error
 * @example：
 * 	creds, _ := client.GetSessionToken(ctx, 2*time.Hour)
 * 	temp, _ := ios.NewClient(creds.AccessKey, creds.Secret,
 * 	    ios.WithEndpoint(endpoint), ios.WithSessionToken(creds.Token))
 * 	defer temp.Close()
 */
func (this *Client) GetSessionToken(ctx context.Context, duration time.Duration) (*SessionCredentials, error) {

	client, err := this.data()
	if err != nil {
		return nil, err
	}

	return client.GetSessionToken(ctx, duration)
}

// ============================= 数据面：STS - 结束 =============================
