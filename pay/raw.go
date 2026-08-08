package pay

import (
	"bytes"
	"regexp"
)

// RawCaptureMode - 原始报文捕获模式
type RawCaptureMode string

const (
	RawCaptureNone     RawCaptureMode = "none"
	RawCaptureRedacted RawCaptureMode = "redacted"
	RawCaptureFull     RawCaptureMode = "full"
)

// RawPayload - 受长度限制的原始网关报文
type RawPayload struct {
	// ContentType - 原始报文媒体类型
	ContentType string `json:"-"`
	// Body - 已按策略处理的报文内容
	Body []byte `json:"-"`
	// Truncated - 是否因长度上限发生截断
	Truncated bool `json:"-"`
}

// RawCapturePolicy - 原始报文捕获策略
type RawCapturePolicy struct {
	// Mode - 捕获模式
	Mode RawCaptureMode
	// MaxBytes - 最大捕获字节数
	MaxBytes int64
}

var sensitivePattern = regexp.MustCompile(`(?i)((?:"[^"]*(?:authorization|token|secret|private[_-]?key|api[_-]?v3[_-]?key|account|phone|mobile|email|card|identity|openid|logon|id[_-]?card)[^"]*"|(?:authorization|token|secret|private[_-]?key|api[_-]?v3[_-]?key|account|phone|mobile|email|card|identity|openid|logon|id[_-]?card))\s*[:=]\s*"?)[^"&\s,}]+`)

// CaptureRaw - 按策略捕获并可选脱敏原始报文
func CaptureRaw(policy RawCapturePolicy, contentType string, body []byte) *RawPayload {
	if policy.Mode == RawCaptureNone {
		return nil
	}
	limit := policy.MaxBytes
	if limit <= 0 {
		limit = 32 << 10
	}
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}
	body = bytes.Clone(body)
	if policy.Mode == RawCaptureRedacted {
		body = sensitivePattern.ReplaceAll(body, []byte("${1}"+redactedValue))
	}
	return &RawPayload{ContentType: contentType, Body: body, Truncated: truncated}
}
