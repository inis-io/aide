package licence

import (
	"context"

	"github.com/inis-io/aide/licence/apis"
)

// 本文件承载 API 商城 typed 方法包（apis 子包）在根 Client 上的挂载：
// Client.Apis 字段在 New 时构造（与管理面 admin 子包挂资源组字段同一风格），
// apisDoer 适配器把根 Client 的统一请求出口 doRequest（withSign=true，
// 自动携带 X-License-* 签名头族 / gRPC 同名 metadata）适配为 apis.Doer 窄接口。
// 依赖方向：root → apis（apis 不 import 根包，编译期保证无环）。

// apisDoer - apis.Doer 的根 Client 适配器（含未激活闸门）。
type apisDoer struct {
	// client - 宿主根 Client
	client *Client
}

// Do - 实现 apis.Doer：未激活直接返回 apis.ErrNotActivated（不发请求）；
// 否则经根 Client doRequest 携带签名头发起调用，返回业务信封原始 JSON 字节。
func (this apisDoer) Do(ctx context.Context, method string, path string, body []byte) ([]byte, error) {

	// 未激活闸门：无 activation token 或授权状态非放行态（passThrough 口径，
	// 与 TenantSync/PullEvents 的 fail-closed 惯例一致）时不发请求
	this.client.mu.RLock()
	token := this.client.state.ActivationToken
	status := this.client.state.Status
	this.client.mu.RUnlock()
	if token == "" || !passThrough(status) {
		return nil, apis.ErrNotActivated
	}
	_, raw, err := this.client.doRequest(ctx, method, path, body, true)
	return raw, err
}

// 编译期接口断言
var _ apis.Doer = apisDoer{}
