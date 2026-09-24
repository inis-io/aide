// Package core - API 商城 typed 方法包的共享核心件。
//
// 本包承载 apis 根包与各能力子包（iplocate/mailsend……）共同依赖的共享件：
// Doer 窄接口、Error + 业务码常量 + ErrNotActivated、Receipt、
// ParseEnvelope（唯一信封解析点）与 ResolveRequestID（调用级幂等键助手）。
//
// 下沉原因：根包要 import 能力子包做挂载，能力子包若 import 根包拿共享件即成环，
// 故共享件全部收敛在本包；core 不 import 任何兄弟包，只依赖标准库。
package core

import "context"

// ============================= 宿主调用能力 =============================

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
