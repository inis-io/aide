package licence

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ============================= 测试辅助 =============================

// newTestUpdater - 构造指向假平台的 Updater：
// 先同步激活 client（token 落内存）再停止后台循环，避免 validate 干扰流水线断言
func newTestUpdater(t *testing.T, platform *fakePlatform, dir string, opts UpdaterOptions) (*Client, *Updater) {

	t.Helper()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("client.Start 激活失败: %v", err)
	}
	client.Stop()
	updater, err := NewUpdater(client, opts)
	if err != nil {
		t.Fatalf("NewUpdater 失败: %v", err)
	}
	return client, updater
}

// writeTestState - 手工写入持久化状态（构造崩溃现场）
func writeTestState(t *testing.T, updater *Updater, state updateState) {

	t.Helper()
	updater.mu.Lock()
	updater.state = state
	updater.mu.Unlock()
	if err := updater.saveState(); err != nil {
		t.Fatalf("写入测试状态失败: %v", err)
	}
}

// reportStatuses - 平台收到的上报轨迹（按时间序）
func reportStatuses(t *testing.T, platform *fakePlatform) []string {

	t.Helper()
	platform.mu.Lock()
	defer platform.mu.Unlock()
	var statuses []string
	for _, item := range platform.reports {
		status, _ := item["status"].(string)
		statuses = append(statuses, status)
	}
	return statuses
}

// lastReportStatus - 最近一次上报状态（空轨迹返回 ""）
func lastReportStatus(t *testing.T, platform *fakePlatform) string {

	statuses := reportStatuses(t, platform)
	if len(statuses) == 0 {
		return ""
	}
	return statuses[len(statuses)-1]
}

// reportRecordNos - 各次上报携带的 recordNo（验证创建与复用的同一性）
func reportRecordNos(t *testing.T, platform *fakePlatform) []string {

	t.Helper()
	platform.mu.Lock()
	defer platform.mu.Unlock()
	var records []string
	for _, item := range platform.reports {
		record, _ := item["recordNo"].(string)
		records = append(records, record)
	}
	return records
}

// boolPtr - 便于显式传 AutoCheck/AutoUpdate 指针
func boolPtr(value bool) *bool { return &value }

// readFileString - 读取文件为字符串（断言用）
func readFileString(t *testing.T, path string) string {

	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败 %s: %v", path, err)
	}
	return string(raw)
}

// buildZip - 构造 zip 字节（键为包内路径，值为内容）
func buildZip(t *testing.T, files map[string]string) []byte {

	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("创建 zip 条目失败 %s: %v", name, err)
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatalf("写入 zip 条目失败 %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 zip 失败: %v", err)
	}
	return buffer.Bytes()
}

