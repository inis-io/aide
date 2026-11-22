package transport

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/inis-io/aide/ios/types"

	openv1 "github.com/inis-io/aide/ios/proto/gen/ios/open/v1"
)

// ============================= gRPC 链路测试 - 开始 =============================

// TestGRPCVerifyAccessKey - 凭据验证：方法路由/metadata 签名/AK 透传/响应转换
func TestGRPCVerifyAccessKey(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	var info types.AccessKeyInfo
	if err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, &info); err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	record := fake.snapshot()
	if record.method != openv1.OpenService_VerifyAccessKey_FullMethodName {
		t.Fatalf("RPC 方法不符合预期：%s", record.method)
	}
	if record.accessKey != testAccessKey {
		t.Fatalf("metadata 缺少 AK：%q", record.accessKey)
	}
	if record.sign == "" {
		t.Fatalf("metadata 缺少签名")
	}
	if info.Name != "演示密钥" || info.Status != "active" || info.Scope != "*" || info.LastUsedAt != 1763567900000 {
		t.Fatalf("响应转换不符合预期：%+v", info)
	}
}

// TestGRPCShareCreateGolden - 签发分享：expires=0（proto3 零值字段）必须参与签名（黄金向量）
// 回归锚点：protojson 默认丢弃零值字段，会导致与 HTTP 链路签名分叉；
// EmitUnpopulated 修正后，gRPC 链路对同一逻辑请求产出与黄金向量一致的签名。
func TestGRPCShareCreateGolden(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	var result types.ShareCreateResult
	err := link.Invoke(context.Background(), OpShareCreate, &types.ShareCreateRequest{
		Bucket: "photos", Key: "a/b.jpg", Expires: -1,
	}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	record := fake.snapshot()
	if record.sign != goldenCreateShare {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", record.sign, goldenCreateShare)
	}
	request, ok := record.request.(*openv1.CreateShareRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期：%T", record.request)
	}
	if request.GetExpires() != 0 {
		t.Fatalf("Normalize 应将负数有效期收敛为 0：%d", request.GetExpires())
	}
	if result.Share.Token != "tok-abc" || result.Share.Id != 7 || result.Reused {
		t.Fatalf("响应转换不符合预期：%+v", result)
	}
}

// TestGRPCRecycleListGolden - 回收站列表：page/limit 默认值注入并参与签名（黄金向量）
func TestGRPCRecycleListGolden(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	var result types.RecycleListResult
	err := link.Invoke(context.Background(), OpRecycleList, &types.RecycleListRequest{}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	record := fake.snapshot()
	if record.sign != goldenListRecycle {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", record.sign, goldenListRecycle)
	}
	request, ok := record.request.(*openv1.ListRecycleRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期：%T", record.request)
	}
	if request.GetPage() != 1 || request.GetLimit() != 20 {
		t.Fatalf("page/limit 默认值注入不符合预期：page=%d limit=%d", request.GetPage(), request.GetLimit())
	}
	if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].Id != 11 || result.Entries[0].StorageClass != "STANDARD" {
		t.Fatalf("响应转换不符合预期：%+v", result)
	}
}

// TestGRPCRestoreObjectsGolden - 回收站还原：ids 逗号串参与签名（黄金向量）+ 冲突列表转换
func TestGRPCRestoreObjectsGolden(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	var result types.RecycleRestoreResult
	err := link.Invoke(context.Background(), OpRecycleRestore, &types.RecycleRestoreRequest{Ids: []int64{1, 2, 3}}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	record := fake.snapshot()
	if record.sign != goldenRestoreObjects {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", record.sign, goldenRestoreObjects)
	}
	request, ok := record.request.(*openv1.RestoreObjectsRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期：%T", record.request)
	}
	if request.GetIds() != "1,2,3" {
		t.Fatalf("ids 应编码为逗号分隔串：%q", request.GetIds())
	}
	if result.Restored != 2 || len(result.Conflicts) != 1 || result.Conflicts[0] != "b.jpg" {
		t.Fatalf("响应转换不符合预期：%+v", result)
	}
}

// TestGRPCAllMethods - 全量 RPC 方法对账（禁止漂移）
func TestGRPCAllMethods(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	cases := []struct {
		// op - 操作
		op Operation
		// req - 请求
		req any
		// method - 期望完整 RPC 方法名
		method string
	}{
		{OpAccessKeyVerify, nil, openv1.OpenService_VerifyAccessKey_FullMethodName},
		{OpBucketInfoList, nil, openv1.OpenService_ListBucketInfos_FullMethodName},
		{OpBucketEmpty, &types.EmptyBucketRequest{Bucket: "photos"}, openv1.OpenService_EmptyBucket_FullMethodName},
		{OpShareCreate, &types.ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg"}, openv1.OpenService_CreateShare_FullMethodName},
		{OpShareList, &types.ShareListRequest{Bucket: "photos", Key: "a/b.jpg"}, openv1.OpenService_ListShares_FullMethodName},
		{OpShareRevoke, &types.ShareRevokeRequest{Id: 42}, openv1.OpenService_RevokeShare_FullMethodName},
		{OpRecycleList, &types.RecycleListRequest{}, openv1.OpenService_ListRecycle_FullMethodName},
		{OpRecycleRestore, &types.RecycleRestoreRequest{Ids: []int64{1}}, openv1.OpenService_RestoreObjects_FullMethodName},
		{OpRecyclePurge, &types.RecyclePurgeRequest{Ids: []int64{4, 5}}, openv1.OpenService_PurgeObjects_FullMethodName},
		{OpRecycleClear, &types.RecycleClearRequest{}, openv1.OpenService_ClearRecycle_FullMethodName},
		{OpUsageOverview, nil, openv1.OpenService_GetUsageOverview_FullMethodName},
		{OpUsageBucketList, nil, openv1.OpenService_ListBucketUsage_FullMethodName},
		{OpUsageEgressTrend, nil, openv1.OpenService_GetEgressTrend_FullMethodName},
	}

	for _, item := range cases {
		if err := link.Invoke(context.Background(), item.op, item.req, nil); err != nil {
			t.Fatalf("%s 调用失败：%v", item.op, err)
		}
		if record := fake.snapshot(); record.method != item.method {
			t.Fatalf("%s RPC 方法不符合预期：got=%s want=%s", item.op, record.method, item.method)
		}
	}
}

// TestGRPCUnauthenticated - 平台拒绝签名（验签失败）归一 ErrInvalidSign
func TestGRPCUnauthenticated(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret + "-wrong"}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("验签失败应归一 ErrInvalidSign：%v", err)
	}
}

// TestGRPCBusinessError - 信封业务错误归一 *APIError（Raw 为空）
func TestGRPCBusinessError(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	fake.verifyAccessKey = func(ctx context.Context, req *openv1.VerifyAccessKeyRequest) (*openv1.VerifyAccessKeyResponse, error) {
		return &openv1.VerifyAccessKeyResponse{
			Envelope: &openv1.Envelope{Code: 404, Msg: "密钥不存在"},
		}, nil
	}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("信封业务错误应归一 *APIError：%v", err)
	}
	if apiErr.Code != 404 || apiErr.Msg != "密钥不存在" || len(apiErr.Raw) != 0 {
		t.Fatalf("APIError 字段不符合预期：%+v", apiErr)
	}
}

