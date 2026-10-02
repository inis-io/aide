package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/inis-io/aide/licence/apis/core"
)

// 本文件承载星链中枢清结算（StarBilling）的 SDK 公开方法：点数购入/发放核销、
// 投流单创建/结算/取消、投流阅读上报。
//
// 资金语义（设计 §5）：钱包是资金账、点数池是库存账，冻结的是真钱；
// 一切写操作幂等键 = 业务单号，经 X-Request-Id / metadata x-request-id 透传
// （服务端防呆：非空幂等键必须等于业务单号）。业务单号支持显式传入，
// 缺省经 core.ResolveRequestID 自动生成（req_ 前缀），生成值在结果中回显供对账。

// StarCreditPurchaseInput - 点数购入入参
type StarCreditPurchaseInput struct {
	// PurchaseNo - 购入单号（幂等键；缺省自动生成并在结果中回显）
	PurchaseNo string
	// Points - 购入点数（> 0）
	Points int64
	// AmountFen - 支付金额（万分；服务端校验 = Points × 汇率快照）
	AmountFen int64
	// RateFen - 汇率快照（1 点 = RateFen 万分，0 = 取平台配置 point_rate_fen）
	RateFen int64
}

// StarCreditPurchaseResult - 点数购入回执
type StarCreditPurchaseResult struct {
	// PurchaseNo - 实际生效的购入单号（显式传入的原样回显，缺省的为自动生成值；对账依据）
	PurchaseNo string
}

// CreditPurchase - 点数购入：舰长钱包 consume → 点数池入账（汇率快照存单）。
// 幂等键 request_id = PurchaseNo；重复调用返回既有结果不双花。
/**
 * @param ctx context.Context - 调用上下文
 * @param input StarCreditPurchaseInput - 购入点数/金额/汇率
 * @return StarCreditPurchaseResult - 生效购入单号
 * @example：
 * 	result, err := client.Star.CreditPurchase(ctx, licence.StarCreditPurchaseInput{Points: 100, AmountFen: 1000000})
 */
func (this *StarService) CreditPurchase(ctx context.Context, input StarCreditPurchaseInput) (StarCreditPurchaseResult, error) {

	if input.Points <= 0 {
		return StarCreditPurchaseResult{}, errors.New("star: 购入点数必须大于 0")
	}
	purchaseNo := core.ResolveRequestID(input.PurchaseNo)
	body, err := json.Marshal(map[string]any{
		"purchaseNo": purchaseNo, "points": input.Points, "amountFen": input.AmountFen, "rateFen": input.RateFen,
	})
	if err != nil {
		return StarCreditPurchaseResult{}, err
	}
	if _, err = this.call(ctx, "/api/v1/star/billing/purchase", body, purchaseNo); err != nil {
		return StarCreditPurchaseResult{}, err
	}
	return StarCreditPurchaseResult{PurchaseNo: purchaseNo}, nil
}

// StarGrantOutInput - 点数发放核销入参
type StarGrantOutInput struct {
	// GrantNo - 发放核销单号（幂等键；缺省自动生成并在结果中回显）
	GrantNo string
	// Points - 发放点数（> 0）
	Points int64
	// TargetNote - 目标说明（发放到站内用户的备注）
	TargetNote string
}

// StarGrantOutResult - 点数发放核销回执
type StarGrantOutResult struct {
	// GrantNo - 实际生效的发放核销单号（回显）
	GrantNo string
	// BalanceAfter - 核销后点数池余额
	BalanceAfter int64
}

// starGrantOutPayload - 发放核销响应 data（gRPC 侧 proto 仅回 balanceAfter，与 HTTP data 取交集）
type starGrantOutPayload struct {
	BalanceAfter int64 `json:"balanceAfter"`
}

// CreditGrantOut - 点数发放核销：舰长点数池扣减（发放到站内用户，im 侧入账凭证回执）。
// 幂等键 request_id = GrantNo。
func (this *StarService) CreditGrantOut(ctx context.Context, input StarGrantOutInput) (StarGrantOutResult, error) {

	if input.Points <= 0 {
		return StarGrantOutResult{}, errors.New("star: 发放点数必须大于 0")
	}
	grantNo := core.ResolveRequestID(input.GrantNo)
	body, err := json.Marshal(map[string]any{
		"grantNo": grantNo, "points": input.Points, "targetNote": input.TargetNote,
	})
	if err != nil {
		return StarGrantOutResult{}, err
	}
	data, err := this.call(ctx, "/api/v1/star/billing/grant-out", body, grantNo)
	if err != nil {
		return StarGrantOutResult{}, err
	}
	var payload starGrantOutPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return StarGrantOutResult{}, fmt.Errorf("star: grant-out 响应解析失败：%w", err)
	}
	return StarGrantOutResult{GrantNo: grantNo, BalanceAfter: payload.BalanceAfter}, nil
}

