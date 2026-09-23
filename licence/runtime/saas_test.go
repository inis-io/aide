package runtime

import (
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// withTenant - 为假平台注入 SaaS 租户
func withTenant(platform *fakePlatform, code string, tenant fakeTenant) {

	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.tenants[code] = tenant
}

func TestFilterManifestMenus(t *testing.T) {
	menus := []ManifestMenu{
		{Code: "root", Type: "directory"},
		{Code: "page", ParentCode: "root", Type: "page"},
		{Code: "hidden", ParentCode: "root", Type: "page", Hidden: true},
	}
	raw, _ := json.Marshal(menus)
	got, err := FilterManifestMenus(&TenantManifest{Version: 2, Menus: raw}, []string{"root", "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ManifestMenu{menus[0], menus[2]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestTenantSync - 全量同步：放行租户携带已验签信封写入缓存，非放行租户只记录状态
func TestTenantSync(t *testing.T) {

	platform := newFakePlatform(t)
	withTenant(platform, "acme", fakeTenant{
		validUntil: "", graceDays: 7, features: map[string]bool{"report.advanced": true},
	})
	withTenant(platform, "blocked", fakeTenant{status: LicenceProtocol.StatusSuspended})

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	syncTime, manifests, err := client.TenantSync(t.Context(), 0)
	if err != nil {
		t.Fatalf("TenantSync 失败: %v", err)
	}
	if syncTime <= 0 || manifests == nil || manifests.Platform != nil || manifests.Tenant == nil || manifests.Tenant.Version != 1 {
		t.Fatalf("同步响应异常: %v %+v", syncTime, manifests)
	}

	// 放行租户：本地状态与功能权益生效
	if status := client.TenantStatus("acme"); status != LicenceProtocol.StatusValid {
		t.Fatalf("acme 应为 VALID，实际 %s", status)
	}
	if !client.TenantFeature("acme", "report.advanced") {
		t.Fatalf("acme 的 report.advanced 应为已授权")
	}
	// 非放行租户：无信封，状态透传
	if status := client.TenantStatus("blocked"); status != LicenceProtocol.StatusSuspended {
		t.Fatalf("blocked 应为 SUSPENDED，实际 %s", status)
	}
	if client.TenantFeature("blocked", "report.advanced") {
		t.Fatalf("blocked 不应有任何功能权益")
	}
	// 未知租户
	if status := client.TenantStatus("nobody"); status != "" {
		t.Fatalf("未知租户应返回空串，实际 %s", status)
	}
}

// TestTenantSearch - HTTP 传输按前三位搜索并只返回可登录租户。
func TestTenantSearch(t *testing.T) {

	platform := newFakePlatform(t)
	withTenant(platform, "mes-east", fakeTenant{})
	withTenant(platform, "mes-west", fakeTenant{status: LicenceProtocol.StatusSuspended})
	withTenant(platform, "other", fakeTenant{})

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()

	items, err := client.TenantSearch(t.Context(), "mes")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].TenantCode != "mes-east" || items[0].TenantName != "mes-east 租户" {
		t.Fatalf("搜索结果异常: %+v", items)
	}
	if _, err = client.TenantSearch(t.Context(), "me"); err == nil {
		t.Fatal("不足 3 位的前缀应被拒绝")
	}
}

// TestTenantValidate - 单租户实时校验：放行携带信封并刷新缓存
func TestTenantValidate(t *testing.T) {

	platform := newFakePlatform(t)
	withTenant(platform, "acme", fakeTenant{
		validUntil: time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339),
		graceDays:  7, features: map[string]bool{"ai.chat": true},
	})

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	status, err := client.TenantValidate(t.Context(), "acme", TenantValidateOptions{Feature: "ai.chat"})
	if err != nil || status != LicenceProtocol.StatusValid {
		t.Fatalf("TenantValidate 失败: %v %v", status, err)
	}
	if !client.TenantFeature("acme", "ai.chat") {
		t.Fatalf("校验后 ai.chat 应为已授权")
	}

	// 不存在的租户 → 错误
	if _, err = client.TenantValidate(t.Context(), "nobody", TenantValidateOptions{}); err == nil {
		t.Fatalf("未知租户必须报错")
	}
}

// TestTenantCurrent - 按需拉取租户当前信封
func TestTenantCurrent(t *testing.T) {

	platform := newFakePlatform(t)
	withTenant(platform, "acme", fakeTenant{
		validUntil: "", graceDays: 7, features: map[string]bool{"report.advanced": true},
	})

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	envelope, err := client.TenantCurrent(t.Context(), "acme")
	if err != nil {
		t.Fatalf("TenantCurrent 失败: %v", err)
	}
	if envelope.Payload.TenantCode != "acme" || envelope.Payload.GrantId != "TEN-2026-000001" {
		t.Fatalf("租户信封内容异常: %+v", envelope.Payload)
	}
	if envelope.Payload.KeyVersion != "license-key-2026-01" {
		t.Fatalf("租户信封密钥版本异常: %s", envelope.Payload.KeyVersion)
	}
}

// ============================= SaaS 菜单过滤与影响分析 =============================

// TestParseSaasMenuWrite - 许可证菜单写响应解析：成功/带影响报告/服务端故障/非放行态。
func TestParseSaasMenuWrite(t *testing.T) {
	client := &Client{}

	t.Run("success with impact report", func(t *testing.T) {
		raw := []byte(`{"status":"VALID","serverTime":1000,"id":5,"version":3,"impactReport":{"removedCodes":["a"],"affectedPlans":[],"affectedTenants":[]}}`)
		result, err := client.parseSaasMenuWrite(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Id != 5 || result.Version != 3 {
			t.Fatalf("id/version mismatch: %+v", result)
		}
		if result.ImpactReport == nil || len(result.ImpactReport.RemovedCodes) != 1 || result.ImpactReport.RemovedCodes[0] != "a" {
			t.Fatalf("impactReport mismatch: %+v", result.ImpactReport)
		}
	})

	t.Run("success without impact report", func(t *testing.T) {
		raw := []byte(`{"status":"VALID","serverTime":1000,"id":1,"version":2}`)
		result, err := client.parseSaasMenuWrite(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Id != 1 || result.Version != 2 {
			t.Fatalf("id/version mismatch: %+v", result)
		}
		if result.ImpactReport != nil {
			t.Fatalf("impactReport should be nil, got %+v", result.ImpactReport)
		}
	})

	t.Run("server error", func(t *testing.T) {
		raw := []byte(`{"status":"ERROR","serverTime":1000,"message":"清单已发布"}`)
		_, err := client.parseSaasMenuWrite(raw)
		if err == nil || !strings.Contains(err.Error(), "清单已发布") {
			t.Fatalf("expected server error with message, got %v", err)
		}
	})

	t.Run("non pass-through license", func(t *testing.T) {
		raw := []byte(`{"status":"EXPIRED","serverTime":1000}`)
		_, err := client.parseSaasMenuWrite(raw)
		if err == nil || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("expected non-pass-through error, got %v", err)
		}
	})
}

// ============================= 租户同步 =============================

func TestTenantPayloadV2Golden(t *testing.T) {
	payload := TenantPayload{
		GrantId: "TEN-1", TenantCode: "acme", UserId: "USR-1", ProjectId: "PRJ-1", PlanCode: "pro",
		Environment: "production", SubscriptionType: "official", ValidFrom: "2026-08-13T00:00:00Z",
		GraceDays: 7, VersionRange: ">=2.0.0", Features: map[string]bool{"report": true, "ai": false},
		Limits: map[string]int64{"storage": 1024, "users": 10}, MenuCodes: []string{"root", "root.report"},
		TenantManifestVersion: 3, IssuedAt: "2026-08-13T01:00:00Z", KeyVersion: "license-1", Nonce: "abc",
	}
	got, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"grantId":"TEN-1","tenantCode":"acme","userId":"USR-1","projectId":"PRJ-1","planCode":"pro","environment":"production","subscriptionType":"official","validFrom":"2026-08-13T00:00:00Z","validUntil":"","graceDays":7,"versionRange":"\u003e=2.0.0","features":{"ai":false,"report":true},"limits":{"storage":1024,"users":10},"menuCodes":["root","root.report"],"tenantManifestVersion":3,"issuedAt":"2026-08-13T01:00:00Z","keyVersion":"license-1","nonce":"abc"}`
	if string(got) != want {
		t.Fatalf("载荷字节不一致\ngot:  %s\nwant: %s", got, want)
	}
}

// TestTenantPayloadV2SignatureGolden - 固定种子签发租户载荷：签名 hex 固定 + 验签通过。
// 载荷与 licen-hub backend/app/common/sign/tenant_test.go 的 canonical 向量逐字节同源（跨仓库镜像）；
// 平台侧暂无同义签名向量，本向量由本仓库按同一 Ed25519-over-canonical-JSON 语义自建，
// 两侧 canonical 任一漂移会先在 TestTenantPayloadV2Golden 暴露并传导到本签名。
func TestTenantPayloadV2SignatureGolden(t *testing.T) {
	payload := TenantPayload{
		GrantId: "TEN-1", TenantCode: "acme", UserId: "USR-1", ProjectId: "PRJ-1", PlanCode: "pro",
		Environment: "production", SubscriptionType: "official", ValidFrom: "2026-08-13T00:00:00Z",
		GraceDays: 7, VersionRange: ">=2.0.0", Features: map[string]bool{"report": true, "ai": false},
		Limits: map[string]int64{"storage": 1024, "users": 10}, MenuCodes: []string{"root", "root.report"},
		TenantManifestVersion: 3, IssuedAt: "2026-08-13T01:00:00Z", KeyVersion: "license-1", Nonce: "abc",
	}
	seed, err := hex.DecodeString(goldenSeed)
	if err != nil {
		t.Fatalf("种子 hex 解码失败: %v", err)
	}
	envelope, err := issueTenant(payload, seed)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	const wantSignature = "cd010a0fedd529d7f9fb11ae6ad359304cbfa1491fe20af47d3cf8888bf9b09e442f67a3549a89579786ce929eb023cc38ab04810104ba903c9dde3785b8f909"
	if envelope.Signature != wantSignature {
		t.Fatalf("签名不一致\n期望: %s\n实际: %s", wantSignature, envelope.Signature)
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("canonical 序列化失败: %v", err)
	}
	if !LicenceProtocol.Licence.VerifySign(canonical, envelope.Signature, goldenPublicKey) {
		t.Fatalf("验签失败: %s", envelope.Signature)
	}
}
