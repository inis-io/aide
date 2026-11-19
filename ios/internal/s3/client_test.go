package s3

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================= 内存版 S3 假网关 - 开始 =============================
//
// 与客户端跑完整 HTTP 往返：网关侧用同一套凭据重算 SigV4 并逐请求比对，
// 任何签名/编码不一致都会在网关侧直接 403（SignatureDoesNotMatch）。

// fakeModTime - 网关对象统一的最后修改时间（Last-Modified 解析断言锚点）
var fakeModTime = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// fakeObject - 网关存储的对象
type fakeObject struct {
	// data - 内容
	data []byte
	// mime - Content-Type
	mime string
}

// fakeUpload - 网关侧分片会话
type fakeUpload struct {
	// bucket - 桶名
	bucket string
	// key - 对象键
	key string
	// mime - 初始化时声明的 Content-Type
	mime string
	// parts - 已传分片（序号 → 内容）
	parts map[int][]byte
}

// fakeGateway - 内存版 S3 假网关（httptest 搭档）
type fakeGateway struct {
	// creds - 验签凭据（与客户端一致）
	creds credentials
	// mu - 状态锁（分片并发上传会并发进入）
	mu sync.Mutex
	// buckets - 桶表（名称 → 创建时间）
	buckets map[string]time.Time
	// objects - 对象表（桶 → 键 → 对象）
	objects map[string]map[string]fakeObject
	// uploads - 分片会话表（uploadId → 会话）
	uploads map[string]*fakeUpload
	// lastRange - 最近一次 GET 的 Range 头
	lastRange string
	// completeBody - 最近一次 Complete 的请求体
	completeBody []byte
	// abortIds - 已中止的 uploadId 序列
	abortIds []string
	// failPartNumber - 指定序号的分片上传返回 500（验证 Abort 路径；0 = 关闭）
	failPartNumber int
	// wrongETag - PutObject 返回错误 ETag（验证 ETag 回核；false = 关闭）
	wrongETag bool
	// multipartETag - PutObject 返回分片形态 ETag（验证跳过回核；false = 关闭）
	multipartETag bool
	// plainError - 错误响应返回非 XML 文本（验证错误归一；false = 关闭）
	plainError bool
	// corsConfigs - 桶 CORS 配置原文（PUT 存 / GET 取 / DELETE 清）
	corsConfigs map[string][]byte
	// lifecycleConfigs - 桶生命周期配置原文
	lifecycleConfigs map[string][]byte
	// versioningStatus - 桶版本控制状态（"" = 未开启）
	versioningStatus map[string]string
	// notificationRules - 桶事件通知规则（PUT 整组替换 / GET 回读含生成的 Id 与 Secret / 空集 = 清除）
	notificationRules map[string][]NotificationRule
	// stsDuration - 最近一次 GetSessionToken 的 DurationSeconds（断言锚点）
	stsDuration int64
	// sessionCreds - 会话凭据（非 nil 时验签额外接受该凭据 + 匹配令牌头）
	sessionCreds *credentials
	// partLog - 各分片会话的分片上传序号序列（续传跳过断言锚点）
	partLog map[string][]int
}

// newFakeGateway - 创建假网关（creds 与客户端使用同一套凭据）
func newFakeGateway() *fakeGateway {

	return &fakeGateway{
		creds:             credentials{accessKey: "AK", secret: "SK"},
		buckets:           map[string]time.Time{},
		objects:           map[string]map[string]fakeObject{},
		uploads:           map[string]*fakeUpload{},
		corsConfigs:       map[string][]byte{},
		lifecycleConfigs:  map[string][]byte{},
		versioningStatus:  map[string]string{},
		notificationRules: map[string][]NotificationRule{},
		partLog:           map[string][]int{},
	}
}

// newTestClient - 创建直连假网关的客户端（注册清理）
func newTestClient(t *testing.T, gateway *fakeGateway) *Client {

	t.Helper()
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)

	client, err := New(Config{
		Endpoint:  server.URL,
		Region:    "us-east-1",
		AccessKey: "AK",
		Secret:    "SK",
		UserAgent: "ios-test/0.1.0",
	})
	if err != nil {
		t.Fatalf("装配客户端失败：%v", err)
	}
	return client
}

// checkSign - 网关侧重算签名并与请求头逐字节比对（永久凭据优先，其次会话凭据）
func (this *fakeGateway) checkSign(request *http.Request) bool {

	if this.verifyWith(request, this.creds, "") {
		return true
	}
	if this.sessionCreds != nil {
		return this.verifyWith(request, *this.sessionCreds, this.sessionCreds.sessionToken)
	}
	return false
}

// verifyWith - 用指定凭据重算签名比对（令牌头必须匹配：永久凭据要求无令牌头）
func (this *fakeGateway) verifyWith(request *http.Request, creds credentials, token string) bool {

	authorization := request.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "AWS4-HMAC-SHA256 Credential="+creds.accessKey+"/") {
		return false
	}
	if request.Header.Get("X-Amz-Security-Token") != token {
		return false
	}
	now, err := time.Parse("20060102T150405Z", request.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}

	query := map[string]string{}
	for key, values := range request.URL.Query() {
		query[key] = values[0]
	}

	signed := creds.sign(signInput{
		method:      request.Method,
		host:        request.Host,
		path:        request.URL.EscapedPath(),
		query:       query,
		payloadHash: request.Header.Get("X-Amz-Content-Sha256"),
		now:         now,
		region:      "us-east-1",
	})
	return signed["Authorization"] == authorization
}

// splitPath - 请求路径拆分为桶与键（路径已解码）
func splitPath(path string) (string, string) {

	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", ""
	}
	if index := strings.Index(trimmed, "/"); index >= 0 {
		return trimmed[:index], trimmed[index+1:]
	}
	return trimmed, ""
}

