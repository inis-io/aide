package licence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

// 升级记录状态（与平台 updates/report 的 oneof 校验一致）
const (
	UpgradePending     = "pending"     // 已创建任务
	UpgradeDownloading = "downloading" // 下载中
	UpgradeInstalling  = "installing"  // 安装中
	UpgradeSuccess     = "success"     // 升级成功
	UpgradeFailed      = "failed"      // 升级失败
	UpgradeRolledBack  = "rolled_back" // 已回滚
)

// UpdateInfo - 更新检查结果
type UpdateInfo struct {
	// Status - 授权状态（仅放行状态会附带可用清单）
	Status string
	// Available - 是否存在可升级版本
	Available bool
	// Manifest - 已验签的更新清单（Available 为 true 时非空）
	Manifest *Manifest
}

// UpgradeReport - 升级结果上报（RecordNo 为空创建记录，非空推进已有记录状态）
type UpgradeReport struct {
	RecordNo      string
	FromVersion   string
	TargetVersion string
	ArtifactNo    string
	Status        string
	Message       string
}

// checkUpdateBody - 更新检查请求体
type checkUpdateBody struct {
	LicenseNo       string `json:"licenseNo"`
	FingerprintHash string `json:"fingerprintHash"`
	OsArch          string `json:"osArch,omitempty"`
	Version         string `json:"version"`
	ClientTime      int64  `json:"clientTime,omitempty"`
}

// checkUpdateResponse - 更新检查响应
type checkUpdateResponse struct {
	Status     string          `json:"status"`
	ServerTime int64           `json:"serverTime"`
	Update     bool            `json:"update"`
	Manifest   json.RawMessage `json:"manifest"`
	Message    string          `json:"message"`
}

// CheckUpdate - 在线更新检查（契约：阶段五 /api/v1/updates/check）
// 上报当前版本与架构，服务端判定授权状态、升级权（upgradeUntil）与灰度规则后
// 返回 release-key 签名的更新清单；本方法完成清单验签与发布物签名复核。
/**
 * @param osArch string - 平台架构（如 "linux/amd64"，空串 = 不区分）
 * @example：
 * 	info, err := client.CheckUpdate(ctx, "linux/amd64")
 * 	if err == nil && info.Available { ... }
 */
func (this *Client) CheckUpdate(ctx context.Context, osArch string) (UpdateInfo, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(checkUpdateBody{
		LicenseNo: this.options.LicenseNo, FingerprintHash: this.fingerprint,
		OsArch: osArch, Version: this.options.Version, ClientTime: time.Now().UnixMilli(),
	})
	if err != nil {
		return UpdateInfo{}, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/updates/check", body, true)
	if err != nil {
		return UpdateInfo{}, err
	}
	if code == http.StatusNotFound {
		return UpdateInfo{}, errors.New("许可证或实例信息无效")
	}

	var response checkUpdateResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return UpdateInfo{}, err
	}
	this.updateClockOffset(response.ServerTime)

	if response.Status == StatusError {
		return UpdateInfo{}, errors.New("服务端故障：" + response.Message)
	}
	info := UpdateInfo{Status: response.Status}
	if !passThrough(response.Status) || !response.Update {
		return info, nil
	}

	manifest, err := this.VerifyManifest(response.Manifest)
	if err != nil {
		return UpdateInfo{}, err
	}
	// 复核每个发布物自身的 release-key 签名（上传时签名锁定的双层校验）
	for _, artifact := range manifest.Payload.Artifacts {
		if !this.verifyArtifact(artifact, manifest.Payload.Version) {
			return UpdateInfo{}, errors.New("发布物签名验签失败：" + artifact.ArtifactNo)
		}
	}
	info.Available = true
	info.Manifest = manifest
	return info, nil
}

