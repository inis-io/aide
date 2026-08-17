package storagex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// ================================== 腾讯云对象存储 - 开始 ==================================

// CosStore - 腾讯云对象存储驱动
type CosStore struct {
	// COS 客户端
	Client *cos.Client
	// 配置
	Config COSConfig
}

// newCosStore - 腾讯云对象存储驱动工厂
func newCosStore(config Config) (Store, error) {

	conf := config.COS
	if utils.Is.Empty(conf.AppId) || utils.Is.Empty(conf.SecretId) || utils.Is.Empty(conf.SecretKey) || utils.Is.Empty(conf.Bucket) {
		return nil, fmt.Errorf("storagex: COS 配置不完整（AppId / SecretId / SecretKey / Bucket 必填）")
	}

	cosUrl, err := url.Parse(fmt.Sprintf("https://%s-%s.cos.%s.myqcloud.com", conf.Bucket, conf.AppId, conf.Region))
	if err != nil {
		return nil, fmt.Errorf("storagex: COS 地址解析失败：%w", err)
	}

	client := cos.NewClient(&cos.BaseURL{BucketURL: cosUrl}, &http.Client{
		// 设置超时时间
		Timeout: 100 * time.Second,
		Transport: &cos.AuthorizationTransport{
			SecretID:  conf.SecretId,
			SecretKey: conf.SecretKey,
		},
	})

	return &CosStore{Client: client, Config: conf}, nil
}

// Root - 存储根目录名
func (this *CosStore) Root() string {
	return strings.Trim(this.Config.Path, "/")
}

// Domain - 访问域名（未配置则用存储桶默认域名）
func (this *CosStore) Domain() string {
	if !utils.Is.Empty(this.Config.Domain) {
		return strings.TrimSuffix(this.Config.Domain, "/")
	}
	return fmt.Sprintf("https://%s-%s.cos.%s.myqcloud.com", this.Config.Bucket, this.Config.AppId, this.Config.Region)
}

// Put - 上传对象（存储桶需预先创建，SDK 不自动建桶）
func (this *CosStore) Put(ctx context.Context, key string, reader io.Reader) (err error) {
	_, err = this.Client.Object.Put(ctx, this.key(key), reader, nil)
	return err
}

// List - 列出目录内容（云端 Marker 分页，目录在前文件在后）
func (this *CosStore) List(ctx context.Context, dir string, marker string, limit int) (entries []Entry, nextMarker string, err error) {

	// 目录前缀 - 如 AIDE/media/
	prefix := this.key(dir) + "/"
	result, _, err := this.Client.Bucket.Get(ctx, &cos.BucketGetOptions{
		Prefix:    prefix,
		Delimiter: "/",
		Marker:    marker,
		MaxKeys:   limit,
	})
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
	for _, item := range result.Contents {
		if item.Key == prefix || strings.HasSuffix(item.Key, "/") {
			continue
		}
		// 修改时间 - RFC3339 格式
		modTime, _ := time.Parse(time.RFC3339, item.LastModified)
		entries = append(entries, Entry{
			Name:    pathpkg.Base(item.Key),
			Size:    item.Size,
			ModTime: modTime.UnixMilli(),
		})
	}

	if result.IsTruncated {
		nextMarker = result.NextMarker
	}
	return entries, nextMarker, nil
}

// MakeDir - 创建目录（以 / 结尾的空对象占位）
func (this *CosStore) MakeDir(ctx context.Context, dir string) (err error) {
	_, err = this.Client.Object.Put(ctx, this.key(dir)+"/", strings.NewReader(""), nil)
	return err
}

// Remove - 删除文件或目录（目录递归删除）
func (this *CosStore) Remove(ctx context.Context, paths ...string) (err error) {
	for _, item := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		keys, err := this.collectKeys(ctx, this.key(item))
		if err != nil {
			return err
		}
		if err = deleteCosKeys(ctx, this.Client, keys); err != nil {
			return err
		}
	}
	return nil
}

// Move - 移动或重命名（逐对象复制后删除源对象）
func (this *CosStore) Move(ctx context.Context, src, dst string) (err error) {

	srcKey := this.key(src)
	dstKey := this.key(dst)
	keys, err := this.collectKeys(ctx, srcKey)
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
		// 复制源地址 - Key 需分段 URL 编码
		source := fmt.Sprintf("%s-%s.cos.%s.myqcloud.com/%s", this.Config.Bucket, this.Config.AppId, this.Config.Region, encodeUriKey(key))
		if _, _, err = this.Client.Object.Copy(ctx, dstKey+strings.TrimPrefix(key, srcKey), source, nil); err != nil {
			return err
		}
	}
	// 复制成功后删除源对象
	return deleteCosKeys(ctx, this.Client, keys)
}

// key - 相对路径转对象 Key
func (this *CosStore) key(rel string) string {
	rel = strings.Trim(rel, "/")
	if utils.Is.Empty(rel) {
		return this.Root()
	}
	return this.Root() + "/" + rel
}

// SignedURL - 签发 COS 预签名 URL（GET，expire 内有效，SDK 可直接下载）
func (this *CosStore) SignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {

	if err := ctx.Err(); err != nil {
		return "", err
	}
	url, err := this.Client.Object.GetPresignedURL(ctx, http.MethodGet, this.key(key),
		this.Config.SecretId, this.Config.SecretKey, expire, nil)
	if err != nil {
		return "", err
	}
	return url.String(), nil
}

// collectKeys - 收集指定 Key 对应的全部对象（文件返回自身，目录返回前缀下全部对象）
func (this *CosStore) collectKeys(ctx context.Context, key string) (keys []string, err error) {

	// 目录场景 - 前缀下全部对象
	marker := ""
	for {
		result, _, item := this.Client.Bucket.Get(ctx, &cos.BucketGetOptions{
			Prefix:  key + "/",
			Marker:  marker,
			MaxKeys: 1000,
		})
		if item != nil {
			return nil, item
		}
		for _, object := range result.Contents {
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
	exist, item := this.Client.Object.IsExist(ctx, key)
	if item != nil {
		return nil, item
	}
	if exist {
		keys = append(keys, key)
	}
	return keys, nil
}

// deleteCosKeys - 分批删除对象（单次最多 1000 个）
func deleteCosKeys(ctx context.Context, client *cos.Client, keys []string) error {
	for len(keys) > 0 {
		batch := keys
		if len(batch) > 1000 {
			batch = keys[:1000]
		}
		objects := make([]cos.Object, len(batch))
		for i, key := range batch {
			objects[i] = cos.Object{Key: key}
		}
		if _, _, err := client.Object.DeleteMulti(ctx, &cos.ObjectDeleteMultiOptions{
			Objects: objects,
			Quiet:   true,
		}); err != nil {
			return err
		}
		keys = keys[len(batch):]
	}
	return nil
}

// encodeUriKey - 对象 Key 分段 URL 编码（保留 / 分隔符）
func encodeUriKey(key string) string {
	parts := strings.Split(key, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// 编译期接口校验
var _ Store = (*CosStore)(nil)

var _ SignedURLer = (*CosStore)(nil)

// ================================== 腾讯云对象存储 - 结束 ==================================
