package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
)

// ============================= 批量删除 - 开始 =============================

const (
	// maxDeleteKeys - DeleteObjects 单请求键数上限（S3 语义 1000；超出由 SDK 自动分批）
	maxDeleteKeys = 1000
)

// DeleteObjectsError - 批量删除单键失败条目（不阻断批次，逐键回传）
type DeleteObjectsError struct {
	// Key - 失败对象键
	Key string
	// Code - 网关错误码（如 InvalidArgument / InternalError）
	Code string
	// Message - 错误描述
	Message string
}

// DeleteObjectsResult - 批量删除结果（Deleted 与 Errors 互补：逐键非成即败）
type DeleteObjectsResult struct {
	// Deleted - 删除成功的键（含本就不存在的键，幂等语义）
	Deleted []string
	// Errors - 删除失败的键与原因
	Errors []DeleteObjectsError
}

// DeleteObjects - 批量删除（POST /{bucket}?delete；超过 1000 键自动分批，单键失败不阻断）
func (this *Client) DeleteObjects(ctx context.Context, bucket string, keys []string) (*DeleteObjectsResult, error) {

	if bucket == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w：删除键列表不能为空", ErrInvalidRequest)
	}
	for _, key := range keys {
		if key == "" {
			return nil, fmt.Errorf("%w：删除键列表存在空键", ErrInvalidRequest)
		}
	}

	result := &DeleteObjectsResult{Deleted: []string{}, Errors: []DeleteObjectsError{}}
	for start := 0; start < len(keys); start += maxDeleteKeys {
		end := start + maxDeleteKeys
		if end > len(keys) {
			end = len(keys)
		}
		batch, err := this.deleteObjectsBatch(ctx, bucket, keys[start:end])
		if err != nil {
			return nil, err
		}
		result.Deleted = append(result.Deleted, batch.Deleted...)
		result.Errors = append(result.Errors, batch.Errors...)
	}

	return result, nil
}

// deleteObjectsBatch - 单批删除（≤1000 键）
func (this *Client) deleteObjectsBatch(ctx context.Context, bucket string, keys []string) (*DeleteObjectsResult, error) {

	objects := make([]xmlDeleteObjectEntry, 0, len(keys))
	for _, key := range keys {
		objects = append(objects, xmlDeleteObjectEntry{Key: key})
	}

	payload, err := xml.Marshal(xmlDeleteRequest{Objects: objects})
	if err != nil {
		return nil, fmt.Errorf("%w：DeleteObjects 请求序列化失败（%v）", ErrInvalidRequest, err)
	}

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPost,
		bucket:  bucket,
		query:   map[string]string{"delete": ""},
		headers: map[string]string{"Content-Type": "application/xml"},
		body:    bytes.NewReader(payload),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlDeleteResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：DeleteObjects 响应解析失败（%v）", ErrTransport, err)
	}

	result := &DeleteObjectsResult{Deleted: []string{}, Errors: []DeleteObjectsError{}}
	for _, item := range raw.Deleted {
		result.Deleted = append(result.Deleted, item.Key)
	}
	for _, item := range raw.Errors {
		result.Errors = append(result.Errors, DeleteObjectsError{Key: item.Key, Code: item.Code, Message: item.Message})
	}

	return result, nil
}

// ============================= 批量删除 - 结束 =============================