// VerifyManifest - 验签更新清单原文（在线响应与离线更新包 manifest.json 共用）
func (this *Client) VerifyManifest(raw json.RawMessage) (*Manifest, error) {

	manifest, rawPayload, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	publicKey, exist := this.options.ReleasePublicKeys[manifest.Payload.KeyVersion]
	if !exist {
		return nil, errors.New("未内置 keyVersion=" + manifest.Payload.KeyVersion + " 的 release 验签公钥")
	}
	if manifest.Version != EnvelopeVersion || manifest.Algorithm != Algorithm ||
		!Licence.VerifyRaw(rawPayload, manifest.Signature, publicKey) {
		return nil, errors.New("更新清单验签失败")
	}
	return &manifest, nil
}

// verifyArtifact - 复核发布物签名（canonical ArtifactPayload + release-key 公钥）
func (this *Client) verifyArtifact(artifact ManifestArtifact, version string) bool {

	publicKey, exist := this.options.ReleasePublicKeys[artifact.KeyVersion]
	if !exist {
		return false
	}
	payloadBytes, err := json.Marshal(ArtifactPayload{
		ArtifactNo: artifact.ArtifactNo, Version: version, Sha256: artifact.Sha256,
	})
	if err != nil {
		return false
	}
	return Licence.VerifyRaw(payloadBytes, artifact.Signature, publicKey)
}

// DownloadArtifact - 下载发布物并校验（签名复核 + SHA-256 摘要，全部通过才落盘到目标路径）
/**
 * @param manifest *Manifest - CheckUpdate 返回的已验签清单
 * @param artifact ManifestArtifact - 清单中的发布物项
 * @param destPath string - 目标文件路径（先写临时文件，校验通过后重命名）
 */
func (this *Client) DownloadArtifact(ctx context.Context, manifest *Manifest, artifact ManifestArtifact, destPath string) error {

	if manifest == nil {
		return errors.New("manifest 不能为空")
	}
	if !this.verifyArtifact(artifact, manifest.Payload.Version) {
		return errors.New("发布物签名验签失败：" + artifact.ArtifactNo)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.Url, nil)
	if err != nil {
		return err
	}
	// 大文件下载独立长超时（不占用运行面短超时）
	downloader := &http.Client{Timeout: 30 * time.Minute}
	response, err := downloader.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return errors.New("发布物下载失败：HTTP " + response.Status)
	}

	tmp := destPath + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hasher), response.Body)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if artifact.Size > 0 && size != artifact.Size {
		_ = os.Remove(tmp)
		return errors.New("发布物大小不一致")
	}
	if hex.EncodeToString(hasher.Sum(nil)) != artifact.Sha256 {
		_ = os.Remove(tmp)
		return errors.New("发布物 SHA-256 校验失败")
	}
	return os.Rename(tmp, destPath)
}

// ReportUpgrade - 上报升级结果（创建或推进升级记录），返回升级记录编号
func (this *Client) ReportUpgrade(ctx context.Context, report UpgradeReport) (string, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo, "recordNo": report.RecordNo,
		"fromVersion": report.FromVersion, "targetVersion": report.TargetVersion,
		"artifactNo": report.ArtifactNo, "status": report.Status, "message": report.Message,
		"clientTime": time.Now().UnixMilli(),
	})
	if err != nil {
		return "", err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/updates/report", body, true)
	if err != nil {
		return "", err
	}
	if code == http.StatusNotFound {
		return "", errors.New("许可证或实例信息无效")
	}
	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status != StatusValid {
		return "", errors.New("升级上报被拒绝：" + response.Status)
	}

	var parsed struct {
		RecordNo string `json:"recordNo"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil || parsed.RecordNo == "" {
		return "", errors.New("升级上报响应缺少 recordNo")
	}
	return parsed.RecordNo, nil
}

// ReportUpgradeLog - 追加升级过程日志（按行，单行最长 512，单次最多 100 行）
func (this *Client) ReportUpgradeLog(ctx context.Context, recordNo string, lines []string) error {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo, "recordNo": recordNo, "lines": lines,
	})
	if err != nil {
		return err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/updates/logs", body, true)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return errors.New("许可证或实例信息无效")
	}
	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status != StatusValid {
		return errors.New("日志上报被拒绝：" + response.Status)
	}
	return nil
}
