package licence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// UpdatePhase - 更新执行阶段（持久化状态机，断电/崩溃可恢复）。
// 迁移：idle → checking → downloading → unpacking → backing_up → swapping
//
//	→ pending_restart ──(进程退出/新进程拉起)──► verifying → committed（清理备份、上报 success）
//	└─ 失败任一环节 → rolling_back → failed（上报 failed/rolled_back）
type UpdatePhase string

const (
	// PhaseIdle - 空闲（无进行中更新）
	PhaseIdle UpdatePhase = "idle"
	// PhaseChecking - 检查中（CheckUpdate + VerifyManifest）
	PhaseChecking UpdatePhase = "checking"
	// PhaseDownloading - 下载中（DownloadArtifact：验签 + SHA-256 + 原子落盘）
	PhaseDownloading UpdatePhase = "downloading"
	// PhaseUnpacking - 解包中（zip / tar.gz / 裸二进制；增量包按 delete.list 应用）
	PhaseUnpacking UpdatePhase = "unpacking"
	// PhaseBackingUp - 备份当前版本中
	PhaseBackingUp UpdatePhase = "backing_up"
	// PhaseSwapping - 替换中（自替换 / 目录级替换）
	PhaseSwapping UpdatePhase = "swapping"
	// PhasePendingRestart - 待重启（旧进程已完成替换，等待退出并由守护/新进程接管）
	PhasePendingRestart UpdatePhase = "pending_restart"
	// PhaseVerifying - 新进程健康确认中（Start 恢复后进入，HealthTimeout 内 Commit 即过）
	PhaseVerifying UpdatePhase = "verifying"
	// PhaseCommitted - 已确认成功（Commit 后清理备份与工作区，随即回 idle）
	PhaseCommitted UpdatePhase = "committed"
	// PhaseRollingBack - 回滚中（从备份反向替换）
	PhaseRollingBack UpdatePhase = "rolling_back"
	// PhaseFailed - 失败终态
	PhaseFailed UpdatePhase = "failed"
)

// updateState - 持久化更新状态（StorageDir/update/state.json，明文：
// 不含令牌/私钥等敏感数据，仅用于断电恢复与崩溃后的版本一致性判定）
type updateState struct {
	// Phase - 当前阶段
	Phase UpdatePhase `json:"phase"`
	// FromVersion - 来源版本（当前运行版本）
	FromVersion string `json:"fromVersion,omitempty"`
	// TargetVersion - 目标版本
	TargetVersion string `json:"targetVersion,omitempty"`
	// ArtifactNo - 选中的发布物编号
	ArtifactNo string `json:"artifactNo,omitempty"`
	// RecordNo - 平台升级记录编号（首次上报创建，后续推进复用）
	RecordNo string `json:"recordNo,omitempty"`
	// RestartMode - 已解析的重启模式（pending_restart 后重启时使用）
	RestartMode string `json:"restartMode,omitempty"`
	// BackupDir - 当前版本备份目录（回滚来源）
	BackupDir string `json:"backupDir,omitempty"`
	// DownloadPath - 下载的发布物落盘路径
	DownloadPath string `json:"downloadPath,omitempty"`
	// UnpackDir - 解包暂存目录（staging）
	UnpackDir string `json:"unpackDir,omitempty"`
	// SwapTarget - 新文件/新目录最终落位路径（swapping 中断时据此判定替换是否完成）
	SwapTarget string `json:"swapTarget,omitempty"`
	// OldFile - Windows 旧可执行文件改名路径（新进程启动时清理 *.old）
	OldFile string `json:"oldFile,omitempty"`
	// UpdatedAt - 最近一次状态写入（毫秒）
	UpdatedAt int64 `json:"updatedAt"`
	// VerifyingUntil - verifying 健康确认截止（毫秒，超过即判失败自动回滚）
	VerifyingUntil int64 `json:"verifyingUntil,omitempty"`
}

// updateDir - 更新工作根目录（StorageDir/update/）
func (this *Updater) updateDir() string {
	return filepath.Join(this.client.options.StorageDir, "update")
}

// statePath - 状态文件路径（StorageDir/update/state.json）
func (this *Updater) statePath() string {
	return filepath.Join(this.updateDir(), "state.json")
}

// loadState - 读取持久化状态（文件不存在返回 false, nil）
func (this *Updater) loadState() (updateState, bool, error) {

	raw, err := os.ReadFile(this.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return updateState{}, false, nil
		}
		return updateState{}, false, err
	}
	var state updateState
	if err = json.Unmarshal(raw, &state); err != nil {
		// 状态文件损坏：按无状态处理并清理，避免卡死启动
		_ = os.Remove(this.statePath())
		return updateState{}, false, nil
	}
	return state, true, nil
}

// saveState - 原子写入状态文件（临时文件 + 重命名，权限 0600）
func (this *Updater) saveState() error {

	if err := os.MkdirAll(this.updateDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(this.state)
	if err != nil {
		return err
	}
	tmp := this.statePath() + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, this.statePath())
}

// setState - 更新内存状态并持久化（阶段迁移唯一入口；verifyingUntil 可选设置）
func (this *Updater) setState(phase UpdatePhase, with func(*updateState)) error {

	this.mu.Lock()
	defer this.mu.Unlock()
	this.state.Phase = phase
	this.state.UpdatedAt = time.Now().UnixMilli()
	if with != nil {
		with(&this.state)
	}
	return this.saveState()
}

// nowMs - 当前毫秒（本地时间，不涉服务端校时）
func nowMs() int64 {
	return time.Now().UnixMilli()
}
