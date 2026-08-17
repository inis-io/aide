package licence

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// ApplyMode - 更新应用模式。
type ApplyMode string

const (
	// ApplySelfBinary - 自更新：替换当前可执行文件（默认）。
	ApplySelfBinary ApplyMode = "self-binary"
	// ApplyDirectory - 目录级更新：整体替换目标目录。
	ApplyDirectory ApplyMode = "directory"
)

// UpdaterOptions - 更新执行器配置（NewUpdater 归一化缺省值）。
//
// 接入最小形态：
//
//	updater, _ := licence.NewUpdater(client, licence.UpdaterOptions{
//		OSArch: runtime.GOOS + "-" + runtime.GOARCH,
//	})
//	go updater.Start(ctx) // 之后检查/下载/替换/重启/上报全自动
type UpdaterOptions struct {
	// OSArch - 平台架构（选包依据，必填；缺省 runtime.GOOS + "-" + runtime.GOARCH）
	OSArch string
	// Mode - 应用模式：ApplySelfBinary（默认，替换当前 exe）| ApplyDirectory（整体替换 TargetPath）
	Mode ApplyMode
	// TargetPath - Directory 模式目标目录（SelfBinary 缺省 os.Executable()）
	TargetPath string
	// AutoCheck - 周期自动检查，默认 true；false = 仅手动 CheckNow
	AutoCheck *bool
	// CheckInterval - 周期检查间隔，默认 6h（±10% 抖动，复用 loop 退避风格）
	CheckInterval time.Duration
	// AutoUpdate - 三级开关（设计 §5.8）：清单携带 updatePolicy 时以其背书为准（force/auto 权威）；
	// 旧清单无策略时以此兜底。nil = 默认 false（只通知不自动）
	AutoUpdate *bool
	// RestartMode - 重启方式：""(跟随平台) | auto(默认探测) | exit-code | respawn | callback
	RestartMode string
	// RestartExitCode - exit-code 模式退出码，默认 42
	RestartExitCode int
	// RestartDelay - 替换完成到退出的宽限，默认 3s
	RestartDelay time.Duration
	// HealthTimeout - 新进程 Commit 等待上限，默认 60s，超时判失败回滚
	HealthTimeout time.Duration
	// KeepBackups - 保留备份份数，默认 1
	KeepBackups int
	// OnUpdateAvailable - 发现新版本回调（被动模式 UI 弹窗用）
	OnUpdateAvailable func(UpdateInfo)
	// OnProgress - 下载/解包进度回调
	OnProgress func(phase string, done int64, total int64)
	// OnBeforeRestart - 重启前优雅收尾钩子（关 listener、flush）
	OnBeforeRestart func()
	// OnError - 非致命错误回调（检查失败等），默认吞掉
	OnError func(error)
}

// Updater - 更新执行器：检查 → 验签 → 下载 → 解包 → 备份 → 替换 → 优雅重启 → 上报 → 失败回滚。
// 与 Client 平级组合，状态机持久化于 StorageDir/update/state.json（断电可恢复）。
type Updater struct {
	// client - 所属运行面客户端
	client *Client
	// options - 归一化后的配置
	options UpdaterOptions

	// mu - 状态读写锁（state / lastInfo / running / cancel 共用）
	mu sync.RWMutex
	// state - 持久化更新状态
	state updateState
	// lastInfo - 最近一次检查结果（Pending / 被动 Apply 用）
	lastInfo UpdateInfo
	// running - 更新流水线是否执行中（单飞防并发）
	running bool
	// cancel - 周期检查循环取消函数
	cancel context.CancelFunc
}

// NewUpdater - 创建更新执行器（归一化配置，不发起网络请求）
func NewUpdater(client *Client, options UpdaterOptions) (*Updater, error) {

	if client == nil {
		return nil, errors.New("client 不能为空")
	}
	if options.OSArch == "" {
		options.OSArch = runtime.GOOS + "-" + runtime.GOARCH
	}
	if options.Mode == "" {
		options.Mode = ApplySelfBinary
	}
	if options.CheckInterval <= 0 {
		options.CheckInterval = 6 * time.Hour
	}
	if options.RestartExitCode == 0 {
		options.RestartExitCode = 42
	}
	if options.RestartDelay <= 0 {
		options.RestartDelay = 3 * time.Second
	}
	if options.HealthTimeout <= 0 {
		options.HealthTimeout = 60 * time.Second
	}
	if options.KeepBackups <= 0 {
		options.KeepBackups = 1
	}
	return &Updater{client: client, options: options, state: updateState{}}, nil
}

