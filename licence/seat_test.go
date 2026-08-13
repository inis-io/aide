package licence

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================= SEAT_RELEASED 全链路语义 =============================

// seedDerivedCaches - 向假平台注入租户/项目配置/平台配置，并同步进客户端派生缓存
// （供 SEAT_RELEASED 清理范围断言：三条派生缓存都必须被清空）
func seedDerivedCaches(t *testing.T, platform *fakePlatform, client *Client) {

	t.Helper()
	platform.mu.Lock()
	platform.tenants["mall"] = fakeTenant{validUntil: "", graceDays: 7, features: map[string]bool{"order": true}}
	platform.configs["app.theme"] = ConfigItem{
		ConfigKey: "app.theme", Name: "主题", Content: json.RawMessage(`{"color":"blue"}`), Version: 1,
	}
	platform.configSyncVersion = 1
	platform.platformConfigs["site.title"] = PlatformConfigItem{
		Key: "site.title", Label: "站点标题", Type: "input", Value: "演示站", Version: 1,
	}
	platform.platformConfigSyncVersion = 1
	platform.mu.Unlock()

	if _, _, err := client.TenantSync(t.Context(), 0); err != nil {
		t.Fatalf("租户同步失败: %v", err)
	}
	if _, err := client.ConfigSync(t.Context()); err != nil {
		t.Fatalf("项目配置同步失败: %v", err)
	}
	if _, err := client.PlatformConfigSync(t.Context()); err != nil {
		t.Fatalf("平台配置同步失败: %v", err)
	}
	if _, ok := client.Config("app.theme"); !ok {
		t.Fatalf("项目配置未进缓存")
	}
	if _, ok := client.PlatformConfig("site.title"); !ok {
		t.Fatalf("平台配置未进缓存")
	}
	if client.TenantStatus("mall") != StatusValid {
		t.Fatalf("租户缓存未生效: %s", client.TenantStatus("mall"))
	}
}

// assertSeatReleasedState - SEAT_RELEASED 终态断言：
// 保留 token/客户端私钥/席位号（红线），清除信封与全部派生缓存
func assertSeatReleasedState(t *testing.T, client *Client, dir string) {

	t.Helper()
	if client.Status() != StatusSeatReleased {
		t.Fatalf("状态应为 SEAT_RELEASED，实际 %s", client.Status())
	}
	client.mu.RLock()
	token, seed, seatNo, activationNo := client.state.ActivationToken, client.state.ClientSeed, client.state.SeatNo, client.state.ActivationNo
	configVersion, platformVersion := client.state.ConfigSyncVersion, client.state.PlatformConfigSyncVersion
	client.mu.RUnlock()
	if token == "" || seed == "" {
		t.Fatalf("SEAT_RELEASED 必须保留 token 与客户端私钥")
	}
	if seatNo != "SEAT-2026-000001" || activationNo != "ACT-2026-000001" {
		t.Fatalf("SEAT_RELEASED 必须保留席位号/激活号: %s %s", seatNo, activationNo)
	}
	if _, exist := client.Envelope(); exist {
		t.Fatalf("SEAT_RELEASED 必须清除缓存信封")
	}
	if _, ok := client.Config("app.theme"); ok || configVersion != 0 {
		t.Fatalf("项目配置快照与水位未清空: %v", configVersion)
	}
	if _, ok := client.PlatformConfig("site.title"); ok || platformVersion != 0 {
		t.Fatalf("平台配置快照与水位未清空: %v", platformVersion)
	}
	if client.TenantStatus("mall") != "" {
		t.Fatalf("租户信封缓存未清空: %s", client.TenantStatus("mall"))
	}
	// SEAT_RELEASED 状态必须落盘（无信封也保留，供重启恢复）
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("SEAT_RELEASED 状态文件应保留: %v %v", entries, err)
	}
}

// forceSeatReleased - 强制假平台运行面进入 SEAT_RELEASED
func forceSeatReleased(platform *fakePlatform) {

	platform.mu.Lock()
	platform.forceStatus = StatusSeatReleased
	platform.mu.Unlock()
}

