package s3

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ============================= 分片上传 - 开始 =============================

const (
	// minPartSize - 最小分片（S3 约束：单片 5MB 起，末片除外）
	minPartSize = 5 << 20
	// maxPartCount - 最大分片数（S3 约束：1–10000）
	maxPartCount = 10000
)

// UploadInput - 高级上传输入（按阈值自动单 PUT / 分片编排）
type UploadInput struct {
	// Bucket - 桶名
	Bucket string
	// Key - 对象键
	Key string
	// Body - 内容流（调用方负责关闭；全程流式，常驻内存 ≤ PartSize × Concurrency）
	Body io.Reader
	// Size - 总字节数（必填；≤ PartSize 走单 PUT）
	Size int64
	// Mime - Content-Type（空 = 网关嗅探/默认）
	Mime string
	// Meta - 用户元数据（x-amz-meta-*，分片会话初始化时写入；单 PUT 路径同样透传）
	Meta map[string]string
	// CacheControl - Cache-Control（分片会话初始化时写入；单 PUT 路径同样透传）
	CacheControl string
	// PartSize - 分片大小（0 = 8MB；自动上调保证片数 ≤10000）
	PartSize int64
	// Concurrency - 分片并发数（0 = 4）
	Concurrency int
}

// Part - 已传分片记录
type Part struct {
	// PartNumber - 分片序号（1–10000）
	PartNumber int
	// ETag - 分片 ETag（该分片 MD5 hex）
	ETag string
	// Size - 分片字节数
	Size int64
}

// CreateInput - 分片会话初始化输入（CreateMultipartUploadEx）
type CreateInput struct {
	// Bucket - 桶名
	Bucket string
	// Key - 对象键
	Key string
	// Mime - Content-Type（空 = 网关缺省）
	Mime string
	// Meta - 用户元数据（序列化为 x-amz-meta-* 头，键统一小写；如 {"name": "摸鱼"} 写对象备注名）
	Meta map[string]string
	// CacheControl - Cache-Control 头（空 = 不携带）
	CacheControl string
}

// CreateMultipartUploadEx - 初始化分片会话（POST /{bucket}/{key}?uploads；支持用户元数据与 Cache-Control）
func (this *Client) CreateMultipartUploadEx(ctx context.Context, in CreateInput) (string, error) {

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPost,
		bucket:  in.Bucket,
		key:     in.Key,
		query:   map[string]string{"uploads": ""},
		headers: writeHeaders(in.Mime, in.Meta, in.CacheControl),
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	var result xmlInitiateMultipartUploadResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("%w：CreateMultipartUpload 响应解析失败（%v）", ErrTransport, err)
	}
	if result.UploadId == "" {
		return "", fmt.Errorf("%w：CreateMultipartUpload 响应缺少 uploadId", ErrTransport)
	}

	return result.UploadId, nil
}

// CreateMultipartUpload - 初始化分片会话（不含元数据的薄封装；完整能力用 CreateMultipartUploadEx）
func (this *Client) CreateMultipartUpload(ctx context.Context, bucket, key, mime string) (string, error) {

	return this.CreateMultipartUploadEx(ctx, CreateInput{Bucket: bucket, Key: key, Mime: mime})
}

