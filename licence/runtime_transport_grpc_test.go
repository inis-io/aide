package licence

import (
	"context"
	"encoding/hex"
	"net"
	"net/http"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type runtimeLicenseServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	t *testing.T
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
