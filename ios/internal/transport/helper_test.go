package transport

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/inis-io/aide/ios/sign"

	openv1 "github.com/inis-io/aide/ios/proto/gen/ios/open/v1"
)

// ============================= 测试公共件 - 开始 =============================

const (
	// testAccessKey - 测试 AccessKey（与 sign 包黄金向量一致）
	testAccessKey = "INIS0123456789ABCDEF"
	// testSecret - 测试密钥（与 sign 包黄金向量一致）
	testSecret = "INIS-TEST-SECRET-0123456789abcdef"
	// testTimestamp - 固定毫秒时间戳（黄金向量锚定值）
	testTimestamp = int64(1763568000000)
)

// referenceSign - 服务端签名算法的独立复算实现（测试基准，与被测 sign 包互为校验）
func referenceSign(params map[string]any, secret string) string {

	keys := make([]string, 0, len(params))
	for key := range params {
		if key == signField {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := referenceValue(params[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
	}

	sum := md5.Sum([]byte(strings.Join(parts, "&") + "&secret=" + secret))

	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// referenceValue - 服务端 cast.ToString 的极简镜像（测试基准）
func referenceValue(value any) string {

	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// fixedNow - 固定时间来源（黄金向量锚定）
func fixedNow() time.Time {

	return time.UnixMilli(testTimestamp)
}

// ============================= 测试公共件 - 结束 =============================

// ============================= HTTP 测试件 - 开始 =============================

// httpCapture - 假平台捕获到的请求
type httpCapture struct {
	// method - HTTP 方法
	method string
	// path - 请求路径
	path string
	// query - 查询参数
	query url.Values
	// body - 解析后的 JSON 请求体
	body map[string]any
	// raw - 原始请求体
	raw []byte
}

// params - 本次请求参与签名的参数集（GET/DELETE 取 query，POST/PUT 取 body；剔除 sign）
func (this *httpCapture) params() map[string]any {

	source := this.body
	if source == nil {
		source = make(map[string]any, len(this.query))
		for key := range this.query {
			source[key] = this.query.Get(key)
		}
	}

	params := make(map[string]any, len(source))
	for key, value := range source {
		params[key] = value
	}

	return params
}

// sign - 本次请求携带的签名
func (this *httpCapture) sign() string {

	if this.body != nil {
		if value, ok := this.body[signField]; ok {
			if text, ok := value.(string); ok {
				return text
			}
		}
		return ""
	}

	return this.query.Get(signField)
}

// startHTTPPlatform - 启动假平台（httptest），reply 决定响应信封
func startHTTPPlatform(t *testing.T, reply func(capture *httpCapture) (int, string)) (*httptest.Server, *httpCapture) {

	t.Helper()

	capture := &httpCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("读取请求体失败：%v", err)
		}

		capture.method = request.Method
		capture.path = request.URL.Path
		capture.query = request.URL.Query()
		capture.raw = raw
		capture.body = nil
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &capture.body); err != nil {
				t.Errorf("请求体不是合法 JSON：%v", err)
			}
		}

		code, text := reply(capture)
		// 平台约定：HTTP 状态恒 200，业务结果只看 body.code
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(code)
		_, _ = writer.Write([]byte(text))
	}))

	t.Cleanup(server.Close)

	return server, capture
}

// startHTTPTransport - 装配 HTTP 链路（固定时间来源；Signer 留空以覆盖 MD5 兜底）
func startHTTPTransport(t *testing.T, endpoint string) Transport {

	t.Helper()

	link := NewHTTP(Config{
		Endpoint:  endpoint,
		AccessKey: testAccessKey,
		Secret:    testSecret,
		Timeout:   5 * time.Second,
		UserAgent: "ios-go/0.1.0",
	})
	link.(*httpTransport).now = fixedNow
	t.Cleanup(func() { _ = link.Close() })

	return link
}

// jsonReply - 组装平台信封响应
func jsonReply(t *testing.T, code int, msg string, data any) string {

	t.Helper()

	text, err := json.Marshal(map[string]any{"code": code, "msg": msg, "data": data})
	if err != nil {
		t.Fatalf("构造响应失败：%v", err)
	}

	return string(text)
}

// ============================= HTTP 测试件 - 结束 =============================

// ============================= gRPC 测试件 - 开始 =============================

// grpcRecord - 假服务捕获到的请求信息
type grpcRecord struct {
	// method - 完整 RPC 方法名
	method string
	// sign - x-ios-sign 值
	sign string
	// accessKey - x-ios-access-key 值
	accessKey string
	// request - 请求消息
	request proto.Message
}

