package licence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"google.golang.org/grpc"
)

// ============================= 申领/兑换假平台（契约 §6 行为镜像） =============================

// fakeProvisionServer - 申领/兑换假平台：业务码放 status 字段，HTTP 200 传输。
// respond 由测试注入：入参为请求体，返回业务状态码与附加字段。
type fakeProvisionServer struct {
	mu       sync.Mutex
	lastBody map[string]any
	respond  func(body map[string]any) (status string, extra map[string]any)
	server   *httptest.Server
}

func newFakeProvisionServer(t *testing.T, respond func(map[string]any) (string, map[string]any)) *fakeProvisionServer {
	t.Helper()
	fake := &fakeProvisionServer{respond: respond}
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		fake.mu.Lock()
		fake.lastBody = body
		fake.mu.Unlock()
		status, extra := fake.respond(body)
		payload := map[string]any{"status": status, "serverTime": time.Now().UnixMilli()}
		for key, value := range extra {
			payload[key] = value
		}
		writeJson(writer, payload)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (this *fakeProvisionServer) body() map[string]any {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.lastBody
}

// okProvisionResponse - 成功响应附加字段。
func okProvisionResponse() map[string]any {
	return map[string]any{
		"licenseNo": "LIC-2026-000888", "salt": "salt-http",
		"bindingPolicy": "single", "seatLimit": 1,
		"expiresAt": time.Now().UnixMilli() + 30*24*3600*1000, "reissued": true,
	}
}

// ============================= 端到端测试（HTTP 传输） =============================

// TestProvisionHTTP - 公开 Provision（HTTP）：参数校验 → 懒生成 install.sn →
// 双协议统一响应映射为结果；重复调用幂等复用同一 SN。
func TestProvisionHTTP(t *testing.T) {

	server := newFakeProvisionServer(t, func(map[string]any) (string, map[string]any) {
		return "OK", okProvisionResponse()
	})
	dir := t.TempDir()
	opts := ProvisionOptions{
		ServerURL: server.server.URL, TemplateCode: "INIS-AUTO", ProvisionToken: "tmpl-token",
		StorageDir: dir, DeviceName: "provision-device",
	}

	result, err := Provision(t.Context(), opts)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}
	if result.LicenseNo != "LIC-2026-000888" || result.Salt != "salt-http" {
		t.Fatalf("许可证/盐映射不符: %s %s", result.LicenseNo, result.Salt)
	}
	if result.BindingPolicy != "single" || result.SeatLimit != 1 {
		t.Fatalf("绑定策略/席位映射不符: %s %d", result.BindingPolicy, result.SeatLimit)
	}
	if result.ExpiresAt <= 0 || !result.Reissued {
		t.Fatalf("有效期/续签标识映射不符: %d %v", result.ExpiresAt, result.Reissued)
	}

	// 请求体上送模板编码/签发令牌/设备名
	body := server.body()
	if body["templateCode"] != "INIS-AUTO" || body["provisionToken"] != "tmpl-token" {
		t.Fatalf("模板/令牌未上送: %v", body)
	}
	if body["deviceName"] != "provision-device" {
		t.Fatalf("设备名未上送: %v", body["deviceName"])
	}

	// 懒生成 install.sn 并持久化
	path := filepath.Join(dir, installSNFile)
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("install.sn 未生成: %v", readErr)
	}
	firstSN := string(raw)
	if !snUUIDPattern.MatchString(firstSN) {
		t.Fatalf("install.sn 非 UUID v4: %q", firstSN)
	}
	if body["installSN"] != firstSN {
		t.Fatalf("上送 SN 与落盘不一致: %q vs %q", body["installSN"], firstSN)
	}

	// 幂等复用：再次申领使用同一 SN，不重新生成
	second, err := Provision(t.Context(), opts)
	if err != nil {
		t.Fatalf("再次 Provision 失败: %v", err)
	}
	if second.LicenseNo != result.LicenseNo {
		t.Fatalf("重复申领应复用结果: %s vs %s", second.LicenseNo, result.LicenseNo)
	}
	if again := server.body()["installSN"]; again != firstSN {
		t.Fatalf("重复申领 SN 应复用落盘值: %q vs %q", again, firstSN)
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != firstSN {
		t.Fatalf("install.sn 被改写: %q vs %q", string(raw), firstSN)
	}
}

