package s3

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"hash"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ============================= 对象操作 - 开始 =============================

// ObjectMeta - 对象元数据（Head/Get 响应头视角）
type ObjectMeta struct {
	// ETag - 实体标签（简单上传 = 内容 MD5 hex；分片 = md5(各片MD5拼接)-{N}）
	ETag string
	// Size - 字节数
	Size int64
	// Mime - Content-Type
	Mime string
	// LastModified - 最后修改时间
	LastModified time.Time
	// Meta - 用户元数据（x-amz-meta-*，键已去除前缀）
	Meta map[string]string
	// CacheControl - Cache-Control（写入时设置，GET/HEAD 原样透传）
	CacheControl string
	// VersionId - 版本号（无版本控制为 "null"）
	VersionId string
}

// ObjectEntry - 列表条目
type ObjectEntry struct {
	// Key - 对象键
	Key string
	// Size - 字节数
	Size int64
	// ETag - 实体标签
	ETag string
	// Mime - Content-Type（列表不返回，恒空；取元数据用 HeadObject）
	Mime string
	// StorageClass - 存储级别
	StorageClass string
	// LastModified - 最后修改时间
	LastModified time.Time
}

// ListInput - 列举输入（ListObjectsV2 同语义）
type ListInput struct {
	// Bucket - 桶名
	Bucket string
	// Prefix - 前缀过滤
	Prefix string
	// Delimiter - 折叠符（"/" 时目录级浏览，公共前缀进 Prefixes）
	Delimiter string
	// Token - 翻页令牌（上一页的 NextToken）
	Token string
	// MaxKeys - 单页上限（0 = 服务端默认，封顶 1000）
	MaxKeys int
}

// ListResult - 列举结果
type ListResult struct {
	// Entries - 对象条目
	Entries []ObjectEntry
	// Prefixes - 公共前缀（目录折叠）
	Prefixes []string
	// IsTruncated - 是否还有更多
	IsTruncated bool
	// NextToken - 下一页令牌（IsTruncated=true 时有效）
	NextToken string
}

// PutInput - 上传输入（单请求流式）
type PutInput struct {
	// Bucket - 桶名
	Bucket string
	// Key - 对象键
	Key string
	// Body - 内容流（不整读内存，调用方负责关闭）
	Body io.Reader
	// Mime - Content-Type（空 = 网关嗅探/默认）
	Mime string
	// Meta - 用户元数据（序列化为 x-amz-meta-* 头，键统一小写；如 {"name": "摸鱼"} 写对象备注名）
	Meta map[string]string
	// CacheControl - Cache-Control 头（空 = 不携带；静态资源长缓存依赖它）
	CacheControl string
	// VerifyETag - 流式计算 MD5 并与响应 ETag 回核（默认开启；调用方置 false 关闭）
	VerifyETag bool
}

// PutResult - 上传结果
type PutResult struct {
	// ETag - 实体标签
	ETag string
	// Size - 实际写入字节数
	Size int64
	// VersionId - 版本号（无版本控制为 "null"）
	VersionId string
}

// writeHeaders - 写入类请求头装配（Content-Type + 用户元数据 + Cache-Control）
// 用户元数据键统一小写并按字典序装配，保证请求头集合确定性。
func writeHeaders(mime string, meta map[string]string, cacheControl string) map[string]string {

	headers := map[string]string{}
	if mime != "" {
		headers["Content-Type"] = mime
	}
	for _, pair := range userMetaHeaders(meta) {
		headers[pair[0]] = pair[1]
	}
	if cacheControl != "" {
		headers["Cache-Control"] = cacheControl
	}
	return headers
}

// userMetaHeaders - 用户元数据头键值对（前缀 x-amz-meta-，键去空白并统一小写后按字典序返回）
func userMetaHeaders(meta map[string]string) [][2]string {

	rawKeys := make([]string, 0, len(meta))
	for key := range meta {
		rawKeys = append(rawKeys, key)
	}
	// 先对原始键排序再归一，大小写重复键的覆盖顺序也是确定的
	sort.Strings(rawKeys)

	normalized := make(map[string]string, len(meta))
	for _, key := range rawKeys {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "" {
			continue
		}
		normalized[lower] = meta[key]
	}

	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	pairs := make([][2]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, [2]string{"x-amz-meta-" + key, normalized[key]})
	}
	return pairs
}

// PutObject - 简单上传（PUT /{bucket}/{key}，流式直通）
func (this *Client) PutObject(ctx context.Context, in PutInput) (*PutResult, error) {

	headers := writeHeaders(in.Mime, in.Meta, in.CacheControl)

	body := in.Body
	var tracker *md5Tracker
	if in.VerifyETag {
		tracker = newMD5Tracker()
		body = io.TeeReader(in.Body, tracker)
	}

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPut,
		bucket:  in.Bucket,
		key:     in.Key,
		headers: headers,
		body:    body,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	// 排空响应体（连接复用；PutObject 响应体为空或极小）
	_, _ = io.Copy(io.Discard, response.Body)

	etag := trimETag(response.Header.Get("ETag"))
	result := &PutResult{
		ETag:      etag,
		VersionId: response.Header.Get("x-amz-version-id"),
	}
	if result.VersionId == "" {
		result.VersionId = "null"
	}

	if tracker != nil {
		result.Size = tracker.size
		sum := hex.EncodeToString(tracker.hash.Sum(nil))
		if etag != "" && !strings.Contains(etag, "-") && !strings.EqualFold(etag, sum) {
			return nil, fmt.Errorf("%w：ETag 校验不一致（本地 %s / 网关 %s）", ErrTransport, sum, etag)
		}
	}

	return result, nil
}

