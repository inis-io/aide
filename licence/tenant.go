package licence

import "encoding/json"

// TenantPayload - SaaS 租户授权签发载荷（与平台 app/common/sign/tenant.go 字节级镜像）
//
// 字段顺序即 JSON 序列化顺序，直接决定签名内容：
// 新增字段只允许追加到末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。
// 租户不做指纹绑定（无 binding），绑定语义由「SaaS 服务端持有效实例凭证」间接达成；
// 签名密钥复用 license-key，验签使用 Options.PublicKeys。
type TenantPayload struct {
	GrantId          string           `json:"grantId"`
	TenantCode       string           `json:"tenantCode"`
	UserId           string           `json:"userId"`
	ProjectId        string           `json:"projectId"`
	PlanCode         string           `json:"planCode"`
	Environment      string           `json:"environment"`
	SubscriptionType string           `json:"subscriptionType"`
	ValidFrom        string           `json:"validFrom"`
	ValidUntil       string           `json:"validUntil"`
	GraceDays        int              `json:"graceDays"`
	VersionRange     string           `json:"versionRange"`
	Features         map[string]bool  `json:"features"`
	Limits           map[string]int64 `json:"limits"`
	MenuCodes        []string         `json:"menuCodes"`
	ManifestVersion  int              `json:"manifestVersion"`
	IssuedAt         string           `json:"issuedAt"`
	KeyVersion       string           `json:"keyVersion"`
	Nonce            string           `json:"nonce"`
}

// TenantEnvelope - 租户签名信封（信封 v1，结构与 Envelope 一致，载荷为 TenantPayload）
type TenantEnvelope struct {
	Version   int           `json:"version"`
	Algorithm string        `json:"algorithm"`
	Payload   TenantPayload `json:"payload"`
	// Signature - 对载荷 canonical JSON 字节的 Ed25519 签名（hex 编码）
	Signature string `json:"signature"`
}

// ParseTenantEnvelope - 解析租户信封 JSON，同时返回载荷原始字节（验签基于原文）
func ParseTenantEnvelope(data []byte) (envelope TenantEnvelope, rawPayload []byte, err error) {

	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return TenantEnvelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return TenantEnvelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}

// issueTenant - 签发租户授权信封（测试与对侧联调辅助；生产签发只在平台进行）
func issueTenant(payload TenantPayload, seed []byte) (TenantEnvelope, error) {

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return TenantEnvelope{}, err
	}
	signature, err := signPayload(payloadBytes, seed)
	if err != nil {
		return TenantEnvelope{}, err
	}
	return TenantEnvelope{Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature}, nil
}
