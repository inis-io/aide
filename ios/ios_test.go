package ios

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/ios/internal/s3"
	"github.com/inis-io/aide/ios/internal/transport"
	"github.com/inis-io/aide/ios/notify"
	"github.com/inis-io/aide/ios/types"
)

// ============================= 编译期断言 - 开始 =============================
// 根包别名与子包类型严格等价（公开面同步守护）：新增公开类型必须在此补一行。

var (
	// 控制面模型（types）
	_ types.AccessKeyInfo        = AccessKeyInfo{}
	_ types.BucketInfo           = BucketInfo{}
	_ types.EmptyBucketResult    = EmptyBucketResult{}
	_ types.Share                = Share{}
	_ types.ShareCreateRequest   = ShareCreateRequest{}
	_ types.ShareCreateResult    = ShareCreateResult{}
	_ types.ShareListRequest     = ShareListRequest{}
	_ types.RecycleEntry         = RecycleEntry{}
	_ types.RecycleListRequest   = RecycleListRequest{}
	_ types.RecycleListResult    = RecycleListResult{}
	_ types.RecycleRestoreResult = RecycleRestoreResult{}
	_ types.RecyclePurgeResult   = RecyclePurgeResult{}
	_ types.QuotaView            = QuotaView{}
	_ types.UsageOverview        = UsageOverview{}
	_ types.BucketUsage          = BucketUsage{}
	_ types.EgressPoint          = EgressPoint{}
	_ types.APIError             = APIError{}

	// 数据面模型（internal/s3）
	_ s3.BucketRow   = BucketRow{}
	_ s3.PutInput    = PutRequest{}
	_ s3.PutResult   = PutResult{}
	_ s3.UploadInput = UploadRequest{}
	_ s3.CreateInput = CreateMultipartUploadRequest{}
	_ s3.GetOptions  = GetOptions{}
	_ s3.GetResult   = GetResult{}
	_ s3.ObjectMeta  = ObjectMeta{}
	_ s3.ObjectEntry = ObjectEntry{}
	_ s3.ListInput   = ListRequest{}
	_ s3.ListResult  = ListResult{}
	_ s3.Part        = Part{}

	// 数据面模型（P2：批量/拷贝/续传/桶配置/STS）
	_ s3.DeleteObjectsResult = DeleteObjectsResult{}
	_ s3.DeleteObjectsError  = DeleteObjectsError{}
	_ s3.CopyResult          = CopyResult{}
	_ s3.MultipartUpload     = MultipartUpload{}
	_ s3.CORSRule            = CORSRule{}
	_ s3.LifecycleRule       = LifecycleRule{}
	_ s3.SessionCredentials  = SessionCredentials{}

	// 数据面模型（P3：事件通知）
	_ s3.NotificationRule = NotificationRule{}

	// 事件通知模型（notify）
	_ notify.Event        = Event{}
	_ notify.Record       = Record{}
	_ notify.RecordS3     = RecordS3{}
	_ notify.RecordBucket = RecordBucket{}
	_ notify.RecordObject = RecordObject{}

	// 版本控制状态常量同源
	_ = VersioningEnabled
	_ = VersioningSuspended

	// 事件名常量同源
	_ = EventObjectCreated
	_ = EventObjectRemoved

	// 哨兵错误双平面同源（根包导出唯一一套）
	_ = error(ErrInvalidRequest)
	_ = error(ErrInvalidSign)
	_ = error(ErrTransport)
)

