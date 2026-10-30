package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/inis-io/aide/licence/config"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc/codes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================= 假授权平台（契约行为镜像） =============================

const (
	// goldenSeed - 固定私钥种子（01 02 ... 20）的 hex（与 protocol 包 golden_test.go 同源）
	goldenSeed = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	// goldenPublicKey - 固定种子推导的 Ed25519 公钥（与 protocol 包 golden_test.go 同源）
	goldenPublicKey = "79b5562e8fe654f94078b112e8a98ba7901f853ae695bed7e0e3910bad049664"
)

// fakePlatform - 运行面假平台：实现契约的 activate/validate/current，
// 含 token 哈希校验与客户端公钥请求验签（时间窗 + 签名内容格式同契约 §2.4）
type fakePlatform struct {
	// mu - 状态读写锁
	mu sync.Mutex
	// seed/publicKey - 平台签发密钥（license-key-2026-01）
	seed      []byte
	publicKey string
	// releaseSeed/releasePublicKey - 发布物签名密钥（release-key-2026-01）
	releaseSeed      []byte
	releasePublicKey string
	// clientPublicKey - activate 注册的客户端验签公钥
	clientPublicKey string
	// deviceName - activate 上报的设备名称
	deviceName string
	// tokenHash - 激活令牌哈希（SHA-256 hex，库存哈希不存原文）
	tokenHash string
	// expiresAt - 激活有效期截止（毫秒）
	expiresAt int64
	// validUntil - 许可证到期（RFC3339，空 = 永久）
	validUntil string
	// graceDays - 宽限期（天）
	graceDays int
	// forceStatus - 非空时强制运行面返回指定状态。
	forceStatus string
	// upgradeUntil - 升级权截止（毫秒，0 = 不限）
	upgradeUntil int64
	// entitlements - 载荷统一权益表（bool / 整数 / 小数混合）
	entitlements map[string]any
	// version - 许可证乐观锁版本（载荷变化递增，触发 validate 下发新信封）
	version int
	// issuedVersion - 最近一次随响应下发的信封版本
	issuedVersion int
	// versions - 已发布版本（更新检查候选）
	versions []fakeVersion
	// tenants - SaaS 租户
	tenants map[string]fakeTenant
	// platformConfigs - 平台配置
	platformConfigs map[string]PlatformConfigItem
	// platformConfigSyncVersion - 平台配置全局增量水位
	platformConfigSyncVersion int
	// tamperManifest - 测试开关：签发后篡改清单签名
	tamperManifest bool
	// reportSeq - 升级记录编号序列
	reportSeq int
	// reports - 升级上报轨迹（handleUpdateReport 记录，供流水线断言）
	reports []map[string]any
	// pushbacks - 配置回推批次记录（handlePushbackConfigs 记录，供断言）
	pushbacks []fakePushback
	// pushbackCode - 非 0 时配置回推模拟平台拒绝（404 模糊 NotFound / 400 校验失败）
	pushbackCode int
	// definitionPushbacks - 配置定义反推批次记录（handlePushbackDefinitions 记录，供断言）
	definitionPushbacks []fakeDefinitionPushback
	// pushbackDefCode - 非 0 时定义反推模拟平台拒绝（403 开关关闭 / 400 校验失败带 errors 明细）
	pushbackDefCode int
	// 调用计数
	activateCalls int
	validateCalls int
	// customerBindCalls - 客户账号绑定调用计数
	customerBindCalls int
	// customerBindRequests - 客户账号绑定请求快照（body 与签名头）
	customerBindRequests []map[string]string
	// customerBindStatus / customerBindMessage - 绑定业务拒绝注入
	customerBindStatus  string
	customerBindMessage string
	// customerBindPrepareCustomer - 预注册已绑定响应携带的脱敏客户视图
	customerBindPrepareCustomer *CustomerView
	// customerBindCode - 非 0 时绑定接口直接返回该 HTTP 状态码 + 空 body
	customerBindCode int
	// events - 项目事件订阅队列（callback_events 简化镜像，eventId 单调递增）
	events   []fakeEvent
	eventSeq int64
	// commands - 待领取实例指令队列（handleCommandPull 弹出全部）
	commands []CommandTask
	// commandReports - 指令回执记录（handleCommandReport 追加，供断言）
	commandReports []map[string]any
	// commandPullCalls - 指令领取调用计数
	commandPullCalls int
	// provisionRequests - 激活码 provision 请求记录（body 原文与签名头快照，供断言）
	provisionRequests []map[string]string
	// provisionStatus - 非空且非 OK 时激活码 provision 返回业务拒绝（如 ACTIVATION_INVALID）
	provisionStatus string
	// provisionMessage - 激活码 provision 业务拒绝提示
	provisionMessage string
	// provisionCode - 非 0 时激活码 provision 直接返回该 HTTP 状态码 + 空 body（404 模糊语义模拟）
	provisionCode int
	// provisionLicenseNo - 激活码目标许可证编号（空 = 与当前证相同，用于同证幂待用例）
	provisionLicenseNo string
	// provisionConflict - 激活码 provision 返回 ACTIVATION_CONFLICT + 对比载荷（不消费码）
	provisionConflict bool
	// provisionRequireFingerprint - 缺指纹时先返回挑战，不直接签发目标证
	provisionRequireFingerprint bool
	// provisionUnsigned - 激活码 provision 收到的请求是否未带签名头（供断言签名强制）
	provisionUnsigned bool
	// server - httptest 实例
	server *httptest.Server
}

// fakeArtifact - 假发布物（artifactType 空 = 全量包；incremental 需填 sourceVersion）
type fakeArtifact struct {
	artifactType  string
	sourceVersion string
	fileName      string
	osArch        string
	data          []byte
}

// fakeVersion - 假发布版本（artifactData 经 /files/{version} 提供下载；
// artifacts 非空时替代单 artifactData 构造多发布物清单，policy 非空时随清单下发 updatePolicy）
type fakeVersion struct {
	version      string
	buildNumber  string
	sourceRange  string
	minUpgrade   string
	osArch       []string
	releasedAt   int64
	grayMode     string
	grayPercent  int
	artifactData []byte
	artifacts    []fakeArtifact
	policy       *ManifestUpdatePolicy
}

// fakeTenant - 假 SaaS 租户（status 非空时优先于时间判定，用于模拟 suspended 等）
type fakeTenant struct {
	status     string
	validUntil string
	graceDays  int
	features   map[string]bool
}

// fakeEvent - 假订阅事件（eventId 即客户端水位，data 为事件摘要）
type fakeEvent struct {
	eventId int64
	eventNo string
	event   string
	data    json.RawMessage
}

// fakePushback - 假配置回推批次（记录客户端上送的快照与幂等号）
type fakePushback struct {
	tenantID     uint64
	items        map[string]string
	clientPushID string
}

// fakeDefinitionPushback - 假配置定义反推批次（记录请求原文与解析结果，供 snake_case/原生 JSON 断言）
type fakeDefinitionPushback struct {
	rawBody      []byte
	groups       []config.ConfigDefinitionGroup
	configs      []config.ConfigDefinitionItem
	clientPushID string
}

// pushEvent - 推送一条订阅事件（eventId 单调递增，等价平台 callback_events 落库）
func (this *fakePlatform) pushEvent(event string, data map[string]any) (int64, error) {

	this.mu.Lock()
	defer this.mu.Unlock()

	this.eventSeq++
	raw, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}
	this.events = append(this.events, fakeEvent{
		eventId: this.eventSeq,
		eventNo: fmt.Sprintf("EVT-2026-%06d", this.eventSeq),
		event:   event, data: raw,
	})
	return this.eventSeq, nil
}

// signCallbackEnvelope - 用平台 seed 现场重签一条订阅事件信封：
// nonce 每次新鲜、occurredAt 为当下、deliveryNo 稳定 SUB-{eventNo}（镜像平台订阅端点行为）。
// 匿名结构的字段顺序与 callback.CallbackPayload/CallbackEnvelope 字节级一致
// （字段顺序即签名内容；根包测试不反向依赖 callback 子包）。
func signCallbackEnvelope(seed []byte, event fakeEvent) (json.RawMessage, error) {

	payload := struct {
		EventNo    string          `json:"eventNo"`
		DeliveryNo string          `json:"deliveryNo"`
		Event      string          `json:"event"`
		ProjectId  string          `json:"projectId"`
		InstanceId string          `json:"instanceId"`
		OccurredAt string          `json:"occurredAt"`
		Nonce      string          `json:"nonce"`
		KeyVersion string          `json:"keyVersion"`
		Data       json.RawMessage `json:"data"`
	}{
		EventNo: event.eventNo, DeliveryNo: "SUB-" + event.eventNo, Event: event.event,
		ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Nonce:      LicenceProtocol.Licence.Nonce(), KeyVersion: "license-key-2026-01",
		Data: event.data,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	signature, err := LicenceProtocol.SignPayload(raw, seed)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}{Version: LicenceProtocol.EnvelopeVersion, Algorithm: LicenceProtocol.Algorithm, Payload: raw, Signature: signature})
}

