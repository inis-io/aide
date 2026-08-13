# AGENTS.md - Licence SDK 开发指南

> 本文件适用于 `github.com/inis-io/aide/licence` 独立 Go module。除本文件外，仍须遵守
> 上级 [`../AGENTS.md`](../AGENTS.md)；发生冲突时，以本文件中更具体的 licence 约束为准。

## 模块定位与当前状态

- 本目录是 Licen Hub 的 Go SDK，包含运行面 `Client`、管理面 `AdminClient`、在线更新、
  SaaS 租户、项目配置、平台配置、回调接收、签名/验签与本地安全存储。
- 本目录有独立 `go.mod`，不参与 aide 根模块的 `go build ./...`，必须在本目录单独构建测试。
- **截至 2026-08-09，运行面 12 个 RPC 与管理面现有全资源均已实现 HTTP + gRPC。**
  HTTP 保持默认值；gRPC 必须通过 `TransportGRPC` 显式选择，且不做跨协议自动回退。
- canonical proto、生成代码和机器可读协议矩阵位于 `proto/licence/v1/`；服务端共同消费该契约，
  禁止在 Licen Hub 仓库复制第二份 proto。
- 跨项目落地方案见
  [`../../licen-hub/docs/plan/Licence-SDK-HTTP与gRPC双协议跨项目落地方案.md`](../../licen-hub/docs/plan/Licence-SDK-HTTP与gRPC双协议跨项目落地方案.md)。

## 权威契约与跨项目关系

- 运行面唯一权威契约：
  [`../../licen-hub/docs/md/许可证运行面接口契约.md`](../../licen-hub/docs/md/许可证运行面接口契约.md)。
- SDK 总体设计：
  [`../../licen-hub/docs/md/开发者SDK设计方案.md`](../../licen-hub/docs/md/开发者SDK设计方案.md)。
- 服务端实现位于兄弟仓库 `licen-hub/backend`。任何平台网络能力的新增、字段变更、鉴权变化、
  状态码变化或签名变化，均属于跨项目改动，必须同时核对服务端、SDK、契约文档和测试向量。
- 禁止凭经验发明路由、RPC、字段或状态语义；管理面以服务端实际 `GenRoute`、稳定权限码和
  数据范围规则为准，运行面以权威契约为准。

## HTTP + gRPC 双协议强制规则

### 覆盖范围

- 所有“SDK 客户端 → Licen Hub”平台能力都必须具备 HTTP 与 gRPC 两种实现，包括运行面、
  在线更新控制接口、SaaS 租户运行面、项目配置同步和管理面 `AdminClient` 资源能力。
- 新增或修改平台能力时，必须在同一个功能批次内同步完成：共享业务契约、HTTP 客户端适配、
  gRPC 客户端适配、Licen Hub HTTP/gRPC 服务端适配、协议一致性测试和文档。
- “同时支持”指 SDK 对同一公开方法提供可配置的传输实现；不是在一次业务调用中并发请求两种协议，
  也不得默认在写操作失败后跨协议自动重试。
- 以下能力不自动视为双协议缺口：更新清单中的对象存储/CDN 下载 URL、作为客户项目接收端的
  `CallbackHandler`。它们是外部文件传输或 webhook 协议，不是 SDK 到 Licen Hub 的同一 RPC。
  若新增类似例外，必须在设计文档和协议矩阵中明确原因，禁止静默只做 HTTP。

### 单一业务语义

- 业务状态机、权限、数据范围、参数校验、幂等、审计和数据库事务只能实现一次；HTTP 与 gRPC
  仅负责传输解析、元数据提取、错误映射和 DTO 转换，禁止复制两套业务逻辑。
- SDK 公共方法的返回值与副作用不得随传输协议变化。HTTP/gRPC 必须保持相同的状态判定、
  离线降级、滑动刷新、401 重登、分页、错误分层和回调时机。
- HTTP 是兼容默认值，gRPC 必须显式选择；未经单独设计，不实现按请求自动回退，避免非幂等操作
  因跨协议重试而重复执行。
- 每项能力必须进入可检查的协议矩阵，至少记录公开 SDK 方法、HTTP 方法与路径、gRPC full method、
  鉴权方式、权限码、数据域/操作、幂等性和错误映射。任一列缺失都不能视为功能完成。

### protobuf 与生成代码

- protobuf 契约采用版本化 package；字段号一经发布不得复用，删除字段必须 `reserved`，破坏性
  变更新开协议版本。生成的 `*.pb.go` / `*_grpc.pb.go` 禁止手改。
- canonical `.proto` 与生成配置必须只有一个权威来源，Licen Hub 服务端与本 SDK 共同消费；
  具体目录和生成流程以双协议落地方案为准。
- 许可证、租户、项目配置、更新清单等 Ed25519 签名信封必须以原始 JSON 字节在 protobuf `bytes`
  字段中传递。禁止转换为 protobuf message 后重新序列化验签，否则未知字段或字段顺序会破坏签名。
- gRPC 请求签名必须严格使用方案/契约规定的 full method、确定性 protobuf 字节、时间戳和 nonce；
  禁止沿用 HTTP path 拼凑或自行设计第二套签名内容。

### SDK 传输层

- 运行面与管理面分别定义最小传输接口，`Client` / `AdminClient` 只依赖接口，不直接依赖
  `http.Client` 或生成的 gRPC client。两种实现必须有编译期接口断言。
