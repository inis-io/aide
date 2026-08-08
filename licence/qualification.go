package licence

import (
	"context"
	"net/http"
	"net/url"

	"github.com/spf13/cast"
)

// QualificationResource - 资格审核资源（/api/qualification/*）
type QualificationResource struct {
	// client - 所属客户端
	client *AdminClient
}

// Apply - 提交资格申请（member 自助，归属用户取登录态）：POST /api/qualification/apply
func (this *QualificationResource) Apply(ctx context.Context, input QualificationApplyInput) (*ApplyResult, error) {

	var result ApplyResult
	if err := this.client.post(ctx, "/api/qualification/apply", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Current - 我的资格状态 + 有效配额 + 最近一条申请：GET /api/qualification/current
func (this *QualificationResource) Current(ctx context.Context) (*QualificationCurrent, error) {

	var result QualificationCurrent
	if err := this.client.get(ctx, "/api/qualification/current", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Mine - 我的申请历史（倒序分页）：GET /api/qualification/mine
func (this *QualificationResource) Mine(ctx context.Context, params *QualificationFindParams) (*Page[QualificationApplication], error) {

	var result Page[QualificationApplication]
	if err := this.client.get(ctx, "/api/qualification/mine", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Rows - 申请队列全部（管理员，不分页，需 qualification.review 权限）：GET /api/qualification/rows
func (this *QualificationResource) Rows(ctx context.Context, params *QualificationFindParams) ([]QualificationApplication, error) {

	var result []QualificationApplication
	if err := this.client.get(ctx, "/api/qualification/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 申请队列分页（管理员，需 qualification.review 权限）：GET /api/qualification/find
func (this *QualificationResource) Find(ctx context.Context, params *QualificationFindParams) (*Page[QualificationApplication], error) {

	var result Page[QualificationApplication]
	if err := this.client.get(ctx, "/api/qualification/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 申请详情（管理员，需 qualification.review 权限）：GET /api/qualification/take?id=N
func (this *QualificationResource) Take(ctx context.Context, id int) (*QualificationApplication, error) {

	var result QualificationApplication
	if err := this.client.getWithQuery(ctx, "/api/qualification/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Review - 审批资格申请（管理员，approve/reject，需 qualification.review 权限）：POST /api/qualification/review
func (this *QualificationResource) Review(ctx context.Context, input QualificationReviewInput) (*ReviewResult, error) {

	var result ReviewResult
	if err := this.client.post(ctx, "/api/qualification/review", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Revoke - 撤销已批准的用户资格（管理员，需 qualification.review 权限）：POST /api/qualification/revoke
func (this *QualificationResource) Revoke(ctx context.Context, userId int, reviewNote string) error {

	return this.client.post(ctx, "/api/qualification/revoke", map[string]any{
		"userId": userId, "reviewNote": reviewNote,
	}, nil)
}

// idQuery - 组装 ?id=N 查询参数（take 类接口共用）
func idQuery(id int) url.Values {
	return url.Values{"id": []string{cast.ToString(id)}}
}

// getWithQuery - 以现成 url.Values 发起 GET（资源层内部共用）
func (this *AdminClient) getWithQuery(ctx context.Context, path string, query url.Values, out any) error {

	data, err := this.doRequest(ctx, http.MethodGet, path, query, nil, "", false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}
