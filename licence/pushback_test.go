package licence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
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
	seed, _, err := generateKeyPair()
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
