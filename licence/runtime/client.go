package runtime

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/inis-io/aide/licence/apis"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	// ProjectNo - 项目编号（使用 BootstrapPublicKeys 动态验 keyset 时必填，精确绑定项目）
	ProjectNo string
	// LicenseNo - 许可证编号（必填），格式 LIC-{年}-%06d
	LicenseNo string
	// InstanceNo - 部署实例编号（可选，许可证绑定实例时上送）
	InstanceNo string
	// DeviceName - 机器名称（可选，仅供席位列表展示；缺省取 os.Hostname）
	DeviceName string
	// Salt - 指纹盐（必填，与实例登记时使用的盐一致）
	Salt string
	// PublicKeys - 静态 license 验签公钥表（与 BootstrapPublicKeys 至少一种，支持多版本并存轮换）
	PublicKeys map[string]string
	// ReleasePublicKeys - 静态 release 验签公钥表（在线更新模块可使用动态 keyset 替代）
	ReleasePublicKeys map[string]string
	// BootstrapPublicKeys - bootstrap 根公钥表（keyVersion -> hex 公钥），用于验证动态 keyset
	BootstrapPublicKeys map[string]string
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
	// DisableConsumptionReport - 关闭平台配置读取埋点上报（默认 false=开启）。
	// 开启时 PlatformConfig 读命中会累计计数并异步上报平台，供管理端「运行面消费」展示。
	DisableConsumptionReport bool
	// ConsumptionReportInterval - 消费埋点上报间隔（默认 30 秒；累计达 50 个 key 时提前触发）
	ConsumptionReportInterval time.Duration
	// CommandPullTimeout - 指令长轮询 hold 时长（默认 25 秒；服务端硬上限 30 秒，超出自动收敛；
	// 客户端请求超时始终大于 hold）
	CommandPullTimeout time.Duration
	// CommandHandlers - 自定义指令动作处理器（与内置动作同名时覆盖内置；
	// handler 须幂等——回执失败平台会重新下发同一指令）
	CommandHandlers map[string]CommandHandler
	// DisableCommands - 关闭实例指令通道（默认 false=开启；为 true 不启动指令循环）
	DisableCommands bool
}

// runtimeState - 持久化运行状态（加密存储，token 仅此一份）
type runtimeState struct {
	// ActivationToken - 激活令牌（仅 activate 返回一次，丢失只能重新激活）
	ActivationToken string `json:"activationToken"`
	// ClientSeed - 客户端签名私钥种子（hex，不出本机）
	ClientSeed string `json:"clientSeed"`
	// ActivationNo - 激活编号
	ActivationNo string `json:"activationNo"`
	// SeatNo - 当前机器席位编号（仅展示/排障）
	SeatNo string `json:"seatNo,omitempty"`
	// ExpiresAt - 激活有效期截止（毫秒，滑动窗口）
	ExpiresAt int64 `json:"expiresAt"`
	// Status - 最近一次服务端判定状态
	Status string `json:"status"`
	// Envelope - 当前缓存信封原文（验签基于载荷原文）
	Envelope json.RawMessage `json:"envelope"`
	// Keyset - 已验证的项目工作公钥集合原文（bootstrap 根签名）
	Keyset json.RawMessage `json:"keyset,omitempty"`
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
	envelope LicenceProtocol.Envelope
	// keyset - 当前可信工作公钥集合（已验 bootstrap 根）
	keyset LicenceProtocol.KeysetEnvelope
	// clockOffset - 服务端时间偏移（serverTime - 本地毫秒，用于签名时间戳与本地判定）
	clockOffset int64
	// pendingUsage - 待上报用量（下次 validate 携带，服务端确认后清空）
	pendingUsage map[string]int64
	// tenantCache - SaaS 租户信封缓存（sync/validate 写入，TenantStatus/TenantFeature 读取）
	tenantCache map[string]tenantCacheItem
	// consumptionMu - 消费埋点互斥（PlatformConfig 读取累计与后台 flush 并发安全）
	consumptionMu sync.Mutex
	// pendingConsumption - 待上报平台配置消费计数（key -> 累计读取次数）
	pendingConsumption map[string]int
	// flushing - 消费上报是否在跑（防并发 flush，阈值提前触发时用）
	flushing atomic.Bool
	// retryDelay - 网络故障退避（请求未达服务端时拉长下轮间隔，恢复后清除）
	retryDelay time.Duration
	// startedAt - 客户端创建时间（指令 runtime_info 的 uptime 统计起点）
	startedAt time.Time

	// Apis - API 商城 typed 方法包挂载点（apis 子包；New 时构造，生命周期跟随本 Client）
	Apis *apis.Client

	// Star - 星链中枢运行面挂载点（New 时构造，生命周期跟随本 Client；
	// 实例身份缺省锚定 Options.InstanceNo）
	Star *StarService

	// cancel - 后台循环取消函数
	cancel context.CancelFunc
}

