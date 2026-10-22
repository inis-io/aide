package openpay

import (
	"errors"
	"fmt"
)

// ============================= 错误模型 - 开始 =============================

// 哨兵错误 - 商户用 errors.Is 判定错误类别，链路差异不外溢
var (
	// ErrInvalidRequest - 请求参数无效（本地校验或链路适配失败，未发出/未按预期发出请求）
	ErrInvalidRequest = errors.New("openpay: 请求参数无效")
	// ErrInvalidSign - 签名验证失败（本地验签不通过，或平台以 Unauthenticated 拒绝）
	ErrInvalidSign = errors.New("openpay: 签名验证失败")
	// ErrTransport - 传输层调用失败（网络错误、连接断开、RPC 状态非业务失败）
	ErrTransport = errors.New("openpay: 传输层调用失败")
)

// APIError - 平台业务错误（HTTP 与 gRPC 链路产出同一结构）
// 平台以信封 {code,msg} 表达业务结果：code==200 为成功，其余均为失败。
// 指针接收者方法对 nil 安全，便于错误链中直接调用 Error()。
type APIError struct {
	// Code - 业务码（200 以外均为失败，如 400 参数错误、403 停用、404 不存在、409 冲突）
	Code int
	// Msg - 平台返回的中文文案（无机器可读错误码体系，按文案或 Code 判定）
	Msg string
	// Raw - 原始响应体（HTTP 链路为完整 body；gRPC 链路为空）
	Raw []byte
}

// Error - 实现 error 接口（nil 接收者安全）
func (this *APIError) Error() string {

	if this == nil {
		return "openpay: <nil> APIError"
	}

	return fmt.Sprintf("openpay: 平台业务错误（code=%d）：%s", this.Code, this.Msg)
}

// ============================= 错误模型 - 结束 =============================
