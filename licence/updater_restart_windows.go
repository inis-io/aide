//go:build windows

package licence

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detectWindowsService - 判定当前进程是否由 Windows 服务 SCM 启动：
// 沿父进程链上溯（上限 16 层），命中 services.exe 或 svchost.exe（服务宿主）即视为守护环境。
func detectWindowsService() bool {

	pid := uint32(windows.GetCurrentProcessId())
	for depth := 0; depth < 16; depth++ {
		parent, name := parentProcessInfo(pid)
		if parent == 0 {
			return false
		}
		if strings.EqualFold(name, "services.exe") || strings.EqualFold(name, "svchost.exe") {
			return true
		}
		pid = parent
	}
	return false
}

// parentProcessInfo - 返回进程 PID 的父进程 PID 与可执行文件名（快照遍历）
func parentProcessInfo(pid uint32) (uint32, string) {

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, ""
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if entry.ProcessID == pid {
			name := windows.UTF16ToString(entry.ExeFile[:])
			return entry.ParentProcessID, name
		}
	}
	return 0, ""
}