// TestProvisionHTTPBusinessError - 业务拒绝：status != OK → *ProvisionError（HTTP 200 传输）。
func TestProvisionHTTPBusinessError(t *testing.T) {

	server := newFakeProvisionServer(t, func(map[string]any) (string, map[string]any) {
		return "RATE_LIMITED", map[string]any{"message": "请求过于频繁"}
	})
	_, err := Provision(t.Context(), ProvisionOptions{
		ServerURL: server.server.URL, TemplateCode: "INIS-AUTO", ProvisionToken: "tmpl-token",
		StorageDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("业务拒绝应返回错误")
	}
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) {
		t.Fatalf("应可断言为 *ProvisionError，实际 %T", err)
	}
	if provisionErr.Status != "RATE_LIMITED" || provisionErr.Message != "请求过于频繁" {
		t.Fatalf("业务状态/提示透传不符: %s %s", provisionErr.Status, provisionErr.Message)
	}
	if provisionErr.Error() == "" {
		t.Fatal("错误文本不应为空")
	}
}

// TestRedeemHTTP - 公开 Redeem（HTTP）：激活码兑换成功路径与参数上送。
func TestRedeemHTTP(t *testing.T) {

	server := newFakeProvisionServer(t, func(map[string]any) (string, map[string]any) {
		return "OK", okProvisionResponse()
	})
	result, err := Redeem(t.Context(), RedeemOptions{
		ProvisionOptions: ProvisionOptions{
			ServerURL: server.server.URL, StorageDir: t.TempDir(), DeviceName: "redeem-device",
		},
		Code: "COM-2026-ABCDEF",
	})
	if err != nil {
		t.Fatalf("Redeem 失败: %v", err)
	}
	if result.LicenseNo != "LIC-2026-000888" {
		t.Fatalf("兑换结果映射不符: %s", result.LicenseNo)
	}
	body := server.body()
	if body["code"] != "COM-2026-ABCDEF" {
		t.Fatalf("激活码未上送: %v", body["code"])
	}
	sn, ok := body["installSN"].(string)
	if !ok || sn == "" || !snUUIDPattern.MatchString(sn) {
		t.Fatalf("兑换应懒生成并上送 SN: %v", body["installSN"])
	}
}

// TestProvisionValidation - 必填参数校验（HTTP/gRPC 共用，裸参数层）。
func TestProvisionValidation(t *testing.T) {

	dir := t.TempDir()
	if _, err := Provision(t.Context(), ProvisionOptions{TemplateCode: "INIS-AUTO", ProvisionToken: "t"}); err == nil {
		t.Fatal("缺少 ServerURL 应报错")
	}
	if _, err := Provision(t.Context(), ProvisionOptions{ServerURL: "http://127.0.0.1:1", ProvisionToken: "t"}); err == nil {
		t.Fatal("缺少 TemplateCode 应报错")
	}
	if _, err := Provision(t.Context(), ProvisionOptions{ServerURL: "http://127.0.0.1:1", TemplateCode: "INIS-AUTO"}); err == nil {
		t.Fatal("缺少 ProvisionToken 应报错")
	}
	if _, err := Redeem(t.Context(), RedeemOptions{ProvisionOptions: ProvisionOptions{ServerURL: "http://127.0.0.1:1", StorageDir: dir}}); err == nil {
		t.Fatal("缺少 Code 应报错")
	}
}

// ============================= 端到端测试（gRPC 传输） =============================

// provisionGRPCServer - gRPC 申领假服务端：捕获请求并返回成功响应。
type provisionGRPCServer struct {
	licencev1.UnimplementedLicenseRuntimeServiceServer
	t       *testing.T
	request *licencev1.ProvisionRequest
}

func (this *provisionGRPCServer) Provision(_ context.Context, request *licencev1.ProvisionRequest) (*licencev1.ProvisionResponse, error) {
	this.request = request
	return &licencev1.ProvisionResponse{
		Status: "OK", ServerTime: time.Now().UnixMilli(),
		LicenseNo: "LIC-2026-000999", Salt: "salt-grpc",
		BindingPolicy: "single", SeatLimit: 1,
		ExpiresAt: time.Now().UnixMilli() + 30*24*3600*1000, Reissued: false,
	}, nil
}

