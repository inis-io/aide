package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
)

// autoTestPlatform - AutoStart 双协议假平台，记录申领/挑战/激活次数与指纹。
type autoTestPlatform struct {
	*keysetTestPlatform
	requireFingerprint     bool
	autoMu                 sync.Mutex
	provisionRequests      int
	provisioned            int
	activateRequests       int
	provisionFingerprint   string
	provisionMigrationCode string
	activateFingerprint    string
	customer               *CustomerView
}

func newAutoTestPlatform(t *testing.T) *autoTestPlatform {
	return &autoTestPlatform{keysetTestPlatform: newKeysetTestPlatform(t), requireFingerprint: true}
}

func (this *autoTestPlatform) provision(fingerprint, migrationCode string) (status, salt string) {
	this.autoMu.Lock()
	this.provisionRequests++
	this.provisionMigrationCode = migrationCode
	if this.requireFingerprint && fingerprint == "" {
		this.autoMu.Unlock()
		return "FINGERPRINT_REQUIRED", "zero-salt"
	}
	this.provisioned++
	this.provisionFingerprint = fingerprint
	this.autoMu.Unlock()
	this.issue(1, "license-key-v1", false)
	return "OK", "zero-salt"
}

func (this *autoTestPlatform) migrationCode() string {
	this.autoMu.Lock()
	defer this.autoMu.Unlock()
	return this.provisionMigrationCode
}

func (this *autoTestPlatform) activate(fingerprint string) {
	this.autoMu.Lock()
	this.activateRequests++
	this.activateFingerprint = fingerprint
	this.autoMu.Unlock()
}

func (this *autoTestPlatform) counts() (requests, provisioned, activations int, provisionFingerprint, activateFingerprint string) {
	this.autoMu.Lock()
	defer this.autoMu.Unlock()
	return this.provisionRequests, this.provisioned, this.activateRequests, this.provisionFingerprint, this.activateFingerprint
}

// setCustomer - 让申领响应携带客户账号视图（迁移/已绑定场景平台会下发）。
func (this *autoTestPlatform) setCustomer(customer *CustomerView) {
	this.autoMu.Lock()
	this.customer = customer
	this.autoMu.Unlock()
}

// customerView - 读取当前应下发的客户视图。
func (this *autoTestPlatform) customerView() *CustomerView {
	this.autoMu.Lock()
	defer this.autoMu.Unlock()
	return this.customer
}

func (this *autoTestPlatform) httpServer() *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/licenses/provision":
			var body provisionBody
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				this.t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			status, salt := this.provision(body.FingerprintHash, body.MigrationCode)
			if status != "OK" {
				writeJson(writer, map[string]any{"status": status, "salt": salt, "serverTime": time.Now().UnixMilli()})
				return
			}
			keysetRaw, _, _ := this.snapshot()
			response := map[string]any{
				"status": status, "salt": salt, "serverTime": time.Now().UnixMilli(),
				"licenseNo": "LIC-ZERO-000001", "bindingPolicy": "single", "seatLimit": 1,
				"expiresAt": time.Now().Add(time.Hour).UnixMilli(), "keyset": keysetRaw,
			}
			if customer := this.customerView(); customer != nil {
				response["customer"] = map[string]any{"maskAccount": customer.MaskAccount, "boundAt": customer.BoundAt}
			}
			writeJson(writer, response)
		case "/api/v1/licenses/activate":
			var body activateBody
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				this.t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			this.activate(body.FingerprintHash)
			this.writeRuntime(writer)
		case "/api/v1/licenses/validate", "/api/v1/licenses/current":
			this.writeRuntime(writer)
		case "/api/v1/commands/pull":
			writeJson(writer, map[string]any{"status": LicenceProtocol.StatusValid, "serverTime": time.Now().UnixMilli(), "commands": []any{}})
		default:
			writeNotFound(writer)
		}
	}))
	this.t.Cleanup(server.Close)
	return server
}