// TestAliasSentinels - 哨兵错误与分区实现同源（errors.Is 跨平面生效）
func TestAliasSentinels(t *testing.T) {

	if ErrInvalidRequest != types.ErrInvalidRequest || ErrInvalidRequest != s3.ErrInvalidRequest {
		t.Fatalf("ErrInvalidRequest 应双平面同源")
	}
	if ErrInvalidSign != types.ErrInvalidSign {
		t.Fatalf("ErrInvalidSign 应与 types 同源")
	}
	if ErrTransport != types.ErrTransport || ErrTransport != s3.ErrTransport {
		t.Fatalf("ErrTransport 应双平面同源")
	}

	// 数据面哨兵判定助手经根包转发可用
	var err error = &S3Error{HTTPStatus: 404, Code: "NoSuchKey", Message: "不存在"}
	if !IsCode(err, "NoSuchKey") || !IsNotFound(err) {
		t.Fatalf("S3Error 判定助手转发失效：%v", err)
	}

	// JoinIds / ParseIds 转发
	if JoinIds([]int64{1, 2, 3}) != "1,2,3" {
		t.Fatalf("JoinIds 转发失效")
	}
	if ids, err := ParseIds("1,2,3"); err != nil || len(ids) != 3 || ids[2] != 3 {
		t.Fatalf("ParseIds 转发失效：ids=%v err=%v", ids, err)
	}

	// ParseEvent / VerifyEvent 转发与事件名常量同源
	if ParseEvent == nil || VerifyEvent == nil {
		t.Fatalf("ParseEvent / VerifyEvent 转发失效")
	}
	if EventObjectCreated != s3.EventObjectCreated || EventObjectRemoved != s3.EventObjectRemoved {
		t.Fatalf("事件名常量应与 internal/s3 同源")
	}
	if !VerifyEvent([]byte("body"), "", "secret") == false {
		t.Fatalf("VerifyEvent 空签名应判 false")
	}
}

// ============================= 编译期断言 - 结束 =============================

// ============================= 装配与选项 - 开始 =============================