// buildTarGz - 构造 tar.gz 字节（键为包内路径，值为内容）
func buildTarGz(t *testing.T, files map[string]string) []byte {

	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("写 tar 头失败 %s: %v", name, err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("写 tar 内容失败 %s: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("关闭 tar 失败: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败: %v", err)
	}
	return buffer.Bytes()
}

// ============================= 自替换核心 =============================

// TestUpdaterSwapBinaryFile - 单文件替换核心：目标被新文件覆盖，源文件被移走，
// Windows 额外保留 *.old 由新进程清理
func TestUpdaterSwapBinaryFile(t *testing.T) {

	dir := t.TempDir()
	source := filepath.Join(dir, "source.bin")
	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(source, []byte("new-content"), 0o600); err != nil {
		t.Fatalf("写源文件失败: %v", err)
	}
	if err := os.WriteFile(target, []byte("old-content"), 0o600); err != nil {
		t.Fatalf("写目标文件失败: %v", err)
	}

	oldFile, err := swapBinaryFile(source, target)
	if err != nil {
		t.Fatalf("swapBinaryFile 失败: %v", err)
	}
	if got := readFileString(t, target); got != "new-content" {
		t.Fatalf("目标文件应为新内容，实际 %q", got)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("源文件应被移走，实际 err=%v", err)
	}
	if runtime.GOOS == "windows" {
		if oldFile != target+".old" {
			t.Fatalf("Windows 应返回 .old 路径，实际 %q", oldFile)
		}
		if got := readFileString(t, target+".old"); got != "old-content" {
			t.Fatalf(".old 应留存旧内容，实际 %q", got)
		}
	} else if oldFile != "" {
		t.Fatalf("非 Windows 不应返回 .old 路径，实际 %q", oldFile)
	}
}

// ============================= 崩溃恢复（状态机迁移） =============================

// TestUpdaterRecoverPendingRestartCommits - pending_restart 崩溃恢复：
// 新进程接管进入 verifying → 健康确认 Commit → 上报 success、状态文件清理
func TestUpdaterRecoverPendingRestartCommits(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{AutoCheck: boolPtr(false)})
	writeTestState(t, updater, updateState{
		Phase: PhasePendingRestart, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240", RecordNo: "",
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	if _, err := os.Stat(updater.statePath()); !os.IsNotExist(err) {
		t.Fatalf("Commit 后状态文件应清理，实际 err=%v", err)
	}
	if got := lastReportStatus(t, platform); got != UpgradeSuccess {
		t.Fatalf("恢复成功应上报 success，实际 %q", got)
	}
}

// TestUpdaterRecoverVerifyingWithinTimeoutCommits - verifying 未超时恢复：直接健康确认
func TestUpdaterRecoverVerifyingWithinTimeoutCommits(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{AutoCheck: boolPtr(false)})
	writeTestState(t, updater, updateState{
		Phase: PhaseVerifying, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240", VerifyingUntil: nowMs() + 60_000,
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	if got := lastReportStatus(t, platform); got != UpgradeSuccess {
		t.Fatalf("未超时应直接确认 success，实际 %q", got)
	}
}

// TestUpdaterRecoverVerifyingTimeoutRollsBack - verifying 超时恢复：自动回滚并上报 rolled_back
func TestUpdaterRecoverVerifyingTimeoutRollsBack(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("建目标目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "app.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("写目标文件失败: %v", err)
	}
	_, updater := newTestUpdater(t, platform, dir, UpdaterOptions{
		Mode: ApplyDirectory, TargetPath: target, AutoCheck: boolPtr(false),
	})
	backupDir := filepath.Join(updater.updateDir(), "backup", "2.4.0")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("建备份目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "app.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("写备份文件失败: %v", err)
	}
	writeTestState(t, updater, updateState{
		Phase: PhaseVerifying, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240", BackupDir: backupDir, VerifyingUntil: nowMs() - 1_000,
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	if got := readFileString(t, filepath.Join(target, "app.txt")); got != "old" {
		t.Fatalf("超时后应回滚到备份内容，实际 %q", got)
	}
	if got := lastReportStatus(t, platform); got != UpgradeRolledBack {
		t.Fatalf("回滚应上报 rolled_back，实际 %q", got)
	}
}

// TestUpdaterRecoverInterruptedDownloadCleans - downloading 中断恢复：清理工作区回 idle
func TestUpdaterRecoverInterruptedDownloadCleans(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{AutoCheck: boolPtr(false)})
	if err := os.MkdirAll(filepath.Join(updater.updateDir(), "work", "2.4.0"), 0o700); err != nil {
		t.Fatalf("构造下载工作区失败: %v", err)
	}
	writeTestState(t, updater, updateState{
		Phase: PhaseDownloading, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240",
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	state, _, err := updater.loadState()
	if err != nil {
		t.Fatalf("loadState 失败: %v", err)
	}
	if state.Phase != PhaseIdle {
		t.Fatalf("中断应清理回 idle，实际 %q", state.Phase)
	}
	if _, err = os.Stat(filepath.Join(updater.updateDir(), "work")); !os.IsNotExist(err) {
		t.Fatalf("中断恢复应清理工作区，实际 err=%v", err)
	}
}

// TestUpdaterRecoverSwappingTargetPresentCommits - swapping 中断但新文件已落位：继续 verifying 确认
func TestUpdaterRecoverSwappingTargetPresentCommits(t *testing.T) {

	platform := newFakePlatform(t)
	dir := t.TempDir()
	swapTarget := filepath.Join(dir, "swap-target")
	if err := os.WriteFile(swapTarget, []byte("new"), 0o600); err != nil {
		t.Fatalf("写替换目标失败: %v", err)
	}
	_, updater := newTestUpdater(t, platform, dir, UpdaterOptions{AutoCheck: boolPtr(false)})
	writeTestState(t, updater, updateState{
		Phase: PhaseSwapping, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240", SwapTarget: swapTarget,
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	if got := lastReportStatus(t, platform); got != UpgradeSuccess {
		t.Fatalf("新文件已落位应确认 success，实际 %q", got)
	}
}

// TestUpdaterRecoverSwappingTargetMissingIdle - swapping 中断且新文件未落位：清理回 idle（旧版本未动）
func TestUpdaterRecoverSwappingTargetMissingIdle(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{AutoCheck: boolPtr(false)})
	writeTestState(t, updater, updateState{
		Phase: PhaseSwapping, FromVersion: "2.3.1", TargetVersion: "2.4.0",
		ArtifactNo: "ART-2026-240", SwapTarget: filepath.Join(t.TempDir(), "not-exist"),
	})

	if err := updater.Start(t.Context()); err != nil {
		t.Fatalf("Start 恢复失败: %v", err)
	}
	state, _, err := updater.loadState()
	if err != nil {
		t.Fatalf("loadState 失败: %v", err)
	}
	if state.Phase != PhaseIdle {
		t.Fatalf("替换未完成应清理回 idle，实际 %q", state.Phase)
	}
}

// ============================= 解包 =============================

// TestUpdaterUnpackZip - zip 解包：目录结构与内容正确
func TestUpdaterUnpackZip(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	raw := buildZip(t, map[string]string{
		"bin/app.exe": "new-binary",
		"config.yaml": "k: v",
	})
	src := filepath.Join(t.TempDir(), "pkg.zip")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}

	staging, deleteList, err := updater.unpackArtifact(t.Context(), ManifestArtifact{FileName: "pkg.zip"}, "2.4.0", src)
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if got := readFileString(t, filepath.Join(staging, "bin", "app.exe")); got != "new-binary" {
		t.Fatalf("解包内容错误: %q", got)
	}
	if got := readFileString(t, filepath.Join(staging, "config.yaml")); got != "k: v" {
		t.Fatalf("解包内容错误: %q", got)
	}
	if len(deleteList) != 0 {
		t.Fatalf("全量包不应有删除清单: %v", deleteList)
	}
}

// TestUpdaterUnpackTarGz - tar.gz 解包
func TestUpdaterUnpackTarGz(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	raw := buildTarGz(t, map[string]string{
		"bin/app.exe": "new-binary",
	})
	src := filepath.Join(t.TempDir(), "pkg.tar.gz")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatalf("写 tar.gz 失败: %v", err)
	}

	staging, _, err := updater.unpackArtifact(t.Context(), ManifestArtifact{FileName: "pkg.tar.gz"}, "2.4.0", src)
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if got := readFileString(t, filepath.Join(staging, "bin", "app.exe")); got != "new-binary" {
		t.Fatalf("解包内容错误: %q", got)
	}
}

// TestUpdaterUnpackRawBinary - 裸二进制：单文件直接落位 staging
func TestUpdaterUnpackRawBinary(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	src := filepath.Join(t.TempDir(), "myapp")
	if err := os.WriteFile(src, []byte("raw-binary"), 0o600); err != nil {
		t.Fatalf("写裸二进制失败: %v", err)
	}

	staging, _, err := updater.unpackArtifact(t.Context(), ManifestArtifact{FileName: "myapp"}, "2.4.0", src)
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if got := readFileString(t, filepath.Join(staging, "myapp")); got != "raw-binary" {
		t.Fatalf("裸二进制内容错误: %q", got)
	}
}

// TestUpdaterUnpackIncrementalDeleteList - 增量包根级 delete.list 解析（忽略注释与空行）
func TestUpdaterUnpackIncrementalDeleteList(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	raw := buildZip(t, map[string]string{
		"bin/app.exe": "new-binary",
		"delete.list": "# 注释行\n\nconfig.yaml\nstale.txt",
	})
	src := filepath.Join(t.TempDir(), "inc.zip")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}

	_, deleteList, err := updater.unpackArtifact(t.Context(), ManifestArtifact{FileName: "inc.zip"}, "2.4.0", src)
	if err != nil {
		t.Fatalf("解包失败: %v", err)
	}
	if len(deleteList) != 2 || deleteList[0] != "config.yaml" || deleteList[1] != "stale.txt" {
		t.Fatalf("delete.list 解析错误: %v", deleteList)
	}
}

// TestUpdaterUnpackRejectsPathEscape - 归档路径逃逸（../）必须拒绝，防止覆盖目标外文件
func TestUpdaterUnpackRejectsPathEscape(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	raw := buildZip(t, map[string]string{
		"../evil.txt": "evil",
	})
	src := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatalf("写 zip 失败: %v", err)
	}

	if _, _, err := updater.unpackArtifact(t.Context(), ManifestArtifact{FileName: "evil.zip"}, "2.4.0", src); err == nil {
		t.Fatalf("路径逃逸应被拒绝")
	}
}

// ============================= 备份滚动清理 =============================

// TestUpdaterCleanupBackups - KeepBackups 滚动清理：仅保留最新 N 份（按修改时间）
func TestUpdaterCleanupBackups(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{
		KeepBackups: 1, AutoCheck: boolPtr(false),
	})
	root := filepath.Join(updater.updateDir(), "backup")
	base := time.Now().Add(-time.Hour)
	for index, version := range []string{"2.1.0", "2.2.0", "2.3.1"} {
		dir := filepath.Join(root, version)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("建备份目录失败: %v", err)
		}
		stamp := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatalf("设置备份时间失败: %v", err)
		}
	}

	updater.cleanupBackups()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读取备份目录失败: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "2.3.1" {
		t.Fatalf("应仅保留最新备份，实际 %v", entries)
	}
}

