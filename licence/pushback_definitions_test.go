package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/inis-io/aide/licence/config"
)

// testDefinitions - 测试用定义快照（含分组树与一条 select 定义，覆盖 snake_case 与原生 JSON 字段）
func testDefinitions() config.ConfigDefinitions {

	return config.ConfigDefinitions{
		Groups: []config.ConfigDefinitionGroup{
			{Name: "storage", Label: "存储", LabelEn: "Storage", Icon: "folder", Sort: 1},
			{Name: "oss", Label: "对象存储", Parent: "storage", Sort: 1},
		},
		Configs: []config.ConfigDefinitionItem{
			{
				Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage",
				Options:     json.RawMessage(`[{"value":"local","label":"本地存储"},{"value":"oss","label":"阿里云 OSS"}]`),
				Rules:       json.RawMessage(`{"required":true}`),
				Placeholder: "请选择存储驱动", Remark: "切换后需重启", DefaultValue: "local",
				Sensitive: false, Sort: 10,
			},
			{
				Key: "storage.oss.bucket", Label: "OSS Bucket", Type: "input", GroupPath: "storage/oss",
				Rules: json.RawMessage(`{"minLen":3,"maxLen":63}`), Sort: 20,
			},
		},
	}
}

// TestPushConfigDefinitionsHTTP - 项目级定义推送：body snake_case、options/rules 原生 JSON、
// groups/configs 分别计数回填、SDK 自动生成 UUID 幂等号
func TestPushConfigDefinitionsHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client := startPushbackClient(t, platform)

	result, err := client.PushConfigDefinitions(t.Context(), testDefinitions())
	if err != nil {
		t.Fatalf("PushConfigDefinitions 失败: %v", err)
	}
	if result.BatchID != 43 {
		t.Fatalf("BatchID 回填异常: %+v", result)
	}
	if result.Groups.Created != 2 || result.Configs.Created != 2 {
		t.Fatalf("groups/configs 应分别计数: %+v", result)
	}
	if result.PushedAt.IsZero() {
		t.Fatalf("PushedAt 应解析成功: %+v", result)
	}
	assertUUIDv4(t, result.ClientPushID)

	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.definitionPushbacks) != 1 {
		t.Fatalf("平台应记录 1 批次，实际 %d", len(platform.definitionPushbacks))
	}
	batch := platform.definitionPushbacks[0]
	if batch.clientPushID != result.ClientPushID {
		t.Fatalf("平台上送的幂等号应与返回值一致: %q != %q", batch.clientPushID, result.ClientPushID)
	}
	// body 必须是 snake_case + options/rules 原生 JSON（非字符串转义）
	rawBody := string(batch.rawBody)
	for _, fragment := range []string{
		`"tenant_id":0`, `"client_push_id":"` + result.ClientPushID + `"`,
		`"label_en":"Storage"`, `"group_path":"storage"`, `"default_value":"local"`,
		`"options":[{"value":"local","label":"本地存储"},{"value":"oss","label":"阿里云 OSS"}]`,
		`"rules":{"required":true}`, `"sensitive":false`, `"sort":10`,
	} {
		if !strings.Contains(rawBody, fragment) {
			t.Fatalf("body 缺少片段 %s\n实际 body: %s", fragment, rawBody)
		}
	}
	// 分组 parent 与解析结果
	if len(batch.groups) != 2 || batch.groups[1].Parent != "storage" || batch.groups[0].LabelEn != "Storage" {
		t.Fatalf("分组上送异常: %+v", batch.groups)
	}
	if len(batch.configs) != 2 || batch.configs[0].GroupPath != "storage" || batch.configs[1].GroupPath != "storage/oss" {
		t.Fatalf("配置项上送异常: %+v", batch.configs)
	}
}

// TestPushConfigDefinitionsCustomPushID - 调用方自定 client_push_id 原样生效
func TestPushConfigDefinitionsCustomPushID(t *testing.T) {

	platform := newFakePlatform(t)
	client := startPushbackClient(t, platform)

	result, err := client.PushConfigDefinitions(t.Context(), config.ConfigDefinitions{}, "def-batch-1")
	if err != nil {
		t.Fatalf("PushConfigDefinitions 失败: %v", err)
	}
	if result.ClientPushID != "def-batch-1" {
		t.Fatalf("自定幂等号应原样生效，实际 %q", result.ClientPushID)
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.definitionPushbacks) != 1 || platform.definitionPushbacks[0].clientPushID != "def-batch-1" {
		t.Fatalf("平台记录的幂等号异常: %+v", platform.definitionPushbacks)
	}
	// 空快照语义合法（清空非 owned 定义）：groups/configs 应为空数组而非缺省
	rawBody := string(platform.definitionPushbacks[0].rawBody)
	if !strings.Contains(rawBody, `"groups":[]`) || !strings.Contains(rawBody, `"configs":[]`) {
		t.Fatalf("空快照应上送空数组: %s", rawBody)
	}
}

// TestPushConfigDefinitionsForbidden - 项目未开启定义反推模式（403）→ 平台文案透传
func TestPushConfigDefinitionsForbidden(t *testing.T) {

	platform := newFakePlatform(t)
	platform.pushbackDefCode = http.StatusForbidden
	client := startPushbackClient(t, platform)

	_, err := client.PushConfigDefinitions(t.Context(), testDefinitions())
	if err == nil || !strings.Contains(err.Error(), "项目未开启定义反推模式") {
		t.Fatalf("403 应透传开关关闭文案，实际 %v", err)
	}
}

