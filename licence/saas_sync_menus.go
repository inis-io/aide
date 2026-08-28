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
	// Trimmed - 仅裁剪悬空码后重签的租户 ID（trim 模式）。
	Trimmed []int `json:"trimmed"`
	// Rebased - 按套餐重新物化授权码后重签的租户 ID（auto 漂移命中 / rebase 强制）。
	Rebased []int `json:"rebased"`
	// Skipped - 收敛后仍为空、被跳过（未重签）的租户。
	Skipped []TenantMenuSyncSkip `json:"skipped"`
	// Unchanged - 判定无需处理（信封与套餐物化结果一致、无悬空码、清单版本未落后）的租户 ID。
	Unchanged []int `json:"unchanged"`
	// Count - 实际重签的租户数量（Trimmed + Rebased）。
	Count int `json:"count"`
}

type saasSyncMenusResponse struct {
	Status     string               `json:"status"`
	ServerTime int64                `json:"serverTime"`
	Trimmed    []int                `json:"trimmed"`
	Rebased    []int                `json:"rebased"`
	Skipped    []TenantMenuSyncSkip `json:"skipped"`
	Unchanged  []int                `json:"unchanged"`
	Count      int                  `json:"count"`
	Message    string               `json:"message"`
}

// SyncTenantMenus - 以许可证身份收敛租户菜单并原子重签。
// mode:
//   - auto（默认，空串亦视为 auto）：漂移感知收敛——信封与「套餐 MenuCodes ∩ 当前清单（叠租户
//     overrides.remove）」物化结果不一致、存在悬空码或清单版本落后时，按套餐重新物化重签；
//     套餐在后台单侧变更（如新勾菜单）必然命中漂移，无需强制 rebase。
//   - trim：仅裁剪信封中相对当前清单的悬空码，不补套餐新增码。
//   - rebase：强制按套餐重新物化全部目标租户（不做漂移判定，一律重签）。
//
// tenantIds 为空时处理项目下全部受影响租户；收敛后菜单为空的租户不签发空信封，计入 Skipped。
// 用于发布租户清单或调整套餐后的存量授权收敛（与 SaasMenuPublish 配套的运行面写通道）。
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
		Skipped: response.Skipped, Unchanged: response.Unchanged, Count: response.Count,
	}, nil
}
