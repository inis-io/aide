// Command fullchain - ios SDK 全链联调 demo
//
// 覆盖设计方案 §13 P1 验收链：「建桶 → 上传（小 + 大文件分片）→ 列 / 下 / 删 → 分享 → 用量 → 回收站还原 / 释放」，
// 并演示 P2 能力：批量删除、服务端拷贝、断点续传、STS 临时凭证。
//
// 用法（Windows PowerShell）：
//
//	$env:IOS_ENDPOINT="https://oss.example.com"
//	$env:IOS_ACCESS_KEY="你的 AccessKey"
//	$env:IOS_SECRET="你的 Secret"
//	go run ./examples/fullchain
//
// 可选环境变量：
//
//	IOS_S3_ENDPOINT - 数据面 S3 网关地址（缺省从控制面端点派生 :9000）
//	IOS_TRANSPORT   - 控制面链路（grpc 默认 / http）
package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/inis-io/aide/ios"
)

const (
	// demoSmallSize - 小对象大小（1KB，走单 PUT）
	demoSmallSize = 1 << 10
	// demoBigSize - 大对象大小（20MB，走分片编排）
	demoBigSize = 20 << 20
)

// main - 全链步骤编排（任一步失败即终止；桶级清理挂 defer 保证现场可回收）
func main() {

	log.SetFlags(0)

	endpoint := os.Getenv("IOS_ENDPOINT")
	accessKey := os.Getenv("IOS_ACCESS_KEY")
	secret := os.Getenv("IOS_SECRET")
	if endpoint == "" || accessKey == "" || secret == "" {
		log.Fatal("请先设置环境变量 IOS_ENDPOINT / IOS_ACCESS_KEY / IOS_SECRET")
	}

	options := []ios.Option{ios.WithEndpoint(endpoint)}
	if s3Endpoint := os.Getenv("IOS_S3_ENDPOINT"); s3Endpoint != "" {
		options = append(options, ios.WithS3Endpoint(s3Endpoint))
	}
	if os.Getenv("IOS_TRANSPORT") == "grpc" {
		options = append(options, ios.WithTransportGRPC())
	}

	client, err := ios.NewClient(accessKey, secret, options...)
	if err != nil {
		log.Fatalf("构造客户端失败：%v", err)
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	bucket := fmt.Sprintf("ios-fullchain-%d", time.Now().Unix())

	// ① 凭据自检（探针）
	info, err := client.VerifyCredentials(ctx)
	if err != nil {
		log.Fatalf("凭据验证失败：%v", err)
	}
	step("凭据有效：密钥 %s（scope=%s）", info.Name, info.Scope)

	// ② 建桶（演示桶独占，结束清理）
	must(client.CreateBucket(ctx, bucket), "建桶")
	step("建桶完成：%s", bucket)
	defer cleanup(client, bucket)

	// ③ 小对象上传（单 PUT）
	small := pattern(demoSmallSize)
	put, err := client.PutObject(ctx, &ios.PutRequest{
		Bucket: bucket, Key: "small/hello.txt", Body: bytes.NewReader(small), Mime: "text/plain",
	})
	must(err, "小对象上传")
	step("小对象上传完成：ETag=%s Size=%d", put.ETag, put.Size)

	// ④ 大对象上传（20MB，按 8MB 阈值自动分片并发）
	big := pattern(demoBigSize)
	upload, err := client.Upload(ctx, &ios.UploadRequest{
		Bucket: bucket, Key: "big/payload.bin", Body: bytes.NewReader(big), Size: int64(len(big)),
	})
	must(err, "大对象分片上传")
	step("大对象上传完成：ETag=%s Size=%d（分片形态）", upload.ETag, upload.Size)

	// ⑤ 断点续传（无进行中会话时等价 Upload；进程中断后重进即自动复用会话跳过重传）
	resume := pattern(demoBigSize)
	resumed, err := client.ResumeUpload(ctx, &ios.UploadRequest{
		Bucket: bucket, Key: "big/resume.bin", Body: bytes.NewReader(resume), Size: int64(len(resume)),
	})
	must(err, "断点续传")
	step("断点续传完成：ETag=%s Size=%d", resumed.ETag, resumed.Size)

	// 遗留分片会话巡检（成功上传后应为空；异常中断的会话在此发现并可 Abort）
	pending, err := client.ListMultipartUploads(ctx, bucket, "")
	must(err, "分片会话巡检")
	step("进行中分片会话：%d 个", len(pending))

	// ⑥ 列举与下载（含 Range 与完整性回核）
	list, err := client.ListObjects(ctx, &ios.ListRequest{Bucket: bucket, Delimiter: "/"})
	must(err, "列举对象")
	step("列举完成：%d 个条目，目录 %v", len(list.Entries), list.Prefixes)

	object, err := client.GetObject(ctx, bucket, "big/payload.bin", nil)
	must(err, "下载大对象")
	downloaded, err := io.ReadAll(object.Body)
	_ = object.Body.Close()
	must(err, "读取大对象内容")
	if md5hex(downloaded) != md5hex(big) {
		log.Fatal("下载内容与大对象原文不一致")
	}
	step("下载完成：%d 字节，MD5 回核一致", len(downloaded))

	// ⑦ 服务端拷贝（P2，零流量回传）
	copied, err := client.CopyObject(ctx, bucket, "copy/payload-copy.bin", bucket, "big/payload.bin")
	must(err, "服务端拷贝")
	step("服务端拷贝完成：ETag=%s", copied.ETag)

	// ⑧ 批量删除（P2；软删进回收站）
	removed, err := client.DeleteObjects(ctx, bucket, []string{"small/hello.txt", "copy/payload-copy.bin"})
	must(err, "批量删除")
	step("批量删除完成：成功 %d 键，失败 %d 键", len(removed.Deleted), len(removed.Errors))

	// ⑨ 分享链接（控制面；返回令牌，访问链接由平台站点域名 + 令牌组装，如 https://<站点>/share/<token>）
	share, err := client.CreateShare(ctx, &ios.ShareCreateRequest{
		Bucket: bucket, Key: "big/payload.bin", Expires: 24 * 3600,
	})
	must(err, "签发分享链接")
	step("分享令牌（24 小时）：%s", share.Share.Token)

	// ⑩ 用量总览（控制面）
	overview, err := client.GetUsageOverview(ctx)
	must(err, "用量总览")
	step("用量总览：存储 %d 字节 / 对象 %d / 桶 %d / 出口流量 %d 字节",
		overview.StorageBytes, overview.ObjectCount, overview.BucketCount, overview.EgressBytes)

	// ⑪ 回收站：列出 → 还原 → 再删 → 释放（控制面）
	recycle, err := client.ListRecycle(ctx, &ios.RecycleListRequest{Bucket: bucket})
	must(err, "回收站列表")
	step("回收站条目：%d（总量 %d）", len(recycle.Entries), recycle.Total)

	ids := make([]int64, 0, len(recycle.Entries))
	for _, entry := range recycle.Entries {
		ids = append(ids, entry.Id)
	}
	if len(ids) > 0 {
		restored, err := client.RestoreObjects(ctx, ids)
		must(err, "回收站还原")
		step("回收站还原：%d 条", restored.Restored)

		// 再删一次回到回收站，随后彻底释放
		_, err = client.DeleteObjects(ctx, bucket, []string{"small/hello.txt", "copy/payload-copy.bin"})
		must(err, "再删对象")
		recycle, err = client.ListRecycle(ctx, &ios.RecycleListRequest{Bucket: bucket})
		must(err, "回收站再列表")
		ids = ids[:0]
		for _, entry := range recycle.Entries {
			ids = append(ids, entry.Id)
		}
		purged, err := client.PurgeObjects(ctx, ids)
		must(err, "回收站释放")
		step("回收站释放：%d 条（不可恢复）", purged.Purged)
	}

	// ⑫ STS 临时凭证（P2；仅数据面可用）
	creds, err := client.GetSessionToken(ctx, time.Hour)
	must(err, "签发临时凭证")
	sessionOptions := append([]ios.Option{}, options...)
	temp, err := ios.NewClient(creds.AccessKey, creds.Secret, append(sessionOptions, ios.WithSessionToken(creds.Token))...)
	must(err, "构造临时凭证客户端")
	defer func() { _ = temp.Close() }()
	buckets, err := temp.ListBuckets(ctx)
	must(err, "临时凭证列桶")
	step("临时凭证（%s 过期）数据面可用：当前 %d 个桶", creds.ExpireAt.Format(time.RFC3339), len(buckets))

	step("全链 demo 完成 ✅")
}

// step - 步骤输出（统一前缀便于观察执行序列）
func step(format string, args ...any) {

	if len(args) == 0 {
		fmt.Printf("==> %s\n", format)
		return
	}
	fmt.Printf("==> %s\n", fmt.Sprintf(format, args...))
}

// must - 步骤守卫（失败即终止）
func must(err error, name string) {

	if err != nil {
		log.Fatalf("%s 失败：%v", name, err)
	}
}

// cleanup - 演示桶清理（清空对象后删桶；失败只告警不中断）
func cleanup(client *ios.Client, bucket string) {

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := client.EmptyBucket(ctx, bucket); err != nil {
		log.Printf("清桶失败（请手动清理 %s）：%v", bucket, err)
		return
	}
	if err := client.DeleteBucket(ctx, bucket); err != nil {
		log.Printf("删桶失败（请手动清理 %s）：%v", bucket, err)
		return
	}
	step("演示桶已清理：%s", bucket)
}

// pattern - 生成确定性字节序列（避免随机源，多次运行结果可比对）
func pattern(size int) []byte {

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31)
	}
	return data
}

// md5hex - MD5 十六进制
func md5hex(data []byte) string {

	sum := md5.Sum(data)
	return fmt.Sprintf("%x", sum)
}
