// Package apis - API 商城 typed 方法包：licence 根包 Client 的唯一业务能力挂载点。
//
// 本包不 import 根包：typed 方法面向自声明的 Doer 窄接口编程，凭证、签名头族、
// HTTP/gRPC 协议选择、连接管理、校时与失败退避全部由宿主（licence 根 Client）
// 经适配器注入（依赖方向 root → apis，编译期保证无环）。
//
// 当前为骨架阶段：仅确立挂载机制（Client{doer} + New）与未激活闸门错误；
// IPLocate/Invoke/Usage 等 typed 方法随 API 商城后端就绪后落地
// （见 licen-hub docs/plan/licence-sdk-reorg/README.md §5 与 docs/plan/apis/06-SDK设计.md）。
package apis

import "context"

// Doer - 宿主（licence 根 Client）注入的已认证调用能力：
// 签名头族、HTTP/gRPC 协议选择、连接管理、校时、失败退避全部由宿主完成。
type Doer interface {
	// Do - 以 HTTP 语义发起一次运行面调用（method + path 为路由 key，与根包传输层同风格），
	// 返回统一业务信封的原始 JSON 字节；
	// gRPC 模式下由宿主传输层把 proto 响应与错误映射为等价形态。
	Do(ctx context.Context, method string, path string, body []byte) ([]byte, error)
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