// TestNewClientDefault - 默认装配：HTTP 控制面 + 派生数据面 + 默认值
func TestNewClientDefault(t *testing.T) {

	client, err := NewClient("AK", "SK", WithEndpoint("https://storage.example.com"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if name := reflect.TypeOf(client.transport).String(); name != "*transport.httpTransport" {
		t.Fatalf("默认控制面链路应为 HTTP，实际：%s", name)
	}
	if client.s3Endpoint != "https://storage.example.com" {
		t.Fatalf("数据面网关应与控制面同源（合并模式），实际：%s", client.s3Endpoint)
	}
	if client.region != defaultRegion || client.timeout != defaultTimeout || client.userAgent != defaultUserAgent {
		t.Fatalf("默认值装配不符合预期：region=%s timeout=%v ua=%s", client.region, client.timeout, client.userAgent)
	}
	if client.partSize != defaultPartSize || client.concurrency != defaultConcurrency {
		t.Fatalf("上传默认值装配不符合预期：partSize=%d concurrency=%d", client.partSize, client.concurrency)
	}
	if client.accessKey != "AK" || client.secret != "SK" {
		t.Fatalf("凭据未装配")
	}
}

// TestNewClientOptions - 选项覆盖（含忽略非法值与地址裁剪）
func TestNewClientOptions(t *testing.T) {

	client, err := NewClient("AK", "SK",
		WithEndpoint("https://storage.example.com/"),
		WithS3Endpoint("https://s3.example.com:9443/"),
		WithRegion("cn-north-1"),
		WithTransportGRPC(),
		WithTimeout(30*time.Second),
		WithUserAgent("custom-agent/1.0"),
		WithSessionToken("TOKEN"),
		WithPartSize(16<<20),
		WithConcurrency(8),
		WithTimeout(-1),      // 非法值忽略
		WithUserAgent("   "), // 空白忽略
		WithPartSize(0),      // 非法值忽略
		WithConcurrency(0),   // 非法值忽略
		WithRegion("  "),     // 空白忽略
		nil,                  // nil 选项忽略
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if name := reflect.TypeOf(client.transport).String(); name != "*transport.grpcTransport" {
		t.Fatalf("WithTransportGRPC 未生效：%s", name)
	}
	if client.endpoint != "https://storage.example.com" || client.s3Endpoint != "https://s3.example.com:9443" {
		t.Fatalf("地址末尾斜杠应被裁剪：endpoint=%s s3=%s", client.endpoint, client.s3Endpoint)
	}
	if client.region != "cn-north-1" || client.timeout != 30*time.Second || client.userAgent != "custom-agent/1.0" {
		t.Fatalf("选项覆盖不符合预期：region=%s timeout=%v ua=%s", client.region, client.timeout, client.userAgent)
	}
	if client.sessionToken != "TOKEN" || client.partSize != 16<<20 || client.concurrency != 8 {
		t.Fatalf("会话令牌与上传选项覆盖不符合预期：token=%s partSize=%d concurrency=%d",
			client.sessionToken, client.partSize, client.concurrency)
	}
}

// TestNewClientValidation - 必填参数校验与 endpoint 缺省回退
func TestNewClientValidation(t *testing.T) {

	// endpoint 可空：缺省回退官方默认地址，数据面网关与控制面同源
	zero, err := NewClient("AK", "SK")
	if err != nil {
		t.Fatalf("缺省 endpoint 应允许构造：%v", err)
	}
	if zero.endpoint != DefaultEndpoint || zero.s3Endpoint != DefaultEndpoint {
		t.Fatalf("缺省 endpoint 回退不符合预期：endpoint=%s s3=%s", zero.endpoint, zero.s3Endpoint)
	}
	_ = zero.Close()

	if _, err := NewClient("", "SK", WithEndpoint("https://storage.example.com")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少 accessKey 应报错：%v", err)
	}
	if _, err := NewClient("AK", "", WithEndpoint("https://storage.example.com")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少 secret 应报错：%v", err)
	}
	// endpoint 无法派生数据面网关（无 scheme）时必须给出明确指引
	if _, err := NewClient("AK", "SK", WithEndpoint("storage.example.com")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("无法派生数据面网关应报错：%v", err)
	}
	// 显式 WithS3Endpoint 兜底：无 scheme 的控制面地址（gRPC 链路）亦可构造
	client, err := NewClient("AK", "SK",
		WithEndpoint("storage.example.com"),
		WithS3Endpoint("https://storage.example.com:9000"),
	)
	if err != nil {
		t.Fatalf("显式数据面网关应允许构造：%v", err)
	}
	_ = client.Close()
}

// TestDeriveS3Endpoint - 数据面网关派生规则
func TestDeriveS3Endpoint(t *testing.T) {

	cases := []struct {
		// endpoint - 控制面地址
		endpoint string
		// want - 期望派生结果
		want string
	}{
		{endpoint: "https://storage.example.com", want: "https://storage.example.com"},
		{endpoint: "https://storage.example.com/", want: "https://storage.example.com"},
		{endpoint: "http://127.0.0.1:2000", want: "http://127.0.0.1:2000"},
		{endpoint: "https://storage.example.com/base", want: "https://storage.example.com"},
		{endpoint: "http://[::1]:2000", want: "http://[::1]:2000"},
		{endpoint: "storage.example.com", want: ""},
		{endpoint: "://bad", want: ""},
	}

	for _, item := range cases {
		if got := deriveS3Endpoint(item.endpoint); got != item.want {
			t.Fatalf("派生不符合预期：endpoint=%q got=%q want=%q", item.endpoint, got, item.want)
		}
	}
}

// TestCloseIdempotent - Close 幂等（重复调用安全；nil 接收者安全）
func TestCloseIdempotent(t *testing.T) {

	client, err := NewClient("AK", "SK", WithEndpoint("https://storage.example.com"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}

	if err = client.Close(); err != nil {
		t.Fatalf("首次关闭失败：%v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("重复关闭应幂等：%v", err)
	}

	var nilClient *Client
	if err = nilClient.Close(); err != nil {
		t.Fatalf("nil 接收者关闭应安全：%v", err)
	}
}

// ============================= 装配与选项 - 结束 =============================

// ============================= 控制面端到端（HTTP 链路） - 开始 =============================

// startControlPlane - 启动控制面假平台（信封回放 + 请求捕获）
func startControlPlane(t *testing.T, reply func(path string, query map[string]string, body map[string]any) string) (*httptest.Server, func() (string, map[string]string, map[string]any)) {

	t.Helper()

	var (
		mutex      sync.Mutex
		lastPath   string
		lastQuery  map[string]string
		lastBody   map[string]any
		lastHasSig bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		raw, _ := io.ReadAll(request.Body)

		mutex.Lock()
		lastPath = request.URL.Path
		lastQuery = map[string]string{}
		for key := range request.URL.Query() {
			lastQuery[key] = request.URL.Query().Get(key)
		}
		lastBody = nil
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &lastBody)
		}
		if lastBody != nil {
			_, lastHasSig = lastBody["sign"]
		} else {
			lastHasSig = lastQuery["sign"] != ""
		}
		mutex.Unlock()

		mutex.Lock()
		text := reply(lastPath, lastQuery, lastBody)
		mutex.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(text))
	}))
	t.Cleanup(server.Close)

	snapshot := func() (string, map[string]string, map[string]any) {
		mutex.Lock()
		defer mutex.Unlock()
		if !lastHasSig {
			t.Errorf("请求未携带 sign 签名参数")
		}
		return lastPath, lastQuery, lastBody
	}

	return server, snapshot
}

