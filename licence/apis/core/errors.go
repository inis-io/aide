package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ============================= 未激活闸门 =============================

// ErrNotActivated - 未激活闸门：根 Client 未完成激活（无 activation token）
// 或授权状态为 EXPIRED / REVOKED / SUSPENDED 时，lic.Apis.* 直接返回本错误（不发请求）；
// 服务端的凭证校验仍是最终边界。
var ErrNotActivated = errors.New("apis: 许可证未激活或授权状态不可用")

// ============================= 业务码（唯一事实源：licen-hub app/service/apis 的 ServiceError.Code） =============================

// 业务码常量：与平台侧 ServiceError.Code 逐字一致（proto/apis/v1/runtime.proto 服务注释的 17 码全集），
// gRPC 侧服务端把业务码逐字挂进 errdetails.ErrorInfo.Reason，故双协议下 Error.Code 恒为同一取值。
const (
	// ErrorCodeInvalidArgument - 请求参数或业务前置校验失败（HTTP 400 / InvalidArgument）
	ErrorCodeInvalidArgument = "INVALID_ARGUMENT"
	// ErrorCodeUnauthorized - 调用者身份无效（HTTP 401 / Unauthenticated）
	ErrorCodeUnauthorized = "UNAUTHORIZED"
	// ErrorCodeForbidden - 数据范围/角色边界拒绝（HTTP 403 / PermissionDenied）
	ErrorCodeForbidden = "FORBIDDEN"
	// ErrorCodeNoEntitlement - 无订阅、无免费额度且无按量定价（HTTP 403 / PermissionDenied）
	ErrorCodeNoEntitlement = "NO_ENTITLEMENT"
	// ErrorCodeQuotaExceeded - 订阅额度耗尽（HTTP 403 / ResourceExhausted；detail 带 scope/resetAt）
	ErrorCodeQuotaExceeded = "QUOTA_EXCEEDED"
	// ErrorCodeInsufficientBalance - 余额不足（HTTP 402 / ResourceExhausted，充值后恢复）
	ErrorCodeInsufficientBalance = "INSUFFICIENT_BALANCE"
	// ErrorCodeSpendLimitExceeded - 月度消费限额（HTTP 429 / ResourceExhausted；detail 带 limit/monthSpent/resetAt）
	ErrorCodeSpendLimitExceeded = "SPEND_LIMIT_EXCEEDED"
	// ErrorCodeAccountFrozen - 账户状态被冻结（HTTP 403 / FailedPrecondition，解冻后恢复）
	ErrorCodeAccountFrozen = "ACCOUNT_FROZEN"
	// ErrorCodeConcurrencyLimited - 并发闸门（HTTP 429 / ResourceExhausted；detail 带 retryAfterMs）
	ErrorCodeConcurrencyLimited = "CONCURRENCY_LIMITED"
	// ErrorCodeRateLimited - 速率闸门（HTTP 429 / ResourceExhausted；detail 带 retryAfterMs）
	ErrorCodeRateLimited = "RATE_LIMITED"
	// ErrorCodeUpstreamError - 上游能力失败（HTTP 502 / Internal，对外不泄露细节）
	ErrorCodeUpstreamError = "UPSTREAM_ERROR"
	// ErrorCodeIPNotFound - IP 归属地空结果（HTTP 404 / NotFound）
	ErrorCodeIPNotFound = "IP_NOT_FOUND"
	// ErrorCodeCapabilityNotFound - 能力未注册或无在售产品（HTTP 404 / NotFound）
	ErrorCodeCapabilityNotFound = "CAPABILITY_NOT_FOUND"
	// ErrorCodeNotFound - 目标资源不存在；也是运行面认证失败的唯一外显码（模糊 404，防爆破）
	ErrorCodeNotFound = "NOT_FOUND"
	// ErrorCodeConflict - 乐观锁/唯一键冲突（HTTP 409 / FailedPrecondition，刷新后重试）
	ErrorCodeConflict = "CONFLICT"
	// ErrorCodeInvalidState - 状态机不允许的流转（HTTP 409 / FailedPrecondition）
	ErrorCodeInvalidState = "INVALID_STATE"
	// ErrorCodeInternal - 服务端故障；未知/未登记业务码的回落（HTTP 500 / Internal）
	ErrorCodeInternal = "INTERNAL_ERROR"
)

