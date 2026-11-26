package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// ============================= 二进制完整性自校验（anti-tamper） =============================
//
// 背景：Ed25519 + keyset 只保证「授权数据不可伪造」，管不住攻击者直接 patch 客户端二进制里
// 的门禁函数（特征开关/配额判定）。本模块提供「执行层完整性」锚点：
//   - 平台在 CheckUpdate 响应附带「客户端上报版本对应的已发布版本签名清单」currentManifest，
//     清单内发布物 SHA-256 由 release-key 双层签名（清单签名 + 发布物签名），攻击者无法伪造；
//   - SDK 对运行中二进制文件计算启动锚点哈希，与清单中匹配 osArch 的 full 发布物哈希比对。
//
// 语义约定：
//   - 锚点哈希每个进程只算一次（首次评估时），进程生命周期内不再重算——在线更新换包后、
//     重启前的窗口期不会误判（换包后旧进程仍按旧锚点比对旧版本清单，重启后新进程锚定新包）；
//   - 无锚可比对（平台未下发 currentManifest / 清单无匹配发布物 / 自哈希失败）一律 Unknown
//     并 fail-open，绝不误伤开发构建与未发布版本；
//   - 只有「有锚且确定不一致」才置 Violation，由业务侧决定是否回落免费版（fail-closed）。

// BinaryIntegrityState - 二进制完整性状态机
type BinaryIntegrityState int32

const (
	// BinaryIntegrityUnknown - 未评估或无锚（不拦截）
	BinaryIntegrityUnknown BinaryIntegrityState = iota
	// BinaryIntegrityOK - 自哈希与平台签名发布物一致
	BinaryIntegrityOK
	// BinaryIntegrityViolation - 有锚且不一致：二进制疑似被篡改
	BinaryIntegrityViolation
)

// String - 状态可读化（日志/诊断）
func (this BinaryIntegrityState) String() string {

	switch this {
	case BinaryIntegrityOK:
		return "ok"
	case BinaryIntegrityViolation:
		return "violation"
	default:
		return "unknown"
	}
}

// integrityAnchor - 启动锚点（进程内只算一次）
type integrityAnchor struct {
	once sync.Once
	hash string
	err  error
}

// selfBinaryHash - 运行中二进制文件 SHA-256（懒加载启动锚点；os.Executable + 符号链接解析）
func (this *integrityAnchor) selfBinaryHash() (string, error) {

	this.once.Do(func() {
		path, err := os.Executable()
		if err != nil {
			this.err = err
			return
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		file, err := os.Open(path)
		if err != nil {
			this.err = err
			return
		}
		defer func() { _ = file.Close() }()
		hasher := sha256.New()
		if _, err = io.Copy(hasher, file); err != nil {
			this.err = err
			return
		}
		this.hash = hex.EncodeToString(hasher.Sum(nil))
	})
	return this.hash, this.err
}

// integrityState - 当前完整性状态（atomic 读写，门禁热路径零锁）
//
//nolint:errname // 状态载体而非错误
type integrityState struct {
	state    atomic.Int32
	mu       sync.RWMutex
	expected string // 平台签名清单中的发布物哈希（最近一次比对）
	actual   string // 本机启动锚点哈希
}

// snapshot - 状态快照（state + 比对双方哈希）
func (this *integrityState) snapshot() (BinaryIntegrityState, string, string) {

	this.mu.RLock()
	defer this.mu.RUnlock()
	return BinaryIntegrityState(this.state.Load()), this.expected, this.actual
}

// BinaryIntegrity - 当前二进制完整性状态与最近一次比对双方哈希（expected=平台签名值，actual=本机锚点）。
// 业务侧建议：state == BinaryIntegrityViolation 时按无授权/免费版回落（fail-closed），其余状态不拦截。
/**
 * @example：
 * 	if state, _, _ := client.BinaryIntegrity(); state == licence.BinaryIntegrityViolation {
 * 		// 二进制疑似被 patch：按免费版回落
 * 	}
 */
func (this *Client) BinaryIntegrity() (BinaryIntegrityState, string, string) {

	return this.integrity.snapshot()
}

// evaluateBinaryIntegrity - 依据平台签名的当前版本清单评估完整性（CheckUpdate 内部调用）。
// 锚选择：优先 osArch 精确匹配的 full 发布物，回退平台无关（osArch 空）的 full 发布物；
// 无锚可比对时保持 Unknown（fail-open），有锚且不一致才置 Violation。
func (this *Client) evaluateBinaryIntegrity(manifest *Manifest, osArch string) {

	if manifest == nil {
		return
	}
	var fallback *ManifestArtifact
	var matched *ManifestArtifact
	for index := range manifest.Payload.Artifacts {
		artifact := &manifest.Payload.Artifacts[index]
		if artifact.ArtifactType != "" && artifact.ArtifactType != "full" {
			continue
		}
		switch {
		case artifact.OsArch != "" && artifact.OsArch == osArch:
			matched = artifact
		case artifact.OsArch == "" && fallback == nil:
			fallback = artifact
		}
	}
	if matched == nil {
		matched = fallback
	}
	if matched == nil || strings.TrimSpace(matched.Sha256) == "" {
		return
	}
	hash, err := this.integrityAnchor.selfBinaryHash()
	if err != nil || hash == "" {
		return
	}
	state := BinaryIntegrityOK
	if !strings.EqualFold(strings.TrimSpace(matched.Sha256), hash) {
		state = BinaryIntegrityViolation
	}
	this.integrity.mu.Lock()
	this.integrity.expected = strings.ToLower(strings.TrimSpace(matched.Sha256))
	this.integrity.actual = hash
	this.integrity.mu.Unlock()
	this.integrity.state.Store(int32(state))
}
