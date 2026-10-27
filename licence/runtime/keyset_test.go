package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
)

// keysetTestPlatform - 双协议轮换假平台；仅使用测试生成的合成密钥。
type keysetTestPlatform struct {
	t                 *testing.T
	projectNo         string
	bootstrapSeed     []byte
	bootstrapPublic   string
	licenseSeeds      map[string][]byte
	licensePublic     map[string]string
	releaseSeeds      map[string][]byte
	releasePublic     map[string]string
	mu                sync.Mutex
	keysetRaw         json.RawMessage
	envelopeRaw       json.RawMessage
	keysetRevision    int64
	currentLicenseKey string
}

func newKeysetTestPlatform(t *testing.T) *keysetTestPlatform {
	t.Helper()
	platform := &keysetTestPlatform{
		t: t, projectNo: "PRJ-ZERO", licenseSeeds: map[string][]byte{}, licensePublic: map[string]string{},
		releaseSeeds: map[string][]byte{}, releasePublic: map[string]string{},
	}
	platform.bootstrapSeed, platform.bootstrapPublic = keysetTestKeyPair(t, "bootstrap-key-test")
	for _, version := range []string{"license-key-v1", "license-key-v2", "license-key-v3"} {
		platform.licenseSeeds[version], platform.licensePublic[version] = keysetTestKeyPair(t, version)
	}
	for _, version := range []string{"release-key-v1", "release-key-v2"} {
		platform.releaseSeeds[version], platform.releasePublic[version] = keysetTestKeyPair(t, version)
	}
	return platform
}

func keysetTestKeyPair(t *testing.T, version string) ([]byte, string) {
	t.Helper()
	seed, publicKey, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成 %s 测试密钥失败: %v", version, err)
	}
	return seed, hex.EncodeToString(publicKey)
}

// issue - 签发指定 revision 的 keyset 与业务信封；keyset 始终保留全部历史公钥。
func (this *keysetTestPlatform) issue(revision int64, licenseVersion string, tamperEnvelope bool) {
	this.t.Helper()
	licenseKeys := make([]LicenceProtocol.KeysetPublicKey, 0, len(this.licensePublic))
	releaseKeys := make([]LicenceProtocol.KeysetPublicKey, 0, len(this.releasePublic))
	for version, publicKey := range this.licensePublic {
		// 当前版本之后才生成的公钥不得提前进入历史集合。
		if version > licenseVersion {
			continue
		}
		licenseKeys = append(licenseKeys, LicenceProtocol.KeysetPublicKey{KeyVersion: version, PublicKey: publicKey})
	}
	for version, publicKey := range this.releasePublic {
		releaseKeys = append(releaseKeys, LicenceProtocol.KeysetPublicKey{KeyVersion: version, PublicKey: publicKey})
	}
	sort.Slice(licenseKeys, func(i, j int) bool { return licenseKeys[i].KeyVersion < licenseKeys[j].KeyVersion })
	sort.Slice(releaseKeys, func(i, j int) bool { return releaseKeys[i].KeyVersion < releaseKeys[j].KeyVersion })
	payload := LicenceProtocol.KeysetPayload{
		ProjectNo: this.projectNo, Revision: revision, IssuedAt: "2026-10-27T08:00:00Z",
		LicenseKeys: licenseKeys, ReleaseKeys: releaseKeys,
		KeyVersion: "bootstrap-key-test", Nonce: LicenceProtocol.RandomNonce(),
	}
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		this.t.Fatal(err)
	}
	signature, err := LicenceProtocol.SignPayload(payloadRaw, this.bootstrapSeed)
	if err != nil {
		this.t.Fatal(err)
	}
	keysetRaw, err := json.Marshal(LicenceProtocol.KeysetEnvelope{
		Version: LicenceProtocol.EnvelopeVersion, Algorithm: LicenceProtocol.Algorithm,
		Payload: payload, Signature: signature,
	})
	if err != nil {
		this.t.Fatal(err)
	}
	envelope, err := LicenceProtocol.Licence.Payload(LicenceProtocol.Payload{
		LicenseId: "LIC-ZERO-000001", ProjectId: this.projectNo, InstanceId: "INS-ZERO-000001",
		Environment: "production", GraceDays: 7, Entitlements: map[string]any{"chat": true},
		IssuedAt: "2026-10-27T08:00:00Z", KeyVersion: licenseVersion, Nonce: LicenceProtocol.RandomNonce(),
	}).Seed(this.licenseSeeds[licenseVersion]).Issue()
	if err != nil {
		this.t.Fatal(err)
	}
	if tamperEnvelope {
		envelope.Signature = "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	}
	envelopeRaw, err := json.Marshal(envelope)
	if err != nil {
		this.t.Fatal(err)
	}
	this.mu.Lock()
	this.keysetRaw = keysetRaw
	this.envelopeRaw = envelopeRaw
	this.keysetRevision = revision
	this.currentLicenseKey = licenseVersion
	this.mu.Unlock()
}

