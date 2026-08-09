package licence

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// handleConfigSync - 项目配置全量/增量同步
func (this *fakePlatform) handleConfigSync(writer http.ResponseWriter, request *http.Request, body []byte) {
	this.mu.Lock()
	defer this.mu.Unlock()
	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		SinceVersion int `json:"sinceVersion"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	keys := make([]string, 0, len(this.configs))
	configs := make([]ConfigItem, 0, len(this.configs))
	for key, item := range this.configs {
		keys = append(keys, key)
		if item.Version > params.SinceVersion {
			configs = append(configs, item)
		}
	}
	payload := ConfigPayload{
		ProjectId: "PRJ-2026-000001", SyncVersion: this.configSyncVersion,
		Keys: keys, Configs: configs, IssuedAt: time.Now().UTC().Format(time.RFC3339),
		KeyVersion: "license-key-2026-01", Nonce: Licence.Nonce(),
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		writeJson(writer, map[string]any{"status": StatusError, "message": err.Error()})
		return
	}
	signature, err := signPayload(rawPayload, this.seed)
	if err != nil {
		writeJson(writer, map[string]any{"status": StatusError, "message": err.Error()})
		return
	}
	writeJson(writer, map[string]any{
		"status": StatusValid, "serverTime": time.Now().UnixMilli(),
		"envelope": ConfigEnvelope{Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature},
	})
}

// TestConfigSyncCacheAndPersistence - 配置增量同步、删除收敛和加密持久化
func TestConfigSyncCacheAndPersistence(t *testing.T) {
	platform := newFakePlatform(t)
	platform.configs["app.theme"] = ConfigItem{
		ConfigKey: "app.theme", Name: "主题", Content: json.RawMessage(`{"color":"blue"}`), Version: 1,
	}
	platform.configSyncVersion = 1
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("启动客户端失败: %v", err)
	}
	client.Stop()
	if _, err = client.ConfigSync(t.Context()); err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	value, exists := client.Config("app.theme")
	if !exists || string(value) != `{"color":"blue"}` {
		t.Fatalf("配置快照不符: exists=%v value=%s", exists, value)
	}

	platform.mu.Lock()
	delete(platform.configs, "app.theme")
	platform.configs["app.flags"] = ConfigItem{
		ConfigKey: "app.flags", Name: "开关", Content: json.RawMessage(`{"beta":true}`), Version: 2,
	}
	platform.configSyncVersion = 2
	platform.mu.Unlock()
	if _, err = client.ConfigSync(t.Context()); err != nil {
		t.Fatalf("增量同步失败: %v", err)
	}
	if _, exists = client.Config("app.theme"); exists {
		t.Fatal("已删除配置未按 keys 清理")
	}
	if string(client.ConfigMust("app.flags")) != `{"beta":true}` {
		t.Fatal("新配置未写入快照")
	}

	restored, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建恢复客户端失败: %v", err)
	}
	if err = restored.restore(); err != nil {
		t.Fatalf("恢复持久化状态失败: %v", err)
	}
	if value, exists = restored.Config("app.flags"); !exists || string(value) != `{"beta":true}` {
		t.Fatalf("持久化配置恢复失败: exists=%v value=%s", exists, value)
	}
}
