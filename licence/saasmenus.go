package licence

import "context"

// SaasMenusResource - SaaS 菜单清单资源（/api/saas-menus/*）
// 服务商自助定义即时生效：草稿可反复修改，发布时结构校验并同事务归档旧 published；
// 平台治理手段为全量可见 + 强制归档。
type SaasMenusResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Find - 菜单清单分页（按 project_id asc, version desc 排序）：GET /api/saas-menus/find
func (this *SaasMenusResource) Find(ctx context.Context, params *SaasMenuFindParams) (*Page[SaasMenuManifest], error) {

	var result Page[SaasMenuManifest]
	if err := this.client.get(ctx, "/api/saas-menus/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 菜单清单详情：GET /api/saas-menus/take?id=N
func (this *SaasMenusResource) Take(ctx context.Context, id int) (*SaasMenuManifest, error) {

	var result SaasMenuManifest
	if err := this.client.getWithQuery(ctx, "/api/saas-menus/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Save - 保存清单草稿（Id=0 新建递增版本草稿，否则更新既有 draft 行）：POST /api/saas-menus/save
func (this *SaasMenusResource) Save(ctx context.Context, input SaasMenuSaveInput) (*SaasMenuSaveResult, error) {

	var result SaasMenuSaveResult
	if err := this.client.post(ctx, "/api/saas-menus/save", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Publish - 发布清单（结构校验通过后转 published，旧 published 同事务转 archived）：POST /api/saas-menus/publish
func (this *SaasMenusResource) Publish(ctx context.Context, id int) (*SaasMenuSaveResult, error) {

	var result SaasMenuSaveResult
	if err := this.client.post(ctx, "/api/saas-menus/publish", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Archive - 强制归档清单（reason 必填；平台操作写审计留痕）：POST /api/saas-menus/archive
func (this *SaasMenusResource) Archive(ctx context.Context, id int, reason string) error {

	return this.client.post(ctx, "/api/saas-menus/archive", map[string]any{
		"id": id, "reason": reason,
	}, nil)
}
