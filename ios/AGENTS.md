# ios/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `ios` 模块的结构、分层与常用命令。根目录通用规范（注释风格、`this` 接收者、中文注释等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 ios 专属约定；面向接入方的使用教程见 [`README.md`](README.md)，设计方案见 object-storage 仓库 `docs/plan/07-ios-sdk设计方案.md`。

## 模块定位

- 模块路径 `github.com/inis-io/aide/ios`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`。
- 定位：inis 对象存储平台（Inis Object Storage）的**接入方 SDK**，一个模块覆盖两个平面——控制面（密钥自检 / 桶管理 / 分享 / 回收站 / 用量，HTTP+gRPC 双链路签名）与数据面（桶与对象读写删列、秒传探测、分片上传、批量与拷贝、断点续传、桶配置族、事件通知、STS、预签名，直连 S3 兼容网关）。
- 直接依赖仅 `google.golang.org/grpc` + `google.golang.org/protobuf`（默认 gRPC 链路所需）；HTTP 链路、签名器与数据面为纯标准库实现。**新增依赖前先确认是否真的必要**。
- 控制面契约唯一事实源：`proto/ios/open/v1/open.proto`（平台后端复制同一份文件生成服务端桩）；生成的客户端桩 `proto/gen/ios/open/v1/*.pb.go`（包名 `openv1`）**提交入库**，接入方 `go get` 后无需安装 protoc。
- **接入方只 import 根包**：根目录仅保留 `ios.go` 一个入口文件，全部公开类型以别名、哨兵与函数转发的方式从根包导出；实现按职责拆在 `types` / `sign` / `notify` / `internal/transport` / `internal/s3`。

## 目录结构

```
├── ios.go              # 唯一入口：包注释 + Client/Option/NewClient/Close + 控制面 13 方法 + 数据面 36 方法 + 全部别名与转发
├── ios_test.go         # 门面测试：别名编译期等价、默认链路、选项、参数校验、Close 幂等、数据面同源派生
├── types/              # 公开：控制面请求/响应模型 + 错误模型（零依赖）
│   ├── accesskey.go    # AccessKeyInfo（Secret 永不下发）
│   ├── bucket.go       # BucketInfo/EmptyBucketResult
│   ├── share.go        # Share/ShareCreate*/ShareListRequest + Normalize
│   ├── recycle.go      # RecycleEntry/RecycleList*/RecycleRestoreResult/RecyclePurgeResult + Normalize
│   ├── usage.go        # QuotaView/UsageOverview/BucketUsage/EgressPoint
│   └── errors.go       # APIError + 哨兵错误 ErrInvalidRequest/ErrInvalidSign/ErrTransport
├── sign/               # 公开：Signer 接口 + MD5 实现 + 值格式化（Value）
├── notify/             # 公开：事件通知接收件（P3）
│   ├── notify.go       # Event/Record 模型 + ParseEvent（解析+验签）+ VerifyEvent（仅验签，constant-time）
│   └── notify_test.go  # 黄金向量（服务端同算法复算）、篡改/缺头/坏密钥拒绝、坏 JSON 分层
├── internal/           # 内部实现，接入方不直接接触
│   ├── transport/      # 控制面 Transport 接口 + Operation 常量 + HTTP/gRPC 实现
│   │   ├── transport.go  # Config、Operation、Transport、签名参数组装公共件
│   │   ├── http.go       # 端点路由表、body/query 签名、信封解析（decodeEnvelope）
│   │   └── grpc.go       # RPC 描述符表、protojson 签名（EmitUnpopulated）、metadata、GRPCTarget、fromProto*
│   └── s3/             # 数据面轻客户端（纯标准库，零第三方依赖）
│       ├── signv4.go     # SigV4 签名（UNSIGNED-PAYLOAD）
│       ├── client.go     # 底层请求装配、XML 错误解析（S3Error）
│       ├── bucket.go     # CreateBucket/DeleteBucket/HeadBucket/ListBuckets
│       ├── object.go     # PutObject（含秒传探测）/GetObject/HeadObject/DeleteObject/ListObjects(V2)
│       ├── delete.go     # DeleteObjects（超千自动分批，单键失败不阻断）
│       ├── copy.go       # CopyObject（源按原形态拼接，不做 URL 编码）
│       ├── multipart.go  # 分片编排 + ListMultipartUploads + ResumeUpload 断点续传
│       ├── bucketconfig.go # 桶配置族（CORS/Versioning/Lifecycle 读写删 + putBucketConfig 公共件）
│       ├── bucketnotification.go # 桶事件通知配置（读写 + Delete=PUT 空配置 + 规则本地校验 + 事件名常量）
│       ├── sts.go        # GetSessionToken（STS 临时凭证签发）
│       ├── presign.go    # 预签名 GET/PUT（本地产链，硬上限 7 天；With 版叠加查询参数签入 + ImageProcessURL 拼链）
│       └── xml.go        # S3 XML 编解码
├── examples/           # 示例（package main，go build 覆盖，禁止测试依赖）
│   ├── fullchain/      # 全链联调 demo（设计方案 §13 P1 验收链 + P2 能力演示）
│   └── webhook/        # 事件通知 Webhook 接收器 demo（验签 → 投递号幂等 → 落库 → 2xx）
├── README.md           # 接入方使用教程
└── proto/              # 控制面契约源 + 生成桩（提交入库）
```

## 分层与依赖方向（禁止成环）

```
types（零依赖） ← sign（零依赖） ← internal/transport（sign + types + proto/gen） ← 根包（全部）
notify（types） ← 根包
internal/s3（纯标准库，不依赖 types/sign/transport） ← 根包
```

- `types` 保持零依赖：不得导入 `sign`/`notify`/`transport`/`s3`/根包，也不得导入 `proto/gen`。
- `notify` 只依赖 `types`（哨兵错误同源：`ErrInvalidRequest` / `ErrInvalidSign`），与 openpay/notify 同款依赖方向。
- `internal/s3` 与 `internal/transport` 互不导入；两者在根包汇合（控制面走 `invoke`，数据面走 `data()`）。
- 根包不得导入 `proto/gen`；只有 `internal/transport/grpc.go` 需要它。
- `sign` 之所以是公开子包：`Signer` 类型出现在根包公开选项签名上（`WithSigner`）；`internal/*` 是纯实现细节。
- **s3 哨兵错误与 types 同源**：`internal/s3` 的 `ErrInvalidRequest`/`ErrTransport` 直接引用 `types` 变量（只此一项例外允许 s3 导入 types），保证接入方 `errors.Is` 判定平面无关。

## 核心约定

- **双链路对称（控制面）**：业务方法只组装强类型请求并调用 `Client.invoke` → `Transport.Invoke`；签名器、默认值注入（`types.*.Normalize`）、错误模型两条链路完全复用。新增控制面能力必须**同时**补 `internal/transport` 的 `httpRoutes`（http.go）与 `grpcOperations`（grpc.go），并同步 proto 契约。
- **新增控制面能力的落地清单**：① `types/` 加请求/响应模型（含 `Normalize`）→ ② `internal/transport/http.go` 加路由与 payload 分支 → ③ `internal/transport/grpc.go` 加描述符与 `fromProto*` 转换（proto 契约同步）→ ④ 根 `ios.go` 加业务方法与**别名**（`type X = types.X`）→ ⑤ 测试：`types`（默认值）、transport（两链路）、根（参数校验与公开面）。
- **签名算法与服务端逐字节一致**（`utils.Md5.GenSign`）：剔除 `sign` 键 → 剔除「字符串形式为空」的值 → 键字典序拼 `k=v&...` → 追加 `&secret=<secret>` → MD5 大写。注意服务端的「空值」判定是 `len(cast.ToString(v)) == 0`，因此**数值 `0`、`false` 会参与签名**（格式化为 `"0"`/`"false"`），只有空串与 `nil` 被剔除。
- **gRPC 签名必须 `protojson.MarshalOptions{EmitUnpopulated: true}`**：服务端拦截器以同一参数集验签，不吐零值字段会把 `0`/`false` 排除出参数集，必然验签分叉。改任何一侧必须两侧同步。
- **默认值只写在 `types` 的 `Normalize()`**（`ShareCreateRequest.Expires<0→0`、`RecycleListRequest.Page=1/Limit=20`），两条链路共用；禁止在 transport 或根包重复注入。
- **签名器禁止复用 `aide/utils`**：`utils.Md5` 是带可变字段的包级全局单例（`SetFields`/`SetOmitEmpty` 会污染全局状态、并发不安全）。`sign` 包为标准库纯函数实现，可并发、可独立测试。
- **HTTP 信封**：控制面 HTTP 状态恒 `200`，业务结果只看 `body.code`（`200` 成功）；`code != 200` → `*types.APIError{Code,Msg,Raw}`；body 非法 JSON → 普通错误（协议违例，不归 `ErrTransport`）。
- **gRPC 错误映射**：`Unauthenticated` → `ErrInvalidSign`；其余 RPC 失败 → `ErrTransport`（文案带 `codes.Code`）；`OK + envelope.code != 200` → `*APIError`（`Raw` 为空）。
- **控制面 13 端点不可自造**（与平台 `app/api/control/open-*.go` 逐一对齐）：`GET /api/open-accesskey/info`、`GET /api/open-bucket/list`、`POST /api/open-bucket/empty`、`POST /api/open-share/create`、`GET /api/open-share/list`、`POST /api/open-share/remove`、`GET /api/open-object/recycle`、`POST /api/open-object/restore`、`POST /api/open-object/purge`、`POST /api/open-object/clear`、`GET /api/open-usage/overview`、`GET /api/open-usage/buckets`、`GET /api/open-usage/egress-trend`。全部必签。
- **gRPC metadata**：写 `x-ios-sign`（签名）；`accessKey`/`timestamp` 在消息体内，拦截器直接取。
- **数据面约定**：SigV4 + `UNSIGNED-PAYLOAD`（请求体不预读哈希，流式直通）；`x-amz-meta-*` 键统一小写并按字典序装配（`PutInput` / `UploadInput` / `CreateInput` 的 `Meta`，`CacheControl` 原样写入 `Cache-Control`；`ObjectMeta.CacheControl` 回读）；分片初始化带元数据用 `CreateMultipartUploadEx`，旧 `CreateMultipartUpload` 保留为薄封装；`Upload` 分片编排固定「顺序读片 + 有界并发 + 任一失败自动 Abort」；预签名有效期硬上限 7 天（超出返回 `ErrInvalidRequest`），`PresignGetObjectWith` 可把额外查询参数（如网关图片处理串 `x-inis-process`）签入 canonical query（私有桶 blur-up 双 URL 必须走它，事后拼接会破坏签名），`ImageProcessURL` 纯本地拼公开桶处理链接零往返；数据面不套整体超时，`ctx` 由调用方全权控制。
- **秒传探测（P2）**：`PutObject` 默认先走秒传——`PutInput.InstantUpload` 为 nil 即开启（显式 `false` 关闭），仅当 `Body` 实现 `io.Seeker` 时生效：保存原偏移 → 流式 SHA-256 + size → Seek 复位 → 签名 `POST /{bucket}/{key}?instant`（头 `x-inis-sha256` / `x-inis-size`，并透传 `Content-Type` / `x-amz-meta-*` / `Cache-Control`）。`200 + InstantResult.Hit=true` 直接返回（ETag 取 XML，Size 取本地长度，VersionId 取响应头或 `null`，触发 `OnInstantHit(key)`）；未命中 / 404 / 403 / 旧网关 / 网络 / 协议错误一律静默回退原上传，不得把探测失败抛给调用方。不可 Seek 的纯流跳过探测；探测后无法复位流属本地流错误，返回 `ErrTransport`（不能带着已消费的流继续上传）。`Upload` / `ResumeUpload` 的小文件单 PUT 路径复用 `PutObject`，自动继承该能力。
- **ResumeUpload 语义**：复用「同桶同键、最新 Initiated」的进行中会话；已传分片必须与本地上传布局一致（序号在界内、非末片大小 = partSize、末片大小 = 余量），不一致返回 `ErrInvalidRequest` 且**不自动中止**（保留现场，防误杀其他进程会话，治理入口是 `AbortMultipartUpload`）；已传分片信任网关记录不重核内容；调用方必须从头提供完整内容流，已传分片本地跳过（`io.ReadSeeker` 直接 Seek，否则读丢弃）；续传失败不自动 Abort（与 `Upload` 相反），供下次继续。
- **DeleteObjects 语义**：超 1000 键 SDK 自动分批聚合；单键失败不阻断，逐键回传 `Errors`；不存在键按网关幂等语义归 `Deleted`。
- **CopyObject 语义**：`X-Amz-Copy-Source` 按「源桶/源键」**原形态**拼接，不做 URL 编码（平台网关按原样解析，编码会破坏键查找）。
- **桶配置族语义**：PUT 均为整体覆盖；CORS/Lifecycle 未配置时 GET 返回 `*S3Error`（`NoSuchCORSConfiguration` / `NoSuchLifecycleConfiguration`），Versioning 未开启返回空串（S3 语义）；`PutBucketVersioning` 本地只收 `Enabled`/`Suspended`（常量导出在 s3 与根包）。
- **事件通知语义（P3）**：规则数 0-100（空列表 = 清除，AWS 无独立 Delete，`DeleteBucketNotification` 以 PUT 空配置实现）；GET 未配置返回空列表（不 404），回读含服务端生成的 `Id` 与 `Secret`（限属主签名端点）；事件名当前仅 `EventObjectCreated`（`s3:ObjectCreated:*`）与 `EventObjectRemoved`（`s3:ObjectRemoved:*`）两个常量，本地校验拒绝其它取值；投递 at-least-once（退避 1s/4s 最多 3 次，2xx 即成功），报文头 `X-Inis-Event` / `X-Inis-Signature`（hex HMAC-SHA256(body, Secret)，小写）/ `X-Inis-Delivery`（投递号，重试间稳定，幂等键）。
- **notify 验签约定**：`ParseEvent` 先验签后解析——密钥为空归 `ErrInvalidRequest`，缺签名头或验签不符一律 `ErrInvalidSign`（不区分细节），载荷非法 JSON 归普通错误；`VerifyEvent` 大小写归一后 `subtle.ConstantTimeCompare`。**验签无跳过开关**，README 强调三条铁律（先验签 → 按投递号幂等 → 先落库再 2xx）。
- **STS 语义**：`GetSessionToken` 有效期缺省 3600 秒、本地钳制 [900, 43200]（与网关一致）；持有会话令牌的客户端再签发直接本地 `ErrInvalidRequest`（防令牌自我续期，网关层同样拒绝）；临时凭证仅数据面可用。
- **根包公开面同步**：`types`/`sign`/`notify`/`internal/s3` 新增任何公开类型，必须在 `ios.go` 补别名（并注明来源），同时在 `ios_test.go` 顶部的编译期断言里加一行 `_ types.X = X{}`（或 `_ s3.X = X{}` / `_ notify.X = X{}`），否则接入方看不到该类型。
- 风格：注释全中文（`// 名称 - 简述`）、结构体内字段逐行中文注释、接收者统一 `this`、对外方法首参 `ctx context.Context`、错误包装 `fmt.Errorf("%w：中文细节", ErrXxx)`、不引入全局状态。

## 签名黄金向量

- `sign/sign_test.go` 顶部的 `golden*` 常量是**服务端实测值**（用 `aide/utils` 的 `utils.Md5.GenSign` 对固定参数集 + 固定密钥 `INIS-TEST-SECRET-0123456789abcdef` 生成），是签名兼容性的验收线。
- `internal/transport/consistency_test.go` 校验 HTTP 与 gRPC 对同一逻辑请求产出同一签名（等于黄金向量）。
- `notify/notify_test.go` 的 `golden*` 常量是**事件投递签名实测值**（按服务端 `dispatcher` 同算法 `hex(HMAC-SHA256(body, secret))` 对固定报文 + 固定密钥复算），改动验签逻辑必须重新生成。

改动 `sign.Sign` / `sign.Value` / `Normalize()` 默认值后必须重新生成全部向量：

1. 在 `ios/` 下建临时模块（自带 `go.mod`，`require github.com/inis-io/aide/utils v0.0.0`，并 `replace` 到 `../../utils` 与 `../../dto`）。
2. 用 `utils.Md5.GenSign(params, secret)` 打印各用例签名；参数集键名须与服务端 `app/types/open.go` 的 json tag 逐字一致。
3. 把结果硬编码回对应测试，**删除临时模块**（不得留库）。

## 构建与测试命令

```bash
cd ios && go mod tidy && go build ./... && go vet ./... && go test ./...   # 本模块独立执行（含全部子包）
cd ios && go test ./sign/ ./types/ ./notify/ ./internal/transport/ ./internal/s3/     # 只跑分区包
```

重新生成 gRPC 桩（仅在契约变更时执行；产物提交入库）：

```bash
cd ios && protoc --proto_path=proto \
  --go_out=proto/gen --go_opt=paths=source_relative \
  --go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
  proto/ios/open/v1/open.proto
```

## 测试说明

同包测试、仅标准库断言、**禁止联网**（`proto/gen` 无测试属正常）：

- `ios_test.go`（根包）：别名编译期等价、默认链路为 gRPC / `WithTransportHTTP` 切换、选项默认值与覆盖、必填参数与业务参数校验、`Close` 幂等、S3 端点派生（默认与控制面同源同端口——平台合并模式单端口部署为零配置默认形态；独立 `:9000` 数据面部署用 `WithS3Endpoint` 显式覆盖）。
- `types/`：`Normalize` 默认值（Share/Recycle）、`APIError`（含 nil 接收者）与哨兵错误。
- `sign/`：17 组黄金向量、`sign` 键剔除、`Value` 格式化逐类型（含 0/false 保留、毫秒时间戳不用科学计数法）。
- `notify/`：黄金向量（黄金报文 + 黄金密钥 + 黄金签名逐字节一致）、大写签名归一、报文篡改 / 错误密钥 / 空参拒绝、字段完整性、缺头 / nil 头拒绝、坏 JSON 分层（验签过但载荷非法归普通错误而非 `ErrInvalidSign`）。
- `internal/transport/`：`httptest` 假平台覆盖端点/方法/默认值/签名复算/`*APIError`/非法 JSON/网络错误/未知操作；`bufconn` + 手写 `OpenServiceServer` 覆盖 metadata 验签、`Unauthenticated → ErrInvalidSign`、空信封与业务错误、状态错误 → `ErrTransport`、拨号目标归一、关闭幂等；`consistency_test.go` 校验双链路同签名。
- `internal/s3/`：`signv4_test.go` 锚定 AWS 官方签名向量（测试密钥 `wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`，注意是斜杠 `/bPxRfiCY`）；`client_test.go` 用内存假网关覆盖桶/对象/分片/预签名与 XML 错误解析；`object_meta_test.go` 覆盖 Meta/CacheControl 透传；`instant_test.go` 用独立假网关覆盖秒传探测与失败回退——命中零上行（断言 `x-inis-sha256` / `x-inis-size` / Content-Type / Meta / Cache-Control / SigV4 头）、未命中 404 复位后完整重传（断言 PUT 体逐字节一致）、旧网关路由级 404 回退、200 坏 XML 协议错误回退、不可 Seek 纯流直通、`InstantUpload=false` 跳过；`presign_extra_test.go` 覆盖叠加查询参数签入（处理串进入 canonical query、签名随参数变化、nil 参数与裸链等价）与 `ImageProcessURL` 拼链转义；`p2_test.go` 扩展假网关（STS/批删/拷贝/会话列表/桶配置三族/事件通知配置 + 会话凭据验签通道）覆盖 P2 与 P3：批量删除（成功/幂等/单键失败/超千分批/校验）、CopyObject（往返/NoSuchKey/校验）、ListMultipartUploads（全量/前缀/校验）、ResumeUpload（无会话全量/部分跳过-断言网关只收缺失片/非 Seeker 读丢弃跳过/布局不一致报错且保留现场）、桶配置族（未配置 404 码/写入回读一致/本地校验/删除回归）、GetSessionToken（默认与钳制有效期/响应解析/临时凭证全链路签名带令牌头/禁再签发）、事件通知配置族（未配置返回空列表不 404/写入回读含生成 Id 与 Secret/整组替换/本地校验/清除回归空列表）。

测试里的 `referenceSign` / `referenceValue`（`internal/transport/helper_test.go`）是服务端签名算法的**独立复算实现**，与被测 `sign` 包互为校验；改动签名逻辑时两者必须同时更新。

## 安全约定

- 不硬编码任何真实密钥；控制面测试密钥 `INIS-TEST-SECRET-0123456789abcdef`、SigV4 测试密钥为 AWS 官方向量密钥，仅用于测试。
- `secret` 只在内存中参与签名：不落盘、不写日志、不进错误文案（`Client.secret` 私有字段，无导出；`types`/`sign`/`s3` 均只接收密钥参数，不保存）。
- 事件通知验签密钥（规则 `Secret`）与账号 Secret 相互独立；`notify` 验签无跳过开关。
- 生产环境 HTTP 链路必须 HTTPS；gRPC 默认 plaintext 仅用于内网/开发，TLS 经 `WithGRPCDialOpts` 注入（传了 dial 选项即不由 SDK 补 insecure 凭证）。
- 控制面时间窗 ±15 分钟由平台判定，SDK 不在本地做时间窗预检（避免双时钟分叉）。

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本，否则每次发 tag 一律在当前版本末位 `+0.0.1`。
- 破坏性变更仅允许发生在「平台契约确实变化」时，并同步更新 README、AGENTS.md 与黄金向量。子包内部结构（`internal/`、文件划分）可自由调整，但**根包公开面（别名清单）不得破坏**。

## 文档

- 接入方使用教程：[`README.md`](README.md)（含「代码结构」小节）。
- 包级 API 文档：`go doc github.com/inis-io/aide/ios`。
- 设计与契约依据：object-storage 仓库 `docs/plan/07-ios-sdk设计方案.md` + 本目录 `proto/ios/open/v1/open.proto`。
