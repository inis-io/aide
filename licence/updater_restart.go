package licence

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// exitProcess - 进程退出函数（包级可替换：单测替换为 no-op，避免真实退出测试进程）
var exitProcess = os.Exit

// startProcess - 进程拉起函数（包级可替换：单测替换为假 Proc，避免真实拉起测试进程）
var startProcess = os.StartProcess

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

// executeRestart - 执行重启（pending_restart 阶段调用）
//
//	exit-code：swap 完成后以 RestartExitCode 退出，由外部守护拉起新二进制
//	auto：探测到守护 → exit-code；无守护 → respawn 拉起自身（设计 §5.4）
//	respawn：os.StartProcess 同 argv/env 拉起自身 → 父进程退出 0
//	callback：仅通知，重启时机由客户自控（K8s 滚动更新/多副本轮替）
func (this *Updater) executeRestart(ctx context.Context, mode string) {

	switch mode {
	case "exit-code":
		this.logf(ctx, "以 exit-code(%d) 退出，等待守护拉起", this.options.RestartExitCode)
		exitProcess(this.options.RestartExitCode)
	case "respawn":
		this.respawn(ctx)
	case "auto":
		if this.detectDaemon() {
			this.logf(ctx, "检测到守护环境，以 exit-code(%d) 退出等待拉起", this.options.RestartExitCode)
			exitProcess(this.options.RestartExitCode)
		}
		// 无守护裸跑：respawn 拉起自身，避免进程退出即死
		this.respawn(ctx)
	default:
		// callback：仅通知，重启时机由客户自控
		this.logf(ctx, "callback 模式：更新已替换，等待外部重启")
	}
}

// respawn - 无守护裸跑场景：以同 argv/env、继承 stdio 拉起自身新进程后父进程退出 0，
// 新进程生命周期独立于父进程（设计 §5.4）；启动失败退化为 exit-code 等待守护。
func (this *Updater) respawn(ctx context.Context) {

	exe, err := os.Executable()
	if err != nil {
		this.logf(ctx, "respawn 失败（无法定位自身），退化为 exit-code(%d)", this.options.RestartExitCode)
		exitProcess(this.options.RestartExitCode)
		return
	}
	dir, _ := os.Getwd()
	argv := append([]string{exe}, os.Args[1:]...)
	proc, err := startProcess(exe, argv, &os.ProcAttr{
		Dir: dir, Env: os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	if err != nil {
		this.logf(ctx, "respawn 启动失败，退化为 exit-code(%d): %v", this.options.RestartExitCode, err)
		exitProcess(this.options.RestartExitCode)
		return
	}
	// 让出进程句柄，父进程退出后新进程继续存活
	if err = proc.Release(); err != nil {
		this.logf(ctx, "respawn 释放句柄失败: %v", err)
	}
	this.logf(ctx, "respawn 已拉起新进程，父进程退出")
	exitProcess(0)
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
