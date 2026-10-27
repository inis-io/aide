package runtime

import (
	"fmt"
	"testing"
)

// TestProvisionByPlanWithoutTokenFingerprintChallenge - sdk_auto 只凭 projectNo + installSN 申领；
// 首次 FINGERPRINT_REQUIRED 不发证，SDK 用挑战盐计算与激活一致的指纹并仅重试一次。
func TestProvisionByPlanWithoutTokenFingerprintChallenge(t *testing.T) {
	calls := 0
	challengeSalt := "challenge-salt"
	server := newFakeProvisionServer(t, func(body map[string]any) (string, map[string]any) {
		calls++
		if _, exists := body["provisionToken"]; exists {
			t.Fatalf("申领请求不得携带 provisionToken: %v", body)
		}
		if body["installSN"] != "zero-install-sn" {
			t.Fatalf("挑战重试必须复用 installSN: %v", body)
		}
		if calls == 1 {
			if body["fingerprintHash"] != nil {
				t.Fatalf("首次缺指纹挑战不应伪造指纹: %v", body)
			}
			return "FINGERPRINT_REQUIRED", map[string]any{"salt": challengeSalt, "message": "需要指纹"}
		}
		expected, err := FingerprintHash(challengeSalt, "", func() (string, error) { return "stable-machine", nil })
		if err != nil {
			t.Fatal(err)
		}
		if body["fingerprintHash"] != expected {
			t.Fatalf("挑战指纹与激活算法不一致: got %v want %s", body["fingerprintHash"], expected)
		}
		result := okProvisionResponse()
		result["salt"] = challengeSalt
		return "OK", result
	})
	result, err := ProvisionByPlan(t.Context(), ProvisionOptions{
		ServerURL: server.server.URL, ProjectNo: "PRJ-ZERO", StorageDir: t.TempDir(), InstallSN: "zero-install-sn",
		Provider: func() (string, error) { return "stable-machine", nil },
	})
	if err != nil {
		t.Fatalf("无 token 申领与指纹挑战应成功: %v", err)
	}
	if calls != 2 {
		t.Fatalf("指纹挑战必须只重试一次，实际 %d 次", calls)
	}
	if result.Salt != challengeSalt || result.LicenseNo != "LIC-2026-000888" {
		t.Fatalf("申领结果异常: %+v", result)
	}
}

// TestProvisionByPlanFingerprintChallengeOnlyOnce - 第二次仍要求指纹时返回业务拒绝，禁止循环申领。
func TestProvisionByPlanFingerprintChallengeOnlyOnce(t *testing.T) {
	calls := 0
	server := newFakeProvisionServer(t, func(map[string]any) (string, map[string]any) {
		calls++
		return "FINGERPRINT_REQUIRED", map[string]any{"salt": fmt.Sprintf("salt-%d", calls)}
	})
	_, err := ProvisionByPlan(t.Context(), ProvisionOptions{
		ServerURL: server.server.URL, ProjectNo: "PRJ-ZERO", StorageDir: t.TempDir(), InstallSN: "zero-install-sn",
		Provider: func() (string, error) { return "stable-machine", nil },
	})
	provisionErr, ok := err.(*ProvisionError)
	if !ok || provisionErr.Status != "FINGERPRINT_REQUIRED" {
		t.Fatalf("第二次挑战应原样返回 ProvisionError: %T %v", err, err)
	}
	if calls != 2 {
		t.Fatalf("挑战最多请求两次，实际 %d 次", calls)
	}
}