// StarPromoteOrderCreateInput - 投流单创建入参
type StarPromoteOrderCreateInput struct {
	// PromoteNo - 投流单号（幂等键，全联邦唯一由发起方保证；缺省自动生成并在结果中回显）
	PromoteNo string
	// NoteOriginInstance - 被投流笔记的源舰实例标识
	NoteOriginInstance string
	// RemoteNoteId - 源站笔记标识
	RemoteNoteId string
	// FromInstanceId - 发起舰实例标识（hold 账户归属；缺省取 Options.InstanceNo）
	FromInstanceId string
	// Points - 投入点数（> 0）
	Points int64
	// AmountFen - 折算金额（万分，汇率快照）
	AmountFen int64
	// TargetViews - 目标阅读量（> 0）
	TargetViews int64
}

// StarPromoteOrderCreateResult - 投流单创建回执
type StarPromoteOrderCreateResult struct {
	// PromoteNo - 实际生效的投流单号（回显；后续 settle/cancel/report-stats 必须使用同一单号）
	PromoteNo string
}

// PromoteOrderCreate - 投流单创建：发起舰长钱包 hold（点数 × 汇率）。
// 幂等键 request_id = PromoteNo；重复创建返回既有结果。
func (this *StarService) PromoteOrderCreate(ctx context.Context, input StarPromoteOrderCreateInput) (StarPromoteOrderCreateResult, error) {

	if input.Points <= 0 || input.TargetViews <= 0 {
		return StarPromoteOrderCreateResult{}, errors.New("star: 投入点数与目标阅读量必须大于 0")
	}
	if strings.TrimSpace(input.NoteOriginInstance) == "" || strings.TrimSpace(input.RemoteNoteId) == "" {
		return StarPromoteOrderCreateResult{}, errors.New("star: 被投流笔记定位（noteOriginInstance/remoteNoteId）不能为空")
	}
	fromInstanceId, err := this.instanceId(input.FromInstanceId)
	if err != nil {
		return StarPromoteOrderCreateResult{}, err
	}
	promoteNo := core.ResolveRequestID(input.PromoteNo)
	body, err := json.Marshal(map[string]any{
		"promoteNo": promoteNo, "noteOriginInstance": input.NoteOriginInstance,
		"remoteNoteId": input.RemoteNoteId, "fromInstanceId": fromInstanceId,
		"points": input.Points, "amountFen": input.AmountFen, "targetViews": input.TargetViews,
	})
	if err != nil {
		return StarPromoteOrderCreateResult{}, err
	}
	if _, err = this.call(ctx, "/api/v1/star/billing/order-create", body, promoteNo); err != nil {
		return StarPromoteOrderCreateResult{}, err
	}
	return StarPromoteOrderCreateResult{PromoteNo: promoteNo}, nil
}

// StarPromoteOrderSettleInput - 投流结算入参
type StarPromoteOrderSettleInput struct {
	// PromoteNo - 投流单号（必填，幂等键；不得自动生成——结算必须指向既有单）
	PromoteNo string
	// DoneViews - 实际达成阅读量（ReportPromoteStats 归集口径）
	DoneViews int64
	// TargetViews - 目标阅读量
	TargetViews int64
	// Reason - 结算原因（complete / 到期 / 人工复核等）
	Reason string
}

// StarSettleTargetLine - 分账目标舰明细行
type StarSettleTargetLine struct {
	// InstanceId - 目标舰实例标识
	InstanceId string `json:"instanceId"`
	// Fen - 分账金额（万分，按有效阅读占比瓜分）
	Fen int64 `json:"fen"`
	// Views - 该舰有效阅读量（accepted 口径）
	Views int64 `json:"views"`
}

// StarSettleBill - 分账快照（20/50/30：源舰长 20% / 目标舰群 50% / 平台 30%）
type StarSettleBill struct {
	// SettleNo - 结算单号（hub 生成，SET-{年}-%06d）
	SettleNo string `json:"settleNo"`
	// AmountFen - 原 hold 金额（万分）
	AmountFen int64 `json:"amountFen"`
	// RefundFen - 退款金额（万分，未达成部分 release）
	RefundFen int64 `json:"refundFen"`
	// SourceFen - 源舰长分账（万分，结算额 × 20%）
	SourceFen int64 `json:"sourceFen"`
	// TargetFen - 目标舰群分账合计（万分，结算额 × 50%）
	TargetFen int64 `json:"targetFen"`
	// PlatformFen - 平台分账（万分，结算额 × 30%）
	PlatformFen int64 `json:"platformFen"`
	// TargetLines - 目标舰群明细行
	TargetLines []StarSettleTargetLine `json:"targetLines"`
	// SettledAt - 结算时间（毫秒戳）
	SettledAt int64 `json:"settledAt"`
}

