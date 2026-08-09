package licence

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// Transport - SDK 到 Licen Hub 的传输协议。
type Transport string

const (
	// TransportHTTP - HTTP JSON 传输，兼容默认值。
	TransportHTTP Transport = "http"
	// TransportGRPC - 原生 gRPC 传输。
	TransportGRPC Transport = "grpc"
)

// GRPCOptions - gRPC 连接配置。
type GRPCOptions struct {
	// AllowInsecure - 允许明文 h2c，仅限开发或受控内网。
	AllowInsecure bool
	// TLSConfig - 自定义 TLS 配置；为空时按 ServerURL 主机名构造安全默认值。
	TLSConfig *tls.Config
	// Authority - 可选 HTTP/2 authority 覆盖。
	Authority string
	// DialTimeout - 首次 RPC/连接超时，默认沿用 HTTPTimeout。
	DialTimeout time.Duration
	// MaxReceiveMessageSize - 最大接收消息字节数，默认 16 MiB。
	MaxReceiveMessageSize int
}

// Options - 运行面客户端配置（ServerURL 为平台 URI 唯一入口）
type Options struct {
	// ServerURL - 授权平台地址（必填），如 "https://license.example.com"
	ServerURL string
	// LicenseNo - 许可证编号（必填），格式 LIC-{年}-%06d
	LicenseNo string
	// InstanceNo - 部署实例编号（可选，许可证绑定实例时上送）
	InstanceNo string
	// Salt - 指纹盐（必填，与实例登记时使用的盐一致）
	Salt string
	// PublicKeys - 验签公钥表（必填，keyVersion -> hex 公钥，支持多版本并存轮换）
	PublicKeys map[string]string
	// ReleasePublicKeys - release 验签公钥表（在线更新模块必填，keyVersion -> hex 公钥）
	ReleasePublicKeys map[string]string
	// StorageDir - 状态存储目录（默认 ./runtime/licence）
	StorageDir string
	// Fingerprint - 显式指纹哈希（可选，覆盖自动采集）
	Fingerprint string
	// Provider - 自定义指纹提供者（可选，容器/云主机场景）
	Provider FingerprintProvider
	// Store - 自定义安全存储（可选，默认 AES-GCM 加密文件）
	Store Store
	// Version - 当前项目版本（随 validate 上送，用于版本范围判定与实例版本回写）
	Version string
	// RefreshInterval - 校验刷新间隔（默认 12 小时，建议 12~24 小时）
	RefreshInterval time.Duration
	// HTTPTimeout - 单次请求超时（默认 15 秒）
	HTTPTimeout time.Duration
	// Transport - 传输协议，零值默认 HTTP。
	Transport Transport
	// GRPC - gRPC 连接配置，仅 TransportGRPC 时生效。
	GRPC GRPCOptions
	// OnStatusChange - 状态变化回调（可选，放行与降级策略在此挂接）
	OnStatusChange func(oldStatus string, newStatus string)
}

// runtimeState - 持久化运行状态（加密存储，token 仅此一份）
type runtimeState struct {
	// ActivationToken - 激活令牌（仅 activate 返回一次，丢失只能重新激活）
	ActivationToken string `json:"activationToken"`
	// ClientSeed - 客户端签名私钥种子（hex，不出本机）
	ClientSeed string `json:"clientSeed"`
	// ActivationNo - 激活编号
	ActivationNo string `json:"activationNo"`
	// ExpiresAt - 激活有效期截止（毫秒，滑动窗口）
	ExpiresAt int64 `json:"expiresAt"`
	// Status - 最近一次服务端判定状态
	Status string `json:"status"`
	// Envelope - 当前缓存信封原文（验签基于载荷原文）
	Envelope json.RawMessage `json:"envelope"`
	// Configs - 已验签的项目配置快照
	Configs map[string]ConfigItem `json:"configs,omitempty"`
	// ConfigSyncVersion - 项目配置增量同步水位
	ConfigSyncVersion int `json:"configSyncVersion,omitempty"`
	// PlatformConfigs - 已验签的平台配置快照（按 Key）
	PlatformConfigs map[string]PlatformConfigItem `json:"platformConfigs,omitempty"`
	// PlatformConfigSyncVersion - 平台配置增量同步水位
	PlatformConfigSyncVersion int `json:"platformConfigSyncVersion,omitempty"`
}

// Client - 运行面客户端（激活/校验/验签/缓存/降级一体化）
// 通过 New 创建，Start 启动后台刷新循环，Stop 停止。
type Client struct {
	// options - 归一化后的配置
	options Options
	// transport - HTTP/gRPC 运行面传输层
	transport runtimeTransport
	// store - 安全存储
	store Store
	// fingerprint - 实例指纹哈希
	fingerprint string

	// opMu - 运行面操作互斥（activate/validate/current 串行）
	opMu sync.Mutex
	// mu - 状态读写锁
	mu sync.RWMutex
	// state - 运行状态
	state runtimeState
	// envelope - 当前缓存信封（已验签）
	envelope Envelope
	// clockOffset - 服务端时间偏移（serverTime - 本地毫秒，用于签名时间戳与本地判定）
	clockOffset int64
	// pendingUsage - 待上报用量（下次 validate 携带，服务端确认后清空）
	pendingUsage map[string]int64
	// tenantCache - SaaS 租户信封缓存（sync/validate 写入，TenantStatus/TenantFeature 读取）
	tenantCache map[string]tenantCacheItem
	// retryDelay - 网络故障退避（请求未达服务端时拉长下轮间隔，恢复后清除）
	retryDelay time.Duration

	// cancel - 后台循环取消函数
	cancel context.CancelFunc
}