// newFakePlatform - 创建假平台（默认：永久授权、基础权益）
func newFakePlatform(t *testing.T) *fakePlatform {

	t.Helper()
	seed, publicKey, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成平台密钥对失败: %v", err)
	}
	platform := &fakePlatform{
		seed: seed, publicKey: hex.EncodeToString(publicKey),
		validUntil: "", graceDays: 7, version: 1,
		entitlements:    map[string]any{"report.advanced": true, "ai.chat": false, "max_users": 100, "max_storage_gb": 2.5},
		tenants:         make(map[string]fakeTenant),
		platformConfigs: make(map[string]PlatformConfigItem),
	}
	releaseSeed, releasePublicKey, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成发布密钥对失败: %v", err)
	}
	platform.releaseSeed = releaseSeed
	platform.releasePublicKey = hex.EncodeToString(releasePublicKey)
	platform.server = httptest.NewServer(http.HandlerFunc(platform.handle))
	t.Cleanup(platform.server.Close)
	return platform
}

// handle - 路由分发
func (this *fakePlatform) handle(writer http.ResponseWriter, request *http.Request) {

	body, _ := readAll(request)
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/activate":
		this.handleActivate(writer, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/validate":
		this.handleValidate(writer, request, body)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/licenses/current":
		this.handleCurrent(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/provision":
		this.handleProvision(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/customer-bind-prepare":
		this.handleCustomerBindPrepare(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/customer-bind":
		this.handleCustomerBind(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/customer-unbind":
		this.handleCustomerUnbind(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/updates/check":
		this.handleUpdateCheck(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/updates/report":
		this.handleUpdateReport(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/updates/logs":
		this.handleUpdateLogs(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/saas/tenants/sync":
		this.handleTenantSync(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/saas/tenants/search":
		this.handleTenantSearch(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/platform/configs/sync":
		this.handlePlatformConfigSync(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/saas/tenants/validate":
		this.handleTenantValidate(writer, request, body)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/saas/tenants/current":
		this.handleTenantCurrent(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/events/subscribe":
		this.handleSubscribe(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/commands/pull":
		this.handleCommandPull(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/commands/report":
		this.handleCommandReport(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/pushback/configs":
		this.handlePushbackConfigs(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/pushback/config-definitions":
		this.handlePushbackDefinitions(writer, request, body)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/files/"):
		this.handleFileDownload(writer, request)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// handleSubscribe - 事件订阅：凭证校验 → 放行态判定 → 按 sinceEventId 过滤并现场重签（nonce 每次新鲜）
func (this *fakePlatform) handleSubscribe(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		LicenseNo    string `json:"licenseNo"`
		SinceEventId int64  `json:"sinceEventId"`
		TimeoutMs    int32  `json:"timeoutMs"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	status := this.status()
	if !LicenceProtocol.PassThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		return
	}
	items := make([]map[string]any, 0, len(this.events))
	for _, event := range this.events {
		if event.eventId <= params.SinceEventId {
			continue
		}
		envelope, err := signCallbackEnvelope(this.seed, event)
		if err != nil {
			writeJson(writer, map[string]any{"status": LicenceProtocol.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		items = append(items, map[string]any{"eventId": event.eventId, "envelope": envelope})
	}
	writeJson(writer, map[string]any{
		"status": status, "serverTime": time.Now().UnixMilli(), "events": items,
	})
}

// handleActivate - 激活：注册客户端公钥与指纹，签发信封 + 一次性令牌
func (this *fakePlatform) handleActivate(writer http.ResponseWriter, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	var params activateBody
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	this.clientPublicKey = params.ClientPublicKey
	this.deviceName = params.DeviceName
	token := LicenceProtocol.Licence.Nonce() + LicenceProtocol.Licence.Nonce() + LicenceProtocol.Licence.Nonce() // 48 字节 hex
	sum := sha256.Sum256([]byte(token))
	this.tokenHash = hex.EncodeToString(sum[:])
	this.expiresAt = time.Now().UnixMilli() + 7*24*3600*1000
	this.issuedVersion = this.version
	this.activateCalls++

	status := this.status()
	envelope, err := this.issue(params.FingerprintHash)
	if err != nil {
		writeJson(writer, map[string]any{"status": LicenceProtocol.StatusError, "serverTime": time.Now().UnixMilli(), "message": "签名服务未就绪"})
		return
	}
	writeJson(writer, map[string]any{
		"status": status, "serverTime": time.Now().UnixMilli(),
		"envelope": envelope, "activationNo": "ACT-2026-000001",
		"activationToken": token, "seatNo": "SEAT-2026-000001", "expiresAt": this.expiresAt,
	})
}

// handleValidate - 校验：token 哈希 + 请求验签 → 状态判定 → 滑动刷新（版本变化时下发新信封）
func (this *fakePlatform) handleValidate(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()
	this.validateCalls++

	if !this.credential(writer, request, body) {
		return
	}

	status := this.status()
	response := map[string]any{"status": status, "serverTime": time.Now().UnixMilli()}
	if LicenceProtocol.PassThrough(status) {
		this.expiresAt = time.Now().UnixMilli() + 7*24*3600*1000
		response["expiresAt"] = this.expiresAt
		if this.version != this.issuedVersion {
			var params validateBody
			_ = json.Unmarshal(body, &params)
			envelope, err := this.issue(params.FingerprintHash)
			if err == nil {
				response["envelope"] = envelope
				this.issuedVersion = this.version
			}
		}
	}
	writeJson(writer, response)
}

// handleCustomerBind - 客户账号绑定：凭证校验 + 请求验签 → 记录授权票据与签名头快照。
func (this *fakePlatform) handleCustomerBind(writer http.ResponseWriter, request *http.Request, body []byte) {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.customerBindCalls++
	if !this.credential(writer, request, body) {
		return
	}
	this.customerBindRequests = append(this.customerBindRequests, map[string]string{
		"body": string(body), "token": request.Header.Get("X-License-Token"),
		"timestamp": request.Header.Get("X-License-Timestamp"),
		"nonce":     request.Header.Get("X-License-Nonce"),
		"signature": request.Header.Get("X-License-Sign"),
	})
	if this.customerBindCode != 0 {
		writer.WriteHeader(this.customerBindCode)
		return
	}
	if this.customerBindStatus != "" && this.customerBindStatus != "OK" {
		writeJson(writer, map[string]any{
			"status": this.customerBindStatus, "serverTime": time.Now().UnixMilli(), "message": this.customerBindMessage,
		})
		return
	}
	writeJson(writer, map[string]any{
		"status": "OK", "serverTime": time.Now().UnixMilli(),
		"customer": map[string]any{"maskAccount": "r***@x.com", "boundAt": 123},
	})
}

// handleProvision - 激活码 provision 分支模拟：携带 currentLicenseNo 时强制凭证验签，
// 记录请求快照；按测试开关返回冲突载荷（不消费码）/业务拒绝/目标许可证 + 脱敏客户视图。
func (this *fakePlatform) handleProvision(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	var params provisionBody
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	signed := request.Header.Get("X-License-Sign") != ""
	// 携带 currentLicenseNo（换证入座）必须带 activation-sign-v1 签名；匿名仅允许全新实例路径
	if params.CurrentLicenseNo != "" {
		if !signed || !this.credential(writer, request, body) {
			if !signed {
				this.provisionUnsigned = true
				writeNotFound(writer)
			}
			return
		}
	} else if !signed {
		this.provisionUnsigned = true
	}
	this.provisionRequests = append(this.provisionRequests, map[string]string{
		"body": string(body), "token": request.Header.Get("X-License-Token"),
		"timestamp": request.Header.Get("X-License-Timestamp"),
		"nonce":     request.Header.Get("X-License-Nonce"),
		"signature": request.Header.Get("X-License-Sign"),
	})
	if this.provisionCode != 0 {
		writer.WriteHeader(this.provisionCode)
		return
	}
	if this.provisionConflict {
		writeJson(writer, map[string]any{
			"status": "ACTIVATION_CONFLICT", "serverTime": time.Now().UnixMilli(),
			"message": "当前许可证已持付费套餐",
			"conflict": map[string]any{
				"currentPlan": map[string]any{
					"licenseNo": "LIC-2026-000123", "planCode": "std", "planName": "标准版",
					"billing": "yearly", "validUntil": "2027-01-01T00:00:00+08:00", "paid": true,
				},
				"targetPlan": map[string]any{
					"licenseNo": "LIC-2026-000999", "planCode": "pro", "planName": "专业版",
					"billing": "monthly", "validUntil": "2026-12-01T00:00:00+08:00", "paid": true,
				},
				"seatUsed": 1, "seatLimit": 3,
			},
		})
		return
	}
	if this.provisionStatus != "" && this.provisionStatus != "OK" {
		writeJson(writer, map[string]any{
			"status": this.provisionStatus, "serverTime": time.Now().UnixMilli(), "message": this.provisionMessage,
		})
		return
	}
	if this.provisionRequireFingerprint && params.FingerprintHash == "" {
		writeJson(writer, map[string]any{
			"status": "FINGERPRINT_REQUIRED", "serverTime": time.Now().UnixMilli(), "salt": "salt-switch",
		})
		return
	}
	licenseNo := this.provisionLicenseNo
	if licenseNo == "" {
		licenseNo = "LIC-2026-000999"
	}
	writeJson(writer, map[string]any{
		"status": "OK", "serverTime": time.Now().UnixMilli(),
		"licenseNo": licenseNo, "salt": "salt-switch",
		"bindingPolicy": "seats", "seatLimit": 3,
		"expiresAt": time.Now().UnixMilli() + 365*24*3600*1000,
		"customer":  map[string]any{"maskAccount": "r***@x.com", "boundAt": 123},
	})
}

// handleCurrent - 当前信封：凭证与验签同 validate，不做滑动刷新
func (this *fakePlatform) handleCurrent(writer http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, nil) {
		return
	}
	status := this.status()
	if !LicenceProtocol.PassThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		return
	}
	envelope, err := this.issue("test-fingerprint")
	if err != nil {
		writeJson(writer, map[string]any{"status": LicenceProtocol.StatusError, "serverTime": time.Now().UnixMilli()})
		return
	}
	writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli(), "envelope": envelope})
}

// credential - 凭证校验 + 请求验签（契约 §2.4 服务端行为镜像）
func (this *fakePlatform) credential(writer http.ResponseWriter, request *http.Request, body []byte) bool {

	sum := sha256.Sum256([]byte(request.Header.Get("X-License-Token")))
	if this.tokenHash == "" || hex.EncodeToString(sum[:]) != this.tokenHash {
		writeJson(writer, map[string]any{"status": LicenceProtocol.StatusExpired, "serverTime": time.Now().UnixMilli(), "message": "凭证无效或已过期，请重新激活"})
		return false
	}

	timestamp := request.Header.Get("X-License-Timestamp")
	nonce := request.Header.Get("X-License-Nonce")
	signature := request.Header.Get("X-License-Sign")
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().UnixMilli()
	if err != nil || nonce == "" || ts < now-5*60*1000 || ts > now+5*60*1000 {
		writeNotFound(writer)
		return false
	}
	bodySum := sha256.Sum256(body)
	content := request.Method + "\n" + request.URL.RequestURI() + "\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(bodySum[:])
	if !LicenceProtocol.Licence.VerifyRaw([]byte(content), signature, this.clientPublicKey) {
		writeNotFound(writer)
		return false
	}
	return true
}

// status - 时间维度状态判定（平台 JudgeLicenseStatus 简化镜像）
func (this *fakePlatform) status() string {
	if this.forceStatus != "" {
		return this.forceStatus
	}

	if this.validUntil == "" {
		return LicenceProtocol.StatusValid
	}
	until, err := time.Parse(time.RFC3339, this.validUntil)
	if err != nil {
		return LicenceProtocol.StatusExpired
	}
	now := time.Now()
	if now.After(until.Add(time.Duration(this.graceDays) * 24 * time.Hour)) {
		return LicenceProtocol.StatusExpired
	}
	if now.After(until) {
		return LicenceProtocol.StatusGrace
	}
	if time.Until(until) <= 30*24*time.Hour {
		return LicenceProtocol.StatusExpiring
	}
	return LicenceProtocol.StatusValid
}

// issue - 签发运行面信封（SDK 签发链路，字节语义已由 golden 向量锚定）
func (this *fakePlatform) issue(fingerprint string) (LicenceProtocol.Envelope, error) {

	payload := LicenceProtocol.Payload{
		LicenseId: "LIC-2026-000123", UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		Environment: "production", ValidFrom: "2026-01-01T00:00:00Z",
		ValidUntil: this.validUntil, GraceDays: this.graceDays,
		VersionRange: ">=2.0.0 <3.0.0",
		Entitlements: this.entitlements,
		Binding:      &LicenceProtocol.Binding{Type: "fingerprint", Value: fingerprint},
		IssuedAt:     time.Now().UTC().Format(time.RFC3339), KeyVersion: "license-key-2026-01", Nonce: LicenceProtocol.Licence.Nonce(),
		BindingPolicy: LicenceProtocol.BindingPolicySingle, SeatLimit: 1,
	}
	return LicenceProtocol.Licence.Payload(payload).Seed(this.seed).Issue()
}

// readAll - 读取请求体
func readAll(request *http.Request) ([]byte, error) {

	defer func() { _ = request.Body.Close() }()
	buffer := make([]byte, 0, 512)
	tmp := make([]byte, 512)
	for {
		n, err := request.Body.Read(tmp)
		buffer = append(buffer, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return buffer, nil
}

// writeJson - 写 JSON 响应
func writeJson(writer http.ResponseWriter, data map[string]any) {

	writer.Header().Set("Content-Type", "application/json")
	raw, _ := json.Marshal(data)
	_, _ = writer.Write(raw)
}

// writeNotFound - 写模糊 404（契约 §5）
func writeNotFound(writer http.ResponseWriter) {

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusNotFound)
	raw, _ := json.Marshal(map[string]any{"status": LicenceProtocol.StatusNotFound, "serverTime": time.Now().UnixMilli(), "message": "许可证或实例信息无效"})
	_, _ = writer.Write(raw)
}

// handleUpdateCheck - 更新检查（平台 update-runtime.check 行为镜像：
// 版本选择 + 升级权门控 + 灰度 + release-key 签名清单）
func (this *fakePlatform) handleUpdateCheck(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params checkUpdateBody
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}

	status := this.status()
	if !LicenceProtocol.PassThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		return
	}

	for index, version := range this.versions {
		if cmp, _ := LicenceProtocol.CompareVersion(version.version, params.Version); cmp <= 0 {
			continue
		}
		if !LicenceProtocol.VersionInRange(params.Version, version.sourceRange) {
			continue
		}
		if cmp, _ := LicenceProtocol.CompareVersion(params.Version, version.minUpgrade); version.minUpgrade != "" &&
			cmp < 0 {
			continue
		}
		// 版本声明了平台架构且不包含客户端上报架构时跳过（多架构版本：数组包含即命中）
		if len(version.osArch) > 0 && params.OsArch != "" && !slices.Contains(version.osArch, params.OsArch) {
			continue
		}
		// 升级权：发布时间晚于 upgradeUntil 则无权升级
		if this.upgradeUntil > 0 && version.releasedAt > this.upgradeUntil {
			continue
		}
		if version.grayMode == "percent" && version.grayPercent <= 0 {
			continue
		}

		artifacts, err := this.signManifestArtifacts(version)
		if err != nil {
			writeJson(writer, map[string]any{"status": LicenceProtocol.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		manifest, err := issueManifest(ManifestPayload{
			ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
			Version: version.version, BuildNumber: version.buildNumber,
			SourceVersionRange: version.sourceRange, MinUpgradeVersion: version.minUpgrade,
			Artifacts: artifacts,
			IssuedAt:  time.Now().UTC().Format(time.RFC3339), KeyVersion: "release-key-2026-01", Nonce: LicenceProtocol.Licence.Nonce(),
			UpdatePolicy: version.policy,
		}, this.releaseSeed)
		if err != nil {
			writeJson(writer, map[string]any{"status": LicenceProtocol.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		if this.tamperManifest {
			tampered, _ := LicenceProtocol.SignPayload([]byte("tampered-manifest"), this.releaseSeed)
			manifest.Signature = tampered
		}
		_ = index
		writeJson(writer, map[string]any{
			"status": status, "serverTime": time.Now().UnixMilli(), "update": true, "manifest": manifest,
		})
		return
	}
	writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli(), "update": false})
}

// signManifestArtifacts - 为假版本签发发布物清单项（多发布物优先，否则回退单 artifactData 兼容既有用例）
func (this *fakePlatform) signManifestArtifacts(version fakeVersion) ([]ManifestArtifact, error) {

	if len(version.artifacts) > 0 {
		var artifacts []ManifestArtifact
		for index, item := range version.artifacts {
			artifactNo := "ART-2026-" + strings.ReplaceAll(version.version, ".", "") + strconv.Itoa(index)
			sum := sha256.Sum256(item.data)
			sha256Hex := hex.EncodeToString(sum[:])
			payload, _ := json.Marshal(ArtifactPayload{ArtifactNo: artifactNo, Version: version.version, Sha256: sha256Hex})
			sign, err := LicenceProtocol.SignPayload(payload, this.releaseSeed)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, ManifestArtifact{
				ArtifactNo: artifactNo, FileName: item.fileName,
				Url:  this.server.URL + "/files/" + item.fileName,
				Size: int64(len(item.data)), OsArch: item.osArch,
				Sha256: sha256Hex, Signature: sign, KeyVersion: "release-key-2026-01",
				ArtifactType: item.artifactType, SourceVersion: item.sourceVersion,
			})
		}
		return artifacts, nil
	}

	artifactNo := "ART-2026-" + strings.ReplaceAll(version.version, ".", "")
	sum := sha256.Sum256(version.artifactData)
	sha256Hex := hex.EncodeToString(sum[:])
	payload, _ := json.Marshal(ArtifactPayload{ArtifactNo: artifactNo, Version: version.version, Sha256: sha256Hex})
	sign, err := LicenceProtocol.SignPayload(payload, this.releaseSeed)
	if err != nil {
		return nil, err
	}
	// 回退分支无独立发布物 osArch，取版本声明的首个架构作为该发布物架构
	arch := ""
	if len(version.osArch) > 0 {
		arch = version.osArch[0]
	}
	return []ManifestArtifact{{
		ArtifactNo: artifactNo, FileName: "app-" + version.version + ".tar.gz",
		Url:  this.server.URL + "/files/" + version.version,
		Size: int64(len(version.artifactData)), OsArch: arch,
		Sha256: sha256Hex, Signature: sign, KeyVersion: "release-key-2026-01",
	}}, nil
}

// handleUpdateReport - 升级结果上报（recordNo 为空创建，非空推进）
func (this *fakePlatform) handleUpdateReport(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params map[string]any
	_ = json.Unmarshal(body, &params)
	recordNo, _ := params["recordNo"].(string)
	if recordNo == "" {
		this.reportSeq++
		recordNo = "UPG-2026-" + strings.Repeat("0", 6-len(strconv.Itoa(this.reportSeq))) + strconv.Itoa(this.reportSeq)
		params["recordNo"] = recordNo
	}
	this.reports = append(this.reports, params)
	writeJson(writer, map[string]any{"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(), "recordNo": recordNo})
}

// handleUpdateLogs - 升级过程日志追加
func (this *fakePlatform) handleUpdateLogs(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	writeJson(writer, map[string]any{"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli()})
}

// handlePushbackConfigs - 配置回推（客户端权威全量快照的平台 diff 行为简镜像：
// 记录批次并返回 created=len(items) 的 diff 统计；pushbackCode 非 0 时模拟拒绝）
func (this *fakePlatform) handlePushbackConfigs(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	if this.pushbackCode == http.StatusNotFound {
		writeNotFound(writer)
		return
	}
	var params pushbackBody
	if err := json.Unmarshal(body, &params); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	if this.pushbackCode == http.StatusBadRequest {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		raw, _ := json.Marshal(map[string]any{"message": "配置键非法：Bad.Key"})
		_, _ = writer.Write(raw)
		return
	}
	this.pushbacks = append(this.pushbacks, fakePushback{
		tenantID: params.TenantID, items: params.Items, clientPushID: params.ClientPushID,
	})
	writeJson(writer, map[string]any{
		"batch_id": 42, "created": len(params.Items), "updated": 0, "deleted": 0, "unchanged": 0,
		"pushed_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handlePushbackDefinitions - 配置定义反推（平台行为简镜像：记录批次并返回
// groups/configs 分别计数；pushbackDefCode 非 0 时模拟 403 开关关闭 / 400 校验失败带 errors 明细）
func (this *fakePlatform) handlePushbackDefinitions(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params pushbackDefinitionsBody
	if err := json.Unmarshal(body, &params); err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	switch this.pushbackDefCode {
	case http.StatusForbidden:
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		raw, _ := json.Marshal(map[string]any{"message": "项目未开启定义反推模式"})
		_, _ = writer.Write(raw)
		return
	case http.StatusBadRequest:
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		raw, _ := json.Marshal(map[string]any{
			"message": "配置定义校验失败",
			"errors": []map[string]string{
				{"where": "configs[0].options", "message": "select 类型必须提供非空 options"},
				{"where": "configs[1].key", "message": "配置键格式非法：Bad.Key"},
			},
		})
		_, _ = writer.Write(raw)
		return
	}
	this.definitionPushbacks = append(this.definitionPushbacks, fakeDefinitionPushback{
		rawBody: append([]byte(nil), body...), groups: params.Groups, configs: params.Configs, clientPushID: params.ClientPushID,
	})
	writeJson(writer, map[string]any{
		"batch_id":  43,
		"groups":    map[string]any{"created": len(params.Groups), "updated": 0, "deleted": 0, "unchanged": 0},
		"configs":   map[string]any{"created": len(params.Configs), "updated": 0, "deleted": 0, "unchanged": 0},
		"pushed_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// handleCommandPull - 指令领取：凭证校验（签名头同 validate）→ 放行态判定 → 弹出全部待执行指令；
// 空批次按 timeoutMs 短暂挂起模拟长轮询（上限 20ms，避免拖慢测试）。
func (this *fakePlatform) handleCommandPull(writer http.ResponseWriter, request *http.Request, body []byte) {

	var params struct {
		LicenseNo string `json:"licenseNo"`
		TimeoutMs int32  `json:"timeoutMs"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	this.mu.Lock()
	if !this.credential(writer, request, body) {
		this.mu.Unlock()
		return
	}
	status := this.status()
	if !LicenceProtocol.PassThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		this.mu.Unlock()
		return
	}
	commands := this.commands
	this.commands = nil
	this.commandPullCalls++
	this.mu.Unlock()

	if len(commands) == 0 && params.TimeoutMs > 0 {
		hold := time.Duration(params.TimeoutMs) * time.Millisecond
		if hold > 20*time.Millisecond {
			hold = 20 * time.Millisecond
		}
		time.Sleep(hold)
	}
	writeJson(writer, map[string]any{
		"status": status, "serverTime": time.Now().UnixMilli(), "commands": commands,
	})
}

// handleCommandReport - 指令回执：凭证校验 → 记录回执（幂等语义简化镜像：一律接收返回成功）
func (this *fakePlatform) handleCommandReport(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params map[string]any
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	this.commandReports = append(this.commandReports, params)
	writeJson(writer, map[string]any{"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli()})
}

// handleFileDownload - 发布物下载（/files/{version}）
func (this *fakePlatform) handleFileDownload(writer http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	key := strings.TrimPrefix(request.URL.Path, "/files/")
	// 多发布物按 fileName 匹配
	for _, item := range this.versions {
		for _, artifact := range item.artifacts {
			if artifact.fileName == key {
				_, _ = writer.Write(artifact.data)
				return
			}
		}
	}
	// 兼容单 artifactData（URL = /files/{version}）
	for _, item := range this.versions {
		if item.version == key {
			_, _ = writer.Write(item.artifactData)
			return
		}
	}
	writer.WriteHeader(http.StatusNotFound)
}

// tenantStatus - 租户状态判定（status 覆盖优先，否则按时间维度）
func (this *fakePlatform) tenantStatus(tenant fakeTenant) string {

	if tenant.status != "" {
		return tenant.status
	}
	return LicenceProtocol.LocalStatus(time.Now().UnixMilli(), tenant.validUntil, tenant.graceDays)
}

// issueTenantEnvelope - 签发租户信封（license-key 签名）
func (this *fakePlatform) issueTenantEnvelope(code string, tenant fakeTenant) (TenantEnvelope, error) {

	return issueTenant(TenantPayload{
		GrantId: "TEN-2026-000001", TenantCode: code, UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001",
		PlanCode: "pro", Environment: "production", SubscriptionType: "yearly",
		ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: tenant.validUntil, GraceDays: tenant.graceDays,
		Features: tenant.features, Limits: map[string]int64{}, MenuCodes: []string{"dashboard"},
		TenantManifestVersion: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339),
		KeyVersion: "license-key-2026-01", Nonce: LicenceProtocol.Licence.Nonce(),
	}, this.seed)
}

// handleTenantSync - 租户全量/增量同步
func (this *fakePlatform) handleTenantSync(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	tenants := make([]map[string]any, 0, len(this.tenants))
	for code, tenant := range this.tenants {
		status := this.tenantStatus(tenant)
		item := map[string]any{"tenantCode": code, "status": status}
		if LicenceProtocol.PassThrough(status) {
			envelope, err := this.issueTenantEnvelope(code, tenant)
			if err == nil {
				item["envelope"] = envelope
			}
		}
		tenants = append(tenants, item)
	}
	writeJson(writer, map[string]any{
		"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(), "syncTime": time.Now().UnixMilli(),
		"manifests": map[string]any{"platform": nil, "tenant": map[string]any{"version": 1, "menus": []any{}}}, "tenants": tenants,
	})
}

// handleTenantSearch - 按租户编码前缀搜索可登录租户。
func (this *fakePlatform) handleTenantSearch(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		Prefix string `json:"prefix"`
	}
	_ = json.Unmarshal(body, &params)
	items := make([]map[string]any, 0)
	for code, tenant := range this.tenants {
		if strings.HasPrefix(strings.ToLower(code), strings.ToLower(params.Prefix)) && LicenceProtocol.PassThrough(this.tenantStatus(tenant)) {
			items = append(items, map[string]any{"tenantCode": code, "tenantName": code + " 租户"})
		}
	}
	writeJson(writer, map[string]any{
		"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(), "tenants": items,
	})
}

// handleTenantValidate - 单租户实时校验
func (this *fakePlatform) handleTenantValidate(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		TenantCode string `json:"tenantCode"`
	}
	_ = json.Unmarshal(body, &params)
	tenant, exist := this.tenants[params.TenantCode]
	if !exist {
		writeNotFound(writer)
		return
	}
	status := this.tenantStatus(tenant)
	response := map[string]any{"status": status, "serverTime": time.Now().UnixMilli()}
	if LicenceProtocol.PassThrough(status) {
		envelope, err := this.issueTenantEnvelope(params.TenantCode, tenant)
		if err == nil {
			response["envelope"] = envelope
		}
	}
	writeJson(writer, response)
}

// handleTenantCurrent - 租户当前信封
func (this *fakePlatform) handleTenantCurrent(writer http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, nil) {
		return
	}
	code := request.URL.Query().Get("tenantCode")
	tenant, exist := this.tenants[code]
	if !exist {
		writeNotFound(writer)
		return
	}
	status := this.tenantStatus(tenant)
	response := map[string]any{"status": status, "serverTime": time.Now().UnixMilli()}
	if LicenceProtocol.PassThrough(status) {
		envelope, err := this.issueTenantEnvelope(code, tenant)
		if err == nil {
			response["envelope"] = envelope
		}
	}
	writeJson(writer, response)
}

// ============================= 端到端测试 =============================

// testOptions - 测试用客户端配置（显式指纹，避免依赖真实硬件采集）
func testOptions(platform *fakePlatform, dir string) Options {
	return Options{
		ServerURL: platform.server.URL, LicenseNo: "LIC-2026-000123", Salt: "test-salt",
		PublicKeys:        map[string]string{"license-key-2026-01": platform.publicKey},
		ReleasePublicKeys: map[string]string{"release-key-2026-01": platform.releasePublicKey},
		StorageDir:        dir,
		Fingerprint:       "test-fingerprint",
		DeviceName:        "test-device",
		Version:           "2.3.1",
		RefreshInterval:   100 * time.Millisecond,
	}
}

// TestClientActivateAndRefresh - 首激活 + 后台滑动刷新 + 权益闸门 + 状态加密落盘
func TestClientActivateAndRefresh(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if client.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("激活后状态应为 VALID，实际 %s", client.Status())
	}
	if client.SeatNo() != "SEAT-2026-000001" {
		t.Fatalf("激活后席位编号错误: %s", client.SeatNo())
	}
	platform.mu.Lock()
	deviceName := platform.deviceName
	platform.mu.Unlock()
	if deviceName != "test-device" {
		t.Fatalf("设备名称未随激活上报: %s", deviceName)
	}
	if granted, ok := client.GetBool("report.advanced"); !ok || !granted {
		t.Fatalf("GetBool(report.advanced) = %v %v, want true true", granted, ok)
	}
	if granted, ok := client.GetBool("ai.chat"); !ok || granted {
		t.Fatalf("GetBool(ai.chat) = %v %v, want false true", granted, ok)
	}
	if _, ok := client.GetBool("max_users"); ok {
		t.Fatalf("数值型权益按布尔读取应返回 ok=false")
	}
	if value, ok := client.GetInt("max_users"); !ok || value != 100 {
		t.Fatalf("GetInt(max_users) = %v %v, want 100 true", value, ok)
	}
	if _, ok := client.GetInt("max_storage_gb"); ok {
		t.Fatalf("小数权益按整数读取应返回 ok=false")
	}
	if value, ok := client.GetFloat("max_storage_gb"); !ok || value != 2.5 {
		t.Fatalf("GetFloat(max_storage_gb) = %v %v, want 2.5 true", value, ok)
	}
	if value, ok := client.GetFloat("max_users"); !ok || value != 100 {
		t.Fatalf("GetFloat(max_users) = %v %v, want 100 true", value, ok)
	}
	if _, ok := client.GetBool("missing"); ok {
		t.Fatalf("缺省键应返回 ok=false")
	}
	if _, ok := client.GetInt("report.advanced"); ok {
		t.Fatalf("布尔权益按整数读取应返回 ok=false")
	}
	if !client.CheckVersion("2.3.1") || client.CheckVersion("3.0.0") {
		t.Fatalf("CheckVersion 判定错误")
	}

	// 后台循环应完成至少一次滑动刷新
	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	validateCalls := platform.validateCalls
	platform.mu.Unlock()
	if validateCalls < 1 {
		t.Fatalf("后台刷新未执行 validate")
	}

	// 状态文件必须存在且密文不含 token 明文
	entries, err := os.ReadDir(client.options.StorageDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("状态文件异常: %v %v", entries, err)
	}
	raw, _ := os.ReadFile(filepath.Join(client.options.StorageDir, entries[0].Name()))
	client.mu.RLock()
	token := client.state.ActivationToken
	client.mu.RUnlock()
	if token == "" || strings.Contains(string(raw), token) {
		t.Fatalf("状态文件未加密或泄露 token 明文")
	}
}

// TestClientResumeWithoutReactivate - 进程重启后从加密状态恢复，不重复激活
func TestClientResumeWithoutReactivate(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()

	first, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = first.Start(t.Context()); err != nil {
		t.Fatalf("首次 Start 失败: %v", err)
	}
	first.Stop()

	second, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = second.Start(t.Context()); err != nil {
		t.Fatalf("重启 Start 失败: %v", err)
	}
	defer second.Stop()

	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()
	if activateCalls != 1 {
		t.Fatalf("重启不应重复激活，activate 调用次数 %d", activateCalls)
	}
	if second.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("恢复后状态应为 VALID，实际 %s", second.Status())
	}
}

// TestClientRejectedActivationDoesNotPoisonState - 首次激活被拒绝不得落盘缺失信封的半成品状态。
func TestClientRejectedActivationDoesNotPoisonState(t *testing.T) {

	platform := newFakePlatform(t)
	platform.mu.Lock()
	platform.validUntil = time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	platform.mu.Unlock()
	dir := t.TempDir()

	first, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = first.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "激活被拒绝："+LicenceProtocol.StatusExpired) {
		t.Fatalf("首次激活应返回业务拒绝，实际: %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("读取状态目录失败: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("激活被拒绝不应保留无信封状态文件: %v", entries)
	}

	second, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("第二次 New 失败: %v", err)
	}
	if err = second.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "激活被拒绝："+LicenceProtocol.StatusExpired) {
		t.Fatalf("重启后应继续返回业务拒绝而非状态解析错误，实际: %v", err)
	}
}

// TestClientHealsLegacyEnvelopeLessState - 升级后自动清理旧版本已落盘的无信封毒状态。
func TestClientHealsLegacyEnvelopeLessState(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	legacy, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	raw, err := json.Marshal(runtimeState{Status: LicenceProtocol.StatusExpired, ClientSeed: "legacy-incomplete-seed"})
	if err != nil {
		t.Fatalf("构造旧状态失败: %v", err)
	}
	if err = legacy.store.Save(raw); err != nil {
		t.Fatalf("写入旧状态失败: %v", err)
	}

	restored, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("重启 New 失败: %v", err)
	}
	if err = restored.Start(t.Context()); err != nil {
		t.Fatalf("旧毒状态应自愈并重新激活，实际: %v", err)
	}
	defer restored.Stop()
	if restored.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("自愈后应重新激活为 VALID，实际: %s", restored.Status())
	}
}

// TestClientOfflineGrace - 平台不可达时按本地缓存信封降级运行（契约 §5）
func TestClientOfflineGrace(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	// 掐断平台，等待下一轮 tick 走离线降级（永久授权信封 → 本地仍 VALID）
	platform.server.Close()
	time.Sleep(350 * time.Millisecond)
	if client.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("离线降级应保持 VALID（永久授权信封），实际 %s", client.Status())
	}
}

// TestClientGraceLicense - 宽限期许可证：激活即 GRACE，离线仍 GRACE
func TestClientGraceLicense(t *testing.T) {

	platform := newFakePlatform(t)
	platform.mu.Lock()
	platform.validUntil = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339) // 已过 validUntil 但在宽限内
	platform.mu.Unlock()

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if client.Status() != LicenceProtocol.StatusGrace {
		t.Fatalf("宽限期激活应标记 GRACE，实际 %s", client.Status())
	}
	platform.server.Close()
	time.Sleep(350 * time.Millisecond)
	if client.Status() != LicenceProtocol.StatusGrace {
		t.Fatalf("离线宽限应保持 GRACE，实际 %s", client.Status())
	}
}

// TestClientEnvelopeRefresh - 载荷变化（权益调整）时 validate 下发新信封，客户端替换缓存
func TestClientEnvelopeRefresh(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	// 平台侧调整权益并递增载荷版本
	platform.mu.Lock()
	platform.entitlements["ai.chat"] = true
	platform.version++
	platform.mu.Unlock()

	time.Sleep(350 * time.Millisecond)
	if granted, ok := client.GetBool("ai.chat"); !ok || !granted {
		t.Fatalf("新信封未生效：ai.chat 应为已授权")
	}
}

// TestClientBadSignRejected - 私钥被篡改后请求验签失败，平台模糊 404
func TestClientBadSignRejected(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	// 篡改内存中的客户端私钥（等价于攻击者伪造请求）
	seed, _, _ := LicenceProtocol.GenerateKeyPair()
	client.mu.Lock()
	client.state.ClientSeed = hex.EncodeToString(seed)
	client.mu.Unlock()

	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	calls := platform.validateCalls
	platform.mu.Unlock()
	t.Logf("validateCalls=%d status=%s", calls, client.Status())
	if client.Status() != LicenceProtocol.StatusNotFound {
		t.Fatalf("验签失败应得 NOT_FOUND，实际 %s（validateCalls=%d）", client.Status(), calls)
	}
}

// TestClientReactivate - 重新激活：生成新密钥对并重新绑定（换机/令牌丢失路径）
func TestClientReactivate(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	client.mu.RLock()
	oldSeed := client.state.ClientSeed
	client.mu.RUnlock()

	if err = client.Reactivate(t.Context()); err != nil {
		t.Fatalf("Reactivate 失败: %v", err)
	}

	client.mu.RLock()
	newSeed := client.state.ClientSeed
	client.mu.RUnlock()
	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()

	if newSeed == oldSeed {
		t.Fatalf("重新激活应生成新密钥对")
	}
	if activateCalls != 2 {
		t.Fatalf("重新激活应再调一次 activate，实际 %d", activateCalls)
	}
	if client.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("重新激活后应为 VALID，实际 %s", client.Status())
	}
}

// TestClientReset - Reset 只清本机状态，不调用平台；再次 Start 才重新激活并恢复席位信息。
func TestClientReset(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	client.Stop()

	if err = client.Reset(); err != nil {
		t.Fatalf("Reset 失败: %v", err)
	}
	if client.Status() != "" || client.SeatNo() != "" {
		t.Fatalf("Reset 后运行状态未清空: status=%s seat=%s", client.Status(), client.SeatNo())
	}
	platform.mu.Lock()
	callsAfterReset := platform.activateCalls
	platform.mu.Unlock()
	if callsAfterReset != 1 {
		t.Fatalf("Reset 不应通知平台，activate 调用次数 %d", callsAfterReset)
	}

	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Reset 后重新 Start 失败: %v", err)
	}
	defer client.Stop()
	if client.SeatNo() != "SEAT-2026-000001" {
		t.Fatalf("重新激活后未恢复席位编号: %s", client.SeatNo())
	}
	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()
	if activateCalls != 2 {
		t.Fatalf("Reset 后 Start 应重新激活，实际调用 %d 次", activateCalls)
	}
}

// TestSeatLimitExceededStopsBackgroundActivate - 席满拒绝后后台循环不得自动抢席。
func TestSeatLimitExceededStopsBackgroundActivate(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	platform.mu.Lock()
	platform.forceStatus = LicenceProtocol.StatusSeatLimitExceeded
	platform.mu.Unlock()
	if err = client.Reactivate(t.Context()); err == nil {
		t.Fatalf("席位已满时 Reactivate 应失败")
	}
	platform.mu.Lock()
	calls := platform.activateCalls
	platform.mu.Unlock()
	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	after := platform.activateCalls
	platform.mu.Unlock()
	if after != calls {
		t.Fatalf("SEAT_LIMIT_EXCEEDED 后不应后台重试激活: before=%d after=%d", calls, after)
	}
}

// ============================= 单元测试 =============================

// TestSignHeaders - 请求签名三要素：格式、时间窗、空 body 哈希、验签回路
func TestSignHeaders(t *testing.T) {

	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成密钥对失败: %v", err)
	}
	client := &Client{}
	client.mu.Lock()
	client.state.ActivationToken = "test-token"
	client.state.ClientSeed = hex.EncodeToString(seed)
	client.mu.Unlock()

	headers, err := client.signHeaders(http.MethodGet, "/api/v1/licenses/current?licenseNo=LIC-2026-000123", nil)
	if err != nil {
		t.Fatalf("signHeaders 失败: %v", err)
	}
	if headers["X-License-Token"] != "test-token" || headers["X-License-Nonce"] == "" {
		t.Fatalf("签名头部缺失: %v", headers)
	}
	// 时间戳须在 ±5 分钟窗口内
	ts, err := strconv.ParseInt(headers["X-License-Timestamp"], 10, 64)
	now := time.Now().UnixMilli()
	if err != nil || ts < now-5*60*1000 || ts > now+5*60*1000 {
		t.Fatalf("签名时间戳超出窗口: %v", headers["X-License-Timestamp"])
	}
	// 空 body 的 sha256 为公认常量
	const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	content := "GET\n/api/v1/licenses/current?licenseNo=LIC-2026-000123\n" +
		headers["X-License-Timestamp"] + "\n" + headers["X-License-Nonce"] + "\n" + emptySHA256
	if !LicenceProtocol.Licence.VerifyRaw([]byte(content), headers["X-License-Sign"], hex.EncodeToString(LicenceProtocol.Ed25519PublicKey(seed))) {
		t.Fatalf("请求签名验签失败")
	}
}

// TestFingerprintOverride - 显式指纹与自定义提供者（不依赖真实硬件）
func TestFingerprintOverride(t *testing.T) {

	if got, _ := FingerprintHash("salt", "explicit-hash", nil); got != "explicit-hash" {
		t.Fatalf("override 应原样返回，实际 %s", got)
	}
	got, err := FingerprintHash("salt", "", func() (string, error) { return "stable-source", nil })
	if err != nil || len(got) != 64 {
		t.Fatalf("provider 指纹异常: %v %v", got, err)
	}
	// 相同盐 + 相同因子源必须稳定
	again, _ := FingerprintHash("salt", "", func() (string, error) { return "stable-source", nil })
	if got != again {
		t.Fatalf("指纹不稳定: %s vs %s", got, again)
	}
	// 不同盐必须不同
	other, _ := FingerprintHash("other-salt", "", func() (string, error) { return "stable-source", nil })
	if got == other {
		t.Fatalf("不同盐指纹应不同")
	}
}

// TestStoreEncryption - 加密文件存储：落盘为密文，可读回原文，权限正确
func TestStoreEncryption(t *testing.T) {

	store, err := newFileStore(t.TempDir(), "LIC-2026-000123", "salt", "fingerprint")
	if err != nil {
		t.Fatalf("newFileStore 失败: %v", err)
	}
	secret := `{"activationToken":"plain-text-token"}`
	if err = store.Save([]byte(secret)); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	raw, err := store.Load()
	if err != nil || string(raw) != secret {
		t.Fatalf("Load 往返不一致: %v %v", string(raw), err)
	}
	disk, _ := os.ReadFile(store.path)
	if strings.Contains(string(disk), "plain-text-token") {
		t.Fatalf("落盘内容含明文")
	}
	// 不同密钥的存储无法解密
	other, _ := newFileStore(t.TempDir(), "LIC-2026-000123", "other-salt", "fingerprint")
	other.path = store.path
	if _, err = other.Load(); err == nil {
		t.Fatalf("错误密钥不应解密成功")
	}
}

// ============================= 事件订阅 =============================

// ============================= PullEvents（HTTP/gRPC 拉取原语） =============================
//
// 订阅器语义（前缀通配分发、水位推进、deliveryNo 去重、验签失败停摆）已随 EventSubscriber
// 迁往 callback 子包测试（callback/subscriber_test.go，假 EventPuller 注入信封）；
// 本文件只覆盖 Client.PullEvents 原语：事件批次拉取、sinceEventId 过滤、状态闸门与传输层未初始化。

// TestPullEventsHTTP - HTTP 长轮询拉取：事件按 eventId 升序返回、sinceEventId 过滤、信封原文可读
func TestPullEventsHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Close()

	if _, err = platform.pushEvent("saas.tenant.created", map[string]any{"tenantNo": "T1"}); err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}
	second, err := platform.pushEvent("saas.plan.updated", map[string]any{"planCode": "pro"})
	if err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}

	events, err := client.PullEvents(t.Context(), 0, time.Second)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 2 || events[0].EventId != 1 || events[1].EventId != second {
		t.Fatalf("事件批次异常: %+v", events)
	}
	// 信封原文可读（匿名结构解析，平台现场重签的 CallbackEnvelope JSON）
	var envelope struct {
		Payload struct {
			Event      string `json:"event"`
			DeliveryNo string `json:"deliveryNo"`
		} `json:"payload"`
	}
	if err = json.Unmarshal(events[0].Envelope, &envelope); err != nil {
		t.Fatalf("信封解析失败: %v", err)
	}
	if envelope.Payload.Event != "saas.tenant.created" || envelope.Payload.DeliveryNo != "SUB-EVT-2026-000001" {
		t.Fatalf("信封载荷异常: %+v", envelope.Payload)
	}

	// sinceEventId 过滤：只返回 > since 的事件
	events, err = client.PullEvents(t.Context(), 1, time.Second)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 1 || events[0].EventId != second {
		t.Fatalf("sinceEventId 过滤错误: %+v", events)
	}
}

// TestPullEventsHTTPNonPassThrough - 非放行态（宽限耗尽）错误文本归一
func TestPullEventsHTTPNonPassThrough(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	// 先以有效状态激活（EXPIRED 时 Start 会拒绝），随后让许可证宽限耗尽
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Close()
	platform.mu.Lock()
	platform.validUntil = time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	platform.mu.Unlock()

	_, err = client.PullEvents(t.Context(), 0, time.Second)
	if err == nil || err.Error() != "许可证非放行态：EXPIRED" {
		t.Fatalf("期望'许可证非放行态：EXPIRED'，实际 %v", err)
	}
}

// TestPullEventsTransportNotReady - 未 Start（传输层未初始化）直接返回错误
func TestPullEventsTransportNotReady(t *testing.T) {

	client := &Client{}
	if _, err := client.PullEvents(context.Background(), 0, 0); err == nil || err.Error() != "运行面传输层未初始化" {
		t.Fatalf("期望'运行面传输层未初始化'，实际 %v", err)
	}
}

// TestPullEventsGRPC - gRPC 服务端流经 PullEvents 收集事件（传输细节见 TestGRPCRuntimeTransportSubscribeEvents）
func TestPullEventsGRPC(t *testing.T) {

	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	server := runtimeEventServer{t: t, seed: seed, events: []fakeEvent{
		{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
		{eventId: 2, eventNo: "EVT-2026-000002", event: "saas.plan.updated", data: json.RawMessage(`{"planCode":"pro"}`)},
	}}
	transport, err := newTestEventTransport(t, server)
	if err != nil {
		t.Fatal(err)
	}
	clientSeed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{
		LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second,
		PublicKeys: map[string]string{"license-key-2026-01": "unused"},
	}}
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	transport.client = client
	client.transport = transport

	events, err := client.PullEvents(t.Context(), 0, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 2 || events[0].EventId != 1 || events[1].EventId != 2 {
		t.Fatalf("gRPC 事件收集异常: %+v", events)
	}
}

// TestPullEventsGRPCNonPassThrough - gRPC 非放行态错误归一（与 HTTP 侧同一错误文本）
func TestPullEventsGRPCNonPassThrough(t *testing.T) {

	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newTestEventTransport(t, runtimeEventServer{t: t, seed: seed, failCode: codes.FailedPrecondition})
	if err != nil {
		t.Fatal(err)
	}
	clientSeed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second}}
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	transport.client = client
	client.transport = transport

	_, err = client.PullEvents(t.Context(), 0, 100*time.Millisecond)
	if err == nil || err.Error() != "许可证非放行态：SUSPENDED" {
		t.Fatalf("期望'许可证非放行态：SUSPENDED'，实际 %v", err)
	}
}

