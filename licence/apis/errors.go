package apis

import "errors"

// ErrNotActivated - 未激活闸门：根 Client 未完成激活（无 activation token）
// 或授权状态为 EXPIRED / REVOKED / SUSPENDED 时，lic.Apis.* 直接返回本错误（不发请求）；
// 服务端的凭证校验仍是最终边界。
var ErrNotActivated = errors.New("apis: 许可证未激活或授权状态不可用")