// New - 创建运行面客户端（归一化配置 + 采集指纹 + 初始化存储，不发起网络请求）
/**
 * @param options Options - 客户端配置（ServerURL/LicenseNo/Salt/PublicKeys 必填）
 * @return *Client - 客户端实例
 * @example：
 * 	client, err := licence.New(licence.Options{
 * 		ServerURL: "https://license.example.com", LicenseNo: "LIC-2026-000123",
 * 		Salt: "my-project-salt", PublicKeys: map[string]string{"license-key-2026-01": "..."},
 * 	})
 */
func New(options Options) (*Client, error) {

	if options.ServerURL == "" {
		return nil, errors.New("ServerURL 不能为空（平台 URI 入口）")
	}
	if options.LicenseNo == "" {
		return nil, errors.New("LicenseNo 不能为空")
	}
	if options.Salt == "" {
		return nil, errors.New("Salt 不能为空（指纹盐，与实例登记一致）")
	}
	if len(options.PublicKeys) == 0 {
		return nil, errors.New("PublicKeys 不能为空（至少内置一个 keyVersion 的验签公钥）")
	}
	if options.StorageDir == "" {
		options.StorageDir = "./runtime/licence"
	}
	if options.RefreshInterval <= 0 {
		options.RefreshInterval = 12 * time.Hour
	}
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = 15 * time.Second
	}

	fingerprint, err := FingerprintHash(options.Salt, options.Fingerprint, options.Provider)
	if err != nil {
		return nil, err
	}

	store := options.Store
	if store == nil {
		store, err = newFileStore(options.StorageDir, options.LicenseNo, options.Salt, fingerprint)
		if err != nil {
			return nil, err
		}
	}

	client := &Client{
		options: options,
		store:   store, fingerprint: fingerprint,
		state: runtimeState{
			Configs:         make(map[string]ConfigItem),
			PlatformConfigs: make(map[string]PlatformConfigItem),
		},
		pendingUsage: make(map[string]int64),
		tenantCache:  make(map[string]tenantCacheItem),
	}
	client.transport, err = newRuntimeTransport(client)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Start - 启动客户端：恢复本地状态或执行首激活，随后进入后台滑动刷新循环
// 有可用缓存（验签通过且在宽限内）时即使平台不可达也能降级启动；
// 无缓存且激活失败时返回错误。
func (this *Client) Start(ctx context.Context) error {

	if err := this.restore(); err != nil {
		return err
	}

	// 无有效凭证：同步执行首激活（失败且无缓存可降级时返回错误）
	this.mu.RLock()
	hasToken := this.state.ActivationToken != ""
	hasEnvelope := this.envelope.Payload.LicenseId != ""
	this.mu.RUnlock()

	if !hasToken {
		this.opMu.Lock()
		err := this.activateLocked(ctx)
		this.opMu.Unlock()
		if err != nil && !hasEnvelope {
			return err
		}
	}

	loopCtx, cancel := context.WithCancel(context.Background())
	this.cancel = cancel
	go this.loop(loopCtx)
	return nil
}

// Stop - 停止后台刷新循环
func (this *Client) Stop() {
	if this.cancel != nil {
		this.cancel()
	}
}

// Close - 关闭后台刷新与底层 HTTP/gRPC 连接，可重复调用。
func (this *Client) Close() error {
	this.Stop()
	if this.transport == nil {
		return nil
	}
	return this.transport.Close()
}

// Status - 当前授权状态（VALID/EXPIRING/GRACE/...，见 status.go 常量）
func (this *Client) Status() string {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.state.Status
}

// Envelope - 当前缓存信封（第二返回值标识是否存在）
func (this *Client) Envelope() (Envelope, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.envelope, this.envelope.Payload.LicenseId != ""
}

// HasFeature - 功能权益闸门：放行状态且载荷 features[code] 为 true
func (this *Client) HasFeature(code string) bool {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return passThrough(this.state.Status) && this.envelope.Payload.Features[code]
}

// GetLimit - 额度查询：返回载荷 limits[key]（未配置返回 0, false）
func (this *Client) GetLimit(key string) (int64, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	value, exist := this.envelope.Payload.Limits[key]
	return value, exist
}

// CheckVersion - 版本范围本地判定（与平台 version-range 语义一致，空范围 = 不限制）
func (this *Client) CheckVersion(version string) bool {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return VersionInRange(version, this.envelope.Payload.VersionRange)
}

