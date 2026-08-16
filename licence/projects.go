package licence

import "context"

// ProjectsResource - 项目资源（/api/projects/*）
type ProjectsResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 项目列表（不分页）：GET /api/projects/rows
func (this *ProjectsResource) Rows(ctx context.Context, params *ProjectFindParams) ([]Project, error) {

	var result []Project
	if err := this.client.get(ctx, "/api/projects/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 项目分页：GET /api/projects/find
func (this *ProjectsResource) Find(ctx context.Context, params *ProjectFindParams) (*Page[Project], error) {

	var result Page[Project]
	if err := this.client.get(ctx, "/api/projects/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 项目详情：GET /api/projects/take?id=N
func (this *ProjectsResource) Take(ctx context.Context, id int) (*Project, error) {

	var result Project
	if err := this.client.getWithQuery(ctx, "/api/projects/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create - 新增项目（member 受资格与配额闸门约束，归属用户取登录态）：POST /api/projects/create
func (this *ProjectsResource) Create(ctx context.Context, input ProjectInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/projects/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 编辑项目（项目归属不可通过编辑变更）：PUT /api/projects/update
func (this *ProjectsResource) Update(ctx context.Context, input ProjectInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/projects/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Remove - 逻辑删除（回收站）：DELETE /api/projects/remove
func (this *ProjectsResource) Remove(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/projects/remove", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 物理删除：DELETE /api/projects/delete
func (this *ProjectsResource) Delete(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/projects/delete", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Restore - 恢复回收站数据：PUT /api/projects/restore
func (this *ProjectsResource) Restore(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.put(ctx, "/api/projects/restore", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AutoProvisionConfig - 项目装机自动授权配置（只读）：GET /api/projects/auto-provision-config?id=X
// 返回项目显式值（null=继承平台）+ 平台级默认（含内置回退 90/1000/20/5），权限 project.read。
func (this *ProjectsResource) AutoProvisionConfig(ctx context.Context, id int) (*ProjectAutoProvisionConfig, error) {

	var result ProjectAutoProvisionConfig
	if err := this.client.get(ctx, "/api/projects/auto-provision-config", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SaveAutoProvision - 保存项目装机自动授权配置（全量替换）：PUT /api/projects/save-auto-provision
// Enabled 必填；5 项参数 nil=恢复继承平台（落库 NULL），显式 0=不限制。权限 project.update。
func (this *ProjectsResource) SaveAutoProvision(ctx context.Context, input SaveProjectAutoProvisionInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/projects/save-auto-provision", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ToggleAutoProvision - 切换项目装机自动授权开关（单字段直改）：PUT /api/projects/toggle-auto-provision
// enabled 显式传 true/false。权限 project.update；全量配置（含运维参数）用 SaveAutoProvision。
func (this *ProjectsResource) ToggleAutoProvision(ctx context.Context, id int, enabled bool) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/projects/toggle-auto-provision", map[string]any{
		"id": id, "autoProvisionEnabled": enabled,
	}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