// ============================= 选包与策略 =============================

// TestUpdaterSelectArtifactPreferIncremental - 增量优先：sourceVersion 精确匹配当前版本
func TestUpdaterSelectArtifactPreferIncremental(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	manifest := &Manifest{Payload: ManifestPayload{Artifacts: []ManifestArtifact{
		{ArtifactNo: "full", ArtifactType: "full"},
		{ArtifactNo: "inc", ArtifactType: "incremental", SourceVersion: "2.3.1"},
	}}}

	got, err := updater.selectArtifact(manifest, "2.3.1")
	if err != nil || got.ArtifactNo != "inc" {
		t.Fatalf("应选中增量包，实际 %v, err=%v", got.ArtifactNo, err)
	}
	got, err = updater.selectArtifact(manifest, "2.0.0")
	if err != nil || got.ArtifactNo != "full" {
		t.Fatalf("来源版本不匹配应回退全量包，实际 %v, err=%v", got.ArtifactNo, err)
	}
}

// TestUpdaterSelectArtifactFiltersOsArch - osArch 不匹配的发布物被过滤
func TestUpdaterSelectArtifactFiltersOsArch(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{OSArch: "linux-amd64"})
	manifest := &Manifest{Payload: ManifestPayload{Artifacts: []ManifestArtifact{
		{ArtifactNo: "other", OsArch: "darwin-arm64", ArtifactType: "full"},
		{ArtifactNo: "match", ArtifactType: "full"},
	}}}

	got, err := updater.selectArtifact(manifest, "2.0.0")
	if err != nil || got.ArtifactNo != "match" {
		t.Fatalf("应过滤 osArch 不匹配项，实际 %v, err=%v", got.ArtifactNo, err)
	}
}