// md5hex - MD5 十六进制
func md5hex(data []byte) string {

	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

// writeError - 写出 S3 错误响应（plainError 时退化为非 XML 文本）
func (this *fakeGateway) writeError(response http.ResponseWriter, status int, code, message, resource string) {

	if this.plainError {
		response.WriteHeader(status)
		_, _ = response.Write([]byte("gateway unavailable"))
		return
	}

	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response,
		`<Error><Code>%s</Code><Message>%s</Message><Resource>%s</Resource><RequestId>req-test-0001</RequestId></Error>`,
		code, message, resource)
}

// bucketExists - 桶存在性
func (this *fakeGateway) bucketExists(bucket string) bool {

	_, ok := this.buckets[bucket]
	return ok
}

// ServeHTTP - 路由分发（实现 http.Handler）
func (this *fakeGateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if !this.checkSign(request) {
		this.writeError(response, http.StatusForbidden, "SignatureDoesNotMatch", "签名不匹配", request.URL.Path)
		return
	}
	if this.plainError {
		this.writeError(response, http.StatusServiceUnavailable, "ServiceUnavailable", "服务不可用", request.URL.Path)
		return
	}

	bucket, key := splitPath(request.URL.Path)
	query := request.URL.Query()
	uploadId := query.Get("uploadId")
	_, hasUploads := query["uploads"]
	_, hasDelete := query["delete"]
	_, hasCors := query["cors"]
	_, hasVersioning := query["versioning"]
	_, hasLifecycle := query["lifecycle"]
	_, hasNotification := query["notification"]
	copySource := request.Header.Get("X-Amz-Copy-Source")

	switch {
	case request.Method == http.MethodPost && bucket == "":
		this.getSessionToken(response, request)
	case request.Method == http.MethodPost && hasUploads:
		this.initiate(response, request, bucket, key)
	case request.Method == http.MethodPut && uploadId != "":
		this.uploadPart(response, request, bucket, key, uploadId, query.Get("partNumber"))
	case request.Method == http.MethodGet && uploadId != "":
		this.listParts(response, bucket, key, uploadId)
	case request.Method == http.MethodPost && uploadId != "":
		this.complete(response, request, bucket, key, uploadId)
	case request.Method == http.MethodDelete && uploadId != "":
		this.abortIds = append(this.abortIds, uploadId)
		delete(this.uploads, uploadId)
		response.WriteHeader(http.StatusNoContent)
	case request.Method == http.MethodPost && hasDelete && key == "":
		this.deleteObjects(response, request, bucket)
	case request.Method == http.MethodGet && hasUploads && key == "":
		this.listMultipartUploads(response, bucket, query)
	case request.Method == http.MethodGet && hasCors && key == "":
		this.getCors(response, bucket)
	case request.Method == http.MethodPut && hasCors && key == "":
		this.putCors(response, request, bucket)
	case request.Method == http.MethodDelete && hasCors && key == "":
		this.deleteCors(response, bucket)
	case request.Method == http.MethodGet && hasVersioning && key == "":
		this.getVersioning(response, bucket)
	case request.Method == http.MethodPut && hasVersioning && key == "":
		this.putVersioning(response, request, bucket)
	case request.Method == http.MethodGet && hasLifecycle && key == "":
		this.getLifecycle(response, bucket)
	case request.Method == http.MethodPut && hasLifecycle && key == "":
		this.putLifecycle(response, request, bucket)
	case request.Method == http.MethodDelete && hasLifecycle && key == "":
		this.deleteLifecycle(response, bucket)
	case request.Method == http.MethodGet && hasNotification && key == "":
		this.getNotification(response, bucket)
	case request.Method == http.MethodPut && hasNotification && key == "":
		this.putNotification(response, request, bucket)
	case request.Method == http.MethodPut && copySource != "":
		this.copyObject(response, request, bucket, key, copySource)
	case request.Method == http.MethodGet && bucket == "":
		this.listBuckets(response)
	case request.Method == http.MethodPut && key == "":
		this.createBucket(response, bucket)
	case request.Method == http.MethodDelete && key == "":
		this.deleteBucket(response, bucket)
	case request.Method == http.MethodHead && key == "":
		this.headBucket(response, bucket)
	case request.Method == http.MethodGet && key == "":
		this.listObjects(response, bucket, query)
	case request.Method == http.MethodPut:
		this.putObject(response, request, bucket, key)
	case request.Method == http.MethodGet:
		this.getObject(response, request, bucket, key)
	case request.Method == http.MethodHead:
		this.headObject(response, bucket, key)
	case request.Method == http.MethodDelete:
		this.deleteObject(response, bucket, key)
	default:
		this.writeError(response, http.StatusBadRequest, "InvalidRequest", "未支持的操作", request.URL.Path)
	}
}

// createBucket - PUT /{bucket}
func (this *fakeGateway) createBucket(response http.ResponseWriter, bucket string) {

	if this.bucketExists(bucket) {
		this.writeError(response, http.StatusConflict, "BucketAlreadyExists", "桶已存在", "/"+bucket)
		return
	}
	this.buckets[bucket] = fakeModTime
	this.objects[bucket] = map[string]fakeObject{}
	response.WriteHeader(http.StatusOK)
}

// deleteBucket - DELETE /{bucket}（桶必须为空）
func (this *fakeGateway) deleteBucket(response http.ResponseWriter, bucket string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	if len(this.objects[bucket]) > 0 {
		this.writeError(response, http.StatusConflict, "BucketNotEmpty", "桶非空", "/"+bucket)
		return
	}
	delete(this.buckets, bucket)
	delete(this.objects, bucket)
	response.WriteHeader(http.StatusNoContent)
}