// New - 创建运行面客户端（归一化配置 + 采集指纹 + 初始化存储，不发起网络请求）
/**
 * @param options Options - 客户端配置（ServerURL/LicenseNo/Salt 必填，工作公钥或 bootstrap 根至少一种）
 * @return *Client - 客户端实例
 * @example：
 * 	client, err := licence.New(licence.Options{
 * 		ServerURL: "https://license.example.com", LicenseNo: "LIC-2026-000123",
 * 		Salt: "my-project-salt", PublicKeys: map[string]string{"license-key-INIS-202608-1a2b3c": "..."},
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
	if len(options.PublicKeys) == 0 && len(options.BootstrapPublicKeys) == 0 {
		return nil, errors.New("PublicKeys 与 BootstrapPublicKeys 至少配置一种")
	}
	if len(options.BootstrapPublicKeys) > 0 && strings.TrimSpace(options.ProjectNo) == "" {
		return nil, errors.New("使用 BootstrapPublicKeys 时 ProjectNo 不能为空")
	}
	if options.StorageDir == "" {
		options.StorageDir = "./runtime/licence"
	}
	if options.DeviceName == "" {
		options.DeviceName, _ = os.Hostname()
	}
	options.DeviceName = strings.TrimSpace(options.DeviceName)
	if len(options.DeviceName) > 128 {
		options.DeviceName = options.DeviceName[:128]
	}
	if options.RefreshInterval <= 0 {
		options.RefreshInterval = 12 * time.Hour
	}
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = 15 * time.Second
	}
	if options.ConsumptionReportInterval <= 0 {
		options.ConsumptionReportInterval = 30 * time.Second
	}
	if options.CommandPullTimeout <= 0 {
		options.CommandPullTimeout = commandDefaultHold
	}
	// 服务端 timeout_ms 硬上限 30000，hold 超出无意义（客户端请求超时随 hold 放大，恒大于 hold）
	if options.CommandPullTimeout > commandMaxHold {
		options.CommandPullTimeout = commandMaxHold
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
		startedAt: time.Now(),
		state: runtimeState{
			PlatformConfigs: make(map[string]PlatformConfigItem),
		},
		pendingUsage:       make(map[string]int64),
		tenantCache:        make(map[string]tenantCacheItem),
		pendingConsumption: make(map[string]int),
	}
	client.Apis = apis.New(apisDoer{client: client})
	client.Star = &StarService{client: client}
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
	if !this.options.DisableConsumptionReport {
		go this.consumptionLoop(loopCtx)
	}
	if !this.options.DisableCommands {
		go this.commandLoop(loopCtx)
	}
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

// SeatNo - 返回当前机器的席位编号；尚未激活或历史服务端未返回时为空。
func (this *Client) SeatNo() string {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.state.SeatNo
}

// DeviceName - 实际生效的机器名称（Options.DeviceName 或其缺省主机名，New 时已归一化）。
// 与激活上送、平台席位列表展示的值一致，仅展示/排障用途。
func (this *Client) DeviceName() string {
	return this.options.DeviceName
}

// StorageDir - 本地安全存储根目录（Options.StorageDir，New 时已归一化）。
// 供 updater 等子包定位 update/ 工作区，替代直接读私有 options。
func (this *Client) StorageDir() string {
	return this.options.StorageDir
}

// Version - 当前运行版本（Options.Version），更新检查与防降级判定的来源版本。
func (this *Client) Version() string {
	return this.options.Version
}

// Envelope - 当前缓存信封（第二返回值标识是否存在）
func (this *Client) Envelope() (LicenceProtocol.Envelope, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.envelope, this.envelope.Payload.LicenseId != ""
}

// GetBool - 读取布尔型权益（项目模块 valueType=bool）：返回载荷 entitlements[code]。
// 键缺省或值不是布尔时返回 (false, false)；本方法只读载荷，不含授权状态闸门，
// 需要「授权放行才开放功能」的调用方必须同时判断 Status()。
func (this *Client) GetBool(code string) (bool, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	value, exist := this.envelope.Payload.Entitlements[code]
	if !exist {
		return false, false
	}
	boolean, ok := value.(bool)
	return boolean, ok
}

// GetInt - 读取整数型权益（项目模块 valueType=int）：返回载荷 entitlements[code]。
// 键缺省、值不是数值或数值含小数时返回 (0, false)；整数值的 float64 可无损转换。
func (this *Client) GetInt(code string) (int64, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	number, ok := entitlementNumber(this.envelope.Payload.Entitlements[code])
	if !ok || number != math.Trunc(number) {
		return 0, false
	}
	return int64(number), true
}

// GetFloat - 读取数值型权益（项目模块 valueType=int/float）：返回载荷 entitlements[code]。
// 键缺省或值不是数值时返回 (0, false)；整数与小数同样接受。
func (this *Client) GetFloat(code string) (float64, bool) {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return entitlementNumber(this.envelope.Payload.Entitlements[code])
}

// entitlementNumber - 权益数值归一：JSON 解析结果一律为 float64，
// 同时兼容进程内直接构造的 int / int64，其余类型视为类型不符。
func entitlementNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		return number, true
	}
	return 0, false
}

// CheckVersion - 版本范围本地判定（与平台 version-range 语义一致，空范围 = 不限制）
func (this *Client) CheckVersion(version string) bool {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return LicenceProtocol.VersionInRange(version, this.envelope.Payload.VersionRange)
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

// CustomerBindResult - 客户账号绑定回执（平台下发的脱敏客户视图）。
type CustomerBindResult struct {
	// MaskAccount - 脱敏账号（如 r***@x.com）
	MaskAccount string
	// BoundAt - 绑定时间（毫秒）
	BoundAt int64
}

// customerBindBody - 客户账号绑定请求体（一次性绑定码由 licen-hub 客户门户签发）。
type customerBindBody struct {
	Code string `json:"code"`
}

// customerBindResponse - 客户账号绑定统一响应（双协议同形：HTTP 为平台原文，gRPC 由传输层合成）。
type customerBindResponse struct {
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	Customer *customerViewJSON `json:"customer,omitempty"`
}

// CustomerBind - 客户账号绑定（一次性绑定码；凭证族与 activate 同级，实例签名防重放）。
// 未激活（无 token）直接报错且不发请求；成功返回脱敏客户视图，业务拒绝（如 BIND_INVALID）
// 以 *ProvisionError 原样透传，errors.As 断言后可按 Status 降级。
/**
 * @param ctx context.Context - 调用上下文
 * @param code string - 一次性绑定码
 * @return CustomerBindResult - 绑定后的脱敏客户视图（脱敏账号 + 绑定时间）
 * @example：
 * 	result, err := client.CustomerBind(ctx, "ABCD-EFGH")
 */
