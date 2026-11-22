// Package transport - ios SDK 的内部传输链路实现（HTTP / gRPC，控制面）。
//
// 本包是内部实现细节：调用方不直接接触，统一经根包 ios.Client 使用。
// 两条链路共用 sign.Signer 与 types 的默认值注入（types.*.Normalize），
// 因此对同一逻辑请求产出同一签名；Operation 是链路无关的稳定操作名。
package transport

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"github.com/inis-io/aide/ios/sign"
)

// ============================= 传输层抽象 - 开始 =============================

// Config - 传输链路装配参数（由根包 Client 填充）
type Config struct {
	// Endpoint - 平台地址（如 https://storage.example.com，末尾斜杠已裁剪）
	Endpoint string
	// AccessKey - 访问密钥 AK（HTTP 链路进 body/query，gRPC 链路进消息体与 metadata）
	AccessKey string
	// Secret - 密钥 SK（仅本地签名使用）
	Secret string
	// Signer - 签名器（nil 时回退 MD5）
	Signer sign.Signer
	// Timeout - 单次调用超时（HTTP 链路用作内置客户端超时）
	Timeout time.Duration
	// UserAgent - 请求 User-Agent
	UserAgent string
	// HTTPClient - HTTP 链路自定义客户端（nil 时按 Timeout 内置）
	HTTPClient *http.Client
	// DialOpts - gRPC 拨号选项（为空时按端点 scheme 选择缺省凭证：https → TLS，其余 → plaintext）
	DialOpts []grpc.DialOption
}

// Operation - 稳定操作名（链路无关；HTTP 映射到 method+path，gRPC 映射到 RPC 方法）
type Operation string

const (
	// OpAccessKeyVerify - 凭据验证/密钥自检（GET /api/open-accesskey/verify；OpenService/VerifyAccessKey）
	OpAccessKeyVerify Operation = "accesskey.verify"
	// OpBucketInfoList - 管理视角桶列表（GET /api/open-bucket/find；OpenService/ListBucketInfos）
	OpBucketInfoList Operation = "bucket.info.list"
	// OpBucketEmpty - 清空桶（DELETE /api/open-bucket/empty；OpenService/EmptyBucket）
	OpBucketEmpty Operation = "bucket.empty"
	// OpShareCreate - 签发分享链接（POST /api/open-share/create；OpenService/CreateShare）
	OpShareCreate Operation = "share.create"
	// OpShareList - 对象分享列表（GET /api/open-share/find；OpenService/ListShares）
	OpShareList Operation = "share.list"
	// OpShareRevoke - 吊销分享（DELETE /api/open-share/remove；OpenService/RevokeShare）
	OpShareRevoke Operation = "share.revoke"
	// OpRecycleList - 回收站列表（GET /api/open-object/recycle；OpenService/ListRecycle）
	OpRecycleList Operation = "recycle.list"
	// OpRecycleRestore - 回收站还原（PUT /api/open-object/restore；OpenService/RestoreObjects）
	OpRecycleRestore Operation = "recycle.restore"
	// OpRecyclePurge - 回收站指定释放（DELETE /api/open-object/purge；OpenService/PurgeObjects）
	OpRecyclePurge Operation = "recycle.purge"
	// OpRecycleClear - 回收站全部释放（DELETE /api/open-object/clear；OpenService/ClearRecycle）
	OpRecycleClear Operation = "recycle.clear"
	// OpUsageOverview - 用量总览（GET /api/open-usage/overview；OpenService/GetUsageOverview）
	OpUsageOverview Operation = "usage.overview"
	// OpUsageBucketList - 按桶用量列表（GET /api/open-usage/find；OpenService/ListBucketUsage）
	OpUsageBucketList Operation = "usage.bucket.list"
	// OpUsageEgressTrend - 出口流量按日序列（GET /api/open-usage/trend；OpenService/GetEgressTrend）
	OpUsageEgressTrend Operation = "usage.egress.trend"
)

// Transport - 传输链路抽象（HTTP / gRPC）
// 业务方法只负责组装强类型请求并调用 Invoke，链路差异全部收敛在实现内。
type Transport interface {
	// Invoke - 调用指定能力；req/resp 为各操作的强类型请求/响应
	Invoke(ctx context.Context, op Operation, req, resp any) error
	// Close - 释放底层连接（HTTP 为 no-op，gRPC 关闭 ClientConn）
	Close() error
}

// dataUnmarshaler - 链路适配解码器
// HTTP 信封 data 结构与 SDK 响应模型的字段名不完全一致时，
// 由响应类型自行实现该接口完成映射；未实现时按标准 json 解码。
type dataUnmarshaler interface {
	// UnmarshalData - 将信封 data 原始 JSON 解码进响应模型
	UnmarshalData(raw []byte) error
}

const (
	// accessKeyField - AccessKey 键名（HTTP 链路须进 body/query，gRPC 链路在消息体内）
	accessKeyField = "accessKey"
	// timestampField - 毫秒时间戳键名（参与签名，服务端 ±15 分钟窗口防重放）
	timestampField = "timestamp"
)

// ============================= 传输层抽象 - 结束 =============================
