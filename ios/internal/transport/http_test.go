package transport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/inis-io/aide/ios/types"
)

// ============================= HTTP 链路测试 - 开始 =============================

// TestHTTPVerifyAccessKey - 凭据验证：端点/方法/参数注入/签名复算/信封解码
func TestHTTPVerifyAccessKey(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"name": "演示密钥", "status": "active", "scope": "*",
			"lastUsedAt": 1763567900000, "expireAt": 0, "createAt": 1763567000000,
		})
	})
	link := startHTTPTransport(t, server.URL)

	var info types.AccessKeyInfo
	if err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, &info); err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	if capture.method != "GET" || capture.path != "/api/open-accesskey/verify" {
		t.Fatalf("端点或方法不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Get("accessKey") != testAccessKey {
		t.Fatalf("query 缺少 accessKey：%v", capture.query)
	}
	if capture.query.Get("timestamp") != "1763568000000" {
		t.Fatalf("query 缺少毫秒时间戳：%v", capture.query)
	}
	if got := referenceSign(capture.params(), testSecret); got != capture.sign() {
		t.Fatalf("签名复算不一致：got=%s want=%s", got, capture.sign())
	}
	if info.Name != "演示密钥" || info.Status != "active" || info.Scope != "*" || info.LastUsedAt != 1763567900000 {
		t.Fatalf("响应解码不符合预期：%+v", info)
	}
}

// TestHTTPEmptyBucket - 清空桶：DELETE 走 query，签名锚定黄金向量
func TestHTTPEmptyBucket(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{"removed": 42})
	})
	link := startHTTPTransport(t, server.URL)

	var result types.EmptyBucketResult
	err := link.Invoke(context.Background(), OpBucketEmpty, &types.EmptyBucketRequest{Bucket: "photos"}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	if capture.method != "DELETE" || capture.path != "/api/open-bucket/empty" {
		t.Fatalf("端点或方法不符合预期：%s %s", capture.method, capture.path)
	}
	if len(capture.raw) != 0 {
		t.Fatalf("DELETE 不应携带请求体：%q", capture.raw)
	}
	if capture.sign() != goldenEmptyBucket {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", capture.sign(), goldenEmptyBucket)
	}
	if result.Removed != 42 {
		t.Fatalf("响应解码不符合预期：%+v", result)
	}
}

// TestHTTPShareCreate - 签发分享：POST 走 JSON body，expires 默认值 0 参与签名（黄金向量）
func TestHTTPShareCreate(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"share": map[string]any{
				"id": 7, "bucket": "photos", "key": "a/b.jpg", "token": "tok-abc",
				"expireAt": 0, "visits": 0, "createAt": 1763568000000,
			},
			"reused": false,
		})
	})
	link := startHTTPTransport(t, server.URL)

	var result types.ShareCreateResult
	err := link.Invoke(context.Background(), OpShareCreate, &types.ShareCreateRequest{
		Bucket: "photos", Key: "a/b.jpg", Expires: -1,
	}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	if capture.method != "POST" || capture.path != "/api/open-share/create" {
		t.Fatalf("端点或方法不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.body == nil {
		t.Fatalf("POST 应携带 JSON 请求体")
	}
	// Normalize：负数有效期收敛为 0，且 0 参与签名
	if capture.body["expires"] != float64(0) {
		t.Fatalf("expires 默认值注入不符合预期：%v", capture.body)
	}
	if capture.sign() != goldenCreateShare {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", capture.sign(), goldenCreateShare)
	}
	if result.Share.Token != "tok-abc" || result.Share.Id != 7 || result.Reused {
		t.Fatalf("响应解码不符合预期：%+v", result)
	}
}

// TestHTTPRecycleList - 回收站列表：page/limit 默认值注入（黄金向量）
func TestHTTPRecycleList(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"entries": []map[string]any{
				{
					"id": 11, "bucket": "photos", "key": "a.jpg", "etag": "etag-a", "size": 1024,
					"mime": "image/jpeg", "storageClass": "STANDARD", "deletedAt": 1763568000000, "purgeAt": 1766150400000,
				},
			},
			"total": 1,
		})
	})
	link := startHTTPTransport(t, server.URL)

	var result types.RecycleListResult
	err := link.Invoke(context.Background(), OpRecycleList, &types.RecycleListRequest{}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	if capture.method != "GET" || capture.path != "/api/open-object/recycle" {
		t.Fatalf("端点或方法不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Get("page") != "1" || capture.query.Get("limit") != "20" {
		t.Fatalf("page/limit 默认值注入不符合预期：%v", capture.query)
	}
	if capture.sign() != goldenListRecycle {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", capture.sign(), goldenListRecycle)
	}
	if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].Key != "a.jpg" || result.Entries[0].Id != 11 {
		t.Fatalf("响应解码不符合预期：%+v", result)
	}
}

