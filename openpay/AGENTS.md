# openpay/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `openpay` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、中文注释等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 openpay 专属约定；面向商户的接入教程见 [`README.md`](README.md)，设计方案见 pay 仓库 `docs/plan/openpay-sdk/00-设计方案.md`。

## 模块定位

- 模块路径 `github.com/inis-io/aide/openpay`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`。
- 定位：**商户侧**调用 inis-pay 开放聚合支付平台的 SDK。与 [`pay`](../pay/AGENTS.md) 的语义边界：`pay` 是平台内部面向支付宝/微信/PayPal 的 Provider 抽象（服务端用），`openpay` 是商户对外接入平台的客户端（商户侧用）。
- 直接依赖仅 `google.golang.org/grpc` + `google.golang.org/protobuf`（默认 gRPC 链路所需）；HTTP 链路与签名器为纯标准库实现。**新增依赖前先确认是否真的必要**。
- 契约唯一事实源：`proto/openpay/merchant/v1/merchant.proto`（平台后端复制同一份文件生成服务端桩）；生成的客户端桩 `proto/gen/openpay/merchant/v1/*.pb.go` **提交入库**，商户 `go get` 后无需安装 protoc。

## 目录结构

```
├── doc.go             # 包注释：双链路、签名、安全约定总览
├── client.go          # Client + Option + NewClient（默认 gRPC）+ Close + 统一 invoke（超时）
├── transport.go       # Transport 接口、Operation 常量、dataUnmarshaler、structToMap
├── transport_http.go  # HTTP 链路：op→(method,path) 映射、body/query 签名、信封解析
├── transport_grpc.go  # gRPC 链路：op→RPC 描述符表、protojson 签名、metadata、proto→模型转换
├── sign.go            # Signer 接口 + md5Signer 纯函数 + signValue 格式化
├── errors.go          # 哨兵错误 ErrInvalidRequest/ErrInvalidSign/ErrTransport + APIError
├── trade.go           # CreateTrade / QueryTrade / GetCheckout + Trade/Gateway/MerchantBrief 模型
├── transfer.go        # SendTransfer / QueryTransfer + Trans 模型
├── channel.go         # ListChannels + Channel 模型
├── notify.go          # NotifyPayload + ParseNotify / VerifyNotify（Webhook 本地验签）
├── proto/
│   ├── openpay/merchant/v1/merchant.proto  # 契约源（不可改）
│   └── gen/openpay/merchant/v1/*.pb.go     # 生成桩（提交入库，包名 merchantv1）
├── README.md          # 商户接入教程
└── *_test.go          # 同包测试，仅标准库断言，禁止联网
```

## 核心约定

- **双链路对称**：业务方法只组装强类型请求并调用 `Client.invoke` → `Transport.Invoke`；签名器、默认值注入（各请求类型的 `normalize()`）、错误模型两条链路完全复用。新增能力时必须**同时**补齐 HTTP 路由（`httpRoutes`）与 gRPC 描述符（`grpcOperations`），并同步 proto 契约。
- **签名算法与服务端逐字节一致**（`utils.Md5.GenSign`）：剔除 `sign` 键 → 剔除「字符串形式为空」的值 → 键字典序拼 `k=v&...` → 追加 `&secret=<secret>` → MD5 大写。注意服务端的「空值」判定是 `len(cast.ToString(v)) == 0`，因此**数值 `0`、`false` 会参与签名**（格式化为 `"0"`/`"false"`），只有空串与 `nil` 被剔除。
- **金额一律 `float64`（元）**，格式化固定 `strconv.FormatFloat(v,'f',-1,64)`（`12.30` → `"12.3"`）；禁止把金额改成字符串或 `decimal`，否则与服务端验签分叉。
- **签名前必须注入服务端默认值**（服务端 `SmartVerify` 绑定参数时按 `default` 标签注入后再验签）：下单 `currency=CNY`、`expired=900`；转账 `currency=CNY`、`payeeType=ALIPAY_LOGON_ID`；收银 `scene=alipay`、`way=auto`、`genUrl=true`。默认值只写在 `normalize()` 里，两条链路共用。
- **签名器禁止复用 `aide/utils`**：`utils.Md5` 是带可变字段的包级全局单例（`SetFields`/`SetOmitEmpty` 会污染全局状态、并发不安全）。`sign.go` 为标准库纯函数实现，可并发、可独立测试。
- **HTTP 信封**：平台 HTTP 状态恒 `200`，业务结果只看 `body.code`（`200` 成功）；`code != 200` → `*APIError{Code,Msg,Raw}`；body 非法 JSON → 普通错误（协议违例，不归 `ErrTransport`）。
- **gRPC 错误映射**：`Unauthenticated` → `ErrInvalidSign`；其余 RPC 失败 → `ErrTransport`（文案带 `codes.Code`）；`OK + envelope.code != 200` → `*APIError`（`Raw` 为空）。
- **HTTP 端点不可自造**：`POST /api/trade/unity`、`GET /api/trade/query`、`GET /api/trade/unity`、`POST /api/trans/send`、`GET /api/trans/query`、`GET /api/merchant/channels`。其中 `GET /api/trade/unity`（收银）是公开端点，**不签名、不携带 mchId**；其余 5 个必签。
- **gRPC metadata**：同时写 `x-openpay-sign` 与 `x-openpay-mch-id`（商户号已在消息体内，metadata 供服务端拦截器取证与日志）。
- 风格：注释全中文（`// 名称 - 简述`）、结构体内字段逐行中文注释、接收者统一 `this`、对外方法首参 `ctx context.Context`、错误包装 `fmt.Errorf("%w：中文细节", ErrXxx)`、不引入全局状态。

## 签名黄金向量

`sign_test.go` 顶部的 `golden*` 常量是**服务端实测值**（用 `aide/utils` 的 `utils.Md5.GenSign` 对固定参数集 + 固定密钥 `test-secret-0123456789abcdef` 生成），是与平台签名兼容性的验收线。改动 `signValue` 格式化、`normalize()` 默认值或 `md5Signer.payload` 后必须重新生成：

1. 在 `openpay/` 下建临时模块（自带 `go.mod`，`require github.com/inis-io/aide/utils v0.0.0`，并 `replace` 到 `../../utils` 与 `../../dto`）。
2. 用 `utils.Md5.GenSign(params, secret)` 打印各用例签名；结构体入参须与服务端 `types.TradeUnityPre` / `TransSend` / `TradeTake` / `MerchantChannels` 的 json tag 逐字一致。
3. 把结果硬编码回 `sign_test.go`，**删除临时模块**（不得留库）。

## 构建与测试命令

```bash
cd openpay && go mod tidy && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

重新生成 gRPC 桩（仅在契约变更时执行；产物提交入库）：

```bash
cd openpay && protoc --proto_path=proto \
  --go_out=proto/gen --go_opt=paths=source_relative \
  --go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
  proto/openpay/merchant/v1/merchant.proto
```

## 测试说明

同包测试、仅标准库断言、**禁止联网**：

- `sign_test.go`：黄金向量（12 组）、`sign` 键剔除、`signValue` 格式化语义。
- `errors_test.go`：`APIError`（含 nil 接收者）、哨兵错误、信封解析三分支（成功无 data / code!=200 / 非法 JSON）。
- `transport_http_test.go`：`httptest` 假平台，覆盖 6 个操作的端点、方法、默认值注入、签名复算、`row` 映射、`*APIError`、非法 JSON、网络错误、自定义 `http.Client`。
- `transport_grpc_test.go`：`bufconn` 内存拨号 + 手写假 `MerchantServiceServer`，校验 incoming metadata 签名（复算）、`Unauthenticated → ErrInvalidSign`、`Envelope.code!=200 → *APIError`、状态错误 → `ErrTransport`、拨号目标归一。
- `consistency_test.go`：同一逻辑请求在 HTTP 与 gRPC 链路产出**同一签名**（等于黄金向量）。
- `client_test.go`：默认链路为 gRPC、`WithTransportHTTP` 切换、必填参数校验、选项默认值、业务方法本地校验、`Close` 幂等。
- `notify_test.go`：黄金报文验签、字段篡改、缺 `sign`、非法 JSON、错误密钥。

测试里的 `referenceSign` / `referenceValue` 是服务端签名算法的**独立复算实现**（与被测实现互为校验），改动签名逻辑时两者必须同时更新。

## 安全约定

- 不硬编码任何真实密钥；测试密钥 `test-secret-0123456789abcdef` 仅用于向量。
- `secret` 只在内存中参与签名：不落盘、不写日志、不进错误文案（`Client.secret` 私有字段，无导出）。
- 生产环境 HTTP 链路必须 HTTPS；gRPC 默认 plaintext 仅用于内网/开发，TLS 经 `WithGRPCDialOpts` 注入（传了 dial 选项即不由 SDK 补 insecure 凭证）。
- Webhook 验签无跳过开关；README 强调「先验签 → 幂等落库 → 应答纯文本 success」。

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本，否则每次发 tag 一律在当前版本末位 `+0.0.1`。
- 破坏性变更仅允许发生在「平台契约确实变化」时，并同步更新 README、AGENTS.md 与黄金向量。

## 文档

- 商户接入教程：[`README.md`](README.md)。
- 包级 API 文档：`go doc github.com/inis-io/aide/openpay`。
- 设计与契约依据：pay 仓库 `docs/plan/openpay-sdk/00-设计方案.md` + 本目录 `proto/openpay/merchant/v1/merchant.proto`。
