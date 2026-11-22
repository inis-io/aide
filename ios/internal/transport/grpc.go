package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/inis-io/aide/ios/sign"
	"github.com/inis-io/aide/ios/types"

	openv1 "github.com/inis-io/aide/ios/proto/gen/ios/open/v1"
)

// ============================= gRPC 链路 - 开始 =============================

const (
	// metadataSign - 请求签名 metadata 键（值 = 请求消息 protojson → map → MD5 大写签名）
	metadataSign = "x-ios-sign"
	// metadataAccessKey - AK metadata 键（AK 同时位于消息体内，此处供服务端拦截器取证与日志）
	metadataAccessKey = "x-ios-access-key"
	// defaultGRPCPort - 拨号目标缺省端口（未显式指定端口时按 443 补全）
	defaultGRPCPort = "443"
)

// grpcOp - gRPC 链路操作描述符
type grpcOp struct {
	// method - 完整 RPC 方法名（/ios.open.v1.OpenService/Xxx）
	method string
	// request - 由 SDK 请求构造 proto 请求消息（含服务端默认值注入与时间戳）
	request func(accessKey string, timestamp int64, req any) (proto.Message, error)
	// response - 构造空的 proto 响应消息
	response func() proto.Message
	// fill - 将 proto 响应消息转换为 SDK 响应模型（信封校验已在外层完成）
	fill func(message proto.Message, resp any) error
}

// grpcTransport - gRPC 链路实现
// 与 HTTP 链路共用同一个 sign.Signer 与同一批 Normalize() 默认值逻辑：
// 请求消息经 protojson 展开为 map 后签名，写入 outgoing metadata x-ios-sign。
type grpcTransport struct {
	// conn - gRPC 连接（惰性建链）
	conn *grpc.ClientConn
	// accessKey - 访问密钥 AK
	accessKey string
	// secret - 密钥 SK
	secret string
	// signer - 签名器
	signer sign.Signer
	// operations - 操作描述符表
	operations map[Operation]grpcOp
	// now - 时间戳来源（测试可注入）
	now func() time.Time
	// once - 保证连接只关闭一次（重复 Close 幂等）
	once sync.Once
	// closeErr - 首次关闭的结果
	closeErr error
}

// NewGRPC - 装配 gRPC 链路（不主动建链，首个请求才真正连接）
func NewGRPC(config Config) (Transport, error) {

	signer := config.Signer
	if signer == nil {
		signer = sign.New()
	}

	dialOpts := []grpc.DialOption{grpc.WithUserAgent(config.UserAgent)}
	if len(config.DialOpts) == 0 {
		// 缺省凭证按端点 scheme 选择：https 用 TLS（grpc-go 自动协商 h2），
		// 其余（http/无 scheme）维持 plaintext（h2c），对齐本地同端口分流部署
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(defaultCredentials(config.Endpoint)))
	} else {
		dialOpts = append(dialOpts, config.DialOpts...)
	}

	conn, err := grpc.NewClient(GRPCTarget(config.Endpoint), dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w：gRPC 连接创建失败（%v）", types.ErrTransport, err)
	}

	return &grpcTransport{
		conn:       conn,
		accessKey:  config.AccessKey,
		secret:     config.Secret,
		signer:     signer,
		operations: grpcOperations(),
		now:        time.Now,
	}, nil
}

// Invoke - 发起 gRPC 调用：构造消息 → protojson 签名 → 写 metadata → 调用 → 解信封 → 转模型
func (this *grpcTransport) Invoke(ctx context.Context, op Operation, req, resp any) error {

	descriptor, ok := this.operations[op]
	if !ok {
		return fmt.Errorf("%w：未知操作 %s", types.ErrInvalidRequest, op)
	}

	message, err := descriptor.request(this.accessKey, this.now().UnixMilli(), req)
	if err != nil {
		return err
	}

	signature, err := this.signature(message)
	if err != nil {
		return err
	}

	ctx = metadata.AppendToOutgoingContext(ctx, metadataAccessKey, this.accessKey, metadataSign, signature)

	reply := descriptor.response()
	if err = this.conn.Invoke(ctx, descriptor.method, message, reply); err != nil {
		return grpcError(err)
	}

	if err = checkEnvelope(reply); err != nil {
		return err
	}

	// 与 HTTP 链路对称：调用方不关心响应体时跳过模型转换
	if resp == nil {
		return nil
	}

	return descriptor.fill(reply, resp)
}

// Close - 关闭 gRPC 连接（幂等，重复调用返回首次结果）
func (this *grpcTransport) Close() error {

	if this == nil || this.conn == nil {
		return nil
	}

	this.once.Do(func() { this.closeErr = this.conn.Close() })

	return this.closeErr
}

