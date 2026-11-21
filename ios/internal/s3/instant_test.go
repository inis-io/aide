package s3

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// ============================= 秒传探测 - 开始 =============================

// instantGateway - 秒传探测假网关（记录探测与回退 PUT，按 mode 返回不同结果）
type instantGateway struct {
	// mu - 状态锁
	mu sync.Mutex
	// mode - hit / miss / legacy / broken
	mode string
	// instantHeaders - 探测请求头序列
	instantHeaders []http.Header
	// instantBodies - 探测请求体序列（契约上应为空）
	instantBodies [][]byte
	// putHeaders - 回退 PUT 请求头序列
	putHeaders []http.Header
	// putBodies - 回退 PUT 请求体序列
	putBodies [][]byte
}

// ServeHTTP - 路由分发（POST ?instant 探测 / PUT 回退上传）
func (this *instantGateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	switch {
	case request.Method == http.MethodPost && request.URL.Query().Has("instant"):
		data, _ := io.ReadAll(request.Body)
		this.instantHeaders = append(this.instantHeaders, request.Header.Clone())
		this.instantBodies = append(this.instantBodies, data)
		switch this.mode {
		case "hit":
			response.Header().Set("Content-Type", "application/xml")
			response.Header().Set("x-amz-version-id", "instant-v1")
			_, _ = io.WriteString(response, `<InstantResult><ETag>"instant-etag"</ETag><Hit>true</Hit></InstantResult>`)
		case "legacy":
			// 旧网关：路由级 404，非 S3 XML 错误体
			response.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(response, "404 page not found")
		case "broken":
			// 协议错误：200 但 XML 非法（不得误判命中）
			response.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(response, `<InstantResult><ETag>"broken"</ETag>`)
		default:
			response.Header().Set("Content-Type", "application/xml")
			response.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(response, `<Error><Code>NoSuchContent</Code><Message>未命中</Message></Error>`)
		}
	case request.Method == http.MethodPut:
		data, _ := io.ReadAll(request.Body)
		this.putHeaders = append(this.putHeaders, request.Header.Clone())
		this.putBodies = append(this.putBodies, data)
		sum := md5.Sum(data)
		response.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
		response.WriteHeader(http.StatusOK)
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

// newInstantClient - 创建直连秒传假网关的客户端（注册清理）
func newInstantClient(t *testing.T, gateway *instantGateway) *Client {

	t.Helper()
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)

	client, err := New(Config{
		Endpoint:  server.URL,
		Region:    "us-east-1",
		AccessKey: "AK",
		Secret:    "SK",
	})
	if err != nil {
		t.Fatalf("装配客户端失败：%v", err)
	}
	return client
}

// nonSeekReader - 仅实现 Read 的纯流（不可 Seek，探测应直接跳过）
type nonSeekReader struct {
	// reader - 底层内容流
	reader io.Reader
}

// Read - 读取内容
func (this *nonSeekReader) Read(data []byte) (int, error) {

	return this.reader.Read(data)
}

// TestPutObjectInstantHit - 命中：零字节上行，返回探测 ETag/VersionId/本地 size，触发 OnInstantHit
func TestPutObjectInstantHit(t *testing.T) {

	gateway := &instantGateway{mode: "hit"}
	client := newInstantClient(t, gateway)
	content := []byte("秒传命中内容-instant")
	enabled := true
	var hitKey string

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/instant.png",
		Body:          bytes.NewReader(content),
		Mime:          "image/png",
		Meta:          map[string]string{"Name": "摸鱼"},
		CacheControl:  "public, max-age=604800",
		VerifyETag:    true,
		InstantUpload: &enabled,
		OnInstantHit:  func(key string) { hitKey = key },
	})
	if err != nil {
		t.Fatalf("秒传命中失败：%v", err)
	}
	if result.ETag != "instant-etag" {
		t.Fatalf("ETag 未取探测响应：%q", result.ETag)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("命中 Size 应为本地内容长度：%d", result.Size)
	}
	if result.VersionId != "instant-v1" {
		t.Fatalf("VersionId 未取响应头：%q", result.VersionId)
	}
	if hitKey != "community/instant.png" {
		t.Fatalf("OnInstantHit 未触发或 key 不符：%q", hitKey)
	}
	if len(gateway.putBodies) != 0 || len(gateway.putHeaders) != 0 {
		t.Fatalf("命中后不得再发 PUT（零字节上行）：puts=%d", len(gateway.putBodies))
	}
	if len(gateway.instantHeaders) != 1 {
		t.Fatalf("探测请求次数不符合预期：%d", len(gateway.instantHeaders))
	}
	if len(gateway.instantBodies[0]) != 0 {
		t.Fatalf("探测请求不得携带 body：%d", len(gateway.instantBodies[0]))
	}

	headers := gateway.instantHeaders[0]
	sum := sha256.Sum256(content)
	if got := headers.Get("X-Inis-Sha256"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("x-inis-sha256 不符：%q", got)
	}
	if got := headers.Get("X-Inis-Size"); got != strconv.Itoa(len(content)) {
		t.Fatalf("x-inis-size 不符：%q", got)
	}
	if got := headers.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type 未透传：%q", got)
	}
	if got := headers.Get("X-Amz-Meta-Name"); got != "摸鱼" {
		t.Fatalf("Meta 未透传：%q", got)
	}
	if got := headers.Get("Cache-Control"); got != "public, max-age=604800" {
		t.Fatalf("Cache-Control 未透传：%q", got)
	}
	if got := headers.Get("Authorization"); !strings.HasPrefix(got, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("探测请求未走 SigV4 签名：%q", got)
	}
}

