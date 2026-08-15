package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// SyncTenantMenusResult - 同步租户菜单（按当前清单裁剪悬空编码并重签）结果。
type SyncTenantMenusResult struct {
	// Ids - 实际被裁剪重签的租户 ID（空切片=无需裁剪）。
	Ids []int `json:"ids"`
	// Count - 裁剪重签的租户数量。
	Count int `json:"count"`
}

type saasSyncMenusResponse struct {
	Status     string `json:"status"`
	ServerTime int64  `json:"serverTime"`
	Ids        []int  `json:"ids"`
	Count      int    `json:"count"`
	Message    string `json:"message"`
}

// SyncTenantMenus - 以许可证身份同步租户菜单：按项目当前 published 租户清单裁剪悬空菜单
// 编码并原子重签租户信封。tenantIds 为空时处理项目下全部受影响租户。
// 用于发布租户清单后收敛存量授权（与 SaasMenuPublish 配套的运行面写通道）。
func (this *Client) SyncTenantMenus(ctx context.Context, tenantIds []int) (*SyncTenantMenusResult, error) {
	if this.options.Transport == TransportGRPC {
		return nil, errors.New("SaaS 租户菜单同步暂不支持 gRPC 传输，请使用 HTTP")
	}

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo,
		"tenantIds": tenantIds,
	})
	if err != nil {
		return nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas/tenants/sync-menus", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("租户或项目信息无效")
	}

	var response saasSyncMenusResponse
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
	return &SyncTenantMenusResult{Ids: response.Ids, Count: response.Count}, nil
}