// PromoteOrderSettle - 投流结算：按比例 r = min(done,target)/target 结算，差额退款，
// 结算额拆账 20/50/30，返回分账快照。幂等键 request_id = PromoteNo；
// 重复结算返回既有快照（replayed，不再双花）。
func (this *StarService) PromoteOrderSettle(ctx context.Context, input StarPromoteOrderSettleInput) (StarSettleBill, error) {

	promoteNo := strings.TrimSpace(input.PromoteNo)
	if promoteNo == "" {
		return StarSettleBill{}, errors.New("star: 投流单号不能为空（结算必须指向既有单）")
	}
	body, err := json.Marshal(map[string]any{
		"promoteNo": promoteNo, "doneViews": input.DoneViews,
		"targetViews": input.TargetViews, "reason": input.Reason,
	})
	if err != nil {
		return StarSettleBill{}, err
	}
	data, err := this.call(ctx, "/api/v1/star/billing/order-settle", body, promoteNo)
	if err != nil {
		return StarSettleBill{}, err
	}
	var bill StarSettleBill
	if err = json.Unmarshal(data, &bill); err != nil {
		return StarSettleBill{}, fmt.Errorf("star: settle 响应解析失败：%w", err)
	}
	return bill, nil
}

// StarPromoteOrderCancelInput - 投流取消/退款入参
type StarPromoteOrderCancelInput struct {
	// PromoteNo - 投流单号（必填，幂等键）
	PromoteNo string
	// RefundRatio - 退款比例（万分位 0~10000，release 解冻比例）
	RefundRatio int
	// Reason - 取消原因（拒审 / 撤销 / 到期等）
	Reason string
}

// StarPromoteOrderCancelResult - 投流取消/退款回执
type StarPromoteOrderCancelResult struct {
	// PromoteNo - 投流单号（回显）
	PromoteNo string
	// RefundFen - 实际退款金额（万分）
	RefundFen int64
}

// starCancelPayload - 取消响应 data（gRPC 侧 proto 仅回 refundFen，与 HTTP data 取交集）
type starCancelPayload struct {
	RefundFen int64 `json:"refundFen"`
}

// PromoteOrderCancel - 投流取消/退款：release 解冻差额（拒审/撤销/到期按比例）。
// 幂等键 request_id = PromoteNo。
func (this *StarService) PromoteOrderCancel(ctx context.Context, input StarPromoteOrderCancelInput) (StarPromoteOrderCancelResult, error) {

	promoteNo := strings.TrimSpace(input.PromoteNo)
	if promoteNo == "" {
		return StarPromoteOrderCancelResult{}, errors.New("star: 投流单号不能为空（取消必须指向既有单）")
	}
	if input.RefundRatio < 0 || input.RefundRatio > 10000 {
		return StarPromoteOrderCancelResult{}, errors.New("star: 退款比例必须为万分位 0~10000")
	}
	body, err := json.Marshal(map[string]any{
		"promoteNo": promoteNo, "refundRatio": input.RefundRatio, "reason": input.Reason,
	})
	if err != nil {
		return StarPromoteOrderCancelResult{}, err
	}
	data, err := this.call(ctx, "/api/v1/star/billing/order-cancel", body, promoteNo)
	if err != nil {
		return StarPromoteOrderCancelResult{}, err
	}
	var payload starCancelPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return StarPromoteOrderCancelResult{}, fmt.Errorf("star: cancel 响应解析失败：%w", err)
	}
	return StarPromoteOrderCancelResult{PromoteNo: promoteNo, RefundFen: payload.RefundFen}, nil
}

// StarReportPromoteStatsInput - 投流阅读上报入参
type StarReportPromoteStatsInput struct {
	// PromoteNo - 投流单号（必填）
	PromoteNo string
	// FromInstanceId - 上报舰（目标舰）实例标识（缺省取 Options.InstanceNo）
	FromInstanceId string
	// PeriodHour - 归集周期小时起点（毫秒戳）
	PeriodHour int64
	// Views - 本周期阅读量
	Views int64
	// UniqueViewers - 本周期去重阅读人数
	UniqueViewers int64
	// Status - 口径：StarStatsAccepted（有效）/ StarStatsQuarantined（隔离，不参与分账）
	Status string
}

// ReportPromoteStats - 投流阅读上报：目标舰按 promote_no 归集有效阅读
// （promote_no + from_instance_id + period_hour 归集 upsert，天然幂等；
// 幂等键取投流单号，供服务端审计归因）。
func (this *StarService) ReportPromoteStats(ctx context.Context, input StarReportPromoteStatsInput) error {

	promoteNo := strings.TrimSpace(input.PromoteNo)
	if promoteNo == "" {
		return errors.New("star: 投流单号不能为空")
	}
	if input.PeriodHour <= 0 {
		return errors.New("star: 归集周期小时起点（periodHour 毫秒戳）必须大于 0")
	}
	if input.Status != StarStatsAccepted && input.Status != StarStatsQuarantined {
		return errors.New("star: 阅读口径必须为 accepted / quarantined")
	}
	fromInstanceId, err := this.instanceId(input.FromInstanceId)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"promoteNo": promoteNo, "fromInstanceId": fromInstanceId, "periodHour": input.PeriodHour,
		"views": input.Views, "uniqueViewers": input.UniqueViewers, "status": input.Status,
	})
	if err != nil {
		return err
	}
	_, err = this.call(ctx, "/api/v1/star/billing/report-stats", body, promoteNo)
	return err
}
