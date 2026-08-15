package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
)

// TenantInfo - 租户运行状态（sync 下发项；放行租户携带已验签信封）
type TenantInfo struct {
	// TenantCode - 租户编码
	TenantCode string
	// Status - 租户状态（VALID/EXPIRING/GRACE/...，无实例专属状态）
	Status string
	// Envelope - 已验签的租户授权信封（非放行租户为 nil）
	Envelope *TenantEnvelope
}

// TenantManifest - 按轨（platform/tenant）下发的项目菜单清单（sync 随响应下发，未发布的轨为 nil）
type TenantManifest struct {
	// Version - 清单版本
	Version int `json:"version"`
	// Menus - 菜单树原文（结构由 SaaS 项目自行解释）
	Menus json.RawMessage `json:"menus"`
}

// TenantManifests - 项目平台/租户双轨菜单清单；未发布的轨为 nil。
type TenantManifests struct {
	Platform *TenantManifest `json:"platform"`
	Tenant   *TenantManifest `json:"tenant"`
}

// ManifestMenuRoute - 菜单路由定义。
type ManifestMenuRoute struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Component string `json:"component"`
	Title     string `json:"title"`
	Icon      string `json:"icon"`
	Sort      int    `json:"sort"`
}

// ManifestMenu - 双轨清单菜单条目。
type ManifestMenu struct {
	Code       string            `json:"code"`
	ParentCode string            `json:"parentCode"`
	Type       string            `json:"type"`
	Route      ManifestMenuRoute `json:"route"`
	Feature    string            `json:"feature"`
	Hidden     bool              `json:"hidden"`
}

// TenantValidateOptions - 单租户实时校验的可选判定输入
type TenantValidateOptions struct {
	// Version - 当前版本（上送则按租户载荷 versionRange 判定）
	Version string
	// Feature - 待校验功能编码
	Feature string
	// Usage - 用量上报（服务端按小时水位落库去重，重试安全）
	Usage map[string]int64
}

// TenantSearchItem - 租户编码前缀搜索结果。
type TenantSearchItem struct {
	TenantCode string `json:"tenantCode"`
	TenantName string `json:"tenantName"`
}

// tenantCacheItem - 租户信封缓存项
type tenantCacheItem struct {
	// status - 最近一次同步/校验的租户状态
	status string
	// envelope - 已验签信封（非放行租户为 nil）
	envelope *TenantEnvelope
}

// syncResponse - 租户同步响应
type syncResponse struct {
	Status     string           `json:"status"`
	ServerTime int64            `json:"serverTime"`
	SyncTime   int64            `json:"syncTime"`
	Manifests  *TenantManifests `json:"manifests"`
	Tenants    []struct {
		TenantCode string          `json:"tenantCode"`
		Status     string          `json:"status"`
		Envelope   json.RawMessage `json:"envelope"`
	} `json:"tenants"`
	Message string `json:"message"`
}

// tenantResponse - 租户校验/当前信封响应
type tenantResponse struct {
	Status     string          `json:"status"`
	ServerTime int64           `json:"serverTime"`
	Envelope   json.RawMessage `json:"envelope"`
	Message    string          `json:"message"`
}

// tenantSearchResponse - 租户编码前缀搜索响应。
type tenantSearchResponse struct {
	Status     string             `json:"status"`
	ServerTime int64              `json:"serverTime"`
	Tenants    []TenantSearchItem `json:"tenants"`
	Message    string             `json:"message"`
}

// TenantSync - 租户授权全量/增量同步（每小时 + 启动时调用）
// sinceTime 为增量水位线（毫秒，0 = 全量）；返回本次同步时间与项目双轨菜单清单。
// 放行租户的信封验签后写入本地缓存；平台不可达时返回错误，本地缓存继续可用（TenantStatus）。
/**
 * @param sinceTime int64 - 增量水位线（上次返回的 syncTime；0 = 全量）
 * @example：
 * 	syncTime, manifests, err := client.TenantSync(ctx, 0)
 */