// headBucket - HEAD /{bucket}
func (this *fakeGateway) headBucket(response http.ResponseWriter, bucket string) {

	if !this.bucketExists(bucket) {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.WriteHeader(http.StatusOK)
}

// listBuckets - GET /
func (this *fakeGateway) listBuckets(response http.ResponseWriter) {

	names := make([]string, 0, len(this.buckets))
	for name := range this.buckets {
		names = append(names, name)
	}
	sort.Strings(names)

	var builder strings.Builder
	builder.WriteString(`<ListAllMyBucketsResult><Buckets>`)
	for _, name := range names {
		_, _ = fmt.Fprintf(&builder, `<Bucket><Name>%s</Name><CreationDate>%s</CreationDate></Bucket>`,
			name, this.buckets[name].UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	builder.WriteString(`</Buckets></ListAllMyBucketsResult>`)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write([]byte(builder.String()))
}

// initiate - POST /{bucket}/{key}?uploads
func (this *fakeGateway) initiate(response http.ResponseWriter, request *http.Request, bucket, key string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	id := fmt.Sprintf("upload-%d", len(this.uploads)+1)
	this.uploads[id] = &fakeUpload{
		bucket: bucket, key: key,
		mime:  request.Header.Get("Content-Type"),
		parts: map[int][]byte{},
	}
	response.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(response,
		`<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`,
		bucket, key, id)
}

// uploadPart - PUT /{bucket}/{key}?partNumber=N&uploadId=U
func (this *fakeGateway) uploadPart(response http.ResponseWriter, request *http.Request, _, _, uploadId, partRaw string) {

	session, ok := this.uploads[uploadId]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchUpload", "分片会话不存在", "")
		return
	}
	number, err := strconv.Atoi(partRaw)
	if err != nil || number <= 0 {
		this.writeError(response, http.StatusBadRequest, "InvalidArgument", "分片序号不合法", "")
		return
	}
	if number == this.failPartNumber {
		this.writeError(response, http.StatusInternalServerError, "InternalError", "模拟分片失败", "")
		return
	}

	data, _ := io.ReadAll(request.Body)
	session.parts[number] = data
	this.partLog[uploadId] = append(this.partLog[uploadId], number)
	response.Header().Set("ETag", `"`+md5hex(data)+`"`)
	response.WriteHeader(http.StatusOK)
}

// listParts - GET /{bucket}/{key}?uploadId=U
func (this *fakeGateway) listParts(response http.ResponseWriter, bucket, key, uploadId string) {

	session, ok := this.uploads[uploadId]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchUpload", "分片会话不存在", "")
		return
	}

	numbers := make([]int, 0, len(session.parts))
	for number := range session.parts {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)

	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder,
		`<ListPartsResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId><IsTruncated>false</IsTruncated>`,
		bucket, key, uploadId)
	for _, number := range numbers {
		_, _ = fmt.Fprintf(&builder,
			`<Part><PartNumber>%d</PartNumber><LastModified>%s</LastModified><ETag>"%s"</ETag><Size>%d</Size></Part>`,
			number, fakeModTime.UTC().Format("2006-01-02T15:04:05.000Z"), md5hex(session.parts[number]), len(session.parts[number]))
	}
	builder.WriteString(`</ListPartsResult>`)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write([]byte(builder.String()))
}

// complete - POST /{bucket}/{key}?uploadId=U（校验清单升序与分片一致性，按 S3 规则合成 ETag）
func (this *fakeGateway) complete(response http.ResponseWriter, request *http.Request, bucket, key, uploadId string) {

	session, ok := this.uploads[uploadId]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchUpload", "分片会话不存在", "")
		return
	}

	body, _ := io.ReadAll(request.Body)
	this.completeBody = body

	var list xmlCompleteMultipartUpload
	if err := xml.Unmarshal(body, &list); err != nil || len(list.Parts) == 0 {
		this.writeError(response, http.StatusBadRequest, "MalformedXML", "清单不合法", "")
		return
	}

	last := 0
	digests := make([]byte, 0, len(list.Parts)*16)
	combined := make([]byte, 0)
	for _, part := range list.Parts {
		if part.PartNumber <= last {
			this.writeError(response, http.StatusBadRequest, "InvalidPartOrder", "清单必须按序号升序", "")
			return
		}
		last = part.PartNumber

		data, exists := session.parts[part.PartNumber]
		if !exists || md5hex(data) != trimETag(part.ETag) {
			this.writeError(response, http.StatusBadRequest, "InvalidPart", "分片缺失或 ETag 不一致", "")
			return
		}
		raw, _ := hex.DecodeString(trimETag(part.ETag))
		digests = append(digests, raw...)
		combined = append(combined, data...)
	}

	etag := md5hex(digests) + "-" + strconv.Itoa(len(list.Parts))
	this.objects[bucket][key] = fakeObject{data: combined, mime: session.mime}
	delete(this.uploads, uploadId)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(response,
		`<CompleteMultipartUploadResult><Location>/%s/%s</Location><Bucket>%s</Bucket><Key>%s</Key><ETag>"%s"</ETag></CompleteMultipartUploadResult>`,
		bucket, key, bucket, key, etag)
}

// putObject - PUT /{bucket}/{key}
func (this *fakeGateway) putObject(response http.ResponseWriter, request *http.Request, bucket, key string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}

	data, _ := io.ReadAll(request.Body)
	mime := request.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}
	this.objects[bucket][key] = fakeObject{data: data, mime: mime}

	etag := `"` + md5hex(data) + `"`
	if this.wrongETag {
		etag = `"00000000000000000000000000000000"`
	}
	if this.multipartETag {
		etag = `"0123456789abcdef0123456789abcdef-2"`
	}
	response.Header().Set("ETag", etag)
	response.WriteHeader(http.StatusOK)
}

// objectHeaders - 对象响应公共头（含用户元数据注入）
func (this *fakeGateway) objectHeaders(response http.ResponseWriter, item fakeObject) {

	response.Header().Set("ETag", `"`+md5hex(item.data)+`"`)
	response.Header().Set("Content-Type", item.mime)
	response.Header().Set("Content-Length", strconv.Itoa(len(item.data)))
	response.Header().Set("Last-Modified", fakeModTime.UTC().Format(http.TimeFormat))
	response.Header().Set("X-Amz-Meta-Owner", "rabbit")
}

