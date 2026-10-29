package runtime

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

// ProvisionOptions - 装机自动申领参数（项目编号 + 安装标识 + 可选 SaaS 子租户）。
// 在 New(Client) 之前调用：先申领拿到证书参数（LicenseNo/Salt），再构造运行面客户端自动激活。
type ProvisionOptions struct {
	// ServerURL - 授权平台地址（必填），如 "https://license.example.com"
	ServerURL string
	// ProjectNo - 项目编号（必填，官方二进制写死），如 "INIS-AUTO"
	ProjectNo string
	// TenantId - SaaS 子租户 ID（可选，0 = 普通装机申领；>0 = 按项目子租户发证档位签发）
	TenantId int64
	// MigrationCode - 一次性迁移码（可选；仅新服务器迁移场景使用，为空保持常规 sdk_auto 申领）
	MigrationCode string
	// ActivationCode - 一次性激活码（可选；入座客户既有许可证的一个席位，不签发新证；
	// 与 MigrationCode 互斥，同时携带平台判无效请求）
	ActivationCode string
	// CurrentLicenseNo - 当前许可证编号（可选；已激活实例换证入座时由 Client.ActivateByCode 注入，
	// 携带时请求带 activation-sign-v1 实例签名，平台据此做冲突评估与旧席位释放）
	CurrentLicenseNo string
	// ReleaseOldSeat - 换证入座成功后同事务释放当前许可证上的旧席位（仅随 CurrentLicenseNo 生效）
	ReleaseOldSeat bool
	// ConfirmReplace - 冲突确认（仅随 CurrentLicenseNo 生效）：当前证已持付费套餐时缺省返回
	// ACTIVATION_CONFLICT 对比载荷且不消费激活码，显式 true 才执行换证
	ConfirmReplace bool
	// StorageDir - install.sn 持久化目录（默认 ./runtime/licence）
	StorageDir string
	// InstallSN - 安装唯一标识（可选；为空时懒生成 UUID v4 并持久化到 StorageDir/install.sn）
	InstallSN string
	// DeviceName - 机器名称（可选，缺省取 os.Hostname）
	DeviceName string
	// Fingerprint - 显式指纹哈希（可选，与运行面 Options.Fingerprint 语义一致）
	Fingerprint string
	// Provider - 自定义指纹提供者（可选，挑战响应的 salt 与激活使用同一算法）
	Provider FingerprintProvider
	// Transport - 传输协议，零值默认 HTTP。
	Transport Transport
	// GRPC - gRPC 连接配置，仅 TransportGRPC 时生效。
	GRPC GRPCOptions
	// HTTPTimeout - 单次请求超时（默认 15 秒）
	HTTPTimeout time.Duration
}

// ProvisionResult - 申领结果，可直接用于构造运行面客户端（New 的 LicenseNo/Salt）。
type ProvisionResult struct {
	// LicenseNo - 许可证编号（New(Client) 必填）
	LicenseNo string
	// Salt - 指纹盐（New(Client) 必填，与签发套餐档位一致）
	Salt string
	// BindingPolicy - 绑定策略（single/seats）
	BindingPolicy string
	// SeatLimit - 席位上限
	SeatLimit int
	// ExpiresAt - 有效期截止（毫秒）
	ExpiresAt int64
	// Reissued - 本次是否为同 SN 有界续签
	Reissued bool
	// Keyset - bootstrap 根签名的项目工作公钥集合原始 JSON
	Keyset json.RawMessage
	// Customer - 客户账号视图（仅许可证已绑定客户账号时下发，未绑定为 nil）
	Customer *CustomerView
}

// CustomerView - 许可证已绑定的客户账号视图（仅展示平台下发的脱敏信息）。
type CustomerView struct {
	// MaskAccount - 脱敏账号（如 r***@x.com）
	MaskAccount string
	// BoundAt - 绑定时间（毫秒）
	BoundAt int64
}

