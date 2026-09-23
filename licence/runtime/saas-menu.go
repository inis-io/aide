package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// SaasMenuWriteInput - 以许可证身份保存菜单清单草稿的参数。
// 与管理面（admin 子包）SaasMenuSaveInput 的区别：无 ProjectId——项目由平台按许可证归属收敛，
// 客户端无法越界指定目标项目。
type SaasMenuWriteInput struct {
	// Id - 清单ID（0=新建递增版本草稿，否则更新既有 draft 行）
	Id int `json:"id,omitempty"`
	// MenuKind - 菜单轨（platform/tenant）
	MenuKind string `json:"menuKind"`
	// Manifest - 清单原文（JSON 字符串，草稿允许半成品）
	Manifest string `json:"manifest"`
	// Remark - 备注
	Remark string `json:"remark,omitempty"`
}

// SaasMenuWriteResult - 菜单清单保存/发布结果。
type SaasMenuWriteResult struct {
	// Id - 清单ID
	Id int `json:"id"`
	// Version - 清单版本号
	Version int `json:"version"`
	// ImpactReport - 发布租户清单时的下游影响报告（仅 publish 返回）
	ImpactReport *SaasMenuImpactReport `json:"impactReport,omitempty"`
}

// SaasMenuImpactItem - 清单发布影响项（运行面与管理面 admin 子包共用，唯一定义在本包）。
type SaasMenuImpactItem struct {
	PlanId     int      `json:"planId,omitempty"`
	PlanCode   string   `json:"planCode,omitempty"`
	TenantId   int      `json:"tenantId,omitempty"`
	TenantCode string   `json:"tenantCode,omitempty"`
	StaleCodes []string `json:"staleCodes"`
}

// SaasMenuImpactReport - 租户清单发布影响报告（运行面与管理面 admin 子包共用，唯一定义在本包）。
type SaasMenuImpactReport struct {
	RemovedCodes    []string             `json:"removedCodes"`
	AffectedPlans   []SaasMenuImpactItem `json:"affectedPlans"`
	AffectedTenants []SaasMenuImpactItem `json:"affectedTenants"`
}

// saasMenuWriteResponse - 许可证签名写操作的统一响应。
type saasMenuWriteResponse struct {
	Status       string          `json:"status"`
	ServerTime   int64           `json:"serverTime"`
	Id           int             `json:"id"`
	Version      int             `json:"version"`
	ImpactReport json.RawMessage `json:"impactReport"`
	Message      string          `json:"message"`
}

// SaasMenuSave - 以许可证身份保存菜单清单草稿（请求 Ed25519 签名）。
// 平台按 license.ProjectId 收敛作用域；仅在实例许可证放行态且席位有效时可写。
func (this *Client) SaasMenuSave(ctx context.Context, input SaasMenuWriteInput) (*SaasMenuWriteResult, error) {
	if this.options.Transport == TransportGRPC {
		return nil, errors.New("SaaS 菜单发布暂不支持 gRPC 传输，请使用 HTTP")
	}

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo,
		"id":        input.Id,
		"menuKind":  input.MenuKind,
		"manifest":  input.Manifest,
		"remark":    input.Remark,
	})
	if err != nil {
		return nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas-menus/save", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("许可证或项目信息无效")
	}
	return this.parseSaasMenuWrite(raw)
}

// SaasMenuPublish - 以许可证身份发布菜单清单（请求 Ed25519 签名）。
// 目标草稿须属于本许可证项目，平台按 project_id 二次断言，防止跨项目发布。
func (this *Client) SaasMenuPublish(ctx context.Context, id int, menuKind string) (*SaasMenuWriteResult, error) {
	if this.options.Transport == TransportGRPC {
		return nil, errors.New("SaaS 菜单发布暂不支持 gRPC 传输，请使用 HTTP")
	}

	this.opMu.Lock()
	defer this.opMu.Unlock()

	body, err := json.Marshal(map[string]any{
		"licenseNo": this.options.LicenseNo,
		"id":        id,
		"menuKind":  menuKind,
	})
	if err != nil {
		return nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/saas-menus/publish", body, true)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, errors.New("许可证或项目信息无效")
	}
	return this.parseSaasMenuWrite(raw)
}

// parseSaasMenuWrite - 解析许可证写操作响应；业务失败映射为 LicenceProtocol.StatusError（原因见 Message）。
func (this *Client) parseSaasMenuWrite(raw []byte) (*SaasMenuWriteResult, error) {
	var response saasMenuWriteResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == LicenceProtocol.StatusError {
		return nil, errors.New("服务端故障：" + response.Message)
	}
	if !LicenceProtocol.PassThrough(response.Status) {
		return nil, errors.New("实例许可证非放行态：" + response.Status)
	}

	result := &SaasMenuWriteResult{Id: response.Id, Version: response.Version}
	if len(response.ImpactReport) > 0 && string(response.ImpactReport) != "null" {
		var impact SaasMenuImpactReport
		if err := json.Unmarshal(response.ImpactReport, &impact); err != nil {
			return nil, err
		}
		result.ImpactReport = &impact
	}
	return result, nil
}

// ============================= 菜单同步执行 =============================

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
	if response.Status == LicenceProtocol.StatusError {
		return nil, errors.New("服务端故障：" + response.Message)
	}
	if !LicenceProtocol.PassThrough(response.Status) {
		return nil, errors.New("实例许可证非放行态：" + response.Status)
	}
	return &SyncTenantMenusResult{
		Trimmed: response.Trimmed, Rebased: response.Rebased,
		Skipped: response.Skipped, Unchanged: response.Unchanged, Count: response.Count,
	}, nil
}