func (this *keysetTestPlatform) snapshot() (json.RawMessage, json.RawMessage, int64) {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append(json.RawMessage(nil), this.keysetRaw...), append(json.RawMessage(nil), this.envelopeRaw...), this.keysetRevision
}

func (this *keysetTestPlatform) roots() map[string]string {
	return map[string]string{"bootstrap-key-test": this.bootstrapPublic}
}

func (this *keysetTestPlatform) options(serverURL, dir string) Options {
	return Options{
		ServerURL: serverURL, ProjectNo: this.projectNo, LicenseNo: "LIC-ZERO-000001", Salt: "zero-salt",
		BootstrapPublicKeys: this.roots(), StorageDir: dir, Fingerprint: "zero-fingerprint",
		Version: "1.0.0", RefreshInterval: time.Hour, HTTPTimeout: 200 * time.Millisecond,
		DisableConsumptionReport: true, DisableCommands: true,
	}
}

func (this *keysetTestPlatform) writeRuntime(writer http.ResponseWriter) {
	keysetRaw, envelopeRaw, _ := this.snapshot()
	writeJson(writer, map[string]any{
		"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(),
		"keyset": keysetRaw, "envelope": envelopeRaw,
		"activationNo": "ACT-ZERO-000001", "activationToken": "zero-token", "expiresAt": time.Now().Add(time.Hour).UnixMilli(),
	})
}

func (this *keysetTestPlatform) httpServer() *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/licenses/activate", "/api/v1/licenses/validate", "/api/v1/licenses/current":
			this.writeRuntime(writer)
		case "/api/v1/updates/check":
			keysetRaw, _, _ := this.snapshot()
			writeJson(writer, map[string]any{"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(), "update": false, "keyset": keysetRaw})
		default:
			writeNotFound(writer)
		}
	}))
	this.t.Cleanup(server.Close)
	return server
}

