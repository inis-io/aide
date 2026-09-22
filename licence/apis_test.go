package licence

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/inis-io/aide/licence/apis"
)

// TestApisMountedOnNew - Apis 挂载点随 New 构造（骨架阶段：仅断言挂载存在）
func TestApisMountedOnNew(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if client.Apis == nil {
		t.Fatalf("New 应构造 Apis 挂载点")
	}
}

// TestApisDoerGateNotActivated - 未激活闸门：无 activation token 或授权状态非放行态
// （passThrough 口径，与 TenantSync/PullEvents 的 fail-closed 惯例一致）直接返回
// apis.ErrNotActivated，且不产生任何网络请求
func TestApisDoerGateNotActivated(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if _, err = (apisDoer{client: client}).Do(t.Context(), "POST", "/api/v1/anything", nil); !errors.Is(err, apis.ErrNotActivated) {
		t.Fatalf("无 token 应返回 ErrNotActivated，实际 %v", err)
	}

	// 已持 token 但状态为非放行态同样闸住（不发请求）：
	// 覆盖 EXPIRED/REVOKED/SUSPENDED 与 SEAT_RELEASED 等非自动恢复终态
	clientSeed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	client.mu.Unlock()
	for _, status := range []string{StatusExpired, StatusRevoked, StatusSuspended, StatusSeatReleased, StatusSeatLimitExceeded, StatusInstanceMismatch} {
		client.mu.Lock()
		client.state.Status = status
		client.mu.Unlock()
		if _, err = (apisDoer{client: client}).Do(t.Context(), "POST", "/api/v1/anything", nil); !errors.Is(err, apis.ErrNotActivated) {
			t.Fatalf("状态 %s 应返回 ErrNotActivated，实际 %v", status, err)
		}
	}

	// 放行状态（VALID/EXPIRING/GRACE/CLOCK_TAMPERED）不被闸门拦截：
	// 请求越过闸门走完签名管线抵达假平台（未知路径回业务错误），绝不返回 ErrNotActivated
	for _, status := range []string{StatusValid, StatusExpiring, StatusGrace, StatusClockTampered} {
		client.mu.Lock()
		client.state.Status = status
		client.mu.Unlock()
		if _, err = (apisDoer{client: client}).Do(t.Context(), "POST", "/api/v1/anything", nil); errors.Is(err, apis.ErrNotActivated) {
			t.Fatalf("放行状态 %s 不应返回 ErrNotActivated", status)
		}
	}
}

// TestApisDoerPassThroughAfterActivation - 激活后 Do 经统一请求出口携带签名头放行
// （借假平台 validate 端点回环验证端到端签名链路）
func TestApisDoerPassThroughAfterActivation(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	raw, err := (apisDoer{client: client}).Do(t.Context(), "POST", "/api/v1/licenses/validate",
		[]byte(`{"licenseNo":"LIC-2026-000123"}`))
	if err != nil {
		t.Fatalf("激活后 Do 应放行: %v", err)
	}
	if !strings.Contains(string(raw), StatusValid) {
		t.Fatalf("放行响应应含 VALID 状态，实际 %s", string(raw))
	}
}