// TestGRPCEmptyEnvelope - 缺失信封按协议违例归一 ErrTransport
func TestGRPCEmptyEnvelope(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	fake.verifyAccessKey = func(ctx context.Context, req *openv1.VerifyAccessKeyRequest) (*openv1.VerifyAccessKeyResponse, error) {
		return &openv1.VerifyAccessKeyResponse{}, nil
	}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if !errors.Is(err, types.ErrTransport) {
		t.Fatalf("缺失信封应归一 ErrTransport：%v", err)
	}
}

// TestGRPCStatusError - 非业务 RPC 失败归一 ErrTransport
func TestGRPCStatusError(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	fake.verifyAccessKey = func(ctx context.Context, req *openv1.VerifyAccessKeyRequest) (*openv1.VerifyAccessKeyResponse, error) {
		return nil, status.Error(codes.Internal, "服务内部错误")
	}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if !errors.Is(err, types.ErrTransport) || errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("RPC 状态错误应归一 ErrTransport：%v", err)
	}
}

// TestGRPCTarget - 拨号目标归一（剥 scheme/路径，缺省端口补 443，IPv6 不改写）
func TestGRPCTarget(t *testing.T) {

	cases := []struct {
		// endpoint - 平台地址
		endpoint string
		// want - 期望拨号目标
		want string
	}{
		{endpoint: "https://storage.example.com", want: "storage.example.com:443"},
		{endpoint: "http://storage.example.com:2000", want: "storage.example.com:2000"},
		{endpoint: "storage.example.com:2000", want: "storage.example.com:2000"},
		{endpoint: "https://storage.example.com/base/path", want: "storage.example.com:443"},
		{endpoint: "  https://storage.example.com  ", want: "storage.example.com:443"},
		{endpoint: "[::1]:2000", want: "[::1]:2000"},
		{endpoint: "127.0.0.1:9000", want: "127.0.0.1:9000"},
	}

	for _, item := range cases {
		if got := GRPCTarget(item.endpoint); got != item.want {
			t.Fatalf("拨号目标归一不符合预期：endpoint=%q got=%q want=%q", item.endpoint, got, item.want)
		}
	}
}

