package licence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultCallbackTimeWindow = 5 * time.Minute
	defaultCallbackDedupTTL   = 10 * time.Minute
	defaultCallbackMaxBody    = int64(1 << 20)
)

// CallbackPayload - 回调事件载荷（与平台签发端字节级镜像）
//
// 字段顺序即签名内容，新增字段只允许追加到末尾。
type CallbackPayload struct {
	EventNo    string          `json:"eventNo"`
	DeliveryNo string          `json:"deliveryNo"`
	Event      string          `json:"event"`
	ProjectId  string          `json:"projectId"`
	InstanceId string          `json:"instanceId"`
	OccurredAt string          `json:"occurredAt"`
	Nonce      string          `json:"nonce"`
	KeyVersion string          `json:"keyVersion"`
	Data       json.RawMessage `json:"data"`
}

// CallbackEnvelope - 回调事件签名信封
type CallbackEnvelope struct {
	Version   int             `json:"version"`
	Algorithm string          `json:"algorithm"`
	Payload   CallbackPayload `json:"payload"`
	Signature string          `json:"signature"`
}

// ParseCallbackEnvelope - 解析回调信封并返回 payload 原文
func ParseCallbackEnvelope(data []byte) (envelope CallbackEnvelope, rawPayload []byte, err error) {
	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return CallbackEnvelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return CallbackEnvelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}

// Ack - 客户端应答词
type Ack string

const (
	// AckSuccess - 已接收并处理
	AckSuccess Ack = "success"
	// AckOk - 已接收并处理（与 success 等价）
	AckOk Ack = "ok"
	// AckIgnored - 已接收但无需处理
	AckIgnored Ack = "ignored"
	// AckRetry - 暂时性失败，要求平台重试
	AckRetry Ack = "retry"
	// AckRejected - 明确拒绝，重试无意义
	AckRejected Ack = "rejected"
)

// 回调事件常量，与平台 `licen-hub/backend/app/common/callback/event.go` 的 supportedEvents 一一对应。
// 已知事件可直接引用常量注册；对未收录的新事件族，用 OnEvent 前缀通配（如 "saas.*"）兜底匹配。
const (
	// EventSaasPlanCreated - SaaS 套餐已创建，data: {planId, planCode, tenantManifestVersion}
	EventSaasPlanCreated = "saas.plan.created"
	// EventSaasPlanUpdated - SaaS 套餐内容已修改，data: {planId, planCode, tenantManifestVersion}
	EventSaasPlanUpdated = "saas.plan.updated"
	// EventSaasPlanEnabled - SaaS 套餐已启用，data: {planId, planCode, tenantManifestVersion}
	EventSaasPlanEnabled = "saas.plan.enabled"
	// EventSaasPlanDisabled - SaaS 套餐已停用，data: {planId, planCode, tenantManifestVersion}
	EventSaasPlanDisabled = "saas.plan.disabled"
	// EventSaasTenantCreated - SaaS 租户已诞生（首次生效），data: {tenantNo, tenantCode, planCode, subscriptionType, environment}
	EventSaasTenantCreated = "saas.tenant.created"
	// EventSaasMenuPublished - SaaS 菜单清单已发布，data: {manifestId, version}
	EventSaasMenuPublished = "saas.menu.published"
	// EventSaasMenuArchived - SaaS 菜单清单已归档，data: {manifestId, version}
	EventSaasMenuArchived = "saas.menu.archived"
	// EventSaasTenantMenusTrimmed - 租户悬空菜单已裁剪并重签，data: {tenantId, tenantCode, removedCodes}
	EventSaasTenantMenusTrimmed = "saas.tenant.menus-trimmed"
	// EventPlatformConfigUpdated - 平台配置值已更新，data: {configKey, projectId}
	EventPlatformConfigUpdated = "platform.config.updated"
	// EventPlatformConfigDefinitionChanged - 平台配置项定义已变更，data: {configKey, version}
	EventPlatformConfigDefinitionChanged = "platform.config.definition.changed"
	// EventUpdateAvailable - 项目发布新版本（hint），data: {version}；仅作近实时提示，
	// 灰度与升级权仍以 updates/check 判定为准（设计 §4.4）
	EventUpdateAvailable = "update.available"
)

