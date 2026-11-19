package s3

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ============================= 预签名 - 开始 =============================

const (
	// maxPresignExpires - 预签名有效期硬上限（7 天，与平台 SigV4 约束一致）
	maxPresignExpires = 7 * 24 * time.Hour
)

// PresignGet - 预签名下载链接（GET，本地自签零往返）
/**
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param expires time.Duration - 有效期（<=0 默认 1 小时，硬上限 7 天）
 */
func (this *Client) PresignGet(_ context.Context, bucket, key string, expires time.Duration) (string, error) {

	return this.presign(http.MethodGet, bucket, key, nil, expires)
}

// PresignPut - 预签名上传链接（PUT，本地自签零往返）
/**
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param mime string - Content-Type（空 = 不约束）
 * @param expires time.Duration - 有效期（<=0 默认 1 小时，硬上限 7 天）
 */
func (this *Client) PresignPut(_ context.Context, bucket, key, mime string, expires time.Duration) (string, error) {

	return this.presign(http.MethodPut, bucket, key, nil, expires)
}

// presign - 组装预签名 URL（SigV4 查询参数形态，UNSIGNED-PAYLOAD）
func (this *Client) presign(method, bucket, key string, extra map[string]string, expires time.Duration) (string, error) {

	if bucket == "" {
		return "", fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}
	if expires <= 0 {
		expires = time.Hour
	}
	if expires > maxPresignExpires {
		expires = maxPresignExpires
	}

	query := map[string]string{}
	for item, value := range extra {
		query[item] = value
	}

	signed := this.creds.presign(signInput{
		method:  method,
		host:    this.host,
		path:    escapePath(bucket, key),
		query:   query,
		now:     this.now(),
		region:  this.region,
		expires: int64(expires / time.Second),
	})

	return this.endpoint + escapePath(bucket, key) + "?" + encodeQuery(signed), nil
}

// ============================= 预签名 - 结束 =============================