// signature - 对请求消息生成签名（protojson 展开为 map 后走同一签名器）
// 必须 EmitUnpopulated：proto3 零值字段（如 expires=0）默认会被 protojson 丢弃，
// 而 HTTP 链路结构体展开包含全部字段（0 参与签名、空串被剔除）；
// 只有显式发出零值字段，两条链路才对同一逻辑请求产出同一签名（黄金向量锚定）。
func (this *grpcTransport) signature(message proto.Message) (string, error) {

	text, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("%w：gRPC 请求序列化失败（%v）", types.ErrInvalidRequest, err)
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return "", fmt.Errorf("%w：gRPC 请求参数解析失败（%v）", types.ErrInvalidRequest, err)
	}

	return this.signer.Sign(params, this.secret), nil
}

// defaultCredentials - 缺省传输凭证（未显式传入拨号选项时按端点 scheme 选择）
// https 端点回退 TLS（grpc-go 自动带 h2 ALPN）；http/无 scheme 端点回退 plaintext（h2c），对齐本地开发部署
func defaultCredentials(endpoint string) credentials.TransportCredentials {

	scheme := strings.TrimSpace(endpoint)
	if index := strings.Index(scheme, "://"); index >= 0 {
		if strings.EqualFold(scheme[:index], "https") {
			return credentials.NewTLS(&tls.Config{})
		}
	}

	return insecure.NewCredentials()
}

// GRPCTarget - 平台地址归一为拨号目标（剥离 scheme 与路径，缺省端口按 443 补全）
func GRPCTarget(endpoint string) string {

	target := strings.TrimSpace(endpoint)
	if index := strings.Index(target, "://"); index >= 0 {
		target = target[index+3:]
	}
	if index := strings.IndexAny(target, "/?#"); index >= 0 {
		target = target[:index]
	}

	// host 无端口时补缺省端口；IPv6 字面量（形如 [::1]:2000）已含冒号，不参与改写
	if !strings.Contains(target, ":") {
		target += ":" + defaultGRPCPort
	}

	return target
}

// grpcError - RPC 状态错误归一（Unauthenticated → ErrInvalidSign，其余 → ErrTransport）
func grpcError(err error) error {

	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w：gRPC 调用失败（%v）", types.ErrTransport, err)
	}

	if st.Code() == codes.Unauthenticated {
		return fmt.Errorf("%w：平台拒绝该请求签名（%s）：%s", types.ErrInvalidSign, st.Code(), st.Message())
	}

	return fmt.Errorf("%w：gRPC 调用失败（%s）：%s", types.ErrTransport, st.Code(), st.Message())
}

// checkEnvelope - 校验响应信封（code!=200 → *types.APIError）
func checkEnvelope(message proto.Message) error {

	enveloped, ok := message.(interface {
		GetEnvelope() *openv1.Envelope
	})
	if !ok {
		return fmt.Errorf("%w：gRPC 响应消息缺少信封字段", types.ErrTransport)
	}

	envelope := enveloped.GetEnvelope()
	if envelope == nil {
		return fmt.Errorf("%w：gRPC 响应消息缺少信封字段", types.ErrTransport)
	}

	if envelope.GetCode() != 200 {
		return &types.APIError{Code: int(envelope.GetCode()), Msg: envelope.GetMsg()}
	}

	return nil
}

// typeError - 请求/响应类型断言失败的统一错误
func typeError(name string) error {
	return fmt.Errorf("%w：%s类型不匹配", types.ErrInvalidRequest, name)
}

