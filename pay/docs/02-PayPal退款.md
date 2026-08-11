# 02 - PayPal 退款与 Capture ID 暴露落地文档

## 1. 背景

PayPal Provider 目前只声明 `CapTradeCreate / CapTradeQuery / CapTradeCapture / CapWebhook`（`pay/providers/paypal/paypal.go:93-95`），**没有退款能力**，接入方无法达到生产可用。落地退款有一个前置依赖：

> PayPal 的退款操作对象是 **capture id**（`POST /v2/payments/captures/{capture_id}/refund`），而当前 `CaptureTrade` 返回的 `GatewayTradeNo` 是 **order id**（`paypal.go:209`）。capture id 出现在 capture 响应里，但被丢弃了，业务拿不到退款的必要标识。

因此本需求 = 「暴露 capture id」+「实现退款 / 退款查询」两件事，一次完成。

## 2. 目标与非目标

### 2.1 目标

- `TradeResult` / `TradeEvent` 增加 `GatewayCaptureNo`，在 Capture 响应、交易查询、Webhook 捕获事件三处统一回填；
- PayPal Provider 实现 `Refunder` / `RefundQuerier`，声明 `CapRefund` / `CapRefundQuery`；
- 退款幂等：复用 `RefundRequest.IdempotencyKey`（`PayPal-Request-Id` 头）与 `OutRefundNo`（`invoice_id`）双保险。

### 2.2 非目标

- 不做 PayPal 关单：`CreateTrade` 固定 `intent=CAPTURE`（`paypal.go:120`），PayPal 的 void 语义只存在于 `AUTHORIZE` intent，本期不引入授权模式；
- 不做退款 Webhook 事件解析（`PAYMENT.CAPTURE.REFUNDED`）：退款结果同步返回，`QueryRefund` 提供补偿查询，事件解析留作可选二期，载荷结构届时以 PayPal 官方文档联调核实后再落；
- 不做争议（dispute）处理。

## 3. 已核实的 SDK 事实（gopay v1.5.122，`paypal/` 包）

- `paypal/payment.go:142` — `func (c *Client) PaymentCaptureRefund(ctx context.Context, captureId string, bm gopay.BodyMap) (*PaymentCaptureRefundRsp, error)`；成功判定：`Code == Success`（HTTP 200/201 之外即走 `ErrorResponse`）；
- `paypal/model.go:744` — `PaymentCaptureRefund{Id, Status, Amount, InvoiceId, ...}`，`Status` 取值 `CANCELLED` / `PENDING` / `COMPLETED`；
- `paypal/payment.go` 约 168 行 — `func (c *Client) PaymentRefundDetail(ctx context.Context, refundId string) (*PaymentRefundDetailRsp, error)`（退款详情查询）；
- capture id 位置：`paypal/model.go:600-604` `Payments{Captures []*Capture}`、`model.go:640` `Capture.Id` —— 即 `OrderDetail.PurchaseUnits[i].Payments.Captures[j].Id`；
- Webhook `PAYMENT.CAPTURE.*` 事件的 `resource.id` 即 capture id，订单号在 `resource.supplementary_data.related_ids.order_id`（现有代码 `paypal.go:303-305` 已按此取 order id）。

## 4. 核心包设计

### 4.1 `TradeResult` / `TradeEvent` 追加字段（`pay/trade.go`）

```go
// TradeResult 追加：
// GatewayCaptureNo - 网关捕获号（仅 PayPal 等先授权后捕获的通道有值；退款以此号为目标）
GatewayCaptureNo string `json:"gatewayCaptureNo"`

// TradeEvent 追加同名字段，语义相同。
```

只追加、不改动既有字段顺序与语义：`GatewayTradeNo` 继续承载 order id。

### 4.2 PayPal 退款入参约定（文档级契约）

`Driver.Refund` 的通用校验已就位（`pay/driver.go:106-117`：`OutTradeNo`、`OutRefundNo`、双金额为正、同币种、退款额 ≤ 总额）。Provider 层约定：

- **`RefundRequest.GatewayTradeNo` 对 PayPal 必填且填 capture id**（从 `CaptureTrade` 结果的 `GatewayCaptureNo` 或 Webhook 交易事件的同名字段取得）；为空报 `ErrInvalidRequest`；
- `OutTradeNo` 仅用于业务关联与日志摘要，不上送网关；
- 退款金额始终显式上送（`RefundAmount`），全额退款由业务传与 `TotalAmount` 相等的值——不利用 PayPal「省略 amount 即全额」的隐式语义，保持渠道间行为一致。

## 5. Provider 落地细节（`pay/providers/paypal/paypal.go`）

### 5.1 capture id 统一回填

新增辅助函数：

```go
// orderCaptureID - 从订单明细提取首个 capture id，无捕获记录返回空
func orderCaptureID(order *paypalv2.OrderDetail) string
```

回填三处：

1. `CaptureTrade`（`paypal.go:184-210`）：成功后 `GatewayCaptureNo = orderCaptureID(response.Response)`；若订单明细缺 captures，返回 `invalidResponse("trade:capture")`（捕获成功却无 capture id 属异常响应，`OutcomeUnknown`）；
2. `QueryTrade`（`paypal.go:160-181`）：`GatewayCaptureNo = orderCaptureID(response.Response)`，无捕获记录时为空字符串（订单未捕获是正常状态）；
3. `webhookEvent` 的 `PAYMENT.CAPTURE.*` 分支（`paypal.go:311-320`）：`TradeEvent.GatewayCaptureNo = resource.ID`，`GatewayTradeNo` 维持 order id。

### 5.2 `Refund`