func (this *Client) CustomerBind(ctx context.Context, code string) (CustomerBindResult, error) {

	this.mu.RLock()
	token := this.state.ActivationToken
	this.mu.RUnlock()
	if token == "" {
		return CustomerBindResult{}, errors.New("客户端未激活，无法绑定客户账号")
	}
	body, err := json.Marshal(customerBindBody{Code: code})
	if err != nil {
		return CustomerBindResult{}, err
	}
	statusCode, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/licenses/customer-bind", body, true)
	if err != nil {
		return CustomerBindResult{}, err
	}
	// 非 200 不解析 body：只把 404（含 gRPC NotFound 经传输层归一）按模糊语义锚定
	// 「许可证或实例信息无效」；其余状态码（含网关 502/504 的空 body）一律返回传输层错误，
	// 不把非凭证故障误报成凭证终态，也不把空 body 的 JSON 解析错误抛给调用方
	if statusCode != http.StatusOK {
		if statusCode == http.StatusNotFound {
			return CustomerBindResult{}, errors.New("许可证或实例信息无效")
		}
		return CustomerBindResult{}, fmt.Errorf("客户绑定请求失败（HTTP %d）", statusCode)
	}
	var response customerBindResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return CustomerBindResult{}, err
	}
	if response.Status != "OK" {
		status := response.Status
		if status == "" {
			status = "ERROR"
		}
		return CustomerBindResult{}, &ProvisionError{Status: status, Message: response.Message}
	}
	result := CustomerBindResult{}
	if response.Customer != nil {
		result.MaskAccount = response.Customer.MaskAccount
		result.BoundAt = response.Customer.BoundAt
	}
	return result, nil
}

