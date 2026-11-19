package s3

import (
	"bytes"
	"context"
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
	"testing"
	"time"
)

// ============================= 假网关 P2 处理器 - 开始 =============================

// getSessionToken - POST /（STS：Action=GetSessionToken，AWS STS 兼容响应）
func (this *fakeGateway) getSessionToken(response http.ResponseWriter, request *http.Request) {

	if err := request.ParseForm(); err != nil {
		this.writeError(response, http.StatusBadRequest, "InvalidArgument", "表单不合法", request.URL.Path)
		return
	}
	if request.PostForm.Get("Action") != "GetSessionToken" {
		this.writeError(response, http.StatusBadRequest, "InvalidAction", "未支持的 Action", request.URL.Path)
		return
	}

	seconds := int64(3600)
	if raw := request.PostForm.Get("DurationSeconds"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			this.writeError(response, http.StatusBadRequest, "InvalidArgument", "DurationSeconds 必须是整数", request.URL.Path)
			return
		}
		seconds = value
	}
	this.stsDuration = seconds

	expire := fakeModTime.Add(time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339)
	response.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(response,
		`<GetSessionTokenResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetSessionTokenResult><Credentials>`+
			`<AccessKeyId>AK-SESSION</AccessKeyId><SecretAccessKey>SK-SESSION</SecretAccessKey>`+
			`<SessionToken>TOKEN-123</SessionToken><Expiration>%s</Expiration>`+
			`</Credentials></GetSessionTokenResult><ResponseMetadata><RequestId>req-test-0001</RequestId></ResponseMetadata></GetSessionTokenResponse>`,
		expire)
}

// deleteObjects - POST /{bucket}?delete（键含 fail 模拟单键失败，不阻断批次）
func (this *fakeGateway) deleteObjects(response http.ResponseWriter, request *http.Request, bucket string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}

	body, _ := io.ReadAll(request.Body)
	var list xmlDeleteRequest
	if err := xml.Unmarshal(body, &list); err != nil || len(list.Objects) == 0 || len(list.Objects) > maxDeleteKeys {
		this.writeError(response, http.StatusBadRequest, "MalformedXML", "删除请求不合法", "/"+bucket)
		return
	}

	var builder strings.Builder
	builder.WriteString(`<DeleteResult>`)
	for _, item := range list.Objects {
		if strings.Contains(item.Key, "fail") {
			_, _ = fmt.Fprintf(&builder, `<Error><Key>%s</Key><Code>InternalError</Code><Message>模拟删除失败</Message></Error>`, item.Key)
			continue
		}
		delete(this.objects[bucket], item.Key)
		_, _ = fmt.Fprintf(&builder, `<Deleted><Key>%s</Key></Deleted>`, item.Key)
	}
	builder.WriteString(`</DeleteResult>`)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write([]byte(builder.String()))
}

// copyObject - PUT /{bucket}/{key} + X-Amz-Copy-Source（源按原形态解析，不解码）
func (this *fakeGateway) copyObject(response http.ResponseWriter, request *http.Request, bucket, key, source string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "目标桶不存在", "/"+bucket)
		return
	}

	srcBucket, srcKey, found := strings.Cut(strings.TrimPrefix(source, "/"), "/")
	if !found || srcBucket == "" || srcKey == "" {
		this.writeError(response, http.StatusBadRequest, "InvalidArgument", "拷贝源头不合法", "/"+bucket)
		return
	}
	src, ok := this.objects[srcBucket][srcKey]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchKey", "源对象不存在", "/"+srcBucket+"/"+srcKey)
		return
	}

	this.objects[bucket][key] = src
	response.Header().Set("Content-Type", "application/xml")
	_, _ = fmt.Fprintf(response,
		`<CopyObjectResult><LastModified>%s</LastModified><ETag>"%s"</ETag></CopyObjectResult>`,
		fakeModTime.UTC().Format("2006-01-02T15:04:05.000Z"), md5hex(src.data))
}