```go
// Refund - 发起 PayPal 捕获退款
func (this *Provider) Refund(ctx context.Context, request pay.RefundRequest) (pay.RefundResult, error)
```

- `request.GatewayTradeNo` 为空 → `ErrInvalidRequest`（提示填 capture id）；
- 持有 `this.mu`（与其他方法一致的 token 临界区），`ensureTokenLocked`；
- 请求头 `PayPal-Request-Id = request.IdempotencyKey`（`SetRequestHeader` + `defer ClearRequestHeader`，同 `CaptureTrade` 写法）；
- body：`{"amount": {"value": request.RefundAmount.MajorString(), "currency_code": request.RefundAmount.Currency.Code}, "invoice_id": request.OutRefundNo, "note_to_payer": request.Reason}`（`note_to_payer` 为空时省略）；
- 状态映射：`COMPLETED → RefundStatusSucceeded`、`PENDING → RefundStatusProcessing`、`CANCELLED → RefundStatusClosed`、其他 → `RefundStatusUnknown`；
- 结果：`OutRefundNo = request.OutRefundNo`、`GatewayRefundNo = rsp.Response.Id`、`Amount = request.RefundAmount`、`GatewayStatus = rsp.Response.Status`、`Raw = capture(...)`。

### 5.3 `QueryRefund`

```go
// QueryRefund - 查询 PayPal 退款
func (this *Provider) QueryRefund(ctx context.Context, request pay.RefundQueryRequest) (pay.RefundResult, error)
```

- 必须提供 `request.GatewayRefundNo`（refund id，`PaymentRefundDetail` 的唯一入参）；`OutRefundNo` 不参与查询（PayPal 不支持按 `invoice_id` 查退款），为空报 `ErrInvalidRequest`；
- 状态映射同 §5.2；金额从响应 `Amount.Value / CurrencyCode` 经 `pay.ParseMoney` 解析。

### 5.4 能力声明

`Capabilities()` 改为：

```go
return []pay.Capability{pay.CapTradeCreate, pay.CapTradeQuery, pay.CapTradeCapture, pay.CapRefund, pay.CapRefundQuery, pay.CapWebhook}
```

`validateProvider`（`pay/registry.go:124-164`）会自动校验声明与实现一致，无需改动。

### 5.5 错误处理

复用现有 `checkResponse` / `gatewayError`（`paypal.go:383-407`）。注意两类业务错误码的 `Outcome` 语义：

- capture id 不存在（404 `RESOURCE_NOT_FOUND`）：`OutcomeKnownFailed`；
- 429 / 5xx：`checkResponse` 已置 `Retryable` 与 `ErrGatewayUnavailable`。

## 6. 安全与边界

- 退款属资金操作：`Raw` 仍按 `RawCapture` 策略捕获，默认关闭；`note_to_payer`（退款原因）可能含业务备注，遵守现有日志白名单（不进 `LogRecord`）；
- 所有新方法持有 `this.mu`，保证与 `Close()` 的互斥（关闭后 `ensureTokenLocked` 返回 `ErrPoolClosed`）；
- 幂等依赖网关：`PayPal-Request-Id` 去重 + `invoice_id` 关联，业务侧仍需按 `pay/README.md` 的约定自行保证重试安全（同一 `OutRefundNo` 重试必须参数一致）。

## 7. 测试要求（沿用假 HTTP Transport，禁止联网）

参照 `paypal_test.go:19-23` 的 `roundTripFunc` 打法：

- `CaptureTrade`：假响应含 `purchase_units[0].payments.captures[0].id=C-1`，断言 `GatewayCaptureNo == "C-1"`；构造无 captures 的假响应，断言返回 `OutcomeUnknown` 错误；
- `QueryTrade`：含 / 不含 captures 两种假响应的字段回填；
- `Refund`：断言请求路径为 `/v2/payments/captures/C-1/refund`、`PayPal-Request-Id` 头、`invoice_id` 与金额 body；`COMPLETED`/`PENDING`/`CANCELLED` 三种状态映射；空 capture id 拒绝；
- `QueryRefund`：路径 `/v2/payments/refunds/R-1`、状态映射、空 `GatewayRefundNo` 拒绝；
- Webhook：`PAYMENT.CAPTURE.COMPLETED` 事件断言 `Trade.GatewayCaptureNo == resource.id` 且 `GatewayTradeNo == order_id`；
- 能力声明：`driver.Supports(pay.CapRefund)` 为真，未实现接口时注册表报错（由 `validateProvider` 既有用例思路覆盖）。

## 8. 任务拆解

1. `pay`：`TradeResult` / `TradeEvent` 加 `GatewayCaptureNo` + 注释；
2. `paypal`：`orderCaptureID` + 三处回填 + `Refund` / `QueryRefund` + `Capabilities` + 测试；
3. 文档：`pay/README.md` 能力矩阵 PayPal 行「退款/查询」改为 ✓，「创建交易」段补一句 capture id 约定（`GatewayTradeNo` 语义 + 退款用 `GatewayCaptureNo`）。

## 9. 验收标准

- `go build ./... && go vet ./... && go test ./...` 全绿；
- 业务可仅凭库内返回值完成闭环：`CreateTrade` → 买家批准 → `CaptureTrade` 拿到 `GatewayCaptureNo` → `Refund`（填 `GatewayCaptureNo`）→ `QueryRefund` 追踪终态，全程无需查 PayPal 控制台；
- 三次重试同一 `IdempotencyKey` 的退款请求在假 HTTP 层只产生一条有效退款语义（头部正确上送即视为满足，网关侧去重为 PayPal 责任）。
