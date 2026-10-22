# openpay/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `openpay` 模块的结构、分层与常用命令。根目录通用规范（注释风格、`this` 接收者、中文注释等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 openpay 专属约定；面向商户的接入教程见 [`README.md`](README.md)，设计方案见 pay 仓库 `docs/plan/openpay-sdk/00-设计方案.md`。

## 模块定位

- 模块路径 `github.com/inis-io/aide/openpay`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`。
- 定位：**商户侧**调用 inis-pay 开放聚合支付平台的 SDK。与 [`pay`](../pay/AGENTS.md) 的语义边界：`pay` 是平台内部面向支付宝/微信/PayPal 的 Provider 抽象（服务端用），`openpay` 是商户对外接入平台的客户端（商户侧用）。
- 直接依赖仅 `google.golang.org/grpc` + `google.golang.org/protobuf`（默认 gRPC 链路所需）；HTTP 链路与签名器为纯标准库实现。**新增依赖前先确认是否真的必要**。
- 契约唯一事实源：`proto/openpay/merchant/v1/merchant.proto`（平台后端复制同一份文件生成服务端桩）；生成的客户端桩 `proto/gen/openpay/merchant/v1/*.pb.go` **提交入库**，商户 `go get` 后无需安装 protoc。
- **商户只 import 根包**：根目录仅保留 `openpay.go` 一个入口文件，全部公开类型以别名、哨兵与函数转发的方式从根包导出；实现按职责拆在 `types` / `sign` / `notify` / `internal/transport`。

## 目录结构

```
├── openpay.go          # 唯一入口：包注释 + Client/Option/NewClient/Close + 六个业务方法 + 全部别名与转发
├── openpay_test.go     # 门面测试：别名编译期等价、默认链路、选项、参数校验、Close 幂等、HTTP 端到端
├── types/              # 公开：请求/响应模型 + 错误模型（零依赖）
│   ├── trade.go        # Trade/Gateway/MerchantBrief/TradeCreate*/TradeQuery*/Checkout* + Normalize/UnmarshalData
│   ├── transfer.go     # Trans/TransferRequest/TransferResult/TransferQueryRequest + Normalize/UnmarshalData
│   ├── channel.go      # Channel
│   ├── notify.go       # NotifyPayload（Webhook 报文）
│   └── errors.go       # APIError + 哨兵错误 ErrInvalidRequest/ErrInvalidSign/ErrTransport
├── sign/               # 公开：Signer 接口 + MD5 实现 + 值格式化（Value）
├── notify/             # 公开：ParseNotify / VerifyNotify（依赖 sign + types）
├── internal/           # 内部实现，商户不直接接触
│   └── transport/      # Transport 接口 + Operation 常量 + HTTP/gRPC 实现 + proto→模型转换
│       ├── transport.go  # Config、Operation、Transport、dataUnmarshaler、mchIdField
│       ├── http.go       # 端点路由表、body/query 签名、信封解析（decodeEnvelope）
│       └── grpc.go       # RPC 描述符表、protojson 签名、metadata、GRPCTarget、fromProto*
├── README.md           # 商户接入教程
└── proto/              # 契约源 + 生成桩（提交入库）
```

## 分层与依赖方向（禁止成环）

```
types（零依赖） ← sign（零依赖） ← notify（sign + types） ← internal/transport（sign + types + proto/gen） ← 根包（全部）
```

- `types` 保持零依赖：不得导入 `sign`/`notify`/`transport`/根包，也不得导入 `proto/gen`。
- `sign` 不导入 `types`；`notify` 不导入 `transport`。
- 根包不得导入 `proto/gen`；只有 `internal/transport/grpc.go` 需要它。
- `sign`/`notify` 之所以是公开子包：其类型（`Signer`、`NotifyPayload`）出现在根包公开方法签名上；`internal/transport` 是纯实现细节。

## 核心约定

- **双链路对称**：业务方法只组装强类型请求并调用 `Client.invoke` → `Transport.Invoke`；签名器、默认值注入（`types.*.Normalize`）、错误模型两条链路完全复用。新增能力必须**同时**补 `internal/transport` 的 `httpRoutes`（http.go）与 `grpcOperations`（grpc.go），并同步 proto 契约。
- **新增能力的落地清单**：① `types/` 加请求/响应模型（含 `Normalize`，需要时实现 `UnmarshalData` 适配 HTTP 的 `row`）→ ② `internal/transport/http.go` 加路由与 payload 分支 → ③ `internal/transport/grpc.go` 加描述符与 `fromProto*` 转换（proto 契约同步）→ ④ 根 `openpay.go` 加业务方法与**别名**（`type X = types.X`）→ ⑤ 测试：`types`（默认值/映射）、transport（两链路）、根（参数校验与公开面）。
- **签名算法与服务端逐字节一致**（`utils.Md5.GenSign`）：剔除 `sign` 键 → 剔除「字符串形式为空」的值 → 键字典序拼 `k=v&...` → 追加 `&secret=<secret>` → MD5 大写。注意服务端的「空值」判定是 `len(cast.ToString(v)) == 0`，因此**数值 `0`、`false` 会参与签名**（格式化为 `"0"`/`"false"`），只有空串与 `nil` 被剔除。
- **金额一律 `float64`（元）**，格式化固定 `strconv.FormatFloat(v,'f',-1,64)`（`12.30` → `"12.3"`）；禁止把金额改成字符串或 `decimal`，否则与服务端验签分叉。
- **签名前必须注入服务端默认值**（服务端 `SmartVerify` 绑定参数时按 `default` 标签注入后再验签）：下单 `currency=CNY`、`expired=900`；转账 `currency=CNY`、`payeeType=ALIPAY_LOGON_ID`；收银 `scene=alipay`、`way=auto`、`genUrl=true`。默认值只写在 `types` 的 `Normalize()` 里，两条链路共用。
- **签名器禁止复用 `aide/utils`**：`utils.Md5` 是带可变字段的包级全局单例（`SetFields`/`SetOmitEmpty` 会污染全局状态、并发不安全）。`sign` 包为标准库纯函数实现，可并发、可独立测试；HTTP 链路组装 query 时复用 `sign.Value`，保证 query 值与签名输入完全一致。
- **HTTP 信封**：平台 HTTP 状态恒 `200`，业务结果只看 `body.code`（`200` 成功）；`code != 200` → `*types.APIError{Code,Msg,Raw}`；body 非法 JSON → 普通错误（协议违例，不归 `ErrTransport`）。
- **gRPC 错误映射**：`Unauthenticated` → `ErrInvalidSign`；其余 RPC 失败 → `ErrTransport`（文案带 `codes.Code`）；`OK + envelope.code != 200` → `*APIError`（`Raw` 为空）。
- **HTTP 端点不可自造**：`POST /api/trade/unity`、`GET /api/trade/query`、`GET /api/trade/unity`、`POST /api/trans/send`、`GET /api/trans/query`、`GET /api/merchant/channels`。其中 `GET /api/trade/unity`（收银）是公开端点，**不签名、不携带 mchId**；其余 5 个必签。
- **gRPC metadata**：同时写 `x-openpay-sign` 与 `x-openpay-mch-id`（商户号已在消息体内，metadata 供服务端拦截器取证与日志）。
- **根包公开面同步**：`types`/`sign` 新增任何公开类型，必须在 `openpay.go` 补别名（并注明来源），同时在 `openpay_test.go` 顶部的编译期断言里加一行 `_ types.X = X{}`，否则商户看不到该类型。
- 风格：注释全中文（`// 名称 - 简述`）、结构体内字段逐行中文注释、接收者统一 `this`、对外方法首参 `ctx context.Context`、错误包装 `fmt.Errorf("%w：中文细节", ErrXxx)`、不引入全局状态。

