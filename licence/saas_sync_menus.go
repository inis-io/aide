package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// TenantMenuSyncSkip - 收敛中被跳过（未重签）的租户。
type TenantMenuSyncSkip struct {
	TenantId   int    `json:"tenantId"`
	TenantCode string `json:"tenantCode"`
	Reason     string `json:"reason"`
}

// SyncTenantMenusResult - 同步租户菜单（收敛悬空编码并重签）结果。
type SyncTenantMenusResult struct {
	// Trimmed - 仅裁剪悬空码后重签的租户 ID。
	Trimmed []int `json:"trimmed"`
	// Rebased - 按套餐重新物化授权码后重签的租户 ID。
	Rebased []int `json:"rebased"`
	// Skipped - 收敛后仍为空、被跳过（未重签）的租户。
	Skipped []TenantMenuSyncSkip `json:"skipped"`
	// Count - 实际重签的租户数量（Trimmed + Rebased）。
	Count int `json:"count"`
}

type saasSyncMenusResponse struct {
	Status     string               `json:"status"`
	ServerTime int64                `json:"serverTime"`
	Trimmed    []int                `json:"trimmed"`
	Rebased    []int                `json:"rebased"`
	Skipped    []TenantMenuSyncSkip `json:"skipped"`
	Count      int                  `json:"count"`
	Message    string               `json:"message"`
}

// SyncTenantMenus - 以许可证身份收敛租户菜单：按项目当前 published 租户清单裁剪悬空菜单编码
// （trim 后为空则按套餐 rebase），跳过收敛后仍为空的租户，并原子重签。
// mode: auto(默认，空串亦视为 auto)/trim/rebase；tenantIds 为空时处理项目下全部受影响租户。
// 用于发布租户清单后收敛存量授权（与 SaasMenuPublish 配套的运行面写通道）。
func (this *Client) SyncTenantMenus(ctx context.Context, tenantIds []int, mode string) (*SyncTenantMenusResult, error) {
	if this.options.Transport == TransportGRPC {
		return nil, errors.New("SaaS 租户菜单同步暂不支持 gRPC 传输，请使用 HTTP")
	}

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo,
		"tenantIds": tenantIds,
		"mode":      mode,
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
	return &SyncTenantMenusResult{
		Trimmed: response.Trimmed, Rebased: response.Rebased,
		Skipped: response.Skipped, Count: response.Count,
	}, nil
}
