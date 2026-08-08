package pay

import (
	"context"
	"net/http"
	"time"
)

// Clock - 可注入时钟
type Clock interface{ Now() time.Time }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// LogRecord - 已按白名单构造的支付日志记录
type LogRecord struct {
	// Level - 日志级别
	Level string
	// Provider - Provider 名称
	Provider string
	// Operation - 操作名称
	Operation string
	// OutNoHash - 业务号脱敏摘要
	OutNoHash string
	// Code - 网关错误码
	Code string
	// Outcome - 资金结果确定性
	Outcome Outcome
	// Retryable - 技术故障是否可能恢复
	Retryable bool
	// Duration - 操作耗时
	Duration time.Duration
}

// Logger - 支付包窄日志接口
type Logger interface {
	Log(ctx context.Context, record LogRecord)
}

// Observation - 支付操作观测记录
type Observation struct {
	// Phase - start 或 end 阶段
	Phase string
	// Provider - Provider 名称
	Provider string
	// Operation - 操作名称
	Operation string
	// OutNoHash - 业务号脱敏摘要
	OutNoHash string
	// Code - 网关错误码
	Code string
	// Outcome - 资金结果确定性
	Outcome Outcome
	// Retryable - 技术故障是否可能恢复
	Retryable bool
	// Duration - 操作耗时
	Duration time.Duration
}

// Observer - 支付操作观测接口
type Observer interface {
	Observe(ctx context.Context, observation Observation)
}

// OpenOptions - Provider 构造选项
type OpenOptions struct {
	// Sandbox - 是否使用沙箱环境
	Sandbox bool
	// Client - 可注入 HTTP 客户端
	Client *http.Client
	// Logger - 窄日志接口
	Logger Logger
	// Timeout - 默认请求超时
	Timeout time.Duration
	// Clock - 可注入时钟
	Clock Clock
	// SecretResolver - 敏感值引用解析器
	SecretResolver SecretResolver
	// Observer - 操作观测接口
	Observer Observer
	// RawCapture - 原始报文捕获策略
	RawCapture RawCapturePolicy
	// NotifyMaxBody - 通知请求体字节上限
	NotifyMaxBody int64
	// NotifyClockSkew - 通知时间戳最大允许偏差
	NotifyClockSkew time.Duration
	// SchemaVersion - Provider 动态配置结构版本
	SchemaVersion uint16
}

// Option - Provider 构造选项函数
type Option func(*OpenOptions)

// WithSandbox - 设置沙箱模式
func WithSandbox(value bool) Option { return func(options *OpenOptions) { options.Sandbox = value } }

// WithHTTPClient - 注入 HTTP 客户端
func WithHTTPClient(client *http.Client) Option {
	return func(options *OpenOptions) { options.Client = client }
}

// WithLogger - 注入窄日志接口
func WithLogger(logger Logger) Option { return func(options *OpenOptions) { options.Logger = logger } }

// WithTimeout - 设置 Provider 默认请求超时
func WithTimeout(timeout time.Duration) Option {
	return func(options *OpenOptions) { options.Timeout = timeout }
}

// WithClock - 注入时钟
func WithClock(clock Clock) Option { return func(options *OpenOptions) { options.Clock = clock } }

// WithSecretResolver - 注入敏感值引用解析器
func WithSecretResolver(resolver SecretResolver) Option {
	return func(options *OpenOptions) { options.SecretResolver = resolver }
}

// WithObserver - 注入观测接口
func WithObserver(observer Observer) Option {
	return func(options *OpenOptions) { options.Observer = observer }
}

// WithRawCapture - 设置原始报文捕获策略
func WithRawCapture(policy RawCapturePolicy) Option {
	return func(options *OpenOptions) { options.RawCapture = policy }
}

// WithNotifyLimits - 设置通知体积与时间偏差限制
func WithNotifyLimits(maxBody int64, clockSkew time.Duration) Option {
	return func(options *OpenOptions) { options.NotifyMaxBody, options.NotifyClockSkew = maxBody, clockSkew }
}

// WithSchemaVersion - 设置动态配置结构版本
func WithSchemaVersion(version uint16) Option {
	return func(options *OpenOptions) { options.SchemaVersion = version }
}

func normalizeOpenOptions(options []Option) OpenOptions {
	result := OpenOptions{Timeout: 15 * time.Second, Clock: systemClock{}, RawCapture: RawCapturePolicy{Mode: RawCaptureNone, MaxBytes: 32 << 10}, NotifyMaxBody: 1 << 20, NotifyClockSkew: 5 * time.Minute, SchemaVersion: 1}
	for _, option := range options {
		if option != nil {
			option(&result)
		}
	}
	if result.Timeout <= 0 {
		result.Timeout = 15 * time.Second
	}
	if result.Clock == nil {
		result.Clock = systemClock{}
	}
	if result.NotifyMaxBody <= 0 {
		result.NotifyMaxBody = 1 << 20
	}
	if result.NotifyClockSkew <= 0 {
		result.NotifyClockSkew = 5 * time.Minute
	}
	if result.RawCapture.MaxBytes <= 0 {
		result.RawCapture.MaxBytes = 32 << 10
	}
	if result.Client == nil {
		result.Client = defaultHTTPClient(result.Timeout)
	}
	return result
}

func defaultHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 0 && request.URL.Hostname() != via[0].URL.Hostname() {
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			request.Header.Del("Wechatpay-Signature")
			request.Header.Del("PayPal-Auth-Algo")
		}
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}
