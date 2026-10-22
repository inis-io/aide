// Package openpay - inis-pay 开放聚合支付平台的商户 SDK。
//
// 商户通过本包调用平台开放能力：统一下单、商户查单、收银取链接、转账发起/查询、
// 商户通道列表，并在自有服务端处理平台的支付结果异步通知（Webhook）。
//
// # 双链路
//
// 默认 gRPC（/openpay.merchant.v1.MerchantService/*）；构造时传 WithTransportHTTP()
// 切换到 HTTP 开放端点（/api/trade/unity 等）。业务方法签名、签名器、默认值注入与
// 错误模型两条链路完全一致，业务代码无需感知链路差异：
//
//	client, err := openpay.NewClient(mchId, secret, openpay.WithEndpoint("https://pay.example.com"))
//	// 或：openpay.NewClient(mchId, secret, openpay.WithEndpoint(...), openpay.WithTransportHTTP())
//
// # 签名
//
// 参数集（HTTP 为 JSON 展开的 map，gRPC 为 protojson 展开的 map）→ 剔除 sign 键 →
// 剔除空值 → 键字典序拼 k1=v1&k2=v2 → 追加 &secret=<商户密钥> → MD5 十六进制大写。
// 与平台服务端 utils.Md5.GenSign 逐字节等价（含 float64 的 FormatFloat(v,'f',-1,64)
// 语义与「只剔除字符串形式为空的值」语义）；实现为无副作用纯函数，Client 可并发使用。
//
// # 安全
//
// secret 由调用方运行时注入（环境变量/配置中心），本包不持久化、不写日志、
// 不出现在任何错误文案中。生产环境 HTTP 链路必须走 HTTPS；
// gRPC 链路默认 plaintext（对齐平台同端口分流），TLS 通过 WithGRPCDialOpts 注入凭证。
package openpay
