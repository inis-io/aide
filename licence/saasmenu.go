package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// SaasMenuWriteInput - 以许可证身份保存菜单清单草稿的参数。
// 与 AdminClient.SaasMenuSaveInput 的区别：无 ProjectId——项目由平台按许可证归属收敛，
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

// parseSaasMenuWrite - 解析许可证写操作响应；业务失败映射为 StatusError（原因见 Message）。
func (this *Client) parseSaasMenuWrite(raw []byte) (*SaasMenuWriteResult, error) {
	var response saasMenuWriteResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == StatusError {
		return nil, errors.New("服务端故障：" + response.Message)
	}
	if !passThrough(response.Status) {
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