// ============================= 激活码换证入座（ActivateByCode） =============================

// TestClientActivateByCode - HTTP 激活码换证：激活后以签名请求上送激活码三要素
// （activationCode + currentLicenseNo + releaseOldSeat），成功后返回已 Start 的新 Client
// （目标证激活占席），auto.json 落盘目标证，客户视图随响应落地。
func TestClientActivateByCode(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	result, err := client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	if err != nil {
		t.Fatalf("ActivateByCode 失败: %v", err)
	}
	defer result.Client.Stop()
	if result.Client == client {
		t.Fatal("换证应返回新 Client")
	}
	if result.LicenseNo != "LIC-2026-000999" || result.Salt != "salt-switch" {
		t.Fatalf("目标证映射不符: %s %s", result.LicenseNo, result.Salt)
	}
	if result.Customer == nil || result.Customer.MaskAccount != "r***@x.com" || result.Customer.BoundAt != 123 {
		t.Fatalf("客户视图映射不符: %+v", result.Customer)
	}
	if result.Client.Status() != LicenceProtocol.StatusValid {
		t.Fatalf("新客户端激活后状态应为 VALID，实际 %s", result.Client.Status())
	}

	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.provisionRequests...)
	platform.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("应上送 1 次激活码请求，实际 %d", len(requests))
	}
	request := requests[0]
	var body map[string]any
	if err = json.Unmarshal([]byte(request["body"]), &body); err != nil {
		t.Fatalf("请求体解析失败: %v", err)
	}
	if body["activationCode"] != "ABCD-EFGH-IJKL" {
		t.Fatalf("激活码未上送: %v", body)
	}
	if body["currentLicenseNo"] != "LIC-2026-000123" {
		t.Fatalf("当前许可证编号未上送: %v", body)
	}
	if body["releaseOldSeat"] != true {
		t.Fatalf("旧席位释放标记未上送: %v", body)
	}
	if _, exists := body["confirmReplace"]; exists {
		t.Fatalf("confirmReplace=false 不应上送: %v", body)
	}
	for _, key := range []string{"token", "timestamp", "nonce", "signature"} {
		if request[key] == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}

	// auto.json 必须落盘目标证（激活码已消费，启动恢复不得回退旧证）
	raw, err := os.ReadFile(filepath.Join(dir, autoMetadataFile))
	if err != nil {
		t.Fatalf("auto.json 未落盘: %v", err)
	}
	var metadata autoMetadata
	if err = json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("auto.json 解析失败: %v", err)
	}
	if metadata.LicenseNo != "LIC-2026-000999" || metadata.Salt != "salt-switch" {
		t.Fatalf("auto.json 目标证不符: %+v", metadata)
	}
}

