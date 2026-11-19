package s3

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ============================= 预签名叠加查询参数 - 开始 =============================

// presignTestClient - 固定时钟的本地预签名客户端（不触网）
func presignTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := New(Config{Endpoint: "https://storage.example.com:9000", AccessKey: "AK", Secret: "SK"})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return fixed }
	return client
}

// TestPresignGetWith - 叠加查询参数签入：处理串进入 URL 与 canonical query（签名随参数变化）
func TestPresignGetWith(t *testing.T) {

	client := presignTestClient(t)
	ctx := context.Background()
	process := "image/resize,w_32,q_40"

	raw, err := client.PresignGetWith(ctx, "photos", "a b.jpg", time.Hour,
		map[string]string{"x-inis-process": process})
	if err != nil {
		t.Fatalf("预签名失败：%v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL 解析失败：%v", err)
	}
	if got := parsed.Query().Get("x-inis-process"); got != process {
		t.Fatalf("处理串应原样进入查询参数，实际 %q", got)
	}
	if parsed.Query().Get("X-Amz-Signature") == "" {
		t.Fatal("缺少 X-Amz-Signature")
	}

	// 处理串参与签名：同时刻同键的裸链签名必须不同
	plain, err := client.PresignGet(ctx, "photos", "a b.jpg", time.Hour)
	if err != nil {
		t.Fatalf("裸链预签名失败：%v", err)
	}
	plainParsed, _ := url.Parse(plain)
	if parsed.Query().Get("X-Amz-Signature") == plainParsed.Query().Get("X-Amz-Signature") {
		t.Fatal("叠加参数后签名应变化（参数必须签入）")
	}

	// nil extraQuery 与 PresignGet 完全等价（固定时钟下逐字节一致）
	viaWith, err := client.PresignGetWith(ctx, "photos", "a b.jpg", time.Hour, nil)
	if err != nil {
		t.Fatalf("空参预签名失败：%v", err)
	}
	if viaWith != plain {
		t.Fatalf("nil extraQuery 应与 PresignGet 等价：\n%s\n%s", viaWith, plain)
	}
}

// TestImageProcessURL - 处理链接拼装：路径转义 + 处理串查询转义（纯本地，公开桶直用）
func TestImageProcessURL(t *testing.T) {

	client := presignTestClient(t)
	got := client.ImageProcessURL("photos", "a b.jpg", "image/resize,w_32,q_40")
	want := "https://storage.example.com:9000/photos/a%20b.jpg?x-inis-process=" +
		url.QueryEscape("image/resize,w_32,q_40")
	if got != want {
		t.Fatalf("处理链接不符：\n got %s\nwant %s", got, want)
	}
	if !strings.HasPrefix(got, "https://storage.example.com:9000/") {
		t.Fatalf("链接应以数据面端点开头：%s", got)
	}
}

// ============================= 预签名叠加查询参数 - 结束 =============================