// fakeOpenService - 假开放服务：校验 metadata 签名并回放响应
type fakeOpenService struct {
	openv1.UnimplementedOpenServiceServer

	// secret - 期望的密钥（故意配错即可触发 Unauthenticated）
	secret string
	// mutex - 保护 record（服务端 handler 并发执行）
	mutex sync.Mutex
	// record - 最近一次请求
	record grpcRecord
	// verifyAccessKey - 凭据验证自定义响应（nil 走默认成功响应）
	verifyAccessKey func(ctx context.Context, req *openv1.VerifyAccessKeyRequest) (*openv1.VerifyAccessKeyResponse, error)
	// createShare - 签发分享自定义响应（nil 走默认成功响应）
	createShare func(ctx context.Context, req *openv1.CreateShareRequest) (*openv1.CreateShareResponse, error)
	// restoreObjects - 回收站还原自定义响应（nil 走默认成功响应）
	restoreObjects func(ctx context.Context, req *openv1.RestoreObjectsRequest) (*openv1.RestoreObjectsResponse, error)
}

// capture - 记录请求并复算 metadata 签名（不通过返回 Unauthenticated）
// 复算与客户端同形态：protojson（EmitUnpopulated）→ map → referenceSign。
func (this *fakeOpenService) capture(ctx context.Context, method string, message proto.Message) error {

	holder, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "缺少 metadata")
	}

	signature := firstValue(holder.Get(metadataSign))
	accessKey := firstValue(holder.Get(metadataAccessKey))

	this.mutex.Lock()
	this.record = grpcRecord{method: method, sign: signature, accessKey: accessKey, request: message}
	this.mutex.Unlock()

	text, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		return status.Error(codes.Internal, "请求序列化失败")
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return status.Error(codes.Internal, "请求解析失败")
	}

	if got := referenceSign(params, this.secret); got != signature {
		return status.Error(codes.Unauthenticated, "签名验证失败")
	}

	return nil
}

// snapshot - 读取最近一次请求快照
func (this *fakeOpenService) snapshot() grpcRecord {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	return this.record
}

// firstValue - 取 metadata 首个值
func firstValue(values []string) string {

	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// VerifyAccessKey - 凭据验证/密钥自检
func (this *fakeOpenService) VerifyAccessKey(ctx context.Context, req *openv1.VerifyAccessKeyRequest) (*openv1.VerifyAccessKeyResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_VerifyAccessKey_FullMethodName, req); err != nil {
		return nil, err
	}
	if this.verifyAccessKey != nil {
		return this.verifyAccessKey(ctx, req)
	}

	return &openv1.VerifyAccessKeyResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Info: &openv1.AccessKeyInfo{
			Name: "演示密钥", Status: "active", Scope: "*",
			LastUsedAt: 1763567900000, ExpireAt: 0, CreateAt: 1763567000000,
		},
	}, nil
}

// ListBucketInfos - 管理视角桶列表
func (this *fakeOpenService) ListBucketInfos(ctx context.Context, req *openv1.ListBucketInfosRequest) (*openv1.ListBucketInfosResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_ListBucketInfos_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.ListBucketInfosResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Buckets: []*openv1.BucketInfo{
			{
				Name: "photos", Region: "us-east-1", Acl: "private", Versioning: "off",
				Status: "active", StorageClass: "STANDARD", CreateAt: 1763567000000, UpdateAt: 1763567000000,
			},
		},
	}, nil
}

// EmptyBucket - 清空桶
func (this *fakeOpenService) EmptyBucket(ctx context.Context, req *openv1.EmptyBucketRequest) (*openv1.EmptyBucketResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_EmptyBucket_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.EmptyBucketResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Removed:  42,
	}, nil
}

// CreateShare - 签发分享链接
func (this *fakeOpenService) CreateShare(ctx context.Context, req *openv1.CreateShareRequest) (*openv1.CreateShareResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_CreateShare_FullMethodName, req); err != nil {
		return nil, err
	}
	if this.createShare != nil {
		return this.createShare(ctx, req)
	}

	return &openv1.CreateShareResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Share: &openv1.Share{
			Id: 7, Bucket: req.GetBucket(), Key: req.GetKey(), Token: "tok-abc",
			ExpireAt: 0, Visits: 0, CreateAt: 1763568000000,
		},
		Reused: false,
	}, nil
}

// ListShares - 对象分享列表
func (this *fakeOpenService) ListShares(ctx context.Context, req *openv1.ListSharesRequest) (*openv1.ListSharesResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_ListShares_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.ListSharesResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Shares: []*openv1.Share{
			{Id: 7, Bucket: req.GetBucket(), Key: req.GetKey(), Token: "tok-abc", ExpireAt: 0, Visits: 3, CreateAt: 1763568000000},
		},
	}, nil
}