// TestClientActivateByCodeFingerprintChallenge - 换证缺指纹时先完成挑战，
// 第二次签名请求必须携带与后续激活一致的指纹，不能直接按业务拒绝返回。
func TestClientActivateByCodeFingerprintChallenge(t *testing.T) {
	platform := newFakePlatform(t)
	platform.provisionRequireFingerprint = true
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	result, err := client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	if err != nil {
		t.Fatalf("挑战后换证失败: %v", err)
	}
	defer result.Client.Stop()
	expected, err := FingerprintHash("salt-switch", "test-fingerprint", nil)
	if err != nil {
		t.Fatal(err)
	}
	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.provisionRequests...)
	platform.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("缺指纹应先挑战再重试一次，实际请求 %d 次", len(requests))
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(requests[1]["body"]), &body); err != nil {
		t.Fatal(err)
	}
	if body["fingerprintHash"] != expected {
		t.Fatalf("挑战后的指纹不符: got %v want %s", body["fingerprintHash"], expected)
	}
}

// TestClientActivateByCodeConflict - 付费证冲突：平台返回 ACTIVATION_CONFLICT + 对比载荷，
// 以 *ActivationConflictError 透出（不消费码），confirmReplace=true 重发后成功换证。
func TestClientActivateByCodeConflict(t *testing.T) {

	platform := newFakePlatform(t)
	platform.provisionConflict = true
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	var conflict *ActivationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("冲突应可断言为 *ActivationConflictError，实际 %T（%v）", err, err)
	}
	if conflict.CurrentPlan.PlanCode != "std" || !conflict.CurrentPlan.Paid {
		t.Fatalf("当前套餐对比载荷不符: %+v", conflict.CurrentPlan)
	}
	if conflict.TargetPlan.PlanCode != "pro" || conflict.TargetPlan.LicenseNo != "LIC-2026-000999" {
		t.Fatalf("目标套餐对比载荷不符: %+v", conflict.TargetPlan)
	}
	if conflict.SeatUsed != 1 || conflict.SeatLimit != 3 {
		t.Fatalf("席位占用载荷不符: %d/%d", conflict.SeatUsed, conflict.SeatLimit)
	}

	// 用户确认后重发：confirmReplace=true 上送，平台放行换证
	platform.mu.Lock()
	platform.provisionConflict = false
	platform.mu.Unlock()
	result, err := client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", true)
	if err != nil {
		t.Fatalf("确认后 ActivateByCode 失败: %v", err)
	}
	defer result.Client.Stop()
	if result.LicenseNo != "LIC-2026-000999" {
		t.Fatalf("确认重发应换证成功: %s", result.LicenseNo)
	}
	platform.mu.Lock()
	last := platform.provisionRequests[len(platform.provisionRequests)-1]
	platform.mu.Unlock()
	var body map[string]any
	if err = json.Unmarshal([]byte(last["body"]), &body); err != nil {
		t.Fatal(err)
	}
	if body["confirmReplace"] != true {
		t.Fatalf("confirmReplace=true 未上送: %v", body)
	}
}