// TestUpdaterShouldAutoPolicy - 自动更新判定：清单策略权威 > 本地配置兜底 > 默认 false
func TestUpdaterShouldAutoPolicy(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})

	info := func(policy *ManifestUpdatePolicy) UpdateInfo {
		return UpdateInfo{Manifest: &Manifest{Payload: ManifestPayload{UpdatePolicy: policy}}}
	}
	if !updater.shouldAuto(info(&ManifestUpdatePolicy{Force: true})) {
		t.Fatalf("force 策略应自动执行")
	}
	if !updater.shouldAuto(info(&ManifestUpdatePolicy{Auto: true})) {
		t.Fatalf("auto 策略应自动执行")
	}
	if updater.shouldAuto(info(nil)) {
		t.Fatalf("无策略 + 未配置应默认不自动")
	}

	updater.options.AutoUpdate = boolPtr(true)
	if !updater.shouldAuto(info(nil)) {
		t.Fatalf("本地 AutoUpdate=true 应兜底自动执行")
	}
	updater.options.AutoUpdate = boolPtr(false)
	if !updater.shouldAuto(info(&ManifestUpdatePolicy{Auto: true, Force: false})) {
		t.Fatalf("清单 auto 策略权威，本地配置不得覆盖")
	}
}

// ============================= 防降级 =============================

