package s3

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
func (this *Client) PresignGet(ctx context.Context, bucket, key string, expires time.Duration) (string, error) {

	return this.PresignGetWith(ctx, bucket, key, expires, nil)
}

// PresignGetWith - 预签名下载链接（GET，叠加额外查询参数并一并签入，本地自签零往返）
// 典型用法：把网关图片处理串签入（extraQuery{"x-inis-process": "image/resize,w_32,q_40"}），
// 私有桶的 blur-up 双 URL（模糊小图 + 原图）必须走本方法，事后拼参数会破坏签名。
/**
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param expires time.Duration - 有效期（<=0 默认 1 小时，硬上限 7 天）
 * @param extraQuery map[string]string - 额外查询参数（参与签名 canonical query；空 = 与 PresignGet 等价）
 */
func (this *Client) PresignGetWith(_ context.Context, bucket, key string, expires time.Duration, extraQuery map[string]string) (string, error) {

	return this.presign(http.MethodGet, bucket, key, extraQuery, expires)
}

// ImageProcessURL - 图片实时处理链接拼装（纯本地，零网络往返）：
// s3Endpoint/{bucket}/{key}?x-inis-process=<process>，适用 public-read 公开桶的 blur-up 渐进加载；
// 私有桶请改用 PresignGetWith 把处理串签入。
/**
 * @param bucket string - 桶名
 * @param key string - 对象键
 * @param process string - 处理串（image/resize,w_32,q_40 形态，网关参数规范见 08 文档 §3）
 */
func (this *Client) ImageProcessURL(bucket, key, process string) string {

	return this.endpoint + escapePath(bucket, key) + "?x-inis-process=" + url.QueryEscape(process)
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
