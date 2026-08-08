package licence

import "context"

// SaasReviewResource - SaaS 租户申请审批资源（/api/saas-review/*）
// 审批人 = 超管或持 saas.review 权限码用户（视角见全量）；
// approve 单事务生效并签发，reject 需填审批意见（tenant 保持 pending）。
type SaasReviewResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Find - 审批队列分页（支持 status/bizType/userId/projectId 筛选）：GET /api/saas-review/find
func (this *SaasReviewResource) Find(ctx context.Context, params *SaasTenantApplicationFindParams) (*Page[SaasTenantApplication], error) {

	var result Page[SaasTenantApplication]
	if err := this.client.get(ctx, "/api/saas-review/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 审批申请详情：GET /api/saas-review/take?id=N
func (this *SaasReviewResource) Take(ctx context.Context, id int) (*SaasTenantApplication, error) {

	var result SaasTenantApplication
	if err := this.client.getWithQuery(ctx, "/api/saas-review/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Review - 审批租户申请（approve 单事务生效并签发；reject 需填审批意见）：POST /api/saas-review/review
func (this *SaasReviewResource) Review(ctx context.Context, input SaasReviewInput) (*SaasReviewResult, error) {

	var result SaasReviewResult
	if err := this.client.post(ctx, "/api/saas-review/review", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
