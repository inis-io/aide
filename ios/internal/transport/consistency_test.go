package transport

import (
	"context"
	"testing"

	"github.com/inis-io/aide/ios/types"
)

// ============================= 双链路一致性测试 - 开始 =============================
//
// 对同一逻辑请求，HTTP 链路（query/body 的 sign）与 gRPC 链路（metadata 的 x-ios-sign）
// 必须产出同一签名，且等于服务端实测黄金向量（sign 包测试的锚定值）。
// 固定时间来源（testTimestamp）保证向量可复现。

// TestConsistentSignature - 双链路同请求同签名（锚定服务端实测黄金向量）
func TestConsistentSignature(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", nil)
	})
	httpLink := startHTTPTransport(t, server.URL)

	fake := &fakeOpenService{secret: testSecret}
	grpcLink := startFakeGRPC(t, fake)

	cases := []struct {
		// name - 用例名
		name string
		// op - 操作
		op Operation
		// req - 逻辑请求
		req any
		// golden - 服务端实测签名（sign 包黄金向量）
		golden string
	}{
		{name: "凭据验证", op: OpAccessKeyVerify, req: nil, golden: "5FDFB63422549D89EDBED2E73FDC77F6"},
		{name: "管理视角桶列表", op: OpBucketInfoList, req: nil, golden: "5FDFB63422549D89EDBED2E73FDC77F6"},
		{name: "清空桶", op: OpBucketEmpty, req: &types.EmptyBucketRequest{Bucket: "photos"}, golden: "9B3034217A86D8B156309458C8544B2B"},
		{name: "签发分享-默认值", op: OpShareCreate, req: &types.ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg"}, golden: "2525E294E7BA40163FA370D18C9DFF5F"},
		{name: "签发分享-有效期3600", op: OpShareCreate, req: &types.ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg", Expires: 3600}, golden: "175CC55AF3C05581247929F6FDC47BC1"},
		{name: "对象分享列表", op: OpShareList, req: &types.ShareListRequest{Bucket: "photos", Key: "a/b.jpg"}, golden: "61C88411F6363F7E0ABA2B49684746B9"},
		{name: "吊销分享", op: OpShareRevoke, req: &types.ShareRevokeRequest{Id: 42}, golden: "FFACAE9E7EA83F81B3132D7C19D16F29"},
		{name: "回收站列表-默认值", op: OpRecycleList, req: &types.RecycleListRequest{}, golden: "AEE6258AD56E2D9D91511984034DB4D7"},
		{name: "回收站列表-全参数", op: OpRecycleList, req: &types.RecycleListRequest{Bucket: "photos", Keyword: "report", Page: 2, Limit: 50}, golden: "3BE7D1969114F0633167108A2259BE3C"},
		{name: "回收站还原", op: OpRecycleRestore, req: &types.RecycleRestoreRequest{Ids: []int64{1, 2, 3}}, golden: "3D3AB2872B827B81BA1B09ED1CEE514C"},
		{name: "回收站指定释放", op: OpRecyclePurge, req: &types.RecyclePurgeRequest{Ids: []int64{4, 5}}, golden: "1E134425EB1224E7A7A7633A751409C0"},
		{name: "回收站全部释放", op: OpRecycleClear, req: &types.RecycleClearRequest{Bucket: "photos"}, golden: "9B3034217A86D8B156309458C8544B2B"},
		{name: "用量总览", op: OpUsageOverview, req: nil, golden: "5FDFB63422549D89EDBED2E73FDC77F6"},
		{name: "按桶用量列表", op: OpUsageBucketList, req: nil, golden: "5FDFB63422549D89EDBED2E73FDC77F6"},
		{name: "出口流量趋势", op: OpUsageEgressTrend, req: nil, golden: "5FDFB63422549D89EDBED2E73FDC77F6"},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {

			if err := httpLink.Invoke(context.Background(), item.op, item.req, nil); err != nil {
				t.Fatalf("HTTP 链路调用失败：%v", err)
			}
			httpSign := capture.sign()
			if httpSign == "" {
				t.Fatalf("HTTP 链路未携带签名")
			}

			if err := grpcLink.Invoke(context.Background(), item.op, item.req, nil); err != nil {
				t.Fatalf("gRPC 链路调用失败：%v", err)
			}
			grpcSign := fake.snapshot().sign
			if grpcSign == "" {
				t.Fatalf("gRPC 链路未携带签名")
			}

			if httpSign != grpcSign {
				t.Fatalf("双链路签名分叉：http=%s grpc=%s", httpSign, grpcSign)
			}
			if httpSign != item.golden {
				t.Fatalf("签名与服务端实测值不一致：got=%s want=%s", httpSign, item.golden)
			}
		})
	}
}

// ============================= 双链路一致性测试 - 结束 =============================
