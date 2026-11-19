package notify

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/inis-io/aide/ios/types"
)

// ============================= 黄金向量 - 开始 =============================
// 与服务端投递器（app/service/event/dispatcher.go postOnce）逐字节一致的
// hex HMAC-SHA256(body, secret) 实测值，是验签兼容性的验收线。

const (
	// goldenSecret - 测试签名密钥（形态同服务端生成的 20 字节 hex）
	goldenSecret = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b"
	// goldenBody - 测试事件报文（S3 风格 Records 载荷）
	goldenBody = `{"Records":[{"eventVersion":"1.0","eventSource":"inis:s3","eventTime":"2026-11-19T12:00:00Z","eventName":"s3:ObjectCreated:*","s3":{"bucket":{"name":"photos"},"object":{"key":"new.bin","size":11,"eTag":"5eb63bbbe01eeed093cb22bb8f5acdc3"}}}]}`
	// goldenSignature - goldenBody + goldenSecret 的投递签名（服务端算法实测）
	goldenSignature = "957a141dd2fc8fe55ae9d393075eb118f8c280c5c9f8a9e28f23071199ade56f"
)

// goldenHeader - 组装带签名头的请求头
func goldenHeader() http.Header {

	header := http.Header{}
	header.Set("X-Inis-Event", "s3:ObjectCreated:*")
	header.Set("X-Inis-Signature", goldenSignature)
	header.Set("X-Inis-Delivery", "delivery-0001")
	return header
}

// ============================= 黄金向量 - 结束 =============================

// TestVerifyEvent - 仅验签：黄金向量通过、大小写归一、篡改/错误密钥/空参拒绝
func TestVerifyEvent(t *testing.T) {

	if !VerifyEvent([]byte(goldenBody), goldenSignature, goldenSecret) {
		t.Fatalf("黄金向量验签应通过")
	}
	// 大写签名归一通过
	if !VerifyEvent([]byte(goldenBody), strings.ToUpper(goldenSignature), goldenSecret) {
		t.Fatalf("大写签名应归一通过")
	}
	// 篡改报文
	if VerifyEvent([]byte(goldenBody+" "), goldenSignature, goldenSecret) {
		t.Fatalf("篡改报文应拒绝")
	}
	// 错误密钥
	if VerifyEvent([]byte(goldenBody), goldenSignature, "wrong-secret") {
		t.Fatalf("错误密钥应拒绝")
	}
	// 空参
	if VerifyEvent([]byte(goldenBody), "", goldenSecret) || VerifyEvent([]byte(goldenBody), goldenSignature, "") {
		t.Fatalf("空签名或空密钥应拒绝")
	}
}

// TestParseEvent - 解析验签：黄金报文字段完整、投递号与事件名落位
func TestParseEvent(t *testing.T) {

	event, err := ParseEvent([]byte(goldenBody), goldenHeader(), goldenSecret)
	if err != nil {
		t.Fatalf("黄金报文验签应通过：%v", err)
	}
	if event.Name != "s3:ObjectCreated:*" || event.Delivery != "delivery-0001" {
		t.Fatalf("头字段落位不符：%+v", event)
	}
	if len(event.Records) != 1 {
		t.Fatalf("应有一条记录：%+v", event.Records)
	}
	record := event.Records[0]
	if record.EventVersion != "1.0" || record.EventSource != "inis:s3" ||
		record.EventTime != "2026-11-19T12:00:00Z" || record.EventName != "s3:ObjectCreated:*" {
		t.Fatalf("记录头字段不符：%+v", record)
	}
	if record.S3.Bucket.Name != "photos" {
		t.Fatalf("桶名不符：%+v", record.S3.Bucket)
	}
	if record.S3.Object.Key != "new.bin" || record.S3.Object.Size != 11 ||
		record.S3.Object.ETag != "5eb63bbbe01eeed093cb22bb8f5acdc3" || record.S3.Object.VersionId != "" {
		t.Fatalf("对象字段不符：%+v", record.S3.Object)
	}
}

// TestParseEventReject - 拒绝路径：缺头、篡改、错误密钥、空密钥、nil 头
func TestParseEventReject(t *testing.T) {

	// 缺少签名头 → ErrInvalidSign
	header := goldenHeader()
	header.Del("X-Inis-Signature")
	if _, err := ParseEvent([]byte(goldenBody), header, goldenSecret); !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("缺签名头应返回 ErrInvalidSign：%v", err)
	}

	// nil 头 → ErrInvalidSign
	if _, err := ParseEvent([]byte(goldenBody), nil, goldenSecret); !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("nil 头应返回 ErrInvalidSign：%v", err)
	}

	// 篡改报文 → ErrInvalidSign
	if _, err := ParseEvent([]byte(goldenBody+"\n"), goldenHeader(), goldenSecret); !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("篡改报文应返回 ErrInvalidSign：%v", err)
	}

	// 错误密钥 → ErrInvalidSign
	if _, err := ParseEvent([]byte(goldenBody), goldenHeader(), "wrong-secret"); !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("错误密钥应返回 ErrInvalidSign：%v", err)
	}

	// 空密钥 → ErrInvalidRequest（本地配置问题）
	if _, err := ParseEvent([]byte(goldenBody), goldenHeader(), ""); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("空密钥应返回 ErrInvalidRequest：%v", err)
	}
}

// TestParseEventBadJSON - 验签通过但载荷非法：普通错误（协议违例，不归 ErrInvalidSign）
func TestParseEventBadJSON(t *testing.T) {

	body := []byte("not-json")
	header := http.Header{}
	header.Set("X-Inis-Signature", sign(body, goldenSecret))

	_, err := ParseEvent(body, header, goldenSecret)
	if err == nil || errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("载荷非法应返回普通解析错误：%v", err)
	}
}
