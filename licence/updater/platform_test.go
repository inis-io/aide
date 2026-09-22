package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/licence"
	"github.com/inis-io/aide/licence/callback"
)

// ============================= 假授权平台（契约行为镜像·更新链路迷你版） =============================

// fakePlatform - 更新链路假平台：实现契约的 activate/validate 与 updates/check、
// updates/report、events/subscribe、/files/ 下载，含 token 哈希校验与客户端公钥请求验签
// （时间窗 + 签名内容格式同契约 §2.4）。仅覆盖 updater 测试所需端点，恒放行态；
// 运行面全量夹具在根包 client_test.go，两边独立维护，禁止反向引用。
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
	// upgradeUntil - 升级权截止（毫秒，0 = 不限）
	upgradeUntil int64
	// versions - 已发布版本（更新检查候选）
	versions []fakeVersion
	// reportSeq - 升级记录编号序列
	reportSeq int
	// reports - 升级上报轨迹（handleUpdateReport 记录，供流水线断言）
	reports []map[string]any
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
	policy       *licence.ManifestUpdatePolicy
}

// fakeEvent - 假订阅事件（eventId 即客户端水位，data 为事件摘要）
type fakeEvent struct {
	eventId int64
	eventNo string
	event   string
	data    json.RawMessage
}

// newFakePlatform - 创建假平台（永久授权、升级权不限）
func newFakePlatform(t *testing.T) *fakePlatform {

	t.Helper()
	seed, publicKey := licence.Licence.GenerateKeyPair().KeyPair()
	releaseSeed, releasePublicKey := licence.Licence.GenerateKeyPair().KeyPair()
	platform := &fakePlatform{
		seed: seed, publicKey: publicKey,
		releaseSeed: releaseSeed, releasePublicKey: releasePublicKey,
	}
	platform.server = httptest.NewServer(http.HandlerFunc(platform.handle))
	t.Cleanup(platform.server.Close)
	return platform
}

