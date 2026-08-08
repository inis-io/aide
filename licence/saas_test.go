package licence

import (
	"testing"
	"time"
)

// withTenant - 为假平台注入 SaaS 租户
func withTenant(platform *fakePlatform, code string, tenant fakeTenant) {

	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.tenants[code] = tenant
}

// TestTenantSync - 全量同步：放行租户携带已验签信封写入缓存，非放行租户只记录状态
func TestTenantSync(t *testing.T) {

	platform := newFakePlatform(t)
	withTenant(platform, "acme", fakeTenant{
		validUntil: "", graceDays: 7, features: map[string]bool{"report.advanced": true},
	})
	withTenant(platform, "blocked", fakeTenant{status: StatusSuspended})

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	syncTime, manifest, err := client.TenantSync(t.Context(), 0)
	if err != nil {
		t.Fatalf("TenantSync 失败: %v", err)
	}
	if syncTime <= 0 || manifest == nil || manifest.Version != 1 {
		t.Fatalf("同步响应异常: %v %+v", syncTime, manifest)
	}

	// 放行租户：本地状态与功能权益生效
	if status := client.TenantStatus("acme"); status != StatusValid {
		t.Fatalf("acme 应为 VALID，实际 %s", status)
	}
	if !client.TenantFeature("acme", "report.advanced") {
		t.Fatalf("acme 的 report.advanced 应为已授权")
	}
	// 非放行租户：无信封，状态透传
	if status := client.TenantStatus("blocked"); status != StatusSuspended {
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
	if err != nil || status != StatusValid {
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
