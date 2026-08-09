package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ConfigItem - 项目配置下发条目
type ConfigItem struct {
	ConfigKey string          `json:"configKey"`
	Name      string          `json:"name"`
	Content   json.RawMessage `json:"content"`
	Version   int             `json:"version"`
}

// ConfigPayload - 项目配置同步载荷（与平台签发端字节级镜像）
//
// 字段顺序即签名内容，新增字段只允许追加到末尾。
type ConfigPayload struct {
	ProjectId   string       `json:"projectId"`
	SyncVersion int          `json:"syncVersion"`
	Keys        []string     `json:"keys"`
	Configs     []ConfigItem `json:"configs"`
	IssuedAt    string       `json:"issuedAt"`
	KeyVersion  string       `json:"keyVersion"`
	Nonce       string       `json:"nonce"`
}

// ConfigEnvelope - 项目配置签名信封
type ConfigEnvelope struct {
	Version   int           `json:"version"`
	Algorithm string        `json:"algorithm"`
	Payload   ConfigPayload `json:"payload"`
	Signature string        `json:"signature"`
}

// ParseConfigEnvelope - 解析项目配置信封并返回 payload 原文
func ParseConfigEnvelope(data []byte) (envelope ConfigEnvelope, rawPayload []byte, err error) {
	var raw struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		return ConfigEnvelope{}, nil, err
	}
	if err = json.Unmarshal(raw.Payload, &envelope.Payload); err != nil {
		return ConfigEnvelope{}, nil, err
	}
	envelope.Version = raw.Version
	envelope.Algorithm = raw.Algorithm
	envelope.Signature = raw.Signature
	return envelope, raw.Payload, nil
}

type configSyncResponse struct {
	Status     string          `json:"status"`
	ServerTime int64           `json:"serverTime"`
	Envelope   json.RawMessage `json:"envelope"`
	Message    string          `json:"message"`
}

// ConfigSync - 增量同步项目配置并持久化本地快照
func (this *Client) ConfigSync(ctx context.Context) (*ConfigEnvelope, error) {
	this.opMu.Lock()
	defer this.opMu.Unlock()

	this.mu.RLock()
	sinceVersion := this.state.ConfigSyncVersion
	this.mu.RUnlock()
	envelope, err := this.configSyncLocked(ctx, sinceVersion)
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

func (this *Client) configSyncLocked(ctx context.Context, sinceVersion int) (*ConfigEnvelope, error) {
	body, err := json.Marshal(map[string]any{"licenseNo": this.options.LicenseNo, "sinceVersion": sinceVersion})
	if err != nil {
		return nil, err
	}
	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/projects/configs/sync", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("许可证或项目信息无效")
	}
	var response configSyncResponse
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
	envelope, err := this.verifyConfigEnvelope(response.Envelope)
	if err != nil {
		return nil, err
	}
	this.applyConfigEnvelope(envelope)
	this.persist()
	return envelope, nil
}

// Config - 读取已同步的项目配置原文
func (this *Client) Config(key string) (json.RawMessage, bool) {
	this.mu.RLock()
	item, exists := this.state.Configs[key]
	this.mu.RUnlock()
	if !exists {
		return nil, false
	}
	return append(json.RawMessage(nil), item.Content...), true
}

// ConfigMust - 读取已同步的项目配置，不存在时 panic
func (this *Client) ConfigMust(key string) json.RawMessage {
	value, exists := this.Config(key)
	if !exists {
		panic("项目配置不存在：" + key)
	}
	return value
}

func (this *Client) verifyConfigEnvelope(raw json.RawMessage) (*ConfigEnvelope, error) {
	envelope, rawPayload, err := ParseConfigEnvelope(raw)
	if err != nil {
		return nil, err
	}
	publicKey, exists := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exists {
		return nil, errors.New("未内置 keyVersion=" + envelope.Payload.KeyVersion + " 的验签公钥")
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm ||
		!verifyPayload(rawPayload, envelope.Signature, publicKey) {
		return nil, errors.New("项目配置信封验签失败")
	}
	return &envelope, nil
}

func (this *Client) applyConfigEnvelope(envelope *ConfigEnvelope) {
	keys := make(map[string]struct{}, len(envelope.Payload.Keys))
	for _, key := range envelope.Payload.Keys {
		keys[key] = struct{}{}
	}
	this.mu.Lock()
	if this.state.Configs == nil {
		this.state.Configs = make(map[string]ConfigItem)
	}
	for key := range this.state.Configs {
		if _, exists := keys[key]; !exists {
			delete(this.state.Configs, key)
		}
	}
	for _, item := range envelope.Payload.Configs {
		item.Content = append(json.RawMessage(nil), item.Content...)
		this.state.Configs[item.ConfigKey] = item
	}
	this.state.ConfigSyncVersion = envelope.Payload.SyncVersion
	this.mu.Unlock()
}