// autoGRPCLicenseServer - gRPC 首装与运行面假平台。
type autoGRPCLicenseServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	platform *autoTestPlatform
}

func (this *autoGRPCLicenseServer) ProvisionByPlan(_ context.Context, request *licencev1.ProvisionRequest) (*licencev1.ProvisionResponse, error) {
	status, salt := this.platform.provision(request.GetFingerprintHash(), request.GetMigrationCode())
	if status != "OK" {
		return &licencev1.ProvisionResponse{Status: status, Salt: salt, ServerTime: time.Now().UnixMilli()}, nil
	}
	keysetRaw, _, _ := this.platform.snapshot()
	response := &licencev1.ProvisionResponse{
		Status: status, Salt: salt, ServerTime: time.Now().UnixMilli(), LicenseNo: "LIC-ZERO-000001",
		BindingPolicy: "single", SeatLimit: 1, ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), KeysetJson: keysetRaw,
	}
	if customer := this.platform.customerView(); customer != nil {
		response.Customer = &licencev1.ProvisionCustomerView{MaskAccount: customer.MaskAccount, BoundAt: customer.BoundAt}
	}
	return response, nil
}
func (this *autoGRPCLicenseServer) Activate(_ context.Context, request *licencev1.ActivateRequest) (*licencev1.RuntimeResponse, error) {
	this.platform.activate(request.GetFingerprintHash())
	keysetRaw, envelopeRaw, _ := this.platform.snapshot()
	return &licencev1.RuntimeResponse{
		Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(), KeysetJson: keysetRaw, EnvelopeJson: envelopeRaw,
		ActivationNo: "ACT-ZERO-GRPC", ActivationToken: "auto-grpc-token", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
	}, nil
}
func (this *autoGRPCLicenseServer) Validate(context.Context, *licencev1.ValidateRequest) (*licencev1.RuntimeResponse, error) {
	keysetRaw, envelopeRaw, _ := this.platform.snapshot()
	return &licencev1.RuntimeResponse{Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(), KeysetJson: keysetRaw, EnvelopeJson: envelopeRaw}, nil
}
func (this *autoGRPCLicenseServer) Current(context.Context, *licencev1.CurrentLicenseRequest) (*licencev1.RuntimeResponse, error) {
	keysetRaw, envelopeRaw, _ := this.platform.snapshot()
	return &licencev1.RuntimeResponse{Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(), KeysetJson: keysetRaw, EnvelopeJson: envelopeRaw}, nil
}

// TestOfficialAutoDefaults - 官方零配置只内置公开地址与迁移后的 bootstrap 根公钥。
func TestOfficialAutoDefaults(t *testing.T) {
	if DefaultServerURL != "https://licen-hub.inis.cn" {
		t.Fatalf("官方平台地址漂移: %s", DefaultServerURL)
	}
	if len(DefaultBootstrapPublicKeys) != 1 || DefaultBootstrapPublicKeys["bootstrap-key-2026-01"] != "cbc89be1e4fc10ed0411ac1fc4f26a6e24aec42a815fe8c09decfd405586ae6a" {
		t.Fatalf("官方 bootstrap 根漂移: %+v", DefaultBootstrapPublicKeys)
	}
}

// TestAutoStartRejectsMultipleVersions - appVersion 是可选项但最多一个，禁止猜测版本。
func TestAutoStartRejectsMultipleVersions(t *testing.T) {
	if _, err := AutoStart(t.Context(), "PRJ-ZERO", "1.0.0", "2.0.0"); err == nil {
		t.Fatal("多个 appVersion 必须返回错误")
	}
}

