package licence

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProvisionOptions - 装机免费自动申领参数。
// 在 New(Client) 之前调用：先申领拿到证书参数（LicenseNo/Salt），再构造运行面客户端自动激活。
// ServerURL/TemplateCode/ProvisionToken 由官方二进制内写死，装机用户不可改。
type ProvisionOptions struct {
	// ServerURL - 授权平台地址（必填），如 "https://license.example.com"
	ServerURL string
	// TemplateCode - 模板编码（必填，官方二进制写死），如 "INIS-AUTO"
	TemplateCode string
	// ProvisionToken - 签发令牌（必填，匿名申领凭证，官方二进制写死）
	ProvisionToken string
	// StorageDir - install.sn 持久化目录（默认 ./runtime/licence）
	StorageDir string
	// InstallSN - 安装唯一标识（可选；为空时懒生成 UUID v4 并持久化到 StorageDir/install.sn）
	InstallSN string
	// DeviceName - 机器名称（可选，缺省取 os.Hostname）
	DeviceName string
	// Transport - 传输协议，零值默认 HTTP。
	Transport Transport
	// GRPC - gRPC 连接配置，仅 TransportGRPC 时生效。
	GRPC GRPCOptions
	// HTTPTimeout - 单次请求超时（默认 15 秒）
	HTTPTimeout time.Duration
}

// RedeemOptions - 商业激活码兑换参数（语义与 ProvisionOptions 一致，另含激活码）。
type RedeemOptions struct {
	ProvisionOptions
	// Code - 激活码（必填）
	Code string
}

// ProvisionResult - 申领/兑换结果，可直接用于构造运行面客户端（New 的 LicenseNo/Salt）。
type ProvisionResult struct {
	// LicenseNo - 许可证编号（New(Client) 必填）
	LicenseNo string
	// Salt - 指纹盐（New(Client) 必填，与申领模板一致）
	Salt string
	// BindingPolicy - 绑定策略（single/seats）
	BindingPolicy string
	// SeatLimit - 席位上限
	SeatLimit int
	// ExpiresAt - 有效期截止（毫秒）
	ExpiresAt int64
	// Reissued - 本次是否为同 SN 有界续签
	Reissued bool
}

// ProvisionError - 申领/兑换业务拒绝（errors.As 断言后按 Status 降级处理）。
type ProvisionError struct {
	// Status - 业务状态码（PROVISION_DISABLED/TEMPLATE_INVALID/QUOTA_EXCEEDED/RATE_LIMITED/...，契约 §6.3）
	Status string
	// Message - 平台提示
	Message string
}

// Error - 错误文本。
func (this *ProvisionError) Error() string {
	if this.Message != "" {
		return "授权申领被拒绝（" + this.Status + "）：" + this.Message
	}
	return "授权申领被拒绝：" + this.Status
}

// Provision - 装机免费自动申领（匿名 Public，无需平台凭证）。
// 必须在 New(Client) 之前调用：先申领拿到证书参数，再构造运行面客户端并自动激活。
// 业务拒绝以 *ProvisionError 返回（errors.As 断言后按 Status 降级）。
func Provision(ctx context.Context, opts ProvisionOptions) (ProvisionResult, error) {
	if strings.TrimSpace(opts.ServerURL) == "" {
		return ProvisionResult{}, errors.New("ServerURL 不能为空（平台 URI 入口）")
	}
	if strings.TrimSpace(opts.TemplateCode) == "" {
		return ProvisionResult{}, errors.New("TemplateCode 不能为空（模板编码由官方二进制写死）")
	}
	if strings.TrimSpace(opts.ProvisionToken) == "" {
		return ProvisionResult{}, errors.New("ProvisionToken 不能为空（签发令牌由官方二进制写死）")
	}
	installSN, err := resolveInstallSN(opts.StorageDir, opts.InstallSN)
	if err != nil {
		return ProvisionResult{}, err
	}
	opts.DeviceName = normalizeDeviceName(opts.DeviceName)
	body, err := json.Marshal(provisionBody{
		TemplateCode: opts.TemplateCode, ProvisionToken: opts.ProvisionToken,
		InstallSN: installSN, DeviceName: opts.DeviceName,
		ClientTime: time.Now().UnixMilli(),
	})
	if err != nil {
		return ProvisionResult{}, err
	}
	response, err := provisionRoundTrip(ctx, opts, "/api/v1/licenses/provision", body)
	if err != nil {
		return ProvisionResult{}, err
	}
	return finishProvision(response)
}

