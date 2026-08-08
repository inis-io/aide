// Package pay 提供框架无关的支付能力协议、工厂、统一 Driver 与多网关实例池。
package pay

import (
	"context"
	"encoding/json"
)

type noUnkeyedLiterals struct{}

// Provider - 支付提供者基础接口
type Provider interface {
	Name() string
	Capabilities() []Capability
	Close() error
}

// TradeCreator - 创建交易能力
type TradeCreator interface {
	CreateTrade(context.Context, TradeCreateRequest) (TradeResult, error)
}

// TradeQuerier - 查询交易能力
type TradeQuerier interface {
	QueryTrade(context.Context, TradeQueryRequest) (TradeResult, error)
}

// TradeCapturer - 捕获交易能力
type TradeCapturer interface {
	CaptureTrade(context.Context, TradeCaptureRequest) (TradeResult, error)
}

// TradeCloser - 关闭交易能力
type TradeCloser interface {
	CloseTrade(context.Context, TradeCloseRequest) error
}

// Refunder - 发起退款能力
type Refunder interface {
	Refund(context.Context, RefundRequest) (RefundResult, error)
}

// RefundQuerier - 查询退款能力
type RefundQuerier interface {
	QueryRefund(context.Context, RefundQueryRequest) (RefundResult, error)
}

// Transferer - 发起转账能力
type Transferer interface {
	Transfer(context.Context, TransferRequest) (TransferResult, error)
}

// TransferQuerier - 查询转账能力
type TransferQuerier interface {
	QueryTransfer(context.Context, TransferQueryRequest) (TransferResult, error)
}

// NotifyParser - 验签解析通知及编码 ACK 的能力
type NotifyParser interface {
	ParseNotify(context.Context, NotifyRequest) (NotifyEvent, error)
	NotifyResponse(NotifyKind, NotifyDecision) NotifyResponse
}

// BillRequest - 账单请求预留结构
type BillRequest struct {
	_ noUnkeyedLiterals
	// Date - 账单日期
	Date string `json:"date"`
	// Extensions - Provider 专属扩展
	Extensions Extensions `json:"extensions"`
}

// BillResult - 账单结果预留结构
type BillResult struct {
	// DownloadURL - 账单下载地址
	DownloadURL string `json:"downloadUrl"`
	// Raw - 按捕获策略保留的原始响应
	Raw *RawPayload `json:"-"`
}

// Biller - 账单下载预留能力
type Biller interface {
	FetchBill(context.Context, BillRequest) (BillResult, error)
}

// ConfigInput - 强类型配置与动态 JSON 配置二选一
type ConfigInput struct {
	// Value - 强类型 Provider 配置
	Value any
	// Raw - 动态 JSON Provider 配置
	Raw json.RawMessage
}

// Factory - Provider 唯一构造入口
type Factory func(context.Context, ConfigInput, OpenOptions) (Provider, error)
