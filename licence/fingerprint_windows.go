//go:build windows

package licence

import (
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// machineFactors - Windows 指纹因子采集（注册表 + CIM 查询，失败静默留空降级）
func machineFactors() []factor {
	return []factor{
		{name: "machine-id", value: machineGuid()},
		{name: "system-uuid", value: cimValue("Win32_ComputerSystemProduct", "UUID")},
		{name: "board-serial", value: cimValue("Win32_BaseBoard", "SerialNumber")},
	}
}

// machineGuid - 读取 HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid（安装期生成，重装才变化）
func machineGuid() string {

	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()
	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// cimValue - 通过 PowerShell CIM 查询硬件属性（如系统 UUID、主板序列号）
func cimValue(class string, property string) string {

	output, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-CimInstance "+class+" | Select-Object -ExpandProperty "+property+")").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
