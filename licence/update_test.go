package licence

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// withUpdate - 为假平台注入一个已发布版本（含发布物）
func withUpdate(platform *fakePlatform, version string, data []byte) {

	platform.mu.Lock()
	defer platform.mu.Unlock()
	platform.versions = append(platform.versions, fakeVersion{
		version: version, buildNumber: "20260808.1", sourceRange: ">=2.0.0",
		releasedAt: time.Now().Add(-time.Hour).UnixMilli(), artifactData: data,
	})
}

// TestCheckUpdateAvailable - 有更高版本时返回已验签清单（含发布物签名复核）
func TestCheckUpdateAvailable(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes-v2.4.0"))

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	info, err := client.CheckUpdate(t.Context(), "linux/amd64")
	if err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	if !info.Available || info.Manifest == nil {
		t.Fatalf("应有可用更新: %+v", info)
	}
	if info.Manifest.Payload.Version != "2.4.0" || info.Manifest.Payload.KeyVersion != "release-key-2026-01" {
		t.Fatalf("清单内容异常: %+v", info.Manifest.Payload)
	}
	if len(info.Manifest.Payload.Artifacts) != 1 {
		t.Fatalf("清单发布物数量异常: %+v", info.Manifest.Payload.Artifacts)
	}
}

// TestCheckUpdateMultiArch - 版本声明多架构（osArch 数组）：客户端上报其一即命中，
// 未声明的架构不命中（多架构 contains 匹配语义，镜像平台 selectVersion）。
func TestCheckUpdateMultiArch(t *testing.T) {
	platform := newFakePlatform(t)
	platform.mu.Lock()
	platform.versions = append(platform.versions, fakeVersion{
		version: "2.4.0", buildNumber: "20260808.1", sourceRange: ">=2.0.0",
		osArch: []string{"linux/amd64", "linux/arm64"},
		releasedAt: time.Now().Add(-time.Hour).UnixMilli(), artifactData: []byte("fake-multi-arch"),
	})
	platform.mu.Unlock()

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	// 命中：版本声明包含客户端上报架构
	info, err := client.CheckUpdate(t.Context(), "linux/arm64")
	if err != nil {
		t.Fatalf("CheckUpdate(arm64) 失败: %v", err)
	}
	if !info.Available || info.Manifest == nil || info.Manifest.Payload.Version != "2.4.0" {
		t.Fatalf("arm64 应命中 2.4.0: %+v", info.Manifest)
	}

	// 未命中：版本未声明该架构
	miss, err := client.CheckUpdate(t.Context(), "windows/amd64")
	if err != nil {
		t.Fatalf("CheckUpdate(windows) 失败: %v", err)
	}
	if miss.Available {
		t.Fatalf("windows/amd64 未声明应不命中: %+v", miss.Manifest)
	}
}

// TestNewUpdaterDefaultOSArch - 缺省 OSArch 必须是 GOOS/GOARCH 斜杠格式（与平台架构字典一致）。
// 历史缺省为连字符（linux-amd64），与平台声明的 linux/amd64 等值失配，
// 导致 selectVersion/selectArtifact 全跳过、自动更新静默失效——回归测试。
func TestNewUpdaterDefaultOSArch(t *testing.T) {
	platform := newFakePlatform(t)
	want := runtime.GOOS + "/" + runtime.GOARCH
	platform.mu.Lock()
	platform.versions = append(platform.versions, fakeVersion{
		version: "2.4.0", buildNumber: "20260808.1", sourceRange: ">=2.0.0",
		osArch: []string{want}, releasedAt: time.Now().Add(-time.Hour).UnixMilli(),
		artifactData: []byte("fake-default-osarch"),
	})
	platform.mu.Unlock()

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	// 不传 OSArch，验证缺省归一化为斜杠格式
	updater, err := NewUpdater(client, UpdaterOptions{})
	if err != nil {
		t.Fatalf("NewUpdater 失败: %v", err)
	}
	if updater.options.OSArch != want {
		t.Fatalf("缺省 OSArch 应为 %q（斜杠，与平台架构字典一致），实际 %q", want, updater.options.OSArch)
	}

	// 缺省 OSArch 应能命中平台声明的同架构版本（等值匹配不失配）
	info, err := updater.CheckNow(t.Context())
	if err != nil {
		t.Fatalf("CheckNow 失败: %v", err)
	}
	if !info.Available || info.Manifest == nil {
		t.Fatalf("缺省 OSArch %q 应命中同架构已发布版本: %+v", want, info)
	}
}