// Reset - 清除本机运行状态，不通知平台释放席位。
// 下次 Start 会以同一指纹重新激活，并由服务端复用或重新占用原席位。
func (this *Client) Reset() error {
	this.opMu.Lock()
	defer this.opMu.Unlock()
	this.mu.Lock()
	this.state = runtimeState{PlatformConfigs: make(map[string]PlatformConfigItem)}
	this.envelope = LicenceProtocol.Envelope{}
	this.keyset = LicenceProtocol.KeysetEnvelope{}
	this.clockOffset = 0
	clear(this.pendingUsage)
	clear(this.tenantCache)
	this.retryDelay = 0
	this.mu.Unlock()
	return this.store.Clear()
}

// Current - 按需拉取当前生效信封（不做滑动刷新；失败返回错误，不影响本地缓存）
func (this *Client) Current(ctx context.Context) (LicenceProtocol.Envelope, error) {

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
	status := this.state.Status
	this.mu.RUnlock()

	if status == LicenceProtocol.StatusSeatLimitExceeded || status == LicenceProtocol.StatusSeatReleased {
		return
	}
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
	this.setStatus(LicenceProtocol.LocalStatus(this.now(), validUntil, graceDays))
}

// restore - 恢复本地状态：读存储 → 先验缓存 keyset 根 → 再验信封 → 恢复凭证与缓存。
func (this *Client) restore() error {

	raw, err := this.store.Load()
	if err != nil || raw == nil {
		return err
	}
	var state runtimeState
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	// 旧版本在首次激活被拒绝时曾落盘不含信封的半成品状态。
	// 该状态不具备离线恢复价值，按未激活处理并清理，避免解析空信封卡死启动。
	rawEnvelope := bytes.TrimSpace(state.Envelope)
	if (len(rawEnvelope) == 0 || bytes.Equal(rawEnvelope, []byte("null"))) && state.Status != LicenceProtocol.StatusSeatReleased {
		return this.store.Clear()
	}
	rawKeyset := bytes.TrimSpace(state.Keyset)
	if len(rawKeyset) == 0 && len(this.options.BootstrapPublicKeys) > 0 && state.Status != LicenceProtocol.StatusSeatReleased {
		// 静态模式旧状态缺少可验根的 keyset，按陈旧缓存清理并落入重新激活。
		return this.store.Clear()
	}
	if len(rawKeyset) > 0 && len(this.options.BootstrapPublicKeys) == 0 {
		return errors.New("本地缓存 keyset 缺少 bootstrap 根公钥")
	}
	var candidate *keysetCandidate
	if len(rawKeyset) > 0 || state.Status != LicenceProtocol.StatusSeatReleased {
		candidate, err = this.verifyKeysetCandidate(state.Keyset)
		if err != nil {
			return err
		}
	}
	// SEAT_RELEASED 无业务信封可验，缺少 keyset 时仍须原样保留终态凭证。

	var envelope LicenceProtocol.Envelope
	if len(rawEnvelope) > 0 && !bytes.Equal(rawEnvelope, []byte("null")) {
		var rawPayload []byte
		envelope, rawPayload, err = LicenceProtocol.ParseEnvelope(state.Envelope)
		if err != nil {
			return err
		}
		publicKey, exist := LookupPublicKey(this.candidatePublicKeys(candidate, false), envelope.Payload.KeyVersion)
		if !exist || !LicenceProtocol.Licence.VerifyRaw(rawPayload, envelope.Signature, publicKey) {
			return errors.New("本地缓存信封验签失败")
		}
	}

	this.mu.Lock()
	if state.PlatformConfigs == nil {
		state.PlatformConfigs = make(map[string]PlatformConfigItem)
	}
	this.state = state
	this.envelope = envelope
	this.keyset = LicenceProtocol.KeysetEnvelope{}
	if candidate != nil {
		this.keyset = *candidate.envelope
	}
	this.mu.Unlock()

	// 启动即按本地时间维度给出初始状态（等待首轮服务端判定修正）
	if state.Status != LicenceProtocol.StatusSeatReleased {
		this.offline()
	}
	return nil
}

