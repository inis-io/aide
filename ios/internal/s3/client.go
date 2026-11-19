package s3

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ============================= 客户端 - 开始 =============================

// Config - 数据面装配参数（由根包 ios.Client 填充）
type Config struct {
	// Endpoint - S3 网关地址（如 https://storage.example.com:9000，末尾斜杠已裁剪）
	Endpoint string
	// Region - 区域（默认 us-east-1，与平台网关配置一致）
	Region string
	// AccessKey - AccessKey
	AccessKey string
	// Secret - SecretKey
	Secret string
	// SessionToken - STS 会话令牌（空 = 永久凭证）
	SessionToken string
	// HTTPClient - 自定义 HTTP 客户端（nil 时内置无整体超时的客户端，超时交 ctx 控制）
	HTTPClient *http.Client
	// UserAgent - 请求 User-Agent
	UserAgent string
}

// Client - 数据面轻客户端（无内部可变状态，可并发使用）
type Client struct {
	// endpoint - 网关地址（已裁剪末尾 /）
	endpoint string
	// host - 网关主机（含端口，SigV4 host 头）
	host string
	// scheme - 协议（http/https）
	scheme string
	// region - 区域
	region string
	// creds - 签名凭据
	creds credentials
	// client - HTTP 客户端
	client *http.Client
	// userAgent - 请求 User-Agent
	userAgent string
	// now - 签名时间来源（测试可注入）
	now func() time.Time
}

// New - 装配数据面客户端（零网络请求，纯结构装配）
func New(config Config) (*Client, error) {

	endpoint := strings.TrimRight(strings.TrimSpace(config.Endpoint), "/")
	if endpoint == "" {
		return nil, fmt.Errorf("ios: S3 网关地址不能为空")
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("ios: S3 网关地址不合法（%q）", config.Endpoint)
	}

	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		// 数据面是大对象流式链路，不设整体超时（取消与截止由调用方 ctx 控制）
		httpClient = &http.Client{}
	}

	return &Client{
		endpoint:  endpoint,
		host:      parsed.Host,
		scheme:    parsed.Scheme,
		region:    region,
		creds:     credentials{accessKey: config.AccessKey, secret: config.Secret, sessionToken: config.SessionToken},
		client:    httpClient,
		userAgent: config.UserAgent,
		now:       time.Now,
	}, nil
}

// requestInput - 一次数据面调用的输入
type requestInput struct {
	// method - HTTP 方法（大写）
	method string
	// bucket - 桶名（空 = 服务级操作）
	bucket string
	// key - 对象键
	key string
	// query - 查询参数
	query map[string]string
	// headers - 额外请求头
	headers map[string]string
	// body - 请求体（nil = 无 body，payload 取空哈希）
	body io.Reader
	// unsigned - 载荷不签名（流式上传；nil body 时强制空哈希）
	unsigned bool
}

// do - 装配请求 → SigV4 签名 → 执行 → 错误归一（成功响应由调用方继续消费 Body）
func (this *Client) do(ctx context.Context, in requestInput) (*http.Response, error) {

	path := escapePath(in.bucket, in.key)

	payloadHash := emptyPayloadHash
	if in.body != nil {
		payloadHash = unsignedPayload
	}

	address := this.endpoint + path
	if in.query != nil {
		if encoded := encodeQuery(in.query); encoded != "" {
			address += "?" + encoded
		}
	}

	request, err := http.NewRequestWithContext(ctx, in.method, address, in.body)
	if err != nil {
		return nil, fmt.Errorf("%w：构造 %s 请求失败（%v）", ErrTransport, in.method, err)
	}

	signed := this.creds.sign(signInput{
		method:      in.method,
		host:        this.host,
		path:        path,
		query:       in.query,
		payloadHash: payloadHash,
		now:         this.now(),
		region:      this.region,
	})
	for key, value := range signed {
		request.Header.Set(key, value)
	}
	for key, value := range in.headers {
		request.Header.Set(key, value)
	}
	if this.userAgent != "" {
		request.Header.Set("User-Agent", this.userAgent)
	}

	response, err := this.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w：S3 请求失败（%v）", ErrTransport, err)
	}

	if response.StatusCode >= 300 {
		defer func() { _ = response.Body.Close() }()
		return nil, decodeError(response)
	}

	return response, nil
}

// encodeQuery - 查询串编码（RFC3986，与 SigV4 canonical 同形态，保证签名与实际发送一致）
func encodeQuery(query map[string]string) string {

	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(uriEncode(key, true))
		builder.WriteByte('=')
		builder.WriteString(uriEncode(query[key], true))
	}
	return builder.String()
}

// ============================= 客户端 - 结束 =============================