// TestAutoStartFirstInstallAndOfflineRestart - 只传 projectNo 即可完成首装；挑战仅重试一次，
// auto.json 回填实例号，断网重启不重复 provision，也不误报失败。
func TestAutoStartFirstInstallAndOfflineRestart(t *testing.T) {
	platform := newAutoTestPlatform(t)
	server := platform.httpServer()
	oldURL, oldRoots := DefaultServerURL, DefaultBootstrapPublicKeys
	DefaultServerURL, DefaultBootstrapPublicKeys = server.URL, platform.roots()
	t.Cleanup(func() { DefaultServerURL, DefaultBootstrapPublicKeys = oldURL, oldRoots })
	t.Chdir(t.TempDir())

	first, err := AutoStart(t.Context(), platform.projectNo, "1.0.0")
	if err != nil {
		t.Fatalf("projectNo-only 首装失败: %v", err)
	}
	if first.Client == nil || !first.Provisioned || first.LicenseNo != "LIC-ZERO-000001" || first.Salt != "zero-salt" || first.InstanceNo != "INS-ZERO-000001" {
		t.Fatalf("首装结果不完整: %+v", first)
	}
	if err = first.Client.Close(); err != nil {
		t.Fatal(err)
	}
	requests, provisioned, activations, provisionFingerprint, activateFingerprint := platform.counts()
	if requests != 2 || provisioned != 1 || activations != 1 {
		t.Fatalf("首装请求次数异常: provision=%d issued=%d activate=%d", requests, provisioned, activations)
	}
	if provisionFingerprint == "" || provisionFingerprint != activateFingerprint {
		t.Fatal("指纹挑战与激活指纹不一致")
	}
	metadataRaw, err := os.ReadFile(filepath.Join("runtime", "licence", "auto.json"))
	if err != nil {
		t.Fatalf("auto.json 未写入: %v", err)
	}
	var metadata struct {
		ProjectNo  string `json:"projectNo"`
		LicenseNo  string `json:"licenseNo"`
		Salt       string `json:"salt"`
		InstanceNo string `json:"instanceNo"`
	}
	if err = json.Unmarshal(metadataRaw, &metadata); err != nil || metadata.ProjectNo != platform.projectNo || metadata.InstanceNo != "INS-ZERO-000001" {
		t.Fatalf("auto.json 元数据异常: %+v %v", metadata, err)
	}

	server.Close()
	second, err := AutoStart(t.Context(), platform.projectNo, "1.0.0")
	if err != nil {
		t.Fatalf("断网重启应使用 auto.json + 加密状态恢复: %v", err)
	}
	if second.Client == nil || second.Provisioned || second.InstanceNo != "INS-ZERO-000001" {
		t.Fatalf("重启结果异常: %+v", second)
	}
	if err = second.Client.Close(); err != nil {
		t.Fatal(err)
	}
	requestsAfter, provisionedAfter, activationsAfter, _, _ := platform.counts()
	if requestsAfter != requests || provisionedAfter != provisioned || activationsAfter != activations {
		t.Fatalf("断网重启不得重复申领/激活: before=%d/%d/%d after=%d/%d/%d", requests, provisioned, activations, requestsAfter, provisionedAfter, activationsAfter)
	}
}

