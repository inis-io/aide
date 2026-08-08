package pay

import (
	"errors"
	"fmt"
)

var (
	ErrProviderNotFound         = errors.New("pay: Provider 未注册")
	ErrInvalidProvider          = errors.New("pay: Provider 能力声明无效")
	ErrUnsupportedCapability    = errors.New("pay: Provider 不支持该能力")
	ErrInvalidConfig            = errors.New("pay: Provider 配置无效")
	ErrInvalidRequest           = errors.New("pay: 支付请求无效")
	ErrDuplicateProvider        = errors.New("pay: Provider 已注册")
	ErrIdempotencyConflict      = errors.New("pay: 幂等请求参数冲突")
	ErrUnsupportedConfigVersion = errors.New("pay: Provider 配置版本不受支持")
	ErrVerifyFailed             = errors.New("pay: 通知验签失败")
	ErrGatewayRejected          = errors.New("pay: 网关拒绝请求")
	ErrGatewayUnavailable       = errors.New("pay: 网关暂时不可用")
	ErrPoolClosed               = errors.New("pay: Provider Pool 已关闭")
)

// GatewayError - 标准网关错误
type GatewayError struct {
	// Provider - Provider 名称
	Provider string
	// Operation - 失败操作
	Operation string
	// Code - 网关原始错误码
	Code string
	// Message - 仅供结构化处理的网关消息，不进入 Error 文本
	Message string
	// Retryable - 技术故障是否可能恢复
	Retryable bool
	// Outcome - 本次资金结果的确定性
	Outcome Outcome
	// Cause - 标准错误分类
	Cause error
}

// Error - 返回不包含 Raw 与敏感配置的错误文本
func (this *GatewayError) Error() string {
	if this == nil {
		return "<nil>"
	}
	if this.Code != "" {
		return fmt.Sprintf("pay: %s %s 失败（%s）", this.Provider, this.Operation, this.Code)
	}
	return fmt.Sprintf("pay: %s %s 失败", this.Provider, this.Operation)
}

// Unwrap - 返回底层分类错误
func (this *GatewayError) Unwrap() error {
	if this == nil {
		return nil
	}
	return this.Cause
}
