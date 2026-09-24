// Package protocol 提供 Licen Hub 平台契约镜像层：
// 许可证签名信封（Envelope/Payload）、Ed25519 签发与验签原语、运行面状态码与本地判定、
// 版本范围语义，以及 HTTP/gRPC 双协议共享的请求签名 canonical。
// 载荷字段顺序即签名内容：新增字段只允许追加到末尾，禁止插入、重排或改名。
package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	// MetadataToken - gRPC 激活令牌 metadata key。
	MetadataToken = "x-license-token"
	// MetadataTimestamp - gRPC 请求签名时间戳 metadata key。
	MetadataTimestamp = "x-license-timestamp"
	// MetadataNonce - gRPC 请求签名 nonce metadata key。
	MetadataNonce = "x-license-nonce"
	// MetadataSignature - gRPC 请求签名 metadata key。
	MetadataSignature = "x-license-sign"
	// MetadataSignVersion - gRPC 请求签名版本 metadata key。
	MetadataSignVersion = "x-license-sign-version"
	// MetadataRequestID - 调用级幂等键 metadata key（等价 HTTP 头 X-Request-Id，头族映射见
	// licen-hub API 商城 04 文档 §2.2）。**不进签名 canonical**，与 HTTP 侧头不入签名同口径。
	MetadataRequestID = "x-request-id"
	// SignVersionV1 - 当前 gRPC 请求签名版本。
	SignVersionV1 = "1"
)

// HTTPContent - 构造 HTTP 运行面请求签名原文。
func HTTPContent(method, requestURI, timestamp, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	content := strings.ToUpper(method) + "\n" + requestURI + "\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
	return []byte(content)
}

// GRPCContent - 构造 gRPC 运行面请求签名原文。
func GRPCContent(fullMethod, timestamp, nonce string, request proto.Message) ([]byte, error) {
	if fullMethod == "" {
		return nil, errors.New("gRPC full method 不能为空")
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	content := "GRPC\n" + fullMethod + "\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
	return []byte(content), nil
}