// UploadPart - 上传分片（PUT /{bucket}/{key}?partNumber=N&uploadId=U，流式 + MD5 回核）
func (this *Client) UploadPart(ctx context.Context, bucket, key, uploadId string, partNumber int, body io.Reader) (*Part, error) {

	tracker := newMD5Tracker()
	response, err := this.do(ctx, requestInput{
		method: http.MethodPut,
		bucket: bucket,
		key:    key,
		query: map[string]string{
			"partNumber": strconv.Itoa(partNumber),
			"uploadId":   uploadId,
		},
		body: io.TeeReader(body, tracker),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)

	etag := trimETag(response.Header.Get("ETag"))
	sum := hex.EncodeToString(tracker.hash.Sum(nil))
	if etag != "" && !strings.EqualFold(etag, sum) {
		return nil, fmt.Errorf("%w：分片 %d ETag 校验不一致（本地 %s / 网关 %s）", ErrTransport, partNumber, sum, etag)
	}
	if etag == "" {
		etag = sum
	}

	return &Part{PartNumber: partNumber, ETag: etag, Size: tracker.size}, nil
}

// ListParts - 查询已传分片（GET /{bucket}/{key}?uploadId=U；自动翻页取全量）
func (this *Client) ListParts(ctx context.Context, bucket, key, uploadId string) ([]Part, error) {

	parts := make([]Part, 0)
	marker := 0
	for {
		query := map[string]string{"uploadId": uploadId}
		if marker > 0 {
			query["part-number-marker"] = strconv.Itoa(marker)
		}

		response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, key: key, query: query})
		if err != nil {
			return nil, err
		}

		var result xmlListPartsResult
		decodeErr := xml.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&result)
		_ = response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("%w：ListParts 响应解析失败（%v）", ErrTransport, decodeErr)
		}

		for _, item := range result.Parts {
			parts = append(parts, Part{PartNumber: item.PartNumber, ETag: trimETag(item.ETag), Size: item.Size})
			marker = item.PartNumber
		}
		if !result.IsTruncated {
			break
		}
	}

	return parts, nil
}

// CompleteMultipartUpload - 完成分片上传（POST /{bucket}/{key}?uploadId=U，清单升序携带）
func (this *Client) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadId string, parts []Part) (*PutResult, error) {

	if len(parts) == 0 {
		return nil, fmt.Errorf("%w：分片清单不能为空", ErrInvalidRequest)
	}

	ordered := make([]xmlCompletePart, 0, len(parts))
	for _, part := range parts {
		ordered = append(ordered, xmlCompletePart{PartNumber: part.PartNumber, ETag: quoteETag(part.ETag)})
	}

	payload, err := xml.Marshal(xmlCompleteMultipartUpload{Parts: ordered})
	if err != nil {
		return nil, fmt.Errorf("%w：Complete 清单序列化失败（%v）", ErrInvalidRequest, err)
	}

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPost,
		bucket:  bucket,
		key:     key,
		query:   map[string]string{"uploadId": uploadId},
		headers: map[string]string{"Content-Type": "application/xml"},
		body:    bytes.NewReader(payload),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var result xmlCompleteMultipartUploadResult
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("%w：CompleteMultipartUpload 响应解析失败（%v）", ErrTransport, err)
	}

	var total int64
	for _, part := range parts {
		total += part.Size
	}

	return &PutResult{
		ETag:      trimETag(result.ETag),
		Size:      total,
		VersionId: response.Header.Get("x-amz-version-id"),
	}, nil
}