// TestPutObjectInstantMissFallback - 未命中（404 NoSuchContent）：复位流后完整重传
func TestPutObjectInstantMissFallback(t *testing.T) {

	gateway := &instantGateway{mode: "miss"}
	client := newInstantClient(t, gateway)
	content := []byte("秒传未命中-完整重传")
	hit := false

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/miss.png",
		Body:         bytes.NewReader(content),
		Mime:         "image/png",
		Meta:         map[string]string{"Name": "摸鱼"},
		CacheControl: "public, max-age=604800",
		VerifyETag:   true,
		OnInstantHit: func(string) { hit = true },
	})
	if err != nil {
		t.Fatalf("未命中回退上传失败：%v", err)
	}
	sum := md5.Sum(content)
	if result.ETag != hex.EncodeToString(sum[:]) {
		t.Fatalf("回退上传 ETag 不符：%q", result.ETag)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("回退上传字节数不符：%d", result.Size)
	}
	if hit {
		t.Fatalf("未命中不得触发 OnInstantHit")
	}
	if len(gateway.instantHeaders) != 1 {
		t.Fatalf("探测请求次数不符合预期：%d", len(gateway.instantHeaders))
	}
	if len(gateway.putBodies) != 1 {
		t.Fatalf("回退后应恰好重传一次：%d", len(gateway.putBodies))
	}
	if !bytes.Equal(gateway.putBodies[0], content) {
		t.Fatalf("回退 PUT 体不完整：got=%q", gateway.putBodies[0])
	}
	if got := gateway.putHeaders[0].Get("Content-Type"); got != "image/png" {
		t.Fatalf("回退 PUT Content-Type 未透传：%q", got)
	}
	if got := gateway.putHeaders[0].Get("X-Amz-Meta-Name"); got != "摸鱼" {
		t.Fatalf("回退 PUT Meta 未透传：%q", got)
	}
	if got := gateway.putHeaders[0].Get("Cache-Control"); got != "public, max-age=604800" {
		t.Fatalf("回退 PUT Cache-Control 未透传：%q", got)
	}
}