## 签名黄金向量

- `sign/sign_test.go` 顶部的 `golden*` 常量是**服务端实测值**（用 `aide/utils` 的 `utils.Md5.GenSign` 对固定参数集 + 固定密钥 `test-secret-0123456789abcdef` 生成），是签名兼容性的验收线。
- `notify/notify_test.go` 自带 Webhook 报文黄金向量（同一密钥，覆盖金额 `12.3` 与毫秒时间戳不带科学计数法）。
- `internal/transport/helper_test.go` 的 `goldenCreateTrade` 从 `sign` 向量拷贝，用于锚定「链路实现产出的签名 = 服务端实测值」。

改动 `sign.Sign` / `sign.Value` / `Normalize()` 默认值后必须重新生成全部向量：

1. 在 `openpay/` 下建临时模块（自带 `go.mod`，`require github.com/inis-io/aide/utils v0.0.0`，并 `replace` 到 `../../utils` 与 `../../dto`）。
2. 用 `utils.Md5.GenSign(params, secret)` 打印各用例签名；结构体入参须与服务端 `types.TradeUnityPre` / `TransSend` / `TradeTake` / `MerchantChannels` 的 json tag 逐字一致。
3. 把结果硬编码回对应测试，**删除临时模块**（不得留库）。

## 构建与测试命令

