// Package protocol 提供 Licence HTTP/gRPC 双协议共享的请求签名 canonical。
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
