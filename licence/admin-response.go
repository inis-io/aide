package licence

import (
	"encoding/json"
	"fmt"
)

// Response - 管理面统一响应信封
// 平台 facade.Comm.Json 输出：HTTP 状态码恒为 200，业务结果以 code 为准（200=成功，其余为业务错误）。
type Response struct {
	// Code - 业务状态码（200=成功；400 参数错误；401 未登录/登录失效；403 无权限；404 不存在；409 状态冲突）
	Code int `json:"code"`
	// Msg - 提示文案（平台已做 i18n）
	Msg string `json:"msg"`
	// Data - 业务数据（无数据时为 null）
	Data json.RawMessage `json:"data"`
}

// APIError - 业务错误（信封 code != 200）
// 与 HTTPError 区分：APIError 表示请求已到达平台并被业务逻辑拒绝。
type APIError struct {
	// Code - 平台业务状态码
	Code int `json:"code"`
	// Msg - 平台返回的提示文案
	Msg string `json:"msg"`
	// Data - 业务错误附带的数据（如登录接口的 require2FA 标记），无附加数据时为 nil
	Data json.RawMessage `json:"data,omitempty"`
	// Require2FA - 登录被拒且平台要求补交 2FA TOTP 验证码时为 true（此时 Code 为 400）
	Require2FA bool `json:"-"`
	// Cause - gRPC status 等底层原因；HTTP 业务错误通常为空。
	Cause error `json:"-"`
}

// Unwrap 保留传输层 cause，便于 errors.Is/errors.As 继续判断 gRPC status。
func (this *APIError) Unwrap() error { return this.Cause }

// Error - 错误文案
func (this *APIError) Error() string {
	return fmt.Sprintf("licence: 平台业务错误（code=%d）：%s", this.Code, this.Msg)
}

// HTTPError - HTTP 传输层错误（响应状态码非 200）
// 平台正常业务响应恒为 200；出现非 200 通常是网关、中间件或服务异常。
type HTTPError struct {
	// StatusCode - HTTP 状态码
	StatusCode int `json:"statusCode"`
	// Body - 响应原文（截断保存，供排查）
	Body string `json:"body"`
}

// Error - 错误文案
func (this *HTTPError) Error() string {
	return fmt.Sprintf("licence: HTTP 传输错误（status=%d）：%s", this.StatusCode, this.Body)
}

// Page - 分页数据结构（find 类接口的 data 内统一为 {data,count,page}）
type Page[T any] struct {
	// Data - 当前页数据行
	Data []T `json:"data"`
	// Count - 符合条件的总行数
	Count int64 `json:"count"`
	// Page - 当前页码
	Page int `json:"page"`
}

// decodeData - 解包信封 data 字段到目标对象（out 为 nil 或 data 为空/null 时直接返回）
func decodeData(data json.RawMessage, out any) error {

	if out == nil || len(data) == 0 || string(data) == "null" {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("licence: 响应数据解析失败: %w", err)
	}
	return nil
}