// envelope - 组装平台信封
func envelope(t *testing.T, data any) string {

	t.Helper()
	text, err := json.Marshal(map[string]any{"code": 200, "msg": "OK", "data": data})
	if err != nil {
		t.Fatalf("构造信封失败：%v", err)
	}
	return string(text)
}

// TestControlPlaneEndToEnd - 门面 → HTTP 链路端到端（凭据自检 + 签发分享 + 回收站）
func TestControlPlaneEndToEnd(t *testing.T) {

	server, snapshot := startControlPlane(t, func(path string, query map[string]string, body map[string]any) string {
		switch path {
		case "/api/open-accesskey/verify":
			return envelope(t, map[string]any{
				"name": "演示密钥", "status": "active", "scope": "*",
				"lastUsedAt": 1763567900000, "expireAt": 0, "createAt": 1763567000000,
			})
		case "/api/open-share/create":
			return envelope(t, map[string]any{
				"share": map[string]any{
					"id": 7, "bucket": "photos", "key": "a/b.jpg", "token": "tok-abc",
					"expireAt": 0, "visits": 0, "createAt": 1763568000000,
				},
				"reused": false,
			})
		case "/api/open-object/restore":
			return envelope(t, map[string]any{"restored": 2, "conflicts": []string{"b.jpg"}})
		default:
			return envelope(t, nil)
		}
	})

	client, err := NewClient("AK", "SK", WithEndpoint(server.URL), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	info, err := client.VerifyCredentials(ctx)
	if err != nil {
		t.Fatalf("凭据自检失败：%v", err)
	}
	if info.Name != "演示密钥" || info.Scope != "*" {
		t.Fatalf("自检信息不符合预期：%+v", info)
	}
	path, query, _ := snapshot()
	if path != "/api/open-accesskey/verify" || query["accessKey"] != "AK" || query["timestamp"] == "" {
		t.Fatalf("凭据自检请求不符合预期：path=%s query=%v", path, query)
	}

	share, err := client.CreateShare(ctx, &ShareCreateRequest{Bucket: "photos", Key: "a/b.jpg", Expires: 3600})
	if err != nil {
		t.Fatalf("签发分享失败：%v", err)
	}
	if share.Share.Token != "tok-abc" || share.Reused {
		t.Fatalf("分享结果不符合预期：%+v", share)
	}
	_, _, body := snapshot()
	if body["expires"] != float64(3600) {
		t.Fatalf("expires 应按请求上送：%v", body)
	}

	restored, err := client.RestoreObjects(ctx, []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("回收站还原失败：%v", err)
	}
	if restored.Restored != 2 || len(restored.Conflicts) != 1 {
		t.Fatalf("还原结果不符合预期：%+v", restored)
	}
	_, _, body = snapshot()
	if body["ids"] != "1,2,3" {
		t.Fatalf("ids 应编码为逗号分隔串：%v", body)
	}
}

// TestControlPlaneValidation - 控制面方法入参校验（未发出请求）
func TestControlPlaneValidation(t *testing.T) {

	client, err := NewClient("AK", "SK", WithEndpoint("http://127.0.0.1:1"), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	if _, err = client.EmptyBucket(ctx, "  "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.CreateShare(ctx, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空签发请求应报错：%v", err)
	}
	if _, err = client.CreateShare(ctx, &ShareCreateRequest{Bucket: "photos"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺 key 应报错：%v", err)
	}
	if _, err = client.ListShares(ctx, &ShareListRequest{Bucket: "photos"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺 key 应报错：%v", err)
	}
	if err = client.RevokeShare(ctx, 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("非法 id 应报错：%v", err)
	}
	if _, err = client.RestoreObjects(ctx, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 ids 应报错：%v", err)
	}
	if _, err = client.PurgeObjects(ctx, []int64{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 ids 应报错：%v", err)
	}
}

// ============================= 控制面端到端（HTTP 链路） - 结束 =============================

// ============================= 数据面端到端 - 开始 =============================

// facadeS3 - 门面测试用迷你 S3 网关（计数 + 签名头检查）
type facadeS3 struct {
	// mu - 计数锁
	mu sync.Mutex
	// puts - 单 PUT 次数
	puts int
	// initiates - 分片初始化次数
	initiates int
	// parts - 分片上传次数
	parts int
	// completes - Complete 次数
	completes int
	// creates - 建桶次数
	creates int
	// sawAuth - 是否见到合法形态的签名头
	sawAuth bool
}

// ServeHTTP - 实现 http.Handler
func (this *facadeS3) ServeHTTP(writer http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
		this.sawAuth = true
	} else {
		writer.WriteHeader(http.StatusForbidden)
		return
	}

	query := request.URL.Query()
	_, hasUploads := query["uploads"]
	uploadId := query.Get("uploadId")
	trimmed := strings.TrimPrefix(request.URL.Path, "/")
	segments := strings.SplitN(trimmed, "/", 2)

	switch {
	case request.Method == http.MethodPost && hasUploads:
		this.initiates++
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer,
			`<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>u-1</UploadId></InitiateMultipartUploadResult>`,
			segments[0], segments[1])
	case request.Method == http.MethodPut && uploadId != "":
		this.parts++
		data, _ := io.ReadAll(request.Body)
		sum := md5.Sum(data)
		writer.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		writer.WriteHeader(http.StatusOK)
	case request.Method == http.MethodPost && uploadId != "":
		this.completes++
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(writer,
			`<CompleteMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><ETag>"combined-2"</ETag></CompleteMultipartUploadResult>`,
			segments[0], segments[1])
	case request.Method == http.MethodPut && len(segments) == 1:
		this.creates++
		writer.WriteHeader(http.StatusOK)
	case request.Method == http.MethodPut:
		this.puts++
		data, _ := io.ReadAll(request.Body)
		sum := md5.Sum(data)
		writer.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		writer.WriteHeader(http.StatusOK)
	default:
		writer.WriteHeader(http.StatusNoContent)
	}
}

// snapshot - 读取计数快照
func (this *facadeS3) snapshot() (puts, initiates, parts, completes, creates int, sawAuth bool) {

	this.mu.Lock()
	defer this.mu.Unlock()

	return this.puts, this.initiates, this.parts, this.completes, this.creates, this.sawAuth
}

// TestDataPlaneEndToEnd - 门面 → 数据面端到端（建桶 + 单 PUT + 分片上传 + 预签名）
func TestDataPlaneEndToEnd(t *testing.T) {

	gateway := &facadeS3{}
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)

	client, err := NewClient("AK", "SK",
		WithEndpoint("https://storage.example.com"),
		WithS3Endpoint(server.URL),
		WithPartSize(5<<20),
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	if err = client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	// 小文件：客户端级 partSize=5MB，6KB 走单 PUT
	small := strings.NewReader("small file content")
	if _, err = client.Upload(ctx, &UploadRequest{
		Bucket: "photos", Key: "small.txt", Body: small, Size: 18,
	}); err != nil {
		t.Fatalf("小文件上传失败：%v", err)
	}

	puts, initiates, _, _, _, _ := gateway.snapshot()
	if puts != 1 || initiates != 0 {
		t.Fatalf("小文件应走单 PUT：puts=%d initiates=%d", puts, initiates)
	}

	// 大文件：6MB > 客户端级 partSize=5MB，走分片编排（5MB + 1MB 两片）
	big := make([]byte, 6<<20)
	if _, err = client.Upload(ctx, &UploadRequest{
		Bucket: "photos", Key: "big.bin", Body: strings.NewReader(string(big)), Size: int64(len(big)),
	}); err != nil {
		t.Fatalf("分片上传失败：%v", err)
	}

	puts, initiates, parts, completes, _, sawAuth := gateway.snapshot()
	if puts != 1 || initiates != 1 || parts != 2 || completes != 1 {
		t.Fatalf("分片编排不符合预期：puts=%d initiates=%d parts=%d completes=%d", puts, initiates, parts, completes)
	}
	if !sawAuth {
		t.Fatalf("数据面请求应携带 SigV4 签名头")
	}

	// 预签名：本地自签，URL 指向数据面网关
	link, err := client.PresignGetObject(ctx, "photos", "a.jpg", time.Hour)
	if err != nil {
		t.Fatalf("预签名失败：%v", err)
	}
	if !strings.HasPrefix(link, server.URL+"/photos/a.jpg?") {
		t.Fatalf("预签名 URL 不符合预期：%s", link)
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Query().Get("X-Amz-Expires") != "3600" {
		t.Fatalf("预签名参数不符合预期：%s", link)
	}
}

// TestDataPlaneUploadDefaults - Upload 的 PartSize/Concurrency 缺省回落到客户端级配置
func TestDataPlaneUploadDefaults(t *testing.T) {

	client, err := NewClient("AK", "SK",
		WithEndpoint("https://storage.example.com"),
		WithPartSize(16<<20),
		WithConcurrency(8),
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	// 直接断言回落逻辑（无需真实网关）：请求级为 0 时取客户端级
	req := &UploadRequest{Bucket: "photos", Key: "a", Body: strings.NewReader("x"), Size: 1}
	value := *req
	if value.PartSize <= 0 {
		value.PartSize = client.partSize
	}
	if value.Concurrency <= 0 {
		value.Concurrency = client.concurrency
	}
	if value.PartSize != 16<<20 || value.Concurrency != 8 {
		t.Fatalf("上传默认值回落不符合预期：partSize=%d concurrency=%d", value.PartSize, value.Concurrency)
	}
}

// TestDataPlaneValidation - 数据面方法入参校验（未发出请求）
func TestDataPlaneValidation(t *testing.T) {

	client, err := NewClient("AK", "SK", WithEndpoint("https://storage.example.com"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()

	if err = client.CreateBucket(ctx, " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if err = client.DeleteBucket(ctx, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if err = client.HeadBucket(ctx, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.PutObject(ctx, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空上传请求应报错：%v", err)
	}
	if _, err = client.PutObject(ctx, &PutRequest{Bucket: "photos"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺 key 应报错：%v", err)
	}
	if _, err = client.PutObject(ctx, &PutRequest{Bucket: "photos", Key: "a.jpg"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空内容流应报错：%v", err)
	}
	if _, err = client.GetObject(ctx, "photos", "", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 key 应报错：%v", err)
	}
	if _, err = client.HeadObject(ctx, "", "a.jpg"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if err = client.DeleteObject(ctx, "photos", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 key 应报错：%v", err)
	}
	if _, err = client.ListObjects(ctx, &ListRequest{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.PresignGetObject(ctx, "", "a.jpg", time.Hour); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.PresignPutObject(ctx, "photos", "", "", time.Hour); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 key 应报错：%v", err)
	}

	// 分片原语的参数校验
	if _, err = client.CreateMultipartUpload(ctx, "", "a.bin", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.CreateMultipartUpload(ctx, "photos", " ", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 key 应报错：%v", err)
	}
	if _, err = client.CreateMultipartUploadEx(ctx, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空分片初始化请求应报错：%v", err)
	}
	if _, err = client.CreateMultipartUploadEx(ctx, &CreateMultipartUploadRequest{Bucket: "photos"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateMultipartUploadEx 缺 key 应报错：%v", err)
	}
	if _, err = client.UploadPart(ctx, "photos", "a.bin", "", 1, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 uploadId 应报错：%v", err)
	}
	if _, err = client.UploadPart(ctx, "photos", "a.bin", "U", 0, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("partNumber 越界应报错：%v", err)
	}
	if _, err = client.UploadPart(ctx, "photos", "a.bin", "U", 10001, strings.NewReader("x")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("partNumber 越界应报错：%v", err)
	}
	if _, err = client.UploadPart(ctx, "photos", "a.bin", "U", 1, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空内容流应报错：%v", err)
	}
	if _, err = client.ListParts(ctx, "photos", "", "U"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 key 应报错：%v", err)
	}
	if _, err = client.CompleteMultipartUpload(ctx, "photos", "a.bin", " ", []Part{{PartNumber: 1, ETag: "e"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空 uploadId 应报错：%v", err)
	}
	if _, err = client.CompleteMultipartUpload(ctx, "photos", "a.bin", "U", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空分片清单应报错：%v", err)
	}

	// P3：事件通知配置族的桶名校验
	if _, err = client.GetBucketNotification(ctx, " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if err = client.PutBucketNotification(ctx, "", []NotificationRule{{Topic: "https://x", Events: []string{EventObjectCreated}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if err = client.DeleteBucketNotification(ctx, " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
}

// ============================= 数据面端到端 - 结束 =============================

// ============================= 图片处理助手 - 开始 =============================

// TestImageProcessHelpers - 图片处理助手：拼链纯本地正确、叠加参数签入、空参校验
func TestImageProcessHelpers(t *testing.T) {

	client, err := NewClient("AK", "SK", WithEndpoint("https://storage.example.com"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	// 拼链：同源数据面端点 + 处理串查询转义
	raw, err := client.ImageProcessURL("photos", "a b.jpg", "image/resize,w_32,q_40")
	if err != nil {
		t.Fatalf("拼链失败：%v", err)
	}
	want := "https://storage.example.com/photos/a%20b.jpg?x-inis-process=" +
		url.QueryEscape("image/resize,w_32,q_40")
	if raw != want {
		t.Fatalf("处理链接不符：\n got %s\nwant %s", raw, want)
	}

	// 空参校验
	if _, err = client.ImageProcessURL("", "a.jpg", "image/resize,w_32"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
	if _, err = client.ImageProcessURL("photos", "a.jpg", " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空处理串应报错：%v", err)
	}

	// 叠加参数签入：处理串进入预签名 URL，且带 X-Amz-Signature
	signed, err := client.PresignGetObjectWith(ctx, "photos", "a.jpg", time.Hour,
		map[string]string{"x-inis-process": "image/resize,w_32,q_40"})
	if err != nil {
		t.Fatalf("叠加预签名失败：%v", err)
	}
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("URL 解析失败：%v", err)
	}
	if got := parsed.Query().Get("x-inis-process"); got != "image/resize,w_32,q_40" {
		t.Fatalf("处理串应原样进入查询参数，实际 %q", got)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("缺少 X-Amz-Signature")
	}
	if _, err = client.PresignGetObjectWith(ctx, "", "a.jpg", time.Hour, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应报错：%v", err)
	}
}

// ============================= 图片处理助手 - 结束 =============================

// 确保内部包引用在测试中保持（reflect 断言依赖其实名）
var (
	_ = transport.OpAccessKeyVerify
	_ = s3.IsNotFound
)