// TestPushConfigDefinitionsBadRequestErrors - 400 校验失败 → *PushbackDefinitionsError 携带 errors 明细
func TestPushConfigDefinitionsBadRequestErrors(t *testing.T) {

	platform := newFakePlatform(t)
	platform.pushbackDefCode = http.StatusBadRequest
	client := startPushbackClient(t, platform)

	_, err := client.PushConfigDefinitions(t.Context(), testDefinitions())
	var definitionsErr *PushbackDefinitionsError
	if !errors.As(err, &definitionsErr) {
		t.Fatalf("400 应映射为 *PushbackDefinitionsError，实际 %T %v", err, err)
	}
	if definitionsErr.Message != "配置定义校验失败" {
		t.Fatalf("汇总消息异常: %q", definitionsErr.Message)
	}
	if len(definitionsErr.Errors) != 2 {
		t.Fatalf("errors 明细应透传 2 条，实际 %+v", definitionsErr.Errors)
	}
	if definitionsErr.Errors[0].Where != "configs[0].options" || definitionsErr.Errors[1].Where != "configs[1].key" {
		t.Fatalf("errors where 定位异常: %+v", definitionsErr.Errors)
	}
	// Error() 单行文本应含汇总与逐条明细（CLI 直接打印 err 即可读全）
	text := err.Error()
	if !strings.Contains(text, "配置定义校验失败") || !strings.Contains(text, "configs[0].options：select 类型必须提供非空 options") {
		t.Fatalf("Error() 文本应含汇总与明细: %s", text)
	}
}

// TestGRPCRuntimeTransportPushbackDefinitions - gRPC 定义反推：签名 metadata 齐全 +
// 请求映射（options/rules 以 JSON 字符串进 proto）与响应映射（groups/configs 分别计数）
func TestGRPCRuntimeTransportPushbackDefinitions(t *testing.T) {

	server := &runtimePushbackServer{t: t}
	transport := newTestPushbackTransport(t, server)

	body, err := json.Marshal(pushbackDefinitionsBody{
		TenantID: 0, Groups: testDefinitions().Groups, Configs: testDefinitions().Configs, ClientPushID: "def-grpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/config-definitions", body, true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Fatalf("code=%d", code)
	}
	if server.defPushID != "def-grpc-1" || len(server.defGroups) != 2 || len(server.defConfigs) != 2 {
		t.Fatalf("gRPC 请求映射不符: pushID=%q groups=%d configs=%d", server.defPushID, len(server.defGroups), len(server.defConfigs))
	}
	if server.defGroups[1].GetParent() != "storage" || server.defGroups[0].GetLabelEn() != "Storage" || server.defGroups[0].GetSort() != 1 {
		t.Fatalf("gRPC 分组映射不符: %+v", server.defGroups[1])
	}
	first := server.defConfigs[0]
	if first.GetKey() != "storage.driver" || first.GetType() != "select" || first.GetGroupPath() != "storage" ||
		first.GetDefaultValue() != "local" || first.GetSensitive() || first.GetSort() != 10 {
		t.Fatalf("gRPC 配置项映射不符: %+v", first)
	}
	if first.GetOptions() != `[{"value":"local","label":"本地存储"},{"value":"oss","label":"阿里云 OSS"}]` {
		t.Fatalf("options 应以 JSON 字符串原文进 proto: %q", first.GetOptions())
	}
	if first.GetRules() != `{"required":true}` {
		t.Fatalf("rules 应以 JSON 字符串原文进 proto: %q", first.GetRules())
	}
	var response pushbackDefinitionsResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.BatchID != 8 || response.Groups.Created != 1 || response.Configs.Created != 2 ||
		response.Configs.Updated != 1 || response.Configs.Unchanged != 3 || response.PushedAt == "" {
		t.Fatalf("gRPC 响应映射不符: %s", raw)
	}
}

// TestGRPCRuntimeTransportPushbackDefinitionsPermissionDenied - gRPC PermissionDenied → 403 + message 体
// （对应项目未开启定义反推模式，与 HTTP 侧错误分层一致）
func TestGRPCRuntimeTransportPushbackDefinitionsPermissionDenied(t *testing.T) {

	transport := newTestPushbackTransport(t, &runtimePushbackServer{t: t, failCode: codes.PermissionDenied, failMessage: "项目未开启定义反推模式"})
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/config-definitions", []byte(`{"tenant_id":0,"groups":[],"configs":[]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusForbidden {
		t.Fatalf("gRPC PermissionDenied 应映射 403，实际 code=%d", code)
	}
	var response pushbackDefinitionsResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "项目未开启定义反推模式" {
		t.Fatalf("403 响应应携带服务端 message，实际 %s", raw)
	}
}

// TestGRPCRuntimeTransportPushbackDefinitionsInvalidArgument - gRPC InvalidArgument → 400 + message 体
func TestGRPCRuntimeTransportPushbackDefinitionsInvalidArgument(t *testing.T) {

	transport := newTestPushbackTransport(t, &runtimePushbackServer{t: t, failCode: codes.InvalidArgument, failMessage: "select 类型必须提供非空 options"})
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/config-definitions", []byte(`{"tenant_id":0,"groups":[],"configs":[{"key":"a"}]}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusBadRequest {
		t.Fatalf("gRPC InvalidArgument 应映射 400，实际 code=%d", code)
	}
	var response pushbackDefinitionsResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "select 类型必须提供非空 options" {
		t.Fatalf("400 响应应携带服务端 message，实际 %s", raw)
	}
}
