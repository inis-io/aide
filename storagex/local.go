package storagex

import (
	"context"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cast"
)

// ================================== 本地存储 - 开始 ==================================

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

// Put - 上传文件（自动创建父目录）
func (this *LocalStore) Put(ctx context.Context, key string, reader io.Reader) (err error) {

	if err = ctx.Err(); err != nil {
		return err
	}

	dest := this.fsPath(key)
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}

	file, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(file, reader)
	return err
}

// List - 列出目录内容（目录按名称排序在前，文件按修改时间倒序在后；内存分页，Marker 为偏移量）
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
