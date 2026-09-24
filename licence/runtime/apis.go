package runtime

import (
	"context"

	"github.com/inis-io/aide/licence/apis"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// 本文件承载 API 商城 typed 方法包（apis 子包）在根 Client 上的挂载：
// Client.Apis 字段在 New 时构造（与管理面 admin 子包挂资源组字段同一风格），
// apisDoer 适配器把根 Client 的统一请求出口 doRequest（withSign=true，
// 自动携带 X-License-* 签名头族 / gRPC 同名 metadata）适配为 apis.Doer 窄接口。
// 依赖方向：runtime → apis（apis 不 import runtime，编译期保证无环）。

// apisRequestIDHeader - 调用级幂等键的 HTTP 头名（设计 04 §2.2 头族映射表；
// gRPC 侧为同名小写 metadata，见 LicenceProtocol.MetadataRequestID）。
const apisRequestIDHeader = "X-Request-Id"

// apisRequestIDKey - 调用级幂等键的 context 键类型。
// apisDoer 注入、传输层读取：幂等键既不进请求体（契约无该字段），也不进签名 canonical，
// 故经 context 传递，避免改动 runtimeTransport.RoundTrip 的既有签名与全部既有调用点。
type apisRequestIDKey struct{}

// withApisRequestID - 把调用级幂等键写入 context（空键不写入，保持无幂等键语义）
func withApisRequestID(ctx context.Context, requestId string) context.Context {

	if requestId == "" {
		return ctx
	}
	return context.WithValue(ctx, apisRequestIDKey{}, requestId)
}

// apisRequestID - 读取 context 中的调用级幂等键（非 apis 调用或空键返回空串）
func apisRequestID(ctx context.Context) string {

	value, _ := ctx.Value(apisRequestIDKey{}).(string)
	return value
}

// apisDoer - apis.Doer 的根 Client 适配器（含未激活闸门）。
type apisDoer struct {
	// client - 宿主根 Client
	client *Client
}

// Do - 实现 apis.Doer：未激活直接返回 apis.ErrNotActivated（不发请求）；
// 否则经根 Client doRequest 携带签名头发起调用，返回业务信封原始 JSON 字节。
// requestId 经 context 传给传输层：HTTP 放 X-Request-Id 头、gRPC 挂 metadata x-request-id
// （04 §2.2 头族映射；两者都不进签名 canonical）。
func (this apisDoer) Do(ctx context.Context, method string, path string, body []byte, requestId string) ([]byte, error) {

	// 未激活闸门：无 activation token 或授权状态非放行态（LicenceProtocol.PassThrough 口径，
	// 与 TenantSync/PullEvents 的 fail-closed 惯例一致）时不发请求
	this.client.mu.RLock()
	token := this.client.state.ActivationToken
	status := this.client.state.Status
	this.client.mu.RUnlock()
	if token == "" || !LicenceProtocol.PassThrough(status) {
		return nil, apis.ErrNotActivated
	}
	_, raw, err := this.client.doRequest(withApisRequestID(ctx, requestId), method, path, body, true)
	return raw, err
}

// 编译期接口断言
var _ apis.Doer = apisDoer{}