// getObject - GET /{bucket}/{key}（支持 Range）
func (this *fakeGateway) getObject(response http.ResponseWriter, request *http.Request, bucket, key string) {

	item, ok := this.objects[bucket][key]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchKey", "对象不存在", "/"+bucket+"/"+key)
		return
	}

	this.lastRange = request.Header.Get("Range")
	this.objectHeaders(response, item)

	if this.lastRange != "" {
		var start, end int
		if _, err := fmt.Sscanf(this.lastRange, "bytes=%d-%d", &start, &end); err == nil {
			if end >= len(item.data) {
				end = len(item.data) - 1
			}
			segment := item.data[start : end+1]
			response.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(item.data)))
			response.Header().Set("Content-Length", strconv.Itoa(len(segment)))
			response.WriteHeader(http.StatusPartialContent)
			_, _ = response.Write(segment)
			return
		}
	}

	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(item.data)
}

// headObject - HEAD /{bucket}/{key}
func (this *fakeGateway) headObject(response http.ResponseWriter, bucket, key string) {

	item, ok := this.objects[bucket][key]
	if !ok {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	this.objectHeaders(response, item)
	response.WriteHeader(http.StatusOK)
}

// deleteObject - DELETE /{bucket}/{key}
func (this *fakeGateway) deleteObject(response http.ResponseWriter, bucket, key string) {

	if _, ok := this.objects[bucket][key]; !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchKey", "对象不存在", "/"+bucket+"/"+key)
		return
	}
	delete(this.objects[bucket], key)
	response.WriteHeader(http.StatusNoContent)
}

// listObjects - GET /{bucket}?list-type=2（ListObjectsV2：前缀 + 折叠 + 翻页）
func (this *fakeGateway) listObjects(response http.ResponseWriter, bucket string, query url.Values) {

	store, ok := this.objects[bucket]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}

	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	token := query.Get("continuation-token")
	maxKeys, _ := strconv.Atoi(query.Get("max-keys"))

	keys := make([]string, 0, len(store))
	for key := range store {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	type entry struct {
		key  string
		item fakeObject
	}
	entries := make([]entry, 0)
	prefixes := make([]string, 0)

	started := token == ""
	truncated := false
	nextToken := ""
	count := 0
	for _, key := range keys {
		if !started {
			if key == token {
				started = true
			}
			continue
		}
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if delimiter != "" {
			if index := strings.Index(key[len(prefix):], delimiter); index >= 0 {
				common := prefix + key[len(prefix):][:index+1]
				if len(prefixes) == 0 || prefixes[len(prefixes)-1] != common {
					prefixes = append(prefixes, common)
					count++
				}
				continue
			}
		}
		entries = append(entries, entry{key: key, item: store[key]})
		count++
		if maxKeys > 0 && count >= maxKeys {
			truncated = true
			nextToken = key
			break
		}
	}

	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder,
		`<ListBucketResult><Name>%s</Name><Prefix>%s</Prefix><MaxKeys>%d</MaxKeys><IsTruncated>%t</IsTruncated>`,
		bucket, prefix, maxKeys, truncated)
	for _, item := range entries {
		_, _ = fmt.Fprintf(&builder,
			`<Contents><Key>%s</Key><LastModified>%s</LastModified><ETag>"%s"</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>`,
			item.key, fakeModTime.UTC().Format("2006-01-02T15:04:05.000Z"), md5hex(item.item.data), len(item.item.data))
	}
	for _, common := range prefixes {
		_, _ = fmt.Fprintf(&builder, `<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes>`, common)
	}
	if nextToken != "" {
		_, _ = fmt.Fprintf(&builder, `<NextContinuationToken>%s</NextContinuationToken>`, nextToken)
	}
	builder.WriteString(`</ListBucketResult>`)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write([]byte(builder.String()))
}

// ============================= 内存版 S3 假网关 - 结束 =============================

// ============================= 装配与桶操作 - 开始 =============================

// TestNewConfig - 装配参数校验与默认值
func TestNewConfig(t *testing.T) {

	if _, err := New(Config{Endpoint: "  "}); err == nil {
		t.Fatalf("空网关地址应报错")
	}
	if _, err := New(Config{Endpoint: "://bad"}); err == nil {
		t.Fatalf("不合法网关地址应报错")
	}

	client, err := New(Config{Endpoint: "https://storage.example.com:9000/", AccessKey: "AK", Secret: "SK"})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if client.region != "us-east-1" {
		t.Fatalf("区域默认值应为 us-east-1，实际 %q", client.region)
	}
	if client.endpoint != "https://storage.example.com:9000" {
		t.Fatalf("网关地址末尾斜杠应被裁剪，实际 %q", client.endpoint)
	}
	if client.host != "storage.example.com:9000" {
		t.Fatalf("签名主机不符合预期：%q", client.host)
	}
}

// TestBucketLifecycle - 建桶 → 列桶 → 探测 → 删桶 全生命周期
func TestBucketLifecycle(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}
	if err := client.CreateBucket(ctx, "photos"); err == nil || !IsCode(err, "BucketAlreadyExists") {
		t.Fatalf("重复建桶应返回 BucketAlreadyExists：%v", err)
	}

	rows, err := client.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("列桶失败：%v", err)
	}
	if len(rows) != 1 || rows[0].Name != "photos" {
		t.Fatalf("桶列表不符合预期：%+v", rows)
	}
	if !rows[0].CreateAt.Equal(fakeModTime) {
		t.Fatalf("桶创建时间解析不符合预期：%v", rows[0].CreateAt)
	}

	if err = client.HeadBucket(ctx, "photos"); err != nil {
		t.Fatalf("探测已存在桶失败：%v", err)
	}
	if err = client.HeadBucket(ctx, "missing"); err == nil {
		t.Fatalf("探测不存在桶应报错")
	}

	if err = client.DeleteBucket(ctx, "missing"); err == nil || !IsCode(err, "NoSuchBucket") {
		t.Fatalf("删除不存在桶应返回 NoSuchBucket：%v", err)
	}
	if err = client.DeleteBucket(ctx, "photos"); err != nil {
		t.Fatalf("删桶失败：%v", err)
	}

	rows, err = client.ListBuckets(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("删桶后列表应为空：rows=%+v err=%v", rows, err)
	}
}

