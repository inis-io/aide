// Package apis - API 商城 typed 方法包：runtime 包 Client 的唯一业务能力挂载点。
//
// 本包不 import runtime：typed 方法面向自声明的 Doer 窄接口编程，凭证、签名头族、
// HTTP/gRPC 协议选择、连接管理、校时与失败退避全部由宿主（runtime 包 Client）
// 经适配器注入（依赖方向 runtime → apis，编译期保证无环）。
//
// 已落地能力（阶段 4）：Invoke（通用兜底）/ IPLocate（typed 便捷）/ Usage（只读对账）。
// 三者的 HTTP 路径、gRPC full method 与业务码集合以同一目录外的权威契约为准——
// proto 见 `licence/proto/apis/v1/runtime.proto`，映射清单见同目录 protocol-matrix.yaml，
// 服务端映射表见 licen-hub docs/plan/apis/04 §2.4；本包只处理 JSON，不 import proto 与传输实现。
//
// 错误归一（唯一解析点，见 errors.go 的 parseEnvelope）：双协议下业务失败都被宿主还原为
// `{code, msg, detail}` 同一形态的信封，本包统一解析为 *apis.Error。典型用法：
//
//	result, receipt, err := lic.Apis.IPLocate(ctx, "203.0.113.10")
//	var apiErr *apis.Error
//	if errors.As(err, &apiErr) && apiErr.Code == apis.ErrorCodeQuotaExceeded {
//		// 额度耗尽：切本地缓存数据源，或按 apiErr.Detail["resetAt"] 安排的恢复时间
//	}
package apis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// ============================= 宿主调用能力与挂载点 =============================

// Doer - 宿主（licence 根 Client）注入的已认证调用能力：
// 签名头族、HTTP/gRPC 协议选择、连接管理、校时、失败退避全部由宿主完成。
type Doer interface {
	// Do - 以 HTTP 语义发起一次运行面调用（method + path 为路由 key，与根包传输层同风格），
	// 返回统一业务信封 {code,msg,data} 的原始 JSON 字节；
	// requestId 为调用级幂等键（宿主 HTTP 放 X-Request-Id 头、gRPC 挂 metadata x-request-id，
	// 不进签名 canonical）；空串表示本次调用不使用幂等键（只读调用，如 Usage）。
	// gRPC 模式下由宿主传输层把 proto 响应与错误映射为等价形态。
	Do(ctx context.Context, method string, path string, body []byte, requestId string) ([]byte, error)
}

// Client - API 商城 typed 方法挂载点（由 licence.New 构造，生命周期跟随根 Client）。
type Client struct {
	// doer - 宿主注入的已认证调用能力
	doer Doer
}

// New - 创建商城客户端（仅装配宿主调用能力，不发起网络请求）
func New(doer Doer) *Client {
	return &Client{doer: doer}
}

// ============================= 请求出口 =============================

// do - 统一调用出口：宿主调一次 → 按唯一解析点解信封（成功返回 data 原文，
// 业务失败返回 *apis.Error）。传输/网络错误原样返回，不伪装业务码。
func (this *Client) do(ctx context.Context, method string, path string, body []byte, requestId string) (json.RawMessage, error) {

	raw, err := this.doer.Do(ctx, method, path, body, requestId)
	if err != nil {
		return nil, err
	}
	return parseEnvelope(raw)
}

// ============================= 调用级幂等键 =============================

// resolveRequestID - 幂等键求解：显式传入的值优先（去首尾空白），全空则自动生成。
// 显式值最终由服务端校验（长度 ≤40；`renew:` / `sys:` 为系统保留前缀，占用即被拒绝）。
func resolveRequestID(candidates ...string) string {

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
