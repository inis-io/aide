package runtime

import (
	"encoding/json"
	"strings"
	"testing"
)

// clearTrackingStore - 记录陈旧缓存清理次数的测试安全存储。
type clearTrackingStore struct {
	Store
	clearCalls int
}

func (this *clearTrackingStore) Clear() error {
	this.clearCalls++
	return this.Store.Clear()
}

// seedLegacyStateWithoutKeyset - 用静态公钥模式生成含有效信封/token、但无 keyset 的旧状态。
func seedLegacyStateWithoutKeyset(t *testing.T, platform *autoTestPlatform, serverURL, dir string) {
	t.Helper()
	options := platform.options(serverURL, dir)
	options.PublicKeys = map[string]string{"license-key-v1": platform.licensePublic["license-key-v1"]}
	options.BootstrapPublicKeys = nil
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("静态模式制备旧状态失败: %v", err)
	}
	raw, err := client.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var state runtimeState
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.ActivationToken == "" || len(state.Envelope) == 0 || len(state.Keyset) != 0 {
		t.Fatalf("旧状态夹具不符合要求: token=%q envelope=%d keyset=%d", state.ActivationToken, len(state.Envelope), len(state.Keyset))
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
}

// newLegacyBootstrapClient - 以 bootstrap 模式打开同一旧状态，并跟踪 store.Clear。
func newLegacyBootstrapClient(t *testing.T, platform *autoTestPlatform, serverURL, dir string) (*Client, *clearTrackingStore) {
	t.Helper()
	options := platform.options(serverURL, dir)
	store, err := newFileStore(dir, options.LicenseNo, options.Salt, options.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	tracked := &clearTrackingStore{Store: store}
	options.Store = tracked
	client, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, tracked
}

// TestBootstrapStartReplacesLegacyStateWithoutKeyset - 动态模式遇到无 keyset 旧状态时，
// 必须清掉陈旧缓存并重新 activate，不得硬失败或误用旧 token。
func TestBootstrapStartReplacesLegacyStateWithoutKeyset(t *testing.T) {
	platform := newAutoTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	dir := t.TempDir()
	seedLegacyStateWithoutKeyset(t, platform, server.URL, dir)
	_, _, activationsBefore, _, _ := platform.counts()
	client, store := newLegacyBootstrapClient(t, platform, server.URL, dir)

	if err := client.Start(t.Context()); err != nil {
		t.Fatalf("无 keyset 旧状态应清理后重新激活，实际: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, activationsAfter, _, _ := platform.counts()
	if activationsAfter != activationsBefore+1 {
		t.Fatalf("旧 token 不应被复用，应重新 activate: before=%d after=%d", activationsBefore, activationsAfter)
	}
	if store.clearCalls != 1 {
		t.Fatalf("陈旧状态应清理一次，实际 %d", store.clearCalls)
	}
	raw, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var state runtimeState
	if err = json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Keyset) == 0 || state.ActivationToken == "" {
		t.Fatalf("重新激活后应保存新 keyset 与凭证: %+v", state)
	}
}

// TestBootstrapStartLegacyStateWithoutKeysetReturnsActivationError - 清理陈旧状态后若网络激活失败，
// Start 必须返回真实网络错误，不得保留旧缓存造成激活成功假象。
func TestBootstrapStartLegacyStateWithoutKeysetReturnsActivationError(t *testing.T) {
	platform := newAutoTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	dir := t.TempDir()
	seedLegacyStateWithoutKeyset(t, platform, server.URL, dir)
	_, _, activationsBefore, _, _ := platform.counts()
	server.Close()
	client, store := newLegacyBootstrapClient(t, platform, server.URL, dir)

	err := client.Start(t.Context())
	if err == nil {
		t.Fatal("清理陈旧状态后网络激活失败，Start 不得返回成功")
	}
	if strings.Contains(err.Error(), "响应缺少 keyset") {
		t.Fatalf("应进入重新激活并返回网络错误，而不是缓存 keyset 错误: %v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, activationsAfter, _, _ := platform.counts()
	if activationsAfter != activationsBefore {
		t.Fatalf("断网后不应出现成功激活: before=%d after=%d", activationsBefore, activationsAfter)
	}
	if store.clearCalls != 1 {
		t.Fatalf("网络激活前应先清理一次陈旧状态，实际 %d", store.clearCalls)
	}
	raw, loadErr := store.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if raw != nil {
		t.Fatal("激活失败后不得恢复或保留旧凭证状态")
	}
}

// TestBootstrapStartPreservesLegacySeatReleasedWithoutKeyset - 静态模式落盘的席位释放终态
// 即使缺 keyset 也必须原样恢复，动态模式不得清缓存或自动重激活。
func TestBootstrapStartPreservesLegacySeatReleasedWithoutKeyset(t *testing.T) {
	platform := newAutoTestPlatform(t)
	platform.issue(1, "license-key-v1", false)
	server := platform.httpServer()
	dir := t.TempDir()
	options := platform.options(server.URL, dir)
	options.PublicKeys = map[string]string{"license-key-v1": platform.licensePublic["license-key-v1"]}
	options.BootstrapPublicKeys = nil
	staticClient, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	staticClient.opMu.Lock()
	if err = staticClient.activateLocked(t.Context()); err != nil {
		staticClient.opMu.Unlock()
		t.Fatalf("静态模式制备激活状态失败: %v", err)
	}
	staticClient.opMu.Unlock()
	staticClient.mu.Lock()
	staticClient.state.SeatNo = "SEAT-LEGACY-001"
	staticClient.mu.Unlock()
	if err = staticClient.seatReleased(); err == nil {
		t.Fatal("seatReleased 应返回既有终态提示")
	}
	rawBefore, err := staticClient.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var released runtimeState
	if err = json.Unmarshal(rawBefore, &released); err != nil {
		t.Fatal(err)
	}
	if released.Status != "SEAT_RELEASED" || (len(released.Envelope) != 0 && string(released.Envelope) != "null") || len(released.Keyset) != 0 ||
		released.ActivationToken == "" || released.ClientSeed == "" || released.SeatNo == "" || released.ActivationNo == "" {
		t.Fatalf("席位释放夹具不符合要求: %+v", released)
	}
	if err = staticClient.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, activationsBefore, _, _ := platform.counts()
	server.Close()
	client, store := newLegacyBootstrapClient(t, platform, server.URL, dir)

	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("SEAT_RELEASED 终态应离线恢复且不得自动激活，实际: %v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatal(err)
	}
	if store.clearCalls != 0 {
		t.Fatalf("SEAT_RELEASED 状态不得 store.Clear，实际 %d 次", store.clearCalls)
	}
	_, _, activationsAfter, _, _ := platform.counts()
	if activationsAfter != activationsBefore {
		t.Fatalf("SEAT_RELEASED 不得自动 activate/Reactivate: before=%d after=%d", activationsBefore, activationsAfter)
	}
	if client.Status() != "SEAT_RELEASED" {
		t.Fatalf("恢复后终态被改变: %s", client.Status())
	}
	rawAfter, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if string(rawAfter) != string(rawBefore) {
		t.Fatal("SEAT_RELEASED 状态文件必须逐字保留 token/ClientSeed/SeatNo/ActivationNo 与状态")
	}
}
