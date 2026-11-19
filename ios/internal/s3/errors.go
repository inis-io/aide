package s3

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/inis-io/aide/ios/types"
)

// ============================= 错误模型 - 开始 =============================

var (
	// ErrInvalidRequest - 请求参数无效（本地校验失败，未发出请求）
	// 与控制面共用 types 的同一哨兵实例：调用方 errors.Is 一次判定覆盖双平面。
	ErrInvalidRequest = types.ErrInvalidRequest
	// ErrTransport - 传输层调用失败（网络错误、连接断开、非 S3 语义的协议违例）
	ErrTransport = types.ErrTransport
)

// S3Error - S3 网关返回的 XML 错误体
type S3Error struct {
	// HTTPStatus - HTTP 状态码（如 404/403/409）
	HTTPStatus int
	// Code - S3 错误码（NoSuchKey/NoSuchBucket/AccessDenied/SignatureDoesNotMatch/QuotaExceeded 等）
	Code string
	// Message - 错误描述
	Message string
	// Resource - 相关资源（桶/键）
	Resource string
	// RequestId - 请求号（与平台访问日志一一对应，排障凭据）
	RequestId string
}

// Error - 实现 error 接口
func (this *S3Error) Error() string {

	if this == nil {
		return "ios: <nil> S3Error"
	}

	return fmt.Sprintf("ios: S3 错误（status=%d code=%s）：%s", this.HTTPStatus, this.Code, this.Message)
}

// IsCode - 判定错误是否为指定 S3 错误码（如 IsCode(err, "NoSuchKey")）
func IsCode(err error, code string) bool {

	var item *S3Error
	if errors.As(err, &item) && item != nil {
		return item.Code == code
	}
	return false
}

// IsNotFound - 判定错误是否为资源不存在（NoSuchKey / NoSuchBucket / 404）
func IsNotFound(err error) bool {

	var item *S3Error
	if errors.As(err, &item) && item != nil {
		return item.Code == "NoSuchKey" || item.Code == "NoSuchBucket" || item.HTTPStatus == http.StatusNotFound
	}
	return false
}

// xmlError - S3 XML 错误体
type xmlError struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestId string   `xml:"RequestId"`
}

// decodeError - 解析错误响应（非 XML 形态按状态码归一）
func decodeError(response *http.Response) error {

	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))

	var item xmlError
	if err := xml.Unmarshal(raw, &item); err == nil && item.Code != "" {
		return &S3Error{
			HTTPStatus: response.StatusCode,
			Code:       item.Code,
			Message:    item.Message,
			Resource:   item.Resource,
			RequestId:  item.RequestId,
		}
	}

	return &S3Error{
		HTTPStatus: response.StatusCode,
		Code:       http.StatusText(response.StatusCode),
		Message:    string(raw),
	}
}

// ============================= 错误模型 - 结束 =============================
