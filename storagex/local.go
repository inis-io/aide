package storagex

import (
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 本地存储 - 开始 ==================================

// multipartRoot - 分片临时区根目录名（存储根下，List 默认排除不对外暴露）
const multipartRoot = ".multipart"

// LocalStore - 本地磁盘存储驱动
type LocalStore struct {
	// 配置
	Config LocalConfig
}

// newLocalStore - 本地存储驱动工厂
func newLocalStore(config Config) (Store, error) {
	return &LocalStore{Config: config.Local}, nil
}

// Root - 存储根目录名（取根目录最后一段，如 public/storage → storage）
func (this *LocalStore) Root() string {
	return pathpkg.Base(pathpkg.Clean(strings.ReplaceAll(this.Config.Root, "\\", "/")))
}

// Domain - 访问域名
func (this *LocalStore) Domain() string {
	return strings.TrimSuffix(this.Config.Domain, "/")
}

// Put - 上传文件（自动创建父目录；临时文件 + rename 原子落盘，写入中断不留半截文件）
func (this *LocalStore) Put(ctx context.Context, key string, reader io.Reader) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}

	dest := this.fsPath(key)
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	temp := dest + ".tmp-" + utils.Rand.String(6)
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(file, reader); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return renameOverwrite(temp, dest)
}

// List - 列出目录内容（目录按名称排序在前，文件按修改时间倒序在后；内存分页，Marker 为偏移量；.multipart 临时区不对外暴露）
func (this *LocalStore) List(ctx context.Context, dir string, marker string, limit int) (entries []Entry, nextMarker string, err error) {

	if err = ctx.Err(); err != nil {
		return nil, "", err
	}

	items, err := os.ReadDir(this.fsPath(dir))
	if err != nil {
		// 目录不存在视为空目录（新用户尚未上传过文件的场景）
		if os.IsNotExist(err) {
			return []Entry{}, "", nil
		}
		return nil, "", err
	}

	// 收集条目 - 目录与文件分开，便于目录前置
	var dirs, files []Entry
	for _, item := range items {
		// 分片临时区不对外暴露
		if item.Name() == multipartRoot {
			continue
		}
		info, e := item.Info()
		if e != nil {
			continue
		}
		entry := Entry{
			Name:    item.Name(),
			IsDir:   item.IsDir(),
			ModTime: info.ModTime().UnixMilli(),
		}
		if item.IsDir() {
			dirs = append(dirs, entry)
			continue
		}
		entry.Size = info.Size()
		files = append(files, entry)
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })
	all := append(dirs, files...)

	// 内存分页 - Marker 为偏移量
	offset := cast.ToInt(marker)
	if offset < 0 {
		offset = 0
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	if end < len(all) {
		nextMarker = cast.ToString(end)
	}
	return all[offset:end], nextMarker, nil
}

// MakeDir - 创建目录
func (this *LocalStore) MakeDir(ctx context.Context, dir string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.MkdirAll(this.fsPath(dir), 0755)
}

// Remove - 删除文件或目录（目录递归删除）
func (this *LocalStore) Remove(ctx context.Context, paths ...string) (err error) {
	for _, item := range paths {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = os.RemoveAll(this.fsPath(item)); err != nil {
			return err
		}
	}
	return nil
}

// Move - 移动或重命名文件/目录（目标父目录不存在则自动创建）
func (this *LocalStore) Move(ctx context.Context, src, dst string) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(this.fsPath(dst)), 0755); err != nil {
		return err
	}
	return os.Rename(this.fsPath(src), this.fsPath(dst))
}

// fsPath - 相对路径转本地文件系统路径（key 已经 Driver 层穿越校验，按原名落盘）
func (this *LocalStore) fsPath(rel string) string {
	return filepath.Join(this.Config.Root, filepath.FromSlash(rel))
}

// 编译期接口校验
var _ Store = (*LocalStore)(nil)

// ================================== 本地存储 - 结束 ==================================

// ================================== 本地分片上传 - 开始 ==================================

// multipartMeta - 分片会话元信息（持久化在临时区 meta.json）
type multipartMeta struct {
	// 目标对象（相对存储根路径）
	Key string `json:"key"`
	// 创建时间（毫秒时间戳）
	CreateAt int64 `json:"createAt"`
}

