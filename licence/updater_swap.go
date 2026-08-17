package licence

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
)

// currentTarget - 当前替换目标（SelfBinary = 当前可执行文件；Directory = 目标目录）
func (this *Updater) currentTarget() string {

	if this.options.Mode == ApplyDirectory {
		return this.options.TargetPath
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// backupCurrent - 备份当前版本到 StorageDir/update/backup/<targetVersion>/（回滚来源）
func (this *Updater) backupCurrent() (string, error) {

	target := this.currentTarget()
	if target == "" {
		return "", errors.New("无法定位当前替换目标")
	}
	backupDir := filepath.Join(this.updateDir(), "backup", this.state.TargetVersion)
	_ = os.RemoveAll(backupDir)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		if err = copyDir(target, backupDir); err != nil {
			return "", err
		}
		return backupDir, nil
	}
	if err := copyFile(target, filepath.Join(backupDir, filepath.Base(target))); err != nil {
		return "", err
	}
	return backupDir, nil
}

// cleanupBackups - 滚动清理备份：保留最新 KeepBackups 份（按修改时间排序）
func (this *Updater) cleanupBackups() {

	backupRoot := filepath.Join(this.updateDir(), "backup")
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		return
	}
	keep := this.options.KeepBackups
	if keep < 1 {
		keep = 1
	}
	if len(entries) <= keep {
		return
	}
	// 旧的在前，新的在后
	sort.Slice(entries, func(i, j int) bool {
		left, _ := entries[i].Info()
		right, _ := entries[j].Info()
		if left == nil || right == nil {
			return entries[i].Name() < entries[j].Name()
		}
		return left.ModTime().Before(right.ModTime())
	})
	for _, entry := range entries[:len(entries)-keep] {
		_ = os.RemoveAll(filepath.Join(backupRoot, entry.Name()))
	}
}

// swap - 执行替换（自替换 / 目录级），失败即回滚，不留半成品
func (this *Updater) swap(ctx context.Context, artifactType string, staging string, deleteList []string) error {

	if this.options.Mode == ApplyDirectory {
		return this.swapDirectory(ctx, artifactType, staging, deleteList)
	}
	return this.swapSelfBinary(ctx, staging)
}

// swapSelfBinary - 自替换当前可执行文件：
//
//	Linux/macOS：新二进制写入同目录 .tmp → os.Rename 覆盖（替换 inode，运行中进程不受影响）
//	Windows：运行中 exe 被锁不可覆盖但可 rename：rename(exe, exe.old) → 新文件落位 → 重启
//
// 防降权：新文件权限继承旧文件 mode（Windows 忽略）。
func (this *Updater) swapSelfBinary(ctx context.Context, staging string) error {

	exe := this.currentTarget()
	if exe == "" {
		return errors.New("无法定位当前可执行文件")
	}
	source, err := findSingleFile(staging)
	if err != nil {
		return err
	}
	tmp := exe + ".newtmp"
	if err = copyFile(source, tmp); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(exe); err == nil {
			_ = os.Chmod(tmp, info.Mode())
		}
	}
	oldFile, err := swapBinaryFile(tmp, exe)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	this.state.OldFile = oldFile
	this.state.SwapTarget = exe
	return nil
}

// swapBinaryFile - 单文件替换核心（自替换场景）：
//
//	Linux/macOS：os.Rename 覆盖目标（替换 inode，运行中进程不受影响）
//	Windows：运行中 exe 被锁不可覆盖但可 rename：rename(target, target.old) → 新文件落位；
//	返回 .old 路径，由新进程启动时清理
func swapBinaryFile(source string, target string) (string, error) {

	if runtime.GOOS == "windows" {
		oldFile := target + ".old"
		_ = os.Remove(oldFile)
		if err := os.Rename(target, oldFile); err != nil {
			return "", errors.New("旧可执行文件改名失败：" + err.Error())
		}
		if err := os.Rename(source, target); err != nil {
			return "", err
		}
		return oldFile, nil
	}
	if err := os.Rename(source, target); err != nil {
		return "", err
	}
	return "", nil
}

