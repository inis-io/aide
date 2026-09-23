package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/inis-io/aide/licence/config"
	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// startPushbackClient - 起一个在假平台上完成激活的客户端（配置回推共用脚手架）
func startPushbackClient(t *testing.T, platform *fakePlatform) *Client {

	t.Helper()
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	t.Cleanup(client.Stop)
	return client
}

// assertUUIDv4 - 断言 SDK 生成的批次幂等号是 UUID 形态（36 字符、4 段连字符）
func assertUUIDv4(t *testing.T, id string) {

	t.Helper()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("client_push_id 应为 UUID 形态，实际: %q", id)
	}
}

// TestPushConfigHTTP - 项目级回推：tenant_id=0、SDK 自动生成 UUID 幂等号、diff 统计回填
func TestPushConfigHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client := startPushbackClient(t, platform)

	items := map[string]string{"feature.x": "on", "rate.limit": "200"}
	result, err := client.PushConfig(t.Context(), items)
	if err != nil {
		t.Fatalf("PushConfig 失败: %v", err)
	}
	if result.BatchID != 42 || result.Created != 2 || result.Updated != 0 || result.Deleted != 0 || result.Unchanged != 0 {
		t.Fatalf("diff 统计回填异常: %+v", result)
	}
	if result.PushedAt.IsZero() {
		t.Fatalf("PushedAt 应解析成功: %+v", result)
	}
	assertUUIDv4(t, result.ClientPushID)

	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.pushbacks) != 1 {
		t.Fatalf("平台应记录 1 批次，实际 %d", len(platform.pushbacks))
	}
	batch := platform.pushbacks[0]
	if batch.tenantID != 0 {
		t.Fatalf("项目级回推 tenant_id 应为 0，实际 %d", batch.tenantID)
	}
	if batch.clientPushID != result.ClientPushID {
		t.Fatalf("平台上送的幂等号应与返回值一致: %q != %q", batch.clientPushID, result.ClientPushID)
	}
	if len(batch.items) != 2 || batch.items["feature.x"] != "on" || batch.items["rate.limit"] != "200" {
		t.Fatalf("快照内容上送异常: %+v", batch.items)
	}
}

// TestPushTenantConfigHTTP - 租户级回推：tenant_id 上送 + 调用方自定 client_push_id
func TestPushTenantConfigHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client := startPushbackClient(t, platform)

	result, err := client.PushTenantConfig(t.Context(), 88, map[string]string{"theme": "dark"}, "batch-custom-1")
	if err != nil {
		t.Fatalf("PushTenantConfig 失败: %v", err)
	}
	if result.ClientPushID != "batch-custom-1" {
		t.Fatalf("自定幂等号应原样生效，实际 %q", result.ClientPushID)
	}

	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.pushbacks) != 1 {
		t.Fatalf("平台应记录 1 批次，实际 %d", len(platform.pushbacks))
	}
	batch := platform.pushbacks[0]
	if batch.tenantID != 88 || batch.clientPushID != "batch-custom-1" || batch.items["theme"] != "dark" {
		t.Fatalf("租户级批次记录异常: %+v", batch)
	}
}

// TestPushTenantConfigRequiresTenantID - tenantID=0 直接报错，不发请求
func TestPushTenantConfigRequiresTenantID(t *testing.T) {

	platform := newFakePlatform(t)
	client := startPushbackClient(t, platform)

	if _, err := client.PushTenantConfig(t.Context(), 0, map[string]string{"a": "1"}); err == nil {
		t.Fatal("tenantID=0 应报错")
	}
	platform.mu.Lock()
	defer platform.mu.Unlock()
	if len(platform.pushbacks) != 0 {
		t.Fatalf("报错分支不应发出请求，实际记录 %d 批次", len(platform.pushbacks))
	}
}

// TestPushConfigNotFound - 平台模糊 404（许可证/实例/租户不存在）→ 统一错误语义
func TestPushConfigNotFound(t *testing.T) {

	platform := newFakePlatform(t)
	platform.pushbackCode = http.StatusNotFound
	client := startPushbackClient(t, platform)

	_, err := client.PushConfig(t.Context(), map[string]string{"feature.x": "on"})
	if err == nil || err.Error() != "许可证、实例或租户信息无效" {
		t.Fatalf("404 应映射为模糊 NotFound 错误，实际 %v", err)
	}
}