// persist - 持久化当前状态（加密写入；失败不阻断运行，下轮覆盖）
func (this *Client) persist() {

	this.mu.RLock()
	if (len(this.state.Envelope) == 0 || this.envelope.Payload.LicenseId == "") && this.state.Status != LicenceProtocol.StatusSeatReleased {
		this.mu.RUnlock()
		// 只有已验签的完整信封才能成为可恢复状态。
		// 首次激活被拒绝时不保存 token/客户端私钥等半成品数据。
		_ = this.store.Clear()
		return
	}
	raw, err := json.Marshal(this.state)
	this.mu.RUnlock()
	if err == nil {
		_ = this.store.Save(raw)
	}
}

// ============================= 事件订阅与广播 =============================

// 事件订阅的客户端侧只剩两个原语：PullEvents（一轮拉取，含时钟校正与放行态闸门）与
// PublicKeys（验签公钥副本）。订阅器（水位推进、去重、前缀通配分发）位于 callback 子包
// （callback.EventSubscriber，经 callback.NewEventSubscriber(client, options) 创建）——
// 因订阅器复用 callback.CallbackHandler 分发内核而 callback 又须 import protocol 子包验签，
// 为保持依赖方向（callback → runtime/protocol），订阅器整体迁出，本文件不再提供 Client.Subscribe。

// PullEvents - 一轮事件拉取（HTTP 长轮询单次 POST / gRPC 服务端流收齐），返回按 eventId 升序的事件批次。
// 信封为平台现场重签的 CallbackEnvelope JSON 原文（occurredAt 重新盖戳、nonce 每次新鲜、
// deliveryNo 稳定为 SUB-{eventNo}），消费方基于 payload 原文验签。
// 时钟校正（updateClockOffset）与状态闸门（LicenceProtocol.StatusError/非放行态 → error）在本方法内完成；
// hold <= 0 时默认 15s（传输层再收敛到各自超时）。未 Start（传输层未初始化）返回错误。
// SDK 使用方通常经 callback.NewEventSubscriber 消费本方法，而非直接调用。
func (this *Client) PullEvents(ctx context.Context, sinceEventId int64, hold time.Duration) ([]SubscribedEvent, error) {

	if hold <= 0 {
		hold = 15 * time.Second
	}
	if this.transport == nil {
		return nil, errors.New("运行面传输层未初始化")
	}
	result, err := this.transport.SubscribeEvents(ctx, this.options.LicenseNo, sinceEventId, hold)
	if err != nil {
		return nil, err
	}
	this.updateClockOffset(result.ServerTime)
	if result.Status == LicenceProtocol.StatusError {
		return nil, errors.New("服务端故障：" + result.Message)
	}
	if !LicenceProtocol.PassThrough(result.Status) {
		return nil, errors.New("许可证非放行态：" + result.Status)
	}
	return result.Events, nil
}

// keysetCandidate - 已通过 bootstrap 根验证、尚未提交的 keyset 与派生公钥快照。
type keysetCandidate struct {
	envelope    *LicenceProtocol.KeysetEnvelope
	raw         json.RawMessage
	licenseKeys map[string]string
	releaseKeys map[string]string
}

// mergeKeysetKeys - 合并静态与 keyset 公钥，同版本不同公钥一律 fail-closed。
func mergeKeysetKeys(static map[string]string, items []LicenceProtocol.KeysetPublicKey) (map[string]string, error) {
	keys := make(map[string]string, len(static)+len(items))
	for version, key := range static {
		keys[version] = key
	}
	for _, item := range items {
		if previous, exists := keys[item.KeyVersion]; exists && previous != item.PublicKey {
			return nil, errors.New("同 keyVersion 对应不同公钥: " + item.KeyVersion)
		}
		keys[item.KeyVersion] = item.PublicKey
	}
	return keys, nil
}

// verifyKeysetHistory - 高 revision 也不得改变既有 keyVersion 的公钥。
func verifyKeysetHistory(current, candidate []LicenceProtocol.KeysetPublicKey) error {
	keys := make(map[string]string, len(current))
	for _, item := range current {
		keys[item.KeyVersion] = item.PublicKey
	}
	for _, item := range candidate {
		if previous, exists := keys[item.KeyVersion]; exists && previous != item.PublicKey {
			return errors.New("同 keyVersion 对应不同公钥: " + item.KeyVersion)
		}
	}
	return nil
}

