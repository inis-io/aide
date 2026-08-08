package licence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// signPayload - 用私钥种子对待签名字节做 Ed25519 签名，返回 hex 编码签名
func signPayload(payloadBytes []byte, seed []byte) (string, error) {
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("私钥种子长度必须为 %d 字节", ed25519.SeedSize)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	return hex.EncodeToString(ed25519.Sign(privateKey, payloadBytes)), nil
}

// verifyPayload - 用 hex 公钥验签：载荷字节 + hex 签名
func verifyPayload(payloadBytes []byte, signatureHex string, publicKeyHex string) bool {
	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	signature, err := hex.DecodeString(signatureHex)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(publicKey, payloadBytes, signature)
}

// generateKeyPair - 生成新的 Ed25519 密钥对，返回私钥种子与公钥（均为原始字节）
func generateKeyPair() (seed []byte, publicKey []byte, err error) {
	pub, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, errors.New("生成 Ed25519 密钥对失败: " + err.Error())
	}
	return privateKey.Seed(), pub, nil
}

// ed25519PublicKey - 由私钥种子推导 Ed25519 公钥（与签发端同一推导：ed25519.NewKeyFromSeed）
func ed25519PublicKey(seed []byte) []byte {
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}

// randomNonce - 生成随机挑战值（hex 编码的 16 字节随机数）
func randomNonce() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return hex.EncodeToString(buffer)
}