// ============================= 装配与桶操作 - 结束 =============================

// ============================= 对象操作 - 开始 =============================

// TestPutGetHeadDeleteObject - 对象写读删全链路（ETag 回核 + 元数据解析 + Range）
func TestPutGetHeadDeleteObject(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	content := []byte("hello inis object storage")
	put, err := client.PutObject(ctx, PutInput{
		Bucket: "photos", Key: "a.jpg",
		Body: strings.NewReader(string(content)), Mime: "image/jpeg", VerifyETag: true,
	})
	if err != nil {
		t.Fatalf("上传失败：%v", err)
	}
	if put.ETag != md5hex(content) {
		t.Fatalf("ETag 不符合预期：%q", put.ETag)
	}
	if put.Size != int64(len(content)) {
		t.Fatalf("写入字节数不符合预期：%d", put.Size)
	}
	if put.VersionId != "null" {
		t.Fatalf("无版本控制时 VersionId 应为 null，实际 %q", put.VersionId)
	}
	if stored := gateway.objects["photos"]["a.jpg"]; !errors.Is(nil, nil) || string(stored.data) != string(content) || stored.mime != "image/jpeg" {
		t.Fatalf("网关存储内容不符合预期：%+v", stored)
	}

	// 下载（全量）
	get, err := client.GetObject(ctx, "photos", "a.jpg", nil)
	if err != nil {
		t.Fatalf("下载失败：%v", err)
	}
	defer func() { _ = get.Body.Close() }()
	body, _ := io.ReadAll(get.Body)
	if string(body) != string(content) {
		t.Fatalf("下载内容不符合预期：%q", body)
	}
	if get.Meta.ETag != md5hex(content) || get.Meta.Size != int64(len(content)) || get.Meta.Mime != "image/jpeg" {
		t.Fatalf("下载元数据不符合预期：%+v", get.Meta)
	}
	if !get.Meta.LastModified.Equal(fakeModTime) {
		t.Fatalf("Last-Modified 解析不符合预期：%v", get.Meta.LastModified)
	}
	if get.Meta.Meta["owner"] != "rabbit" {
		t.Fatalf("用户元数据解析不符合预期：%+v", get.Meta.Meta)
	}
	if get.Meta.VersionId != "null" {
		t.Fatalf("VersionId 默认应为 null，实际 %q", get.Meta.VersionId)
	}
	_ = get.Body.Close()

	// 下载（Range 区间）
	partial, err := client.GetObject(ctx, "photos", "a.jpg", &GetOptions{Range: "bytes=0-4"})
	if err != nil {
		t.Fatalf("Range 下载失败：%v", err)
	}
	segment, _ := io.ReadAll(partial.Body)
	_ = partial.Body.Close()
	if string(segment) != "hello" {
		t.Fatalf("Range 下载内容不符合预期：%q", segment)
	}
	if gateway.lastRange != "bytes=0-4" {
		t.Fatalf("网关未收到 Range 头：%q", gateway.lastRange)
	}

	// 元数据
	meta, err := client.HeadObject(ctx, "photos", "a.jpg")
	if err != nil {
		t.Fatalf("取元数据失败：%v", err)
	}
	if meta.Size != int64(len(content)) || meta.Mime != "image/jpeg" || meta.ETag != md5hex(content) {
		t.Fatalf("Head 元数据不符合预期：%+v", meta)
	}

	// 删除后再读应 404
	if err = client.DeleteObject(ctx, "photos", "a.jpg"); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if _, err = client.GetObject(ctx, "photos", "a.jpg", nil); !IsCode(err, "NoSuchKey") {
		t.Fatalf("删除后再下载应返回 NoSuchKey：%v", err)
	}
}

// TestPutObjectETagMismatch - 网关返回错误 ETag 时回核失败
func TestPutObjectETagMismatch(t *testing.T) {

	gateway := newFakeGateway()
	gateway.wrongETag = true
	client := newTestClient(t, gateway)

	err := client.CreateBucket(context.Background(), "photos")
	if err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	_, err = client.PutObject(context.Background(), PutInput{
		Bucket: "photos", Key: "a.jpg", Body: strings.NewReader("hello"), VerifyETag: true,
	})
	if !errors.Is(err, ErrTransport) || !strings.Contains(err.Error(), "ETag 校验不一致") {
		t.Fatalf("ETag 不一致应回核失败：%v", err)
	}
}

// TestPutObjectSkipsMultipartETag - 分片形态 ETag（含 -）跳过回核
func TestPutObjectSkipsMultipartETag(t *testing.T) {

	gateway := newFakeGateway()
	gateway.multipartETag = true
	client := newTestClient(t, gateway)

	if err := client.CreateBucket(context.Background(), "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	put, err := client.PutObject(context.Background(), PutInput{
		Bucket: "photos", Key: "a.jpg", Body: strings.NewReader("hello"), VerifyETag: true,
	})
	if err != nil {
		t.Fatalf("分片形态 ETag 不应触发回核失败：%v", err)
	}
	if put.ETag != "0123456789abcdef0123456789abcdef-2" {
		t.Fatalf("ETag 透传不符合预期：%q", put.ETag)
	}
}