// TestPushConfigBadRequest - 平台 400 校验失败 → 错误携带平台 message
func TestPushConfigBadRequest(t *testing.T) {

	platform := newFakePlatform(t)
	platform.pushbackCode = http.StatusBadRequest
	client := startPushbackClient(t, platform)

	_, err := client.PushConfig(t.Context(), map[string]string{"Bad.Key": "1"})
	if err == nil || !strings.Contains(err.Error(), "配置键非法：Bad.Key") {
		t.Fatalf("400 应携带平台校验消息，实际 %v", err)
	}
}

// runtimePushbackServer - 配置回推 gRPC 假服务端：校验签名 metadata 齐全，记录上送字段
type runtimePushbackServer struct {
	licencev1.UnimplementedConfigPushbackRuntimeServiceServer
	t        *testing.T
	tenantID uint64
	items    map[string]string
	pushID   string
	failCode codes.Code
	// failMessage - PushDefinitions 失败分支携带的消息（缺省按通用文案）
	failMessage string
	// defGroups/defConfigs/defPushID - PushDefinitions 记录的上送内容
	defGroups  []*licencev1.ConfigDefinitionGroup
	defConfigs []*licencev1.ConfigDefinitionItem
	defPushID  string
}

func (s *runtimePushbackServer) Push(ctx context.Context, request *licencev1.ConfigPushbackPushRequest) (*licencev1.ConfigPushbackPushResponse, error) {

	s.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			s.t.Fatalf("缺少签名 metadata %s", key)
		}
	}
	if s.failCode != codes.OK {
		return nil, status.Error(s.failCode, "配置键非法：Bad.Key")
	}
	s.tenantID = request.GetTenantId()
	s.items = request.GetItems()
	s.pushID = request.GetClientPushId()
	return &licencev1.ConfigPushbackPushResponse{
		BatchId: 7, Created: 2, Updated: 1, Deleted: 0, Unchanged: 5, PushedAt: "2026-08-10T12:00:00Z",
	}, nil
}

// PushDefinitions - 配置定义反推 gRPC 假服务端：同 Push 的签名 metadata 校验，记录定义快照
func (s *runtimePushbackServer) PushDefinitions(ctx context.Context, request *licencev1.ConfigPushbackDefinitionsRequest) (*licencev1.ConfigPushbackDefinitionsResponse, error) {

	s.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			s.t.Fatalf("缺少签名 metadata %s", key)
		}
	}
	if s.failCode != codes.OK {
		message := s.failMessage
		if message == "" {
			message = "配置定义校验失败"
		}
		return nil, status.Error(s.failCode, message)
	}
	s.defGroups = request.GetGroups()
	s.defConfigs = request.GetConfigs()
	s.defPushID = request.GetClientPushId()
	return &licencev1.ConfigPushbackDefinitionsResponse{
		BatchId:  8,
		Groups:   &licencev1.ConfigPushbackDiffStats{Created: 1},
		Configs:  &licencev1.ConfigPushbackDiffStats{Created: 2, Updated: 1, Unchanged: 3},
		PushedAt: "2026-08-10T12:00:00Z",
	}, nil
}

// newTestPushbackTransport - bufconn 拨号的配置回推 gRPC 传输（已注入激活凭证）
func newTestPushbackTransport(t *testing.T, pushbackServer licencev1.ConfigPushbackRuntimeServiceServer) *grpcRuntimeTransport {

	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterConfigPushbackRuntimeServiceServer(server, pushbackServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{HTTPTimeout: time.Second}, state: runtimeState{ActivationToken: "token", ClientSeed: hex.EncodeToString(seed)}}
	return &grpcRuntimeTransport{client: client, conn: conn, pushback: licencev1.NewConfigPushbackRuntimeServiceClient(conn)}
}

// TestGRPCRuntimeTransportPushback - gRPC 配置回推：签名 metadata 齐全 + 请求/响应双协议映射一致
func TestGRPCRuntimeTransportPushback(t *testing.T) {

	server := &runtimePushbackServer{t: t}
	transport := newTestPushbackTransport(t, server)

	body, err := json.Marshal(pushbackBody{
		TenantID: 88, Items: map[string]string{"theme": "dark", "rate.limit": "200"}, ClientPushID: "batch-grpc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/configs", body, true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Fatalf("code=%d", code)
	}
	if server.tenantID != 88 || server.pushID != "batch-grpc-1" || len(server.items) != 2 || server.items["theme"] != "dark" {
		t.Fatalf("gRPC 请求映射不符: tenant=%d pushID=%q items=%+v", server.tenantID, server.pushID, server.items)
	}
	var response pushbackResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.BatchID != 7 || response.Created != 2 || response.Updated != 1 || response.Unchanged != 5 || response.PushedAt == "" {
		t.Fatalf("gRPC 响应映射不符: %s", raw)
	}
}

// TestGRPCRuntimeTransportPushbackNotFound - gRPC NotFound → 404（与 HTTP 模糊 NotFound 语义一致）
func TestGRPCRuntimeTransportPushbackNotFound(t *testing.T) {

	transport := newTestPushbackTransport(t, &runtimePushbackServer{t: t, failCode: codes.NotFound})
	code, _, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/configs", []byte(`{"tenant_id":0,"items":{}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusNotFound {
		t.Fatalf("gRPC NotFound 应映射 404，实际 code=%d", code)
	}
}

