package licence

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Store - 安全存储接口（activationToken / 客户端私钥 / 信封缓存）
// 默认实现为 AES-256-GCM 加密文件（权限 0600）；
// 需要系统密钥库（DPAPI/Keychain/keyring）时可注入自定义实现。
type Store interface {
	// Load - 读取密文状态并解密（不存在返回 nil, nil）
	Load() ([]byte, error)
	// Save - 加密并原子写入状态
	Save(data []byte) error
	// Clear - 清除状态
	Clear() error
}

// fileStore - 加密文件存储：AES-256-GCM，密钥派生自 项目盐 + 实例指纹
type fileStore struct {
	// path - 状态文件路径
	path string
	// key - 派生密钥（32 字节）
	key []byte
}

// newFileStore - 创建加密文件存储
// 文件名按许可证编号隔离：licence-{licenseNo}.state；
// 密钥 = sha256("licen-hub/licence-store/v1|" + salt + "|" + fingerprint)，不出内存。
func newFileStore(dir string, licenseNo string, salt string, fingerprint string) (*fileStore, error) {

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte("licen-hub/licence-store/v1|" + salt + "|" + fingerprint))
	name := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(licenseNo)
	return &fileStore{path: filepath.Join(dir, "licence-"+name+".state"), key: sum[:]}, nil
}

// Load - 读取并解密状态（文件不存在返回 nil, nil）
func (this *fileStore) Load() ([]byte, error) {

	raw, err := os.ReadFile(this.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(this.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("状态文件损坏：长度不足")
	}
	return gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
}

// Save - 加密并原子写入状态（临时文件 + 重命名，权限 0600）
func (this *fileStore) Save(data []byte) error {

	block, err := aes.NewCipher(this.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	raw := gcm.Seal(nonce, nonce, data, nil)

	tmp := this.path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, this.path)
}

// Clear - 删除状态文件（不存在不视为错误）
func (this *fileStore) Clear() error {

	if err := os.Remove(this.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