// TestAutoStartGRPC - 高级选项选择 gRPC 后，首装与 keyset 原始 JSON 语义与 HTTP 一致。
func TestAutoStartGRPC(t *testing.T) {
	platform := newAutoTestPlatform(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	licencev1.RegisterLicenseRuntimeServiceServer(server, &autoGRPCLicenseServer{platform: platform})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	result, err := AutoStartWithOptions(t.Context(), AutoOptions{
		ProjectNo: platform.projectNo, AppVersion: "1.0.0",
		ServerURL: "grpc://" + listener.Addr().String(), BootstrapPublicKeys: platform.roots(),
		StorageDir: t.TempDir(), Provider: func() (string, error) { return "auto-grpc-machine", nil },
		Transport: TransportGRPC, GRPC: GRPCOptions{AllowInsecure: true}, HTTPTimeout: time.Second,
		DisableCommands: true, DisableConsumptionReport: true,
	})
	if err != nil {
		t.Fatalf("gRPC AutoStart 失败: %v", err)
	}
	if result.Client == nil || !result.Provisioned || result.InstanceNo != "INS-ZERO-000001" {
		t.Fatalf("gRPC AutoStart 结果异常: %+v", result)
	}
	if err = result.Client.Close(); err != nil {
		t.Fatal(err)
	}
	requests, provisioned, activations, provisionFingerprint, activateFingerprint := platform.counts()
	if requests != 2 || provisioned != 1 || activations != 1 || provisionFingerprint != activateFingerprint {
		t.Fatalf("gRPC 首装/挑战次数或指纹异常: %d/%d/%d", requests, provisioned, activations)
	}
}

// TestAutoStartWithMigrationCode - 新服务器迁移：AutoOptions.MigrationCode 必须透传到申领请求，
// 平台签发的许可证参数写入 auto.json，平台下发的客户账号视图透传到 AutoResult.Customer。
func TestAutoStartWithMigrationCode(t *testing.T) {

	platform := newAutoTestPlatform(t)
	platform.setCustomer(&CustomerView{MaskAccount: "m***@x.com", BoundAt: 1735689600000})
	server := platform.httpServer()
	dir := t.TempDir()
	result, err := AutoStartWithOptions(t.Context(), AutoOptions{
		ProjectNo: platform.projectNo, AppVersion: "1.0.0",
		ServerURL: server.URL, BootstrapPublicKeys: platform.roots(),
		StorageDir: dir, MigrationCode: "ABCD-EFGH",
		Provider:        func() (string, error) { return "migrate-machine", nil },
		DisableCommands: true, DisableConsumptionReport: true, HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("迁移码 AutoStart 失败: %v", err)
	}
	if result.Client == nil {
		t.Fatal("AutoStart 未返回客户端")
	}
	if err = result.Client.Close(); err != nil {
		t.Fatal(err)
	}
	if code := platform.migrationCode(); code != "ABCD-EFGH" {
		t.Fatalf("迁移码未透传到申领请求: %q", code)
	}
	if result.LicenseNo != "LIC-ZERO-000001" || result.Salt != "zero-salt" {
		t.Fatalf("申领结果映射不符: %s %s", result.LicenseNo, result.Salt)
	}
	if result.Customer == nil || result.Customer.MaskAccount != "m***@x.com" || result.Customer.BoundAt != 1735689600000 {
		t.Fatalf("AutoResult.Customer 未透传申领客户视图: %+v", result.Customer)
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, autoMetadataFile))
	if readErr != nil {
		t.Fatalf("auto.json 未写入: %v", readErr)
	}
	var metadata autoMetadata
	if err = json.Unmarshal(raw, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.LicenseNo != "LIC-ZERO-000001" || metadata.Salt != "zero-salt" {
		t.Fatalf("auto.json 未写入返回的许可证参数: %+v", metadata)
	}
}

// TestAutoStartRejectsUntrustedProvision - 申领 keyset 未过官方根前，不得写 auto.json 或发起激活。
func TestAutoStartRejectsUntrustedProvision(t *testing.T) {
	platform := newAutoTestPlatform(t)
	server := platform.httpServer()
	wrongRoots := newKeysetTestPlatform(t).roots()
	dir := t.TempDir()
	result, err := AutoStartWithOptions(t.Context(), AutoOptions{
		ProjectNo: platform.projectNo, ServerURL: server.URL, BootstrapPublicKeys: wrongRoots,
		StorageDir: dir, Provider: func() (string, error) { return "auto-machine", nil },
		DisableCommands: true, DisableConsumptionReport: true,
	})
	if err == nil || result.Client != nil {
		t.Fatalf("错根 keyset 必须使首装失败: %+v %v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, autoMetadataFile)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("未验根前不得写 auto.json: %v", statErr)
	}
	_, _, activations, _, _ := platform.counts()
	if activations != 0 {
		t.Fatalf("未验根前不得发起激活: %d", activations)
	}
}