// publicKeysSnapshot - 读取最新加锁快照，始终返回独立副本。
func (this *Client) publicKeysSnapshot(release bool) map[string]string {
	this.mu.RLock()
	defer this.mu.RUnlock()
	static := this.options.PublicKeys
	items := this.keyset.Payload.LicenseKeys
	if release {
		static = this.options.ReleasePublicKeys
		items = this.keyset.Payload.ReleaseKeys
	}
	keys, _ := mergeKeysetKeys(static, items)
	return keys
}

// verifyKeysetCandidate - 验证候选 keyset，但不修改当前可信状态。
func (this *Client) verifyKeysetCandidate(raw json.RawMessage) (*keysetCandidate, error) {
	if len(this.options.BootstrapPublicKeys) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("响应缺少 keyset")
	}
	this.mu.RLock()
	currentRaw := append(json.RawMessage(nil), this.state.Keyset...)
	currentLicenseKeys := append([]LicenceProtocol.KeysetPublicKey(nil), this.keyset.Payload.LicenseKeys...)
	currentReleaseKeys := append([]LicenceProtocol.KeysetPublicKey(nil), this.keyset.Payload.ReleaseKeys...)
	minimumRevision := this.keyset.Payload.Revision
	this.mu.RUnlock()
	envelope, err := LicenceProtocol.VerifyKeyset(trimmed, this.options.ProjectNo, this.options.BootstrapPublicKeys, minimumRevision)
	if err != nil {
		return nil, err
	}
	if envelope.Payload.Revision == minimumRevision && len(currentRaw) > 0 &&
		!bytes.Equal(bytes.TrimSpace(currentRaw), trimmed) {
		return nil, errors.New("同 revision 的 keyset 内容不一致")
	}
	if err = verifyKeysetHistory(currentLicenseKeys, envelope.Payload.LicenseKeys); err != nil {
		return nil, err
	}
	if err = verifyKeysetHistory(currentReleaseKeys, envelope.Payload.ReleaseKeys); err != nil {
		return nil, err
	}
	licenseKeys, err := mergeKeysetKeys(this.options.PublicKeys, envelope.Payload.LicenseKeys)
	if err != nil {
		return nil, err
	}
	releaseKeys, err := mergeKeysetKeys(this.options.ReleasePublicKeys, envelope.Payload.ReleaseKeys)
	if err != nil {
		return nil, err
	}
	return &keysetCandidate{
		envelope: envelope, raw: append(json.RawMessage(nil), trimmed...),
		licenseKeys: licenseKeys, releaseKeys: releaseKeys,
	}, nil
}

// candidatePublicKeys - 选择候选快照；无候选时读取当前已信任快照。
func (this *Client) candidatePublicKeys(candidate *keysetCandidate, release bool) map[string]string {
	if candidate != nil {
		if release {
			return candidate.releaseKeys
		}
		return candidate.licenseKeys
	}
	return this.publicKeysSnapshot(release)
}

// commitKeyset - 提交已验证 keyset；调用方负责与业务结果在同一操作内落盘。
func (this *Client) commitKeyset(candidate *keysetCandidate) {
	if candidate == nil {
		return
	}
	this.mu.Lock()
	this.keyset = *candidate.envelope
	this.state.Keyset = append(json.RawMessage(nil), candidate.raw...)
	this.mu.Unlock()
}

// commitEnvelopeAndKeyset - 原子替换业务信封与 keyset，任一验签失败都不会进入本方法。
func (this *Client) commitEnvelopeAndKeyset(candidate *keysetCandidate, envelope LicenceProtocol.Envelope, raw json.RawMessage) {
	this.mu.Lock()
	if candidate != nil {
		this.keyset = *candidate.envelope
		this.state.Keyset = append(json.RawMessage(nil), candidate.raw...)
	}
	this.envelope = envelope
	this.state.Envelope = append(json.RawMessage(nil), raw...)
	this.mu.Unlock()
}

// PublicKeys - 返回最新 license 公钥副本（keyVersion → hex 公钥），供回调/订阅验签复用。
// 返回副本而非内部 map 引用，调用方修改不影响客户端。
func (this *Client) PublicKeys() map[string]string {
	return this.publicKeysSnapshot(false)
}

// ============================= 消费记录上报 =============================

// 平台配置读取埋点与消费上报。
// PlatformConfig/PlatformConfigMust 读命中后 trackConfigConsumption 累计计数（仅互斥 map 自增，
// 不阻塞读取）；后台 consumptionLoop 按间隔（默认 30s，累计达 50 个 key 提前触发）批量上报
// /api/v1/platform/configs/consume，经 transport.RoundTrip 统一分发 HTTP/gRPC 双协议，gate 验签防重放。
// best-effort：失败静默并保留计数下轮重试；不上报成功/失败到调用方，不污染授权刷新循环的退避状态。

