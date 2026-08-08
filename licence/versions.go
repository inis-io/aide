package licence

import "context"

// VersionsResource - 项目版本资源（/api/project-versions/*）
type VersionsResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Rows - 版本列表（不分页）：GET /api/project-versions/rows
func (this *VersionsResource) Rows(ctx context.Context, params *VersionFindParams) ([]ProjectVersion, error) {

	var result []ProjectVersion
	if err := this.client.get(ctx, "/api/project-versions/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 版本分页：GET /api/project-versions/find
func (this *VersionsResource) Find(ctx context.Context, params *VersionFindParams) (*Page[ProjectVersion], error) {

	var result Page[ProjectVersion]
	if err := this.client.get(ctx, "/api/project-versions/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 版本详情：GET /api/project-versions/take?id=N
func (this *VersionsResource) Take(ctx context.Context, id int) (*ProjectVersion, error) {

	var result ProjectVersion
	if err := this.client.getWithQuery(ctx, "/api/project-versions/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Create - 新建版本草稿（仅允许 draft/testing；发布与归档走专用接口）：POST /api/project-versions/create
func (this *VersionsResource) Create(ctx context.Context, input VersionInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/project-versions/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Update - 更新版本（released/archived 版本仅 remark/supportUntil 允许修改）：PUT /api/project-versions/update
func (this *VersionsResource) Update(ctx context.Context, input VersionInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/project-versions/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Release - 发布版本（需至少 1 个已锁定发布物；更新日志若存在须已发布）：POST /api/project-versions/release
func (this *VersionsResource) Release(ctx context.Context, id int) (*ReleaseResult, error) {

	var result ReleaseResult
	if err := this.client.post(ctx, "/api/project-versions/release", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Archive - 归档版本（仅已发布版本可归档，构建/发布物/更新日志全部保留）：PUT /api/project-versions/archive
func (this *VersionsResource) Archive(ctx context.Context, id int) (*IdResult, error) {

	var result IdResult
	if err := this.client.put(ctx, "/api/project-versions/archive", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Remove - 逻辑删除（已发布/已归档版本禁止删除）：DELETE /api/project-versions/remove
func (this *VersionsResource) Remove(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-versions/remove", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Delete - 物理删除（已发布/已归档版本禁止删除）：DELETE /api/project-versions/delete
func (this *VersionsResource) Delete(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.del(ctx, "/api/project-versions/delete", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Restore - 恢复回收站数据：PUT /api/project-versions/restore
func (this *VersionsResource) Restore(ctx context.Context, ids []int) (*IdsResult, error) {

	var result IdsResult
	if err := this.client.put(ctx, "/api/project-versions/restore", map[string]any{"ids": ids}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