// ProvisionError - 申领业务拒绝（errors.As 断言后按 Status 降级处理）。
type ProvisionError struct {
	// Status - 业务状态码（PROVISION_DISABLED / TEMPLATE_DEPRECATED / PLAN_INVALID /
	// QUOTA_EXCEEDED / RATE_LIMITED / ACTIVATION_INVALID / ...，契约 §6.3）。SDK 不维护白名单：
	// 服务端新增或未知的业务码一律原样透传（含旧版客户端申领被拒的 TEMPLATE_DEPRECATED），交调用方按需降级。
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

// ActivationConflictPlan - 换证冲突对比中的单证套餐视图。
type ActivationConflictPlan struct {
	// LicenseNo - 许可证编号
	LicenseNo string
	// PlanCode - 套餐编码
	PlanCode string
	// PlanName - 套餐名称
	PlanName string
	// Billing - 计费周期（monthly/yearly/buyout，免费证为空）
	Billing string
	// ValidUntil - 到期时间（RFC3339，免费证为空）
	ValidUntil string
	// Paid - 是否付费套餐
	Paid bool
}

// ActivationConflictError - 激活码换证冲突（status=ACTIVATION_CONFLICT）：
// 当前许可证已持付费套餐且与激活码目标证不同，平台未消费激活码；
// 调用方展示对比载荷由用户确认后以 ConfirmReplace=true 重发。
type ActivationConflictError struct {
	// Message - 平台提示
	Message string
	// CurrentPlan - 当前许可证套餐
	CurrentPlan ActivationConflictPlan
	// TargetPlan - 激活码目标许可证套餐
	TargetPlan ActivationConflictPlan
	// SeatUsed - 目标证已用席位数
	SeatUsed int
	// SeatLimit - 目标证席位上限
	SeatLimit int
}

// Error - 错误文本。
func (this *ActivationConflictError) Error() string {
	if this.Message != "" {
		return "激活码换证冲突（ACTIVATION_CONFLICT）：" + this.Message
	}
	return "激活码换证冲突：当前许可证已持付费套餐"
}

// conflictView - 冲突线格式 → 对比载荷。
func (this *conflictViewJSON) conflictView() (ActivationConflictPlan, ActivationConflictPlan) {
	var current, target ActivationConflictPlan
	if this == nil {
		return current, target
	}
	if this.CurrentPlan != nil {
		current = ActivationConflictPlan{
			LicenseNo: this.CurrentPlan.LicenseNo, PlanCode: this.CurrentPlan.PlanCode,
			PlanName: this.CurrentPlan.PlanName, Billing: this.CurrentPlan.Billing,
			ValidUntil: this.CurrentPlan.ValidUntil, Paid: this.CurrentPlan.Paid,
		}
	}
	if this.TargetPlan != nil {
		target = ActivationConflictPlan{
			LicenseNo: this.TargetPlan.LicenseNo, PlanCode: this.TargetPlan.PlanCode,
			PlanName: this.TargetPlan.PlanName, Billing: this.TargetPlan.Billing,
			ValidUntil: this.TargetPlan.ValidUntil, Paid: this.TargetPlan.Paid,
		}
	}
	return current, target
}

// ProvisionByPlan - 装机自动申领（sdk_auto 匿名 Public，projectNo + installSN 定位）。
// 签发参数由平台按项目配置的套餐档位决定；tenantId > 0 时按项目子租户发证档位签发。
// 强制指纹项目首次返回 FINGERPRINT_REQUIRED + salt 时，SDK 计算与激活一致的指纹并只重试一次。
// 业务拒绝以 *ProvisionError 返回（errors.As 断言后按 Status 降级）。
func ProvisionByPlan(ctx context.Context, opts ProvisionOptions) (ProvisionResult, error) {
	if strings.TrimSpace(opts.ServerURL) == "" {
		return ProvisionResult{}, errors.New("ServerURL 不能为空（平台 URI 入口）")
	}
	if strings.TrimSpace(opts.ProjectNo) == "" {
		return ProvisionResult{}, errors.New("ProjectNo 不能为空（项目编号由官方二进制写死）")
	}
	if opts.TenantId < 0 {
		return ProvisionResult{}, errors.New("TenantId 不能为负数（0 = 普通装机申领，>0 = SaaS 子租户）")
	}
	installSN, err := resolveInstallSN(opts.StorageDir, opts.InstallSN)
	if err != nil {
		return ProvisionResult{}, err
	}
	opts.DeviceName = normalizeDeviceName(opts.DeviceName)
	body := provisionBody{
		ProjectNo: opts.ProjectNo, TenantId: opts.TenantId,
		InstallSN: installSN, DeviceName: opts.DeviceName,
		MigrationCode: opts.MigrationCode, ActivationCode: opts.ActivationCode,
		CurrentLicenseNo: opts.CurrentLicenseNo, ReleaseOldSeat: opts.ReleaseOldSeat,
		ConfirmReplace: opts.ConfirmReplace,
	}
	response, err := provisionRequest(ctx, opts, body)
	if err != nil {
		return ProvisionResult{}, err
	}
	if response.Status == "FINGERPRINT_REQUIRED" {
		if strings.TrimSpace(response.Salt) == "" {
			return ProvisionResult{}, errors.New("指纹挑战响应缺少 salt")
		}
		body.FingerprintHash, err = FingerprintHash(response.Salt, opts.Fingerprint, opts.Provider)
		if err != nil {
			return ProvisionResult{}, err
		}
		response, err = provisionRequest(ctx, opts, body)
		if err != nil {
			return ProvisionResult{}, err
		}
	}
	return finishProvision(response)
}

// provisionRequest - 组装当前毫秒时间并执行一次申领请求；挑战重试复用同一 installSN。
func provisionRequest(ctx context.Context, opts ProvisionOptions, body provisionBody) (provisionResponse, error) {
	body.ClientTime = time.Now().UnixMilli()
	raw, err := json.Marshal(body)
	if err != nil {
		return provisionResponse{}, err
	}
	return provisionRoundTrip(ctx, opts, "/api/v1/licenses/provision", raw)
}

// provisionRoundTrip - 双协议统一请求出口：选传传输 → RoundTrip → 解析统一响应。
// 申领请求不做请求签名（匿名凭证，withSign=false 避免访问运行面状态）；
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

// finishProvision - 统一响应 → 结果或业务拒绝（*ProvisionError / *ActivationConflictError）。
func finishProvision(response provisionResponse) (ProvisionResult, error) {
	if response.Status == "ACTIVATION_CONFLICT" {
		current, target := response.Conflict.conflictView()
		conflict := &ActivationConflictError{Message: response.Message, CurrentPlan: current, TargetPlan: target}
		if response.Conflict != nil {
			conflict.SeatUsed = response.Conflict.SeatUsed
			conflict.SeatLimit = response.Conflict.SeatLimit
		}
		return ProvisionResult{}, conflict
	}
	if response.Status != "OK" {
		return ProvisionResult{}, &ProvisionError{Status: response.Status, Message: response.Message}
	}
	if strings.TrimSpace(response.LicenseNo) == "" || strings.TrimSpace(response.Salt) == "" {
		return ProvisionResult{}, errors.New("申领响应缺少许可证参数")
	}
	result := ProvisionResult{
		LicenseNo: response.LicenseNo, Salt: response.Salt,
		BindingPolicy: response.BindingPolicy, SeatLimit: response.SeatLimit,
		ExpiresAt: response.ExpiresAt, Reissued: response.Reissued,
		Keyset: append(json.RawMessage(nil), response.Keyset...),
	}
	if response.Customer != nil {
		result.Customer = &CustomerView{
			MaskAccount: response.Customer.MaskAccount, BoundAt: response.Customer.BoundAt,
		}
	}
	return result, nil
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