- `context.Context`、deadline、取消、TLS、连接关闭与错误 cause 必须贯穿传输层；gRPC 连接由客户端
  生命周期统一复用和关闭，禁止每次调用重新拨号。
- 生产 gRPC 必须使用 TLS；明文 h2c 仅允许显式配置在开发或受控内网环境使用。
- 大文件上传/带文件验签使用 gRPC client-streaming，禁止把完整文件读入单个 message；对象存储/CDN
  下载继续按更新清单 URL 下载并执行大小、SHA-256 与 release-key 校验。

## 签名与安全兼容

- `Payload`、`TenantPayload`、`ManifestPayload`、`ConfigPayload` 等签名载荷字段顺序即签名内容；
  新字段只允许追加，禁止插入、重排或改名。信封验签始终优先使用解析得到的 payload 原文字节。
- **载荷 v2 破坏性例外**：双轨菜单重构已获准清表重建，`TenantPayload.manifestVersion`
  在 v2 中更名为 `tenantManifestVersion`；这是无正式存量信封前的一次性例外，后续仍恢复只追加纪律。
- license-key / release-key 私钥永远不进入 SDK；SDK 只持公钥。客户端 Ed25519 私钥与
  `activationToken` 只允许经 `Store` 保存，不出本机、不进日志。
- HTTP 现有签名 canonical 不得因 gRPC 上线而改变；gRPC 使用独立、明确版本化的 canonical，
  两种协议共用同一激活记录、公钥、token 与 nonce 防重放存储。
- 日志不得输出完整 token、JWT、私钥、请求签名和指纹；指纹最多记录前 8 位。

## 多机席位约定（开发/预发许可证）

- 信封载荷含 `bindingPolicy`（`single` 单机 / `seats` 多机席位）与 `seatLimit`（席位上限，
  single 固定 1）；历史无席位字段的信封按 `single`/1 缺省解释（`normalizePayload`），
  与平台签发端字节级镜像。
- 席位身份键是实例指纹哈希（平台 `license_seats` 登记键）：同机必须长期稳定、异机必须
  不同；克隆镜像/容器等因子雷同场景必须注入 `Options.Fingerprint` 或 `Provider` 区分。
- `Options.DeviceName` 仅供平台席位列表展示（缺省 `os.Hostname()`，去空格限长 128）；
  proto 契约为 `ActivateRequest.device_name`（上送）与 `RuntimeResponse.seat_no`（回填），
  HTTP 对应字段 `deviceName`/`seatNo`，双协议映射必须同时维护。
- `SEAT_LIMIT_EXCEEDED`（仅 activate 返回）与 `SEAT_RELEASED`（validate/current 返回）是
  非自动恢复终态：后台循环停摆（tick 特判），SDK 不自动重试/重激活。
- `SEAT_RELEASED` 处置红线：只清除信封与派生缓存（项目/平台配置快照、配置同步水位、
  租户信封缓存），**保留** activation token、客户端私钥（ClientSeed）、SeatNo、ActivationNo
  与状态文件（restore/persist 有对应特判，重启后稳定停在该状态）；恢复只走显式
  `Reactivate`（通知平台重新绑席）或 `Reset`（只清本机态，不通知平台释放席位）。
  validate/current 两条路径必须共用同一处置（`seatReleased()`），清理范围禁止分叉。
- 已知时滞语义：`SEAT_RELEASED` 经 updates/saas/config/events 通道到达时只 fail-closed
  返回错误、不回写 `client.state.Status`，授权状态收敛以 validate 循环为准（最长一个
  刷新周期）。

## 测试与协议一致性

- 纯业务/签名 golden 测试只维护一套；传输测试必须以同一组用例分别跑 HTTP `httptest` 与 gRPC
  `bufconn`，覆盖成功、参数错误、未认证、无权限、模糊 NotFound、业务状态、网络故障和超时。
- 每个新增 SDK 网络方法必须有 HTTP 与 gRPC 双实现测试；只有一侧通过时不得合并。
- 管理面必须验证 JWT、`auth_version`、稳定权限码、数据范围和审计在两种协议下结果一致；
  运行面必须验证 token、时间窗、nonce 防重放、Ed25519 请求签名和离线降级一致。
- protobuf 变更必须执行 lint、生成漂移检查和兼容检查；签名载荷变化必须同步运行跨仓库 golden 向量。
- 禁止联网单元测试；HTTP 使用 `httptest`，gRPC 使用 `bufconn` 或进程内测试服务器。

## 开发与验证命令

当前基线：

```bash
go build ./...
go vet ./...
go test ./...
```

涉及 gRPC 时还必须执行 protobuf lint/generate 漂移检查。若改动同时涉及
`licen-hub/backend`，还必须在该目录执行 `go build ./...`，并运行双协议契约/集成测试。

## 完成定义

一个面向 Licen Hub 的新增或变更能力，只有同时满足以下条件才算完成：

1. 共享业务语义与版本化契约已确定；
2. HTTP 与 gRPC 的服务端、SDK 适配均已实现；
3. 鉴权、权限、数据范围、错误、幂等与审计语义一致；
4. 双协议测试、proto 检查、Go 构建与安全检查全部通过；
5. README、API/契约文档和协议矩阵已同步更新。
