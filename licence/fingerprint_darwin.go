//go:build darwin

package licence

import (
	"os/exec"
	"strings"
)

// machineFactors - macOS 指纹因子采集（IOPlatformUUID，失败静默留空降级）
func machineFactors() []factor {
	return []factor{
		{name: "machine-id", value: platformUUID()},
	}
}

// platformUUID - 通过 ioreg 读取 IOPlatformUUID（硬件绑定，重装系统不变）
func platformUUID() string {

	output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		if index := strings.LastIndex(line, "\""); index > 0 {
			line = line[:index]
			if start := strings.LastIndex(line, "\""); start >= 0 {
				return strings.TrimSpace(line[start+1:])
			}
		}
	}
	return ""
}