// Redeem - 商业激活码兑换（匿名 Public，无需平台凭证）。
// 业务拒绝以 *ProvisionError 返回（errors.As 断言后按 Status 降级）。
func Redeem(ctx context.Context, opts RedeemOptions) (ProvisionResult, error) {
	if strings.TrimSpace(opts.ServerURL) == "" {
		return ProvisionResult{}, errors.New("ServerURL 不能为空（平台 URI 入口）")
	}
	if strings.TrimSpace(opts.Code) == "" {
		return ProvisionResult{}, errors.New("Code 不能为空（激活码）")
	}
	installSN, err := resolveInstallSN(opts.StorageDir, opts.InstallSN)
	if err != nil {
		return ProvisionResult{}, err
	}
	opts.DeviceName = normalizeDeviceName(opts.DeviceName)
	body, err := json.Marshal(redeemBody{
		Code: opts.Code, InstallSN: installSN, DeviceName: opts.DeviceName,
		ClientTime: time.Now().UnixMilli(),
	})
	if err != nil {
		return ProvisionResult{}, err
	}
	response, err := provisionRoundTrip(ctx, opts.ProvisionOptions, "/api/v1/licenses/redeem", body)
	if err != nil {
		return ProvisionResult{}, err
	}
	return finishProvision(response)
}

// provisionRoundTrip - 双协议统一请求出口：选传传输 → RoundTrip → 解析统一响应。
// 申领/兑换请求不做请求签名（匿名凭证，withSign=false 避免访问运行面状态）；
// 业务码放 status 字段，两协议线格式一致。
func provisionRoundTrip(ctx context.Context, opts ProvisionOptions, uri string, body []byte) (provisionResponse, error) {
	timeout := opts.HTTPTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	// 传输层仅依赖运行面参数（ServerURL/GRPC/HTTPTimeout），用裸 Client 承载 options 复用既有实现。
	bare := &Client{options: Options{
		ServerURL: opts.ServerURL, Transport: opts.Transport,
		GRPC: opts.GRPC, HTTPTimeout: timeout,
	}}
	var (
		transport runtimeTransport
		err       error
	)
	switch opts.Transport {
	case "", TransportHTTP:
		transport = newHTTPRuntimeTransport(bare)
	case TransportGRPC:
		transport, err = newGRPCRuntimeTransport(bare)
	default:
		return provisionResponse{}, errors.New("不支持的运行面传输协议：" + string(opts.Transport))
	}
	if err != nil {
		return provisionResponse{}, err
	}
	defer transport.Close()

	code, raw, err := transport.RoundTrip(ctx, http.MethodPost, uri, body, false)
	if err != nil {
		return provisionResponse{}, err
	}
	if code != http.StatusOK {
		// 业务拒绝统一 200 + status 字段；非 200 仅发生在传输层故障（404 路由缺失/5xx 网关）
		return provisionResponse{}, fmt.Errorf("申领请求失败（HTTP %d）", code)
	}
	var response provisionResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return provisionResponse{}, err
	}
	return response, nil
}

// finishProvision - 统一响应 → 结果或业务拒绝（*ProvisionError）。
func finishProvision(response provisionResponse) (ProvisionResult, error) {
	if response.Status != "OK" {
		return ProvisionResult{}, &ProvisionError{Status: response.Status, Message: response.Message}
	}
	if strings.TrimSpace(response.LicenseNo) == "" || strings.TrimSpace(response.Salt) == "" {
		return ProvisionResult{}, errors.New("申领响应缺少许可证参数")
	}
	return ProvisionResult{
		LicenseNo: response.LicenseNo, Salt: response.Salt,
		BindingPolicy: response.BindingPolicy, SeatLimit: response.SeatLimit,
		ExpiresAt: response.ExpiresAt, Reissued: response.Reissued,
	}, nil
}

// installSNFile - 安装唯一标识持久化文件名。
// 独立于按 licenseNo 命名的状态文件，保证跨许可证/跨激活稳定不变。
const installSNFile = "install.sn"

// resolveInstallSN - 解析安装唯一标识：
// explicit 非空直接采用；为空时读 install.sn 幂等复用；不存在则懒生成 UUID v4 原子落盘。
func resolveInstallSN(storageDir, explicit string) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		return value, nil
	}
	dir := strings.TrimSpace(storageDir)
	if dir == "" {
		dir = "./runtime/licence"
	}
	path := filepath.Join(dir, installSNFile)
	if raw, err := os.ReadFile(path); err == nil {
		if value := strings.TrimSpace(string(raw)); value != "" {
			return value, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	value := randomUUIDv4()
	if value == "" {
		return "", errors.New("生成安装唯一标识失败")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return value, nil
}

// randomUUIDv4 - 生成 UUID v4（RFC 4122，版本 4 变体 10），格式 xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx。
func randomUUIDv4() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40 // 版本 4
	buffer[8] = (buffer[8] & 0x3f) | 0x80 // 变体 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

// normalizeDeviceName - 机器名称归一（去空白/截断，与 New 的归一逻辑一致）。
func normalizeDeviceName(deviceName string) string {
	value := strings.TrimSpace(deviceName)
	if value == "" {
		value, _ = os.Hostname()
	}
	value = strings.TrimSpace(value)
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}