// TestClientActivateByCodeRejected - 业务拒绝（ACTIVATION_INVALID）以 *ProvisionError 原样透传。
func TestClientActivateByCodeRejected(t *testing.T) {

	platform := newFakePlatform(t)
	platform.provisionStatus = "ACTIVATION_INVALID"
	platform.provisionMessage = "激活码无效或已过期"
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.ActivateByCode(t.Context(), "EXPIRED-CODE", false)
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) {
		t.Fatalf("业务拒绝应可断言为 *ProvisionError，实际 %T", err)
	}
	if provisionErr.Status != "ACTIVATION_INVALID" || provisionErr.Message != "激活码无效或已过期" {
		t.Fatalf("业务拒绝透传不符: %+v", provisionErr)
	}
}

// TestClientActivateByCodeNotActivated - 未激活（无 token）调用直接报错且不发请求。
func TestClientActivateByCodeNotActivated(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	if err == nil || !strings.Contains(err.Error(), "客户端未激活") {
		t.Fatalf("未激活应返回指定错误，实际 %v", err)
	}
	platform.mu.Lock()
	requests := len(platform.provisionRequests)
	platform.mu.Unlock()
	if requests != 0 {
		t.Fatalf("未激活不应发起激活码请求，实际 %d 次", requests)
	}
}

