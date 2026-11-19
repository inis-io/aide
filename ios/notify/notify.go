// Package notify - ios SDK 的事件通知接收件（P3）。
//
// 平台事件以 Webhook 投递：POST application/json，头 X-Inis-Event（事件名）、
// X-Inis-Signature（hex HMAC-SHA256(body, secret)）、X-Inis-Delivery（投递号，幂等键）。
// 接入纪律：先验签 → 按投递号幂等落库 → 应答 2xx；验签无跳过开关。
package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/inis-io/aide/ios/types"
)

// ============================= 事件模型 - 开始 =============================

// RecordBucket - 事件桶信息
type RecordBucket struct {
	// Name - 桶名
	Name string `json:"name"`
}

// RecordObject - 事件对象信息
type RecordObject struct {
	// Key - 对象键
	Key string `json:"key"`
	// Size - 字节数
	Size int64 `json:"size"`
	// ETag - 实体标签
	ETag string `json:"eTag"`
	// VersionId - 版本号（无版本控制时缺省）
	VersionId string `json:"versionId,omitempty"`
}

// RecordS3 - S3 事件主体
type RecordS3 struct {
	// Bucket - 桶信息
	Bucket RecordBucket `json:"bucket"`
	// Object - 对象信息
	Object RecordObject `json:"object"`
}

// Record - 单条事件记录（S3 风格）
type Record struct {
	// EventVersion - 事件结构版本（当前 "1.0"）
	EventVersion string `json:"eventVersion"`
	// EventSource - 事件源（固定 "inis:s3"）
	EventSource string `json:"eventSource"`
	// EventTime - 事件时间（RFC3339 UTC）
	EventTime string `json:"eventTime"`
	// EventName - 事件名（s3:ObjectCreated:* / s3:ObjectRemoved:*）
	EventName string `json:"eventName"`
	// S3 - 事件主体
	S3 RecordS3 `json:"s3"`
}

// Event - 验签后的结构化事件报文
type Event struct {
	// Name - 事件名（X-Inis-Event 头；与 Records 内 eventName 一致）
	Name string
	// Delivery - 投递号（X-Inis-Delivery 头；同一逻辑投递在全部重试间稳定，幂等键）
	Delivery string
	// Records - 事件记录列表
	Records []Record
}

// ============================= 事件模型 - 结束 =============================

// ============================= 验签与解析 - 开始 =============================

// VerifyEvent - 仅验签（constant-time 比对；secret 为空或签名缺失/不符一律 false）
/**
 * @param body []byte - 原始请求体（必须原文，不要先格式化）
 * @param signature string - X-Inis-Signature 头值（hex HMAC-SHA256）
 * @param secret string - 规则签名密钥（GET ?notification 回读可见）
 * @return bool - 验签通过与否
 */
func VerifyEvent(body []byte, signature, secret string) bool {

	if secret == "" || signature == "" {
		return false
	}
	// 服务端产出小写 hex；大小写归一后 constant-time 比对
	expected := sign(body, secret)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(signature)), []byte(expected)) == 1
}

// ParseEvent - 报文解析 + 验签（先验签后解析；验签失败一律 ErrInvalidSign，切勿继续处理）
/**
 * @param body []byte - 原始请求体（必须原文，不要先格式化）
 * @param header http.Header - 请求头（取 X-Inis-Event / X-Inis-Signature / X-Inis-Delivery）
 * @param secret string - 规则签名密钥
 * @return *Event - 结构化事件（含投递号幂等键）
 * @return error - ErrInvalidRequest（密钥为空）/ ErrInvalidSign（验签失败）/ 普通错误（载荷非法 JSON）
 * @example：
 * 	body, _ := io.ReadAll(request.Body)
 * 	event, err := notify.ParseEvent(body, request.Header, secret)
 * 	if err != nil { writer.WriteHeader(http.StatusBadRequest); return }
 * 	// 按 event.Delivery 幂等落库后应答 2xx
 */
func ParseEvent(body []byte, header http.Header, secret string) (*Event, error) {

	if secret == "" {
		return nil, fmt.Errorf("%w：验签密钥不能为空", types.ErrInvalidRequest)
	}

	signature := ""
	if header != nil {
		signature = header.Get("X-Inis-Signature")
	}
	if signature == "" {
		return nil, fmt.Errorf("%w：缺少 X-Inis-Signature 头", types.ErrInvalidSign)
	}
	if !VerifyEvent(body, signature, secret) {
		return nil, fmt.Errorf("%w：事件通知验签失败", types.ErrInvalidSign)
	}

	var payload struct {
		Records []Record `json:"Records"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("事件载荷解析失败（%v）", err)
	}

	event := &Event{Records: payload.Records}
	if header != nil {
		event.Name = header.Get("X-Inis-Event")
		event.Delivery = header.Get("X-Inis-Delivery")
	}
	return event, nil
}

// sign - 计算投递签名（hex HMAC-SHA256(body, secret)，与服务端 dispatcher 逐字节一致）
func sign(body []byte, secret string) string {

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ============================= 验签与解析 - 结束 =============================