// TestObjectNotFound - 404 错误映射（NoSuchKey / IsNotFound / RequestId 透传）
func TestObjectNotFound(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)

	if err := client.CreateBucket(context.Background(), "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	_, err := client.GetObject(context.Background(), "photos", "missing.jpg", nil)
	if err == nil {
		t.Fatalf("下载不存在对象应报错")
	}
	if !IsCode(err, "NoSuchKey") || !IsNotFound(err) {
		t.Fatalf("错误码映射不符合预期：%v", err)
	}

	var item *S3Error
	if !errors.As(err, &item) {
		t.Fatalf("错误应可断言为 *S3Error：%T", err)
	}
	if item.HTTPStatus != http.StatusNotFound || item.RequestId != "req-test-0001" || item.Resource != "/photos/missing.jpg" {
		t.Fatalf("错误字段不符合预期：%+v", item)
	}

	// HEAD 404 无响应体：按状态码归一
	_, err = client.HeadObject(context.Background(), "photos", "missing.jpg")
	if !IsNotFound(err) {
		t.Fatalf("HEAD 404 应判定为不存在：%v", err)
	}

	if err = client.DeleteObject(context.Background(), "photos", "missing.jpg"); !IsCode(err, "NoSuchKey") {
		t.Fatalf("删除不存在对象应返回 NoSuchKey：%v", err)
	}
}

// TestErrorNonXML - 非 XML 错误体按状态码归一
func TestErrorNonXML(t *testing.T) {

	gateway := newFakeGateway()
	gateway.plainError = true
	client := newTestClient(t, gateway)

	err := client.CreateBucket(context.Background(), "photos")
	if err == nil {
		t.Fatalf("非 XML 错误应报错")
	}

	var item *S3Error
	if !errors.As(err, &item) {
		t.Fatalf("错误应可断言为 *S3Error：%T", err)
	}
	if item.HTTPStatus != http.StatusServiceUnavailable || item.Code != "Service Unavailable" || item.Message != "gateway unavailable" {
		t.Fatalf("非 XML 错误归一不符合预期：%+v", item)
	}
}

// TestSignatureMismatch - 错误凭据触发网关侧验签拒绝
func TestSignatureMismatch(t *testing.T) {

	gateway := newFakeGateway()
	server := httptest.NewServer(gateway)
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL, Region: "us-east-1", AccessKey: "AK", Secret: "WRONG"})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}

	err = func() error {
		_, listErr := client.ListBuckets(context.Background())
		return listErr
	}()
	if !IsCode(err, "SignatureDoesNotMatch") {
		t.Fatalf("错误凭据应返回 SignatureDoesNotMatch：%v", err)
	}
}

// ============================= 对象操作 - 结束 =============================

// ============================= 列表浏览 - 开始 =============================

// seedObjects - 批量种入对象（列表测试前置）
func seedObjects(t *testing.T, client *Client, bucket string, keys ...string) {

	t.Helper()
	for _, key := range keys {
		_, err := client.PutObject(context.Background(), PutInput{
			Bucket: bucket, Key: key, Body: strings.NewReader("data-" + key), VerifyETag: true,
		})
		if err != nil {
			t.Fatalf("种入对象 %s 失败：%v", key, err)
		}
	}
}

// TestListObjects - 前缀过滤 + 目录折叠 + 字段解析
func TestListObjects(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}
	seedObjects(t, client, "photos", "photos/a.jpg", "photos/b.jpg", "docs/readme.txt", "top.txt")

	// 前缀过滤
	result, err := client.ListObjects(ctx, ListInput{Bucket: "photos", Prefix: "photos/"})
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Key != "photos/a.jpg" || result.Entries[1].Key != "photos/b.jpg" {
		t.Fatalf("前缀过滤结果不符合预期：%+v", result.Entries)
	}
	if result.IsTruncated || result.NextToken != "" {
		t.Fatalf("单页结果不应有翻页标记：%+v", result)
	}
	entry := result.Entries[0]
	if entry.Size != int64(len("data-photos/a.jpg")) || entry.StorageClass != "STANDARD" {
		t.Fatalf("条目字段不符合预期：%+v", entry)
	}
	if entry.ETag != md5hex([]byte("data-photos/a.jpg")) || strings.Contains(entry.ETag, `"`) {
		t.Fatalf("ETag 应去除引号：%q", entry.ETag)
	}
	if !entry.LastModified.Equal(fakeModTime) {
		t.Fatalf("LastModified 解析不符合预期：%v", entry.LastModified)
	}

	// 目录折叠（顶层对象保留为条目，含 / 的折叠为公共前缀）
	folded, err := client.ListObjects(ctx, ListInput{Bucket: "photos", Delimiter: "/"})
	if err != nil {
		t.Fatalf("折叠列举失败：%v", err)
	}
	if len(folded.Entries) != 1 || folded.Entries[0].Key != "top.txt" {
		t.Fatalf("折叠后的条目不符合预期：%+v", folded.Entries)
	}
	if len(folded.Prefixes) != 2 || folded.Prefixes[0] != "docs/" || folded.Prefixes[1] != "photos/" {
		t.Fatalf("折叠后的公共前缀不符合预期：%+v", folded.Prefixes)
	}
}

// TestListObjectsPagination - MaxKeys 截断 + 翻页令牌往返
func TestListObjectsPagination(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}
	seedObjects(t, client, "photos", "a/1.txt", "a/2.txt", "a/3.txt")

	page1, err := client.ListObjects(ctx, ListInput{Bucket: "photos", MaxKeys: 2})
	if err != nil {
		t.Fatalf("第一页列举失败：%v", err)
	}
	if len(page1.Entries) != 2 || !page1.IsTruncated || page1.NextToken != "a/2.txt" {
		t.Fatalf("第一页不符合预期：%+v", page1)
	}

	page2, err := client.ListObjects(ctx, ListInput{Bucket: "photos", MaxKeys: 2, Token: page1.NextToken})
	if err != nil {
		t.Fatalf("第二页列举失败：%v", err)
	}
	if len(page2.Entries) != 1 || page2.Entries[0].Key != "a/3.txt" || page2.IsTruncated {
		t.Fatalf("第二页不符合预期：%+v", page2)
	}
}