// CallbackEvent - 分发给业务回调的事件对象
type CallbackEvent struct {
	// Payload - 完整回调载荷
	Payload CallbackPayload
	// Data - 事件数据摘要（Payload.Data 的只读视图）
	Data json.RawMessage
}

// MustData - 将事件 Data 解码到 v，失败时 panic
func (this *CallbackEvent) MustData(v any) {
	if err := json.Unmarshal(this.Data, v); err != nil {
		panic(err)
	}
}

// CallbackFunc - 业务回调，error 非空等价于 AckRetry
type CallbackFunc func(ctx context.Context, event *CallbackEvent) (Ack, error)

// CallbackOptions - 回调接收端配置
type CallbackOptions struct {
	// PublicKeys - keyVersion 到 hex 公钥的映射
	PublicKeys map[string]string
	// TimeWindow - occurredAt 防重放窗口，默认正负 5 分钟
	TimeWindow time.Duration
	// DedupTTL - nonce 和 deliveryNo 去重时长，默认 10 分钟
	DedupTTL time.Duration
	// MaxBody - 请求体上限，默认 1MB
	MaxBody int64
}

type callbackDedupItem struct {
	expiresAt time.Time
	ack       Ack
	ready     bool
}

// CallbackHandler - 平台回调接收端，实现 http.Handler
type CallbackHandler struct {
	options    CallbackOptions
	mu         sync.Mutex
	handlers   map[string]CallbackFunc
	onAny      CallbackFunc
	nonces     map[string]callbackDedupItem
	deliveries map[string]callbackDedupItem
}

// NewCallbackHandler - 创建回调接收端
func NewCallbackHandler(options CallbackOptions) *CallbackHandler {
	if options.TimeWindow <= 0 {
		options.TimeWindow = defaultCallbackTimeWindow
	}
	if options.DedupTTL <= 0 {
		options.DedupTTL = defaultCallbackDedupTTL
	}
	if options.MaxBody <= 0 {
		options.MaxBody = defaultCallbackMaxBody
	}
	publicKeys := make(map[string]string, len(options.PublicKeys))
	for version, key := range options.PublicKeys {
		publicKeys[version] = key
	}
	options.PublicKeys = publicKeys
	return &CallbackHandler{
		options:    options,
		handlers:   make(map[string]CallbackFunc),
		nonces:     make(map[string]callbackDedupItem),
		deliveries: make(map[string]callbackDedupItem),
	}
}

// OnEvent - 注册精确事件或前缀通配处理器
func (this *CallbackHandler) OnEvent(event string, fn CallbackFunc) *CallbackHandler {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.handlers[event] = fn
	return this
}

// OnAny - 注册未匹配事件的兜底处理器
func (this *CallbackHandler) OnAny(fn CallbackFunc) *CallbackHandler {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.onAny = fn
	return this
}

// validateCallbackEnvelope - 校验信封版本/算法与必填载荷字段（ServeHTTP 的 400 分支；订阅也先调用）。
func validateCallbackEnvelope(envelope CallbackEnvelope) error {
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm {
		return errors.New("invalid callback envelope")
	}
	if envelope.Payload.Event == "" || envelope.Payload.DeliveryNo == "" || envelope.Payload.Nonce == "" ||
		envelope.Payload.KeyVersion == "" {
		return errors.New("invalid callback payload")
	}
	return nil
}

