package licence

import "context"

// LicensesResource - 许可证与授权申请资源（/api/licenses/*）
type LicensesResource struct {
	// client - 所属客户端
	client *AdminClient
}

// ============================= 我的许可证 =============================

// Rows - 许可证列表（不分页；非审批视角限本人）：GET /api/licenses/rows
func (this *LicensesResource) Rows(ctx context.Context, params *LicenseFindParams) ([]License, error) {

	var result []License
	if err := this.client.get(ctx, "/api/licenses/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// Find - 许可证分页：GET /api/licenses/find
func (this *LicensesResource) Find(ctx context.Context, params *LicenseFindParams) (*Page[License], error) {

	var result Page[License]
	if err := this.client.get(ctx, "/api/licenses/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Take - 许可证详情（非审批视角限本人）：GET /api/licenses/take?id=N
func (this *LicensesResource) Take(ctx context.Context, id int) (*License, error) {

	var result License
	if err := this.client.getWithQuery(ctx, "/api/licenses/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// TakePayload - 查看签发载荷（载荷/签名原文，非审批视角限本人）：GET /api/licenses/take-payload?id=N
// 返回的 Payload 为载荷 JSON 原文，可配合 aide/licence 包 Parse + 公钥验签。
func (this *LicensesResource) TakePayload(ctx context.Context, id int) (*LicensePayloadView, error) {

	var result LicensePayloadView
	if err := this.client.getWithQuery(ctx, "/api/licenses/take-payload", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PublicKey - 项目许可证验签公钥表（任意登录用户可读）：GET /api/licenses/public-key?projectId=X
// projectId 必填（许可证密钥按项目隔离）；返回全版本公钥表（含历史 rotated keys），
// 装机端据此生成完整 PublicKeys map（与信封载荷 keyVersion 精确对应）。
func (this *LicensesResource) PublicKey(ctx context.Context, projectId int) (*LicensePublicKey, error) {

	var result LicensePublicKey
	if err := this.client.get(ctx, "/api/licenses/public-key", map[string]any{"projectId": projectId}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 授权申请 =============================

// Apply - 提交授权申请（member 自助，归属用户取登录态）：POST /api/licenses/apply
func (this *LicensesResource) Apply(ctx context.Context, input LicenseApplyInput) (*ApplyResult, error) {

	var result ApplyResult
	if err := this.client.post(ctx, "/api/licenses/apply", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Cancel - 撤回授权申请（仅本人 pending 申请可撤回）：POST /api/licenses/cancel
func (this *LicensesResource) Cancel(ctx context.Context, id int) error {

	return this.client.post(ctx, "/api/licenses/cancel", map[string]any{"id": id}, nil)
}

// Applications - 授权申请列表（不分页；审批视角全量+筛选，其余用户仅本人）：GET /api/licenses/applications/rows
func (this *LicensesResource) Applications(ctx context.Context, params *LicenseApplicationFindParams) ([]LicenseApplication, error) {

	var result []LicenseApplication
	if err := this.client.get(ctx, "/api/licenses/applications/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ApplicationTake - 授权申请详情（非审批视角限本人）：GET /api/licenses/applications/take?id=N
func (this *LicensesResource) ApplicationTake(ctx context.Context, id int) (*LicenseApplication, error) {

	var result LicenseApplication
	if err := this.client.getWithQuery(ctx, "/api/licenses/applications/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Review - 审批授权申请（approve 自动签发许可证；reject 需填审批意见，需 license.review 权限）：POST /api/licenses/review
func (this *LicensesResource) Review(ctx context.Context, input LicenseReviewInput) (*ReviewResult, error) {

	var result ReviewResult
	if err := this.client.post(ctx, "/api/licenses/review", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 许可证运维 =============================

// Renew - 续期许可证（重签新期限；revoked 不可续期，需 license.renew 权限）：POST /api/licenses/renew
func (this *LicensesResource) Renew(ctx context.Context, input LicenseActionInput) (*LicenseNoResult, error) {

	var result LicenseNoResult
	if err := this.client.post(ctx, "/api/licenses/renew", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Suspend - 暂停许可证（active → suspended，不重签，Reason 必填，需 license.suspend 权限）：POST /api/licenses/suspend
func (this *LicensesResource) Suspend(ctx context.Context, input LicenseActionInput) (*StatusResult, error) {

	var result StatusResult
	if err := this.client.post(ctx, "/api/licenses/suspend", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Revoke - 吊销许可证（不可逆，Reason 必填，需 license.revoke 权限）：POST /api/licenses/revoke
func (this *LicensesResource) Revoke(ctx context.Context, input LicenseActionInput) (*StatusResult, error) {

	var result StatusResult
	if err := this.client.post(ctx, "/api/licenses/revoke", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Reissue - 重新签发（以现载荷为基础 + IssuePayload 覆盖，重签并全量替换子表，需 license.review 权限）：POST /api/licenses/reissue
func (this *LicensesResource) Reissue(ctx context.Context, input LicenseActionInput) (*LicenseNoResult, error) {

	var result LicenseNoResult
	if err := this.client.post(ctx, "/api/licenses/reissue", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 变更历史与激活记录 =============================

// History - 许可证变更历史分页（非审批视角限本人）：GET /api/licenses/history/rows
func (this *LicensesResource) History(ctx context.Context, params *LicenseHistoryFindParams) (*Page[LicenseHistory], error) {

	var result Page[LicenseHistory]
	if err := this.client.get(ctx, "/api/licenses/history/rows", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HistoryTake - 变更历史详情（非审批视角限本人）：GET /api/licenses/history/take?id=N
func (this *LicensesResource) HistoryTake(ctx context.Context, id int) (*LicenseHistory, error) {

	var result LicenseHistory
	if err := this.client.getWithQuery(ctx, "/api/licenses/history/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Activations - 激活记录列表（不分页；审批视角全量+筛选，其余用户限本人）：GET /api/licenses/activations/rows
func (this *LicensesResource) Activations(ctx context.Context, params *ActivationFindParams) ([]Activation, error) {

	var result []Activation
	if err := this.client.get(ctx, "/api/licenses/activations/rows", params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ActivationTake - 激活记录详情（非审批视角限本人）：GET /api/licenses/activations/take?id=N
func (this *LicensesResource) ActivationTake(ctx context.Context, id int) (*Activation, error) {

	var result Activation
	if err := this.client.getWithQuery(ctx, "/api/licenses/activations/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Seats - 许可证机器席位分页：GET /api/licenses/seats/find。
func (this *LicensesResource) Seats(ctx context.Context, params *LicenseSeatFindParams) (*Page[LicenseSeat], error) {
	var result Page[LicenseSeat]
	if err := this.client.get(ctx, "/api/licenses/seats/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SeatTake - 机器席位详情：GET /api/licenses/seats/take?id=N。
func (this *LicensesResource) SeatTake(ctx context.Context, id int) (*LicenseSeat, error) {
	var result LicenseSeat
	if err := this.client.getWithQuery(ctx, "/api/licenses/seats/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReleaseSeat - 释放机器席位（仅平台用户，需 license.seat.release）：POST /api/licenses/seats/release。
func (this *LicensesResource) ReleaseSeat(ctx context.Context, input LicenseSeatReleaseInput) (*StatusResult, error) {
	var result StatusResult
	if err := this.client.post(ctx, "/api/licenses/seats/release", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
