package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/inis-io/aide/licence/apis/core"
)

// 本文件承载星链中枢仲裁归集（StarArbitration）的 SDK 公开方法：ReportOutcome。
// 举报路由本身舰际直达（im 侧已通），本接口是处置结果与统计的中枢归集
// （源舰处置后主动上报 + 举报舰可申诉补录）；身份绑定由服务端强制
// （调用舰必须是举报发起舰或源舰）。

// StarReportOutcomeInput - 仲裁结果上报入参
type StarReportOutcomeInput struct {
	// ReportNo - 举报单号（幂等键；缺省自动生成并作为返回值回显）
	ReportNo string
	// FromInstanceId - 举报发起舰实例标识
	FromInstanceId string
	// TargetInstanceId - 被举报舰（= 源舰）实例标识
	TargetInstanceId string
	// RemoteNoteId - 源站笔记标识
	RemoteNoteId string
	// Reason - 举报原因
	Reason string
	// Outcome - 处置结果：StarOutcomePending / StarOutcomeUpheld（成立）/ StarOutcomeRejected（驳回）
	Outcome string
	// OutcomeAt - 处置时间（毫秒戳；0 = 服务端取当前时间）
	OutcomeAt int64
}

// ReportOutcome - 仲裁结果上报。幂等键 request_id = ReportNo；
// upheld 首次落定触发一次 trust 调整（重复上报幂等返回既有结果）。
/**
 * @param ctx context.Context - 调用上下文
 * @param input StarReportOutcomeInput - 举报单号/双方实例/笔记/处置结果
 * @return string - 实际生效的举报单号（显式传入的原样回显，缺省的为自动生成值）
 * @example：
 * 	reportNo, err := client.Star.ReportOutcome(ctx, licence.StarReportOutcomeInput{
 * 		ReportNo: "RPT-2026-000001", FromInstanceId: "INS-A", TargetInstanceId: "INS-B",
 * 		RemoteNoteId: "note-42", Reason: "spam", Outcome: licence.StarOutcomeUpheld,
 * 	})
 */
func (this *StarService) ReportOutcome(ctx context.Context, input StarReportOutcomeInput) (string, error) {

	if strings.TrimSpace(input.FromInstanceId) == "" || strings.TrimSpace(input.TargetInstanceId) == "" ||
		strings.TrimSpace(input.RemoteNoteId) == "" {
		return "", errors.New("star: 举报双方实例与源站笔记标识不能为空")
	}
	switch input.Outcome {
	case StarOutcomePending, StarOutcomeUpheld, StarOutcomeRejected:
	default:
		return "", errors.New("star: 处置结果必须为 pending / upheld / rejected")
	}
	reportNo := core.ResolveRequestID(input.ReportNo)
	body, err := json.Marshal(map[string]any{
		"reportNo": reportNo, "fromInstanceId": input.FromInstanceId,
		"targetInstanceId": input.TargetInstanceId, "remoteNoteId": input.RemoteNoteId,
		"reason": input.Reason, "outcome": input.Outcome, "outcomeAt": input.OutcomeAt,
	})
	if err != nil {
		return "", err
	}
	if _, err = this.call(ctx, "/api/v1/star/arbitration/report-outcome", body, reportNo); err != nil {
		return "", err
	}
	return reportNo, nil
}
