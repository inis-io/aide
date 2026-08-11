package pay

import (
	"net/http"
	"net/url"
	"time"
)

// NotifyRequest - 与 Web 框架无关的通知请求
type NotifyRequest struct {
	_ noUnkeyedLiterals
	// Kind - 通知种类
	Kind NotifyKind `json:"kind"`
	// Method - HTTP 方法
	Method string `json:"method"`
	// Headers - HTTP 请求头
	Headers http.Header `json:"-"`
	// Query - URL 查询参数
	Query url.Values `json:"-"`
	// Body - 原始 HTTP 请求体
	Body []byte `json:"-"`
}

// NotifyEvent - 完成验签后的标准通知事件
type NotifyEvent struct {
	// ID - 稳定的网关事件标识（仅用于排查与对账；幂等请使用 DedupeKey）
	ID string `json:"id"`
	// DedupeKey - 业务幂等去重键：业务单号（缺省退化为网关单号）+ "|" + 标准事件类型，由 Driver 统一派生
	DedupeKey string `json:"dedupeKey"`
	// Type - 标准事件类型
	Type EventType `json:"type"`
	// Provider - Provider 名称
	Provider string `json:"provider"`
	// Trade - 交易通知资源
	Trade *TradeEvent `json:"trade,omitempty"`
	// Refund - 退款通知资源
	Refund *RefundEvent `json:"refund,omitempty"`
	// Transfer - 转账通知资源
	Transfer *TransferEvent `json:"transfer,omitempty"`
	// OccurredAt - 网关事件发生时间
	OccurredAt time.Time `json:"occurredAt"`
	// VerifiedAt - Provider 完成验签时间
	VerifiedAt time.Time `json:"verifiedAt"`
	// VerificationKeyID - 验签密钥或证书标识
	VerificationKeyID string `json:"verificationKeyId"`
	// Raw - 按捕获策略保留的原始通知
	Raw *RawPayload `json:"-"`
}

// NotifyResponse - Provider 编码后的 HTTP 应答
type NotifyResponse struct {
	// StatusCode - HTTP 状态码
	StatusCode int
	// Header - HTTP 响应头
	Header http.Header
	// Body - HTTP 响应体
	Body []byte
}

// Valid - 判断通知事件是否恰好携带一种资源
func (this NotifyEvent) Valid() bool {
	count := 0
	if this.Trade != nil {
		count++
	}
	if this.Refund != nil {
		count++
	}
	if this.Transfer != nil {
		count++
	}
	return this.ID != "" && this.Provider != "" && !this.VerifiedAt.IsZero() && count == 1
}