// listMultipartUploads - GET /{bucket}?uploads（单页返回全量匹配）
func (this *fakeGateway) listMultipartUploads(response http.ResponseWriter, bucket string, query url.Values) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	prefix := query.Get("prefix")

	ids := make([]string, 0, len(this.uploads))
	for id, session := range this.uploads {
		if session.bucket == bucket && strings.HasPrefix(session.key, prefix) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	var builder strings.Builder
	_, _ = fmt.Fprintf(&builder,
		`<ListMultipartUploadsResult><Bucket>%s</Bucket><KeyMarker></KeyMarker><UploadIdMarker></UploadIdMarker><MaxUploads>1000</MaxUploads><IsTruncated>false</IsTruncated>`,
		bucket)
	for _, id := range ids {
		_, _ = fmt.Fprintf(&builder,
			`<Upload><Key>%s</Key><UploadId>%s</UploadId><Initiated>%s</Initiated><StorageClass>STANDARD</StorageClass></Upload>`,
			this.uploads[id].key, id, fakeModTime.UTC().Format("2006-01-02T15:04:05.000Z"))
	}
	builder.WriteString(`</ListMultipartUploadsResult>`)

	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write([]byte(builder.String()))
}

// getCors - GET /{bucket}?cors（未配置 404 NoSuchCORSConfiguration）
func (this *fakeGateway) getCors(response http.ResponseWriter, bucket string) {

	body, ok := this.corsConfigs[bucket]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchCORSConfiguration", "未配置 CORS", "/"+bucket)
		return
	}
	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write(body)
}

// putCors - PUT /{bucket}?cors（原文落存，基本形态校验）
func (this *fakeGateway) putCors(response http.ResponseWriter, request *http.Request, bucket string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	body, _ := io.ReadAll(request.Body)
	var parsed xmlCORSConfiguration
	if err := xml.Unmarshal(body, &parsed); err != nil || len(parsed.Rules) == 0 {
		this.writeError(response, http.StatusBadRequest, "MalformedXML", "CORS 配置不合法", "/"+bucket)
		return
	}
	this.corsConfigs[bucket] = body
	response.WriteHeader(http.StatusOK)
}

// deleteCors - DELETE /{bucket}?cors
func (this *fakeGateway) deleteCors(response http.ResponseWriter, bucket string) {

	delete(this.corsConfigs, bucket)
	response.WriteHeader(http.StatusNoContent)
}

// getVersioning - GET /{bucket}?versioning（未开启返回空 Status）
func (this *fakeGateway) getVersioning(response http.ResponseWriter, bucket string) {

	status := this.versioningStatus[bucket]
	response.Header().Set("Content-Type", "application/xml")
	if status == "" {
		_, _ = response.Write([]byte(`<VersioningConfiguration></VersioningConfiguration>`))
		return
	}
	_, _ = fmt.Fprintf(response, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, status)
}

// putVersioning - PUT /{bucket}?versioning（仅收 Enabled / Suspended）
func (this *fakeGateway) putVersioning(response http.ResponseWriter, request *http.Request, bucket string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	body, _ := io.ReadAll(request.Body)
	var parsed xmlVersioningConfiguration
	if err := xml.Unmarshal(body, &parsed); err != nil {
		this.writeError(response, http.StatusBadRequest, "MalformedXML", "版本控制配置不合法", "/"+bucket)
		return
	}
	if parsed.Status != VersioningEnabled && parsed.Status != VersioningSuspended {
		this.writeError(response, http.StatusBadRequest, "IllegalVersioningConfigurationException", "状态不合法", "/"+bucket)
		return
	}
	this.versioningStatus[bucket] = parsed.Status
	response.WriteHeader(http.StatusOK)
}

// getLifecycle - GET /{bucket}?lifecycle（未配置 404 NoSuchLifecycleConfiguration）
func (this *fakeGateway) getLifecycle(response http.ResponseWriter, bucket string) {

	body, ok := this.lifecycleConfigs[bucket]
	if !ok {
		this.writeError(response, http.StatusNotFound, "NoSuchLifecycleConfiguration", "未配置生命周期", "/"+bucket)
		return
	}
	response.Header().Set("Content-Type", "application/xml")
	_, _ = response.Write(body)
}

// putLifecycle - PUT /{bucket}?lifecycle（原文落存，基本形态校验）
func (this *fakeGateway) putLifecycle(response http.ResponseWriter, request *http.Request, bucket string) {

	if !this.bucketExists(bucket) {
		this.writeError(response, http.StatusNotFound, "NoSuchBucket", "桶不存在", "/"+bucket)
		return
	}
	body, _ := io.ReadAll(request.Body)
	var parsed xmlLifecycleConfiguration
	if err := xml.Unmarshal(body, &parsed); err != nil || len(parsed.Rules) == 0 {
		this.writeError(response, http.StatusBadRequest, "MalformedXML", "生命周期配置不合法", "/"+bucket)
		return
	}
	this.lifecycleConfigs[bucket] = body
	response.WriteHeader(http.StatusOK)
}

