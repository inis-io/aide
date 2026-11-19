package s3

import (
	"strings"
	"testing"
	"time"
)

// ============================= SigV4 黄金向量 - 开始 =============================
//
// 用例取自 AWS 官方文档《Signature Version 4 signing process》的经典示例，
// 是签名算法正确性的外部锚点（与平台网关实现的同源算法互为校验）。

// TestSignV4HeaderAWSExample - AWS 官方文档 GET object 示例（Header 形态）
// 文档：https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
// canonical request（GET /test.txt，空载荷哈希，host;x-amz-content-sha256;x-amz-date 签名头），
// 期望值由 AWS 文档同源参数独立复算锚定（df548e2c...）。
// 注意：官方示例密钥为 wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY（K7MDENG 后接 /bPxRfiCY 斜杠形态），
// 与另一组示例使用的 +bPxRfiCY（加号形态）仅一字之差，混用会导致签名恒不匹配。
func TestSignV4HeaderAWSExample(t *testing.T) {

	creds := credentials{accessKey: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	headers := creds.sign(signInput{
		method:      "GET",
		host:        "examplebucket.s3.amazonaws.com",
		path:        "/test.txt",
		query:       map[string]string{},
		payloadHash: emptyPayloadHash,
		now:         now,
		region:      "us-east-1",
	})

	authorization := headers["Authorization"]
	const wantSignature = "df548e2ce037944d03f3e68682813b093763996d597cf890ca3d9037fd231eb4"
	if !strings.Contains(authorization, "Signature="+wantSignature) {
		t.Fatalf("签名与官方向量不一致：%s", authorization)
	}
	if !strings.Contains(authorization, "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request") {
		t.Fatalf("凭证作用域不符合预期：%s", authorization)
	}
	if !strings.Contains(authorization, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Fatalf("签名头集合不符合预期：%s", authorization)
	}
	if headers["X-Amz-Date"] != "20130524T000000Z" {
		t.Fatalf("X-Amz-Date 不符合预期：%s", headers["X-Amz-Date"])
	}
}

// TestSignV4PresignAWSExample - AWS 官方文档预签名 GET object 示例（查询参数形态）
// canonical query（X-Amz-* 全量，SignedHeaders=host，UNSIGNED-PAYLOAD）的期望签名
// aeeed9bb...f604d404（AWS 文档与多语言实现共同锚定）。
func TestSignV4PresignAWSExample(t *testing.T) {

	creds := credentials{accessKey: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

	query := creds.presign(signInput{
		method:  "GET",
		host:    "examplebucket.s3.amazonaws.com",
		path:    "/test.txt",
		query:   map[string]string{},
		now:     now,
		region:  "us-east-1",
		expires: 86400,
	})

	const wantSignature = "aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if query["X-Amz-Signature"] != wantSignature {
		t.Fatalf("预签名与 AWS 官方示例不一致：got=%s want=%s", query["X-Amz-Signature"], wantSignature)
	}
	if query["X-Amz-Credential"] != "AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request" {
		t.Fatalf("X-Amz-Credential 不符合预期：%s", query["X-Amz-Credential"])
	}
	if query["X-Amz-Expires"] != "86400" {
		t.Fatalf("X-Amz-Expires 不符合预期：%s", query["X-Amz-Expires"])
	}
	if query["X-Amz-SignedHeaders"] != "host" {
		t.Fatalf("X-Amz-SignedHeaders 不符合预期：%s", query["X-Amz-SignedHeaders"])
	}
}

// TestSignV4SessionToken - 会话令牌参与签名（头集合与 canonical headers 均含 token）
func TestSignV4SessionToken(t *testing.T) {

	creds := credentials{accessKey: "AK", secret: "SK", sessionToken: "TOKEN"}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	headers := creds.sign(signInput{
		method:      "GET",
		host:        "storage.example.com:9000",
		path:        "/photos/a.jpg",
		query:       map[string]string{},
		payloadHash: emptyPayloadHash,
		now:         now,
		region:      "us-east-1",
	})

	if headers["X-Amz-Security-Token"] != "TOKEN" {
		t.Fatalf("缺少 X-Amz-Security-Token 头")
	}
	if !strings.Contains(headers["Authorization"], "SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-security-token") {
		t.Fatalf("签名头集合未包含 x-amz-security-token：%s", headers["Authorization"])
	}

	query := creds.presign(signInput{
		method:  "GET",
		host:    "storage.example.com:9000",
		path:    "/photos/a.jpg",
		query:   map[string]string{},
		now:     now,
		region:  "us-east-1",
		expires: 3600,
	})
	if query["X-Amz-Security-Token"] != "TOKEN" {
		t.Fatalf("预签名缺少 X-Amz-Security-Token 参数")
	}
}

// TestUriEncode - RFC3986 转义（SigV4 规范）
func TestUriEncode(t *testing.T) {

	tests := []struct {
		name        string
		input       string
		encodeSlash bool
		want        string
	}{
		{name: "非保留字符原样", input: "abcXYZ-_.~019", encodeSlash: true, want: "abcXYZ-_.~019"},
		{name: "空格百分号编码", input: "a b", encodeSlash: true, want: "a%20b"},
		{name: "斜杠保留", input: "a/b.jpg", encodeSlash: false, want: "a/b.jpg"},
		{name: "斜杠编码", input: "a/b.jpg", encodeSlash: true, want: "a%2Fb.jpg"},
		{name: "UTF-8 按字节编码", input: "中文", encodeSlash: true, want: "%E4%B8%AD%E6%96%87"},
		{name: "特殊字符", input: "a?b=c&d", encodeSlash: true, want: "a%3Fb%3Dc%26d"},
	}
	for _, test := range tests {
		if got := uriEncode(test.input, test.encodeSlash); got != test.want {
			t.Fatalf("%s：got=%q want=%q", test.name, got, test.want)
		}
	}
}

// TestEscapePath - 对象键路径转义（保留段间斜杠）
func TestEscapePath(t *testing.T) {

	if got := escapePath("photos", "a/b c.jpg"); got != "/photos/a/b%20c.jpg" {
		t.Fatalf("路径转义不符合预期：%q", got)
	}
	if got := escapePath("photos", ""); got != "/photos" {
		t.Fatalf("空 key 路径转义不符合预期：%q", got)
	}
	if got := escapePath("", ""); got != "/" {
		t.Fatalf("服务级路径转义不符合预期：%q", got)
	}
}

// TestCanonicalQuery - 规范查询串排序与转义
func TestCanonicalQuery(t *testing.T) {

	got := canonicalQuery(map[string]string{"b": "2 2", "a": "1", "c": ""})
	if got != "a=1&b=2%202&c=" {
		t.Fatalf("规范查询串不符合预期：%q", got)
	}
}

// ============================= SigV4 黄金向量 - 结束 =============================