// grpcOperations - 构建 操作 → RPC 描述符 表
func grpcOperations() map[Operation]grpcOp {

	return map[Operation]grpcOp{

		OpAccessKeyVerify: {
			method: openv1.OpenService_VerifyAccessKey_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				return &openv1.VerifyAccessKeyRequest{AccessKey: accessKey, Timestamp: timestamp}, nil
			},
			response: func() proto.Message { return &openv1.VerifyAccessKeyResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.AccessKeyInfo)
				if !ok || out == nil {
					return typeError("凭据验证响应")
				}
				reply, ok := message.(*openv1.VerifyAccessKeyResponse)
				if !ok {
					return fmt.Errorf("%w：凭据验证响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoAccessKeyInfo(reply.GetInfo())
				return nil
			},
		},

		OpBucketInfoList: {
			method: openv1.OpenService_ListBucketInfos_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				return &openv1.ListBucketInfosRequest{AccessKey: accessKey, Timestamp: timestamp}, nil
			},
			response: func() proto.Message { return &openv1.ListBucketInfosResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*[]types.BucketInfo)
				if !ok || out == nil {
					return typeError("桶列表响应")
				}
				reply, ok := message.(*openv1.ListBucketInfosResponse)
				if !ok {
					return fmt.Errorf("%w：桶列表响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoBucketInfos(reply.GetBuckets())
				return nil
			},
		},

		OpBucketEmpty: {
			method: openv1.OpenService_EmptyBucket_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.EmptyBucketRequest)
				if !ok || in == nil {
					return nil, typeError("清空桶请求")
				}
				return &openv1.EmptyBucketRequest{AccessKey: accessKey, Timestamp: timestamp, Bucket: in.Bucket}, nil
			},
			response: func() proto.Message { return &openv1.EmptyBucketResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.EmptyBucketResult)
				if !ok || out == nil {
					return typeError("清空桶响应")
				}
				reply, ok := message.(*openv1.EmptyBucketResponse)
				if !ok {
					return fmt.Errorf("%w：清空桶响应类型不匹配", types.ErrTransport)
				}
				out.Removed = reply.GetRemoved()
				return nil
			},
		},

		OpShareCreate: {
			method: openv1.OpenService_CreateShare_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.ShareCreateRequest)
				if !ok || in == nil {
					return nil, typeError("签发分享请求")
				}
				value := *in
				value.Normalize()
				return &openv1.CreateShareRequest{
					AccessKey: accessKey, Timestamp: timestamp,
					Bucket: value.Bucket, Key: value.Key, Expires: value.Expires,
				}, nil
			},
			response: func() proto.Message { return &openv1.CreateShareResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.ShareCreateResult)
				if !ok || out == nil {
					return typeError("签发分享响应")
				}
				reply, ok := message.(*openv1.CreateShareResponse)
				if !ok {
					return fmt.Errorf("%w：签发分享响应类型不匹配", types.ErrTransport)
				}
				out.Share = fromProtoShare(reply.GetShare())
				out.Reused = reply.GetReused()
				return nil
			},
		},

		OpShareList: {
			method: openv1.OpenService_ListShares_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.ShareListRequest)
				if !ok || in == nil {
					return nil, typeError("分享列表请求")
				}
				return &openv1.ListSharesRequest{AccessKey: accessKey, Timestamp: timestamp, Bucket: in.Bucket, Key: in.Key}, nil
			},
			response: func() proto.Message { return &openv1.ListSharesResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*[]types.Share)
				if !ok || out == nil {
					return typeError("分享列表响应")
				}
				reply, ok := message.(*openv1.ListSharesResponse)
				if !ok {
					return fmt.Errorf("%w：分享列表响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoShares(reply.GetShares())
				return nil
			},
		},

		OpShareRevoke: {
			method: openv1.OpenService_RevokeShare_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.ShareRevokeRequest)
				if !ok || in == nil {
					return nil, typeError("吊销分享请求")
				}
				return &openv1.RevokeShareRequest{AccessKey: accessKey, Timestamp: timestamp, Id: in.Id}, nil
			},
			response: func() proto.Message { return &openv1.RevokeShareResponse{} },
			fill: func(message proto.Message, resp any) error {
				return nil
			},
		},

		OpRecycleList: {
			method: openv1.OpenService_ListRecycle_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.RecycleListRequest)
				if !ok || in == nil {
					return nil, typeError("回收站列表请求")
				}
				value := *in
				value.Normalize()
				return &openv1.ListRecycleRequest{
					AccessKey: accessKey, Timestamp: timestamp,
					Bucket: value.Bucket, Keyword: value.Keyword,
					Page: int32(value.Page), Limit: int32(value.Limit),
				}, nil
			},
			response: func() proto.Message { return &openv1.ListRecycleResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.RecycleListResult)
				if !ok || out == nil {
					return typeError("回收站列表响应")
				}
				reply, ok := message.(*openv1.ListRecycleResponse)
				if !ok {
					return fmt.Errorf("%w：回收站列表响应类型不匹配", types.ErrTransport)
				}
				out.Entries = fromProtoRecycleEntries(reply.GetEntries())
				out.Total = reply.GetTotal()
				return nil
			},
		},

		OpRecycleRestore: {
			method: openv1.OpenService_RestoreObjects_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.RecycleRestoreRequest)
				if !ok || in == nil {
					return nil, typeError("回收站还原请求")
				}
				return &openv1.RestoreObjectsRequest{AccessKey: accessKey, Timestamp: timestamp, Ids: types.JoinIds(in.Ids)}, nil
			},
			response: func() proto.Message { return &openv1.RestoreObjectsResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.RecycleRestoreResult)
				if !ok || out == nil {
					return typeError("回收站还原响应")
				}
				reply, ok := message.(*openv1.RestoreObjectsResponse)
				if !ok {
					return fmt.Errorf("%w：回收站还原响应类型不匹配", types.ErrTransport)
				}
				out.Restored = reply.GetRestored()
				out.Conflicts = append([]string{}, reply.GetConflicts()...)
				return nil
			},
		},

		OpRecyclePurge: {
			method: openv1.OpenService_PurgeObjects_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.RecyclePurgeRequest)
				if !ok || in == nil {
					return nil, typeError("回收站释放请求")
				}
				return &openv1.PurgeObjectsRequest{AccessKey: accessKey, Timestamp: timestamp, Ids: types.JoinIds(in.Ids)}, nil
			},
			response: func() proto.Message { return &openv1.PurgeObjectsResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.RecyclePurgeResult)
				if !ok || out == nil {
					return typeError("回收站释放响应")
				}
				reply, ok := message.(*openv1.PurgeObjectsResponse)
				if !ok {
					return fmt.Errorf("%w：回收站释放响应类型不匹配", types.ErrTransport)
				}
				out.Purged = reply.GetPurged()
				return nil
			},
		},

		OpRecycleClear: {
			method: openv1.OpenService_ClearRecycle_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				in, ok := req.(*types.RecycleClearRequest)
				if !ok || in == nil {
					return nil, typeError("回收站清空请求")
				}
				return &openv1.ClearRecycleRequest{AccessKey: accessKey, Timestamp: timestamp, Bucket: in.Bucket}, nil
			},
			response: func() proto.Message { return &openv1.ClearRecycleResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.RecyclePurgeResult)
				if !ok || out == nil {
					return typeError("回收站清空响应")
				}
				reply, ok := message.(*openv1.ClearRecycleResponse)
				if !ok {
					return fmt.Errorf("%w：回收站清空响应类型不匹配", types.ErrTransport)
				}
				out.Purged = reply.GetPurged()
				return nil
			},
		},

		OpUsageOverview: {
			method: openv1.OpenService_GetUsageOverview_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				return &openv1.GetUsageOverviewRequest{AccessKey: accessKey, Timestamp: timestamp}, nil
			},
			response: func() proto.Message { return &openv1.GetUsageOverviewResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.UsageOverview)
				if !ok || out == nil {
					return typeError("用量总览响应")
				}
				reply, ok := message.(*openv1.GetUsageOverviewResponse)
				if !ok {
					return fmt.Errorf("%w：用量总览响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoUsageOverview(reply.GetOverview())
				return nil
			},
		},

		OpUsageBucketList: {
			method: openv1.OpenService_ListBucketUsage_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				return &openv1.ListBucketUsageRequest{AccessKey: accessKey, Timestamp: timestamp}, nil
			},
			response: func() proto.Message { return &openv1.ListBucketUsageResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*[]types.BucketUsage)
				if !ok || out == nil {
					return typeError("按桶用量响应")
				}
				reply, ok := message.(*openv1.ListBucketUsageResponse)
				if !ok {
					return fmt.Errorf("%w：按桶用量响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoBucketUsages(reply.GetRows())
				return nil
			},
		},

		OpUsageEgressTrend: {
			method: openv1.OpenService_GetEgressTrend_FullMethodName,
			request: func(accessKey string, timestamp int64, req any) (proto.Message, error) {
				return &openv1.GetEgressTrendRequest{AccessKey: accessKey, Timestamp: timestamp}, nil
			},
			response: func() proto.Message { return &openv1.GetEgressTrendResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*[]types.EgressPoint)
				if !ok || out == nil {
					return typeError("流量趋势响应")
				}
				reply, ok := message.(*openv1.GetEgressTrendResponse)
				if !ok {
					return fmt.Errorf("%w：流量趋势响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoEgressPoints(reply.GetPoints())
				return nil
			},
		},
	}
}