// ReportUsage - 用量上报：合并进待上报表，随下次 validate 携带，服务端确认后清空
func (this *Client) ReportUsage(usage map[string]int64) {
	this.mu.Lock()
	defer this.mu.Unlock()
	for key, value := range usage {
		this.pendingUsage[key] = value
	}
}

// Reactivate - 重新激活（换机/令牌丢失/EXPIRED 引导路径）：
// 生成新客户端密钥对并重新绑定，旧公钥随旧激活记录失效
func (this *Client) Reactivate(ctx context.Context) error {

	this.opMu.Lock()
	defer this.opMu.Unlock()
	this.mu.Lock()
	this.state.ActivationToken = ""
	this.state.ClientSeed = ""
	this.mu.Unlock()
	return this.activateLocked(ctx)
}

// Current - 按需拉取当前生效信封（不做滑动刷新；失败返回错误，不影响本地缓存）
func (this *Client) Current(ctx context.Context) (Envelope, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()
	return this.currentLocked(ctx)
}

// loop - 后台刷新循环：立即执行一轮，之后按刷新间隔（带抖动）周期性校验
func (this *Client) loop(ctx context.Context) {

	this.tick(ctx)
	for {
		timer := time.NewTimer(this.nextDelay())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			this.tick(ctx)
		}
	}
}

// tick - 单轮刷新：有凭证走 validate，凭证缺失/失效走 activate
func (this *Client) tick(ctx context.Context) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	this.mu.RLock()
	hasToken := this.state.ActivationToken != ""
	this.mu.RUnlock()

	if !hasToken {
		_ = this.activateLocked(ctx)
		return
	}
	_ = this.validateLocked(ctx)
}

// nextDelay - 下一次刷新延迟：刷新间隔 ±10% 抖动（避免整点惊群）；
// 网络故障时按退避间隔重试；激活有效期剩余不足时提前（下限 1 秒）
func (this *Client) nextDelay() time.Duration {

	this.mu.RLock()
	retryDelay := this.retryDelay
	expiresAt := this.state.ExpiresAt
	this.mu.RUnlock()

	delay := this.options.RefreshInterval
	if retryDelay > 0 {
		delay = retryDelay
	} else {
		jitter := int64(delay / 10)
		if jitter > 0 {
			delay += time.Duration(rand.Int64N(2*jitter) - jitter)
		}
	}

	if expiresAt > 0 {
		if remain := time.Duration(expiresAt-this.now()) * time.Millisecond; remain > 0 && remain < delay {
			delay = remain / 2
		}
	}
	if delay <= 0 {
		delay = time.Second
	}
	return delay
}

// now - 校时后的当前毫秒（本地时间 + 服务端偏移，用于签名时间戳与本地判定）
func (this *Client) now() int64 {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return time.Now().UnixMilli() + this.clockOffset
}

// setStatus - 更新状态并触发变化回调（回调在锁外执行）
func (this *Client) setStatus(status string) {

	this.mu.Lock()
	old := this.state.Status
	if old != status {
		this.state.Status = status
	}
	this.mu.Unlock()

	if old != status && this.options.OnStatusChange != nil {
		this.options.OnStatusChange(old, status)
	}
}

// offline - 离线/服务端故障降级：依据缓存信封做本地时间维度判定（契约 §5）
func (this *Client) offline() {

	this.mu.RLock()
	validUntil := this.envelope.Payload.ValidUntil
	graceDays := this.envelope.Payload.GraceDays
	hasEnvelope := this.envelope.Payload.LicenseId != ""
	this.mu.RUnlock()

	if !hasEnvelope {
		return
	}
	this.setStatus(localStatus(this.now(), validUntil, graceDays))
}

// restore - 恢复本地状态：读存储 → 验签信封 → 恢复凭证与缓存
func (this *Client) restore() error {

	raw, err := this.store.Load()
	if err != nil || raw == nil {
		return err
	}
	var state runtimeState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}

	envelope, rawPayload, err := ParseEnvelope(state.Envelope)
	if err != nil {
		return err
	}
	publicKey, exist := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exist || !Licence.VerifyRaw(rawPayload, envelope.Signature, publicKey) {
		return errors.New("本地缓存信封验签失败")
	}

	this.mu.Lock()
	if state.Configs == nil {
		state.Configs = make(map[string]ConfigItem)
	}
	if state.PlatformConfigs == nil {
		state.PlatformConfigs = make(map[string]PlatformConfigItem)
	}
	this.state = state
	this.envelope = envelope
	this.mu.Unlock()

	// 启动即按本地时间维度给出初始状态（等待首轮服务端判定修正）
	this.offline()
	return nil
}

// persist - 持久化当前状态（加密写入；失败不阻断运行，下轮覆盖）
func (this *Client) persist() {

	this.mu.RLock()
	raw, err := json.Marshal(this.state)
	this.mu.RUnlock()
	if err == nil {
		_ = this.store.Save(raw)
	}
}