// deleteLifecycle - DELETE /{bucket}?lifecycle
func (this *fakeGateway) deleteLifecycle(response http.ResponseWriter, bucket string) {

	delete(this.lifecycleConfigs, bucket)
	response.WriteHeader(http.StatusNoContent)
}

// ============================= 假网关 P2 处理器 - 结束 =============================

// ============================= P2 测试 - 开始 =============================

// mustCreateBucket - 测试助手：建桶（失败即终止）
func mustCreateBucket(t *testing.T, client *Client, ctx context.Context, bucket string) {

	t.Helper()
	if err := client.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("建桶失败：%v", err)
	}
}

// mustPutBytes - 测试助手：上传字节内容（失败即终止）
func mustPutBytes(t *testing.T, client *Client, ctx context.Context, bucket, key string, data []byte) {

	t.Helper()
	_, err := client.PutObject(ctx, PutInput{Bucket: bucket, Key: key, Body: bytes.NewReader(data)})
	if err != nil {
		t.Fatalf("上传 %s 失败：%v", key, err)
	}
}

// mustGetBytes - 测试助手：下载并读全（失败即终止）
func mustGetBytes(t *testing.T, client *Client, ctx context.Context, bucket, key string) []byte {

	t.Helper()
	result, err := client.GetObject(ctx, bucket, key, nil)
	if err != nil {
		t.Fatalf("下载 %s 失败：%v", key, err)
	}
	defer func() { _ = result.Body.Close() }()
	data, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("读取 %s 内容失败：%v", key, err)
	}
	return data
}