func (this *Client) TenantSync(ctx context.Context, sinceTime int64) (int64, *TenantManifests, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{"licenseNo": this.options.LicenseNo, "sinceTime": sinceTime})
	if err != nil {
		return 0, nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas/tenants/sync", body, true)
	if err != nil {
		return 0, nil, err
	}
	if code == http.StatusNotFound {
		return 0, nil, errors.New("租户或项目信息无效")
	}

	var response syncResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return 0, nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == StatusError {
		return 0, nil, errors.New("服务端故障：" + response.Message)
	}
	// 前置闸门：实例许可证非放行态时以其状态码直接响应（fail-closed 总闸）
	if !passThrough(response.Status) {
		return 0, nil, errors.New("实例许可证非放行态：" + response.Status)
	}

	// 全量同步时以本次响应整体替换缓存（清除已删除租户）；增量同步合并覆盖。
	// 整体替换采用原子 swap，避免与请求路径的 TenantStatus/TenantFeature 并发读竞态。
	entries := make(map[string]tenantCacheItem, len(response.Tenants))
	for _, item := range response.Tenants {
		cached := tenantCacheItem{status: item.Status}
		if passThrough(item.Status) && len(item.Envelope) > 0 {
			envelope, err := this.verifyTenantEnvelope(item.Envelope)
			if err != nil {
				return 0, nil, err
			}
			cached.envelope = envelope
		}
		entries[item.TenantCode] = cached
	}
	this.mu.Lock()
	if sinceTime == 0 {
		this.tenantCache = entries
	} else {
		if this.tenantCache == nil {
			this.tenantCache = make(map[string]tenantCacheItem)
		}
		for code, cached := range entries {
			this.tenantCache[code] = cached
		}
	}
	this.mu.Unlock()
	return response.SyncTime, response.Manifests, nil
}

// TenantCodes - 返回本地租户缓存中的编码列表（升序）。
// 缓存由 TenantSync 全量/增量写入；全量同步（sinceTime=0）后即 Hub 当前租户全集。
// 供对账/枚举使用：不发起网络请求，读取的是最近一次同步的结果。
func (this *Client) TenantCodes() []string {
	this.mu.RLock()
	codes := make([]string, 0, len(this.tenantCache))
	for code := range this.tenantCache {
		codes = append(codes, code)
	}
	this.mu.RUnlock()
	sort.Strings(codes)
	return codes
}

// TenantSearch - 按租户编码前缀搜索当前许可证项目下可登录的租户。
// prefix 至少 3 位；服务端固定限制返回条数，避免登录页枚举全部租户。
func (this *Client) TenantSearch(ctx context.Context, prefix string) ([]TenantSearchItem, error) {

	prefix = strings.TrimSpace(prefix)
	if len(prefix) < 3 || len(prefix) > 64 {
		return nil, errors.New("租户编码搜索前缀长度必须为 3 到 64 位")
	}

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{"licenseNo": this.options.LicenseNo, "prefix": prefix})
	if err != nil {
		return nil, err
	}
	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas/tenants/search", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("租户或项目信息无效")
	}

	var response tenantSearchResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == StatusError {
		return nil, errors.New("服务端故障：" + response.Message)
	}
	if !passThrough(response.Status) {
		return nil, errors.New("实例许可证非放行态：" + response.Status)
	}
	if response.Tenants == nil {
		return []TenantSearchItem{}, nil
	}
	return response.Tenants, nil
}

// FilterManifestMenus - 按签名载荷 menuCodes 过滤租户清单，保持清单原顺序与 parentCode。
// 平台菜单不应调用本函数裁剪；hidden 节点仍会返回，由接入方注册路由但不展示导航。
func FilterManifestMenus(manifest *TenantManifest, codes []string) ([]ManifestMenu, error) {
	if manifest == nil {
		return nil, nil
	}
	var menus []ManifestMenu
	if err := json.Unmarshal(manifest.Menus, &menus); err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(codes))
	for _, code := range codes {
		allowed[code] = true
	}
	result := make([]ManifestMenu, 0, len(menus))
	for _, menu := range menus {
		if allowed[menu.Code] {
			result = append(result, menu)
		}
	}
	return result, nil
}