// TestListObjectsEncodedPrefix - 含空格前缀的查询编码与签名一致性（网关侧验签回核覆盖）
func TestListObjectsEncodedPrefix(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}
	seedObjects(t, client, "photos", "my docs/a.txt", "my docs/b.txt", "other/c.txt")

	result, err := client.ListObjects(ctx, ListInput{Bucket: "photos", Prefix: "my docs/"})
	if err != nil {
		t.Fatalf("列举失败：%v", err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Key != "my docs/a.txt" {
		t.Fatalf("含空格前缀的列举结果不符合预期：%+v", result.Entries)
	}
}

// ============================= 列表浏览 - 结束 =============================

// ============================= 分片上传 - 开始 =============================

// TestUploadSinglePut - 小于分片阈值走单 PUT（不产生分片会话）
func TestUploadSinglePut(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	content := []byte("small file")
	result, err := client.Upload(ctx, UploadInput{
		Bucket: "photos", Key: "small.txt",
		Body: strings.NewReader(string(content)), Size: int64(len(content)),
	})
	if err != nil {
		t.Fatalf("上传失败：%v", err)
	}
	if result.ETag != md5hex(content) || result.Size != int64(len(content)) {
		t.Fatalf("单 PUT 结果不符合预期：%+v", result)
	}
	if len(gateway.uploads) != 0 {
		t.Fatalf("小文件不应产生分片会话：%+v", gateway.uploads)
	}
	if stored := gateway.objects["photos"]["small.txt"]; string(stored.data) != string(content) {
		t.Fatalf("网关存储内容不符合预期")
	}
}

// TestUploadMultipart - 大于分片阈值走分片编排（并发上传 + 升序 Complete + 内容合成）
func TestUploadMultipart(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	// 12MB 确定性内容（5MB + 5MB + 2MB 三片）
	const total = 12 << 20
	content := make([]byte, total)
	for index := range content {
		content[index] = byte(index % 251)
	}

	result, err := client.Upload(ctx, UploadInput{
		Bucket: "photos", Key: "big.bin",
		Body: strings.NewReader(string(content)), Size: total,
		Mime: "application/octet-stream", PartSize: minPartSize, Concurrency: 3,
	})
	if err != nil {
		t.Fatalf("分片上传失败：%v", err)
	}
	if result.Size != total {
		t.Fatalf("分片上传字节数不符合预期：%d", result.Size)
	}
	if !strings.HasSuffix(result.ETag, "-3") {
		t.Fatalf("分片 ETag 应带片数后缀：%q", result.ETag)
	}

	stored, ok := gateway.objects["photos"]["big.bin"]
	if !ok || len(stored.data) != total {
		t.Fatalf("网关未合成完整对象：ok=%v len=%d", ok, len(stored.data))
	}
	for index := range content {
		if stored.data[index] != content[index] {
			t.Fatalf("合成对象第 %d 字节不一致", index)
		}
	}
	if len(gateway.uploads) != 0 {
		t.Fatalf("Complete 后不应残留分片会话：%+v", gateway.uploads)
	}

	// Complete 清单必须升序（网关侧 400 InvalidPartOrder 兜底，这里断言成功即升序）
	var list xmlCompleteMultipartUpload
	if err = xml.Unmarshal(gateway.completeBody, &list); err != nil || len(list.Parts) != 3 {
		t.Fatalf("Complete 清单不符合预期：err=%v parts=%d", err, len(list.Parts))
	}
	for index, part := range list.Parts {
		if part.PartNumber != index+1 {
			t.Fatalf("清单序号应升序：%+v", list.Parts)
		}
		if !strings.HasPrefix(part.ETag, `"`) || !strings.HasSuffix(part.ETag, `"`) {
			t.Fatalf("清单 ETag 应带引号回传：%q", part.ETag)
		}
	}
}

// TestUploadMultipartAbortOnFailure - 分片失败自动 Abort（幂等清理）
func TestUploadMultipartAbortOnFailure(t *testing.T) {

	gateway := newFakeGateway()
	gateway.failPartNumber = 2
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	const total = 12 << 20
	content := make([]byte, total)

	_, err := client.Upload(ctx, UploadInput{
		Bucket: "photos", Key: "big.bin",
		Body: strings.NewReader(string(content)), Size: total,
		PartSize: minPartSize, Concurrency: 1,
	})
	if err == nil {
		t.Fatalf("分片失败应整体报错")
	}
	if !IsCode(err, "InternalError") {
		t.Fatalf("失败原因应为分片上传错误：%v", err)
	}
	if len(gateway.abortIds) != 1 {
		t.Fatalf("失败后应自动 Abort 一次：%+v", gateway.abortIds)
	}
	if len(gateway.uploads) != 0 {
		t.Fatalf("Abort 后不应残留分片会话：%+v", gateway.uploads)
	}
}

// TestUploadInvalidInput - 上传入参本地校验
func TestUploadInvalidInput(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	cases := []UploadInput{
		{Bucket: "", Key: "k", Body: strings.NewReader("x"), Size: 1},
		{Bucket: "b", Key: "", Body: strings.NewReader("x"), Size: 1},
		{Bucket: "b", Key: "k", Body: nil, Size: 1},
		{Bucket: "b", Key: "k", Body: strings.NewReader("x"), Size: -1},
	}
	for index, input := range cases {
		if _, err := client.Upload(ctx, input); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("用例 %d 应返回 ErrInvalidRequest：%v", index, err)
		}
	}
}