// AbortMultipartUpload - 中止分片上传（DELETE /{bucket}/{key}?uploadId=U；幂等）
func (this *Client) AbortMultipartUpload(ctx context.Context, bucket, key, uploadId string) error {

	response, err := this.do(ctx, requestInput{
		method: http.MethodDelete,
		bucket: bucket,
		key:    key,
		query:  map[string]string{"uploadId": uploadId},
	})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// Upload - 高级上传：Size ≤ PartSize 走单 PUT，否则分片编排（并发读片上传，失败自动 Abort）
func (this *Client) Upload(ctx context.Context, in UploadInput) (*PutResult, error) {

	if err := checkUploadInput(&in); err != nil {
		return nil, err
	}

	partSize := normPartSize(in.Size, in.PartSize)

	if in.Size <= partSize {
		return this.PutObject(ctx, PutInput{
			Bucket: in.Bucket, Key: in.Key, Body: in.Body, Mime: in.Mime,
			Meta: in.Meta, CacheControl: in.CacheControl, VerifyETag: true,
		})
	}

	concurrency := in.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	uploadId, err := this.CreateMultipartUploadEx(ctx, CreateInput{
		Bucket: in.Bucket, Key: in.Key, Mime: in.Mime,
		Meta: in.Meta, CacheControl: in.CacheControl,
	})
	if err != nil {
		return nil, err
	}

	parts, err := this.uploadParts(ctx, in, uploadId, partSize, concurrency)
	if err != nil {
		// 失败自动中止（幂等），不留碎片
		_ = this.AbortMultipartUpload(ctx, in.Bucket, in.Key, uploadId)
		return nil, err
	}

	return this.CompleteMultipartUpload(ctx, in.Bucket, in.Key, uploadId, parts)
}

// checkUploadInput - 高级上传输入校验（Upload / ResumeUpload 共用）
func checkUploadInput(in *UploadInput) error {

	if in.Bucket == "" || in.Key == "" {
		return fmt.Errorf("%w：bucket 与 key 不能为空", ErrInvalidRequest)
	}
	if in.Body == nil {
		return fmt.Errorf("%w：上传内容流不能为空", ErrInvalidRequest)
	}
	if in.Size < 0 {
		return fmt.Errorf("%w：size 不能为负", ErrInvalidRequest)
	}
	return nil
}

// normPartSize - 分片大小归一（默认 8MB、下限 5MB、自动上调保证片数 ≤10000）
func normPartSize(size, partSize int64) int64 {

	if partSize <= 0 {
		partSize = 8 << 20 // 默认 8MB
	}
	if partSize < minPartSize {
		partSize = minPartSize
	}
	// 自动上调分片大小，保证片数不超上限
	for (size+partSize-1)/partSize > maxPartCount {
		partSize *= 2
	}
	return partSize
}

// uploadParts - 顺序读片 + 有界并发上传（常驻内存 ≤ partSize × concurrency）
func (this *Client) uploadParts(ctx context.Context, in UploadInput, uploadId string, partSize int64, concurrency int) ([]Part, error) {

	type job struct {
		// number - 分片序号
		number int
		// data - 分片内容（末片可能小于 partSize）
		data []byte
	}
	type outcome struct {
		// part - 成功分片
		part *Part
		// err - 失败
		err error
	}

	jobs := make(chan job, concurrency)
	results := make(chan outcome, concurrency)

	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				part, err := this.UploadPart(ctx, in.Bucket, in.Key, uploadId, item.number, bytes.NewReader(item.data))
				results <- outcome{part: part, err: err}
			}
		}()
	}

	go func() {
		workers.Wait()
		close(results)
	}()

	// 顺序读片投递（上游读失败时停止投递并收尾）
	var readErr error
	number := 0
	go func() {
		defer close(jobs)
		for {
			buffer := make([]byte, partSize)
			n, err := io.ReadFull(in.Body, buffer)
			if n > 0 {
				number++
				jobs <- job{number: number, data: buffer[:n]}
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			if err != nil {
				readErr = err
				break
			}
		}
	}()

	parts := make([]Part, 0, number)
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if result.part != nil {
			parts = append(parts, *result.part)
		}
	}

	if readErr != nil {
		return nil, fmt.Errorf("%w：读取上传流失败（%v）", ErrInvalidRequest, readErr)
	}
	if firstErr != nil {
		return nil, firstErr
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w：分片上传未产生任何分片", ErrTransport)
	}

	// 并发回收乱序，Complete 前按序号升序
	sortParts(parts)
	return parts, nil
}

// sortParts - 分片按序号升序（插入排序，分片数通常很小）
func sortParts(parts []Part) {

	for i := 1; i < len(parts); i++ {
		for j := i; j > 0 && parts[j].PartNumber < parts[j-1].PartNumber; j-- {
			parts[j], parts[j-1] = parts[j-1], parts[j]
		}
	}
}

// ============================= 分片上传 - 结束 =============================

// ============================= 断点续传 - 开始 =============================

// MultipartUpload - 进行中的分片会话（ListMultipartUploads 条目）
type MultipartUpload struct {
	// Key - 对象键
	Key string
	// UploadId - 分片会话 ID
	UploadId string
	// Initiated - 会话初始化时间
	Initiated time.Time
}