// HTTPStatusByCode - 业务码 → HTTP 等价状态码（licen-hub API 商城 04 文档 §2.4 映射表，
// 与服务端 ServiceError.HTTPStatus 同源）。未登记业务码一律 500（服务端同口径：回落 INTERNAL_ERROR）。
// 供 Error.HTTPStatus 推导与 gRPC 失败信封合成共用，保证双协议看到同一取值。
func HTTPStatusByCode(code string) int {

	switch code {
	case ErrorCodeInvalidArgument:
		return http.StatusBadRequest
	case ErrorCodeUnauthorized:
		return http.StatusUnauthorized
	case ErrorCodeInsufficientBalance:
		return http.StatusPaymentRequired
	case ErrorCodeForbidden, ErrorCodeNoEntitlement, ErrorCodeQuotaExceeded, ErrorCodeAccountFrozen:
		return http.StatusForbidden
	case ErrorCodeIPNotFound, ErrorCodeCapabilityNotFound, ErrorCodeNotFound:
		return http.StatusNotFound
	case ErrorCodeConflict, ErrorCodeInvalidState:
		return http.StatusConflict
	case ErrorCodeRateLimited, ErrorCodeConcurrencyLimited, ErrorCodeSpendLimitExceeded:
		return http.StatusTooManyRequests
	case ErrorCodeUpstreamError:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// ============================= typed 业务错误 =============================

// Error - 运行面业务错误（双协议同一形态，HTTP 与 gRPC 的失败信封在宿主侧已归一）。
// 与 ErrNotActivated 的关系：ErrNotActivated 是本地闸门（未发请求），本类型是服务端拒绝。
type Error struct {
	// Code - 业务码（取值见 ErrorCode* 常量，与平台 ServiceError.Code 逐字一致）
	Code string
	// Message - 平台提示文本（HTTP 信封 msg / gRPC status message）
	Message string
	// Detail - 结构化闸门明细，键名与 HTTP 信封 detail 一致（retryAfterMs/scope/resetAt/limit/monthSpent 等）。
	// 类型随协议：HTTP 保持 JSON 原始类型，gRPC 侧服务端把明细扁平化为字符串（04 §2.4）
	Detail map[string]any
	// HTTPStatus - HTTP 等价状态码（由业务码按 04 §2.4 契约表推导，见 HTTPStatusByCode）；
	// 供客户按 HTTP 语义归类处置（401 重登、429 退避……），非线上原始状态码
	HTTPStatus int
}

// Error - 实现 error 接口（nil 接收者安全，避免日志格式化时 panic）
func (this *Error) Error() string {

	if this == nil {
		return ""
	}
	return "apis: 调用被拒（" + this.Code + "）：" + this.Message
}

// ============================= 业务信封解析（唯一解析点） =============================

// envelope - 运行面业务信封：成功为 `{code:0, msg:"ok", data:{…}}`，
// 失败为 `{code:"业务码", msg:"提示", detail:{…}}`（04 §2.3）。
type envelope struct {
	// Code - 数字 0 = 成功；字符串 = 业务码
	Code json.RawMessage `json:"code"`
	// Msg - 提示文本
	Msg string `json:"msg"`
	// Data - 成功载荷原文（result/receipt 或 records/count/page）
	Data json.RawMessage `json:"data"`
	// Detail - 失败明细（可选）
	Detail map[string]any `json:"detail"`
}

// ParseEnvelope - 双协议统一的信封解析点：成功返回 data 原文，
// 业务失败（code 为字符串业务码）返回 *Error（errors.As 断言）。
// 信封本身不可解析（既非成功也非业务失败，如网关返回 HTML 错误页）按协议错误返回，
// 不伪装成业务码。
func ParseEnvelope(raw []byte) (json.RawMessage, error) {

	var body envelope
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("apis: 响应信封解析失败：%w", err)
	}
	code := strings.TrimSpace(string(body.Code))
	// 成功：code 为数字 0（缺省/null 同义，兼容无 code 字段的极简成功响应）
	if code == "" || code == "null" || code == "0" {
		return body.Data, nil
	}
	var businessCode string
	if err := json.Unmarshal(body.Code, &businessCode); err != nil || businessCode == "" {
		return nil, fmt.Errorf("apis: 响应信封 code 形态非法（成功为 0，失败为字符串业务码）：%s", code)
	}
	return nil, &Error{
		Code: businessCode, Message: body.Msg, Detail: body.Detail,
		HTTPStatus: HTTPStatusByCode(businessCode),
	}
}
