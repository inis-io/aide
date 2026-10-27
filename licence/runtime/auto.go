package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// autoMetadataFile - projectNo-only 自动授权的非机密元数据文件。
const autoMetadataFile = "auto.json"

// AutoOptions - 自动授权高级参数；官方平台只需 ProjectNo，其他字段均有安全默认值。
type AutoOptions struct {
	// ProjectNo - 项目编号（必填，与 keyset 精确绑定）。
	ProjectNo string
	// AppVersion - 应用自身版本元数据（可选；为空不妨碍授权，但 CheckUpdate 会明确拒绝）。
	AppVersion string
	// ServerURL - 自定义授权平台地址（可选，缺省 DefaultServerURL）。
	ServerURL string
	// BootstrapPublicKeys - 自定义平台 bootstrap 根公钥（可选，缺省 DefaultBootstrapPublicKeys）。
	BootstrapPublicKeys map[string]string
	// TenantId - SaaS 子租户 ID（可选，语义同 ProvisionOptions.TenantId）。
	TenantId int64
	// StorageDir - auto.json、install.sn 与加密运行状态的存储目录（默认 ./runtime/licence）。
	StorageDir string
	// InstallSN - 安装唯一标识（可选，为空时懒生成并持久化）。
	InstallSN string
	// InstanceNo - 已有部署实例编号（可选；首激活后以签名信封中的实例号为准）。
	InstanceNo string
	// DeviceName - 机器名称（可选，缺省取 os.Hostname）。
	DeviceName string
	// Fingerprint - 显式指纹哈希（可选，覆盖自动采集）。
	Fingerprint string
	// Provider - 自定义指纹提供者（可选）。
	Provider FingerprintProvider
	// Store - 自定义加密运行状态存储（可选，auto.json/install.sn 仍写 StorageDir）。
	Store Store
	// RefreshInterval - 授权滑动刷新间隔（默认 12 小时）。
	RefreshInterval time.Duration
	// HTTPTimeout - 单次请求超时（默认 15 秒）。
	HTTPTimeout time.Duration
	// Transport - 传输协议，零值默认 HTTP。
	Transport Transport
	// GRPC - gRPC 连接配置，仅 TransportGRPC 时生效。
	GRPC GRPCOptions
	// OnStatusChange - 授权状态变化回调（可选）。
	OnStatusChange func(oldStatus string, newStatus string)
	// DisableConsumptionReport - 关闭平台配置消费埋点上报。
	DisableConsumptionReport bool
	// ConsumptionReportInterval - 消费埋点上报间隔。
	ConsumptionReportInterval time.Duration
	// CommandPullTimeout - 实例指令长轮询 hold 时长。
	CommandPullTimeout time.Duration
	// CommandHandlers - 自定义实例指令处理器。
	CommandHandlers map[string]CommandHandler
	// DisableCommands - 关闭实例指令通道。
	DisableCommands bool
}

// AutoResult - 已完成申领（如需）并启动的真实运行面结果。
type AutoResult struct {
	// Client - 已 Start 的运行面客户端，调用方不得重复 New/Start；不再使用时调用 Close。
	Client *Client
	// LicenseNo - 当前许可证编号。
	LicenseNo string
	// Salt - 当前指纹盐。
	Salt string
	// InstanceNo - 签名许可证信封锚定的部署实例编号。
	InstanceNo string
	// Provisioned - 本次调用是否实际执行了申领；auto.json 恢复为 false。
	Provisioned bool
}

// autoMetadata - auto.json 非机密元数据；token、客户端私钥与工作公钥仍由 Store 加密保存。
type autoMetadata struct {
	ProjectNo  string `json:"projectNo"`
	LicenseNo  string `json:"licenseNo"`
	Salt       string `json:"salt"`
	InstanceNo string `json:"instanceNo,omitempty"`
}

// AutoStart - 官方平台 projectNo-only 自动授权入口。
// 首次装机自动申领并激活；后续启动复用 auto.json 与加密状态，不重复申领。
func AutoStart(ctx context.Context, projectNo string, appVersion ...string) (AutoResult, error) {
	if len(appVersion) > 1 {
		return AutoResult{}, errors.New("appVersion 最多只能传一个")
	}
	opts := AutoOptions{ProjectNo: projectNo}
	if len(appVersion) == 1 {
		opts.AppVersion = appVersion[0]
	}
	return AutoStartWithOptions(ctx, opts)
}