// TestMultipartDirect - 分片六操作直调（Create/UploadPart/ListParts/Abort）
func TestMultipartDirect(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()

	if err := client.CreateBucket(ctx, "photos"); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}

	uploadId, err := client.CreateMultipartUpload(ctx, "photos", "movie.mp4", "video/mp4")
	if err != nil {
		t.Fatalf("初始化分片失败：%v", err)
	}
	if uploadId == "" {
		t.Fatalf("uploadId 不应为空")
	}

	first := []byte("part-one-content")
	part1, err := client.UploadPart(ctx, "photos", "movie.mp4", uploadId, 1, strings.NewReader(string(first)))
	if err != nil {
		t.Fatalf("上传分片 1 失败：%v", err)
	}
	if part1.PartNumber != 1 || part1.ETag != md5hex(first) || part1.Size != int64(len(first)) {
		t.Fatalf("分片 1 结果不符合预期：%+v", part1)
	}

	second := []byte("part-two-content")
	if _, err = client.UploadPart(ctx, "photos", "movie.mp4", uploadId, 2, strings.NewReader(string(second))); err != nil {
		t.Fatalf("上传分片 2 失败：%v", err)
	}

	parts, err := client.ListParts(ctx, "photos", "movie.mp4", uploadId)
	if err != nil {
		t.Fatalf("查询分片失败：%v", err)
	}
	if len(parts) != 2 || parts[0].PartNumber != 1 || parts[1].PartNumber != 2 {
		t.Fatalf("分片列表不符合预期：%+v", parts)
	}
	if strings.Contains(parts[0].ETag, `"`) {
		t.Fatalf("分片 ETag 应去引号：%q", parts[0].ETag)
	}

	if err = client.AbortMultipartUpload(ctx, "photos", "movie.mp4", uploadId); err != nil {
		t.Fatalf("中止分片失败：%v", err)
	}
	if len(gateway.abortIds) != 1 || gateway.abortIds[0] != uploadId {
		t.Fatalf("网关未记录 Abort：%+v", gateway.abortIds)
	}
	if _, err = client.ListParts(ctx, "photos", "movie.mp4", uploadId); !IsCode(err, "NoSuchUpload") {
		t.Fatalf("Abort 后查询应返回 NoSuchUpload：%v", err)
	}
}

// ============================= 分片上传 - 结束 =============================

// ============================= 预签名 - 开始 =============================

// TestPresignGetPut - 预签名 URL 组装（默认有效期 / 上限 / 会话令牌 / 与底层签名一致性）
func TestPresignGetPut(t *testing.T) {

	client, err := New(Config{Endpoint: "https://storage.example.com:9000", AccessKey: "AK", Secret: "SK"})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	fixed := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return fixed }
	ctx := context.Background()

	// GET：默认 1 小时
	raw, err := client.PresignGet(ctx, "photos", "a b.jpg", 0)
	if err != nil {
		t.Fatalf("预签名失败：%v", err)
	}
	if !strings.HasPrefix(raw, "https://storage.example.com:9000/photos/a%20b.jpg?") {
		t.Fatalf("预签名 URL 前缀不符合预期：%s", raw)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("预签名 URL 解析失败：%v", err)
	}
	query := parsed.Query()
	if query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" ||
		query.Get("X-Amz-Credential") != "AK/20261119/us-east-1/s3/aws4_request" ||
		query.Get("X-Amz-Date") != "20261119T120000Z" ||
		query.Get("X-Amz-Expires") != "3600" ||
		query.Get("X-Amz-SignedHeaders") != "host" ||
		len(query.Get("X-Amz-Signature")) != 64 {
		t.Fatalf("预签名参数不符合预期：%s", raw)
	}

	// 与底层签名器同输入直签结果必须一致（URL 组装无二次加工）
	want := client.creds.presign(signInput{
		method: http.MethodGet, host: "storage.example.com:9000", path: "/photos/a%20b.jpg",
		query: map[string]string{}, now: fixed, region: "us-east-1", expires: 3600,
	})
	if query.Get("X-Amz-Signature") != want["X-Amz-Signature"] {
		t.Fatalf("URL 签名与底层直签不一致：got=%s want=%s", query.Get("X-Amz-Signature"), want["X-Amz-Signature"])
	}

	// 超过 7 天硬上限自动收敛
	capped, err := client.PresignGet(ctx, "photos", "a.jpg", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("预签名失败：%v", err)
	}
	parsed, _ = url.Parse(capped)
	if parsed.Query().Get("X-Amz-Expires") != "604800" {
		t.Fatalf("有效期应收敛到 7 天：%s", capped)
	}

	// PUT 预签名
	putURL, err := client.PresignPut(ctx, "photos", "a.jpg", "image/jpeg", 2*time.Hour)
	if err != nil {
		t.Fatalf("PUT 预签名失败：%v", err)
	}
	parsed, _ = url.Parse(putURL)
	if parsed.Query().Get("X-Amz-Expires") != "7200" {
		t.Fatalf("PUT 预签名有效期不符合预期：%s", putURL)
	}

	// 空桶名校验
	if _, err = client.PresignGet(ctx, "", "a.jpg", 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应返回 ErrInvalidRequest：%v", err)
	}
}

// TestPresignSessionToken - 会话令牌进入预签名查询参数
func TestPresignSessionToken(t *testing.T) {

	client, err := New(Config{
		Endpoint: "https://storage.example.com:9000",
		AccessKey: "AK", Secret: "SK", SessionToken: "TOKEN",
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}

	raw, err := client.PresignGet(context.Background(), "photos", "a.jpg", time.Hour)
	if err != nil {
		t.Fatalf("预签名失败：%v", err)
	}
	parsed, _ := url.Parse(raw)
	if parsed.Query().Get("X-Amz-Security-Token") != "TOKEN" {
		t.Fatalf("预签名缺少会话令牌：%s", raw)
	}
}

// ============================= 预签名 - 结束 =============================

// ============================= 工具函数 - 开始 =============================

// TestTrimQuoteETag - ETag 引号处理
func TestTrimQuoteETag(t *testing.T) {

	if got := trimETag(`"abc123"`); got != "abc123" {
		t.Fatalf("去引号失败：%q", got)
	}
	if got := quoteETag("abc123"); got != `"abc123"` {
		t.Fatalf("加引号失败：%q", got)
	}
	if got := quoteETag(""); got != "" {
		t.Fatalf("空 ETag 加引号应为空：%q", got)
	}
}

// ============================= 工具函数 - 结束 =============================
