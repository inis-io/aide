package licence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// FingerprintProvider - 自定义实例指纹提供者
// 容器/云主机等硬件因子不稳定的场景，由业务注入稳定指纹源；
// 返回值为指纹原文，SDK 统一加盐哈希后使用。
type FingerprintProvider func() (string, error)

// factor - 指纹因子（name 固定顺序参与哈希，保证同机稳定、异机不同）
type factor struct {
	name  string
	value string
}

// FingerprintHash - 生成实例指纹哈希（契约 §7）
// 多因子组合（机器 ID + 系统 UUID + 主板序列号）加盐 SHA-256，禁止单用 IP/MAC；
// 单因子不可得的平台按既定降级组合（有几个用几个，全不可得则报错）。
// override 非空时直接使用（视为业务已完成采集与哈希）；provider 优先于自动采集。
/**
 * @param salt string - 项目盐（与实例登记时使用的盐一致）
 * @param override string - 显式指纹哈希（可选，直接返回）
 * @param provider FingerprintProvider - 自定义指纹提供者（可选）
 * @return string - 64 位 hex 指纹哈希
 */
func FingerprintHash(salt string, override string, provider FingerprintProvider) (string, error) {

	if override != "" {
		return override, nil
	}
	if provider != nil {
		raw, err := provider()
		if err != nil {
			return "", err
		}
		return hashFactors(salt, []factor{{name: "provider", value: raw}}), nil
	}

	factors := machineFactors()
	available := make([]factor, 0, len(factors))
	for _, item := range factors {
		if item.value != "" {
			available = append(available, item)
		}
	}
	if len(available) == 0 {
		return "", errors.New("实例指纹采集失败：机器 ID/系统 UUID/主板序列号均不可得，请注入 FingerprintProvider")
	}
	return hashFactors(salt, available), nil
}

// hashFactors - 因子组合加盐哈希：sha256(salt|name=value|name=value...) 的 hex
func hashFactors(salt string, factors []factor) string {

	var builder strings.Builder
	builder.WriteString(salt)
	for _, item := range factors {
		builder.WriteString("|")
		builder.WriteString(item.name)
		builder.WriteString("=")
		builder.WriteString(item.value)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