// TestClientActivateByCodeNotFound - 平台 404（许可证已删除/路由模糊语义）与空 body
// 统一归一为「许可证或实例信息无效」，不得把空 body 的 JSON 解析错误抛给调用方。
func TestClientActivateByCodeNotFound(t *testing.T) {

	platform := newFakePlatform(t)
	platform.provisionCode = http.StatusNotFound
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	if err == nil || err.Error() != "许可证或实例信息无效" {
		t.Fatalf("404 应归一为「许可证或实例信息无效」，实际 %v", err)
	}
}

// TestClientActivateByCodeSameLicence - 同证幂等：平台返回当前许可证编号时短路为
// Reactivate 刷新信封，不构造新客户端、不改写 auto.json。
func TestClientActivateByCodeSameLicence(t *testing.T) {

	platform := newFakePlatform(t)
	platform.provisionLicenseNo = "LIC-2026-000123"
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	platform.mu.Lock()
	activateBefore := platform.activateCalls
	platform.mu.Unlock()
	result, err := client.ActivateByCode(t.Context(), "ABCD-EFGH-IJKL", false)
	if err != nil {
		t.Fatalf("同证 ActivateByCode 失败: %v", err)
	}
	if result.Client != client {
		t.Fatal("同证幂等应返回当前 Client")
	}
	if result.LicenseNo != "LIC-2026-000123" {
		t.Fatalf("同证幂等许可证编号不符: %s", result.LicenseNo)
	}
	platform.mu.Lock()
	activateAfter := platform.activateCalls
	platform.mu.Unlock()
	if activateAfter != activateBefore+1 {
		t.Fatalf("同证幂等应触发一次 Reactivate 激活（%d → %d）", activateBefore, activateAfter)
	}
	if _, err = os.Stat(filepath.Join(dir, autoMetadataFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("同证幂等不应写 auto.json: %v", err)
	}
}

// ============================= 客户账号绑定（CustomerBind） =============================

// TestClientCustomerBind - 激活后以签名请求上送一次性授权票据，成功返回脱敏客户视图。
func TestClientCustomerBind(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	result, err := client.CustomerBind(t.Context(), "ABCD-EFGH")
	if err != nil {
		t.Fatalf("CustomerBind 失败: %v", err)
	}
	if result.MaskAccount != "r***@x.com" || result.BoundAt != 123 {
		t.Fatalf("客户视图映射不符: %+v", result)
	}
	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.customerBindRequests...)
	platform.mu.Unlock()
	if len(requests) != 1 || requests[0]["body"] != `{"code":"ABCD-EFGH"}` {
		t.Fatalf("绑定请求体不符: %+v", requests)
	}
	for _, key := range []string{"token", "timestamp", "nonce", "signature"} {
		if requests[0][key] == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}
}

