# pay/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `pay` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 pay 专属约定；面向接入方的使用教程见 [`README.md`](README.md)，需求与设计文档见 [`docs/`](docs/README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/pay`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，依赖自包含。
- 定位：与 Web 框架、数据库和业务订单模型解耦的**支付能力协议 + 工厂**。核心包只描述金额、请求、结果、能力、错误、通知与 Provider 生命周期，不写数据库、不写 HTTP Response、不碰配置中心。
- 直接依赖仅两个：`github.com/go-pay/gopay v1.5.122`（三家官方 SDK 适配）与 `github.com/spf13/cast`。**升级 gopay 前必须复核 `docs/` 各文档 §3 的「已核实的 SDK 事实」清单**，接口签名以模块源码为准，禁止凭记忆调用。

## 目录结构

```
├── pay.go         # Provider 基础接口与全部能力接口（TradeCreator/Refunder/Biller 等）、Factory、ConfigInput
├── capability.go  # Capability 能力位、BillType、TradeMode、Status/EventType/Outcome 等枚举
├── trade.go       # 交易请求/结果与 TradeEvent（含 GatewayCaptureNo）
├── refund.go      # 退款请求/结果与 RefundEvent
├── transfer.go    # 转账请求/结果与 TransferEvent
├── notify.go      # NotifyRequest/NotifyEvent（含 DedupeKey）/NotifyResponse 与 Valid()
├── driver.go      # Driver 统一门面：能力校验、入参归一、observe 观测链路、DedupeKey 派生
├── registry.go    # Registry 注册表与 validateProvider（能力声明 ⇔ 接口实现一致性校验）
├── pool.go        # 多网关实例池：按 key+版本+配置摘要复用，原子替换，租约释放后关闭旧实例
├── options.go     # OpenOptions 构造选项（Logger/Observer/RawCapture/BillMaxBytes 等）与默认值归一
├── error.go       # 哨兵错误、Reason 标准错误分类、GatewayError、ReasonOf
├── money.go       # Money 金额模型（最小货币单位整数）
├── extension.go   # Extensions 命名空间扩展的编解码
├── sensitive.go   # SensitiveString/SecretRef 敏感值
├── raw.go         # RawCapture 原始报文捕获策略（默认关闭）
├── fingerprint.go # 请求指纹（Metadata 不参与）
├── resolver.go    # Pool 的配置解析器接口
├── action.go      # PaymentAction 支付后续动作（二维码/重定向/表单/SDK）
├── providers/
│   ├── alipay/    # 支付宝官方适配（alipay/v3 + legacy 通知验签）
│   ├── wechat/    # 微信支付 V3 官方适配
│   ├── paypal/    # PayPal 官方适配（v2）
│   └── all/       # 一键注册全部官方 Provider
└── docs/          # 设计文档与需求落地文档（01-03 已实施，04 待实施）
```

## 核心约定

- **装配**：`Factory` 是 Provider 唯一构造入口；`Registry` 显式注册（官方 Provider 也不依赖 `init()`，`providers/all` 除外），注册名转小写去空白，重复注册返回 `ErrDuplicateProvider`。`validateProvider` 强制能力声明与接口实现一致——**新增能力必须同时改 `Capabilities()`、实现接口、`capabilityOf` 泛型调用三处**，缺一即构造报错。
- **Driver 门面**：所有能力调用经 `capabilityOf[T]` 校验能力位，统一入参校验与归一（如账单日期格式、退款金额边界）、`validateOwnExtensions` 命名空间校验，再走 `observe` 观测链路。Provider 层不重复 Driver 已做的通用校验，只做通道专属的入参要求（如 PayPal 退款必须填 capture id）。
- **金额**：`Money` 用最小货币单位整数，**禁止 float64**；`MajorString()` 负责上送网关的主单位字符串。
- **扩展**：Provider 专属参数放 `Extensions[Provider名]`，跨命名空间与未知字段一律拒绝；`Metadata` 只用于本地关联，不上送网关。
- **错误**：`GatewayError` 携带 `Code`（网关原码）+ `Reason`（标准分类）+ `Outcome` + `Retryable`；`Error()` 文本不含报文与敏感值。**`Reason` 映射表必须集中为各 Provider 包级 `map[string]pay.Reason` 常量表 + 出处注释，只增不减**，未知码返回 `ReasonNone`；传输层错误（`gatewayError`）固定 `ReasonNone`。
- **通知**：`ParseNotify` 只做验签、时间窗校验、解密与事件标准化，**绝无副作用**（不 Capture、不落库、不写 Response）；ACK 由 `NotifyResponse` 编码。`DedupeKey` 由 `Driver.ParseNotify` 统一派生（业务单号优先、网关单号兜底、事件 ID 退化），Provider 零改动；`event.ID` 保留网关原值，不作幂等键。
- **单号语义（PayPal）**：`GatewayTradeNo` 承载 order id；退款以 capture id 为目标，从 `CaptureTrade` 结果或 Webhook 捕获事件的 `GatewayCaptureNo` 取得，填入 `RefundRequest.GatewayTradeNo`。
- **Outcome 语义**：捕获成功却缺 capture id、账单摘要校验失败等「网关响应异常」返回 `OutcomeUnknown`，不产生部分可信数据。

## 安全约定

- 私钥/APIv3Key/ClientSecret 只走 `SensitiveString`（输出固定 `[REDACTED]`）或 `SecretRef`，内联与引用二选一；仓库中不得硬编码任何真实凭据。
- `Raw` 捕获默认关闭；日志与 Observer 只出现白名单字段（Provider、操作名、`OutNoHash` 摘要、错误码、`Reason`、Outcome、耗时），**业务单号明文、`DedupeKey`、退款原因、账单 `Content` 一律不进日志**。
- 账单 `Content` 属敏感数据：`Raw` 只保留「申请账单」的 JSON 响应，不含文件内容。
- 通知请求体受 `NotifyMaxBody` 限制，时间戳受 `NotifyClockSkew` 限制；PayPal cert URL 校验 `*.paypal.com` 白名单。
- 微信 V3 无沙箱端点，`WithSandbox(true)` 明确报 `ErrInvalidConfig`，不静默请求生产。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，**禁止联网**：
  - 核心包：假 Provider（`testProvider` 族）直挂 `Registry`，覆盖校验、观测链路与派生逻辑。
  - 支付宝/微信：包内 `sdkClient` 接口 + `fakeSDK` 断言上送字段；支付宝通知用**真实 RSA2 自签 fixture**（测试内生成密钥对与自签证书）走完整验签链路。
  - PayPal：`roundTripFunc` 假 HTTP Transport，断言请求路径、幂等头与 body。
- 新增能力时按 `docs/` 对应需求文档的「测试要求」补齐用例；映射表改动用表驱动用例覆盖每个条目 + 未知码。

## 构建与测试命令

```bash
cd pay && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本（如 `+0.1.0`、`+1.0.0`），否则每次发 tag 一律在当前版本末位 `+0.0.1`，不自行跨版本。

## 文档

- 接入教程与能力矩阵：[`README.md`](README.md)
- 需求落地文档（含 SDK 事实、设计、验收标准）：[`docs/`](docs/README.md)；实施新需求前先读对应文档，完成后同步状态表与本文件。