// ListMultipartUploads - 列出进行中的分片会话（GET /{bucket}?uploads，自动翻页取全量）
func (this *Client) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]MultipartUpload, error) {

	if bucket == "" {
		return nil, fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	uploads := make([]MultipartUpload, 0)
	keyMarker, uploadIdMarker := "", ""
	for {
		query := map[string]string{"uploads": ""}
		if prefix != "" {
			query["prefix"] = prefix
		}
		if keyMarker != "" {
			query["key-marker"] = keyMarker
		}
		if uploadIdMarker != "" {
			query["upload-id-marker"] = uploadIdMarker
		}

		response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, query: query})
		if err != nil {
			return nil, err
		}

		var result xmlListMultipartUploadsResult
		decodeErr := xml.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&result)
		_ = response.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("%w：ListMultipartUploads 响应解析失败（%v）", ErrTransport, decodeErr)
		}

		for _, item := range result.Uploads {
			uploads = append(uploads, MultipartUpload{Key: item.Key, UploadId: item.UploadId, Initiated: item.Initiated.Time})
		}
		if !result.IsTruncated || result.NextKeyMarker == "" {
			break
		}
		keyMarker, uploadIdMarker = result.NextKeyMarker, result.NextUploadIdMarker
	}

	return uploads, nil
}

// ResumeUpload - 断点续传：发现同桶同键的进行中分片会话时复用该会话，已传分片本地跳过、只补传缺失分片；
// 无进行中会话时等价 Upload（新建会话全量上传）。
//
// 调用方必须从头提供完整内容流；已上传分片的字节区间由 SDK 在本地跳过
// （Body 实现 io.ReadSeeker 时直接 Seek，否则读丢弃）。已传分片信任网关记录（大小对齐布局即采纳，
// 不重新校验内容），如需强一致请先 AbortMultipartUpload 后重新全量上传。
// 复用已有会话时，Meta / CacheControl 沿用会话创建时的取值（S3 语义：元数据在初始化时确定）。
func (this *Client) ResumeUpload(ctx context.Context, in UploadInput) (*PutResult, error) {

	if err := checkUploadInput(&in); err != nil {
		return nil, err
	}

	partSize := normPartSize(in.Size, in.PartSize)

	if in.Size <= partSize {
		return this.PutObject(ctx, PutInput{
			Bucket: in.Bucket, Key: in.Key, Body: in.Body, Mime: in.Mime,
			Meta: in.Meta, CacheControl: in.CacheControl, VerifyETag: true,
		})
	}

	// 发现进行中会话（同桶同键取最新 Initiated；前缀过滤 + 本地精确匹配防兄弟键误伤）
	uploads, err := this.ListMultipartUploads(ctx, in.Bucket, in.Key)
	if err != nil {
		return nil, err
	}
	var current *MultipartUpload
	for i := range uploads {
		if uploads[i].Key != in.Key {
			continue
		}
		if current == nil || uploads[i].Initiated.After(current.Initiated) {
			current = &uploads[i]
		}
	}

	concurrency := in.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	// 无进行中会话：退化为全量分片上传
	if current == nil {
		uploadId, createErr := this.CreateMultipartUploadEx(ctx, CreateInput{
			Bucket: in.Bucket, Key: in.Key, Mime: in.Mime,
			Meta: in.Meta, CacheControl: in.CacheControl,
		})
		if createErr != nil {
			return nil, createErr
		}
		parts, uploadErr := this.uploadParts(ctx, in, uploadId, partSize, concurrency)
		if uploadErr != nil {
			_ = this.AbortMultipartUpload(ctx, in.Bucket, in.Key, uploadId)
			return nil, uploadErr
		}
		return this.CompleteMultipartUpload(ctx, in.Bucket, in.Key, uploadId, parts)
	}

	// 有进行中会话：取已传分片并校验布局与本地上传参数一致
	uploaded, err := this.ListParts(ctx, in.Bucket, in.Key, current.UploadId)
	if err != nil {
		return nil, err
	}
	count := int((in.Size + partSize - 1) / partSize)
	existing, err := checkResumeLayout(uploaded, in.Size, partSize, count)
	if err != nil {
		return nil, fmt.Errorf("%w：进行中会话与本次上传布局不一致（%v），请先中止该会话后重新上传", ErrInvalidRequest, err)
	}

	parts, err := this.resumeParts(ctx, in, current.UploadId, partSize, concurrency, count, existing)
	if err != nil {
		// 续传失败不自动中止（保留现场供下次续传），碎片由桶生命周期或调用方治理
		return nil, err
	}

	return this.CompleteMultipartUpload(ctx, in.Bucket, in.Key, current.UploadId, parts)
}