```bash
cd openpay && go mod tidy && go build ./... && go vet ./... && go test ./...   # 本模块独立执行（含全部子包）
cd openpay && go test ./sign/ ./types/ ./notify/ ./internal/transport/          # 只跑分区包
```

重新生成 gRPC 桩（仅在契约变更时执行；产物提交入库）：

```bash
cd openpay && protoc --proto_path=proto \
  --go_out=proto/gen --go_opt=paths=source_relative \
  --go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
  proto/openpay/merchant/v1/merchant.proto
```

## 测试说明

同包测试、仅标准库断言、**禁止联网**（`proto/gen` 无测试属正常）：

- `openpay_test.go`（根包）：别名编译期等价、默认链路为 gRPC / `WithTransportHTTP` 切换、选项默认值与覆盖、必填参数与业务参数校验、`Close` 幂等、`ParseNotify` 转发、门面 → HTTP 端到端（默认值注入、信封解析、`errors.As` 取 `*APIError`）。
- `types/`：`Normalize` 默认值（三个请求类型）、`UnmarshalData` 的 `row` 映射与非法 JSON 归一、`APIError`（含 nil 接收者）与哨兵错误。
- `sign/`：12 组黄金向量、`sign` 键剔除、`Value` 格式化逐类型（含 0/false 保留、毫秒时间戳不用科学计数法）。
- `notify/`：黄金报文验签、字段篡改、缺 `sign`、非法 JSON、错误密钥。
- `internal/transport/`：`httptest` 假平台覆盖 6 个操作的端点/方法/默认值/签名复算/`row` 映射/`*APIError`/非法 JSON/网络错误/未知操作；`bufconn` + 手写 `MerchantServiceServer` 覆盖 metadata 验签、`Unauthenticated → ErrInvalidSign`、空信封与业务错误、状态错误 → `ErrTransport`、拨号目标归一、关闭幂等；`consistency_test.go` 校验 HTTP 与 gRPC 对同一逻辑请求产出同一签名（等于黄金向量）。

测试里的 `referenceSign` / `referenceValue`（`internal/transport/helper_test.go`）是服务端签名算法的**独立复算实现**，与被测 `sign` 包互为校验；改动签名逻辑时两者必须同时更新。

## 安全约定

- 不硬编码任何真实密钥；测试密钥 `test-secret-0123456789abcdef` 仅用于向量。
- `secret` 只在内存中参与签名：不落盘、不写日志、不进错误文案（`Client.secret` 私有字段，无导出；`types`/`sign` 均只接收密钥参数，不保存）。
- 生产环境 HTTP 链路必须 HTTPS；gRPC 默认 plaintext 仅用于内网/开发，TLS 经 `WithGRPCDialOpts` 注入（传了 dial 选项即不由 SDK 补 insecure 凭证）。
- Webhook 验签无跳过开关；README 强调「先验签 → 幂等落库 → 应答纯文本 success」。

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本，否则每次发 tag 一律在当前版本末位 `+0.0.1`。
- 破坏性变更仅允许发生在「平台契约确实变化」时，并同步更新 README、AGENTS.md 与黄金向量。子包内部结构（`internal/`、文件划分）可自由调整，但**根包公开面（别名清单）不得破坏**。

## 文档

- 商户接入教程：[`README.md`](README.md)（含「代码结构」小节）。
- 包级 API 文档：`go doc github.com/inis-io/aide/openpay`。
- 设计与契约依据：pay 仓库 `docs/plan/openpay-sdk/00-设计方案.md` + 本目录 `proto/openpay/merchant/v1/merchant.proto`。