// multipartDir - 分片临时区目录：{Root}/.multipart/{uploadId}；uploadId 含目录成分或 .. 视为非法
func (this *LocalStore) multipartDir(uploadId string) (string, error) {
	if utils.Is.Empty(uploadId) || strings.ContainsAny(uploadId, `/\`) || strings.Contains(uploadId, "..") {
		return "", fmt.Errorf("storagex: uploadId 不合法：%s", uploadId)
	}
	return filepath.Join(this.Config.Root, multipartRoot, uploadId), nil
}

// InitMultipart - 初始化分片会话：生成 uploadId（毫秒时间戳-随机后缀），建临时区目录并写入 meta.json
func (this *LocalStore) InitMultipart(ctx context.Context, key string) (uploadId string, err error) {

	if err = ctx.Err(); err != nil {
		return "", err
	}

	uploadId = cast.ToString(time.Now().UnixMilli()) + "-" + utils.Rand.String(6)
	dir, err := this.multipartDir(uploadId)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	meta := utils.Json.Encode(multipartMeta{Key: key, CreateAt: time.Now().UnixMilli()})
	if err = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0644); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return uploadId, nil
}

// UploadPart - 流式写入分片（tmp+rename 原子落盘；同 index 幂等覆盖；size >= 0 时校验实际字节数一致）
func (this *LocalStore) UploadPart(ctx context.Context, key, uploadId string, index int, reader io.Reader, size int64) (part Part, err error) {

	if err = ctx.Err(); err != nil {
		return part, err
	}
	if index < 1 {
		return part, fmt.Errorf("storagex: 分片序号从 1 开始，实际 %d", index)
	}
	dir, err := this.multipartDir(uploadId)
	if err != nil {
		return part, err
	}
	if info, e := os.Stat(dir); e != nil || !info.IsDir() {
		return part, fmt.Errorf("storagex: 分片会话不存在或已清理：%s", uploadId)
	}

	dest := filepath.Join(dir, cast.ToString(index)+".part")
	temp := dest + ".tmp-" + utils.Rand.String(6)
	file, err := os.Create(temp)
	if err != nil {
		return part, err
	}
	written, err := io.Copy(file, reader)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return part, err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(temp)
		return part, err
	}
	if size >= 0 && written != size {
		_ = os.Remove(temp)
		return part, fmt.Errorf("storagex: 分片 %d 实际字节数 %d 与声明 %d 不符", index, written, size)
	}
	if err = renameOverwrite(temp, dest); err != nil {
		return part, err
	}
	return Part{Index: index, ETag: "size:" + cast.ToString(written), Size: written}, nil
}

// ListParts - 扫描临时区目录，按序号升序组装已上传分片（ETag 为 "size:字节数" 摘要）
func (this *LocalStore) ListParts(ctx context.Context, key, uploadId string) (parts []Part, err error) {

	if err = ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := this.multipartDir(uploadId)
	if err != nil {
		return nil, err
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		// 临时区不存在视为无已传分片（会话尚未上传或已被清理）
		if os.IsNotExist(err) {
			return []Part{}, nil
		}
		return nil, err
	}

	parts = []Part{}
	for _, item := range items {
		name := item.Name()
		if item.IsDir() || !strings.HasSuffix(name, ".part") {
			continue
		}
		index := cast.ToInt(strings.TrimSuffix(name, ".part"))
		if index < 1 {
			continue
		}
		info, e := item.Info()
		if e != nil {
			return nil, e
		}
		parts = append(parts, Part{Index: index, ETag: "size:" + cast.ToString(info.Size()), Size: info.Size()})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	return parts, nil
}

// CompleteMultipart - 按序号升序流式拼接分片到 {dest}.tmp → fsync → rename 到正式位 → 清临时区
func (this *LocalStore) CompleteMultipart(ctx context.Context, key, uploadId string, parts []Part) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("storagex: 分片清单不能为空")
	}
	dir, err := this.multipartDir(uploadId)
	if err != nil {
		return err
	}
	if info, e := os.Stat(dir); e != nil || !info.IsDir() {
		return fmt.Errorf("storagex: 分片会话不存在或已清理：%s", uploadId)
	}

	// 统一按序号升序拼接（与云端驱动语义对齐；复制切片避免改动调用方数据）
	ordered := make([]Part, len(parts))
	copy(ordered, parts)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })

	dest := this.fsPath(key)
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	temp := dest + ".tmp-" + utils.Rand.String(6)
	file, err := os.Create(temp)
	if err != nil {
		return err
	}

	// 逐片流式拼接；失败仅清理合并临时文件，保留临时区供重试
	concat := func() error {
		for _, part := range ordered {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunk, err := os.Open(filepath.Join(dir, cast.ToString(part.Index)+".part"))
			if err != nil {
				return fmt.Errorf("storagex: 分片 %d 缺失：%w", part.Index, err)
			}
			_, err = io.Copy(file, chunk)
			_ = chunk.Close()
			if err != nil {
				return err
			}
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if concat != nil {
		_ = os.Remove(temp)
		return concat
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}

	if err = renameOverwrite(temp, dest); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// AbortMultipart - 中止会话：删除临时区目录（幂等，目录不存在不报错）
func (this *LocalStore) AbortMultipart(ctx context.Context, key, uploadId string) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}
	dir, err := this.multipartDir(uploadId)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Get - 读取对象内容（key 经 fsPath 换算；调用方负责关闭）
func (this *LocalStore) Get(ctx context.Context, key string) (reader io.ReadCloser, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return os.Open(this.fsPath(key))
}

// renameOverwrite - 临时文件原子改名到正式位；Windows 下 Rename 不允许覆盖已存在文件，删除目标后重试（对齐 cachex file.go 既有处理）
func renameOverwrite(temp, dest string) (err error) {
	if err = os.Rename(temp, dest); err != nil {
		_ = os.RemoveAll(dest)
		if err = os.Rename(temp, dest); err != nil {
			_ = os.RemoveAll(temp)
			return err
		}
	}
	return nil
}

// 编译期接口校验
var _ Multiparter = (*LocalStore)(nil)

var _ Getter = (*LocalStore)(nil)

// ================================== 本地分片上传 - 结束 ==================================
