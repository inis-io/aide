package licence

import (
	"context"
	"net/url"

	"github.com/spf13/cast"
)

// SaasTenantsResource - SaaS 租户资源（/api/saas-tenants/*）
// 租户是 SaaS 轨道授权落点：开通/权益变更集中到唯一审批闸门（申请单），
// 非权益字段直改即时生效；suspend/resume/revoke 即时生效不设审批；
// 平台用户操作直通生效、不产生申请单。
type SaasTenantsResource struct {
	// client - 所属客户端
	client *AdminClient
}

// ============================= 租户查询 =============================

// Find - 租户分页（member 限本人；platform 按范围策略，支持 userId 筛选）：GET /api/saas-tenants/find
func (this *SaasTenantsResource) Find(ctx context.Context, params *SaasTenantFindParams) (*Page[SaasTenant], error) {

	var result Page[SaasTenant]
	if err := this.client.get(ctx, "/api/saas-tenants/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 租户详情：GET /api/saas-tenants/take?id=N
func (this *SaasTenantsResource) Take(ctx context.Context, id int) (*SaasTenant, error) {

	var result SaasTenant
	if err := this.client.getWithQuery(ctx, "/api/saas-tenants/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// TakePayload - 查看租户授权原文（payload/signature，仅归属人/平台可见）：GET /api/saas-tenants/take-payload?id=N
// 返回的 Payload 为载荷 JSON 原文，可用本包 TenantPayload 解析并配合公钥验签。
func (this *SaasTenantsResource) TakePayload(ctx context.Context, id int) (*SaasTenantPayloadView, error) {

	var result SaasTenantPayloadView
	if err := this.client.getWithQuery(ctx, "/api/saas-tenants/take-payload", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 开通与变更 =============================

// Subscribe - 租户开通申请（member 创建 pending 行 + 申请单，命中自动过单同事务生效；
// platform 直通生效不产生申请单）：POST /api/saas-tenants/subscribe
func (this *SaasTenantsResource) Subscribe(ctx context.Context, input SaasTenantSubscribeInput) (*SaasTenantSubscribeResult, error) {

	var result SaasTenantSubscribeResult
	if err := this.client.post(ctx, "/api/saas-tenants/subscribe", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Change - 租户权益变更申请（仅 active 可发起且一律人工审批；pending 租户为驳回后重新提审；
// platform 直通生效）：POST /api/saas-tenants/change
func (this *SaasTenantsResource) Change(ctx context.Context, input SaasTenantChangeInput) (*SaasTenantChangeResult, error) {

	var result SaasTenantChangeResult
	if err := this.client.post(ctx, "/api/saas-tenants/change", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateInfo - 租户非权益字段直改（tenantName/contact 即时生效 + 审计）：POST /api/saas-tenants/update-info
func (this *SaasTenantsResource) UpdateInfo(ctx context.Context, input SaasTenantInfoUpdateInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/saas-tenants/update-info", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Cancel - 撤回租户申请（仅本人 pending 申请可撤回）：POST /api/saas-tenants/cancel
func (this *SaasTenantsResource) Cancel(ctx context.Context, id int) error {

	return this.client.post(ctx, "/api/saas-tenants/cancel", map[string]any{"id": id}, nil)
}

// Delete - 删除租户（仅 pending 可删，软删；名下 pending 申请单一并流转 cancelled）：DELETE /api/saas-tenants/delete
func (this *SaasTenantsResource) Delete(ctx context.Context, id int) error {

	return this.client.del(ctx, "/api/saas-tenants/delete", map[string]any{"id": id}, nil)
}

// ============================= 状态机与重签 =============================

// Suspend - 暂停租户（active → suspended，即时生效不设审批；不重签，reason 必填）：POST /api/saas-tenants/suspend
func (this *SaasTenantsResource) Suspend(ctx context.Context, id int, reason string) (*StatusResult, error) {

	return this.transition(ctx, "suspend", id, reason)
}

// Resume - 恢复租户（suspended → active，即时生效，reason 必填）：POST /api/saas-tenants/resume
func (this *SaasTenantsResource) Resume(ctx context.Context, id int, reason string) (*StatusResult, error) {

	return this.transition(ctx, "resume", id, reason)
}

// Revoke - 吊销租户（active/suspended → revoked，不可逆，reason 必填）：POST /api/saas-tenants/revoke
func (this *SaasTenantsResource) Revoke(ctx context.Context, id int, reason string) (*StatusResult, error) {

	return this.transition(ctx, "revoke", id, reason)
}

// transition - 租户状态机流转共用出口（suspend/resume/revoke）
func (this *SaasTenantsResource) transition(ctx context.Context, action string, id int, reason string) (*StatusResult, error) {

	var result StatusResult
	if err := this.client.post(ctx, "/api/saas-tenants/"+action, map[string]any{
		"id": id, "reason": reason,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Reissue - 重签租户授权（平台纠错通道：以现载荷为基础按入参覆盖，空值沿用现载荷）：POST /api/saas-tenants/reissue
func (this *SaasTenantsResource) Reissue(ctx context.Context, input SaasTenantReissueInput) (*SaasTenantNoResult, error) {

	var result SaasTenantNoResult
	if err := this.client.post(ctx, "/api/saas-tenants/reissue", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BatchRenew - 租户批量续期（仅 active/suspended 可续；member 逐租户生成 change 申请单走审批，
// platform 直通生效重签；ids 须全部处于写数据范围内否则整体拒绝）：POST /api/saas-tenants/batch-renew
func (this *SaasTenantsResource) BatchRenew(ctx context.Context, input SaasTenantBatchRenewInput) (*SaasTenantBatchRenewResult, error) {

	var result SaasTenantBatchRenewResult
	if err := this.client.post(ctx, "/api/saas-tenants/batch-renew", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 申请单 =============================

// Applications - 我的申请分页（member 限本人；platform 按范围可见）：GET /api/saas-tenants/applications/find
func (this *SaasTenantsResource) Applications(ctx context.Context, params *SaasTenantApplicationFindParams) (*Page[SaasTenantApplication], error) {

	var result Page[SaasTenantApplication]
	if err := this.client.get(ctx, "/api/saas-tenants/applications/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ApplicationTake - 我的申请详情（非审批视角限本人申请）：GET /api/saas-tenants/applications/take?id=N
func (this *SaasTenantsResource) ApplicationTake(ctx context.Context, id int) (*SaasTenantApplication, error) {

	var result SaasTenantApplication
	if err := this.client.getWithQuery(ctx, "/api/saas-tenants/applications/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 用量与留痕 =============================

// UsageFind - 租户用量历史分页（按租户/额度项/时间段过滤）：GET /api/saas-tenants/usage/find
func (this *SaasTenantsResource) UsageFind(ctx context.Context, params *SaasTenantUsageFindParams) (*Page[SaasTenantUsageRow], error) {

	var result Page[SaasTenantUsageRow]
	if err := this.client.get(ctx, "/api/saas-tenants/usage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UsageSummary - 租户用量水位（每额度项最新上报值 vs 载荷 limits 上限）：GET /api/saas-tenants/usage/summary?id=N
func (this *SaasTenantsResource) UsageSummary(ctx context.Context, id int) (*SaasTenantUsageSummary, error) {

	var result SaasTenantUsageSummary
	if err := this.client.getWithQuery(ctx, "/api/saas-tenants/usage/summary", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HistoryExport - 租户留痕导出（CSV 文本包裹在 JSON 内返回；tenantId/projectId 可选过滤，
// 0=不过滤；member 经数据范围强制本人范围）：GET /api/saas-tenants/history/export
func (this *SaasTenantsResource) HistoryExport(ctx context.Context, tenantId int, projectId int) (*SaasTenantHistoryExport, error) {

	query := url.Values{}
	if tenantId > 0 {
		query.Set("tenantId", cast.ToString(tenantId))
	}
	if projectId > 0 {
		query.Set("projectId", cast.ToString(projectId))
	}

	var result SaasTenantHistoryExport
	if err := this.client.getWithQuery(ctx, "/api/saas-tenants/history/export", query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