// TestProvisionGRPC - 公开 Provision（gRPC）：裸 Client 承载 grpc 传输，
// RoundTrip 走 /api/v1/licenses/provision 分发，统一响应映射为结果。
func TestProvisionGRPC(t *testing.T) {

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &provisionGRPCServer{t: t}
	server := grpc.NewServer()
	licencev1.RegisterLicenseRuntimeServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	dir := t.TempDir()
	result, err := Provision(t.Context(), ProvisionOptions{
		ServerURL: "grpc://" + listener.Addr().String(),
		TemplateCode: "INIS-AUTO", ProvisionToken: "tmpl-token",
		StorageDir: dir, DeviceName: "g-device",
		Transport: TransportGRPC, GRPC: GRPCOptions{AllowInsecure: true},
		HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("gRPC Provision 失败: %v", err)
	}
	if result.LicenseNo != "LIC-2026-000999" || result.Salt != "salt-grpc" {
		t.Fatalf("gRPC 结果映射不符: %s %s", result.LicenseNo, result.Salt)
	}

	// gRPC 请求字段对齐 HTTP 线格式（templateCode/provisionToken/installSN/deviceName）
	if fake.request.GetTemplateCode() != "INIS-AUTO" || fake.request.GetProvisionToken() != "tmpl-token" {
		t.Fatalf("gRPC 模板/令牌未上送: %+v", fake.request)
	}
	if fake.request.GetDeviceName() != "g-device" {
		t.Fatalf("gRPC 设备名未上送: %q", fake.request.GetDeviceName())
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, installSNFile))
	if readErr != nil {
		t.Fatalf("gRPC 路径未持久化 install.sn: %v", readErr)
	}
	if fake.request.GetInstallSn() != string(raw) {
		t.Fatalf("gRPC 上送 SN 与落盘不一致: %q vs %q", fake.request.GetInstallSn(), string(raw))
	}
}

// ============================= 单元测试 =============================

var snUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestRandomUUIDv4 - UUID v4 格式（版本 4 变体 10）与唯一性。
func TestRandomUUIDv4(t *testing.T) {

	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		value := randomUUIDv4()
		if !snUUIDPattern.MatchString(value) {
			t.Fatalf("UUID v4 格式不符: %q", value)
		}
		if seen[value] {
			t.Fatalf("UUID 重复: %q", value)
		}
		seen[value] = true
	}
}

// TestResolveInstallSN - 安装唯一标识解析：显式优先 / 懒生成持久化 / 幂等复用 / 预置文件。
func TestResolveInstallSN(t *testing.T) {

	// 显式 SN 直接采用，不落盘
	dir := t.TempDir()
	sn, err := resolveInstallSN(dir, "explicit-sn-123")
	if err != nil || sn != "explicit-sn-123" {
		t.Fatalf("显式 SN 应直接采用: %q %v", sn, err)
	}
	if _, err = os.Stat(filepath.Join(dir, installSNFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("显式 SN 不应落盘: %v", err)
	}

	// 懒生成：首次生成并持久化，重复调用复用同一值
	generated, err := resolveInstallSN(dir, "")
	if err != nil || !snUUIDPattern.MatchString(generated) {
		t.Fatalf("懒生成 SN 异常: %q %v", generated, err)
	}
	again, err := resolveInstallSN(dir, "")
	if err != nil || again != generated {
		t.Fatalf("重复解析应复用落盘 SN: %q vs %q", again, generated)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, installSNFile))
	if string(raw) != generated {
		t.Fatalf("落盘值与解析值不一致: %q vs %q", string(raw), generated)
	}

	// 预置文件优先复用
	preSeed := t.TempDir()
	if err = os.MkdirAll(preSeed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(preSeed, installSNFile), []byte("pre-seeded-sn"), 0o600); err != nil {
		t.Fatal(err)
	}
	seeded, err := resolveInstallSN(preSeed, "")
	if err != nil || seeded != "pre-seeded-sn" {
		t.Fatalf("预置文件应优先复用: %q %v", seeded, err)
	}
}

// TestProvisionErrorText - ProvisionError 错误文本与 errors.As 断言。
func TestProvisionErrorText(t *testing.T) {

	provisionErr := &ProvisionError{Status: "QUOTA_EXCEEDED", Message: "模板每日发放上限已用尽"}
	if !errors.Is(provisionErr, provisionErr) {
		t.Fatal("errors.Is 自身应成立")
	}
	if text := provisionErr.Error(); text != "授权申领被拒绝（QUOTA_EXCEEDED）：模板每日发放上限已用尽" {
		t.Fatalf("错误文本不符: %q", text)
	}
	if text := (&ProvisionError{Status: "CODE_INVALID"}).Error(); text != "授权申领被拒绝：CODE_INVALID" {
		t.Fatalf("无提示错误文本不符: %q", text)
	}
}