// TestGRPCRuntimeTransportPushbackInvalidArgument - gRPC InvalidArgument → 400 + message 体（对齐 HTTP 校验失败分层）
func TestGRPCRuntimeTransportPushbackInvalidArgument(t *testing.T) {

	transport := newTestPushbackTransport(t, &runtimePushbackServer{t: t, failCode: codes.InvalidArgument})
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/pushback/configs", []byte(`{"tenant_id":0,"items":{"Bad.Key":"1"}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusBadRequest {
		t.Fatalf("gRPC InvalidArgument 应映射 400，实际 code=%d", code)
	}
	var response pushbackResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "配置键非法：Bad.Key" {
		t.Fatalf("400 响应应携带服务端 message，实际 %s", raw)
	}
}

// TestParsePushbackTime - pushed_at 解析：RFC3339 / 平台本地格式 / 空值 / 非法值
func TestParsePushbackTime(t *testing.T) {

	parsed, err := parsePushbackTime("2026-08-10T12:00:00Z")
	if err != nil || parsed.UTC().Format("2006-01-02 15:04:05") != "2026-08-10 12:00:00" {
		t.Fatalf("RFC3339 解析异常: %v %v", parsed, err)
	}
	parsed, err = parsePushbackTime("2026-08-10 20:00:00")
	if err != nil || parsed.UTC().Format("2006-01-02 15:04:05") != "2026-08-10 12:00:00" {
		t.Fatalf("本地格式应按 UTC+8 解释: %v %v", parsed, err)
	}
	if parsed, err = parsePushbackTime(""); err != nil || !parsed.IsZero() {
		t.Fatalf("空值应返回零时间: %v %v", parsed, err)
	}
	if _, err = parsePushbackTime("not-a-time"); err == nil {
		t.Fatal("非法格式应报错")
	}
}

// ============================= 配置定义回推 =============================

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

// ============================= 配置定义反推校验 =============================

// 引擎级测试（ParseConfigRuleSet / ValidateConfigDefinition / ValidateConfigValue）
// 已随校验引擎迁往 config 子包（config/validate_test.go）；
// 本文件只保留 Client 薄壳方法（ValidateConfig / ValidateConfigDefinitions）的装配级测试。

// TestClientValidateConfig - 本地快照值预校验：逐 key 校验、敏感跳过、无定义放行、快照空放行
func TestClientValidateConfig(t *testing.T) {

	client := &Client{}
	client.state.PlatformConfigs = map[string]PlatformConfigItem{
		"rate.limit": {
			Key: "rate.limit", Type: "number",
			Rules: json.RawMessage(`{"min":0,"max":100}`),
		},
		"app.title": {
			Key: "app.title", Type: "input",
			Rules: json.RawMessage(`{"required":true,"minLen":2}`),
		},
		"storage.driver": {
			Key: "storage.driver", Type: "select",
			Options: json.RawMessage(`[{"value":"local","label":"本地"}]`),
		},
		"security.api_token": {
			Key: "security.api_token", Type: "number", Sensitive: true,
		},
		"feature.mode": {
			Key: "feature.mode", Type: "input",
			Rules: json.RawMessage(`{"enum":["a","b"]}`),
		},
	}

	// 全部合法 → nil
	failures := client.ValidateConfig(map[string]string{
		"rate.limit": "50", "app.title": "系统", "storage.driver": "local", "feature.mode": "a",
	})
	if len(failures) != 0 {
		t.Fatalf("合法值应全部通过: %+v", failures)
	}

	// 逐条失败定位：where = key
	failures = client.ValidateConfig(map[string]string{
		"rate.limit": "101", "app.title": "", "storage.driver": "cos", "feature.mode": "c",
	})
	if len(failures) != 4 {
		t.Fatalf("应产生 4 条失败，实际 %+v", failures)
	}
	for _, failure := range failures {
		if failure.Where == "" || failure.Message == "" {
			t.Fatalf("失败明细缺 where/message: %+v", failure)
		}
	}
	if failures[0].Where != "app.title" { // 输出按 key 排序，首条应为 app.title（required 违规）
		t.Fatalf("失败明细应按键排序，实际 %+v", failures)
	}

	// 敏感定义跳过（脱敏值不做真值校验）；无定义放行（前向兼容）
	failures = client.ValidateConfig(map[string]string{
		"security.api_token": "not-a-number", "unknown.key": "anything",
	})
	if len(failures) != 0 {
		t.Fatalf("敏感键与未定义键应放行: %+v", failures)
	}

	// 快照为空（未同步/项目无配置项）→ nil，以平台侧校验为准
	emptyClient := &Client{}
	if failures = emptyClient.ValidateConfig(map[string]string{"rate.limit": "abc"}); failures != nil {
		t.Fatalf("空快照应放行: %+v", failures)
	}
}

