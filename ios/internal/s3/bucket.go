package s3

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"time"
)

// ============================= 桶操作 - 开始 =============================

// BucketRow - S3 视角桶行
type BucketRow struct {
	// Name - 桶名
	Name string
	// CreateAt - 创建时间
	CreateAt time.Time
}

// CreateBucket - 创建桶（PUT /{bucket}）
func (this *Client) CreateBucket(ctx context.Context, bucket string) error {

	response, err := this.do(ctx, requestInput{method: http.MethodPut, bucket: bucket})
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// DeleteBucket - 删除桶（DELETE /{bucket}；桶必须为空）
func (this *Client) DeleteBucket(ctx context.Context, bucket string) error {

	response, err := this.do(ctx, requestInput{method: http.MethodDelete, bucket: bucket})
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// HeadBucket - 探测桶存在与访问权（HEAD /{bucket}）
func (this *Client) HeadBucket(ctx context.Context, bucket string) error {

	response, err := this.do(ctx, requestInput{method: http.MethodHead, bucket: bucket})
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// ListBuckets - 列出当前 AK 用户的桶（GET /）
func (this *Client) ListBuckets(ctx context.Context) ([]BucketRow, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var result xmlListAllMyBucketsResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 10<<20)).Decode(&result); err != nil {
		return nil, err
	}

	rows := make([]BucketRow, 0, len(result.Buckets))
	for _, item := range result.Buckets {
		rows = append(rows, BucketRow{Name: item.Name, CreateAt: item.CreationDate.Time})
	}

	return rows, nil
}

// ============================= 桶操作 - 结束 =============================
