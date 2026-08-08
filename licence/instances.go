package licence

import "context"

// InstancesResource - 部署实例资源（/api/instances/*）
type InstancesResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 实例列表（不分页）：GET /api/instances/rows
func (this *InstancesResource) Rows(ctx context.Context, params *InstanceFindParams) ([]DeploymentInstance, error) {

	var result []DeploymentInstance
	if err := this.client.get(ctx, "/api/instances/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 实例分页：GET /api/instances/find
func (this *InstancesResource) Find(ctx context.Context, params *InstanceFindParams) (*Page[DeploymentInstance], error) {

	var result Page[DeploymentInstance]
	if err := this.client.get(ctx, "/api/instances/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 实例详情：GET /api/instances/take?id=N
func (this *InstancesResource) Take(ctx context.Context, id int) (*DeploymentInstance, error) {

	var result DeploymentInstance
	if err := this.client.getWithQuery(ctx, "/api/instances/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create - 新增实例（归属用户取登录态；服务器指纹原文提交，平台加盐哈希存储）：POST /api/instances/create
func (this *InstancesResource) Create(ctx context.Context, input InstanceInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/instances/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 编辑实例（实例归属不可通过编辑变更）：PUT /api/instances/update
func (this *InstancesResource) Update(ctx context.Context, input InstanceInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/instances/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Remove - 逻辑删除（回收站）：DELETE /api/instances/remove
func (this *InstancesResource) Remove(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/instances/remove", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 物理删除：DELETE /api/instances/delete
func (this *InstancesResource) Delete(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/instances/delete", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Restore - 恢复回收站数据：PUT /api/instances/restore
func (this *InstancesResource) Restore(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.put(ctx, "/api/instances/restore", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
