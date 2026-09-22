//go:build !windows

package updater

// detectWindowsService - 非 Windows 平台无 SCM 概念，恒 false
// （Linux 守护探测走 systemd INVOCATION_ID / Docker /.dockerenv，见 updater_restart.go）
func detectWindowsService() bool {
	return false
}
