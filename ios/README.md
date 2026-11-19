# ios — inis 对象存储平台 Go SDK

`ios`（Inis Object Storage）是 inis 对象存储平台的官方 Go SDK，一个模块覆盖两个平面：

- **控制面**：密钥自检、桶管理（列表 / 清空）、分享链接、回收站、用量与配额 —— HTTP / gRPC 双链路签名调用（默认 gRPC）
- **数据面**：桶与对象的读写删列、分片上传、预签名链接 —— 直连平台 S3 兼容网关（自实现 SigV4 轻客户端，纯标准库）

- 模块路径：`github.com/inis-io/aide/ios`（独立 Go 模块，与 `aide` 主模块并列）
- 直接依赖：`google.golang.org/grpc`、`google.golang.org/protobuf`（HTTP 链路与数据面零第三方依赖）
- 默认控制面链路：**gRPC**；`WithTransportHTTP()` 一键切换到 HTTP 开放端点

## 1. 安装

```bash
go get github.com/inis-io/aide/ios@latest
```

gRPC 客户端桩已随仓库提交（`proto/gen/ios/open/v1/`），**无需安装 protoc**；契约源文件见 `proto/ios/open/v1/open.proto`。

## 2. 构造客户端

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/inis-io/aide/ios"
)

func main() {

	accessKey := os.Getenv("IOS_ACCESS_KEY") // 平台用户中心签发的 AccessKey
	secret := os.Getenv("IOS_SECRET")        // 对应 Secret，运行时注入

	client, err := ios.NewClient(accessKey, secret,
		ios.WithEndpoint("https://oss.example.com"),
	)
	if err != nil {
		log.Fatalf("构造客户端失败：%v", err)
	}
	defer client.Close() // 释放 gRPC 连接（HTTP 链路与数据面为 no-op）

	_ = context.Background()
}
```

`NewClient` 只做参数校验与链路装配，不发起任何业务请求。`Client` 无内部可变状态，可并发使用。

接入方保存凭据前建议先验证一次有效性（以只读的密钥自检为探针，无副作用）：

```go
info, err := client.VerifyCredentials(context.Background())
if err != nil {
	// ErrInvalidSign / *APIError：凭据无效（密钥不存在、已禁用或签名不符）
	// ErrTransport：平台暂不可达，本次无法判定
	log.Fatalf("凭据验证失败：%v", err)
}
// info.Name / info.Scope / info.ExpiredAt（Secret 永不下发）
```

## 3. 选择链路：gRPC（默认）还是 HTTP（仅控制面）

| 维度 | gRPC（默认） | HTTP |
|---|---|---|
| 端点 | `/ios.open.v1.OpenService/*` | `GET/POST /api/open-accesskey/info`、`/api/open-bucket/list` 等 |
| 开关 | 默认 | `ios.WithTransportHTTP()` |
| 端口 | 与 HTTP 同端口（平台按 HTTP/2 + `application/grpc` 分流），无需额外端口 | 同 HTTP |
| 签名位置 | outgoing metadata `x-ios-sign` | body 的 `sign` 字段 / query 的 `sign` 参数 |
| 传输安全 | 默认 plaintext，生产建议 TLS（见下） | 生产必须 HTTPS |
| 错误模型 | 与 HTTP 完全一致 | 与 gRPC 完全一致 |

两条链路的业务方法、签名器、默认值注入与错误模型完全相同，切换链路不改一行业务代码。数据面（S3 网关）与链路选择无关，永远走独立的数据面端点。

```go
// HTTP 链路
client, err := ios.NewClient(accessKey, secret,
	ios.WithEndpoint("https://oss.example.com"),
	ios.WithTransportHTTP(),
)

// gRPC 链路启用 TLS
client, err = ios.NewClient(accessKey, secret,
	ios.WithEndpoint("oss.example.com:9443"),
	ios.WithGRPCDialOpts(
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})),
	),
)
```

> gRPC 拨号目标由 endpoint 自动归一：剥离 `http://`/`https://` 与路径，缺省端口按 `443` 补全。

## 4. 能力一览

### 控制面（13 个方法，全部签名）

| 方法 | gRPC | HTTP | 说明 |
|---|---|---|---|
| `VerifyCredentials` | `OpenService/GetAccessKeyInfo` | `GET /api/open-accesskey/info` | 密钥自检（探针），返回名称 / scope / 过期时间 |
| `ListBucketInfos` | `OpenService/ListBuckets` | `GET /api/open-bucket/list` | 管理视角桶列表（acl / versioning / status / 存储级别） |
| `EmptyBucket` | `OpenService/EmptyBucket` | `POST /api/open-bucket/empty` | 清空桶（对象软删进回收站，不删桶本身） |
| `CreateShare` | `OpenService/CreateShare` | `POST /api/open-share/create` | 签发分享链接（令牌制，可设有效期） |
| `ListShares` | `OpenService/ListShares` | `GET /api/open-share/list` | 对象分享列表（含已过期项） |
| `RevokeShare` | `OpenService/RevokeShare` | `POST /api/open-share/remove` | 吊销分享（立即失效） |
| `ListRecycle` | `OpenService/ListRecycle` | `GET /api/open-object/recycle` | 回收站列表（分页 + key 模糊） |
| `RestoreObjects` | `OpenService/RestoreObjects` | `POST /api/open-object/restore` | 按对象行 ID 批量还原 |
| `PurgeObjects` | `OpenService/PurgeObjects` | `POST /api/open-object/purge` | 指定释放（硬删，不可恢复） |
| `ClearRecycle` | `OpenService/ClearRecycle` | `POST /api/open-object/clear` | 全部释放（不可恢复） |
| `GetUsageOverview` | `OpenService/GetUsageOverview` | `GET /api/open-usage/overview` | 用量总览 + 有效配额视图 |
| `ListBucketUsage` | `OpenService/ListBucketUsage` | `GET /api/open-usage/buckets` | 按桶用量列表 |
| `GetEgressTrend` | `OpenService/GetEgressTrend` | `GET /api/open-usage/egress-trend` | 出口流量按日序列（近 30 天） |

### 数据面（S3 网关，SigV4 签名）

| 方法 | S3 语义 | 说明 |
|---|---|---|
| `CreateBucket` | PutBucket | 创建桶（桶名全局唯一） |
| `DeleteBucket` | DeleteBucket | 删除桶（桶必须为空） |
| `HeadBucket` | HeadBucket | 探测桶存在与访问权 |
| `ListBuckets` | ListBuckets | 当前 AK 用户的桶（名称 + 创建时间） |
| `PutObject` | PutObject | 简单上传（流式 MD5 与 ETag 回核） |
| `Upload` | 单 PUT / 分片自动编排 | 高级上传（阈值默认 8MB，并发默认 4） |
| `ResumeUpload` | 分片续传编排 | 断点续传（复用进行中会话，已传分片本地跳过） |
| `ListMultipartUploads` | ListMultipartUploads | 进行中分片会话（续传发现 / 遗留治理） |
| `AbortMultipartUpload` | AbortMultipartUpload | 中止分片会话（幂等） |
| `GetObject` | GetObject | 下载（流式，支持 Range） |
| `HeadObject` | HeadObject | 取对象元数据 |
| `DeleteObject` | DeleteObject | 删除对象（软删进回收站） |
| `DeleteObjects` | DeleteObjects | 批量删除（超 1000 键自动分批，单键失败不阻断） |
| `CopyObject` | CopyObject | 服务端拷贝（零流量回传） |
| `ListObjects` | ListObjectsV2 | 浏览对象（prefix / delimiter / token 分页） |
| `PresignGetObject` | 预签名 GET | 本地产链，零网络往返 |
| `PresignPutObject` | 预签名 PUT | 本地产链，零网络往返 |
| `GetBucketCors` / `PutBucketCors` / `DeleteBucketCors` | 桶 CORS 三操作 | 跨域配置（整体覆盖语义） |
| `GetBucketVersioning` / `PutBucketVersioning` | 桶版本控制 | 开启 / 暂停（开启后不可回 off） |
| `GetBucketLifecycle` / `PutBucketLifecycle` / `DeleteBucketLifecycle` | 桶生命周期三操作 | 过期 / 历史版本清理 / 分片中止 |
| `GetSessionToken` | STS GetSessionToken | 签发临时凭证（默认 1 小时，钳制 15 分钟 - 12 小时） |

## 5. 常用示例

### 5.1 建桶 → 上传 → 下载 → 发预签名链接

```go
ctx := context.Background()

// ① 建桶（幂等：桶已存在且属自己时平台返回成功）
if err := client.CreateBucket(ctx, "my-bucket"); err != nil {
	return err
}

// ② 上传（Size ≤ 8MB 走单 PUT，更大自动分片编排）
result, err := client.Upload(ctx, &ios.UploadRequest{
	Bucket: "my-bucket",
	Key:    "docs/report.pdf",
	Body:   file,          // io.Reader
	Size:   size,          // 已知长度；未知传 -1（自动走分片）
	Mime:   "application/pdf",
})
if err != nil {
	return err
}
fmt.Println(result.ETag, result.VersionId)

// ③ 下载（流式；支持 GetOptions.Range 字节区间）
object, err := client.GetObject(ctx, "my-bucket", "docs/report.pdf", nil)
if err != nil {
	return err
}
defer object.Body.Close()
_, err = io.Copy(target, object.Body)

// ④ 预签名下载链接（本地自签，零网络往返；上限 7 天）
url, err := client.PresignGetObject(ctx, "my-bucket", "docs/report.pdf", 24*time.Hour)
if err != nil {
	return err
}
fmt.Println(url)
```

### 5.2 签发分享链接（控制面）

```go
share, err := client.CreateShare(ctx, &ios.ShareCreateRequest{
	Bucket:  "my-bucket",
	Key:     "docs/report.pdf",
	Expires: 7 * 24 * 3600, // 秒；0 = 永久；负值由 SDK 归一为 0
})
if err != nil {
	return err
}
fmt.Println(share.Share.Token) // 访问令牌；完整链接 = 平台地址 + /share/{token}
```

### 5.3 用量总览与配额核对

```go
overview, err := client.GetUsageOverview(ctx)
if err != nil {
	return err
}
fmt.Println(overview.StorageBytes, overview.ObjectCount, overview.EgressBytes)
fmt.Println(overview.Quota.MaxStorageBytes, overview.Quota.MonthlyBandwidthBytes) // 0 = 不限
```

### 5.4 回收站：列出 → 还原

```go
page, err := client.ListRecycle(ctx, &ios.RecycleListRequest{
	Bucket: "my-bucket",
	// Page: 1, Limit: 20 —— 缺省由 SDK 注入
})
if err != nil {
	return err
}
for _, entry := range page.Entries {
	fmt.Println(entry.Id, entry.Key, entry.Size, entry.DeletedAt)
}

restored, err := client.RestoreObjects(ctx, []int64{page.Entries[0].Id})
if err != nil {
	return err
}
fmt.Println(restored.Restored) // 实际还原条数
```

### 5.5 STS 临时凭证下发（P2）

用永久凭证签发临时凭证，下发给不可信环境（边缘节点 / 合作方）做限期数据面操作：

```go
creds, err := client.GetSessionToken(ctx, 2*time.Hour) // 默认 1 小时，钳制 15 分钟 - 12 小时
if err != nil {
	return err
}

// 临时凭证仅数据面可用（控制面签名只收永久凭证）
temp, err := ios.NewClient(creds.AccessKey, creds.Secret,
	ios.WithEndpoint("https://oss.example.com"),
	ios.WithSessionToken(creds.Token),
)
if err != nil {
	return err
}
defer temp.Close()

objects, err := temp.ListObjects(ctx, &ios.ListRequest{Bucket: "my-bucket"})
```

### 5.6 拷贝、批量删除与断点续传（P2）

```go
// 服务端拷贝（零流量回传；源与目标须同在该 AK 授权范围内）
copied, err := client.CopyObject(ctx, "my-bucket", "backup/report.pdf", "my-bucket", "docs/report.pdf")

// 批量删除（超 1000 键自动分批；单键失败不阻断，逐键回传）
removed, err := client.DeleteObjects(ctx, "my-bucket", []string{"tmp/a.bin", "tmp/b.bin"})
for _, failure := range removed.Errors {
	fmt.Println("删除失败：", failure.Key, failure.Code, failure.Message)
}

// 断点续传：进程中断后重进，自动复用进行中分片会话，已传分片本地跳过
resumed, err := client.ResumeUpload(ctx, &ios.UploadRequest{
	Bucket: "my-bucket", Key: "big.iso", Body: file, Size: size,
})

// 遗留会话治理（异常中断现场清理）
pending, err := client.ListMultipartUploads(ctx, "my-bucket", "")
for _, session := range pending {
	_ = client.AbortMultipartUpload(ctx, "my-bucket", session.Key, session.UploadId)
}
```

### 5.7 桶配置族（P2）

```go
// 版本控制（开启后不可回 off，只可暂停）
_ = client.PutBucketVersioning(ctx, "my-bucket", ios.VersioningEnabled)

// CORS（整体覆盖语义）
_ = client.PutBucketCors(ctx, "my-bucket", []ios.CORSRule{{
	AllowedOrigins: []string{"https://app.example.com"},
	AllowedMethods: []string{"GET", "PUT"},
	AllowedHeaders: []string{"*"},
	MaxAgeSeconds:  600,
}})

// 生命周期（整体覆盖语义；日志前缀 30 天过期、历史版本 7 天清理、碎片分片 3 天中止）
_ = client.PutBucketLifecycle(ctx, "my-bucket", []ios.LifecycleRule{{
	ID:                  "logs-rotate",
	Status:              "Enabled",
	Prefix:              "logs/",
	ExpirationDays:      30,
	NoncurrentDays:      7,
	AbortIncompleteDays: 3,
}})
```

## 6. 数据面（S3）要点

- **双端点**：控制面走 `WithEndpoint`；数据面走 `WithS3Endpoint`，缺省时从控制面端点派生（`scheme://host:9000`，IPv6 自动加方括号）。网关端口不是 9000 时必须显式注入 `WithS3Endpoint`。
- **UNSIGNED-PAYLOAD**：请求体不参与签名（网关侧兼容 aws-chunked 与明文流），上传大文件不做全量哈希预读，流式直通；完整性由流式 MD5 与响应 ETag 回核兜底。
- **分片上传**：`Upload` 按 `WithPartSize`（默认 8MB，平台约束单片 5MB 起，自动上调保证片数 ≤ 10000）与 `WithConcurrency`（默认 4）编排；顺序读片、有界并发、任一失败自动 Abort 清理。
- **预签名**：本地产链零往返，有效期硬上限 7 天（SigV4 约束），超上限返回 `ErrInvalidRequest`。
- **断点续传**：`ResumeUpload` 发现同桶同键的进行中分片会话即复用——已传分片信任网关记录（大小对齐布局即采纳），本地按布局跳过字节区间（`io.ReadSeeker` 直接 Seek，否则读丢弃），只补传缺失分片；布局不一致（PartSize 变化 / 键被其他会话占用）返回 `ErrInvalidRequest`，可先 `AbortMultipartUpload` 再重传。调用方必须从头提供完整内容流。
- **STS**：`GetSessionToken` 以永久凭证签发临时凭证（默认 1 小时，钳制 15 分钟 - 12 小时）；临时凭证**仅数据面可用**（控制面签名只收永久凭证），用 `WithSessionToken` 构造新客户端使用；临时凭证不可再签发（防令牌自我续期）。
- **区域**：默认 `us-east-1`，须与平台网关配置一致，不一致用 `WithRegion` 对齐（签名域不同必然 SignatureDoesNotMatch）。

## 7. 错误处理

```go
err := client.EmptyBucket(ctx, "my-bucket")
switch {
case err == nil:
	// 成功
case errors.Is(err, ios.ErrInvalidRequest):
	// 本地参数问题，请求未发出（或链路适配失败）
case errors.Is(err, ios.ErrInvalidSign):
	// 签名问题：核对 Secret、参与签名的默认值与时间窗
case errors.Is(err, ios.ErrTransport):
	// 网络 / 连接 / 非业务 RPC 失败（文案内带 gRPC 状态码）
default:
	var apiErr *ios.APIError
	if errors.As(err, &apiErr) {
		// 平台业务错误：apiErr.Code（200 以外均为失败）、apiErr.Msg（中文文案）、apiErr.Raw（HTTP 原始报文）
	}
}
```

控制面 HTTP 状态恒为 `200`，业务结果只看信封 `code`：

| 业务码 | 含义 |
|---|---|
| `200` | 成功 |
| `400` | 参数错误 |
| `401` | 签名缺失 / 无效 / 超时间窗（HTTP 链路签名错误归业务码，不归 `ErrInvalidSign`） |
| `403` | scope 越权（ro 密钥写操作、限定授权跨桶）、账号封禁 |
| `404` | 资源不存在（含非属主访问，防桶名探测） |
| `500` | 平台内部错误 |

> gRPC 链路：平台验签失败返回 `Unauthenticated` → SDK 归为 `ErrInvalidSign`；其余业务失败仍为 `OK + Envelope.code != 200` → `*APIError`。

数据面错误是 S3 网关返回的 XML 错误体，SDK 解为 `*ios.S3Error`（`Code` / `Message` / `StatusCode`）：

```go
_, err = client.HeadObject(ctx, "my-bucket", "missing.pdf")
var s3Err *ios.S3Error
if errors.As(err, &s3Err) && s3Err.IsNotFound() {
	// 对象不存在
}
```

## 8. 签名与默认值（控制面，务必了解）

签名算法与平台服务端 `utils.Md5.GenSign` **逐字节一致**：

```
1. 参数集（HTTP：请求结构体按 JSON tag 展开；gRPC：请求消息按 protojson 展开，键名均为 camelCase）
2. 剔除 sign 键
3. 剔除「字符串形式为空」的值（空串、null）
4. 剩余键按字典序排序，拼成 k1=v1&k2=v2
5. 末尾追加 &secret=<Secret>
6. MD5 → 十六进制 → 大写（32 位）
```

三个实践要点：

- **数值 `0` 与 `false` 会参与签名**（格式化为 `"0"`/`"false"`），只有空串与 `null` 被剔除。gRPC 链路因此固定 `protojson.MarshalOptions{EmitUnpopulated: true}` 展开请求，与平台拦截器参数集保持一致。
- **默认值由 SDK 补齐后再签名**：`CreateShare` 的 `Expires < 0` 归一为 `0`；`ListRecycle` 缺省 `Page=1`、`Limit=20`（服务端封顶 200）。平台服务端同样先注入默认值再验签，缺一项必然验签失败。
- **毫秒时间戳不落地**：控制面请求不带调用方时间戳字段，时间窗校验由平台对 `timestamp` 参数（SDK 自动注入当前毫秒）做 ±15 分钟判定，防重放。

需要切换签名协议（如平台升级 HMAC-SHA256）时用 `ios.WithSigner(...)` 注入新实现，业务代码零改动。

## 9. 安全约定

- `secret` 由调用方**运行时注入**（环境变量 / 配置中心 / 密钥管理服务），SDK 不持久化、不写日志、不出现在任何错误文案中。
- 生产环境 HTTP 链路必须 HTTPS；gRPC 链路默认 plaintext 仅适用于内网/开发环境，生产用 `WithGRPCDialOpts` 注入 TLS 凭证。
- 签名时间窗 ±15 分钟（平台判定），请保证部署机器时钟同步；偏差过大一律 401。
- 按最小授权在平台用户中心签发密钥：只读场景用 `scope=ro`，限定桶场景配置桶白名单；写操作与跨桶访问会被平台 403 拒绝。
- 不要把 `secret` 下发给前端/客户端代码；预签名链接才是给终端用户的下发形态。

## 10. 常见问题

**Q：一直返回 401（签名无效）怎么办？**
A：逐项核对 ① `secret` 是否与平台签发的密钥一致；② 机器时钟偏差是否超过 ±15 分钟；③ 是否自行构造了请求而漏了 SDK 注入的默认值（用 SDK 方法调用则无需关心）；④ 自建网关时确认参数集展开方式（gRPC 必须 `EmitUnpopulated`）。

**Q：数据面报 SignatureDoesNotMatch？**
A：先核对 `WithRegion` 与平台网关区域一致（默认 `us-east-1`），再确认 `WithS3Endpoint` 指向的是 S3 网关端口而不是控制面端口。

**Q：S3 端点派生出来的端口不对？**
A：派生规则固定为 `scheme://host:9000`。网关不在 9000 时必须显式 `WithS3Endpoint("https://s3.example.com:9443")`。

**Q：能并发调用吗？**
A：可以。`Client` 无内部可变状态，`Signer` 是无状态纯函数；`Close()` 只在退出时调用一次（重复调用幂等）。

**Q：控制面超时多久？数据面呢？**
A：控制面单次调用默认 10 秒（`WithTimeout` 可调）。数据面是流式链路，不套整体超时，用 `ctx` 全权控制。

## 11. 版本与演进

- P2 已落地：数据面 `ResumeUpload` 断点续传（含 `ListMultipartUploads` / `AbortMultipartUpload` 会话治理）、`DeleteObjects` 批量删除、`CopyObject` 服务端拷贝、STS `GetSessionToken`（`WithSessionToken` 全链路）、桶配置族（CORS / Versioning / Lifecycle）。
- P3 规划：事件通知 `notify` 子包（解析 + 验签）、`PutBucketNotification` 封装、更多示例（备份脚本、静态资源同步、CI 产物上传）。
- 契约源文件 `proto/ios/open/v1/open.proto` 是控制面唯一事实源；平台后端复制同一份文件生成服务端桩。
- SDK 方法签名稳定，能力上线后以新增方法方式扩展（不破坏现有接口）。
- 全链联调 demo：`examples/fullchain`（设置 `IOS_ENDPOINT` / `IOS_ACCESS_KEY` / `IOS_SECRET` 后 `go run ./examples/fullchain`）。

## 12. 代码结构

接入方只需 `import "github.com/inis-io/aide/ios"` 一个包：根包用类型别名与函数转发导出全部公开面，
内部实现按职责分区（只为可维护性，不增加接入成本）。

| 路径 | 作用 |
|---|---|
| `ios.go` | 唯一入口：包注释 + `Client` / `Option` / `NewClient` / `Close` + 控制面 13 方法 + 数据面 12 方法 + 全部别名与转发 |
| `types/` | 控制面公开数据模型与错误模型（`Normalize` 默认值、`APIError`、哨兵错误；零依赖） |
| `sign/` | 公开控制面签名器：`Signer` 接口 + MD5 实现（`sign.New()`），供 `WithSigner` 使用 |
| `internal/transport/` | 内部控制面传输链路（HTTP / gRPC 双链路实现与 `Operation` 常量），接入方不直接使用 |
| `internal/s3/` | 内部数据面轻客户端（SigV4 签名、桶/对象/分片/续传/批量/拷贝/桶配置/STS，纯标准库） |
| `proto/` | 控制面契约源 `ios/open/v1/open.proto` 与生成的客户端桩（已入库，接入方免装 protoc） |
| `examples/fullchain/` | 全链联调 demo（建桶→上传→下载→分享→用量→回收站全链，含 P2 能力演示） |

## 13. 相关文档

- 模块 AI 约定：[AGENTS.md](AGENTS.md)
- 包注释与 API 文档：`go doc github.com/inis-io/aide/ios`
- 设计方案：`object-storage` 仓库 `docs/plan/07-ios-sdk设计方案.md`（平台仓库）