// Start - 启动更新执行器：恢复持久化状态（崩溃恢复）→ 清理替换残留 → 周期检查循环。
//
// 崩溃恢复（设计 §5.3）：
//   - pending_restart：旧进程已替换退出，新进程接管进入 verifying，健康确认后 Commit
//   - verifying 未超时：直接健康确认；超时：自动回滚并上报
//   - swapping 中断：新文件已落位则继续 verifying，否则清理回 idle
//   - downloading/unpacking/backing_up 中断：旧版本未动，清理工作区回 idle
func (this *Updater) Start(ctx context.Context) error {

	if err := this.recoverState(ctx); err != nil {
		return err
	}
	this.cleanupOldFiles()

	loopCtx, cancel := context.WithCancel(context.Background())
	this.mu.Lock()
	this.cancel = cancel
	this.mu.Unlock()

	autoCheck := true
	if this.options.AutoCheck != nil {
		autoCheck = *this.options.AutoCheck
	}
	if autoCheck {
		go this.checkLoop(loopCtx)
	}
	return nil
}

// Stop - 停止周期检查循环（不中断正在执行的更新流水线）
func (this *Updater) Stop() {

	this.mu.Lock()
	defer this.mu.Unlock()
	if this.cancel != nil {
		this.cancel()
		this.cancel = nil
	}
}

// CheckNow - 立即检查更新（绕过周期间隔），结果同时推给 OnUpdateAvailable
func (this *Updater) CheckNow(ctx context.Context) (UpdateInfo, error) {

	info, err := this.client.CheckUpdate(ctx, this.options.OSArch)
	if err != nil {
		return info, err
	}
	if info.Available {
		this.mu.Lock()
		this.lastInfo = info
		this.mu.Unlock()
		if this.options.OnUpdateAvailable != nil {
			this.options.OnUpdateAvailable(info)
		}
	}
	return info, nil
}

// Apply - 对一次已确认的更新执行完整流水线（含替换与重启触发）
func (this *Updater) Apply(ctx context.Context, info UpdateInfo) error {

	this.mu.Lock()
	if this.running {
		this.mu.Unlock()
		return errors.New("已有更新流水线在执行")
	}
	this.running = true
	this.mu.Unlock()
	defer func() {
		this.mu.Lock()
		this.running = false
		this.mu.Unlock()
	}()
	return this.apply(ctx, info)
}

// Commit - 新进程健康确认（Start 恢复时自动调用；幂等：非 verifying 状态直接返回）。
// 确认后上报 success、清理备份与工作区。
func (this *Updater) Commit(ctx context.Context) error {

	this.mu.Lock()
	if this.state.Phase != PhaseVerifying {
		this.mu.Unlock()
		return nil
	}
	this.state.Phase = PhaseCommitted
	fromVersion := this.state.FromVersion
	targetVersion := this.state.TargetVersion
	artifactNo := this.state.ArtifactNo
	recordNo := this.state.RecordNo
	this.mu.Unlock()

	// 上报 success（失败不阻断清理，next 轮不会重复推进：记录已落 success）
	_, _ = this.client.ReportUpgrade(ctx, UpgradeReport{
		RecordNo: recordNo, FromVersion: fromVersion, TargetVersion: targetVersion,
		ArtifactNo: artifactNo, Status: UpgradeSuccess, Message: "升级完成，运行正常",
	})
	this.cleanupBackups()
	this.cleanupWork()

	this.mu.Lock()
	this.state = updateState{}
	this.mu.Unlock()
	_ = os.Remove(this.statePath())
	return nil
}

// Rollback - 手动回滚到上一备份（限存在备份且当前版本与备份版本不一致）
func (this *Updater) Rollback(ctx context.Context) error {

	this.mu.RLock()
	backup := this.state.BackupDir
	this.mu.RUnlock()
	if backup == "" || !dirExists(backup) {
		return errors.New("无可用备份，无法回滚")
	}
	return this.rollback(ctx)
}