// TestHTTPRecycleRestore - 回收站还原：PUT + ids 逗号分隔串（黄金向量）
func TestHTTPRecycleRestore(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{"restored": 2, "conflicts": []string{"b.jpg"}})
	})
	link := startHTTPTransport(t, server.URL)

	var result types.RecycleRestoreResult
	err := link.Invoke(context.Background(), OpRecycleRestore, &types.RecycleRestoreRequest{Ids: []int64{1, 2, 3}}, &result)
	if err != nil {
		t.Fatalf("调用失败：%v", err)
	}

	if capture.method != "PUT" || capture.path != "/api/open-object/restore" {
		t.Fatalf("端点或方法不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.body["ids"] != "1,2,3" {
		t.Fatalf("ids 应编码为逗号分隔串：%v", capture.body)
	}
	if capture.sign() != goldenRestoreObjects {
		t.Fatalf("签名应锚定黄金向量：got=%s want=%s", capture.sign(), goldenRestoreObjects)
	}
	if result.Restored != 2 || len(result.Conflicts) != 1 || result.Conflicts[0] != "b.jpg" {
		t.Fatalf("响应解码不符合预期：%+v", result)
	}
}

// TestHTTPAllRoutes - 全量路由对账（方法 + 路径，禁止漂移）
func TestHTTPAllRoutes(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", nil)
	})
	link := startHTTPTransport(t, server.URL)

	cases := []struct {
		// op - 操作
		op Operation
		// req - 请求
		req any
		// method - 期望 HTTP 方法
		method string
		// path - 期望路径
		path string
	}{
		{OpAccessKeyVerify, nil, "GET", "/api/open-accesskey/verify"},
		{OpBucketInfoList, nil, "GET", "/api/open-bucket/find"},
		{OpBucketEmpty, &types.EmptyBucketRequest{Bucket: "photos"}, "DELETE", "/api/open-bucket/empty"},
		{OpShareCreate, &types.ShareCreateRequest{Bucket: "photos", Key: "a.jpg"}, "POST", "/api/open-share/create"},
		{OpShareList, &types.ShareListRequest{Bucket: "photos", Key: "a.jpg"}, "GET", "/api/open-share/find"},
		{OpShareRevoke, &types.ShareRevokeRequest{Id: 42}, "DELETE", "/api/open-share/remove"},
		{OpRecycleList, &types.RecycleListRequest{}, "GET", "/api/open-object/recycle"},
		{OpRecycleRestore, &types.RecycleRestoreRequest{Ids: []int64{1}}, "PUT", "/api/open-object/restore"},
		{OpRecyclePurge, &types.RecyclePurgeRequest{Ids: []int64{4, 5}}, "DELETE", "/api/open-object/purge"},
		{OpRecycleClear, &types.RecycleClearRequest{}, "DELETE", "/api/open-object/clear"},
		{OpUsageOverview, nil, "GET", "/api/open-usage/overview"},
		{OpUsageBucketList, nil, "GET", "/api/open-usage/find"},
		{OpUsageEgressTrend, nil, "GET", "/api/open-usage/trend"},
	}

	for _, item := range cases {
		if err := link.Invoke(context.Background(), item.op, item.req, nil); err != nil {
			t.Fatalf("%s 调用失败：%v", item.op, err)
		}
		if capture.method != item.method || capture.path != item.path {
			t.Fatalf("%s 路由不符合预期：got=%s %s want=%s %s",
				item.op, capture.method, capture.path, item.method, item.path)
		}
		if got := referenceSign(capture.params(), testSecret); got != capture.sign() {
			t.Fatalf("%s 签名复算不一致：got=%s want=%s", item.op, got, capture.sign())
		}
	}
}