// TestCheckUpdateNone - 当前已是最新版本时返回无更新
func TestCheckUpdateNone(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes"))

	options := testOptions(platform, t.TempDir())
	options.Version = "2.4.0"
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	info, err := client.CheckUpdate(t.Context(), "linux/amd64")
	if err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	if info.Available {
		t.Fatalf("已是最新不应有更新: %+v", info.Manifest)
	}
}

// TestCheckUpdateUpgradeRightGate - 升级权门控：发布时间晚于 upgradeUntil 的版本不可见
func TestCheckUpdateUpgradeRightGate(t *testing.T) {

	platform := newFakePlatform(t)
	platform.mu.Lock()
	platform.upgradeUntil = time.Now().Add(-2 * time.Hour).UnixMilli() // 升级权已过期（早于版本发布）
	platform.mu.Unlock()
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes"))

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	info, err := client.CheckUpdate(t.Context(), "linux/amd64")
	if err != nil {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}
	if info.Available {
		t.Fatalf("升级权过期不应命中新版本")
	}
}

// TestCheckUpdateTamperedManifest - 清单签名被篡改时验签失败
func TestCheckUpdateTamperedManifest(t *testing.T) {

	platform := newFakePlatform(t)
	withUpdate(platform, "2.4.0", []byte("fake-artifact-bytes"))
	platform.mu.Lock()
	platform.tamperManifest = true
	platform.mu.Unlock()

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	if _, err = client.CheckUpdate(t.Context(), "linux/amd64"); err == nil {
		t.Fatalf("篡改清单必须验签失败")
	}
}

// TestDownloadArtifact - 下载发布物：大小 + SHA-256 校验通过落盘；内容被篡改时拒绝
func TestDownloadArtifact(t *testing.T) {

	platform := newFakePlatform(t)
	data := []byte("fake-artifact-bytes-v2.4.0")
	withUpdate(platform, "2.4.0", data)

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	info, err := client.CheckUpdate(t.Context(), "linux/amd64")
	if err != nil || !info.Available {
		t.Fatalf("CheckUpdate 失败: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "app-2.4.0.tar.gz")
	if err = client.DownloadArtifact(t.Context(), info.Manifest, info.Manifest.Payload.Artifacts[0], dest); err != nil {
		t.Fatalf("DownloadArtifact 失败: %v", err)
	}
	downloaded, _ := os.ReadFile(dest)
	if string(downloaded) != string(data) {
		t.Fatalf("下载内容不一致")
	}

	// 篡改服务端文件内容后下载必须失败（SHA-256 不匹配）
	platform.mu.Lock()
	platform.versions[0].artifactData = []byte("tampered-bytes")
	platform.mu.Unlock()
	if err = client.DownloadArtifact(t.Context(), info.Manifest, info.Manifest.Payload.Artifacts[0], dest); err == nil {
		t.Fatalf("篡改内容必须校验失败")
	}
}

// TestReportUpgradeFlow - 升级结果上报：创建记录 → 推进状态 → 追加日志
func TestReportUpgradeFlow(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Stop()

	recordNo, err := client.ReportUpgrade(t.Context(), UpgradeReport{
		FromVersion: "2.3.1", TargetVersion: "2.4.0", Status: UpgradeDownloading, Message: "开始下载",
	})
	if err != nil || recordNo == "" {
		t.Fatalf("创建升级记录失败: %v %v", recordNo, err)
	}

	again, err := client.ReportUpgrade(t.Context(), UpgradeReport{
		RecordNo: recordNo, TargetVersion: "2.4.0", Status: UpgradeSuccess, Message: "升级完成",
	})
	if err != nil || again != recordNo {
		t.Fatalf("推进升级记录失败: %v %v", again, err)
	}

	if err = client.ReportUpgradeLog(t.Context(), recordNo, []string{"预检通过", "安装完成"}); err != nil {
		t.Fatalf("日志上报失败: %v", err)
	}
}