// TestClientValidateConfigDefinitions - 定义快照预校验：分组闭包/深层链、key 唯一、逐条结构校验
func TestClientValidateConfigDefinitions(t *testing.T) {

	client := &Client{}

	// 合法快照：深层分组链 + 引用分组路径
	valid := config.ConfigDefinitions{
		Groups: []config.ConfigDefinitionGroup{
			{Name: "storage", Label: "存储"},
			{Name: "oss", Label: "对象存储", Parent: "storage"},
			{Name: "advanced", Label: "高级", Parent: "storage/oss"},
		},
		Configs: []config.ConfigDefinitionItem{
			{Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage",
				Options: json.RawMessage(`[{"value":"local","label":"本地"}]`)},
			{Key: "storage.oss.ak", Label: "AK", Type: "input", GroupPath: "storage/oss/advanced"},
			{Key: "app.title", Label: "标题", Type: "input"}, // 未分组
		},
	}
	if failures := client.ValidateConfigDefinitions(valid); len(failures) != 0 {
		t.Fatalf("合法快照应通过: %+v", failures)
	}

	// key 快照内重复
	dupKey := valid
	dupKey.Configs = append(append([]config.ConfigDefinitionItem{}, valid.Configs...), valid.Configs[0])
	failures := client.ValidateConfigDefinitions(dupKey)
	assertHasFailure(t, failures, "configs[3].key", "重复")

	// 分组 name 非法 + 路径重复
	badGroups := config.ConfigDefinitions{Groups: []config.ConfigDefinitionGroup{
		{Name: "Bad Name", Label: "非法"},
		{Name: "a", Label: "A"},
		{Name: "a", Label: "A 重复"},
	}}
	failures = client.ValidateConfigDefinitions(badGroups)
	assertHasFailure(t, failures, "groups[0].name", "格式非法")
	assertHasFailure(t, failures, "groups[2].name", "重复")

	// parent 闭包：父路径不在快照内
	dangling := config.ConfigDefinitions{Groups: []config.ConfigDefinitionGroup{
		{Name: "child", Label: "子", Parent: "missing"},
	}}
	failures = client.ValidateConfigDefinitions(dangling)
	assertHasFailure(t, failures, "groups[0].parent", "不在快照内")

	// groupPath 悬空（引用的分组不在快照）
	danglingRef := valid
	danglingRef.Configs = append(append([]config.ConfigDefinitionItem{}, valid.Configs...),
		config.ConfigDefinitionItem{Key: "x.y", Label: "悬空", Type: "input", GroupPath: "storage/missing"})
	failures = client.ValidateConfigDefinitions(danglingRef)
	assertHasFailure(t, failures, "configs[3].group_path", "不存在")

	// 逐条结构校验聚合进统一定位（configs[i].field 形态，与平台 errors 数组同构）
	badItem := config.ConfigDefinitions{Configs: []config.ConfigDefinitionItem{
		{Key: "a.b", Label: "缺选项", Type: "select"},
	}}
	failures = client.ValidateConfigDefinitions(badItem)
	assertHasFailure(t, failures, "configs[0].options", "非空 options")
}

// assertHasFailure - 断言失败明细中存在指定 where 且消息含给定片段
func assertHasFailure(t *testing.T, failures []config.ConfigValidationError, where string, messagePart string) {

	t.Helper()
	for _, failure := range failures {
		if failure.Where == where && strings.Contains(failure.Message, messagePart) {
			return
		}
	}
	t.Fatalf("应存在 where=%q 且含 %q 的失败，实际 %+v", where, messagePart, failures)
}
