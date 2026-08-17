package licence

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// exitProcess - 进程退出函数（包级可替换：单测替换为 no-op，避免真实退出测试进程）
var exitProcess = os.Exit

// resolveRestartMode - 解析生效的重启模式：清单策略 > SDK 集成配置 > auto 自动探测
func (this *Updater) resolveRestartMode(manifest *Manifest) string {

	if manifest != nil && manifest.Payload.UpdatePolicy != nil &&
		manifest.Payload.UpdatePolicy.RestartMode != "" {
		return manifest.Payload.UpdatePolicy.RestartMode
	}
	if this.options.RestartMode != "" {
		return this.options.RestartMode
	}
	return "auto"
}

// detectDaemon - 守护环境探测（auto 模式分流依据）：
// systemd（INVOCATION_ID）/ Docker（/.dockerenv）/ Windows 服务 SCM（父链 services.exe）
func (this *Updater) detectDaemon() bool {

	if runtime.GOOS == "windows" {
		return detectWindowsService()
	}
	if os.Getenv("INVOCATION_ID") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// executeRestart - 执行重启（pending_restart 阶段调用；respawn/callback 阶段四接入）
//
//	exit-code：swap 完成后以 RestartExitCode 退出，由外部守护拉起新二进制
//	auto：探测到守护 → exit-code；无守护 → 阶段三以 exit-code 兜底退出（respawn 阶段四提供）
func (this *Updater) executeRestart(ctx context.Context, mode string) {

	switch mode {
	case "exit-code":
		this.logf(ctx, "以 exit-code(%d) 退出，等待守护拉起", this.options.RestartExitCode)
		exitProcess(this.options.RestartExitCode)
	case "auto":
		if this.detectDaemon() {
			this.logf(ctx, "检测到守护环境，以 exit-code(%d) 退出等待拉起", this.options.RestartExitCode)
			exitProcess(this.options.RestartExitCode)
		}
		// 无守护：阶段三以 exit-code 兜底（respawn 模式阶段四接入后自动切换）
		this.logf(ctx, "未检测到守护，以 exit-code(%d) 退出（respawn 模式阶段四接入）", this.options.RestartExitCode)
		exitProcess(this.options.RestartExitCode)
	default:
		// callback：仅通知，重启时机由客户自控（阶段四细化）
		this.logf(ctx, "callback 模式：更新已替换，等待外部重启")
	}
}

// logf - 追加升级过程日志到平台（RecordNo 为空时忽略；上报失败不阻断流程）
func (this *Updater) logf(ctx context.Context, format string, args ...any) {

	this.mu.RLock()
	recordNo := this.state.RecordNo
	this.mu.RUnlock()
	if recordNo == "" {
		return
	}
	_ = this.client.ReportUpgradeLog(ctx, recordNo, []string{fmt.Sprintf(format, args...)})
}