// EventUpdates - 返回近实时更新提示订阅器：收到 `update.available` 事件即触发一次
// 立即检查（CheckNow），策略放行时自动执行流水线；仅作提示，灰度与升级权仍以 check 判定为准。
// 需调用方执行 sub.Run(ctx)（或自行循环 Poll）；周期检查仍是保底通道（设计 §4.4）。
func (this *Updater) EventUpdates() *EventSubscriber {
	return this.client.Subscribe(CallbackOptions{}).
		OnEvent(EventUpdateAvailable, func(ctx context.Context, event *CallbackEvent) (Ack, error) {
			this.checkTick(ctx)
			return AckSuccess, nil
		})
}

// Pending - 查询是否存在「已替换待重启」的更新（客户 UI 显示「重启以完成更新」）
func (this *Updater) Pending() (UpdateInfo, bool) {

	this.mu.RLock()
	defer this.mu.RUnlock()
	if (this.state.Phase == PhasePendingRestart || this.state.Phase == PhaseVerifying) &&
		(this.lastInfo.Manifest != nil || this.state.TargetVersion != "") {
		return this.lastInfo, true
	}
	return UpdateInfo{}, false
}

// Run - 包装项目 main：启动 Updater（含崩溃恢复），main 返回后检测「更新待重启」并完成重启分流。
// 与直接调 Start 等价，但把「更新触发的退出」与「业务退出」分流，main 函数签名不变。
//
//	updater, _ := licence.NewUpdater(client, opts)
//	os.Exit(updater.Run(ctx, func(ctx context.Context) error {
//		return srv.ListenAndServe()
//	}))
func (this *Updater) Run(ctx context.Context, main func(ctx context.Context) error) error {

	if err := this.Start(ctx); err != nil {
		return err
	}
	mainErr := main(ctx)
	this.Stop()

	this.mu.RLock()
	pending := this.state.Phase == PhasePendingRestart
	restartMode := this.state.RestartMode
	this.mu.RUnlock()
	if pending {
		if this.options.OnBeforeRestart != nil {
			this.options.OnBeforeRestart()
		}
		this.executeRestart(ctx, restartMode)
	}
	return mainErr
}

// ============================ 内部实现 ============================

// checkLoop - 周期检查循环：立即检查一轮，之后按 CheckInterval ±10% 抖动周期执行
func (this *Updater) checkLoop(ctx context.Context) {

	this.checkTick(ctx)
	for {
		timer := time.NewTimer(this.nextCheckDelay())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			this.checkTick(ctx)
		}
	}
}

// nextCheckDelay - 下一次检查延迟（CheckInterval ±10% 抖动，避免整点惊群）
func (this *Updater) nextCheckDelay() time.Duration {

	delay := this.options.CheckInterval
	jitter := int64(delay / 10)
	if jitter > 0 {
		delay += time.Duration(rand.Int64N(2*jitter) - jitter)
	}
	return delay
}

// checkTick - 单轮检查：有可用更新且策略放行时自动执行流水线
func (this *Updater) checkTick(ctx context.Context) {

	info, err := this.CheckNow(ctx)
	if err != nil {
		if this.options.OnError != nil {
			this.options.OnError(err)
		}
		return
	}
	if !info.Available {
		return
	}
	if this.shouldAuto(info) {
		if err = this.Apply(ctx, info); err != nil && this.options.OnError != nil {
			this.options.OnError(err)
		}
	}
}

