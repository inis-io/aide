package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// PlatformConfigItem - 平台配置下发条目（服务端已完成维度匹配，value 为最终值）
//
// 字段顺序即签名内容，与 licen-hub app/common/sign/platform-config.go 字节级镜像，
// 新增字段只允许追加到末尾。
type PlatformConfigItem struct {
	Key          string          `json:"key"`
	Label        string          `json:"label"`
	Type         string          `json:"type"`
	Value        string          `json:"value"`
	DefaultValue string          `json:"defaultValue"`
	Options      json.RawMessage `json:"options"`
	Rules        json.RawMessage `json:"rules"`
	Placeholder  string          `json:"placeholder"`
	Remark       string          `json:"remark"`
	Sensitive    bool            `json:"sensitive"`
	Version      int             `json:"version"`
}

// PlatformConfigGroup - 平台配置分组（树形，扁平输出 + children 嵌套）
type PlatformConfigGroup struct {
	Id       int                   `json:"id"`
	Pid      int                   `json:"pid"`
	Name     string                `json:"name"`
	Label    string                `json:"label"`
	Icon     string                `json:"icon"`
	Sort     int                   `json:"sort"`
	Children []PlatformConfigGroup `json:"children"`
}

// PlatformConfigPayload - 平台配置同步载荷（与平台签发端字节级镜像）
//
// 字段顺序即签名内容，新增字段只允许追加到末尾。
type PlatformConfigPayload struct {
	ProjectId   string                `json:"projectId"`
	SyncVersion int                   `json:"syncVersion"`
	Groups      []PlatformConfigGroup `json:"groups"`
	Configs     []PlatformConfigItem  `json:"configs"`
	IssuedAt    string                `json:"issuedAt"`
	KeyVersion  string                `json:"keyVersion"`
	Nonce       string                `json:"nonce"`
}

// PlatformConfigEnvelope - 平台配置签名信封
type PlatformConfigEnvelope struct {
	Version   int                   `json:"version"`
	Algorithm string                `json:"algorithm"`
	Payload   PlatformConfigPayload `json:"payload"`
	Signature string                `json:"signature"`
}

// ParsePlatformConfigEnvelope - 解析平台配置信封并返回 payload 原文
func ParsePlatformConfigEnvelope(data []byte) (envelope PlatformConfigEnvelope, rawPayload []byte, err error) {
	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return PlatformConfigEnvelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return PlatformConfigEnvelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}

type platformConfigSyncBody struct {
	LicenseNo    string `json:"licenseNo"`
	ProjectId    int    `json:"projectId"`
	SinceVersion int    `json:"sinceVersion"`
}

type platformConfigSyncResponse struct {
	Status     string          `json:"status"`
	ServerTime int64           `json:"serverTime"`
	Envelope   json.RawMessage `json:"envelope"`
	Message    string          `json:"message"`
}

// PlatformConfigSync - 增量同步平台配置并持久化本地快照
func (this *Client) PlatformConfigSync(ctx context.Context) (*PlatformConfigEnvelope, error) {
	this.opMu.Lock()
	defer this.opMu.Unlock()

	this.mu.RLock()
	sinceVersion := this.state.PlatformConfigSyncVersion
	this.mu.RUnlock()
	envelope, err := this.platformConfigSyncLocked(ctx, sinceVersion)
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

func (this *Client) platformConfigSyncLocked(ctx context.Context, sinceVersion int) (*PlatformConfigEnvelope, error) {
	body, err := json.Marshal(platformConfigSyncBody{LicenseNo: this.options.LicenseNo, ProjectId: 0, SinceVersion: sinceVersion})
	if err != nil {
		return nil, err
	}
	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/platform/configs/sync", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("许可证或项目信息无效")
	}
	var response platformConfigSyncResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == StatusError {
		return nil, errors.New("服务端故障：" + response.Message)
	}
	if !passThrough(response.Status) {
		return nil, errors.New("实例许可证非放行态：" + response.Status)
	}
	envelope, err := this.verifyPlatformConfigEnvelope(response.Envelope)
	if err != nil {
		return nil, err
	}
	this.applyPlatformConfigEnvelope(envelope)
	this.persist()
	return envelope, nil
}

// PlatformConfig - 读取已同步的平台配置条目副本
func (this *Client) PlatformConfig(key string) (PlatformConfigItem, bool) {
	this.mu.RLock()
	item, exists := this.state.PlatformConfigs[key]
	this.mu.RUnlock()
	if !exists {
		return PlatformConfigItem{}, false
	}
	item.Options = append(json.RawMessage(nil), item.Options...)
	item.Rules = append(json.RawMessage(nil), item.Rules...)
	return item, true
}

// PlatformConfigMust - 读取已同步的平台配置条目，不存在时 panic
func (this *Client) PlatformConfigMust(key string) PlatformConfigItem {
	item, exists := this.PlatformConfig(key)
	if !exists {
		panic("平台配置不存在：" + key)
	}
	return item
}

func (this *Client) verifyPlatformConfigEnvelope(raw json.RawMessage) (*PlatformConfigEnvelope, error) {
	envelope, rawPayload, err := ParsePlatformConfigEnvelope(raw)
	if err != nil {
		return nil, err
	}
	publicKey, exists := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exists {
		return nil, errors.New("未内置 keyVersion=" + envelope.Payload.KeyVersion + " 的验签公钥")
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm ||
		!verifyPayload(rawPayload, envelope.Signature, publicKey) {
		return nil, errors.New("平台配置信封验签失败")
	}
	return &envelope, nil
}

func (this *Client) applyPlatformConfigEnvelope(envelope *PlatformConfigEnvelope) {
	configs := make(map[string]PlatformConfigItem, len(envelope.Payload.Configs))
	for _, item := range envelope.Payload.Configs {
		item.Options = append(json.RawMessage(nil), item.Options...)
		item.Rules = append(json.RawMessage(nil), item.Rules...)
		configs[item.Key] = item
	}
	this.mu.Lock()
	this.state.PlatformConfigs = configs
	this.state.PlatformConfigSyncVersion = envelope.Payload.SyncVersion
	this.mu.Unlock()
}