// dispatchEnvelope - 验签、防重放并分发单个事件信封；ServeHTTP 与 EventSubscriber 共用。
// 返回 ack = 已接收（重复投递时重放已记录的应答，不重复执行业务回调）；
// 返回 error = 验签/防重放失败，未投递。业务回调 panic 时清理 deliveryNo 去重项并重新抛出，
// 由调用方（ServeHTTP 的 500 / 订阅器的 error）兜底。
func (this *CallbackHandler) dispatchEnvelope(ctx context.Context, envelope CallbackEnvelope, rawPayload []byte) (ack Ack, err error) {
	deliveryNo := envelope.Payload.DeliveryNo
	defer func() {
		if recovered := recover(); recovered != nil {
			this.mu.Lock()
			delete(this.deliveries, deliveryNo)
			this.mu.Unlock()
			panic(recovered)
		}
	}()

	publicKey, exists := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exists || !verifyPayload(rawPayload, envelope.Signature, publicKey) {
		return "", errors.New("callback signature verification failed")
	}
	occurredAt, err := time.Parse(time.RFC3339, envelope.Payload.OccurredAt)
	if err != nil || durationAbs(time.Since(occurredAt)) > this.options.TimeWindow {
		return "", errors.New("callback occurredAt expired")
	}

	now := time.Now()
	this.mu.Lock()
	this.cleanupLocked(now)
	if item, exists := this.nonces[envelope.Payload.Nonce]; exists && item.expiresAt.After(now) {
		this.mu.Unlock()
		return "", errors.New("callback nonce replayed")
	}
	this.nonces[envelope.Payload.Nonce] = callbackDedupItem{expiresAt: now.Add(this.options.DedupTTL)}
	this.scheduleCleanup(envelope.Payload.Nonce, false, now.Add(this.options.DedupTTL))
	if item, exists := this.deliveries[deliveryNo]; exists && item.expiresAt.After(now) {
		this.mu.Unlock()
		if item.ready {
			return item.ack, nil
		}
		return AckRetry, nil
	}
	deliveryExpiresAt := now.Add(this.options.DedupTTL)
	this.deliveries[deliveryNo] = callbackDedupItem{expiresAt: deliveryExpiresAt}
	this.scheduleCleanup(deliveryNo, true, deliveryExpiresAt)
	fn := this.matchLocked(envelope.Payload.Event)
	this.mu.Unlock()

	ack = AckIgnored
	if fn != nil {
		ack, err = fn(ctx, &CallbackEvent{
			Payload: envelope.Payload, Data: envelope.Payload.Data,
		})
		if err != nil {
			ack = AckRetry
		}
	}
	if !validAck(ack) {
		ack = AckRetry
	}
	this.mu.Lock()
	deliveryExpiresAt = time.Now().Add(this.options.DedupTTL)
	this.deliveries[deliveryNo] = callbackDedupItem{
		expiresAt: deliveryExpiresAt, ack: ack, ready: true,
	}
	this.mu.Unlock()
	this.scheduleCleanup(deliveryNo, true, deliveryExpiresAt)
	return ack, nil
}

// ServeHTTP - 验签、防重放并分发回调事件（webhook 接收端，平台 → 客户）。
func (this *CallbackHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	defer func() {
		if recover() != nil {
			http.Error(writer, "callback handler panic", http.StatusInternalServerError)
		}
	}()

	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, this.options.MaxBody))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(writer, "invalid request body", http.StatusBadRequest)
		return
	}
	envelope, rawPayload, err := ParseCallbackEnvelope(body)
	if err != nil {
		http.Error(writer, "invalid callback envelope", http.StatusBadRequest)
		return
	}
	if err = validateCallbackEnvelope(envelope); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	ack, err := this.dispatchEnvelope(request.Context(), envelope, rawPayload)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnauthorized)
		return
	}
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(ack))
}

func (this *CallbackHandler) cleanupLocked(now time.Time) {
	for key, item := range this.nonces {
		if !item.expiresAt.After(now) {
			delete(this.nonces, key)
		}
	}
	for key, item := range this.deliveries {
		if !item.expiresAt.After(now) {
			delete(this.deliveries, key)
		}
	}
}

func (this *CallbackHandler) scheduleCleanup(key string, delivery bool, expiresAt time.Time) {
	time.AfterFunc(time.Until(expiresAt), func() {
		this.mu.Lock()
		defer this.mu.Unlock()
		items := this.nonces
		if delivery {
			items = this.deliveries
		}
		if item, exists := items[key]; exists && !item.expiresAt.After(time.Now()) {
			delete(items, key)
		}
	})
}

func (this *CallbackHandler) matchLocked(event string) CallbackFunc {
	if fn := this.handlers[event]; fn != nil {
		return fn
	}
	parts := strings.Split(event, ".")
	for index := len(parts) - 1; index > 0; index-- {
		if fn := this.handlers[strings.Join(parts[:index], ".")+".*"]; fn != nil {
			return fn
		}
	}
	return this.onAny
}

func validAck(ack Ack) bool {
	switch ack {
	case AckSuccess, AckOk, AckIgnored, AckRetry, AckRejected:
		return true
	default:
		return false
	}
}

func durationAbs(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