// TestDynamicKeysetRotationUpdatesAllVerificationSnapshots - license/release 轮换后，
// 许可证、租户、平台配置、清单与发布物统一读取新快照，历史公钥仍可验证。
func TestDynamicKeysetRotationUpdatesAllVerificationSnapshots(t *testing.T) {
	platform := newKeysetTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	client, err := New(platform.options(server.URL, t.TempDir()))
	if err != nil {
		t.Fatalf("bootstrap-only New 应成功: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("动态首激活失败: %v", err)
	}
	platform.issue(2, "license-key-v2", false)
	if _, err = client.Current(t.Context()); err != nil {
		t.Fatalf("轮换后的 current 失败: %v", err)
	}
	keys := client.PublicKeys()
	if keys["license-key-v1"] != platform.licensePublic["license-key-v1"] || keys["license-key-v2"] != platform.licensePublic["license-key-v2"] {
		t.Fatalf("许可证快照未保留新旧公钥: %+v", keys)
	}
	tenant, err := issueTenant(TenantPayload{TenantCode: "TENANT", KeyVersion: "license-key-v2", Nonce: "n"}, platform.licenseSeeds["license-key-v2"])
	if err != nil {
		t.Fatal(err)
	}
	tenantRaw, _ := json.Marshal(tenant)
	if _, err = client.verifyTenantEnvelope(tenantRaw); err != nil {
		t.Fatalf("租户信封未使用轮换快照: %v", err)
	}
	configPayload := PlatformConfigPayload{ProjectId: platform.projectNo, SyncVersion: 2, KeyVersion: "license-key-v2", Nonce: "n"}
	configPayloadRaw, _ := json.Marshal(configPayload)
	configSignature, _ := LicenceProtocol.SignPayload(configPayloadRaw, platform.licenseSeeds["license-key-v2"])
	configRaw, _ := json.Marshal(PlatformConfigEnvelope{Version: 1, Algorithm: LicenceProtocol.Algorithm, Payload: configPayload, Signature: configSignature})
	if _, err = client.verifyPlatformConfigEnvelope(configRaw); err != nil {
		t.Fatalf("平台配置未使用轮换快照: %v", err)
	}
	for _, version := range []string{"release-key-v1", "release-key-v2"} {
		manifest, issueErr := issueManifest(ManifestPayload{Version: "2.0.0", KeyVersion: version, Nonce: "n"}, platform.releaseSeeds[version])
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		manifestRaw, _ := json.Marshal(manifest)
		if _, err = client.VerifyManifest(manifestRaw); err != nil {
			t.Fatalf("历史/新 release 公钥均应验证清单 %s: %v", version, err)
		}
	}
	artifactPayload, _ := json.Marshal(ArtifactPayload{ArtifactNo: "ART-ZERO", Version: "2.0.0", Sha256: "abc"})
	artifactSignature, _ := LicenceProtocol.SignPayload(artifactPayload, platform.releaseSeeds["release-key-v2"])
	if !client.verifyArtifact(ManifestArtifact{ArtifactNo: "ART-ZERO", KeyVersion: "release-key-v2", Sha256: "abc", Signature: artifactSignature}, "2.0.0") {
		t.Fatal("发布物未使用轮换后的 release 快照")
	}
}

// TestDynamicKeysetRejectsAtomicFailures - 业务信封篡改、低 revision、同 revision 不同内容
// 均不得提交候选 keyset 或覆盖旧可信信封。
func TestDynamicKeysetRejectsAtomicFailures(t *testing.T) {
	platform := newKeysetTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	client, err := New(platform.options(server.URL, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	trustedEnvelope, _ := client.Envelope()
	platform.issue(2, "license-key-v2", true)
	if _, err = client.Current(t.Context()); err == nil {
		t.Fatal("业务信封篡改必须拒绝")
	}
	if _, exists := client.PublicKeys()["license-key-v2"]; exists {
		t.Fatal("业务信封失败时不得提交候选公钥")
	}
	platform.issue(1, "license-key-v2", false)
	if _, err = client.Current(t.Context()); err == nil {
		t.Fatal("同 revision 不同 keyset 内容必须拒绝")
	}
	platform.issue(0, "license-key-v2", false)
	if _, err = client.Current(t.Context()); err == nil {
		t.Fatal("低 revision 必须拒绝")
	}
	currentEnvelope, _ := client.Envelope()
	if currentEnvelope.Signature != trustedEnvelope.Signature {
		t.Fatal("失败响应不得覆盖旧可信信封")
	}
	client.mu.RLock()
	revision := client.keyset.Payload.Revision
	client.mu.RUnlock()
	if revision != 1 {
		t.Fatalf("失败响应不得推进可信 revision: %d", revision)
	}
}

// TestDynamicKeysetRejectsChangedHistoricalKey - 更高 revision 也不能给既有 keyVersion
// 分配不同公钥；冲突时必须保留完整旧可信快照。
func TestDynamicKeysetRejectsChangedHistoricalKey(t *testing.T) {
	platform := newKeysetTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	client, err := New(platform.options(server.URL, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	trustedPublicKey := client.PublicKeys()["license-key-v1"]
	platform.licenseSeeds["license-key-v1"], platform.licensePublic["license-key-v1"] = keysetTestKeyPair(t, "conflicting-license-key-v1")
	platform.issue(2, "license-key-v2", false)
	if _, err = client.Current(t.Context()); err == nil {
		t.Fatal("同 keyVersion 不同公钥必须拒绝，即使 revision 更高")
	}
	if got := client.PublicKeys()["license-key-v1"]; got != trustedPublicKey {
		t.Fatal("冲突响应不得覆盖旧公钥")
	}
	if _, exists := client.PublicKeys()["license-key-v2"]; exists {
		t.Fatal("冲突响应不得提交任何候选公钥")
	}
}

// TestDynamicKeysetOfflineRestore - 已验证 keyset 与 revision 加密持久化，断网重启先验根再验缓存。
func TestDynamicKeysetOfflineRestore(t *testing.T) {
	platform := newKeysetTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	dir := t.TempDir()
	options := platform.options(server.URL, dir)
	first, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	platform.issue(2, "license-key-v2", false)
	if _, err = first.Current(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	server.Close()
	second, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err = second.Start(t.Context()); err != nil {
		t.Fatalf("断网重启应使用可信缓存: %v", err)
	}
	if second.PublicKeys()["license-key-v2"] != platform.licensePublic["license-key-v2"] {
		t.Fatal("重启未恢复动态工作公钥")
	}
	second.mu.RLock()
	revision := second.keyset.Payload.Revision
	second.mu.RUnlock()
	if revision != 2 {
		t.Fatalf("重启未恢复 keyset revision: %d", revision)
	}
}

// keysetGRPCLicenseServer - gRPC 许可证响应原始 keyset_json 假平台。
type keysetGRPCLicenseServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	platform *keysetTestPlatform
}

func (this *keysetGRPCLicenseServer) response() *licencev1.RuntimeResponse {
	keysetRaw, envelopeRaw, _ := this.platform.snapshot()
	return &licencev1.RuntimeResponse{
		Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(),
		KeysetJson: keysetRaw, EnvelopeJson: envelopeRaw,
		ActivationNo: "ACT-ZERO-GRPC", ActivationToken: "zero-grpc-token", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
	}
}
func (this *keysetGRPCLicenseServer) Activate(context.Context, *licencev1.ActivateRequest) (*licencev1.RuntimeResponse, error) {
	return this.response(), nil
}
func (this *keysetGRPCLicenseServer) Validate(context.Context, *licencev1.ValidateRequest) (*licencev1.RuntimeResponse, error) {
	return this.response(), nil
}
func (this *keysetGRPCLicenseServer) Current(context.Context, *licencev1.CurrentLicenseRequest) (*licencev1.RuntimeResponse, error) {
	return this.response(), nil
}

type keysetGRPCUpdateServer struct {
	licencev1.UnimplementedUpdateRuntimeServiceServer
	platform *keysetTestPlatform
}

func (this *keysetGRPCUpdateServer) Check(context.Context, *licencev1.UpdateCheckRequest) (*licencev1.UpdateCheckResponse, error) {
	keysetRaw, _, _ := this.platform.snapshot()
	return &licencev1.UpdateCheckResponse{Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(), Update: false, KeysetJson: keysetRaw}, nil
}

// TestDynamicKeysetGRPC - gRPC 与 HTTP 共用原始 JSON 验签语义，无更新响应也提交已验证 keyset。
func TestDynamicKeysetGRPC(t *testing.T) {
	platform := newKeysetTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	licencev1.RegisterLicenseRuntimeServiceServer(server, &keysetGRPCLicenseServer{platform: platform})
	licencev1.RegisterUpdateRuntimeServiceServer(server, &keysetGRPCUpdateServer{platform: platform})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	options := platform.options("grpc://"+listener.Addr().String(), t.TempDir())
	options.Transport = TransportGRPC
	options.GRPC = GRPCOptions{AllowInsecure: true}
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("gRPC 动态激活失败: %v", err)
	}
	platform.issue(2, "license-key-v2", false)
	info, err := client.CheckUpdate(t.Context(), "windows/amd64")
	if err != nil || info.Available {
		t.Fatalf("gRPC 无更新响应异常: %+v %v", info, err)
	}
	if client.PublicKeys()["license-key-v2"] != platform.licensePublic["license-key-v2"] {
		t.Fatal("gRPC 无更新响应未提交新 keyset")
	}
}