// swapDirectory - 目录级替换：解到 TargetPath.new（全量包 = 空基底 + staging；
//
//	增量包 = 旧目录副本 + 变更覆盖 + delete.list）
//	→ rename 旧目录为 .old → rename 新目录就位（同分区原子）
func (this *Updater) swapDirectory(ctx context.Context, artifactType string, staging string, deleteList []string) error {

	target := this.options.TargetPath
	if target == "" {
		return errors.New("Directory 模式需配置 TargetPath")
	}
	if _, err := os.Stat(target); err != nil {
		return errors.New("目标目录不存在：" + target)
	}
	newDir := target + ".new"
	_ = os.RemoveAll(newDir)
	// 1. 基底：全量包 = 全新空目录（期望文件树即发布物）；增量包 = 旧目录副本（在其上应用变更与删除）
	if artifactType == "incremental" {
		if err := copyDir(target, newDir); err != nil {
			_ = os.RemoveAll(newDir)
			return err
		}
	} else if err := os.MkdirAll(newDir, 0o700); err != nil {
		_ = os.RemoveAll(newDir)
		return err
	}
	// 2. 覆盖 staging 变更文件
	if err := mergeDir(staging, newDir); err != nil {
		_ = os.RemoveAll(newDir)
		return err
	}
	// 3. 应用删除清单（相对 newDir 的文件树）
	for _, line := range deleteList {
		clean, err := safeArchivePath(line)
		if err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(newDir, clean))
	}
	// 4. 交换
	oldDir := target + ".old"
	_ = os.RemoveAll(oldDir)
	if err := os.Rename(target, oldDir); err != nil {
		_ = os.RemoveAll(newDir)
		return err
	}
	if err := os.Rename(newDir, target); err != nil {
		// 还原旧目录
		_ = os.Rename(oldDir, target)
		_ = os.RemoveAll(newDir)
		return err
	}
	this.state.SwapTarget = target
	return nil
}

// restoreBackup - 从备份还原（优先 .old 留存，其次备份目录），供自动回滚与手动 Rollback 使用
func (this *Updater) restoreBackup() error {

	if this.options.Mode == ApplyDirectory {
		target := this.options.TargetPath
		oldDir := target + ".old"
		if dirExists(oldDir) {
			_ = os.RemoveAll(target)
			return os.Rename(oldDir, target)
		}
		if this.state.BackupDir != "" && dirExists(this.state.BackupDir) {
			_ = os.RemoveAll(target)
			return copyDir(this.state.BackupDir, target)
		}
		return errors.New("无可回滚的旧版本")
	}

	exe := this.currentTarget()
	if exe == "" {
		return errors.New("无法定位当前可执行文件")
	}
	// Windows .old 方案：旧 exe 以 .old 留存，rename 还原即可
	if this.state.OldFile != "" && fileExists(this.state.OldFile) {
		discard := exe + ".bad"
		_ = os.Remove(discard)
		if err := os.Rename(exe, discard); err == nil {
			_ = os.RemoveAll(discard)
			return os.Rename(this.state.OldFile, exe)
		}
		// 当前 exe 移不走（被占用）：尝试直接以备份覆盖
	}
	if this.state.BackupDir != "" {
		backed := filepath.Join(this.state.BackupDir, filepath.Base(exe))
		if fileExists(backed) {
			return copyFileReplace(backed, exe)
		}
	}
	return errors.New("无可回滚的旧版本")
}

// cleanupOldFiles - 清理替换残留（Windows *.old / 目录 .old）；清理失败不阻塞，下轮再清
func (this *Updater) cleanupOldFiles() {

	if this.options.Mode == ApplyDirectory {
		_ = os.RemoveAll(this.options.TargetPath + ".old")
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

// cleanupWork - 清理解包/下载工作区（committed / failed / 中断恢复时调用）
func (this *Updater) cleanupWork() {

	_ = os.RemoveAll(filepath.Join(this.updateDir(), "work"))
}

// findSingleFile - 解包目录中查找唯一常规文件（SelfBinary 场景的发布物主体）
func findSingleFile(dir string) (string, error) {

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	if len(files) != 1 {
		return "", errors.New("解包目录须包含单一可执行文件，实际 " + strconv.Itoa(len(files)) + " 个")
	}
	return filepath.Join(dir, files[0]), nil
}

// copyDir - 递归复制目录（保留目录结构，权限 0700）
func copyDir(src string, dest string) error {

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		return copyFile(path, target)
	})
}

// mergeDir - 将 src 目录树逐文件覆盖写入 dest（增量包变更应用）
func mergeDir(src string, dest string) error {

	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dest, rel))
	})
}

// copyFileReplace - 以文件覆盖替换目标（临时文件 + 替换；Windows 覆盖运行中文件由调用方兜底）
func copyFileReplace(src string, dest string) error {

	tmp := dest + ".newtmp"
	if err := copyFile(src, tmp); err != nil {
		return err
	}
	// POSIX rename 可覆盖已存在文件；Windows 需先删除目标
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// dirExists / fileExists - 路径存在性判断
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
