package licence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
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

type runtimeLicenseServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	t *testing.T
}

type rejectingRuntimeLicenseServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
}

func (rejectingRuntimeLicenseServer) Activate(context.Context, *licencev1.ActivateRequest) (*licencev1.RuntimeResponse, error) {
	return &licencev1.RuntimeResponse{Status: StatusExpired, ServerTime: time.Now().UnixMilli()}, nil
}

func (s runtimeLicenseServer) check(ctx context.Context, signed bool) {
	s.t.Helper()
	if !signed {
		return
	}
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			s.t.Fatalf("缺少签名 metadata %s", key)
		}
	}
}
func (s runtimeLicenseServer) Activate(context.Context, *licencev1.ActivateRequest) (*licencev1.RuntimeResponse, error) {
	return &licencev1.RuntimeResponse{Status: StatusValid, ServerTime: time.Now().UnixMilli()}, nil
}
func (s runtimeLicenseServer) Validate(ctx context.Context, _ *licencev1.ValidateRequest) (*licencev1.RuntimeResponse, error) {
	s.check(ctx, true)
	return &licencev1.RuntimeResponse{Status: StatusValid, ServerTime: time.Now().UnixMilli()}, nil
}
func (s runtimeLicenseServer) Current(ctx context.Context, _ *licencev1.CurrentLicenseRequest) (*licencev1.RuntimeResponse, error) {
	s.check(ctx, true)
	return &licencev1.RuntimeResponse{Status: StatusValid, ServerTime: time.Now().UnixMilli()}, nil
}

type runtimeUpdateServer struct {
	licencev1.UnimplementedUpdateRuntimeServiceServer
}

func (runtimeUpdateServer) Check(context.Context, *licencev1.UpdateCheckRequest) (*licencev1.UpdateCheckResponse, error) {
	return &licencev1.UpdateCheckResponse{Status: StatusValid}, nil
}
func (runtimeUpdateServer) Report(context.Context, *licencev1.UpdateReportRequest) (*licencev1.UpdateReportResponse, error) {
	return &licencev1.UpdateReportResponse{Status: StatusValid}, nil
}
func (runtimeUpdateServer) AppendLogs(context.Context, *licencev1.UpdateLogsRequest) (*licencev1.UpdateReportResponse, error) {
	return &licencev1.UpdateReportResponse{Status: StatusValid}, nil
}

type runtimeSaasServer struct {
	licencev1.UnimplementedSaasRuntimeServiceServer
}

func (runtimeSaasServer) Sync(context.Context, *licencev1.TenantSyncRequest) (*licencev1.TenantSyncResponse, error) {
	return &licencev1.TenantSyncResponse{Status: StatusValid}, nil
}
func (runtimeSaasServer) Validate(context.Context, *licencev1.TenantValidateRequest) (*licencev1.TenantResponse, error) {
	return &licencev1.TenantResponse{Status: StatusValid}, nil
}
func (runtimeSaasServer) Current(context.Context, *licencev1.TenantCurrentRequest) (*licencev1.TenantResponse, error) {
	return &licencev1.TenantResponse{Status: StatusValid}, nil
}

type runtimeConfigServer struct {
	licencev1.UnimplementedProjectConfigRuntimeServiceServer
}

func (runtimeConfigServer) Sync(context.Context, *licencev1.ProjectConfigSyncRequest) (*licencev1.ProjectConfigSyncResponse, error) {
	return &licencev1.ProjectConfigSyncResponse{Status: StatusValid}, nil
}

type runtimePlatformConfigServer struct {
	licencev1.UnimplementedPlatformConfigRuntimeServiceServer
}

func (runtimePlatformConfigServer) Sync(context.Context, *licencev1.PlatformConfigSyncRequest) (*licencev1.PlatformConfigSyncResponse, error) {
	return &licencev1.PlatformConfigSyncResponse{Status: StatusValid}, nil
}

// runtimeEventServer - 事件订阅 gRPC 服务端流假实现：
// 校验签名 metadata 齐全；按 sinceEventId 过滤并现场重签；failCode 非零时直接报错（非放行态模拟）
type runtimeEventServer struct {
	licencev1.UnimplementedEventRuntimeServiceServer
	t        *testing.T
	seed     []byte
	events   []fakeEvent
	failCode codes.Code
}

func (s runtimeEventServer) Subscribe(request *licencev1.EventSubscribeRequest, stream licencev1.EventRuntimeService_SubscribeServer) error {
	s.t.Helper()
	md, _ := metadata.FromIncomingContext(stream.Context())
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			s.t.Fatalf("缺少签名 metadata %s", key)
		}
	}
	if s.failCode != codes.OK {
		return status.Error(s.failCode, "许可证非放行态：SUSPENDED")
	}
	for _, event := range s.events {
		if event.eventId <= request.GetSinceEventId() {
			continue
		}
		envelope, err := signCallbackEnvelope(s.seed, event)
		if err != nil {
			return err
		}
		if err := stream.Send(&licencev1.EventMessage{EventId: event.eventId, EnvelopeJson: envelope}); err != nil {
			return err
		}
	}
	return nil
}

