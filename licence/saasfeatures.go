package licence

import "context"

// SaasFeaturesResource - SaaS 功能字典资源（/api/saas-features/*）
// 项目级登记制：code 项目内唯一、登记/禁用即时生效；被任一套餐引用时禁止物理删除只能禁用。
type SaasFeaturesResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Find - 功能字典分页：GET /api/saas-features/find
func (this *SaasFeaturesResource) Find(ctx context.Context, params *SaasFeatureFindParams) (*Page[SaasFeatureDict], error) {

	var result Page[SaasFeatureDict]
	if err := this.client.get(ctx, "/api/saas-features/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 功能字典详情：GET /api/saas-features/take?id=N
func (this *SaasFeaturesResource) Take(ctx context.Context, id int) (*SaasFeatureDict, error) {

	var result SaasFeatureDict
	if err := this.client.getWithQuery(ctx, "/api/saas-features/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Save - 登记/修改功能（Id=0 登记；登记后 code 不可改，仅名称与说明可改；
// 命中已软删的同 code 记录时恢复复用）：POST /api/saas-features/save
func (this *SaasFeaturesResource) Save(ctx context.Context, input SaasFeatureSaveInput) (*IdResult, error) {

	var result IdResult
	if err := this.client.post(ctx, "/api/saas-features/save", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Disable - 禁用功能（即时生效：套餐不可再引用，存量已签发信封不受影响）：POST /api/saas-features/disable
func (this *SaasFeaturesResource) Disable(ctx context.Context, id int) error {

	return this.client.post(ctx, "/api/saas-features/disable", map[string]any{"id": id}, nil)
}

// Delete - 物理删除功能（被任一套餐引用时禁止删除只能禁用）：DELETE /api/saas-features/delete
func (this *SaasFeaturesResource) Delete(ctx context.Context, id int) error {

	return this.client.del(ctx, "/api/saas-features/delete", map[string]any{"id": id}, nil)
}