// GetOptions - 下载选项
type GetOptions struct {
	// Range - 字节区间（形如 "bytes=0-1023"；空 = 全量）
	Range string
}

// GetResult - 下载结果（Body 由调用方关闭）
type GetResult struct {
	// Body - 内容流
	Body io.ReadCloser
	// Meta - 对象元数据
	Meta ObjectMeta
}

// GetObject - 下载（GET /{bucket}/{key}，流式回写，支持 Range）
func (this *Client) GetObject(ctx context.Context, bucket, key string, options *GetOptions) (*GetResult, error) {

	headers := map[string]string{}
	if options != nil && options.Range != "" {
		headers["Range"] = options.Range
	}

	response, err := this.do(ctx, requestInput{
		method:  http.MethodGet,
		bucket:  bucket,
		key:     key,
		headers: headers,
	})
	if err != nil {
		return nil, err
	}

	return &GetResult{Body: response.Body, Meta: headerMeta(response.Header)}, nil
}

// HeadObject - 取对象元数据（HEAD /{bucket}/{key}）
func (this *Client) HeadObject(ctx context.Context, bucket, key string) (*ObjectMeta, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodHead, bucket: bucket, key: key})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)

	meta := headerMeta(response.Header)
	return &meta, nil
}

// DeleteObject - 删除对象（DELETE /{bucket}/{key}；软删进回收站）
func (this *Client) DeleteObject(ctx context.Context, bucket, key string) error {

	response, err := this.do(ctx, requestInput{method: http.MethodDelete, bucket: bucket, key: key})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// ListObjects - 浏览对象（GET /{bucket}?list-type=2，ListObjectsV2 同语义）
func (this *Client) ListObjects(ctx context.Context, in ListInput) (*ListResult, error) {

	query := map[string]string{"list-type": "2"}
	if in.Prefix != "" {
		query["prefix"] = in.Prefix
	}
	if in.Delimiter != "" {
		query["delimiter"] = in.Delimiter
	}
	if in.Token != "" {
		query["continuation-token"] = in.Token
	}
	if in.MaxKeys > 0 {
		query["max-keys"] = strconv.Itoa(in.MaxKeys)
	}

	response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: in.Bucket, query: query})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlListBucketResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：ListObjectsV2 响应解析失败（%v）", ErrTransport, err)
	}

	result := &ListResult{IsTruncated: raw.IsTruncated, NextToken: raw.NextContinuationToken}
	for _, item := range raw.Contents {
		result.Entries = append(result.Entries, ObjectEntry{
			Key:          item.Key,
			Size:         item.Size,
			ETag:         trimETag(item.ETag),
			StorageClass: item.StorageClass,
			LastModified: item.LastModified.Time,
		})
	}
	for _, item := range raw.CommonPrefixes {
		result.Prefixes = append(result.Prefixes, item.Prefix)
	}

	return result, nil
}

// headerMeta - 从响应头提取对象元数据
func headerMeta(header http.Header) ObjectMeta {

	meta := ObjectMeta{
		ETag:         trimETag(header.Get("ETag")),
		Size:         headerSize(header.Get("Content-Length")),
		Mime:         header.Get("Content-Type"),
		CacheControl: header.Get("Cache-Control"),
		VersionId:    header.Get("x-amz-version-id"),
		Meta:         map[string]string{},
	}
	if meta.VersionId == "" {
		meta.VersionId = "null"
	}
	if modified := header.Get("Last-Modified"); modified != "" {
		if parsed, err := http.ParseTime(modified); err == nil {
			meta.LastModified = parsed
		}
	}
	for key := range header {
		if lower := strings.ToLower(key); strings.HasPrefix(lower, "x-amz-meta-") {
			meta.Meta[lower[len("x-amz-meta-"):]] = header.Get(key)
		}
	}

	return meta
}

// headerSize - Content-Length 解析（失败为 0）
func headerSize(raw string) int64 {

	size, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	return size
}

// md5Tracker - 流式 MD5 摘要器（TeeReader 搭档；统计写入字节数）
type md5Tracker struct {
	// hash - MD5 哈希
	hash hash.Hash
	// size - 已写入字节数
	size int64
}

// newMD5Tracker - 创建 MD5 摘要器
func newMD5Tracker() *md5Tracker {

	return &md5Tracker{hash: md5.New()}
}

// Write - 写入并计数
func (this *md5Tracker) Write(data []byte) (int, error) {

	n, err := this.hash.Write(data)
	this.size += int64(n)
	return n, err
}

// ============================= 对象操作 - 结束 =============================