// RevokeShare - 吊销分享
func (this *fakeOpenService) RevokeShare(ctx context.Context, req *openv1.RevokeShareRequest) (*openv1.RevokeShareResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_RevokeShare_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.RevokeShareResponse{Envelope: &openv1.Envelope{Code: 200, Msg: "OK"}}, nil
}

// ListRecycle - 回收站列表
func (this *fakeOpenService) ListRecycle(ctx context.Context, req *openv1.ListRecycleRequest) (*openv1.ListRecycleResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_ListRecycle_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.ListRecycleResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Entries: []*openv1.RecycleEntry{
			{
				Id: 11, Bucket: "photos", Key: "a.jpg", Etag: "etag-a", Size: 1024,
				Mime: "image/jpeg", StorageClass: "STANDARD", DeletedAt: 1763568000000, PurgeAt: 1766150400000,
			},
		},
		Total: 1,
	}, nil
}

// RestoreObjects - 回收站还原
func (this *fakeOpenService) RestoreObjects(ctx context.Context, req *openv1.RestoreObjectsRequest) (*openv1.RestoreObjectsResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_RestoreObjects_FullMethodName, req); err != nil {
		return nil, err
	}
	if this.restoreObjects != nil {
		return this.restoreObjects(ctx, req)
	}

	return &openv1.RestoreObjectsResponse{
		Envelope:  &openv1.Envelope{Code: 200, Msg: "OK"},
		Restored:  2,
		Conflicts: []string{"b.jpg"},
	}, nil
}

// PurgeObjects - 回收站指定释放
func (this *fakeOpenService) PurgeObjects(ctx context.Context, req *openv1.PurgeObjectsRequest) (*openv1.PurgeObjectsResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_PurgeObjects_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.PurgeObjectsResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Purged:   2,
	}, nil
}

// ClearRecycle - 回收站全部释放
func (this *fakeOpenService) ClearRecycle(ctx context.Context, req *openv1.ClearRecycleRequest) (*openv1.ClearRecycleResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_ClearRecycle_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.ClearRecycleResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Purged:   9,
	}, nil
}

// GetUsageOverview - 用量总览
func (this *fakeOpenService) GetUsageOverview(ctx context.Context, req *openv1.GetUsageOverviewRequest) (*openv1.GetUsageOverviewResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_GetUsageOverview_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.GetUsageOverviewResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Overview: &openv1.UsageOverview{
			StorageBytes: 1048576, ObjectCount: 128, BucketCount: 3, EgressBytes: 2097152,
			Quota: &openv1.QuotaView{
				MaxBuckets: 10, MaxObjects: 100000, MaxStorageBytes: 10737418240,
				MaxObjectSize: 5368709120, MonthlyBandwidthBytes: 107374182400,
			},
		},
	}, nil
}

// ListBucketUsage - 按桶用量列表
func (this *fakeOpenService) ListBucketUsage(ctx context.Context, req *openv1.ListBucketUsageRequest) (*openv1.ListBucketUsageResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_ListBucketUsage_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.ListBucketUsageResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Rows: []*openv1.BucketUsage{
			{Bucket: "photos", StorageBytes: 1048576, ObjectCount: 100, EgressBytes: 1048576},
		},
	}, nil
}

// GetEgressTrend - 出口流量按日序列
func (this *fakeOpenService) GetEgressTrend(ctx context.Context, req *openv1.GetEgressTrendRequest) (*openv1.GetEgressTrendResponse, error) {

	if err := this.capture(ctx, openv1.OpenService_GetEgressTrend_FullMethodName, req); err != nil {
		return nil, err
	}

	return &openv1.GetEgressTrendResponse{
		Envelope: &openv1.Envelope{Code: 200, Msg: "OK"},
		Points: []*openv1.EgressPoint{
			{Date: "2026-11-18", Bytes: 1024},
			{Date: "2026-11-19", Bytes: 2048},
		},
	}, nil
}

// startFakeGRPC - 用 bufconn 起内存 gRPC 服务并返回挂载真实链路实现的 Transport（固定时间来源）
func startFakeGRPC(t *testing.T, fake *fakeOpenService) Transport {

	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	openv1.RegisterOpenServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("建立内存连接失败：%v", err)
	}

	// 客户端恒用正确密钥签名；假服务按自身 secret 验签（故意配错即可模拟平台拒绝）
	link := &grpcTransport{
		conn:       conn,
		accessKey:  testAccessKey,
		secret:     testSecret,
		signer:     sign.New(),
		operations: grpcOperations(),
		now:        fixedNow,
	}
	t.Cleanup(func() { _ = link.Close() })

	return link
}

// ============================= gRPC 测试件 - 结束 =============================
