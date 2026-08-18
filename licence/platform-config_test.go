package licence

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ============================= 假平台：平台配置同步 =============================

// handlePlatformConfigSync - 平台配置全量/增量同步（契约行为镜像：
// projectId 非 0 视为越权返回 status=ERROR；否则按 sinceVersion 过滤下发 + license-key 签名）
func (this *fakePlatform) handlePlatformConfigSync(writer http.ResponseWriter, request *http.Request, body []byte) {
	this.mu.Lock()
	defer this.mu.Unlock()
	if !this.credential(writer, request, body) {
		return
	}
	var params struct {
		ProjectId    int `json:"projectId"`
		SinceVersion int `json:"sinceVersion"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		writeNotFound(writer)
		return
	}
	// 越权：projectId 非 0 且与许可证归属不一致 → status=ERROR（HTTP 200）
	if params.ProjectId != 0 {
		writeJson(writer, map[string]any{"status": StatusError, "serverTime": time.Now().UnixMilli(), "message": "项目与许可证归属不一致"})
		return
	}
	// 服务端契约：恒下发权威全量快照（无变更重同步也不返回空集）。
	// 与真实后端 licen-hub platform-config.go 一致：sinceVersion 仅作水位提示，不裁剪下发。
	configs := make([]PlatformConfigItem, 0, len(this.platformConfigs))
	for _, item := range this.platformConfigs {
		configs = append(configs, item)
	}
	payload := PlatformConfigPayload{
		ProjectId: "PRJ-2026-000001", SyncVersion: this.platformConfigSyncVersion,
		Groups: []PlatformConfigGroup{
			{Id: 1, Pid: 0, Name: "general", Label: "通用", Icon: "settings", Sort: 1},
			{Id: 2, Pid: 1, Name: "theme", Label: "主题", Icon: "palette", Sort: 2},
		},
		Configs: configs, IssuedAt: time.Now().UTC().Format(time.RFC3339),
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
		"envelope": PlatformConfigEnvelope{Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature},
	})
}

// ============================= Golden 向量 =============================

// Golden 向量由 licen-hub 平台签发端（backend/app/common/sign）原样生成，
// 字段顺序即签名内容，任何序列化/签名语义漂移都会在此暴露。

// goldenPlatformConfigPayload - 固定载荷：含特殊字符 <>&、非空 Options/Rules、Sensitive 配置
var goldenPlatformConfigPayload = PlatformConfigPayload{
	ProjectId:   "PRJ-2026-000001",
	SyncVersion: 3,
	Groups: []PlatformConfigGroup{
		{Id: 1, Pid: 0, Name: "general", Label: "通用", Icon: "settings", Sort: 1},
		{Id: 2, Pid: 1, Name: "theme", Label: "主题", Icon: "palette", Sort: 2},
	},
	Configs: []PlatformConfigItem{
		{
			Key: "app.region", Label: "部署区域", Type: "select",
			Value: "ap-southeast-1", DefaultValue: "ap-east-1",
			Options: json.RawMessage(`[{"label":"香港","value":"ap-east-1"},{"label":"新加坡","value":"ap-southeast-1"}]`),
			Rules:   json.RawMessage(`[{"key":"app.region","operator":"==","value":"<prod>&"}]`),
			Remark:  "上线后不可改", Sensitive: false, Version: 3, GroupPath: "general/theme",
		},
		{
			Key: "security.api_token", Label: "接口令牌", Type: "password",
			Value: "S3cr3t&<key>", DefaultValue: "",
			Options:    json.RawMessage(`null`),
			Rules:      json.RawMessage(`{"min":12}`),
			Placeholder: "至少 12 位", Remark: "敏感项", Sensitive: true, Version: 2, GroupPath: "general",
		},
	},
	IssuedAt:   "2026-08-08T12:00:00Z",
	KeyVersion: "license-key-2026-01",
	Nonce:      "a1b2c3d4e5f60718293a4b5c6d7e8f90",
}

// goldenPlatformConfigCanonicalPlain - 平台签发端 canonical 的字面形态（< > & 未转义，便于阅读）
// 完整转义形态由 escapeJSONHTML 在测试内展开，避免源码内出现反斜杠序列。
const goldenPlatformConfigCanonicalPlain = `{"projectId":"PRJ-2026-000001","syncVersion":3,"groups":[{"id":1,"pid":0,"name":"general","label":"通用","icon":"settings","sort":1,"children":null},{"id":2,"pid":1,"name":"theme","label":"主题","icon":"palette","sort":2,"children":null}],"configs":[{"key":"app.region","label":"部署区域","type":"select","value":"ap-southeast-1","defaultValue":"ap-east-1","options":[{"label":"香港","value":"ap-east-1"},{"label":"新加坡","value":"ap-southeast-1"}],"rules":[{"key":"app.region","operator":"==","value":"<prod>&"}],"placeholder":"","remark":"上线后不可改","sensitive":false,"version":3,"groupPath":"general/theme"},{"key":"security.api_token","label":"接口令牌","type":"password","value":"S3cr3t&<key>","defaultValue":"","options":null,"rules":{"min":12},"placeholder":"至少 12 位","remark":"敏感项","sensitive":true,"version":2,"groupPath":"general"}],"issuedAt":"2026-08-08T12:00:00Z","keyVersion":"license-key-2026-01","nonce":"a1b2c3d4e5f60718293a4b5c6d7e8f90"}`

const goldenPlatformConfigSignature = "53ff67e33517e41f81b20dc21ae36a7d2071c6cd2fc3fd5229386f4a2d5854fa18d3afc4d394500297aa3180c2907dab4d1071f05cad76c3ea65286d28af4f0c"

// escapeJSONHTML - 复现 Go encoding/json 的 HTML 转义（< > & → < > &）。
// 反斜杠在运行期用字节构造，源码内不出现反斜杠序列。
func escapeJSONHTML(input string) string {
	backslash := string(rune(0x5c))
	input = strings.ReplaceAll(input, "<", backslash+"u003c")
	input = strings.ReplaceAll(input, ">", backslash+"u003e")
	input = strings.ReplaceAll(input, "&", backslash+"u0026")
	return input
}

// goldenPlatformConfigCanonical - 完整转义形态（与平台签发端逐字节一致）
var goldenPlatformConfigCanonical = escapeJSONHTML(goldenPlatformConfigCanonicalPlain)

// TestPlatformConfigGoldenCanonical - canonical 字节必须与平台签发端逐字节一致（含 <>& 转义）
func TestPlatformConfigGoldenCanonical(t *testing.T) {
	canonical, err := json.Marshal(goldenPlatformConfigPayload)
	if err != nil {
		t.Fatalf("json.Marshal 失败: %v", err)
	}
	if string(canonical) != goldenPlatformConfigCanonical {
		t.Fatalf("canonical 字节不一致\n期望: %s\n实际: %s", goldenPlatformConfigCanonical, string(canonical))
	}
	if !strings.Contains(goldenPlatformConfigCanonical, "u003c") || !strings.Contains(goldenPlatformConfigCanonical, "u0026") {
		t.Fatalf("canonical 缺少 <>& 转义序列: %s", goldenPlatformConfigCanonical)
	}
}

// TestPlatformConfigGoldenSignature - 用固定种子对 canonical 签名，结果必须与平台签发端一致
func TestPlatformConfigGoldenSignature(t *testing.T) {
	seed, err := hex.DecodeString(goldenSeed)
	if err != nil {
		t.Fatalf("种子 hex 解码失败: %v", err)
	}
	signature, err := signPayload([]byte(goldenPlatformConfigCanonical), seed)
	if err != nil {
		t.Fatalf("signPayload 失败: %v", err)
	}
	if signature != goldenPlatformConfigSignature {
		t.Fatalf("签名不一致\n期望: %s\n实际: %s", goldenPlatformConfigSignature, signature)
	}
	if !Licence.VerifySign([]byte(goldenPlatformConfigCanonical), signature, goldenPublicKey) {
		t.Fatalf("验签失败")
	}
}

// TestPlatformConfigGoldenTamper - 篡改 canonical 任意字节或使用错误公钥，验签必须失败
func TestPlatformConfigGoldenTamper(t *testing.T) {
	tampered := []byte(goldenPlatformConfigCanonical)
	tampered[len(tampered)/2] ^= 0x01
	if Licence.VerifySign(tampered, goldenPlatformConfigSignature, goldenPublicKey) {
		t.Fatalf("篡改后验签竟然通过")
	}
	if Licence.VerifySign([]byte(goldenPlatformConfigCanonical), goldenPlatformConfigSignature, strings.Repeat("00", 32)) {
		t.Fatalf("错误公钥验签竟然通过")
	}
}

// ============================= 端到端测试 =============================

// TestPlatformConfigSyncCacheAndPersistence - 平台配置增量同步、快照替换与加密持久化
func TestPlatformConfigSyncCacheAndPersistence(t *testing.T) {
	platform := newFakePlatform(t)
	platform.platformConfigs["app.region"] = PlatformConfigItem{
		Key: "app.region", Label: "部署区域", Type: "select",
		Value: "ap-southeast-1", DefaultValue: "ap-east-1",
		Options: json.RawMessage(`[{"label":"香港","value":"ap-east-1"},{"label":"新加坡","value":"ap-southeast-1"}]`),
		Rules:   json.RawMessage(`[{"key":"app.region","operator":"==","value":"<prod>&"}]`),
		Remark:  "上线后不可改", Sensitive: false, Version: 3, GroupPath: "general/theme",
	}
	platform.platformConfigs["security.api_token"] = PlatformConfigItem{
		Key: "security.api_token", Label: "接口令牌", Type: "password",
		Value: "S3cr3t&<key>", DefaultValue: "",
		Options:    json.RawMessage(`null`),
		Rules:      json.RawMessage(`{"min":12}`),
		Placeholder: "至少 12 位", Remark: "敏感项", Sensitive: true, Version: 2, GroupPath: "general",
	}
	platform.platformConfigSyncVersion = 3
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("启动客户端失败: %v", err)
	}
	client.Stop()
	if _, err = client.PlatformConfigSync(t.Context()); err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	item, exists := client.PlatformConfig("app.region")
	if !exists || item.Value != "ap-southeast-1" || item.Version != 3 || item.Sensitive || item.GroupPath != "general/theme" {
		t.Fatalf("平台配置快照不符: exists=%v item=%+v", exists, item)
	}
	if sensitive := client.PlatformConfigMust("security.api_token"); !sensitive.Sensitive || sensitive.Value != "S3cr3t&<key>" || sensitive.GroupPath != "general" {
		t.Fatalf("敏感配置快照不符: %+v", sensitive)
	}
	client.mu.RLock()
	syncVersion := client.state.PlatformConfigSyncVersion
	client.mu.RUnlock()
	if syncVersion != 3 {
		t.Fatalf("同步水位应为 3，实际 %d", syncVersion)
	}

	// 增量：删旧加新，水位提升
	var rate PlatformConfigItem
	platform.mu.Lock()
	delete(platform.platformConfigs, "app.region")
	platform.platformConfigs["security.rate_limit"] = PlatformConfigItem{
		Key: "security.rate_limit", Label: "限流阈值", Type: "number",
		Value: "120", DefaultValue: "60",
		Rules: json.RawMessage(`{"min":10,"max":1000}`), Sensitive: false, Version: 4,
	}
	platform.platformConfigSyncVersion = 4
	platform.mu.Unlock()
	if _, err = client.PlatformConfigSync(t.Context()); err != nil {
		t.Fatalf("增量同步失败: %v", err)
	}
	if _, exists = client.PlatformConfig("app.region"); exists {
		t.Fatal("已删除平台配置未按快照替换清理")
	}
	if rate = client.PlatformConfigMust("security.rate_limit"); rate.Value != "120" || rate.Version != 4 {
		t.Fatalf("新平台配置未写入快照: %+v", rate)
	}
	if _, exists = client.PlatformConfig("security.api_token"); !exists {
		t.Fatal("未变更平台配置在增量同步后被误删")
	}

	// 重启恢复
	restored, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建恢复客户端失败: %v", err)
	}
	if err = restored.restore(); err != nil {
		t.Fatalf("恢复持久化状态失败: %v", err)
	}
	if rate, exists = restored.PlatformConfig("security.rate_limit"); !exists || rate.Value != "120" {
		t.Fatalf("持久化平台配置恢复失败: exists=%v rate=%+v", exists, rate)
	}
}

// TestPlatformConfigNoChangeResyncKeepsSnapshot - 无变更重同步不得清空平台配置快照。
// 回归：服务端水位相等时若返回空集，客户端全量替换会清空快照（真实缺陷）。
func TestPlatformConfigNoChangeResyncKeepsSnapshot(t *testing.T) {
	platform := newFakePlatform(t)
	platform.platformConfigs["app.region"] = PlatformConfigItem{
		Key: "app.region", Label: "部署区域", Type: "select",
		Value: "ap-southeast-1", DefaultValue: "ap-east-1", Version: 3,
	}
	platform.platformConfigs["security.api_token"] = PlatformConfigItem{
		Key: "security.api_token", Label: "接口令牌", Type: "password",
		Value: "S3cr3t", Sensitive: true, Version: 2,
	}
	platform.platformConfigSyncVersion = 3
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("启动客户端失败: %v", err)
	}
	client.Stop()

	if _, err = client.PlatformConfigSync(t.Context()); err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	if _, exists := client.PlatformConfig("app.region"); !exists {
		t.Fatal("首次同步后配置缺失")
	}

	// 无变更重同步：水位相等，服务端仍返回全量快照，客户端不得清空。
	if _, err = client.PlatformConfigSync(t.Context()); err != nil {
		t.Fatalf("无变更重同步失败: %v", err)
	}
	if _, exists := client.PlatformConfig("app.region"); !exists {
		t.Fatal("无变更重同步清空了平台配置快照")
	}
	if _, exists := client.PlatformConfig("security.api_token"); !exists {
		t.Fatal("无变更重同步清空了敏感配置")
	}
}

// TestPlatformConfigNilMapBackstop - 老版本状态文件缺省平台配置字段，恢复后 nil map 兜底不 panic
func TestPlatformConfigNilMapBackstop(t *testing.T) {
	platform := newFakePlatform(t)
	dir := t.TempDir()
	client, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("启动客户端失败: %v", err)
	}
	client.Stop()

	// 模拟老版本状态：置 nil 并持久化（omitempty 使 JSON 缺省该字段）
	client.mu.Lock()
	client.state.PlatformConfigs = nil
	client.mu.Unlock()
	client.persist()

	restored, err := New(testOptions(platform, dir))
	if err != nil {
		t.Fatalf("创建恢复客户端失败: %v", err)
	}
	if err = restored.restore(); err != nil {
		t.Fatalf("恢复持久化状态失败: %v", err)
	}
	restored.mu.RLock()
	configs := restored.state.PlatformConfigs
	restored.mu.RUnlock()
	if configs == nil {
		t.Fatal("nil map 兜底未生效")
	}
}