// ============================= proto → SDK 模型 - 开始 =============================

// fromProtoAccessKeyInfo - proto 密钥自检信息 → SDK 密钥自检信息
func fromProtoAccessKeyInfo(source *openv1.AccessKeyInfo) types.AccessKeyInfo {

	if source == nil {
		return types.AccessKeyInfo{}
	}

	return types.AccessKeyInfo{
		Name:       source.GetName(),
		Status:     source.GetStatus(),
		Scope:      source.GetScope(),
		LastUsedAt: source.GetLastUsedAt(),
		ExpireAt:   source.GetExpireAt(),
		CreateAt:   source.GetCreateAt(),
	}
}

// fromProtoBucketInfos - proto 桶列表 → SDK 桶列表
func fromProtoBucketInfos(source []*openv1.BucketInfo) []types.BucketInfo {

	if len(source) == 0 {
		return nil
	}

	buckets := make([]types.BucketInfo, 0, len(source))
	for _, item := range source {
		buckets = append(buckets, types.BucketInfo{
			Name:         item.GetName(),
			Region:       item.GetRegion(),
			Acl:          item.GetAcl(),
			Versioning:   item.GetVersioning(),
			Status:       item.GetStatus(),
			StorageClass: item.GetStorageClass(),
			CreateAt:     item.GetCreateAt(),
			UpdateAt:     item.GetUpdateAt(),
		})
	}

	return buckets
}

