package s3

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

// ============================= 元数据与缓存头 - 开始 =============================

// TestUserMetaHeaders - 元数据头键去空白、统一小写并按字典序输出（请求集合确定性）
func TestUserMetaHeaders(t *testing.T) {

	pairs := userMetaHeaders(map[string]string{
		"Name":   "摸鱼",
		" ZETA ": "1",
		"alpha":  "2",
		"":       "空键丢弃",
	})

	want := [][2]string{
		{"x-amz-meta-alpha", "2"},
		{"x-amz-meta-name", "摸鱼"},
		{"x-amz-meta-zeta", "1"},
	}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("元数据头装配不符合预期：got=%v want=%v", pairs, want)
	}

	if len(userMetaHeaders(nil)) != 0 {
		t.Fatalf("空元数据应装配为空集合")
	}
}

// TestPutObjectMetaAndCacheControl - httptest 假网关断言 PutObject 透传 Meta（键小写）与 Cache-Control
func TestPutObjectMetaAndCacheControl(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "emoji"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	content := []byte("moyu-png")
	result, err := client.PutObject(ctx, PutInput{
		Bucket: "emoji", Key: "community/moyu.png",
		Body:         strings.NewReader(string(content)),
		Mime:         "image/png",
		Meta:         map[string]string{"Name": "摸鱼", "Author": "rabbit"},
		CacheControl: "public, max-age=604800",
		VerifyETag:   true,
	})
	if err != nil {
		t.Fatalf("上传失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("写入字节数不符合预期：%d", result.Size)
	}

	headers := gateway.putHeaders
	if got := headers.Get("X-Amz-Meta-Name"); got != "摸鱼" {
		t.Fatalf("Meta name 未透传：%q", got)
	}
	if got := headers.Get("X-Amz-Meta-Author"); got != "rabbit" {
		t.Fatalf("Meta author 未透传：%q", got)
	}
	if got := headers.Get("Cache-Control"); got != "public, max-age=604800" {
		t.Fatalf("Cache-Control 未透传：%q", got)
	}
	if got := headers.Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type 未透传：%q", got)
	}

	// 读侧：GET/HEAD 解析 Cache-Control 响应头
	meta, err := client.HeadObject(ctx, "emoji", "community/moyu.png")
	if err != nil {
		t.Fatalf("取元数据失败：%v", err)
	}
	if meta.CacheControl != "public, max-age=604800" {
		t.Fatalf("Head 未解析 Cache-Control：%q", meta.CacheControl)
	}
}

// TestCreateMultipartUploadExHeaders - 分片会话初始化透传 Meta 与 Cache-Control，旧方法保持薄封装
func TestCreateMultipartUploadExHeaders(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "emoji"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	uploadId, err := client.CreateMultipartUploadEx(ctx, CreateInput{
		Bucket: "emoji", Key: "community/big.gif",
		Mime:         "image/gif",
		Meta:         map[string]string{"name": "大图"},
		CacheControl: "public, max-age=2592000",
	})
	if err != nil {
		t.Fatalf("初始化分片会话失败：%v", err)
	}
	if uploadId == "" {
		t.Fatalf("分片会话 ID 不能为空")
	}

	headers := gateway.initiateHeaders
	if got := headers.Get("X-Amz-Meta-Name"); got != "大图" {
		t.Fatalf("分片会话 Meta 未透传：%q", got)
	}
	if got := headers.Get("Cache-Control"); got != "public, max-age=2592000" {
		t.Fatalf("分片会话 Cache-Control 未透传：%q", got)
	}
	if got := headers.Get("Content-Type"); got != "image/gif" {
		t.Fatalf("分片会话 Content-Type 未透传：%q", got)
	}

	// 旧签名（bucket/key/mime）仍可用
	if _, err = client.CreateMultipartUpload(ctx, "emoji", "community/legacy.gif", "image/gif"); err != nil {
		t.Fatalf("旧 CreateMultipartUpload 薄封装失败：%v", err)
	}
}

// TestUploadMultipartForwardsMetaAndCacheControl - Upload 分片路径内部走 CreateMultipartUploadEx
func TestUploadMultipartForwardsMetaAndCacheControl(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "emoji"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	// 超过最小分片阈值（5MB）触发分片编排（2 片）
	content := bytes.Repeat([]byte("x"), int(minPartSize)+1)
	result, err := client.Upload(ctx, UploadInput{
		Bucket: "emoji", Key: "community/big.png",
		Body:         bytes.NewReader(content),
		Size:         int64(len(content)),
		Mime:         "image/png",
		Meta:         map[string]string{"name": "摸鱼"},
		CacheControl: "public, max-age=604800",
		PartSize:     minPartSize,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatalf("分片上传失败：%v", err)
	}
	if result.Size != int64(len(content)) {
		t.Fatalf("分片上传总字节数不符合预期：%d", result.Size)
	}

	headers := gateway.initiateHeaders
	if got := headers.Get("X-Amz-Meta-Name"); got != "摸鱼" {
		t.Fatalf("Upload 分片路径 Meta 未透传：%q", got)
	}
	if got := headers.Get("Cache-Control"); got != "public, max-age=604800" {
		t.Fatalf("Upload 分片路径 Cache-Control 未透传：%q", got)
	}
}

// ============================= 元数据与缓存头 - 结束 =============================
