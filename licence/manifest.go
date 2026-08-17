package licence

import "encoding/json"

// ManifestArtifact - 更新清单中的发布物项（随载荷一起被 release-key 签名）
type ManifestArtifact struct {
	ArtifactNo string `json:"artifactNo"`
	FileName   string `json:"fileName"`
	Url        string `json:"url"`
	Size       int64  `json:"size"`
	OsArch     string `json:"osArch"`
	Sha256     string `json:"sha256"`
	Signature  string `json:"signature"`
	KeyVersion string `json:"keyVersion"`
	// ArtifactType - 发布物类型（full=全量包 / incremental=文件级增量包）
	ArtifactType string `json:"artifactType"`
	// SourceVersion - 增量包来源版本（全量包为空）
	SourceVersion string `json:"sourceVersion"`
}

// ManifestUpdatePolicy - 更新策略（随清单签名下发；优先级见 licen-hub 设计文档 §5.8：
// force 不可被本地配置关闭 > 版本显式 auto > SDK 集成配置 > 项目默认 > 全局默认 false）
type ManifestUpdatePolicy struct {
	// Auto - 允许 SDK 无需确认自动执行更新
	Auto bool `json:"auto"`
	// Force - 强制更新：不得因本地配置跳过（安全修复场景）
	Force bool `json:"force"`
	// RestartMode - 建议重启方式：auto/exit-code/respawn/callback；空 = SDK 自适应探测
	RestartMode string `json:"restartMode"`
}

// ManifestPayload - 更新清单签名载荷（与平台 app/common/sign/manifest.go 字节级镜像）
//
// 字段顺序即 JSON 序列化顺序，直接决定签名内容：
// 新增字段只允许追加到末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。
type ManifestPayload struct {
	ProjectId          string             `json:"projectId"`
	InstanceId         string             `json:"instanceId"`
	Version            string             `json:"version"`
	BuildNumber        string             `json:"buildNumber"`
	SourceVersionRange string             `json:"sourceVersionRange"`
	MinUpgradeVersion  string             `json:"minUpgradeVersion"`
	NeedDowntime       bool               `json:"needDowntime"`
	MigrationVersion   string             `json:"migrationVersion"`
	EstimatedDuration  int                `json:"estimatedDuration"`
	ChangelogSummary   string             `json:"changelogSummary"`
	Artifacts          []ManifestArtifact `json:"artifacts"`
	IssuedAt           string             `json:"issuedAt"`
	KeyVersion         string             `json:"keyVersion"`
	Nonce              string             `json:"nonce"`
	// UpdatePolicy - 更新策略（只许追加在末尾；验签基于原文字节，新旧 SDK 互不影响）
	UpdatePolicy *ManifestUpdatePolicy `json:"updatePolicy"`
}

// Manifest - 更新清单信封（release-key 签名，与许可证信封同构）
type Manifest struct {
	Version   int             `json:"version"`
	Algorithm string          `json:"algorithm"`
	Payload   ManifestPayload `json:"payload"`
	// Signature - 对载荷 canonical JSON 字节的 Ed25519 签名（hex 编码）
	Signature string `json:"signature"`
}

// ArtifactPayload - 发布物签名载荷（阶段三：artifactNo/version/sha256，与平台镜像）
type ArtifactPayload struct {
	ArtifactNo string `json:"artifactNo"`
	Version    string `json:"version"`
	Sha256     string `json:"sha256"`
}

// MarshalManifestPayload - 序列化更新清单载荷为 canonical JSON 待签名字节
func MarshalManifestPayload(payload ManifestPayload) ([]byte, error) {
	return json.Marshal(payload)
}

// ParseManifest - 解析更新清单 JSON，同时返回载荷原始字节（验签基于原文，兼容只追加的新字段）
func ParseManifest(data []byte) (manifest Manifest, rawPayload []byte, err error) {

	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &manifest.Payload); err != nil {
		return Manifest{}, nil, err
	}
	manifest.Version = raw.Version
	manifest.Algorithm = raw.Algorithm
	manifest.Signature = raw.Signature
	return manifest, raw.Payload, nil
}

// issueManifest - 签发更新清单（测试与对侧联调辅助；生产签发只在平台进行）
func issueManifest(payload ManifestPayload, seed []byte) (Manifest, error) {

	payloadBytes, err := MarshalManifestPayload(payload)
	if err != nil {
		return Manifest{}, err
	}
	signature, err := signPayload(payloadBytes, seed)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature}, nil
}