// TestDeleteObjects - 批量删除：成功逐键回传、不存在键幂等、单键失败不阻断、超千自动分批、参数校验
func TestDeleteObjects(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	mustPutBytes(t, client, ctx, "demo", "a.txt", []byte("aaa"))
	mustPutBytes(t, client, ctx, "demo", "b.txt", []byte("bbb"))

	// 成功 + 不存在键幂等（归 Deleted）
	result, err := client.DeleteObjects(ctx, "demo", []string{"a.txt", "b.txt", "missing.txt"})
	if err != nil {
		t.Fatalf("批量删除失败：%v", err)
	}
	if len(result.Deleted) != 3 || len(result.Errors) != 0 {
		t.Fatalf("删除结果不符合预期：%+v", result)
	}
	if _, exists := gateway.objects["demo"]["a.txt"]; exists {
		t.Fatalf("a.txt 应已删除")
	}

	// 单键失败不阻断，逐键回传错误
	result, err = client.DeleteObjects(ctx, "demo", []string{"ok.txt", "fail-1.txt"})
	if err != nil {
		t.Fatalf("批量删除（含失败键）失败：%v", err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "ok.txt" {
		t.Fatalf("成功键不符合预期：%+v", result.Deleted)
	}
	if len(result.Errors) != 1 || result.Errors[0].Key != "fail-1.txt" || result.Errors[0].Code != "InternalError" {
		t.Fatalf("失败键不符合预期：%+v", result.Errors)
	}

	// 超千自动分批（1001 键 → 两批）
	keys := make([]string, 0, maxDeleteKeys+1)
	for i := 0; i < maxDeleteKeys+1; i++ {
		keys = append(keys, fmt.Sprintf("batch/%04d.bin", i))
	}
	result, err = client.DeleteObjects(ctx, "demo", keys)
	if err != nil {
		t.Fatalf("超千分批删除失败：%v", err)
	}
	if len(result.Deleted) != maxDeleteKeys+1 || len(result.Errors) != 0 {
		t.Fatalf("分批结果不符合预期：deleted=%d errors=%d", len(result.Deleted), len(result.Errors))
	}

	// 参数校验
	if _, err = client.DeleteObjects(ctx, "", []string{"a"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应返回 ErrInvalidRequest：%v", err)
	}
	if _, err = client.DeleteObjects(ctx, "demo", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空键列表应返回 ErrInvalidRequest：%v", err)
	}
	if _, err = client.DeleteObjects(ctx, "demo", []string{"a", ""}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("含空键应返回 ErrInvalidRequest：%v", err)
	}
}

// TestCopyObject - 服务端拷贝：成功往返、源不存在归 NoSuchKey、参数校验
func TestCopyObject(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	content := []byte("hello-copy")
	mustPutBytes(t, client, ctx, "demo", "src/hello.txt", content)

	// 成功拷贝
	result, err := client.CopyObject(ctx, "demo", "dst/copied.txt", "demo", "src/hello.txt")
	if err != nil {
		t.Fatalf("拷贝失败：%v", err)
	}
	if result.ETag != md5hex(content) {
		t.Fatalf("拷贝 ETag 不符合预期：%s", result.ETag)
	}
	if !result.LastModified.Equal(fakeModTime) {
		t.Fatalf("拷贝时间不符合预期：%v", result.LastModified)
	}
	if result.VersionId != "null" {
		t.Fatalf("无版本控制时 VersionId 应为 null：%s", result.VersionId)
	}
	if got := mustGetBytes(t, client, ctx, "demo", "dst/copied.txt"); !bytes.Equal(got, content) {
		t.Fatalf("拷贝内容不一致：%q", got)
	}

	// 源不存在 → NoSuchKey（IsNotFound 判定）
	_, err = client.CopyObject(ctx, "demo", "dst/x.txt", "demo", "src/missing.txt")
	var s3Err *S3Error
	if !errors.As(err, &s3Err) || !IsNotFound(err) {
		t.Fatalf("源不存在应返回可判定的 NoSuchKey：%v", err)
	}

	// 参数校验
	if _, err = client.CopyObject(ctx, "", "k", "b", "s"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空参数应返回 ErrInvalidRequest：%v", err)
	}
	if _, err = client.CopyObject(ctx, "demo", "k", "", "s"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空源桶应返回 ErrInvalidRequest：%v", err)
	}
}

// TestListMultipartUploads - 进行中分片会话列表：全量、前缀过滤、参数校验
func TestListMultipartUploads(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	id1, err := client.CreateMultipartUpload(ctx, "demo", "k1.bin", "")
	if err != nil {
		t.Fatalf("初始化分片会话 1 失败：%v", err)
	}
	id2, err := client.CreateMultipartUpload(ctx, "demo", "dir/k2.bin", "")
	if err != nil {
		t.Fatalf("初始化分片会话 2 失败：%v", err)
	}

	uploads, err := client.ListMultipartUploads(ctx, "demo", "")
	if err != nil {
		t.Fatalf("列出分片会话失败：%v", err)
	}
	if len(uploads) != 2 {
		t.Fatalf("会话数不符合预期：%+v", uploads)
	}
	byId := map[string]MultipartUpload{}
	for _, item := range uploads {
		byId[item.UploadId] = item
	}
	if byId[id1].Key != "k1.bin" || byId[id2].Key != "dir/k2.bin" {
		t.Fatalf("会话内容不符合预期：%+v", uploads)
	}
	if !byId[id1].Initiated.Equal(fakeModTime) {
		t.Fatalf("会话初始化时间不符合预期：%v", byId[id1].Initiated)
	}

	// 前缀过滤
	uploads, err = client.ListMultipartUploads(ctx, "demo", "dir/")
	if err != nil {
		t.Fatalf("前缀过滤失败：%v", err)
	}
	if len(uploads) != 1 || uploads[0].UploadId != id2 {
		t.Fatalf("前缀过滤结果不符合预期：%+v", uploads)
	}

	// 参数校验
	if _, err = client.ListMultipartUploads(ctx, "", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空桶名应返回 ErrInvalidRequest：%v", err)
	}
}

// TestResumeUploadNoSession - 无进行中会话：ResumeUpload 等价全量分片上传
func TestResumeUploadNoSession(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	partSize := int64(minPartSize)
	data := buildData(2*partSize + 100)

	result, err := client.ResumeUpload(ctx, UploadInput{
		Bucket: "demo", Key: "big.bin", Body: bytes.NewReader(data), Size: int64(len(data)), PartSize: partSize,
	})
	if err != nil {
		t.Fatalf("ResumeUpload（无会话）失败：%v", err)
	}
	if !strings.HasSuffix(result.ETag, "-3") {
		t.Fatalf("应按 3 分片完成：%s", result.ETag)
	}
	if got := mustGetBytes(t, client, ctx, "demo", "big.bin"); !bytes.Equal(got, data) {
		t.Fatalf("内容不一致")
	}
}

// TestResumeUploadPartialSkip - 有进行中会话：已传分片跳过（不上网关），只补传缺失分片
func TestResumeUploadPartialSkip(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	partSize := int64(minPartSize)
	data := buildData(2*partSize + 100)

	// 手动初始化并上传 1、2 两片
	uploadId, err := client.CreateMultipartUpload(ctx, "demo", "big.bin", "")
	if err != nil {
		t.Fatalf("初始化分片会话失败：%v", err)
	}
	for number := 1; number <= 2; number++ {
		offset := int64(number-1) * partSize
		if _, err = client.UploadPart(ctx, "demo", "big.bin", uploadId, number, bytes.NewReader(data[offset:offset+partSize])); err != nil {
			t.Fatalf("手动上传分片 %d 失败：%v", number, err)
		}
	}
	if got := gateway.partLog[uploadId]; len(got) != 2 {
		t.Fatalf("手动阶段应上传 2 片：%v", got)
	}

	// 续传：只补传第 3 片（1、2 本地跳过）
	result, err := client.ResumeUpload(ctx, UploadInput{
		Bucket: "demo", Key: "big.bin", Body: bytes.NewReader(data), Size: int64(len(data)), PartSize: partSize,
	})
	if err != nil {
		t.Fatalf("ResumeUpload（续传）失败：%v", err)
	}
	if result.Size != int64(len(data)) {
		t.Fatalf("总字节数不符合预期：%d", result.Size)
	}
	got := append([]int{}, gateway.partLog[uploadId]...)
	sort.Ints(got)
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("续传应只补传第 3 片：%v", got)
	}
	if got := mustGetBytes(t, client, ctx, "demo", "big.bin"); !bytes.Equal(got, data) {
		t.Fatalf("续传完成内容不一致")
	}
}

// TestResumeUploadNonSeeker - 非 ReadSeeker 内容流：已传分片以读丢弃方式跳过
func TestResumeUploadNonSeeker(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	partSize := int64(minPartSize)
	data := buildData(partSize + 100)

	uploadId, err := client.CreateMultipartUpload(ctx, "demo", "big.bin", "")
	if err != nil {
		t.Fatalf("初始化分片会话失败：%v", err)
	}
	if _, err = client.UploadPart(ctx, "demo", "big.bin", uploadId, 1, bytes.NewReader(data[:partSize])); err != nil {
		t.Fatalf("手动上传分片 1 失败：%v", err)
	}

	// 非 Seeker 包装（只暴露 io.Reader）
	body := struct{ io.Reader }{Reader: bytes.NewReader(data)}
	if _, err = client.ResumeUpload(ctx, UploadInput{
		Bucket: "demo", Key: "big.bin", Body: body, Size: int64(len(data)), PartSize: partSize,
	}); err != nil {
		t.Fatalf("ResumeUpload（非 Seeker）失败：%v", err)
	}
	if got := mustGetBytes(t, client, ctx, "demo", "big.bin"); !bytes.Equal(got, data) {
		t.Fatalf("非 Seeker 续传内容不一致")
	}
}

// TestResumeUploadLayoutMismatch - 已传分片与本地布局不一致：ErrInvalidRequest，会话保留可治理
func TestResumeUploadLayoutMismatch(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	partSize := int64(minPartSize)
	data := buildData(2*partSize + 100)

	// 已传分片大小（100 字节）与布局期望（partSize）不一致
	uploadId, err := client.CreateMultipartUpload(ctx, "demo", "big.bin", "")
	if err != nil {
		t.Fatalf("初始化分片会话失败：%v", err)
	}
	if _, err = client.UploadPart(ctx, "demo", "big.bin", uploadId, 1, bytes.NewReader(data[:100])); err != nil {
		t.Fatalf("手动上传分片失败：%v", err)
	}

	_, err = client.ResumeUpload(ctx, UploadInput{
		Bucket: "demo", Key: "big.bin", Body: bytes.NewReader(data), Size: int64(len(data)), PartSize: partSize,
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("布局不一致应返回 ErrInvalidRequest：%v", err)
	}
	// 会话保留（未自动中止），可治理后重传
	if _, exists := gateway.uploads[uploadId]; !exists {
		t.Fatalf("布局不一致时应保留会话现场")
	}
}

// TestBucketCorsConfig - CORS 配置族：未配置 404、写入回读、删除、本地校验
func TestBucketCorsConfig(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	// 未配置 → NoSuchCORSConfiguration
	_, err := client.GetBucketCors(ctx, "demo")
	if !IsCode(err, "NoSuchCORSConfiguration") {
		t.Fatalf("未配置应返回 NoSuchCORSConfiguration：%v", err)
	}

	// 写入 → 回读一致
	rules := []CORSRule{
		{
			AllowedOrigins: []string{"https://example.com"},
			AllowedMethods: []string{"GET", "PUT"},
			AllowedHeaders: []string{"*"},
			ExposeHeaders:  []string{"ETag"},
			MaxAgeSeconds:  600,
		},
		{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET"},
		},
	}
	if err = client.PutBucketCors(ctx, "demo", rules); err != nil {
		t.Fatalf("写入 CORS 失败：%v", err)
	}
	got, err := client.GetBucketCors(ctx, "demo")
	if err != nil {
		t.Fatalf("回读 CORS 失败：%v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint(rules) {
		t.Fatalf("CORS 回读不一致：\n写入 %+v\n回读 %+v", rules, got)
	}

	// 本地校验
	if err = client.PutBucketCors(ctx, "demo", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空规则集应返回 ErrInvalidRequest：%v", err)
	}
	if err = client.PutBucketCors(ctx, "demo", []CORSRule{{AllowedMethods: []string{"GET"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺来源应返回 ErrInvalidRequest：%v", err)
	}
	if err = client.PutBucketCors(ctx, "demo", []CORSRule{{AllowedOrigins: []string{"*"}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺方法应返回 ErrInvalidRequest：%v", err)
	}

	// 删除 → 再读 404
	if err = client.DeleteBucketCors(ctx, "demo"); err != nil {
		t.Fatalf("删除 CORS 失败：%v", err)
	}
	if _, err = client.GetBucketCors(ctx, "demo"); !IsCode(err, "NoSuchCORSConfiguration") {
		t.Fatalf("删除后应回到未配置：%v", err)
	}
}

// TestBucketVersioningConfig - 版本控制：未开启空状态、开启/暂停往返、非法状态本地拒绝
func TestBucketVersioningConfig(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	// 未开启 → 空状态（S3 语义不返回 404）
	status, err := client.GetBucketVersioning(ctx, "demo")
	if err != nil || status != "" {
		t.Fatalf("未开启应返回空状态：status=%q err=%v", status, err)
	}

	// 非法状态本地拒绝
	if err = client.PutBucketVersioning(ctx, "demo", "Off"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("非法状态应返回 ErrInvalidRequest：%v", err)
	}

	// 开启 → 暂停
	if err = client.PutBucketVersioning(ctx, "demo", VersioningEnabled); err != nil {
		t.Fatalf("开启版本控制失败：%v", err)
	}
	if status, err = client.GetBucketVersioning(ctx, "demo"); err != nil || status != VersioningEnabled {
		t.Fatalf("开启后状态不符合预期：status=%q err=%v", status, err)
	}
	if err = client.PutBucketVersioning(ctx, "demo", VersioningSuspended); err != nil {
		t.Fatalf("暂停版本控制失败：%v", err)
	}
	if status, err = client.GetBucketVersioning(ctx, "demo"); err != nil || status != VersioningSuspended {
		t.Fatalf("暂停后状态不符合预期：status=%q err=%v", status, err)
	}
}

// TestBucketLifecycleConfig - 生命周期配置族：未配置 404、三动作写入回读、删除、本地校验
func TestBucketLifecycleConfig(t *testing.T) {

	gateway := newFakeGateway()
	client := newTestClient(t, gateway)
	ctx := context.Background()
	mustCreateBucket(t, client, ctx, "demo")

	// 未配置 → NoSuchLifecycleConfiguration
	_, err := client.GetBucketLifecycle(ctx, "demo")
	if !IsCode(err, "NoSuchLifecycleConfiguration") {
		t.Fatalf("未配置应返回 NoSuchLifecycleConfiguration：%v", err)
	}

	// 三动作写入 → 回读一致
	rules := []LifecycleRule{
		{
			ID:                  "archive-then-clean",
			Status:              "Enabled",
			Prefix:              "logs/",
			ExpirationDays:      30,
			NoncurrentDays:      7,
			AbortIncompleteDays: 3,
		},
	}
	if err = client.PutBucketLifecycle(ctx, "demo", rules); err != nil {
		t.Fatalf("写入生命周期失败：%v", err)
	}
	got, err := client.GetBucketLifecycle(ctx, "demo")
	if err != nil {
		t.Fatalf("回读生命周期失败：%v", err)
	}
	if fmt.Sprint(got) != fmt.Sprint(rules) {
		t.Fatalf("生命周期回读不一致：\n写入 %+v\n回读 %+v", rules, got)
	}

	// 本地校验
	if err = client.PutBucketLifecycle(ctx, "demo", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("空规则集应返回 ErrInvalidRequest：%v", err)
	}
	if err = client.PutBucketLifecycle(ctx, "demo", []LifecycleRule{{ID: "x", Status: "On", ExpirationDays: 1}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("非法状态应返回 ErrInvalidRequest：%v", err)
	}
	if err = client.PutBucketLifecycle(ctx, "demo", []LifecycleRule{{ID: "x", Status: "Enabled"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("无动作应返回 ErrInvalidRequest：%v", err)
	}

	// 删除 → 再读 404
	if err = client.DeleteBucketLifecycle(ctx, "demo"); err != nil {
		t.Fatalf("删除生命周期失败：%v", err)
	}
	if _, err = client.GetBucketLifecycle(ctx, "demo"); !IsCode(err, "NoSuchLifecycleConfiguration") {
		t.Fatalf("删除后应回到未配置：%v", err)
	}
}

// TestGetSessionToken - STS：默认/钳制有效期、响应解析、临时凭证全链路（签名带令牌头）、临时凭证禁再签发
func TestGetSessionToken(t *testing.T) {

	gateway := newFakeGateway()
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)

	ctx := context.Background()
	client, err := New(Config{Endpoint: server.URL, Region: "us-east-1", AccessKey: "AK", Secret: "SK"})
	if err != nil {
		t.Fatalf("装配客户端失败：%v", err)
	}

	// 默认有效期 3600
	creds, err := client.GetSessionToken(ctx, 0)
	if err != nil {
		t.Fatalf("GetSessionToken 失败：%v", err)
	}
	if gateway.stsDuration != 3600 {
		t.Fatalf("默认有效期应为 3600：%d", gateway.stsDuration)
	}
	if creds.AccessKey != "AK-SESSION" || creds.Secret != "SK-SESSION" || creds.Token != "TOKEN-123" {
		t.Fatalf("临时凭证不符合预期：%+v", creds)
	}
	if !creds.ExpireAt.Equal(fakeModTime.Add(time.Hour)) {
		t.Fatalf("过期时间不符合预期：%v", creds.ExpireAt)
	}

	// 钳制：100s → 900；100h → 43200
	if _, err = client.GetSessionToken(ctx, 100*time.Second); err != nil {
		t.Fatalf("GetSessionToken（钳制下限）失败：%v", err)
	}
	if gateway.stsDuration != 900 {
		t.Fatalf("应钳制到 900：%d", gateway.stsDuration)
	}
	if _, err = client.GetSessionToken(ctx, 100*time.Hour); err != nil {
		t.Fatalf("GetSessionToken（钳制上限）失败：%v", err)
	}
	if gateway.stsDuration != 43200 {
		t.Fatalf("应钳制到 43200：%d", gateway.stsDuration)
	}

	// 临时凭证全链路：网关登记会话凭据后，临时凭证签名请求验签通过（令牌头参与签名）
	gateway.sessionCreds = &credentials{accessKey: "AK-SESSION", secret: "SK-SESSION", sessionToken: "TOKEN-123"}
	session, err := New(Config{
		Endpoint: server.URL, Region: "us-east-1",
		AccessKey: creds.AccessKey, Secret: creds.Secret, SessionToken: creds.Token,
	})
	if err != nil {
		t.Fatalf("装配会话客户端失败：%v", err)
	}
	if err = session.CreateBucket(ctx, "session-bucket"); err != nil {
		t.Fatalf("临时凭证建桶失败：%v", err)
	}
	mustPutBytes(t, session, ctx, "session-bucket", "a.txt", []byte("session-data"))
	if got := mustGetBytes(t, session, ctx, "session-bucket", "a.txt"); string(got) != "session-data" {
		t.Fatalf("临时凭证读写内容不一致：%q", got)
	}

	// 临时凭证不可再签发（本地预检）
	if _, err = session.GetSessionToken(ctx, 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("临时凭证再签发应返回 ErrInvalidRequest：%v", err)
	}
}

// buildData - 生成确定性的分片测试数据（逐字节伪随机，免压缩伪影）
func buildData(size int64) []byte {

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31)
	}
	return data
}

// ============================= P2 测试 - 结束 =============================