// testOptions - 测试用客户端配置（显式指纹，避免依赖真实硬件采集）
func testOptions(platform *fakePlatform, dir string) licence.Options {
	return licence.Options{
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

// handle - 路由分发
func (this *fakePlatform) handle(writer http.ResponseWriter, request *http.Request) {

	body, _ := readAll(request)
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/activate":
		this.handleActivate(writer, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/licenses/validate":
		this.handleValidate(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/updates/check":
		this.handleUpdateCheck(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/updates/report":
		this.handleUpdateReport(writer, request, body)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/events/subscribe":
		this.handleSubscribe(writer, request, body)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/files/"):
		this.handleFileDownload(writer, request)
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// handleActivate - 激活：注册客户端公钥与指纹，签发信封 + 一次性令牌
func (this *fakePlatform) handleActivate(writer http.ResponseWriter, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	var params struct {
		FingerprintHash string `json:"fingerprintHash"`
		ClientPublicKey string `json:"clientPublicKey"`
		DeviceName      string `json:"deviceName"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	this.clientPublicKey = params.ClientPublicKey
	this.deviceName = params.DeviceName
	token := licence.Licence.Nonce() + licence.Licence.Nonce() + licence.Licence.Nonce() // 48 字节 hex
	sum := sha256.Sum256([]byte(token))
	this.tokenHash = hex.EncodeToString(sum[:])
	this.expiresAt = time.Now().UnixMilli() + 7*24*3600*1000

	envelope, err := licence.Licence.Payload(licence.Payload{
		LicenseId: "LIC-2026-000123", UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001",
		InstanceId: "INS-2026-000001", Environment: "production", ValidFrom: "2026-01-01T00:00:00Z",
		ValidUntil: "", GraceDays: 7, VersionRange: ">=2.0.0 <3.0.0",
		Features: map[string]bool{"report.advanced": true}, Limits: map[string]int64{"max_users": 100},
		Binding:  &licence.Binding{Type: "fingerprint", Value: params.FingerprintHash},
		IssuedAt: time.Now().UTC().Format(time.RFC3339), KeyVersion: "license-key-2026-01",
		Nonce: licence.Licence.Nonce(), BindingPolicy: licence.BindingPolicySingle, SeatLimit: 1,
	}).Seed(this.seed).Issue()
	if err != nil {
		writeJson(writer, map[string]any{"status": licence.StatusError, "serverTime": time.Now().UnixMilli(), "message": "签名服务未就绪"})
		return
	}
	writeJson(writer, map[string]any{
		"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(),
		"envelope": envelope, "activationNo": "ACT-2026-000001",
		"activationToken": token, "seatNo": "SEAT-2026-000001", "expiresAt": this.expiresAt,
	})
}

// handleValidate - 校验：token 哈希 + 请求验签 → 滑动刷新（本夹具恒放行态）
func (this *fakePlatform) handleValidate(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	this.expiresAt = time.Now().UnixMilli() + 7*24*3600*1000
	writeJson(writer, map[string]any{
		"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(), "expiresAt": this.expiresAt,
	})
}

// credential - 凭证校验 + 请求验签（契约 §2.4 服务端行为镜像）
func (this *fakePlatform) credential(writer http.ResponseWriter, request *http.Request, body []byte) bool {

	sum := sha256.Sum256([]byte(request.Header.Get("X-License-Token")))
	if this.tokenHash == "" || hex.EncodeToString(sum[:]) != this.tokenHash {
		writeJson(writer, map[string]any{"status": licence.StatusExpired, "serverTime": time.Now().UnixMilli(), "message": "凭证无效或已过期，请重新激活"})
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
	if !licence.Licence.VerifyRaw([]byte(content), signature, this.clientPublicKey) {
		writeNotFound(writer)
		return false
	}
	return true
}

// handleUpdateCheck - 更新检查（平台 update-runtime.check 行为镜像：
// 版本选择 + 升级权门控 + 灰度 + release-key 签名清单；本夹具恒放行态）
func (this *fakePlatform) handleUpdateCheck(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		LicenseNo string `json:"licenseNo"`
		OsArch    string `json:"osArch"`
		Version   string `json:"version"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}

	for _, version := range this.versions {
		// 目标版本必须高于当前版本
		if cmp, _ := licence.CompareVersion(version.version, params.Version); cmp <= 0 {
			continue
		}
		if !licence.VersionInRange(params.Version, version.sourceRange) {
			continue
		}
		if version.minUpgrade != "" {
			if cmp, _ := licence.CompareVersion(params.Version, version.minUpgrade); cmp < 0 {
				continue
			}
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
			writeJson(writer, map[string]any{"status": licence.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		manifest, err := this.issueManifest(version, artifacts)
		if err != nil {
			writeJson(writer, map[string]any{"status": licence.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		writeJson(writer, map[string]any{
			"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(), "update": true, "manifest": manifest,
		})
		return
	}
	writeJson(writer, map[string]any{"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(), "update": false})
}

// issueManifest - 签发更新清单（release-key 签名，与平台签发端同语义）
func (this *fakePlatform) issueManifest(version fakeVersion, artifacts []licence.ManifestArtifact) (licence.Manifest, error) {

	payload := licence.ManifestPayload{
		ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		Version: version.version, BuildNumber: version.buildNumber,
		SourceVersionRange: version.sourceRange, MinUpgradeVersion: version.minUpgrade,
		Artifacts: artifacts,
		IssuedAt:  time.Now().UTC().Format(time.RFC3339), KeyVersion: "release-key-2026-01",
		Nonce: licence.Licence.Nonce(), UpdatePolicy: version.policy,
	}
	payloadBytes, err := licence.MarshalManifestPayload(payload)
	if err != nil {
		return licence.Manifest{}, err
	}
	signature, err := licence.Licence.Seed(this.releaseSeed).Sign(payloadBytes)
	if err != nil {
		return licence.Manifest{}, err
	}
	return licence.Manifest{
		Version: licence.EnvelopeVersion, Algorithm: licence.Algorithm,
		Payload: payload, Signature: signature,
	}, nil
}

// signManifestArtifacts - 为假版本签发发布物清单项（多发布物优先，否则回退单 artifactData 兼容既有用例）
func (this *fakePlatform) signManifestArtifacts(version fakeVersion) ([]licence.ManifestArtifact, error) {

	if len(version.artifacts) > 0 {
		var artifacts []licence.ManifestArtifact
		for index, item := range version.artifacts {
			artifactNo := "ART-2026-" + strings.ReplaceAll(version.version, ".", "") + strconv.Itoa(index)
			sum := sha256.Sum256(item.data)
			sha256Hex := hex.EncodeToString(sum[:])
			payload, _ := json.Marshal(licence.ArtifactPayload{ArtifactNo: artifactNo, Version: version.version, Sha256: sha256Hex})
			sign, err := licence.Licence.Seed(this.releaseSeed).Sign(payload)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, licence.ManifestArtifact{
				ArtifactNo: artifactNo, FileName: item.fileName,
				Url: this.server.URL + "/files/" + item.fileName,
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
	payload, _ := json.Marshal(licence.ArtifactPayload{ArtifactNo: artifactNo, Version: version.version, Sha256: sha256Hex})
	sign, err := licence.Licence.Seed(this.releaseSeed).Sign(payload)
	if err != nil {
		return nil, err
	}
	// 回退分支无独立发布物 osArch，取版本声明的首个架构作为该发布物架构
	arch := ""
	if len(version.osArch) > 0 {
		arch = version.osArch[0]
	}
	return []licence.ManifestArtifact{{
		ArtifactNo: artifactNo, FileName: "app-" + version.version + ".tar.gz",
		Url: this.server.URL + "/files/" + version.version,
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
	writeJson(writer, map[string]any{"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(), "recordNo": recordNo})
}

// handleSubscribe - 事件订阅：凭证校验 → 按 sinceEventId 过滤并现场重签（nonce 每次新鲜）
func (this *fakePlatform) handleSubscribe(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		LicenseNo    string `json:"licenseNo"`
		SinceEventId int64  `json:"sinceEventId"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	items := make([]map[string]any, 0, len(this.events))
	for _, event := range this.events {
		if event.eventId <= params.SinceEventId {
			continue
		}
		envelope, err := signCallbackEnvelope(this.seed, event)
		if err != nil {
			writeJson(writer, map[string]any{"status": licence.StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		items = append(items, map[string]any{"eventId": event.eventId, "envelope": envelope})
	}
	writeJson(writer, map[string]any{
		"status": licence.StatusValid, "serverTime": time.Now().UnixMilli(), "events": items,
	})
}

// signCallbackEnvelope - 用平台 seed 现场重签一条订阅事件信封（镜像平台订阅端点行为：
// nonce 每次新鲜、occurredAt 为当下、deliveryNo 稳定 SUB-{eventNo}）。
// updater 子包可 import callback，故直接使用 callback 包类型（与平台签发端字节级一致）。
func signCallbackEnvelope(seed []byte, event fakeEvent) (json.RawMessage, error) {

	payload := callback.CallbackPayload{
		EventNo: event.eventNo, DeliveryNo: "SUB-" + event.eventNo, Event: event.event,
		ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
		Nonce:      licence.Licence.Nonce(), KeyVersion: "license-key-2026-01",
		Data: event.data,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	signature, err := licence.Licence.Seed(seed).Sign(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(callback.CallbackEnvelope{
		Version: licence.EnvelopeVersion, Algorithm: licence.Algorithm,
		Payload: payload, Signature: signature,
	})
}

// handleFileDownload - 发布物下载（先按发布物 fileName 匹配，再按版本号匹配单 artifactData）
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

// ============================= 测试辅助 =============================

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
	raw, _ := json.Marshal(map[string]any{"status": licence.StatusNotFound, "serverTime": time.Now().UnixMilli(), "message": "许可证或实例信息无效"})
	_, _ = writer.Write(raw)
}