// TestPutObjectInstantLegacyGatewayFallback - 旧网关路由级 404（非 S3 XML）：静默回退完整重传
func TestPutObjectInstantLegacyGatewayFallback(t *testing.T) {

	gateway := &instantGateway{mode: "legacy"}
	client := newInstantClient(t, gateway)
	content := []byte("旧网关-回退重传")

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/legacy.png",
		Body:       bytes.NewReader(content),
		VerifyETag: true,
	})
	if err != nil {
		t.Fatalf("旧网关回退失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("回退上传字节数不符：%d", result.Size)
	}
	if len(gateway.instantHeaders) != 1 || len(gateway.putBodies) != 1 {
		t.Fatalf("旧网关应探测一次后完整重传：instant=%d put=%d", len(gateway.instantHeaders), len(gateway.putBodies))
	}
	if !bytes.Equal(gateway.putBodies[0], content) {
		t.Fatalf("旧网关回退 PUT 体不完整：got=%q", gateway.putBodies[0])
	}
}

// TestPutObjectInstantProtocolErrorFallback - 200 但响应协议违例（XML 非法）：不得误判命中，回退重传
func TestPutObjectInstantProtocolErrorFallback(t *testing.T) {

	gateway := &instantGateway{mode: "broken"}
	client := newInstantClient(t, gateway)
	content := []byte("协议错误-回退重传")

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/broken.png",
		Body:       bytes.NewReader(content),
		VerifyETag: true,
	})
	if err != nil {
		t.Fatalf("协议错误回退失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("回退上传字节数不符：%d", result.Size)
	}
	if len(gateway.putBodies) != 1 || !bytes.Equal(gateway.putBodies[0], content) {
		t.Fatalf("协议错误后应完整重传：puts=%d", len(gateway.putBodies))
	}
}

// TestPutObjectInstantNonSeekableStream - 不可 Seek 的纯流：跳过探测，行为与现状一致
func TestPutObjectInstantNonSeekableStream(t *testing.T) {

	gateway := &instantGateway{mode: "hit"}
	client := newInstantClient(t, gateway)
	content := []byte("不可重置流-直传")

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/stream.png",
		Body:       &nonSeekReader{reader: bytes.NewReader(content)},
		VerifyETag: true,
	})
	if err != nil {
		t.Fatalf("纯流直传失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("直传字节数不符：%d", result.Size)
	}
	if len(gateway.instantHeaders) != 0 {
		t.Fatalf("不可 Seek 流不得探测：%d", len(gateway.instantHeaders))
	}
	if len(gateway.putBodies) != 1 || !bytes.Equal(gateway.putBodies[0], content) {
		t.Fatalf("纯流应完整直传：puts=%d", len(gateway.putBodies))
	}
}

// TestPutObjectInstantDisabled - 显式 InstantUpload=false：即使可 Seek 也跳过探测
func TestPutObjectInstantDisabled(t *testing.T) {

	gateway := &instantGateway{mode: "hit"}
	client := newInstantClient(t, gateway)
	content := []byte("显式关闭秒传-直传")
	disabled := false
	hit := false

	result, err := client.PutObject(context.Background(), PutInput{
		Bucket: "emoji", Key: "community/disabled.png",
		Body:          bytes.NewReader(content),
		VerifyETag:    true,
		InstantUpload: &disabled,
		OnInstantHit:  func(string) { hit = true },
	})
	if err != nil {
		t.Fatalf("关闭秒传后直传失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("直传字节数不符：%d", result.Size)
	}
	if hit {
		t.Fatalf("关闭秒传不得触发 OnInstantHit")
	}
	if len(gateway.instantHeaders) != 0 {
		t.Fatalf("显式关闭后不得探测：%d", len(gateway.instantHeaders))
	}
	if len(gateway.putBodies) != 1 || !bytes.Equal(gateway.putBodies[0], content) {
		t.Fatalf("关闭秒传后应完整直传：puts=%d", len(gateway.putBodies))
	}
}

// ============================= 秒传探测 - 结束 =============================
