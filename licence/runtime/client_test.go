package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// features/limits - 载荷权益
	features map[string]bool
	limits   map[string]int64
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
	// events - 项目事件订阅队列（callback_events 简化镜像，eventId 单调递增）
	events   []fakeEvent
	eventSeq int64
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
		features:        map[string]bool{"report.advanced": true, "ai.chat": false},
		limits:          map[string]int64{"max_users": 100},
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
		Features:     this.features, Limits: this.limits,
		Binding:  &LicenceProtocol.Binding{Type: "fingerprint", Value: fingerprint},
		IssuedAt: time.Now().UTC().Format(time.RFC3339), KeyVersion: "license-key-2026-01", Nonce: LicenceProtocol.Licence.Nonce(),
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
	if !client.HasFeature("report.advanced") || client.HasFeature("ai.chat") {
		t.Fatalf("HasFeature 判定错误")
	}
	if value, exist := client.GetLimit("max_users"); !exist || value != 100 {
		t.Fatalf("GetLimit 判定错误: %v %v", value, exist)
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
	platform.features["ai.chat"] = true
	platform.version++
	platform.mu.Unlock()

	time.Sleep(350 * time.Millisecond)
	if !client.HasFeature("ai.chat") {
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