func newTestEventTransport(t *testing.T, eventServer licencev1.EventRuntimeServiceServer) (*grpcRuntimeTransport, error) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterEventRuntimeServiceServer(server, eventServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = conn.Close() })
	seed, _, err := generateKeyPair()
	if err != nil {
		return nil, err
	}
	client := &Client{options: Options{HTTPTimeout: time.Second}, state: runtimeState{ActivationToken: "token", ClientSeed: hex.EncodeToString(seed)}}
	return &grpcRuntimeTransport{client: client, conn: conn, event: licencev1.NewEventRuntimeServiceClient(conn)}, nil
}

// TestGRPCRejectedActivationDoesNotPoisonState - gRPC 拒绝响应与 HTTP 共用无信封不落盘语义。
func TestGRPCRejectedActivationDoesNotPoisonState(t *testing.T) {

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	licencev1.RegisterLicenseRuntimeServiceServer(server, rejectingRuntimeLicenseServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	dir := t.TempDir()
	options := Options{
		ServerURL: "grpc://" + listener.Addr().String(), LicenseNo: "LIC-2026-000123", Salt: "test-salt",
		PublicKeys: map[string]string{"license-key-2026-01": "unused-for-rejected-activation"},
		StorageDir: dir, Fingerprint: "test-fingerprint", HTTPTimeout: time.Second,
		Transport: TransportGRPC, GRPC: GRPCOptions{AllowInsecure: true},
	}

	first, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err = first.Start(t.Context()); err == nil || err.Error() != "激活被拒绝："+StatusExpired {
		t.Fatalf("gRPC 首次激活应返回业务拒绝，实际: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("gRPC 激活被拒绝不应保留无信封状态文件: %v", entries)
	}

	second, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err = second.Start(t.Context()); err == nil || err.Error() != "激活被拒绝："+StatusExpired {
		t.Fatalf("gRPC 重启后应继续返回业务拒绝，实际: %v", err)
	}
}

// TestGRPCRuntimeTransportSubscribeEvents - gRPC 服务端流订阅：收集事件 + metadata 签名齐全 + 非放行态错误归一
func TestGRPCRuntimeTransportSubscribeEvents(t *testing.T) {

	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	server := runtimeEventServer{t: t, seed: seed, events: []fakeEvent{
		{eventId: 1, eventNo: "EVT-2026-000001", event: EventSaasTenantCreated, data: json.RawMessage(`{"tenantNo":"T1"}`)},
		{eventId: 2, eventNo: "EVT-2026-000002", event: EventSaasPlanUpdated, data: json.RawMessage(`{"planCode":"pro"}`)},
	}}
	transport, err := newTestEventTransport(t, server)
	if err != nil {
		t.Fatal(err)
	}

	result, err := transport.SubscribeEvents(context.Background(), "LIC-2026-000123", 0, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("应收集 2 条事件，实际 %d", len(result.Events))
	}
	if result.Events[0].EventId != 1 || result.Events[1].EventId != 2 {
		t.Fatalf("事件顺序错误: %d %d", result.Events[0].EventId, result.Events[1].EventId)
	}
	envelope, _, err := ParseCallbackEnvelope(result.Events[1].Envelope)
	if err != nil || envelope.Payload.Event != EventSaasPlanUpdated {
		t.Fatalf("信封解析失败: %v %v", envelope.Payload.Event, err)
	}

	// sinceEventId 过滤：只返回 > since 的事件
	result, err = transport.SubscribeEvents(context.Background(), "LIC-2026-000123", 1, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].EventId != 2 {
		t.Fatalf("sinceEventId 过滤错误: %d 条 %+v", len(result.Events), result.Events)
	}
}

// TestGRPCRuntimeTransportSubscribeNonPassThrough - 非放行态 gRPC 服务端流报错 → 归一为同一错误文本
func TestGRPCRuntimeTransportSubscribeNonPassThrough(t *testing.T) {

	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newTestEventTransport(t, runtimeEventServer{t: t, seed: seed, failCode: codes.FailedPrecondition})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.SubscribeEvents(context.Background(), "LIC-2026-000123", 0, 100*time.Millisecond)
	if err == nil || err.Error() != "许可证非放行态：SUSPENDED" {
		t.Fatalf("非放行态错误文本应归一为'许可证非放行态：SUSPENDED'，实际 %v", err)
	}
}

func TestGRPCRuntimeTransportMapsAllRoutes(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterLicenseRuntimeServiceServer(server, runtimeLicenseServer{t: t})
	licencev1.RegisterUpdateRuntimeServiceServer(server, runtimeUpdateServer{})
	licencev1.RegisterSaasRuntimeServiceServer(server, runtimeSaasServer{})
	licencev1.RegisterProjectConfigRuntimeServiceServer(server, runtimeConfigServer{})
	licencev1.RegisterPlatformConfigRuntimeServiceServer(server, runtimePlatformConfigServer{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{HTTPTimeout: time.Second}, state: runtimeState{ActivationToken: "token", ClientSeed: hex.EncodeToString(seed)}}
	transport := &grpcRuntimeTransport{client: client, conn: conn, license: licencev1.NewLicenseRuntimeServiceClient(conn), update: licencev1.NewUpdateRuntimeServiceClient(conn), saas: licencev1.NewSaasRuntimeServiceClient(conn), config: licencev1.NewProjectConfigRuntimeServiceClient(conn), platformConfig: licencev1.NewPlatformConfigRuntimeServiceClient(conn)}
	cases := []struct {
		name, method, uri, body string
		signed                  bool
	}{
		{"activate", http.MethodPost, "/api/v1/licenses/activate", `{"licenseNo":"LIC-1"}`, false}, {"validate", http.MethodPost, "/api/v1/licenses/validate", `{"licenseNo":"LIC-1"}`, true}, {"current", http.MethodGet, "/api/v1/licenses/current?licenseNo=LIC-1", "", true},
		{"update-check", http.MethodPost, "/api/v1/updates/check", `{"licenseNo":"LIC-1"}`, true}, {"update-report", http.MethodPost, "/api/v1/updates/report", `{"licenseNo":"LIC-1"}`, true}, {"update-logs", http.MethodPost, "/api/v1/updates/logs", `{"licenseNo":"LIC-1"}`, true},
		{"tenant-sync", http.MethodPost, "/api/v1/saas/tenants/sync", `{"licenseNo":"LIC-1"}`, true}, {"tenant-validate", http.MethodPost, "/api/v1/saas/tenants/validate", `{"licenseNo":"LIC-1","tenantCode":"t1"}`, true}, {"tenant-current", http.MethodGet, "/api/v1/saas/tenants/current?licenseNo=LIC-1&tenantCode=t1", "", true}, {"config-sync", http.MethodPost, "/api/v1/projects/configs/sync", `{"licenseNo":"LIC-1"}`, true}, {"platform-config-sync", http.MethodPost, "/api/v1/platform/configs/sync", `{"licenseNo":"LIC-1"}`, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			code, _, err := transport.RoundTrip(context.Background(), item.method, item.uri, []byte(item.body), item.signed)
			if err != nil {
				t.Fatal(err)
			}
			if code != http.StatusOK {
				t.Fatalf("code=%d", code)
			}
		})
	}
	if err = transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err = transport.Close(); err != nil {
		t.Fatal(err)
	}
}

// runtimeSeatServer - 席位映射断言用运行面假服务端：
// Activate 捕获 deviceName 上送并返回 seatNo/activationNo/expiresAt，
// 验证 gRPC activate 的 device_name 上送与 seat_no 回填映射
type runtimeSeatServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	t          *testing.T
	deviceName string
}

func (s *runtimeSeatServer) Activate(_ context.Context, request *licencev1.ActivateRequest) (*licencev1.RuntimeResponse, error) {
	s.deviceName = request.GetDeviceName()
	return &licencev1.RuntimeResponse{
		Status: StatusValid, ServerTime: time.Now().UnixMilli(),
		ActivationNo: "ACT-2026-000007", SeatNo: "SEAT-2026-000007",
		ExpiresAt: time.Now().UnixMilli() + 7*24*3600*1000,
	}, nil
}

// TestGRPCRuntimeActivateMapsDeviceNameAndSeatNo - gRPC activate：
// deviceName 随 ActivateRequest 上送，seatNo 经 runtimeMap 回填进协议无关 JSON 响应
func TestGRPCRuntimeActivateMapsDeviceNameAndSeatNo(t *testing.T) {

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	seatServer := &runtimeSeatServer{t: t}
	licencev1.RegisterLicenseRuntimeServiceServer(server, seatServer)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{HTTPTimeout: time.Second}, state: runtimeState{ActivationToken: "token", ClientSeed: hex.EncodeToString(seed)}}
	transport := &grpcRuntimeTransport{client: client, conn: conn, license: licencev1.NewLicenseRuntimeServiceClient(conn)}

	body, err := json.Marshal(activateBody{
		LicenseNo: "LIC-2026-000123", FingerprintHash: "fp-hash", ClientPublicKey: "pub", DeviceName: "dev-notebook",
	})
	if err != nil {
		t.Fatal(err)
	}
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost, "/api/v1/licenses/activate", body, false)
	if err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK {
		t.Fatalf("code=%d", code)
	}
	if seatServer.deviceName != "dev-notebook" {
		t.Fatalf("deviceName 未随 gRPC 请求上送: %q", seatServer.deviceName)
	}
	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.SeatNo != "SEAT-2026-000007" || response.ActivationNo != "ACT-2026-000007" {
		t.Fatalf("seatNo 回填映射不符: %s", string(raw))
	}
	if response.ExpiresAt <= 0 {
		t.Fatalf("expiresAt 回填映射不符: %s", string(raw))
	}
}