// TestUpdaterRejectsDowngrade - 目标版本不高于当前版本时拒绝执行
func TestUpdaterRejectsDowngrade(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})
	err := updater.apply(t.Context(), UpdateInfo{Manifest: &Manifest{
		Payload: ManifestPayload{Version: "2.0.0"},
	}})
	if err == nil || !strings.Contains(err.Error(), "拒绝降级") {
		t.Fatalf("降级应被拒绝，实际 %v", err)
	}
}

// ============================= Pending 查询 =============================

// TestUpdaterPending - Pending 仅当已替换待重启（pending_restart / verifying）
func TestUpdaterPending(t *testing.T) {

	platform := newFakePlatform(t)
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{})

	updater.mu.Lock()
	updater.state.Phase = PhaseIdle
	updater.mu.Unlock()
	if _, pending := updater.Pending(); pending {
		t.Fatalf("idle 不应处于待重启")
	}

	updater.mu.Lock()
	updater.state.Phase = PhasePendingRestart
	updater.state.TargetVersion = "2.4.0"
	updater.lastInfo = UpdateInfo{Available: true, Manifest: &Manifest{Payload: ManifestPayload{Version: "2.4.0"}}}
	updater.mu.Unlock()
	if info, pending := updater.Pending(); !pending || info.Manifest == nil {
		t.Fatalf("pending_restart 应处于待重启")
	}
}

// ============================= 完整流水线（Directory 模式） =============================

// TestUpdaterApplyDirectoryFullFlow - 全量包完整流水线：
// 下载 → 解包 → 备份 → 替换 → pending_restart，上报轨迹 [downloading, installing] 且 recordNo 复用
func TestUpdaterApplyDirectoryFullFlow(t *testing.T) {

	platform := newFakePlatform(t)
	platform.versions = []fakeVersion{{
		version: "2.4.0", buildNumber: "2026081801", sourceRange: ">=2.0.0",
		artifacts: []fakeArtifact{{
			fileName: "app.zip",
			data:     buildZip(t, map[string]string{"bin/new.txt": "new-content"}),
		}},
	}}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("建目标目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("写旧文件失败: %v", err)
	}
	_, updater := newTestUpdater(t, platform, dir, UpdaterOptions{
		Mode: ApplyDirectory, TargetPath: target, AutoCheck: boolPtr(false), RestartDelay: 1,
	})

	origExit := exitProcess
	exitProcess = func(code int) {}
	defer func() { exitProcess = origExit }()

	info, err := updater.CheckNow(t.Context())
	if err != nil || !info.Available {
		t.Fatalf("CheckNow 应发现可用更新: %v", err)
	}
	if err = updater.Apply(t.Context(), info); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}

	if got := readFileString(t, filepath.Join(target, "bin", "new.txt")); got != "new-content" {
		t.Fatalf("新文件未落位: %q", got)
	}
	if _, err = os.Stat(filepath.Join(target, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("全量包应整体替换，旧文件不应存在，实际 err=%v", err)
	}
	updater.mu.RLock()
	phase := updater.state.Phase
	targetVersion := updater.state.TargetVersion
	updater.mu.RUnlock()
	if phase != PhasePendingRestart || targetVersion != "2.4.0" {
		t.Fatalf("应停在 pending_restart，实际 %q / %q", phase, targetVersion)
	}

	statuses := reportStatuses(t, platform)
	if len(statuses) != 2 || statuses[0] != UpgradeDownloading || statuses[1] != UpgradeInstalling {
		t.Fatalf("上报轨迹应为 [downloading installing]，实际 %v", statuses)
	}
	records := reportRecordNos(t, platform)
	if len(records) != 2 || records[0] == "" || records[0] != records[1] {
		t.Fatalf("recordNo 应创建后复用，实际 %v", records)
	}
}