// AutoStartWithOptions - 自定义平台或存储/指纹/传输参数的自动授权入口。
func AutoStartWithOptions(ctx context.Context, opts AutoOptions) (AutoResult, error) {
	if strings.TrimSpace(opts.ProjectNo) == "" {
		return AutoResult{}, errors.New("ProjectNo 不能为空")
	}
	if strings.TrimSpace(opts.StorageDir) == "" {
		opts.StorageDir = "./runtime/licence"
	}
	if strings.TrimSpace(opts.ServerURL) == "" {
		opts.ServerURL = DefaultServerURL
	}
	if len(opts.BootstrapPublicKeys) == 0 {
		opts.BootstrapPublicKeys = DefaultBootstrapPublicKeys
	}
	roots := cloneAutoPublicKeys(opts.BootstrapPublicKeys)
	if len(roots) == 0 {
		return AutoResult{}, errors.New("BootstrapPublicKeys 不能为空")
	}

	metadata, restored, err := loadAutoMetadata(opts.StorageDir, opts.ProjectNo)
	if err != nil {
		return AutoResult{}, err
	}
	provisioned := !restored
	var provisionKeyset json.RawMessage
	if provisioned {
		provision, provisionErr := ProvisionByPlan(ctx, ProvisionOptions{
			ServerURL: opts.ServerURL, ProjectNo: opts.ProjectNo, TenantId: opts.TenantId,
			StorageDir: opts.StorageDir, InstallSN: opts.InstallSN, DeviceName: opts.DeviceName,
			Fingerprint: opts.Fingerprint, Provider: opts.Provider,
			Transport: opts.Transport, GRPC: opts.GRPC, HTTPTimeout: opts.HTTPTimeout,
		})
		if provisionErr != nil {
			return AutoResult{}, provisionErr
		}
		metadata = autoMetadata{
			ProjectNo: opts.ProjectNo, LicenseNo: provision.LicenseNo,
			Salt: provision.Salt, InstanceNo: opts.InstanceNo,
		}
		provisionKeyset = provision.Keyset
	}

	client, err := New(Options{
		ServerURL: opts.ServerURL, ProjectNo: opts.ProjectNo,
		LicenseNo: metadata.LicenseNo, InstanceNo: metadata.InstanceNo,
		DeviceName: opts.DeviceName, Salt: metadata.Salt, BootstrapPublicKeys: roots,
		StorageDir: opts.StorageDir, Fingerprint: opts.Fingerprint, Provider: opts.Provider, Store: opts.Store,
		Version: opts.AppVersion, RefreshInterval: opts.RefreshInterval, HTTPTimeout: opts.HTTPTimeout,
		Transport: opts.Transport, GRPC: opts.GRPC, OnStatusChange: opts.OnStatusChange,
		DisableConsumptionReport:  opts.DisableConsumptionReport,
		ConsumptionReportInterval: opts.ConsumptionReportInterval,
		CommandPullTimeout:        opts.CommandPullTimeout, CommandHandlers: opts.CommandHandlers,
		DisableCommands: opts.DisableCommands,
	})
	if err != nil {
		return AutoResult{}, err
	}
	closeWithError := func(err error) (AutoResult, error) {
		_ = client.Close()
		return AutoResult{}, err
	}
	if provisioned {
		candidate, candidateErr := client.verifyKeysetCandidate(provisionKeyset)
		if candidateErr != nil {
			return closeWithError(fmt.Errorf("申领 keyset 验证失败: %w", candidateErr))
		}
		// 未验证 keyset 前不得落 auto.json；写入后才允许进入激活流程。
		if err = saveAutoMetadata(opts.StorageDir, metadata); err != nil {
			return closeWithError(err)
		}
		client.commitKeyset(candidate)
	}
	if err = client.Start(ctx); err != nil {
		return closeWithError(err)
	}

	instanceNo := metadata.InstanceNo
	if envelope, exists := client.Envelope(); exists && strings.TrimSpace(envelope.Payload.InstanceId) != "" {
		instanceNo = strings.TrimSpace(envelope.Payload.InstanceId)
	}
	if instanceNo != metadata.InstanceNo {
		metadata.InstanceNo = instanceNo
		if err = saveAutoMetadata(opts.StorageDir, metadata); err != nil {
			return closeWithError(err)
		}
	}
	return AutoResult{
		Client: client, LicenseNo: metadata.LicenseNo, Salt: metadata.Salt,
		InstanceNo: instanceNo, Provisioned: provisioned,
	}, nil
}

// cloneAutoPublicKeys - 复制根公钥表，避免调用方或默认值后续被外部修改。
func cloneAutoPublicKeys(keys map[string]string) map[string]string {
	cloned := make(map[string]string, len(keys))
	for version, key := range keys {
		cloned[version] = key
	}
	return cloned
}

// loadAutoMetadata - 读取并校验本机元数据；损坏或跨项目文件 fail-closed，不擅自重复申领。
func loadAutoMetadata(storageDir, projectNo string) (autoMetadata, bool, error) {
	raw, err := os.ReadFile(filepath.Join(storageDir, autoMetadataFile))
	if errors.Is(err, os.ErrNotExist) {
		return autoMetadata{}, false, nil
	}
	if err != nil {
		return autoMetadata{}, false, err
	}
	var metadata autoMetadata
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return autoMetadata{}, false, fmt.Errorf("解析 auto.json 失败: %w", err)
	}
	if metadata.ProjectNo != projectNo {
		return autoMetadata{}, false, errors.New("auto.json 项目编号不匹配")
	}
	if strings.TrimSpace(metadata.LicenseNo) == "" || strings.TrimSpace(metadata.Salt) == "" {
		return autoMetadata{}, false, errors.New("auto.json 缺少许可证编号或指纹盐")
	}
	return metadata, true, nil
}

// saveAutoMetadata - 以 0600 权限原子写入非机密元数据。
func saveAutoMetadata(storageDir string, metadata autoMetadata) error {
	if err := os.MkdirAll(storageDir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	path := filepath.Join(storageDir, autoMetadataFile)
	tmp := path + ".tmp"
	defer func() { _ = os.Remove(tmp) }()
	if err = os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err == nil {
		return nil
	}
	// Windows 不允许 Rename 覆盖既有文件时，删除旧元数据后重试。
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp, path)
}
