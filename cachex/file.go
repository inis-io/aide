package cachex

import (
	"path"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/afero"
)

// ================================== 文件缓存 - 开始 ==================================

// FileStore - 文件缓存驱动（基于 afero，可注入内存文件系统便于测试）
type FileStore struct {
	// 文件系统对象
	Fs afero.Fs
	// 配置
	Config FileConfig
}

// newFileStore - 文件缓存驱动工厂
func newFileStore(config Config) (Store, error) {
	return &FileStore{
		Fs:     afero.NewOsFs(),
		Config: config.File,
	}, nil
}

// fileBody - 文件缓存内容结构
type fileBody struct {
	// 过期时间戳
	Expired int64 `json:"expired"`
	// 缓存值
	Value any `json:"value"`
}

// Has - 判断缓存是否存在（过期视为不存在）
func (this *FileStore) Has(key string) (ok bool) {
	return this.get(key) != nil
}

// Get - 获取缓存（未命中或已过期返回 nil）
func (this *FileStore) Get(key string) (value any) {
	return this.get(key)
}

// Set - 设置缓存（expired <= 0 表示永不过期；临时文件 + 改名原子写入）
func (this *FileStore) Set(key string, value any, expired time.Duration) (ok bool) {

	// 创建存储目录
	_ = this.Fs.MkdirAll(this.Config.Root, 0755)

	// 过期时间戳，永不过期按一百年计
	at := time.Now().AddDate(100, 0, 0)
	if expired > 0 {
		at = time.Now().Add(expired)
	}

	data := utils.Json.Encode(fileBody{Expired: at.Unix(), Value: value})

	// 先写临时文件再改名，避免写入中途崩溃留下半个文件
	dest := this.dest(key)
	temp := dest + ".tmp"
	if err := afero.WriteFile(this.Fs, temp, []byte(data), 0755); err != nil {
		return false
	}
	if err := this.Fs.Rename(temp, dest); err != nil {
		// Windows 下 Rename 不允许覆盖已存在文件，删除目标后重试
		_ = this.Fs.RemoveAll(dest)
		if err = this.Fs.Rename(temp, dest); err != nil {
			_ = this.Fs.RemoveAll(temp)
			return false
		}
	}

	return true
}

// Delete - 删除缓存
func (this *FileStore) Delete(key ...string) (ok bool) {
	for _, item := range key {
		_ = this.Fs.RemoveAll(this.dest(item))
	}
	return true
}

// Clear - 清空缓存
func (this *FileStore) Clear() (ok bool) {
	if err := this.Fs.RemoveAll(this.Config.Root); err != nil {
		return false
	}
	// 重建目录
	_ = this.Fs.MkdirAll(this.Config.Root, 0755)
	return true
}

// dest - 缓存文件路径（存储目录/键名.后缀）
func (this *FileStore) dest(key string) string {
	return path.Join(this.Config.Root, key+"."+this.Config.Suffix)
}

// get - 读取缓存值：文件不存在、解析失败或已过期返回 nil（过期文件顺带删除）
func (this *FileStore) get(key string) any {

	dest := this.dest(key)

	row, err := this.read(dest)
	if err != nil {
		return nil
	}

	if row.Expired < time.Now().Unix() {
		_ = this.Fs.RemoveAll(dest)
		return nil
	}

	return row.Value
}

// read - 读取并解析缓存文件
func (this *FileStore) read(dest string) (row fileBody, err error) {
	data, err := afero.ReadFile(this.Fs, dest)
	if err != nil {
		return row, err
	}
	if err := utils.Json.Unmarshal(data, &row); err != nil {
		return row, err
	}
	return row, nil
}

// 编译期接口校验
var _ Store = (*FileStore)(nil)

// ================================== 文件缓存 - 结束 ==================================
