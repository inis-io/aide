// Package licence - 许可证签发与验签
// 信封格式 v1 与签名规则需与平台侧保持一致，见 licen-hub docs/md/许可证运行面接口契约.md 第 6 节。
package licence

import "encoding/json"

// Algorithm - 签名算法标识（固定 Ed25519）
const Algorithm = "Ed25519"

// EnvelopeVersion - 信封结构版本
const EnvelopeVersion = 1

// Binding - 载荷绑定信息（实例/指纹/域名）
type Binding struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Payload - 许可证签发载荷
//
// 字段顺序即 JSON 序列化顺序，直接决定签名内容：
// 新增字段只允许追加到末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。
// userId/projectId/instanceId 使用业务编号（USR-/PRJ-/INS-），不暴露数据库自增 id。
// 四个期限字段为 RFC3339 字符串，空串表示不限制（validUntil 空 = 永久授权）。
type Payload struct {
	LicenseId        string           `json:"licenseId"`
	UserId           string           `json:"userId"`
	ProjectId        string           `json:"projectId"`
	InstanceId       string           `json:"instanceId"`
	Environment      string           `json:"environment"`
	ValidFrom        string           `json:"validFrom"`
	ValidUntil       string           `json:"validUntil"`
	MaintenanceUntil string           `json:"maintenanceUntil"`
	UpgradeUntil     string           `json:"upgradeUntil"`
	GraceDays        int              `json:"graceDays"`
	VersionRange     string           `json:"versionRange"`
	Features         map[string]bool  `json:"features"`
	Limits           map[string]int64 `json:"limits"`
	Binding          *Binding         `json:"binding"`
	IssuedAt         string           `json:"issuedAt"`
	KeyVersion       string           `json:"keyVersion"`
	Nonce            string           `json:"nonce"`
}

// Envelope - 签名信封（下发给客户端的完整许可证文件结构）
type Envelope struct {
	Version   int     `json:"version"`
	Algorithm string  `json:"algorithm"`
	Payload   Payload `json:"payload"`
	// Signature - 对 MarshalPayload(Payload) 字节的 Ed25519 签名（hex 编码）
	Signature string `json:"signature"`
}

// MarshalPayload - 序列化载荷为待签名字节
// 结构体字段顺序稳定，map 字段（features/limits）由 encoding/json 按键名排序，
// 同一载荷任意时刻序列化结果字节一致，验签方可复现。
func MarshalPayload(payload Payload) ([]byte, error) {
	return json.Marshal(payload)
}

// ParseEnvelope - 解析信封 JSON，同时返回载荷原始字节
// 验签必须基于载荷原文（而非重序列化）：平台只追加新字段时，
// 旧版 SDK 重序列化会丢失未知字段导致验签失败，原文验签天然兼容。
func ParseEnvelope(data []byte) (envelope Envelope, rawPayload []byte, err error) {

	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return Envelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return Envelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}
