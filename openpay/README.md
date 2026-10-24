# openpay — inis-pay 开放聚合支付平台商户 SDK

`openpay` 是商户侧调用 inis-pay 开放平台的 Go SDK：统一下单、商户查单、收银取支付链接、转账发起/查询、商户通道列表，以及平台支付结果异步通知（Webhook）的解析验签。

- 模块路径：`github.com/inis-io/aide/openpay`（独立 Go 模块，与 `aide` 主模块并列）
- 直接依赖：`google.golang.org/grpc`、`google.golang.org/protobuf`（HTTP 链路零第三方依赖）
- 默认链路：**gRPC**；`WithTransportHTTP()` 一键切换到 HTTP 开放端点

## 1. 安装

```bash
go get github.com/inis-io/aide/openpay@latest
```

gRPC 客户端桩已随仓库提交（`proto/gen/`），**无需安装 protoc**；契约源文件见 `proto/openpay/merchant/v1/merchant.proto`。

## 2. 构造客户端

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/inis-io/aide/openpay"
)

func main() {

	mchId := os.Getenv("OPENPAY_MCH_ID")   // 商户号，形如 M1000001
	secret := os.Getenv("OPENPAY_SECRET")  // 商户密钥，运行时注入

	client, err := openpay.NewClient(mchId, secret,
		openpay.WithEndpoint("https://pay.example.com"),
	)
	if err != nil {
		log.Fatalf("构造客户端失败：%v", err)
	}
	defer client.Close() // 释放 gRPC 连接（HTTP 链路为 no-op）

	_ = context.Background()
}
```

`NewClient` 只做参数校验与链路装配，不发起任何业务请求。`Client` 无内部可变状态，可并发使用。

## 3. 选择链路：gRPC（默认）还是 HTTP

| 维度 | gRPC（默认） | HTTP |
|---|---|---|
| 端点 | `/openpay.merchant.v1.MerchantService/*` | `POST /api/trade/unity`、`GET /api/trade/query` 等 |
| 开关 | 默认 | `openpay.WithTransportHTTP()` |
| 端口 | 与 HTTP 同端口（平台按 HTTP/2 + `application/grpc` 分流），无需额外端口 | 同 HTTP |
| 签名位置 | outgoing metadata `x-openpay-sign` | body 的 `sign` 字段 / query 的 `sign` 参数 |
| 传输安全 | 默认 plaintext，生产建议 TLS（见下） | 生产必须 HTTPS |
| 错误模型 | 与 HTTP 完全一致 | 与 gRPC 完全一致 |

两条链路的业务方法、签名器、默认值注入与错误模型完全相同，切换链路不改一行业务代码。

```go
// HTTP 链路
client, err := openpay.NewClient(mchId, secret,
	openpay.WithEndpoint("https://pay.example.com"),
	openpay.WithTransportHTTP(),
)

// gRPC 链路启用 TLS
client, err = openpay.NewClient(mchId, secret,
	openpay.WithEndpoint("pay.example.com:9443"),
	openpay.WithGRPCDialOpts(
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
	),
)
```

> gRPC 拨号目标由 endpoint 自动归一：剥离 `http://`/`https://` 与路径，缺省端口按 `443` 补全。

## 4. 能力一览

| 方法 | gRPC | HTTP | 签名 | 说明 |
|---|---|---|---|---|
| `CreateTrade` | `MerchantService/CreateTrade` | `POST /api/trade/unity` | ✅ | 统一下单，返回订单行 + 可用网关 |
| `QueryTrade` | `MerchantService/QueryTrade` | `GET /api/trade/query` | ✅ | 按 `TradeNo` 或 `OutTradeNo` 查单 |
| `GetCheckout` | `MerchantService/GetCheckout` | `GET /api/trade/unity` | ✅ / — | 收银信息，支付链接在 `Trade.Url`（HTTP 端点为公开端点，不签名） |
| `SendTransfer` | `MerchantService/SendTransfer` | `POST /api/trans/send` | ✅ | 转账发起 |
| `QueryTransfer` | `MerchantService/QueryTransfer` | `GET /api/trans/query` | ✅ | 转账查询 |
| `ListChannels` | `MerchantService/ListChannels` | `GET /api/merchant/channels` | ✅ | 商户可用通道 |
| `ParseNotify` / `VerifyNotify` | — | — | 本地验签 | Webhook 报文解析 + 验签（与链路无关） |

## 5. 常用示例

### 5.1 下单 → 取支付链接（两阶段）

平台是两阶段语义：`CreateTrade` 只落库返回订单行（**不含支付链接**），`GetCheckout` 才产出链接。

```go
ctx := context.Background()

// ① 统一下单
created, err := client.CreateTrade(ctx, &openpay.TradeCreateRequest{
	Amount:      12.30,                 // 元
	ChannelCode: "ALIPAY_PC",           // 取 ListChannels 返回的通道编码
	OutTradeNo:  "TP-20261024-0001",     // 商户订单号 ≤32，平台以此幂等；推荐「前缀-日期-序号」格式
	Describe:    "测试订单",
	NotifyUrl:   "https://merchant.example.com/openpay/notify",
	ReturnUrl:   "https://merchant.example.com/pay/return",
})
if err != nil {
	return err
}
tradeNo := created.Trade.TradeNo

// ② 取收银信息 / 支付链接
checkout, err := client.GetCheckout(ctx, &openpay.CheckoutRequest{
	TradeNo: tradeNo,
	Scene:   "alipay",  // 默认 alipay
	Way:     "auto",    // 默认 auto
})
if err != nil {
	return err
}

// ③ 把 checkout.Trade.Url 交给前端跳转，或渲染成二维码
fmt.Println(checkout.Trade.Url)
```

### 5.2 查单

```go
trade, err := client.QueryTrade(ctx, &openpay.TradeQueryRequest{
	TradeNo: "202608151200000001",       // 与 OutTradeNo 至少填一个
	// OutTradeNo: "TP-20261024-0001",
})
if err != nil {
	return err
}
if trade.Status == "paid" {
	// 已支付：trade.GatewayTradeNo、trade.Amount、trade.UpdateAt（毫秒）等
}
```

### 5.3 转账

```go
sent, err := client.SendTransfer(ctx, &openpay.TransferRequest{
	ChannelCode:  "ALIPAY_TRANS",
	Scene:        "alipay",
	Amount:       88.80,
	PayeeAccount: "payee@example.com",
	PayeeName:    "张三",
	OutTradeNo:   "TF-20261024-0001",
	Describe:     "服务报酬",
})
if err != nil {
	return err
}

trans, err := client.QueryTransfer(ctx, &openpay.TransferQueryRequest{
	OutTradeNo: "TF-20261024-0001",
})
```

### 5.4 通道列表

```go
channels, err := client.ListChannels(ctx)
for _, channel := range channels {
	// channel.Code 即下单 / 转账用的 ChannelCode
	fmt.Println(channel.Code, channel.Name, channel.Currency, channel.FeeRate)
}
```

## 6. 处理支付结果通知（Webhook）

平台向 `NotifyUrl` 发起 `POST`（`Content-Type: application/json`），报文字段：
`tradeNo / outTradeNo / gatewayTradeNo / mchId / amount / currency / scene / way / status / createAt / updateAt / sign`（时间为毫秒时间戳）。

```go
func notifyHandler(secret string) http.HandlerFunc {

	return func(writer http.ResponseWriter, request *http.Request) {

		body, err := io.ReadAll(request.Body) // 必须用原始报文验签，不要先格式化
		if err != nil {
			http.Error(writer, "fail", http.StatusBadRequest)
			return
		}

		payload, err := openpay.ParseNotify(body, secret)
		if err != nil {
			// ErrInvalidSign：验签失败（切勿继续处理）
			http.Error(writer, "fail", http.StatusBadRequest)
			return
		}

		// 幂等：以 tradeNo 去重（平台会多轮重试，且报文无防重放时间戳）
		if err = savePaidOrder(payload); err != nil {
			http.Error(writer, "fail", http.StatusInternalServerError)
			return
		}

		// 成功应答必须是纯文本 success（大小写不敏感，trim 后比较）
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("success"))
	}
}
```

Webhook 处理铁律：

1. **先验签，再落库**；`ParseNotify` 验签失败一律拒绝，没有跳过开关。
2. **成功应答必须是 `success`**：平台判成功条件是 HTTP 2xx 且响应体 `strings.TrimSpace(strings.ToLower(body)) == "success"`，其余一律视为失败并进入重试（5 轮 × 8 次 × 15 秒，轮间 10 分钟）。
3. **以 `tradeNo` 做幂等**：通知会重复投递，且平台不携带时间戳/nonce，商户端需自行去重。
4. **先完成自身业务落库再应答**，否则重试耗尽后平台不再补发（`NotifyRespStatus=offline`，无补发入口）。

## 7. 错误处理

```go
trade, err := client.QueryTrade(ctx, &openpay.TradeQueryRequest{OutTradeNo: "TP-20261024-0001"})
switch {
case err == nil:
	// 成功
case errors.Is(err, openpay.ErrInvalidRequest):
	// 本地参数问题，请求未发出（或链路适配失败）
case errors.Is(err, openpay.ErrInvalidSign):
	// 签名问题：核对商户密钥、参与签名的默认值
case errors.Is(err, openpay.ErrTransport):
	// 网络 / 连接 / 非业务 RPC 失败（文案内带 gRPC 状态码）
default:
	var apiErr *openpay.APIError
	if errors.As(err, &apiErr) {
		// 平台业务错误：apiErr.Code（200 以外均为失败）、apiErr.Msg（中文文案）、apiErr.Raw（HTTP 原始报文）
	}
}
```

平台 HTTP 状态恒为 `200`，业务结果只看信封 `code`：

| 业务码 | 含义 |
|---|---|
| `200` | 成功 |
| `400` | 参数错误 / 签名验证失败（HTTP 链路签名错误归业务码，不归 `ErrInvalidSign`） |
| `403` | 商户停用等权限问题 |
| `404` | 商户不存在 |
| `409` | 订单状态冲突（如重复支付） |
| `204` | 无数据 |

> gRPC 链路：平台验签失败返回 `Unauthenticated` → SDK 归为 `ErrInvalidSign`；其余业务失败仍为 `OK + Envelope.code != 200` → `*APIError`。

## 8. 签名与默认值（务必了解）

签名算法与平台服务端 `utils.Md5.GenSign` **逐字节一致**：

```
1. 参数集（HTTP：请求结构体按 JSON tag 展开；gRPC：请求消息按 protojson 展开，键名均为 camelCase）
2. 剔除 sign 键
3. 剔除「字符串形式为空」的值（空串、null）
4. 剩余键按字典序排序，拼成 k1=v1&k2=v2
5. 末尾追加 &secret=<商户密钥>
6. MD5 → 十六进制 → 大写（32 位）
```

三个实践要点：

- **金额按 float64 语义格式化**：`12.30` → `"12.3"`、`12.00` → `"12"`。SDK 内部强制 `float64` 并通过 `strconv.FormatFloat(v,'f',-1,64)` 格式化，**请勿把金额当字符串拼接**。
- **数值 `0` 与 `false` 会参与签名**（格式化为 `"0"`/`"false"`），只有空串与 `null` 被剔除。这也是 `genUrl=true` 必须显式注入的原因。
- **默认值由 SDK 补齐后再签名**：下单 `currency=CNY`、`expired=900`；转账 `currency=CNY`、`payeeType=ALIPAY_LOGON_ID`；收银 `scene=alipay`、`way=auto`、`genUrl=true`。平台服务端同样先注入 `default` 标签值再验签，缺一项必然验签失败。

需要切换签名协议（如平台升级 HMAC-SHA256）时用 `openpay.WithSigner(...)` 注入新实现，业务代码零改动。

## 9. 安全约定

- `secret` 由调用方**运行时注入**（环境变量 / 配置中心 / 密钥管理服务），SDK 不持久化、不写日志、不出现在任何错误文案中。
- 生产环境 HTTP 链路必须 HTTPS；gRPC 链路默认 plaintext 仅适用于内网/开发环境，生产用 `WithGRPCDialOpts` 注入 TLS 凭证。
- Webhook 的 `NotifyUrl` 必须是公网可达的 HTTPS 地址，且**强制验签**。
- 不要把 `secret` 下发给前端/客户端代码；下单与投递通知都应在商户服务端完成。
- 平台内部字段（`ip`、`agent`、`costRate`、`funding` 等）不在 SDK 模型中暴露，不要依赖未声明字段。

## 10. 常见问题

**Q：一直返回「签名验证失败！」怎么办？**
A：逐项核对 ① `secret` 是否与平台商户密钥一致；② 是否自行拼装了金额字符串（应传 `float64`）；③ 是否漏了默认值字段（用 SDK 方法调用则无需关心，自行构造请求时才会踩到）。

**Q：`GetCheckout` 拿到的 `Trade.Url` 是空的？**
A：`Url` 由 `GetCheckout` 生成；`CreateTrade` 返回的订单行不含链接。另外确认订单状态未关闭/未过期。

**Q：为什么 HTTP 链路的 `GetCheckout` 没有签名？**
A：`GET /api/trade/unity` 是平台面向收银页的公开端点（不验签，请求体也没有 `mchId` 字段）；gRPC 链路的 `GetCheckout` 则统一要求签名。两者语义一致，SDK 已按各链路实际契约处理。

**Q：能并发调用吗？**
A：可以。`Client` 无内部可变状态，`Signer` 是无状态纯函数；`Close()` 只在退出时调用一次（重复调用幂等）。

**Q：金额用 `int` 分会不会更安全？**
A：本 SDK 统一 `float64` 元（与平台模型一致）。为避免二进制浮点误差，建议金额来源只做「一次乘法/除法」转换（如 `float64(fen) / 100`），不要做多次浮点累加。

## 11. 版本与演进

- 平台 Webhook 目前只有 `trade.paid` 一种通知，退款、转账不落通知。
- 退款、关单、钱包、对账单等能力尚在平台蓝图阶段；SDK 方法签名稳定，能力上线后以新增方法方式扩展（不破坏现有接口）。
- 契约源文件 `proto/openpay/merchant/v1/merchant.proto` 是唯一事实源；平台后端复制同一份文件生成服务端桩。

## 12. 代码结构

商户只需 `import "github.com/inis-io/aide/openpay"` 一个包：根包用类型别名与函数转发导出全部公开面，
内部实现按职责分区（只为可维护性，不增加接入成本）。

| 路径 | 作用 |
|---|---|
| `openpay.go` | 唯一入口：包注释 + `Client` / `Option` / `NewClient` / `Close` + 六个业务方法 + 全部别名与转发 |
| `types/` | 公开数据模型与错误模型（各请求/响应结构、`NotifyPayload`、`APIError`、哨兵错误；零依赖） |
| `sign/` | 公开签名器：`Signer` 接口 + MD5 实现（`sign.New()`），供 `WithSigner` 使用 |
| `notify/` | 公开 Webhook 报文解析与验签（`ParseNotify` / `VerifyNotify`，根包已转发） |
| `internal/transport/` | 内部传输链路（HTTP / gRPC 实现与 `Operation` 常量），商户不直接使用 |
| `proto/` | 契约源 `openpay/merchant/v1/merchant.proto` 与生成的客户端桩（已入库，商户免装 protoc） |

## 13. 相关文档

- 模块 AI 约定：[AGENTS.md](AGENTS.md)
- 包注释与 API 文档：`go doc github.com/inis-io/aide/openpay`
- 设计方案：`pay/docs/plan/openpay-sdk/00-设计方案.md`（平台仓库）
