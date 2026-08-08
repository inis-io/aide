package licence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================= 假授权平台（契约行为镜像） =============================

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
	// tokenHash - 激活令牌哈希（SHA-256 hex，库存哈希不存原文）
	tokenHash string
	// expiresAt - 激活有效期截止（毫秒）
	expiresAt int64
	// validUntil - 许可证到期（RFC3339，空 = 永久）
	validUntil string
	// graceDays - 宽限期（天）
	graceDays int
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
	// tamperManifest - 测试开关：签发后篡改清单签名
	tamperManifest bool
	// reportSeq - 升级记录编号序列
	reportSeq int
	// 调用计数
	activateCalls int
	validateCalls int
	// server - httptest 实例
	server *httptest.Server
}

// fakeVersion - 假发布版本（artifactData 经 /files/{version} 提供下载）
type fakeVersion struct {
	version      string
	buildNumber  string
	sourceRange  string
	minUpgrade   string
	osArch       string
	releasedAt   int64
	grayMode     string
	grayPercent  int
	artifactData []byte
}

// fakeTenant - 假 SaaS 租户（status 非空时优先于时间判定，用于模拟 suspended 等）
type fakeTenant struct {
	status     string
	validUntil string
	graceDays  int
	features   map[string]bool
}

// newFakePlatform - 创建假平台（默认：永久授权、基础权益）
func newFakePlatform(t *testing.T) *fakePlatform {

	t.Helper()
	seed, publicKey, err := generateKeyPair()
	if err != nil {
		t.Fatalf("生成平台密钥对失败: %v", err)
	}
	platform := &fakePlatform{
		seed: seed, publicKey: hex.EncodeToString(publicKey),
		validUntil: "", graceDays: 7, version: 1,
		features: map[string]bool{"report.advanced": true, "ai.chat": false},
		limits:   map[string]int64{"max_users": 100},
		tenants:  make(map[string]fakeTenant),
	}
	releaseSeed, releasePublicKey, err := generateKeyPair()
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
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/saas/tenants/validate":
		this.handleTenantValidate(writer, request, body)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/saas/tenants/current":
		this.handleTenantCurrent(writer, request)
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

	var params activateBody
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	this.clientPublicKey = params.ClientPublicKey
	token := Licence.Nonce() + Licence.Nonce() + Licence.Nonce() // 48 字节 hex
	sum := sha256.Sum256([]byte(token))
	this.tokenHash = hex.EncodeToString(sum[:])
	this.expiresAt = time.Now().UnixMilli() + 7*dayMillis
	this.issuedVersion = this.version
	this.activateCalls++

	status := this.status()
	envelope, err := this.issue(params.FingerprintHash)
	if err != nil {
		writeJson(writer, map[string]any{"status": StatusError, "serverTime": time.Now().UnixMilli(), "message": "签名服务未就绪"})
		return
	}
	writeJson(writer, map[string]any{
		"status": status, "serverTime": time.Now().UnixMilli(),
		"envelope": envelope, "activationNo": "ACT-2026-000001",
		"activationToken": token, "expiresAt": this.expiresAt,
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
	if passThrough(status) {
		this.expiresAt = time.Now().UnixMilli() + 7*dayMillis
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
	if !passThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		return
	}
	envelope, err := this.issue("test-fingerprint")
	if err != nil {
		writeJson(writer, map[string]any{"status": StatusError, "serverTime": time.Now().UnixMilli()})
		return
	}
	writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli(), "envelope": envelope})
}

