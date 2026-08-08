package licence

import "context"

// SaasPlansResource - SaaS 套餐模板资源（/api/saas-plans/*）
// 自助即时生效：保存时校验功能字典引用完整性与菜单子集约束；仅 enabled 可被订阅；
// 平台治理手段为全量可见 + 强制 disabled。
type SaasPlansResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Find - 套餐分页（按 sort asc, id desc 排序）：GET /api/saas-plans/find
func (this *SaasPlansResource) Find(ctx context.Context, params *SaasPlanFindParams) (*Page[SaasPlan], error) {

	var result Page[SaasPlan]
	if err := this.client.get(ctx, "/api/saas-plans/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 套餐详情：GET /api/saas-plans/take?id=N
func (this *SaasPlansResource) Take(ctx context.Context, id int) (*SaasPlan, error) {

	var result SaasPlan
	if err := this.client.getWithQuery(ctx, "/api/saas-plans/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create - 新建套餐（初始 draft；保存即做字典引用与菜单子集校验）：POST /api/saas-plans/create
func (this *SaasPlansResource) Create(ctx context.Context, input SaasPlanSaveInput) (*PlanNoResult, error) {

	var result PlanNoResult
	if err := this.client.post(ctx, "/api/saas-plans/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 修改套餐（planCode 不可改；重新校验引用完整性；不波及存量租户与在途申请）：POST /api/saas-plans/update
func (this *SaasPlansResource) Update(ctx context.Context, input SaasPlanSaveInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/saas-plans/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Status - 套餐状态流转（draft→enabled 发布；enabled/disabled 互转；启用前重新校验）：POST /api/saas-plans/status
// status 仅支持 enabled/disabled。
func (this *SaasPlansResource) Status(ctx context.Context, id int, status string, reason string) (*StatusResult, error) {

	var result StatusResult
	if err := this.client.post(ctx, "/api/saas-plans/status", map[string]any{
		"id": id, "status": status, "reason": reason,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 删除套餐（逻辑删除；被租户引用禁止删除）：DELETE /api/saas-plans/delete
func (this *SaasPlansResource) Delete(ctx context.Context, id int) error {

	return this.client.del(ctx, "/api/saas-plans/delete", map[string]any{"id": id}, nil)
}