// recoverState - 崩溃恢复（Start 时同步执行），见 Start 文档注释
func (this *Updater) recoverState(ctx context.Context) error {

	state, ok, err := this.loadState()
	if err != nil || !ok {
		return err
	}
	this.mu.Lock()
	this.state = state
	this.mu.Unlock()

	switch state.Phase {
	case PhasePendingRestart:
		// 旧进程已完成替换退出，新进程接管：进入 verifying 并健康确认
		if err = this.setState(PhaseVerifying, func(s *updateState) {
			s.VerifyingUntil = nowMs() + this.options.HealthTimeout.Milliseconds()
		}); err != nil {
			return err
		}
		this.report(ctx, UpgradeInstalling, "新进程启动，健康确认中")
		return this.Commit(ctx)
	case PhaseVerifying:
		if state.VerifyingUntil > 0 && nowMs() < state.VerifyingUntil {
			// 未超时：直接健康确认
			return this.Commit(ctx)
		}
		// 超时：自动回滚（设计 §5.3）
		this.logf(ctx, "健康确认超时，自动回滚")
		return this.rollback(ctx)
	case PhaseSwapping:
		// 替换中断：新文件已落位则继续 verifying，否则清理回 idle（旧版本未动）
		this.mu.RLock()
		swapTarget := this.state.SwapTarget
		this.mu.RUnlock()
		if swapTarget != "" && fileExists(swapTarget) {
			if err = this.setState(PhaseVerifying, func(s *updateState) {
				s.VerifyingUntil = nowMs() + this.options.HealthTimeout.Milliseconds()
			}); err != nil {
				return err
			}
			return this.Commit(ctx)
		}
		this.cleanupWork()
		return this.setState(PhaseIdle, nil)
	case PhaseDownloading, PhaseUnpacking, PhaseBackingUp:
		// 未动旧版本：清理工作区回 idle
		this.cleanupWork()
		return this.setState(PhaseIdle, nil)
	}
	return nil
}

// apply - 完整更新流水线（CheckUpdate 已验签；本流程只消费已验签落盘的文件）
func (this *Updater) apply(ctx context.Context, info UpdateInfo) error {

	if info.Manifest == nil {
		return errors.New("清单为空")
	}
	manifest := info.Manifest
	target := manifest.Payload.Version
	from := this.currentVersion()

	// 防降级（设计 §7.4）：拒绝 targetVersion <= fromVersion
	if from != "" {
		targetVer, targetOk := parseSemver(target)
		fromVer, fromOk := parseSemver(from)
		if targetOk && fromOk && compareSemver(targetVer, fromVer) <= 0 {
			return errors.New("拒绝降级：目标 " + target + " <= 当前 " + from)
		}
	}

	artifact, err := this.selectArtifact(manifest, from)
	if err != nil {
		return err
	}

	// 写入基础状态（版本 / 发布物）
	this.mu.Lock()
	this.state.FromVersion = from
	this.state.TargetVersion = target
	this.state.ArtifactNo = artifact.ArtifactNo
	this.mu.Unlock()
	if err = this.setState(PhaseDownloading, nil); err != nil {
		return err
	}
	this.report(ctx, UpgradeDownloading, "开始下载更新包 " + artifact.ArtifactNo)

	// downloading：DownloadArtifact（验签 + SHA-256 + 原子落盘）
	downloadDir := filepath.Join(this.updateDir(), "download")
	if err = os.MkdirAll(downloadDir, 0o700); err != nil {
		return this.fail(ctx, err, "创建下载目录失败")
	}
	dest := filepath.Join(downloadDir, artifact.FileName)
	if err = this.client.DownloadArtifact(ctx, manifest, artifact, dest); err != nil {
		return this.fail(ctx, err, "下载更新包失败")
	}
	this.mu.Lock()
	this.state.DownloadPath = dest
	this.mu.Unlock()
	this.progress("downloading", artifact.Size, artifact.Size)

	// unpacking：按扩展名解包（增量包应用 delete.list 由 swap 阶段完成）
	if err = this.setState(PhaseUnpacking, nil); err != nil {
		return err
	}
	staging, deleteList, err := this.unpackArtifact(ctx, artifact, target, dest)
	if err != nil {
		return this.fail(ctx, err, "解包失败")
	}
	this.mu.Lock()
	this.state.UnpackDir = staging
	this.mu.Unlock()
	this.progress("unpacking", 1, 1)

	// backing_up：备份当前版本
	if err = this.setState(PhaseBackingUp, nil); err != nil {
		return err
	}
	backupDir, err := this.backupCurrent()
	if err != nil {
		return this.fail(ctx, err, "备份当前版本失败")
	}
	this.mu.Lock()
	this.state.BackupDir = backupDir
	this.mu.Unlock()

	// swapping：自替换 / 目录级替换；失败即回滚
	if err = this.setState(PhaseSwapping, nil); err != nil {
		return err
	}
	if err = this.swap(ctx, artifact.ArtifactType, staging, deleteList); err != nil {
		this.logf(ctx, "替换失败，开始回滚")
		if rollbackErr := this.rollback(ctx); rollbackErr != nil {
			this.report(ctx, UpgradeFailed, "替换失败且回滚失败："+rollbackErr.Error())
		}
		this.setState(PhaseFailed, nil)
		return err
	}

	// pending_restart：写状态 → 上报 installing → 优雅收尾 → 按重启模式退出/通知
	if err = this.setState(PhasePendingRestart, func(s *updateState) {
		s.RestartMode = this.resolveRestartMode(manifest)
	}); err != nil {
		return err
	}
	this.report(ctx, UpgradeInstalling, "替换完成，准备重启")
	if this.options.OnBeforeRestart != nil {
		this.options.OnBeforeRestart()
	}
	time.Sleep(this.options.RestartDelay)
	this.executeRestart(ctx, this.state.RestartMode)
	return nil
}

