# aide/pay

`pay` 是一个与 Web 框架、数据库和业务订单模型解耦的支付能力工厂。核心包只描述金额、请求、结果、能力、错误、通知与 Provider 生命周期；支付宝、微信支付和 PayPal 适配位于 `providers/*`。

## 安装与装配

```go
registry := pay.NewRegistry()
if err := wechat.Register(registry); err != nil {
	return err
}

driver, err := registry.New(ctx, "wechat", wechat.Config{
	AppID:         "wx-app-id",
	MerchantID:    "merchant-id",
	APIv3KeyRef:   "secret://pay/wechat/api-v3-key",
	SerialNo:      "merchant-certificate-serial",
	PrivateKeyRef: "secret://pay/wechat/private-key",
	PublicKeyID:   "PUB_KEY_ID_xxx",
	PublicKey:     wechatPublicKey,
}, pay.WithSecretResolver(secretResolver))
if err != nil {
	return err
}
defer driver.Close()
```

全部官方 Provider 也必须显式装配，不依赖 `init()`：

```go
registry := pay.NewRegistry()
if err := all.Register(registry); err != nil {
	return err
}
```

注册名会转为小写并去除首尾空白。重复注册返回 `pay.ErrDuplicateProvider`；只有 `Registry.Replace` 会显式替换已有工厂。

## 创建支付

金额使用最小货币单位整数，不接受 `float64`：

```go
request := pay.NewTradeCreateRequest(
	"trade-202608080001",
	pay.TradeModeQR,
	"商品",
	pay.NewMoneyMinor(1001, "CNY"), // 10.01 CNY
)
request.NotifyURL = "https://merchant.example/notify/wechat/trade"

result, err := driver.CreateTrade(ctx, request)
```

`PaymentAction` 会明确返回二维码内容、重定向 URL、受约束表单或 SDK 参数。Provider 不返回应直接执行的 HTML 或 script。

Provider 专属参数必须放在对应命名空间中：

```go
request.Extensions, err = pay.SetExtension(
	request.Extensions,
	"alipay",
	alipay.TradeOptions{TimeoutExpress: "15m"},
)
```

Provider 会严格拒绝其他命名空间和未知字段。`Metadata` 只用于调用方本地关联，不会默认发送给网关。

## 多网关 Pool

数据库、配置中心与配置解密留在业务系统，通过 `Resolver` 接入：

```go
pool := pay.NewPool(
	registry,
	gatewayResolver,
	pay.WithPoolMaxEntries(128),
	pay.WithPoolIdleTTL(15*time.Minute),
	pay.WithPoolDrainTimeout(30*time.Second),
	pay.WithPoolOpenOptions(pay.WithSecretResolver(secretResolver)),
)

lease, err := pool.Acquire(ctx, "gateway:12")
if err != nil {
	return err
}
defer lease.Release()

result, err := lease.Driver().CreateTrade(ctx, request)
```

相同 key、`Version`、`SchemaVersion`、沙箱标记和配置摘要会复用实例。配置变化时先完整构造新 Driver，再原子替换；旧实例会等最后一个租约释放后关闭。Pool 不保存配置原文，只保留 SHA-256 摘要。

## 通知与 ACK

HTTP Controller 负责读取受限 Body 并映射为 `NotifyRequest`。Provider 只负责验签、时间窗校验、解密和事件标准化，不写数据库，也不写 HTTP Response：

```go
event, err := driver.ParseNotify(ctx, notifyRequest)
if err != nil {
	write(driver.NotifyResponse(notifyRequest.Kind, pay.NotifyReject))
	return
}

if err := service.ApplyGatewayEvent(ctx, event); err != nil {
	write(driver.NotifyResponse(notifyRequest.Kind, pay.NotifyRetry))
	return
}

write(driver.NotifyResponse(notifyRequest.Kind, pay.NotifyAccept))
```

PayPal 的 `CHECKOUT.ORDER.APPROVED` 只产生 `trade.approved` 事件。业务提交后再显式调用 `CaptureTrade`；通知解析绝不产生扣款副作用。

## 安全约定

- 私钥、APIv3Key、ClientSecret 使用 `SensitiveString` 或 `SecretRef`；内联值与引用二选一。
- `SensitiveString` 的字符串、GoString 与 JSON 输出固定为 `[REDACTED]`。
- Raw 默认关闭；`RawCaptureRedacted` 会脱敏并截断，`RawCaptureFull` 必须由调用方显式启用。
- `GatewayError.Error()` 不包含完整网关报文。
- 请求日志和 Observer 只收到 Provider、操作名、业务号摘要、网关错误码、网关原始错误消息（`Message`，面向商户、不含密钥）、结果确定性与耗时。
- Go 字符串不可可靠擦除。SecretResolver 返回的 `[]byte` 会在构造后覆盖，但 SDK 内部通常仍需保存密钥字符串；应通过短生命周期进程、最小权限和密钥轮换降低风险。
- URL 白名单、订单状态机、费率、钱包、结算、事件唯一约束和商户 Webhook 仍由业务系统负责。

## 官方能力

| Provider | 创建/查询 | Capture | 关单 | 退款/查询 | 转账/查询 | 通知 |
|---|---|---|---|---|---|---|
| Alipay | QR/WAP/PC/Barcode/BusinessQR/App | — | 是 | 是 | 是 | 交易 |
| WeChat | Native QR/H5 | — | 是 | 是 | 是 | 交易/退款/转账 |
| PayPal | QR/网页 | 是 | — | — | — | Webhook |

微信支付 V3 没有可用于这些 API 的沙箱端点，因此 `WithSandbox(true)` 会明确返回 `ErrInvalidConfig`，不会静默请求生产环境。PayPal 使用可停止的手动 token 刷新策略，不启动 SDK 的永久后台刷新 goroutine。