// TestUpdaterApplyDirectoryIncrementalFlow - 增量包完整流水线：
// 基底 = 旧目录副本，变更覆盖 + delete.list 删除，未变更文件保留
func TestUpdaterApplyDirectoryIncrementalFlow(t *testing.T) {

	platform := newFakePlatform(t)
	platform.versions = []fakeVersion{{
		version: "2.4.0", buildNumber: "2026081801", sourceRange: ">=2.0.0",
		artifacts: []fakeArtifact{
			{artifactType: "full", fileName: "full.zip",
				data: buildZip(t, map[string]string{"full-only.txt": "x"})},
			{artifactType: "incremental", sourceVersion: "2.3.1", fileName: "inc.zip",
				data: buildZip(t, map[string]string{
					"bin/new.txt": "new-content",
					"delete.list": "# 删除旧配置\nstale.txt",
				})},
		},
	}}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	for _, file := range []string{"old.txt", "stale.txt", "bin/old-bin.txt"} {
		path := filepath.Join(target, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(path, []byte("keep-or-delete"), 0o600); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	_, updater := newTestUpdater(t, platform, dir, UpdaterOptions{
		Mode: ApplyDirectory, TargetPath: target, AutoCheck: boolPtr(false), RestartDelay: 1,
	})

	origExit := exitProcess
	exitProcess = func(code int) {}
	defer func() { exitProcess = origExit }()

	info, err := updater.CheckNow(t.Context())
	if err != nil || !info.Available {
		t.Fatalf("CheckNow 应发现可用更新: %v", err)
	}
	if err = updater.Apply(t.Context(), info); err != nil {
		t.Fatalf("Apply 失败: %v", err)
	}

	if got := readFileString(t, filepath.Join(target, "bin", "new.txt")); got != "new-content" {
		t.Fatalf("增量新增文件未落位: %q", got)
	}
	for _, file := range []string{"old.txt", "bin/old-bin.txt"} {
		if _, err = os.Stat(filepath.Join(target, file)); err != nil {
			t.Fatalf("增量包不应删除未变更文件 %s: %v", file, err)
		}
	}
	if _, err = os.Stat(filepath.Join(target, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("delete.list 应删除 stale.txt，实际 err=%v", err)
	}
	if _, err = os.Stat(filepath.Join(target, "full-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("不应应用未选中的全量包内容，实际 err=%v", err)
	}
}

// TestFindSingleFileRejects - 自替换前置定位：解包目录必须恰好一个常规文件
// （SelfBinary 完整流水线会替换测试进程自身，故只测替换前的定位安全门槛）
func TestFindSingleFileRejects(t *testing.T) {

	if _, err := findSingleFile(t.TempDir()); err == nil {
		t.Fatalf("空解包目录应拒绝定位唯一文件")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("a"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.bin"), []byte("b"), 0o600); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	if _, err := findSingleFile(dir); err == nil {
		t.Fatalf("多文件解包目录应拒绝定位唯一文件")
	}
	_ = os.Remove(filepath.Join(dir, "b.bin"))
	if got, err := findSingleFile(dir); err != nil || got != filepath.Join(dir, "a.bin") {
		t.Fatalf("单文件应返回该文件: %v %v", got, err)
	}
}

// TestUpdaterApplyBackupFailureReportsFailed - 备份失败走统一 fail 出口：
// 上报 failed 并落 failed 终态（Directory 模式 target 缺失触发）
func TestUpdaterApplyBackupFailureReportsFailed(t *testing.T) {

	platform := newFakePlatform(t)
	platform.versions = []fakeVersion{{
		version: "2.4.0", buildNumber: "2026081801", sourceRange: ">=2.0.0",
		artifacts: []fakeArtifact{{fileName: "app.zip",
			data: buildZip(t, map[string]string{"bin/app.exe": "new"})}},
	}}
	_, updater := newTestUpdater(t, platform, t.TempDir(), UpdaterOptions{
		Mode: ApplyDirectory, TargetPath: filepath.Join(t.TempDir(), "missing-target"),
		AutoCheck: boolPtr(false), RestartDelay: 1,
	})

	info, err := updater.CheckNow(t.Context())
	if err != nil || !info.Available {
		t.Fatalf("CheckNow 应发现可用更新: %v", err)
	}
	if err = updater.Apply(t.Context(), info); err == nil {
		t.Fatalf("目标目录缺失应导致流水线失败")
	}
	if got := lastReportStatus(t, platform); got != UpgradeFailed {
		t.Fatalf("备份失败应上报 failed，实际 %q", got)
	}
	updater.mu.RLock()
	phase := updater.state.Phase
	updater.mu.RUnlock()
	if phase != PhaseFailed {
		t.Fatalf("失败后应落 failed 终态，实际 %q", phase)
	}
}