// fail - 流水线失败统一出口：上报 failed + 持久化 failed 终态
func (this *Updater) fail(ctx context.Context, err error, message string) error {

	this.report(ctx, UpgradeFailed, message+":"+err.Error())
	this.setState(PhaseFailed, nil)
	return err
}

// rollback - 回滚：从备份反向替换 + 上报（rolled_back/failed）
func (this *Updater) rollback(ctx context.Context) error {

	if err := this.setState(PhaseRollingBack, nil); err != nil {
		return err
	}
	if err := this.restoreBackup(); err != nil {
		this.report(ctx, UpgradeFailed, "回滚失败："+err.Error())
		this.setState(PhaseFailed, nil)
		return err
	}
	this.report(ctx, UpgradeRolledBack, "已回滚到上一版本")
	this.cleanupWork()
	this.setState(PhaseFailed, nil)
	return nil
}

// report - 上报升级状态（首次创建记录，回写 RecordNo 并持久化；失败不阻断流程）
func (this *Updater) report(ctx context.Context, status string, message string) {

	this.mu.RLock()
	report := UpgradeReport{
		RecordNo: this.state.RecordNo, FromVersion: this.state.FromVersion,
		TargetVersion: this.state.TargetVersion, ArtifactNo: this.state.ArtifactNo,
		Status: status, Message: message,
	}
	this.mu.RUnlock()

	recordNo, err := this.client.ReportUpgrade(ctx, report)
	if err != nil {
		return
	}
	this.mu.Lock()
	if this.state.RecordNo == "" && recordNo != "" {
		this.state.RecordNo = recordNo
		_ = this.saveState()
	}
	this.mu.Unlock()
}

// progress - 进度回调透传（阶段四细化下载/解包进度）
func (this *Updater) progress(phase string, done int64, total int64) {
	if this.options.OnProgress != nil {
		this.options.OnProgress(phase, done, total)
	}
}

// shouldAuto - 自动更新判定（设计 §5.8）：
// 清单 updatePolicy 随签名背书，force/auto 权威；旧清单无策略时以 SDK AutoUpdate 配置兜底
func (this *Updater) shouldAuto(info UpdateInfo) bool {

	if info.Manifest == nil {
		return false
	}
	policy := info.Manifest.Payload.UpdatePolicy
	if policy != nil {
		return policy.Force || policy.Auto
	}
	if this.options.AutoUpdate != nil {
		return *this.options.AutoUpdate
	}
	return false
}

// selectArtifact - 选包：增量优先（sourceVersion = 当前版本 精确匹配），未命中回退全量（设计 §4.3）
func (this *Updater) selectArtifact(manifest *Manifest, fromVersion string) (ManifestArtifact, error) {

	for _, artifact := range manifest.Payload.Artifacts {
		if artifact.OsArch != "" && artifact.OsArch != this.options.OSArch {
			continue
		}
		if artifact.ArtifactType == "incremental" && artifact.SourceVersion == fromVersion {
			return artifact, nil
		}
	}
	for _, artifact := range manifest.Payload.Artifacts {
		if artifact.OsArch != "" && artifact.OsArch != this.options.OSArch {
			continue
		}
		if artifact.ArtifactType == "" || artifact.ArtifactType == "full" {
			return artifact, nil
		}
	}
	return ManifestArtifact{}, errors.New("无匹配当前环境（osArch/来源版本）的发布物")
}

// currentVersion - 当前运行版本（来自运行面客户端配置）
func (this *Updater) currentVersion() string {
	return this.client.options.Version
}