// TestHTTPAPIError - 业务错误归一为 *APIError（code!=200）
func TestHTTPAPIError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 401, "签名验证失败", nil)
	})
	link := startHTTPTransport(t, server.URL)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if err == nil {
		t.Fatalf("业务错误应返回错误")
	}

	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误应可断言为 *APIError：%T", err)
	}
	if apiErr.Code != 401 || apiErr.Msg != "签名验证失败" {
		t.Fatalf("APIError 字段不符合预期：%+v", apiErr)
	}
	if len(apiErr.Raw) == 0 {
		t.Fatalf("HTTP 链路 APIError 应携带原始响应体")
	}
}

// TestHTTPInvalidJSON - 非法 JSON 响应按协议违例报错（不归 ErrTransport）
func TestHTTPInvalidJSON(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, "not-json"
	})
	link := startHTTPTransport(t, server.URL)

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "响应不是合法 JSON") {
		t.Fatalf("非法 JSON 响应应报协议违例：%v", err)
	}
	if errors.Is(err, types.ErrTransport) {
		t.Fatalf("协议违例不应归类为传输错误：%v", err)
	}
}

// TestHTTPNetworkError - 连接失败归一 ErrTransport
func TestHTTPNetworkError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", nil)
	})
	link := startHTTPTransport(t, server.URL)
	server.Close() // 主动断开，制造网络错误

	err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, nil)
	if !errors.Is(err, types.ErrTransport) {
		t.Fatalf("网络错误应归一 ErrTransport：%v", err)
	}
}

// TestHTTPUnknownOperation - 未知操作本地拒绝
func TestHTTPUnknownOperation(t *testing.T) {

	link := startHTTPTransport(t, "http://127.0.0.1:1")

	err := link.Invoke(context.Background(), Operation("unknown.op"), nil, nil)
	if !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("未知操作应返回 ErrInvalidRequest：%v", err)
	}
}

// TestHTTPRequestTypeMismatch - 请求类型不匹配本地拒绝
func TestHTTPRequestTypeMismatch(t *testing.T) {

	link := startHTTPTransport(t, "http://127.0.0.1:1")

	err := link.Invoke(context.Background(), OpBucketEmpty, &types.ShareRevokeRequest{Id: 1}, nil)
	if !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("请求类型不匹配应返回 ErrInvalidRequest：%v", err)
	}
}

// customDecoder - 自定义 data 解码器（验证 dataUnmarshaler 扩展点）
type customDecoder struct {
	// called - 解码被调用
	called bool
	// raw - 收到的原始 data
	raw []byte
}

// UnmarshalData - 实现 dataUnmarshaler
func (this *customDecoder) UnmarshalData(raw []byte) error {

	this.called = true
	this.raw = append([]byte(nil), raw...)
	return nil
}

// TestHTTPDataUnmarshaler - 响应类型实现 UnmarshalData 时优先走自定义解码
func TestHTTPDataUnmarshaler(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(capture *httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{"custom": true})
	})
	link := startHTTPTransport(t, server.URL)

	decoder := &customDecoder{}
	if err := link.Invoke(context.Background(), OpAccessKeyVerify, nil, decoder); err != nil {
		t.Fatalf("调用失败：%v", err)
	}
	if !decoder.called || !strings.Contains(string(decoder.raw), `"custom":true`) {
		t.Fatalf("自定义解码器未被调用或数据不符：%+v", decoder)
	}
}

// ============================= HTTP 链路测试 - 结束 =============================

// 黄金向量引用（从 sign 包测试拷贝，锚定「链路产出签名 = 服务端实测值」）
const (
	// goldenEmptyBucket - 清空桶
	goldenEmptyBucket = "9B3034217A86D8B156309458C8544B2B"
	// goldenCreateShare - 签发分享（默认值 expires=0）
	goldenCreateShare = "2525E294E7BA40163FA370D18C9DFF5F"
	// goldenListRecycle - 回收站列表（默认值 page=1/limit=20）
	goldenListRecycle = "AEE6258AD56E2D9D91511984034DB4D7"
	// goldenRestoreObjects - 回收站还原（ids 逗号串）
	goldenRestoreObjects = "3D3AB2872B827B81BA1B09ED1CEE514C"
)
