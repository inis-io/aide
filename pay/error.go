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

// Reason - 网关错误的标准分类；映射缺失时为空串，业务不得依赖空值之外的取值集合收敛
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonOrderNotFound    Reason = "order-not-found"   // 订单/退款/转账单不存在
	ReasonAmountMismatch   Reason = "amount-mismatch"   // 金额与原单不一致
	ReasonDuplicateRequest Reason = "duplicate-request" // 幂等冲突（网关侧判定重复提交且参数不一致）
	ReasonRateLimited      Reason = "rate-limited"      // 网关频率限制
	ReasonInvalidRequest   Reason = "invalid-request"   // 参数或状态被网关判定非法
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
	// Reason - 标准错误分类，由 Provider 按错误码映射表填写
	Reason Reason
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

// ReasonOf - 提取错误的标准分类，非 GatewayError 或未映射返回 ReasonNone
func ReasonOf(err error) Reason {
	var gatewayError *GatewayError
	if errors.As(err, &gatewayError) {
		return gatewayError.Reason
	}
	return ReasonNone
}
