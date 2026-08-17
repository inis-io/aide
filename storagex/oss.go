package storagex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	pathpkg "path"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/inis-io/aide/utils"
)

// ================================== 阿里云对象存储 - 开始 ==================================

// OssStore - 阿里云对象存储驱动
type OssStore struct {
	// OSS 客户端
	Client *oss.Client
	// 配置
	Config OSSConfig
}

// newOssStore - 阿里云对象存储驱动工厂
func newOssStore(config Config) (Store, error) {

	conf := config.OSS
	if utils.Is.Empty(conf.AccessKeyId) || utils.Is.Empty(conf.AccessKeySecret) || utils.Is.Empty(conf.Bucket) {
		return nil, fmt.Errorf("storagex: OSS 配置不完整（AccessKeyId / AccessKeySecret / Bucket 必填）")
	}

	client, err := oss.New(conf.Endpoint, conf.AccessKeyId, conf.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("storagex: OSS 客户端初始化失败：%w", err)
	}

	return &OssStore{Client: client, Config: conf}, nil
}

// Root - 存储根目录名
func (this *OssStore) Root() string {
	return strings.Trim(this.Config.Path, "/")
}

// Domain - 访问域名（未配置则用存储桶外网默认域名）
func (this *OssStore) Domain() string {
	if !utils.Is.Empty(this.Config.Domain) {
		return strings.TrimSuffix(this.Config.Domain, "/")
	}
	return "https://" + this.Config.Bucket + "." + this.Config.Endpoint
}

// Put - 上传对象（存储桶需预先创建，SDK 不自动建桶）
func (this *OssStore) Put(ctx context.Context, key string, reader io.Reader) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}

	bucket, err := this.bucket()
	if err != nil {
		return err
	}
	return bucket.PutObject(this.key(key), reader)
}

// List - 列出目录内容（云端 Marker 分页，目录在前文件在后）
func (this *OssStore) List(ctx context.Context, dir string, marker string, limit int) (entries []Entry, nextMarker string, err error) {

	if err = ctx.Err(); err != nil {
		return nil, "", err
	}

	bucket, err := this.bucket()
	if err != nil {
		return nil, "", err
	}

	// 目录前缀 - 如 AIDE/media/
	prefix := this.key(dir) + "/"
	options := []oss.Option{
		oss.Prefix(prefix),
		oss.Delimiter("/"),
		oss.MaxKeys(limit),
	}
	if !utils.Is.Empty(marker) {
		options = append(options, oss.Marker(marker))
	}

	result, err := bucket.ListObjects(options...)
	if err != nil {
		return nil, "", err
	}

	entries = []Entry{}
	// 目录 - CommonPrefixes 形如 AIDE/media/users/
	for _, item := range result.CommonPrefixes {
		entries = append(entries, Entry{
			Name:  pathpkg.Base(strings.TrimSuffix(item, "/")),
			IsDir: true,
		})
	}
	// 文件 - 跳过目录占位对象
	for _, item := range result.Objects {
		if item.Key == prefix || strings.HasSuffix(item.Key, "/") {
			continue
		}
		entries = append(entries, Entry{
			Name:    pathpkg.Base(item.Key),
			Size:    item.Size,
			ModTime: item.LastModified.UnixMilli(),
		})
	}

	if result.IsTruncated {
		nextMarker = result.NextMarker
	}
	return entries, nextMarker, nil
}

// MakeDir - 创建目录（以 / 结尾的空对象占位）
func (this *OssStore) MakeDir(ctx context.Context, dir string) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}

	bucket, err := this.bucket()
	if err != nil {
		return err
	}
	return bucket.PutObject(this.key(dir)+"/", bytes.NewReader([]byte{}))
}

// Remove - 删除文件或目录（目录递归删除）
func (this *OssStore) Remove(ctx context.Context, paths ...string) (err error) {

	bucket, err := this.bucket()
	if err != nil {
		return err
	}

	for _, item := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		keys, err := this.collectKeys(bucket, this.key(item))
		if err != nil {
			return err
		}
		if err = deleteOssKeys(bucket, keys); err != nil {
			return err
		}
	}
	return nil
}

// Move - 移动或重命名（逐对象复制后删除源对象）
func (this *OssStore) Move(ctx context.Context, src, dst string) (err error) {

	bucket, err := this.bucket()
	if err != nil {
		return err
	}

	srcKey := this.key(src)
	dstKey := this.key(dst)
	keys, err := this.collectKeys(bucket, srcKey)
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return fmt.Errorf("storagex: 源路径不存在")
	}

	// 逐对象复制到新路径
	for _, key := range keys {
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = bucket.CopyObject(key, dstKey+strings.TrimPrefix(key, srcKey)); err != nil {
			return err
		}
	}
	// 复制成功后删除源对象
	return deleteOssKeys(bucket, keys)
}

// bucket - 取存储桶句柄（不探测、不创建，bucket 需预先存在）
func (this *OssStore) bucket() (*oss.Bucket, error) {
	if this.Client == nil {
		return nil, fmt.Errorf("storagex: OSS 客户端未初始化")
	}
	return this.Client.Bucket(this.Config.Bucket)
}

// key - 相对路径转对象 Key
func (this *OssStore) key(rel string) string {
	rel = strings.Trim(rel, "/")
	if utils.Is.Empty(rel) {
		return this.Root()
	}
	return this.Root() + "/" + rel
}

// SignedURL - 签发 OSS 预签名 URL（GET，expire 内有效，SDK 可直接下载）
func (this *OssStore) SignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {

	if err := ctx.Err(); err != nil {
		return "", err
	}
	bucket, err := this.bucket()
	if err != nil {
		return "", err
	}
	seconds := int64(expire / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	return bucket.SignURL(this.key(key), oss.HTTPGet, seconds)
}

// collectKeys - 收集指定 Key 对应的全部对象（文件返回自身，目录返回前缀下全部对象）
func (this *OssStore) collectKeys(bucket *oss.Bucket, key string) (keys []string, err error) {

	// 目录场景 - 前缀下全部对象
	marker := ""
	for {
		result, item := bucket.ListObjects(oss.Prefix(key+"/"), oss.Marker(marker), oss.MaxKeys(1000))
		if item != nil {
			return nil, item
		}
		for _, object := range result.Objects {
			keys = append(keys, object.Key)
		}
		if !result.IsTruncated {
			break
		}
		marker = result.NextMarker
	}
	if len(keys) > 0 {
		return keys, nil
	}

	// 单文件场景
	exist, item := bucket.IsObjectExist(key)
	if item != nil {
		return nil, item
	}
	if exist {
		keys = append(keys, key)
	}
	return keys, nil
}

// deleteOssKeys - 分批删除对象（单次最多 1000 个）
func deleteOssKeys(bucket *oss.Bucket, keys []string) error {
	for len(keys) > 0 {
		batch := keys
		if len(batch) > 1000 {
			batch = keys[:1000]
		}
		if _, err := bucket.DeleteObjects(batch, oss.DeleteObjectsQuiet(true)); err != nil {
			return err
		}
		keys = keys[len(batch):]
	}
	return nil
}

// 编译期接口校验
var _ Store = (*OssStore)(nil)

var _ SignedURLer = (*OssStore)(nil)

// ================================== 阿里云对象存储 - 结束 ==================================
