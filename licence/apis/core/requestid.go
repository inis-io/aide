package core

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// ============================= 调用级幂等键 =============================

// ResolveRequestID - 幂等键求解：显式传入的值优先（去首尾空白），全空则自动生成。
// 显式值最终由服务端校验（长度 ≤40；`renew:` / `sys:` 为系统保留前缀，占用即被拒绝）。
func ResolveRequestID(candidates ...string) string {

	for _, candidate := range candidates {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return generatedRequestID()
}

// generatedRequestID - 生成调用级幂等键：`req_` + 32 位无横线 UUID v4
// （与平台兜底 apisRuntimeRequestIDPrefix + uuid.NewString() 去横线同构，只依赖标准库）。
// 熵源不可用时返回空串：宿主不下发幂等键，由服务端兜底生成后再经 Receipt.RequestId 回显。
func generatedRequestID() string {

	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return ""
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40 // 版本位：v4
	buffer[8] = (buffer[8] & 0x3f) | 0x80 // 变体位：RFC 4122
	return "req_" + hex.EncodeToString(buffer[:])
}
