package licence

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

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
	if !Licence.VerifySign(canonical, envelope.Signature, goldenPublicKey) {
		t.Fatalf("验签失败: %s", envelope.Signature)
	}
}