// fromProtoShare - proto 分享 → SDK 分享
func fromProtoShare(source *openv1.Share) types.Share {

	if source == nil {
		return types.Share{}
	}

	return types.Share{
		Id:       source.GetId(),
		Bucket:   source.GetBucket(),
		Key:      source.GetKey(),
		Token:    source.GetToken(),
		ExpireAt: source.GetExpireAt(),
		Visits:   source.GetVisits(),
		CreateAt: source.GetCreateAt(),
	}
}

// fromProtoShares - proto 分享列表 → SDK 分享列表
func fromProtoShares(source []*openv1.Share) []types.Share {

	if len(source) == 0 {
		return nil
	}

	shares := make([]types.Share, 0, len(source))
	for _, item := range source {
		shares = append(shares, fromProtoShare(item))
	}

	return shares
}

// fromProtoRecycleEntries - proto 回收站条目列表 → SDK 回收站条目列表
func fromProtoRecycleEntries(source []*openv1.RecycleEntry) []types.RecycleEntry {

	if len(source) == 0 {
		return nil
	}

	entries := make([]types.RecycleEntry, 0, len(source))
	for _, item := range source {
		entries = append(entries, types.RecycleEntry{
			Id:           item.GetId(),
			Bucket:       item.GetBucket(),
			Key:          item.GetKey(),
			Etag:         item.GetEtag(),
			Size:         item.GetSize(),
			Mime:         item.GetMime(),
			StorageClass: item.GetStorageClass(),
			DeletedAt:    item.GetDeletedAt(),
			PurgeAt:      item.GetPurgeAt(),
		})
	}

	return entries
}

// fromProtoUsageOverview - proto 用量总览 → SDK 用量总览
func fromProtoUsageOverview(source *openv1.UsageOverview) types.UsageOverview {

	if source == nil {
		return types.UsageOverview{}
	}

	quota := source.GetQuota()
	result := types.UsageOverview{
		StorageBytes: source.GetStorageBytes(),
		ObjectCount:  source.GetObjectCount(),
		BucketCount:  source.GetBucketCount(),
		EgressBytes:  source.GetEgressBytes(),
	}
	if quota != nil {
		result.Quota = types.QuotaView{
			MaxBuckets:            quota.GetMaxBuckets(),
			MaxObjects:            quota.GetMaxObjects(),
			MaxStorageBytes:       quota.GetMaxStorageBytes(),
			MaxObjectSize:         quota.GetMaxObjectSize(),
			MonthlyBandwidthBytes: quota.GetMonthlyBandwidthBytes(),
		}
	}

	return result
}

// fromProtoBucketUsages - proto 按桶用量列表 → SDK 按桶用量列表
func fromProtoBucketUsages(source []*openv1.BucketUsage) []types.BucketUsage {

	if len(source) == 0 {
		return nil
	}

	rows := make([]types.BucketUsage, 0, len(source))
	for _, item := range source {
		rows = append(rows, types.BucketUsage{
			Bucket:       item.GetBucket(),
			StorageBytes: item.GetStorageBytes(),
			ObjectCount:  item.GetObjectCount(),
			EgressBytes:  item.GetEgressBytes(),
		})
	}

	return rows
}

// fromProtoEgressPoints - proto 趋势点列表 → SDK 趋势点列表
func fromProtoEgressPoints(source []*openv1.EgressPoint) []types.EgressPoint {

	if len(source) == 0 {
		return nil
	}

	points := make([]types.EgressPoint, 0, len(source))
	for _, item := range source {
		points = append(points, types.EgressPoint{Date: item.GetDate(), Bytes: item.GetBytes()})
	}

	return points
}

// ============================= proto → SDK 模型 - 结束 =============================

// ============================= gRPC 链路 - 结束 =============================
