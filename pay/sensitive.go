package pay

import (
	"context"
	"encoding/json"
)

const redactedValue = "[REDACTED]"

// SecretRef - 由业务 SecretResolver 解释的敏感值引用
type SecretRef string

// SensitiveString - 默认不可打印和序列化的敏感字符串
type SensitiveString struct{ value string }

// NewSensitiveString - 创建敏感字符串
func NewSensitiveString(value string) SensitiveString { return SensitiveString{value: value} }

// Reveal - 显式读取敏感字符串明文
func (this SensitiveString) Reveal() string { return this.value }

// String - 返回固定脱敏文本
func (this SensitiveString) String() string { return redactedValue }

// GoString - 返回固定脱敏文本
func (this SensitiveString) GoString() string { return redactedValue }

// MarshalJSON - JSON 编码固定输出脱敏文本
func (this SensitiveString) MarshalJSON() ([]byte, error) { return json.Marshal(redactedValue) }

// UnmarshalJSON - 从受信配置源读取敏感明文
func (this *SensitiveString) UnmarshalJSON(body []byte) error {
	return json.Unmarshal(body, &this.value)
}

// SecretResolver - 敏感值引用解析器
type SecretResolver interface {
	ResolveSecret(ctx context.Context, ref SecretRef) ([]byte, error)
}