// TestClientCustomerBindRejected - 业务拒绝以 *ProvisionError 原样透传。
func TestClientCustomerBindRejected(t *testing.T) {
	platform := newFakePlatform(t)
	platform.customerBindStatus = "BIND_INVALID"
	platform.customerBindMessage = "授权票据无效或已过期"
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.CustomerBind(t.Context(), "EXPIRED-CODE")
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "BIND_INVALID" || provisionErr.Message != "授权票据无效或已过期" {
		t.Fatalf("业务拒绝透传不符: %T %+v", err, err)
	}
}

// TestClientCustomerBindNotActivated - 未激活直接报错且不发请求。
func TestClientCustomerBindNotActivated(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.CustomerBind(t.Context(), "ABCD-EFGH")
	if err == nil || err.Error() != "客户端未激活，无法绑定客户账号" {
		t.Fatalf("未激活应返回指定错误，实际 %v", err)
	}
	platform.mu.Lock()
	calls := platform.customerBindCalls
	platform.mu.Unlock()
	if calls != 0 {
		t.Fatalf("未激活不应发起绑定请求，实际 %d 次", calls)
	}
}

// TestClientCustomerBindNotFound - 404 与空 body 归一为模糊凭证错误。
func TestClientCustomerBindNotFound(t *testing.T) {
	platform := newFakePlatform(t)
	platform.customerBindCode = http.StatusNotFound
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	_, err = client.CustomerBind(t.Context(), "ABCD-EFGH")
	if err == nil || err.Error() != "许可证或实例信息无效" {
		t.Fatalf("404 应归一为「许可证或实例信息无效」，实际 %v", err)
	}
}
