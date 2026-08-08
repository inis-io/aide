package licence

import (
	"encoding/hex"
	"encoding/json"
)

// Licence - 许可证实例（全局，nil 安全，可直接链式调用）
/**
 * @example：
 * envelope, err := licence.Licence.Payload(payload).GenerateKeyPair().Issue()
 * ok := licence.Licence.PublicKey(publicKey).Verify(envelope)
 */
var Licence *LicenceClass

// LicenceClass - 许可证签发与验签
type LicenceClass struct {
	// payload - 许可证载荷
	payload Payload
	// seed - 私钥种子
	seed []byte
	// publicKey - 公钥（hex 编码）
	publicKey string
}

// clone - 克隆许可证实例（nil 时返回新实例，隔离链式上下文）
func (this *LicenceClass) clone() *LicenceClass {
	if this == nil {
		return &LicenceClass{}
	}
	clone := *this
	return &clone
}

// Payload - 设置载荷
func (this *LicenceClass) Payload(payload Payload) *LicenceClass {
	item := this.clone()
	item.payload = payload
	return item
}

// Seed - 设置私钥种子
func (this *LicenceClass) Seed(seed []byte) *LicenceClass {
	item := this.clone()
	item.seed = seed
	return item
}

// PublicKey - 设置公钥（hex 编码）
func (this *LicenceClass) PublicKey(publicKey string) *LicenceClass {
	item := this.clone()
	item.publicKey = publicKey
	return item
}

// GenerateKeyPair - 生成新的 Ed25519 密钥对并写入实例，返回自身便于链式调用
func (this *LicenceClass) GenerateKeyPair() *LicenceClass {
	item := this.clone()
	seed, publicKey, err := generateKeyPair()
	if err != nil {
		return item
	}
	item.seed = seed
	item.publicKey = hex.EncodeToString(publicKey)
	return item
}

// KeyPair - 获取当前实例的私钥种子与公钥（hex 编码）
func (this *LicenceClass) KeyPair() (seed []byte, publicKey string) {
	if this == nil {
		return nil, ""
	}
	return this.seed, this.publicKey
}

// Issue - 签发：序列化当前载荷并签名，组装完整信封
func (this *LicenceClass) Issue() (Envelope, error) {
	item := this.clone()
	payloadBytes, err := MarshalPayload(item.payload)
	if err != nil {
		return Envelope{}, err
	}
	signature, err := signPayload(payloadBytes, item.seed)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Version:   EnvelopeVersion,
		Algorithm: Algorithm,
		Payload:   item.payload,
		Signature: signature,
	}, nil
}

// Sign - 对载荷字节做 Ed25519 签名，返回 hex 编码签名
func (this *LicenceClass) Sign(payloadBytes []byte) (string, error) {
	item := this.clone()
	return signPayload(payloadBytes, item.seed)
}

// Verify - 校验完整信封：重序列化载荷并验签（可传参覆盖当前公钥）
func (this *LicenceClass) Verify(envelope Envelope, publicKey ...string) bool {
	item := this.clone()
	if len(publicKey) > 0 {
		item.publicKey = publicKey[0]
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm {
		return false
	}
	payloadBytes, err := MarshalPayload(envelope.Payload)
	if err != nil {
		return false
	}
	return verifyPayload(payloadBytes, envelope.Signature, item.publicKey)
}

// VerifySign - 用公钥校验载荷签名（可传参覆盖当前公钥）
func (this *LicenceClass) VerifySign(payloadBytes []byte, signatureHex string, publicKey ...string) bool {
	item := this.clone()
	if len(publicKey) > 0 {
		item.publicKey = publicKey[0]
	}
	return verifyPayload(payloadBytes, signatureHex, item.publicKey)
}

// VerifyRaw - 用公钥校验载荷原文签名（验签首选：兼容平台只追加的新字段）
func (this *LicenceClass) VerifyRaw(rawPayload []byte, signatureHex string, publicKey ...string) bool {
	item := this.clone()
	if len(publicKey) > 0 {
		item.publicKey = publicKey[0]
	}
	return verifyPayload(rawPayload, signatureHex, item.publicKey)
}

// Parse - 解析信封 JSON
func (this *LicenceClass) Parse(data []byte) (Envelope, error) {
	var envelope Envelope
	err := json.Unmarshal(data, &envelope)
	return envelope, err
}

// Nonce - 生成随机挑战值（hex 编码的 16 字节随机数），作为载荷防重放 nonce
func (this *LicenceClass) Nonce() string {
	return randomNonce()
}