// consumptionKeyThreshold - 待上报 key 数达到该值提前触发一次上报（防高并发读积压）
const consumptionKeyThreshold = 50

// platformConfigConsumeBody - 配置消费上报请求体（与平台 types.PlatformConfigConsume 契约一致）
type platformConfigConsumeBody struct {
	LicenseNo string                      `json:"licenseNo"`
	Items     []platformConfigConsumeItem `json:"items"`
}

// platformConfigConsumeItem - 单配置键消费计数
type platformConfigConsumeItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// platformConfigConsumeResponse - 配置消费上报响应
type platformConfigConsumeResponse struct {
	Status     string `json:"status"`
	ServerTime int64  `json:"serverTime"`
	Message    string `json:"message"`
}

// trackConfigConsumption - 记录一次配置 key 读取（仅在开启埋点且读取命中时调用）。
func (this *Client) trackConfigConsumption(key string) {
	if this.options.DisableConsumptionReport {
		return
	}
	this.consumptionMu.Lock()
	this.pendingConsumption[key]++
	trigger := len(this.pendingConsumption) >= consumptionKeyThreshold
	this.consumptionMu.Unlock()
	// 达到阈值时异步提前冲刷（flush 内部 CAS 防并发），避免等待下个 ticker 的空窗期
	if trigger {
		go this.flushConsumption(context.Background())
	}
}

// consumptionLoop - 后台消费上报循环（Start 启动，与授权刷新循环独立；停用开关时不启动）。
func (this *Client) consumptionLoop(ctx context.Context) {
	ticker := time.NewTicker(this.options.ConsumptionReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// 退出前尽力冲刷一次，避免已累计计数滞留
			this.flushConsumption(context.Background())
			return
		case <-ticker.C:
			this.flushConsumption(context.Background())
		}
	}
}

// flushConsumption - 冲刷一次待上报消费：CAS 防并发，snapshot 后上报，成功才扣减已上报部分。
// 失败静默（计数保留待下轮重试）；期间新增的读取计数不受影响。
func (this *Client) flushConsumption(ctx context.Context) {
	if this.options.DisableConsumptionReport {
		return
	}
	if !this.flushing.CompareAndSwap(false, true) {
		return // 已有上报在跑，下轮再冲刷
	}
	defer this.flushing.Store(false)

	this.consumptionMu.Lock()
	if len(this.pendingConsumption) == 0 {
		this.consumptionMu.Unlock()
		return
	}
	items := make([]platformConfigConsumeItem, 0, len(this.pendingConsumption))
	for key, count := range this.pendingConsumption {
		items = append(items, platformConfigConsumeItem{Key: key, Count: count})
	}
	this.consumptionMu.Unlock()

	if err := this.reportConsumption(ctx, items); err != nil {
		return // 失败静默：计数仍在 pending，下轮重试
	}
	this.consumptionMu.Lock()
	for _, item := range items {
		if remain := this.pendingConsumption[item.Key] - item.Count; remain > 0 {
			this.pendingConsumption[item.Key] = remain
		} else {
			delete(this.pendingConsumption, item.Key)
		}
	}
	this.consumptionMu.Unlock()
}

// reportConsumption - 上报消费明细到平台。直连 transport.RoundTrip（不经 doRequest），
// 避免失败/成功改写授权刷新循环的 retryDelay 退避状态；clockOffset 校时仍复用。
func (this *Client) reportConsumption(ctx context.Context, items []platformConfigConsumeItem) error {
	if this.transport == nil {
		return errors.New("运行面传输层未初始化")
	}
	if len(items) == 0 {
		return nil
	}
	body, err := json.Marshal(platformConfigConsumeBody{LicenseNo: this.options.LicenseNo, Items: items})
	if err != nil {
		return err
	}
	code, raw, err := this.transport.RoundTrip(ctx, http.MethodPost, "/api/v1/platform/configs/consume", body, true)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return errors.New("许可证或实例信息无效")
	}
	var response platformConfigConsumeResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)
	if !LicenceProtocol.PassThrough(response.Status) {
		return errors.New("消费上报被拒绝：" + response.Status)
	}
	return nil
}