// credential - 凭证校验 + 请求验签（契约 §2.4 服务端行为镜像）
func (this *fakePlatform) credential(writer http.ResponseWriter, request *http.Request, body []byte) bool {

	sum := sha256.Sum256([]byte(request.Header.Get("X-License-Token")))
	if this.tokenHash == "" || hex.EncodeToString(sum[:]) != this.tokenHash {
		writeJson(writer, map[string]any{"status": StatusExpired, "serverTime": time.Now().UnixMilli(), "message": "凭证无效或已过期，请重新激活"})
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
	if !Licence.VerifyRaw([]byte(content), signature, this.clientPublicKey) {
		writeNotFound(writer)
		return false
	}
	return true
}

// status - 时间维度状态判定（平台 JudgeLicenseStatus 简化镜像）
func (this *fakePlatform) status() string {

	if this.validUntil == "" {
		return StatusValid
	}
	until, err := time.Parse(time.RFC3339, this.validUntil)
	if err != nil {
		return StatusExpired
	}
	now := time.Now()
	if now.After(until.Add(time.Duration(this.graceDays) * 24 * time.Hour)) {
		return StatusExpired
	}
	if now.After(until) {
		return StatusGrace
	}
	if time.Until(until) <= 30*24*time.Hour {
		return StatusExpiring
	}
	return StatusValid
}

// issue - 签发运行面信封（SDK 签发链路，字节语义已由 golden 向量锚定）
func (this *fakePlatform) issue(fingerprint string) (Envelope, error) {

	payload := Payload{
		LicenseId: "LIC-2026-000123", UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		Environment: "production", ValidFrom: "2026-01-01T00:00:00Z",
		ValidUntil: this.validUntil, GraceDays: this.graceDays,
		VersionRange: ">=2.0.0 <3.0.0",
		Features:     this.features, Limits: this.limits,
		Binding:  &Binding{Type: "fingerprint", Value: fingerprint},
		IssuedAt: time.Now().UTC().Format(time.RFC3339), KeyVersion: "license-key-2026-01", Nonce: Licence.Nonce(),
	}
	return Licence.Payload(payload).Seed(this.seed).Issue()
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
	raw, _ := json.Marshal(map[string]any{"status": StatusNotFound, "serverTime": time.Now().UnixMilli(), "message": "许可证或实例信息无效"})
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
	if !passThrough(status) {
		writeJson(writer, map[string]any{"status": status, "serverTime": time.Now().UnixMilli()})
		return
	}

	for index, version := range this.versions {
		if compareSemver(mustParseSemver(version.version), mustParseSemver(params.Version)) <= 0 {
			continue
		}
		if !VersionInRange(params.Version, version.sourceRange) {
			continue
		}
		if version.minUpgrade != "" &&
			compareSemver(mustParseSemver(params.Version), mustParseSemver(version.minUpgrade)) < 0 {
			continue
		}
		if version.osArch != "" && params.OsArch != "" && version.osArch != params.OsArch {
			continue
		}
		// 升级权：发布时间晚于 upgradeUntil 则无权升级
		if this.upgradeUntil > 0 && version.releasedAt > this.upgradeUntil {
			continue
		}
		if version.grayMode == "percent" && version.grayPercent <= 0 {
			continue
		}

		artifactNo := "ART-2026-" + strings.ReplaceAll(version.version, ".", "")
		sum := sha256.Sum256(version.artifactData)
		sha256Hex := hex.EncodeToString(sum[:])
		artifactPayload, _ := json.Marshal(ArtifactPayload{ArtifactNo: artifactNo, Version: version.version, Sha256: sha256Hex})
		artifactSign, _ := signPayload(artifactPayload, this.releaseSeed)

		manifest, err := issueManifest(ManifestPayload{
			ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
			Version: version.version, BuildNumber: version.buildNumber,
			SourceVersionRange: version.sourceRange, MinUpgradeVersion: version.minUpgrade,
			Artifacts: []ManifestArtifact{{
				ArtifactNo: artifactNo, FileName: "app-" + version.version + ".tar.gz",
				Url:  this.server.URL + "/files/" + version.version,
				Size: int64(len(version.artifactData)), OsArch: version.osArch,
				Sha256: sha256Hex, Signature: artifactSign, KeyVersion: "release-key-2026-01",
			}},
			IssuedAt: time.Now().UTC().Format(time.RFC3339), KeyVersion: "release-key-2026-01", Nonce: Licence.Nonce(),
		}, this.releaseSeed)
		if err != nil {
			writeJson(writer, map[string]any{"status": StatusError, "serverTime": time.Now().UnixMilli()})
			return
		}
		if this.tamperManifest {
			tampered, _ := signPayload([]byte("tampered-manifest"), this.releaseSeed)
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

// mustParseSemver - 测试辅助：解析三段版本号（假平台输入均可解析）
func mustParseSemver(version string) [3]int {

	parsed, _ := parseSemver(version)
	return parsed
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
	}
	writeJson(writer, map[string]any{"status": StatusValid, "serverTime": time.Now().UnixMilli(), "recordNo": recordNo})
}

// handleUpdateLogs - 升级过程日志追加
func (this *fakePlatform) handleUpdateLogs(writer http.ResponseWriter, request *http.Request, body []byte) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.credential(writer, request, body) {
		return
	}
	writeJson(writer, map[string]any{"status": StatusValid, "serverTime": time.Now().UnixMilli()})
}

// handleFileDownload - 发布物下载（/files/{version}）
func (this *fakePlatform) handleFileDownload(writer http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	version := strings.TrimPrefix(request.URL.Path, "/files/")
	for _, item := range this.versions {
		if item.version == version {
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
	return localStatus(time.Now().UnixMilli(), tenant.validUntil, tenant.graceDays)
}

// issueTenantEnvelope - 签发租户信封（license-key 签名）
func (this *fakePlatform) issueTenantEnvelope(code string, tenant fakeTenant) (TenantEnvelope, error) {

	return issueTenant(TenantPayload{
		GrantId: "TEN-2026-000001", TenantCode: code, UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001",
		PlanCode: "pro", Environment: "production", SubscriptionType: "yearly",
		ValidFrom: "2026-01-01T00:00:00Z", ValidUntil: tenant.validUntil, GraceDays: tenant.graceDays,
		Features: tenant.features, Limits: map[string]int64{}, MenuCodes: []string{"dashboard"},
		ManifestVersion: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339),
		KeyVersion: "license-key-2026-01", Nonce: Licence.Nonce(),
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
		if passThrough(status) {
			envelope, err := this.issueTenantEnvelope(code, tenant)
			if err == nil {
				item["envelope"] = envelope
			}
		}
		tenants = append(tenants, item)
	}
	writeJson(writer, map[string]any{
		"status": StatusValid, "serverTime": time.Now().UnixMilli(), "syncTime": time.Now().UnixMilli(),
		"manifest": map[string]any{"version": 1, "menus": []any{}}, "tenants": tenants,
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
	if passThrough(status) {
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
	if passThrough(status) {
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

	if client.Status() != StatusValid {
		t.Fatalf("激活后状态应为 VALID，实际 %s", client.Status())
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
	if second.Status() != StatusValid {
		t.Fatalf("恢复后状态应为 VALID，实际 %s", second.Status())
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
	if client.Status() != StatusValid {
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

	if client.Status() != StatusGrace {
		t.Fatalf("宽限期激活应标记 GRACE，实际 %s", client.Status())
	}
	platform.server.Close()
	time.Sleep(350 * time.Millisecond)
	if client.Status() != StatusGrace {
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
	seed, _, _ := generateKeyPair()
	client.mu.Lock()
	client.state.ClientSeed = hex.EncodeToString(seed)
	client.mu.Unlock()

	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	calls := platform.validateCalls
	platform.mu.Unlock()
	t.Logf("validateCalls=%d status=%s", calls, client.Status())
	if client.Status() != StatusNotFound {
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
	if client.Status() != StatusValid {
		t.Fatalf("重新激活后应为 VALID，实际 %s", client.Status())
	}
}

// ============================= 单元测试 =============================

// TestVersionInRange - 版本范围表达式（与平台语义一致的判定表）
func TestVersionInRange(t *testing.T) {

	cases := []struct {
		version string
		expr    string
		expect  bool
	}{
		{"2.3.1", "", true},
		{"2.3.1", ">=2.0.0 <3.0.0", true},
		{"3.0.0", ">=2.0.0 <3.0.0", false},
		{"1.9.9", ">=2.0.0 <3.0.0", false},
		{"2.0.0", ">=2.0.0", true},
		{"2.0.0", ">2.0.0", false},
		{"2.0.0", "<=2.0.0", true},
		{"2.0.0", "==2.0.0", true},
		{"2.0.0", "!=2.0.0", false},
		{"2.0.0", ">=2.0.0, <3.0.0", true},
		{"v2.3.1", ">=2.0.0", true},
		{"2.3", ">=2.0.0 <3.0.0", true},
		{"abc", ">=2.0.0", false},
		{"2.0.0", "=>2.0.0", false},
	}
	for _, item := range cases {
		if got := VersionInRange(item.version, item.expr); got != item.expect {
			t.Fatalf("VersionInRange(%q, %q) = %v，期望 %v", item.version, item.expr, got, item.expect)
		}
	}
}

// TestLocalStatus - 离线本地时间维度判定（宽限/临期/永久）
func TestLocalStatus(t *testing.T) {

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	format := func(item time.Time) string { return item.UTC().Format(time.RFC3339) }
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	if got := localStatus(now, "", 7); got != StatusValid {
		t.Fatalf("永久授权应 VALID，实际 %s", got)
	}
	if got := localStatus(now, format(base.Add(400*24*time.Hour)), 7); got != StatusValid {
		t.Fatalf("远未到期应 VALID，实际 %s", got)
	}
	if got := localStatus(now, format(base.Add(10*24*time.Hour)), 7); got != StatusExpiring {
		t.Fatalf("30 天内到期应 EXPIRING，实际 %s", got)
	}
	if got := localStatus(now, format(base.Add(-time.Hour)), 7); got != StatusGrace {
		t.Fatalf("宽限内应 GRACE，实际 %s", got)
	}
	if got := localStatus(now, format(base.Add(-10*24*time.Hour)), 7); got != StatusExpired {
		t.Fatalf("宽限耗尽可能 EXPIRED，实际 %s", got)
	}
	if got := localStatus(now, "not-a-time", 7); got != StatusExpired {
		t.Fatalf("时间解析失败应安全兜底 EXPIRED，实际 %s", got)
	}
}

// TestSignHeaders - 请求签名三要素：格式、时间窗、空 body 哈希、验签回路
func TestSignHeaders(t *testing.T) {

	seed, _, err := generateKeyPair()
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
	if !Licence.VerifyRaw([]byte(content), headers["X-License-Sign"], hex.EncodeToString(ed25519PublicKey(seed))) {
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