// TestDefaultCredentials - 缺省传输凭证按端点 scheme 选择（https → TLS，http/无 scheme → plaintext）
// 回归锚点：https 端点走明文 h2c 会被 TLS 端口直接拒绝（nginx 回 HTTP/1.1 400）。
func TestDefaultCredentials(t *testing.T) {

	cases := []struct {
		// endpoint - 平台地址
		endpoint string
		// want - 期望的安全协议（tls / insecure）
		want string
	}{
		{endpoint: "https://storage.example.com", want: "tls"},
		{endpoint: "HTTPS://storage.example.com", want: "tls"},
		{endpoint: "  https://storage.example.com  ", want: "tls"},
		{endpoint: "https://storage.example.com/base/path", want: "tls"},
		{endpoint: "http://127.0.0.1:2000", want: "insecure"},
		{endpoint: "127.0.0.1:9000", want: "insecure"},
		{endpoint: "storage.example.com:2000", want: "insecure"},
		{endpoint: "[::1]:2000", want: "insecure"},
	}

	for _, item := range cases {
		if got := defaultCredentials(item.endpoint).Info().SecurityProtocol; got != item.want {
			t.Fatalf("缺省凭证不符合预期：endpoint=%q got=%q want=%q", item.endpoint, got, item.want)
		}
	}
}

// TestGRPCCloseIdempotent - 关闭幂等（重复关闭返回首次结果，不 panic）
func TestGRPCCloseIdempotent(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	first := link.Close()
	second := link.Close()
	if first != second {
		t.Fatalf("重复关闭应返回首次结果：%v != %v", first, second)
	}

	// 关闭后调用应失败（连接已断）
	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if err == nil {
		t.Fatalf("关闭后调用应失败")
	}
}

// TestGRPCUnknownOperation - 未知操作本地拒绝
func TestGRPCUnknownOperation(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), Operation("unknown.op"), nil, nil)
	if !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("未知操作应返回 ErrInvalidRequest：%v", err)
	}
}

// TestGRPCRequestTypeMismatch - 请求类型不匹配本地拒绝
func TestGRPCRequestTypeMismatch(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpBucketEmpty, &types.ShareRevokeRequest{Id: 1}, nil)
	if !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("请求类型不匹配应返回 ErrInvalidRequest：%v", err)
	}
}

// TestGRPCFillCoversAll - 其余操作的响应转换覆盖（桶/用量/趋势/清空）
func TestGRPCFillCoversAll(t *testing.T) {

	fake := &fakeOpenService{secret: testSecret}
	link := startFakeGRPC(t, fake)
	ctx := context.Background()

	var buckets []types.BucketInfo
	if err := link.Invoke(ctx, OpBucketInfoList, nil, &buckets); err != nil {
		t.Fatalf("桶列表调用失败：%v", err)
	}
	if len(buckets) != 1 || buckets[0].Name != "photos" || buckets[0].StorageClass != "STANDARD" {
		t.Fatalf("桶列表转换不符合预期：%+v", buckets)
	}

	var empty types.EmptyBucketResult
	if err := link.Invoke(ctx, OpBucketEmpty, &types.EmptyBucketRequest{Bucket: "photos"}, &empty); err != nil {
		t.Fatalf("清空桶调用失败：%v", err)
	}
	if empty.Removed != 42 {
		t.Fatalf("清空桶转换不符合预期：%+v", empty)
	}

	var overview types.UsageOverview
	if err := link.Invoke(ctx, OpUsageOverview, nil, &overview); err != nil {
		t.Fatalf("用量总览调用失败：%v", err)
	}
	if overview.StorageBytes != 1048576 || overview.ObjectCount != 128 || overview.Quota.MaxBuckets != 10 {
		t.Fatalf("用量总览转换不符合预期：%+v", overview)
	}

	var rows []types.BucketUsage
	if err := link.Invoke(ctx, OpUsageBucketList, nil, &rows); err != nil {
		t.Fatalf("按桶用量调用失败：%v", err)
	}
	if len(rows) != 1 || rows[0].Bucket != "photos" || rows[0].EgressBytes != 1048576 {
		t.Fatalf("按桶用量转换不符合预期：%+v", rows)
	}

	var points []types.EgressPoint
	if err := link.Invoke(ctx, OpUsageEgressTrend, nil, &points); err != nil {
		t.Fatalf("流量趋势调用失败：%v", err)
	}
	if len(points) != 2 || points[0].Date != "2026-11-18" || points[1].Bytes != 2048 {
		t.Fatalf("流量趋势转换不符合预期：%+v", points)
	}

	var purged types.RecyclePurgeResult
	if err := link.Invoke(ctx, OpRecycleClear, &types.RecycleClearRequest{}, &purged); err != nil {
		t.Fatalf("回收站清空调用失败：%v", err)
	}
	if purged.Purged != 9 {
		t.Fatalf("回收站清空转换不符合预期：%+v", purged)
	}

	var shares []types.Share
	if err := link.Invoke(ctx, OpShareList, &types.ShareListRequest{Bucket: "photos", Key: "a/b.jpg"}, &shares); err != nil {
		t.Fatalf("分享列表调用失败：%v", err)
	}
	if len(shares) != 1 || shares[0].Token != "tok-abc" || shares[0].Visits != 3 {
		t.Fatalf("分享列表转换不符合预期：%+v", shares)
	}
}

// ============================= gRPC 链路测试 - 结束 =============================
