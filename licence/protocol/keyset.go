package protocol

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// KeysetPublicKey - keyset 只读公钥项，字段顺序即验签 JSON 顺序。
type KeysetPublicKey struct {
	// KeyVersion - 公钥版本。
	KeyVersion string `json:"keyVersion"`
	// PublicKey - Ed25519 公钥（hex）。
	PublicKey string `json:"publicKey"`
}

// KeysetPayload - 项目工作公钥集合，字段顺序与平台签发端固定一致。
type KeysetPayload struct {
	// ProjectNo - 项目关联键，必须与调用方传入值精确一致。
	ProjectNo string `json:"projectNo"`
	// Revision - 项目内单调递增的 keyset 修订号。
	Revision int64 `json:"revision"`
	// IssuedAt - 平台签发时间（RFC3339）。
	IssuedAt string `json:"issuedAt"`
	// LicenseKeys - 当前与全部历史 license 工作公钥，按 keyVersion 排序。
	LicenseKeys []KeysetPublicKey `json:"licenseKeys"`
	// ReleaseKeys - 当前与全部历史 release 工作公钥，按 keyVersion 排序。
	ReleaseKeys []KeysetPublicKey `json:"releaseKeys"`
	// KeyVersion - 签署本 keyset 的 bootstrap 根版本。
	KeyVersion string `json:"keyVersion"`
	// Nonce - 平台生成的随机挑战值。
	Nonce string `json:"nonce"`
}

// KeysetEnvelope - bootstrap 根签名的 keyset 信封。
type KeysetEnvelope struct {
	// Version - 信封结构版本。
	Version int `json:"version"`
	// Algorithm - 签名算法，固定 Ed25519。
	Algorithm string `json:"algorithm"`
	// Payload - 项目工作公钥集合。
	Payload KeysetPayload `json:"payload"`
	// Signature - payload 原始 JSON 字节的 Ed25519 签名（hex）。
	Signature string `json:"signature"`
}

// ParseKeyset - 解析 keyset 信封并返回 payload 原始字节。
func ParseKeyset(data []byte) (envelope KeysetEnvelope, rawPayload []byte, err error) {
	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return KeysetEnvelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return KeysetEnvelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}

// VerifyKeyset - 按 bootstrap 根验证 keyset 原文、项目绑定与最低 revision。
// minimumRevision 为本地已信任 revision；更低版本拒绝，相同版本允许幂等恢复。
func VerifyKeyset(raw []byte, projectNo string, roots map[string]string, minimumRevision int64) (*KeysetEnvelope, error) {
	envelope, rawPayload, err := ParseKeyset(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 keyset 失败: %w", err)
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm {
		return nil, errors.New("keyset 信封版本或算法无效")
	}
	if projectNo == "" || envelope.Payload.ProjectNo != projectNo {
		return nil, errors.New("keyset 项目编号不匹配")
	}
	if envelope.Payload.Revision <= 0 || envelope.Payload.Revision < minimumRevision {
		return nil, fmt.Errorf("keyset revision 回退: 当前 %d，最低允许 %d", envelope.Payload.Revision, minimumRevision)
	}
	if envelope.Payload.KeyVersion == "" || envelope.Payload.Nonce == "" {
		return nil, errors.New("keyset 缺少根版本或 nonce")
	}
	if _, err = time.Parse(time.RFC3339, envelope.Payload.IssuedAt); err != nil {
		return nil, errors.New("keyset issuedAt 无效")
	}
	root, exists := lookupKeysetRoot(roots, envelope.Payload.KeyVersion)
	if !exists {
		return nil, errors.New("未内置 keyVersion=" + envelope.Payload.KeyVersion + " 的 bootstrap 根公钥")
	}
	if !VerifyPayload(rawPayload, envelope.Signature, root) {
		return nil, errors.New("keyset 验签失败")
	}
	if err = verifyKeysetPublicKeys(envelope.Payload.LicenseKeys, true); err != nil {
		return nil, fmt.Errorf("license 工作公钥无效: %w", err)
	}
	if err = verifyKeysetPublicKeys(envelope.Payload.ReleaseKeys, false); err != nil {
		return nil, fmt.Errorf("release 工作公钥无效: %w", err)
	}
	return &envelope, nil
}

// lookupKeysetRoot - 精确查找 bootstrap 根，兼容配置库把 map 键转小写的形态。
func lookupKeysetRoot(roots map[string]string, keyVersion string) (string, bool) {
	if root, exists := roots[keyVersion]; exists {
		return root, true
	}
	root, exists := roots[strings.ToLower(keyVersion)]
	return root, exists
}

// verifyKeysetPublicKeys - 校验公钥内容、排序与同版本冲突。
func verifyKeysetPublicKeys(keys []KeysetPublicKey, required bool) error {
	if required && len(keys) == 0 {
		return errors.New("公钥集合不能为空")
	}
	seen := make(map[string]string, len(keys))
	for index, item := range keys {
		if item.KeyVersion == "" {
			return errors.New("keyVersion 不能为空")
		}
		if index > 0 && keys[index-1].KeyVersion > item.KeyVersion {
			return errors.New("公钥项未按 keyVersion 排序")
		}
		publicKey, err := hex.DecodeString(item.PublicKey)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			return errors.New("公钥必须是 32 字节 Ed25519 hex: " + item.KeyVersion)
		}
		if previous, exists := seen[item.KeyVersion]; exists && previous != item.PublicKey {
			return errors.New("同 keyVersion 对应不同公钥: " + item.KeyVersion)
		}
		seen[item.KeyVersion] = item.PublicKey
	}
	return nil
}
