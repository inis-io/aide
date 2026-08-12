package cachex

import (
	"fmt"
	"path"
	"sync"
	"time"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/afero"
	"github.com/spf13/cast"
)

// ================================== 文件缓存 - 开始 ==================================

// FileStore - 文件缓存驱动（基于 afero，可注入内存文件系统便于测试）
type FileStore struct {
	// 文件系统对象
	Fs afero.Fs
	// 配置
	Config FileConfig
	// 原子方法（Incr/SetNX）的进程内互斥锁：读-改-写串行化，文件缓存本就是单机兜底场景
	mu sync.Mutex
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

	data := utils.Json.Encode(fileBody{Expired: this.expiredAt(expired), Value: value})
	return this.write(this.dest(key), []byte(data)) == nil
}

// Incr - 原子自增 1（读-改-写在互斥锁内串行；仅当自增结果为 1 时写入过期时间）
func (this *FileStore) Incr(key string, expired time.Duration) (count int64, err error) {

	this.mu.Lock()
	defer this.mu.Unlock()

	dest := this.dest(key)
	row, readErr := this.read(dest)

	// 已存在且未过期：保留原过期时间戳，仅累加计数
	if readErr == nil && row.Expired >= time.Now().Unix() {
		count = cast.ToInt64(row.Value) + 1
		row.Value = count
	} else {
		// 不存在或已过期：从 1 重新计数并写入过期时间
		count = 1
		row = fileBody{Expired: this.expiredAt(expired), Value: count}
	}

	if err = this.write(dest, []byte(utils.Json.Encode(row))); err != nil {
		return 0, err
	}
	return count, nil
}

// SetNX - 仅当键不存在时设置（已存在不覆盖、不续期）
func (this *FileStore) SetNX(key string, value any, expired time.Duration) (ok bool, err error) {

	this.mu.Lock()
	defer this.mu.Unlock()

	if this.get(key) != nil {
		return false, nil
	}
	if !this.Set(key, value, expired) {
		return false, fmt.Errorf("cachex: SetNX 写入失败")
	}
	return true, nil
}

// TTL - 剩余存活秒数（>0 有效；0 = 不存在或已过期；-1 = 存在但永不过期）
func (this *FileStore) TTL(key string) (seconds int64, err error) {

	row, readErr := this.read(this.dest(key))
	if readErr != nil {
		return 0, nil
	}
	// 永不过期按一百年写入，回读时还原为 -1 语义
	if row.Expired >= time.Now().AddDate(99, 0, 0).Unix() {
		return -1, nil
	}
	if seconds = row.Expired - time.Now().Unix(); seconds < 0 {
		seconds = 0
	}
	return seconds, nil
}

// expiredAt - 过期时间戳（expired <= 0 表示永不过期，按一百年计）
func (this *FileStore) expiredAt(expired time.Duration) int64 {
	at := time.Now().AddDate(100, 0, 0)
	if expired > 0 {
		at = time.Now().Add(expired)
	}
	return at.Unix()
}

// write - 写入缓存文件：先写临时文件再改名，避免写入中途崩溃留下半个文件
func (this *FileStore) write(dest string, data []byte) (err error) {

	temp := dest + ".tmp"
	if err = afero.WriteFile(this.Fs, temp, data, 0755); err != nil {
		return err
	}
	if err = this.Fs.Rename(temp, dest); err != nil {
		// Windows 下 Rename 不允许覆盖已存在文件，删除目标后重试
		_ = this.Fs.RemoveAll(dest)
		if err = this.Fs.Rename(temp, dest); err != nil {
			_ = this.Fs.RemoveAll(temp)
			return err
		}
	}
	return nil
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
