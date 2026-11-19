// Package s3 - ios SDK 的数据面轻客户端（S3 协议子集）。
//
// 直连平台 S3 网关（独立端口，默认 :9000）：SigV4 签名自实现（纯标准库），
// XML 编解码只覆盖 SDK 用到的操作子集；字节流全程流式，不整读内存。
//
// 本包是内部实现细节：调用方不直接接触，统一经根包 ios.Client 使用。
package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================= SigV4 签名 - 开始 =============================

const (
	// algorithm - 签名算法标识
	algorithm = "AWS4-HMAC-SHA256"
	// serviceS3 - 服务名（凭证作用域组成部分）
	serviceS3 = "s3"
	// terminator - 凭证作用域终止符
	terminator = "aws4_request"
	// unsignedPayload - 不签名载荷标记（平台网关支持；HTTPS 下客户端常用形态）
	unsignedPayload = "UNSIGNED-PAYLOAD"
	// emptyPayloadHash - 空载荷 SHA-256（GET/HEAD/DELETE 等无 body 请求）
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// credentials - 签名凭据
type credentials struct {
	// accessKey - AccessKey
	accessKey string
	// secret - SecretKey
	secret string
	// sessionToken - STS 会话令牌（空 = 永久凭证）
	sessionToken string
}

// signInput - 一次签名所需的全部输入
type signInput struct {
	// method - HTTP 方法（大写）
	method string
	// host - 请求主机（含端口）
	host string
	// path - 已转义的请求路径（/{bucket}/{key...}，段间 / 保留）
	path string
	// query - 查询参数（未转义键值；内部完成排序与转义）
	query map[string]string
	// payloadHash - x-amz-content-sha256 取值（UNSIGNED-PAYLOAD 或实哈希）
	payloadHash string
	// now - 签名时间
	now time.Time
	// region - 区域
	region string
	// expires - 预签名有效期（秒；>0 表示生成查询参数形态的预签名）
	expires int64
}

// uriEncode - RFC3986 转义（SigV4 规范：仅保留非保留字符 A-Za-z0-9-_.~）
// encodeSlash=false 时保留 /（用于路径），否则 %2F（用于查询键值与单段）。
func uriEncode(text string, encodeSlash bool) string {

	var builder strings.Builder
	builder.Grow(len(text))
	for i := 0; i < len(text); i++ {
		char := text[i]
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == '~' {
			builder.WriteByte(char)
			continue
		}
		if char == '/' && !encodeSlash {
			builder.WriteByte(char)
			continue
		}
		const digits = "0123456789ABCDEF"
		builder.WriteByte('%')
		builder.WriteByte(digits[char>>4])
		builder.WriteByte(digits[char&15])
	}
	return builder.String()
}

// escapePath - 对象键转义为请求路径（保留段间 /，逐段转义）
func escapePath(bucket, key string) string {

	path := "/" + uriEncode(bucket, true)
	if key != "" {
		path += "/" + uriEncode(key, false)
	}
	return path
}

// canonicalQuery - 规范查询串（键值均转义，按键升序，同键按值升序）
func canonicalQuery(query map[string]string) string {

	type pair struct{ key, value string }
	pairs := make([]pair, 0, len(query))
	for key, value := range query {
		pairs = append(pairs, pair{uriEncode(key, true), uriEncode(value, true)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].value < pairs[j].value
	})

	var builder strings.Builder
	for i, item := range pairs {
		if i > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(item.key)
		builder.WriteByte('=')
		builder.WriteString(item.value)
	}
	return builder.String()
}

// scope - 凭证作用域（date/region/s3/aws4_request）
func (this credentials) scope(date, region string) string {
	return date + "/" + region + "/" + serviceS3 + "/" + terminator
}

// signingKey - 派生签名密钥（HMAC 链：date → region → service → terminator）
func (this credentials) signingKey(date, region string) []byte {

	kDate := hmacSHA256([]byte("AWS4"+this.secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, serviceS3)
	return hmacSHA256(kService, terminator)
}

// hmacSHA256 - HMAC-SHA256
func hmacSHA256(key []byte, data string) []byte {

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// sha256Hex - SHA-256 十六进制
func sha256Hex(data []byte) string {

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sign - Header 形态签名：返回需写入请求的头部集合
func (this credentials) sign(in signInput) map[string]string {

	date := in.now.Format("20060102")
	amzDate := in.now.Format("20060102T150405Z")

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + in.host + "\n" +
		"x-amz-content-sha256:" + in.payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"

	headers := map[string]string{
		"X-Amz-Date":           amzDate,
		"X-Amz-Content-Sha256": in.payloadHash,
	}
	if this.sessionToken != "" {
		signedHeaders += ";x-amz-security-token"
		canonicalHeaders += "x-amz-security-token:" + this.sessionToken + "\n"
		headers["X-Amz-Security-Token"] = this.sessionToken
	}

	canonicalRequest := in.method + "\n" + in.path + "\n" + canonicalQuery(in.query) + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + in.payloadHash

	stringToSign := algorithm + "\n" + amzDate + "\n" + this.scope(date, in.region) + "\n" +
		sha256Hex([]byte(canonicalRequest))

	signature := hex.EncodeToString(hmacSHA256(this.signingKey(date, in.region), stringToSign))

	headers["Authorization"] = algorithm +
		" Credential=" + this.accessKey + "/" + this.scope(date, in.region) +
		", SignedHeaders=" + signedHeaders +
		", Signature=" + signature

	return headers
}

// presign - 查询参数形态签名（预签名 URL）：返回完整查询参数集（含 X-Amz-Signature）
func (this credentials) presign(in signInput) map[string]string {

	date := in.now.Format("20060102")
	amzDate := in.now.Format("20060102T150405Z")

	query := make(map[string]string, len(in.query)+6)
	for key, value := range in.query {
		query[key] = value
	}
	query["X-Amz-Algorithm"] = algorithm
	query["X-Amz-Credential"] = this.accessKey + "/" + this.scope(date, in.region)
	query["X-Amz-Date"] = amzDate
	query["X-Amz-Expires"] = strconv.FormatInt(in.expires, 10)
	query["X-Amz-SignedHeaders"] = "host"
	if this.sessionToken != "" {
		query["X-Amz-Security-Token"] = this.sessionToken
	}

	canonicalHeaders := "host:" + in.host + "\n"
	signedHeaders := "host"

	canonicalRequest := in.method + "\n" + in.path + "\n" + canonicalQuery(query) + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + unsignedPayload

	stringToSign := algorithm + "\n" + amzDate + "\n" + this.scope(date, in.region) + "\n" +
		sha256Hex([]byte(canonicalRequest))

	query["X-Amz-Signature"] = hex.EncodeToString(hmacSHA256(this.signingKey(date, in.region), stringToSign))

	return query
}

// ============================= SigV4 签名 - 结束 =============================
