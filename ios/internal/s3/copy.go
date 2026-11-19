package s3

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ============================= 对象拷贝 - 开始 =============================

// CopyResult - 拷贝结果
type CopyResult struct {
	// ETag - 目标对象实体标签
	ETag string
	// LastModified - 目标对象最后修改时间
	LastModified time.Time
	// VersionId - 目标对象版本号（无版本控制为 "null"）
	VersionId string
}

// CopyObject - 服务端拷贝（PUT /{bucket}/{key} + X-Amz-Copy-Source，零流量回传）
// 源与目标必须在同一 AK 的授权范围内（网关逐侧校验属主与 scope）
func (this *Client) CopyObject(ctx context.Context, bucket, key, sourceBucket, sourceKey string) (*CopyResult, error) {

	if bucket == "" || key == "" || sourceBucket == "" || sourceKey == "" {
		return nil, fmt.Errorf("%w：目标 bucket/key 与源 bucket/key 均不能为空", ErrInvalidRequest)
	}

	response, err := this.do(ctx, requestInput{
		method: http.MethodPut,
		bucket: bucket,
		key:    key,
		// 网关按原样解析源（不做 URL 解码），直接拼接「源桶/源键」原形态
		headers: map[string]string{"X-Amz-Copy-Source": sourceBucket + "/" + sourceKey},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlCopyObjectResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：CopyObject 响应解析失败（%v）", ErrTransport, err)
	}

	versionId := response.Header.Get("x-amz-version-id")
	if versionId == "" {
		versionId = "null"
	}

	return &CopyResult{
		ETag:         trimETag(raw.ETag),
		LastModified: raw.LastModified.Time,
		VersionId:    versionId,
	}, nil
}

// ============================= 对象拷贝 - 结束 =============================