// TestSeatReleasedValidateKeepsCredentials - validate 收到 SEAT_RELEASED：
// 保留 token/私钥/席位号与状态文件，清除信封与派生缓存，上报 OnStatusChange，
// 后台循环停摆且不触发任何自动 Reactivate
func TestSeatReleasedValidateKeepsCredentials(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	var logMu sync.Mutex
	var statusLog []string
	options := testOptions(platform, dir)
	options.OnStatusChange = func(oldStatus string, newStatus string) {
		logMu.Lock()
		statusLog = append(statusLog, oldStatus+"->"+newStatus)
		logMu.Unlock()
	}
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()
	seedDerivedCaches(t, platform, client)

	forceSeatReleased(platform)
	time.Sleep(350 * time.Millisecond) // 等待后台循环跑一轮 validate

	assertSeatReleasedState(t, client, dir)
	logMu.Lock()
	defer logMu.Unlock()
	released := false
	for _, item := range statusLog {
		if strings.HasSuffix(item, "->"+StatusSeatReleased) {
			released = true
		}
	}
	if !released {
		t.Fatalf("OnStatusChange 未上报 SEAT_RELEASED: %v", statusLog)
	}

	// 终态稳定：后台循环不得再发请求，更不得自动重激活
	platform.mu.Lock()
	activateCalls, validateCalls := platform.activateCalls, platform.validateCalls
	platform.mu.Unlock()
	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	activateAfter, validateAfter := platform.activateCalls, platform.validateCalls
	platform.mu.Unlock()
	if activateCalls != 1 || activateAfter != 1 {
		t.Fatalf("SEAT_RELEASED 不得自动重激活: before=%d after=%d", activateCalls, activateAfter)
	}
	if validateAfter != validateCalls {
		t.Fatalf("SEAT_RELEASED 终态后台循环应停摆: before=%d after=%d", validateCalls, validateAfter)
	}
}

// TestSeatReleasedCurrentSameSemantics - Current 路径与 validate 清理范围一致：
// 保留凭证、清除信封与派生缓存、返回统一错误文本
func TestSeatReleasedCurrentSameSemantics(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	client.Stop() // 停掉后台循环，保证只有 Current 一条路径触发 SEAT_RELEASED
	seedDerivedCaches(t, platform, client)

	forceSeatReleased(platform)
	if _, err = client.Current(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "席位已被释放") {
		t.Fatalf("Current 应返回席位释放错误，实际: %v", err)
	}
	assertSeatReleasedState(t, client, dir)
	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()
	if activateCalls != 1 {
		t.Fatalf("Current 路径不得自动重激活: %d", activateCalls)
	}
}

// TestSeatReleasedRestoreStable - 进程重启 restore 后稳定停在 SEAT_RELEASED：
// 无信封状态不被清理、不被本地判定覆盖、后台循环不自动重激活
func TestSeatReleasedRestoreStable(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	first, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = first.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	forceSeatReleased(platform)
	time.Sleep(350 * time.Millisecond)
	if first.Status() != StatusSeatReleased {
		t.Fatalf("预期 SEAT_RELEASED，实际 %s", first.Status())
	}
	first.Stop()

	second, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("重启 New 失败: %v", err)
	}
	if err = second.Start(t.Context()); err != nil {
		t.Fatalf("SEAT_RELEASED 状态应可恢复启动，实际: %v", err)
	}
	defer second.Stop()
	if second.Status() != StatusSeatReleased {
		t.Fatalf("重启后应稳定停在 SEAT_RELEASED，实际 %s", second.Status())
	}
	if second.SeatNo() != "SEAT-2026-000001" {
		t.Fatalf("重启后席位号应保留: %s", second.SeatNo())
	}
	time.Sleep(350 * time.Millisecond)
	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()
	if activateCalls != 1 {
		t.Fatalf("重启后不得自动重激活: %d", activateCalls)
	}
}

// TestSeatReleasedUpdateChannelFailsClosed - updates 通道收到 SEAT_RELEASED：
// fail-closed 返回错误、不自动重激活、不回写 client 授权状态
// （授权状态收敛以 validate 循环为准，最长一个刷新周期）
func TestSeatReleasedUpdateChannelFailsClosed(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes-v2.4.0"))
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	client.Stop() // 停掉后台循环，只走 CheckUpdate 通道

	forceSeatReleased(platform)
	info, err := client.CheckUpdate(t.Context(), "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), StatusSeatReleased) {
		t.Fatalf("SEAT_RELEASED 应 fail-closed 返回错误，实际: %v", err)
	}
	if info.Available || info.Manifest != nil {
		t.Fatalf("SEAT_RELEASED 不得下发更新清单: %+v", info)
	}
	if client.Status() != StatusValid {
		t.Fatalf("updates 通道不得回写授权状态: %s", client.Status())
	}
	platform.mu.Lock()
	activateCalls := platform.activateCalls
	platform.mu.Unlock()
	if activateCalls != 1 {
		t.Fatalf("updates 通道不得自动重激活: %d", activateCalls)
	}
}