// checkResumeLayout - 校验已传分片与本地布局一致（序号在界内、非末片大小等于 partSize、末片大小等于余量）
// 通过则以「序号 → 分片」返回可复用清单
func checkResumeLayout(uploaded []Part, size, partSize int64, count int) (map[int]Part, error) {

	existing := make(map[int]Part, len(uploaded))
	for _, part := range uploaded {
		if part.PartNumber < 1 || part.PartNumber > count {
			return nil, fmt.Errorf("分片序号 %d 超出布局范围（1-%d）", part.PartNumber, count)
		}
		expect := partSize
		if part.PartNumber == count {
			expect = size - int64(count-1)*partSize
		}
		if part.Size != expect {
			return nil, fmt.Errorf("分片 %d 大小 %d 与布局期望 %d 不一致", part.PartNumber, part.Size, expect)
		}
		if _, dup := existing[part.PartNumber]; dup {
			return nil, fmt.Errorf("分片序号 %d 重复", part.PartNumber)
		}
		existing[part.PartNumber] = part
	}
	return existing, nil
}

// resumeParts - 续传编排：顺序走片，已传分片本地跳过，缺失分片有界并发补传
func (this *Client) resumeParts(ctx context.Context, in UploadInput, uploadId string, partSize int64, concurrency, count int, existing map[int]Part) ([]Part, error) {

	type job struct {
		// number - 分片序号
		number int
		// data - 分片内容（末片可能小于 partSize）
		data []byte
	}
	type outcome struct {
		// part - 成功分片
		part *Part
		// err - 失败
		err error
	}

	jobs := make(chan job, concurrency)
	results := make(chan outcome, concurrency)

	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range jobs {
				part, err := this.UploadPart(ctx, in.Bucket, in.Key, uploadId, item.number, bytes.NewReader(item.data))
				results <- outcome{part: part, err: err}
			}
		}()
	}

	go func() {
		workers.Wait()
		close(results)
	}()

	// 顺序走片投递（已传跳过、缺失补传；上游读失败时停止投递并收尾）
	var readErr error
	go func() {
		defer close(jobs)
		for number := 1; number <= count; number++ {
			expect := partSize
			if number == count {
				expect = in.Size - int64(count-1)*partSize
			}
			if _, ok := existing[number]; ok {
				// 已传分片：本地跳过对应字节区间（Seek 优先，否则读丢弃）
				if err := skipBody(in.Body, expect); err != nil {
					readErr = err
					return
				}
				continue
			}
			buffer := make([]byte, expect)
			if _, err := io.ReadFull(in.Body, buffer); err != nil {
				readErr = err
				return
			}
			jobs <- job{number: number, data: buffer}
		}
	}()

	parts := make([]Part, 0, count)
	for _, part := range existing {
		parts = append(parts, part)
	}
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if result.part != nil {
			parts = append(parts, *result.part)
		}
	}

	if readErr != nil {
		return nil, fmt.Errorf("%w：读取上传流失败（%v）", ErrInvalidRequest, readErr)
	}
	if firstErr != nil {
		return nil, firstErr
	}

	// 并发回收乱序，Complete 前按序号升序
	sortParts(parts)
	return parts, nil
}

// skipBody - 跳过内容流 n 字节（io.ReadSeeker 直接 Seek，否则读丢弃）
func skipBody(body io.Reader, n int64) error {

	if seeker, ok := body.(io.ReadSeeker); ok {
		_, err := seeker.Seek(n, io.SeekCurrent)
		return err
	}
	_, err := io.CopyN(io.Discard, body, n)
	return err
}

// ============================= 断点续传 - 结束 =============================
