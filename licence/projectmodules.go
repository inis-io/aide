package licence

import "context"

// ProjectModulesResource - 项目功能模块资源（/api/project-modules/*）
type ProjectModulesResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 模块列表（不分页）：GET /api/project-modules/rows
func (this *ProjectModulesResource) Rows(ctx context.Context, params *ProjectModuleFindParams) ([]ProjectModule, error) {

	var result []ProjectModule
	if err := this.client.get(ctx, "/api/project-modules/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 模块分页：GET /api/project-modules/find
func (this *ProjectModulesResource) Find(ctx context.Context, params *ProjectModuleFindParams) (*Page[ProjectModule], error) {

	var result Page[ProjectModule]
	if err := this.client.get(ctx, "/api/project-modules/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 模块详情：GET /api/project-modules/take?id=N
func (this *ProjectModulesResource) Take(ctx context.Context, id int) (*ProjectModule, error) {

	var result ProjectModule
	if err := this.client.getWithQuery(ctx, "/api/project-modules/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create - 新增模块（moduleCode 项目内唯一）：POST /api/project-modules/create
func (this *ProjectModulesResource) Create(ctx context.Context, input ProjectModuleInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/project-modules/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 编辑模块：PUT /api/project-modules/update
func (this *ProjectModulesResource) Update(ctx context.Context, input ProjectModuleInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/project-modules/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Sort - 修改排序（mode：up-上移 / down-下移）：PUT /api/project-modules/sort
func (this *ProjectModulesResource) Sort(ctx context.Context, id int, mode string) error {

	return this.client.put(ctx, "/api/project-modules/sort", map[string]any{
		"id": id, "mode": mode,
	}, nil)
}

// Remove - 逻辑删除（回收站）：DELETE /api/project-modules/remove
func (this *ProjectModulesResource) Remove(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-modules/remove", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 物理删除：DELETE /api/project-modules/delete
func (this *ProjectModulesResource) Delete(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-modules/delete", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Restore - 恢复回收站数据：PUT /api/project-modules/restore
func (this *ProjectModulesResource) Restore(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.put(ctx, "/api/project-modules/restore", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