// TenantValidate - 单租户实时校验（租户用户登录/访问受控功能时调用）
// 放行返回状态码并把信封写入本地缓存；非放行只返回状态码。
func (this *Client) TenantValidate(ctx context.Context, tenantCode string, options TenantValidateOptions) (string, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo, "tenantCode": tenantCode,
		"version": options.Version, "feature": options.Feature, "usage": options.Usage,
	})
	if err != nil {
		return "", err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas/tenants/validate", body, true)
	if err != nil {
		return "", err
	}
	if code == http.StatusNotFound {
		return "", errors.New("租户或项目信息无效")
	}

	var response tenantResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == StatusError {
		return "", errors.New("服务端故障：" + response.Message)
	}

	cached := tenantCacheItem{status: response.Status}
	if passThrough(response.Status) && len(response.Envelope) > 0 {
		envelope, err := this.verifyTenantEnvelope(response.Envelope)
		if err != nil {
			return "", err
		}
		cached.envelope = envelope
	}
	this.mu.Lock()
	this.tenantCache[tenantCode] = cached
	this.mu.Unlock()
	return response.Status, nil
}

// TenantCurrent - 取租户当前生效信封（不更新缓存水位，仅按需拉取）
func (this *Client) TenantCurrent(ctx context.Context, tenantCode string) (*TenantEnvelope, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	uri := "/api/v1/saas/tenants/current?licenseNo=" + this.options.LicenseNo + "&tenantCode=" + tenantCode
	code, raw, err := this.doRequest(ctx, http.MethodGet, uri, nil, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("租户或项目信息无效")
	}

	var response tenantResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if !passThrough(response.Status) {
		return nil, errors.New("租户授权不可用：" + response.Status)
	}
	return this.verifyTenantEnvelope(response.Envelope)
}

// TenantStatus - 租户本地状态：优先返回缓存的服务端判定，再按缓存信封做时间维度本地判定
// （平台不可达时的降级判定依据；无缓存返回空串）。fail-open/fail-closed 策略由服务商
// 依据本方法结果自行决策并写入集成文档。
func (this *Client) TenantStatus(tenantCode string) string {

	this.mu.RLock()
	cached, exist := this.tenantCache[tenantCode]
	this.mu.RUnlock()
	if !exist {
		return ""
	}
	if cached.envelope == nil {
		return cached.status
	}
	return localStatus(this.now(), cached.envelope.Payload.ValidUntil, cached.envelope.Payload.GraceDays)
}

// TenantFeature - 租户功能权益本地判定：缓存信封放行且 features[code] 为 true
func (this *Client) TenantFeature(tenantCode string, code string) bool {

	this.mu.RLock()
	cached, exist := this.tenantCache[tenantCode]
	this.mu.RUnlock()
	if !exist || cached.envelope == nil {
		return false
	}
	return passThrough(localStatus(this.now(), cached.envelope.Payload.ValidUntil, cached.envelope.Payload.GraceDays)) &&
		cached.envelope.Payload.Features[code]
}

// verifyTenantEnvelope - 验签租户信封（license-key，公钥按载荷 keyVersion 选取）
func (this *Client) verifyTenantEnvelope(raw json.RawMessage) (*TenantEnvelope, error) {

	envelope, rawPayload, err := ParseTenantEnvelope(raw)
	if err != nil {
		return nil, err
	}
	publicKey, exist := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exist {
		return nil, errors.New("未内置 keyVersion=" + envelope.Payload.KeyVersion + " 的验签公钥")
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm ||
		!Licence.VerifyRaw(rawPayload, envelope.Signature, publicKey) {
		return nil, errors.New("租户信封验签失败")
	}
	return &envelope, nil
}
