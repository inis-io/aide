//go:build linux

package licence

import (
	"os"
	"strings"
)

// machineFactors - Linux 指纹因子采集（文件读取，权限不足时静默留空降级）
func machineFactors() []factor {
	return []factor{
		{name: "machine-id", value: readFirstLine("/etc/machine-id", "/var/lib/dbus/machine-id")},
		{name: "system-uuid", value: readFirstLine("/sys/class/dmi/id/product_uuid")},
		{name: "board-serial", value: readFirstLine("/sys/class/dmi/id/board_serial")},
	}
}

// readFirstLine - 依次读取候选文件的首行内容（全部失败返回空串）
func readFirstLine(paths ...string) string {

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		line, _, _ := strings.Cut(string(content), "\n")
		if text := strings.TrimSpace(line); text != "" {
			return text
		}
	}
	return ""
}
