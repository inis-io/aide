// Package email - API 商城「邮件代发」能力子包（线网能力编码为 mail-send/send，包名取更简短的 email）。
//
// 能力子包约定：每个能力一个子包，包内暴露 Resource（挂载在 apis.Client 的
// Email 字段上）与能力专属类型（本包为 Input/Result）；共享件（Doer/Error/Receipt/
// 信封解析/幂等键）一律来自 apis/core，不 import apis 根包（根包反向 import
// 子包做挂载，import 即成环）。新增能力的落点见 licen-hub docs/plan/apis/08 能力接入指南。
package email

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/inis-io/aide/licence/apis/core"
)

// mailSendPath - 邮件代发路径（protocol-matrix.yaml 的 MailSend 行；HTTP ↔ gRPC MailSend 一一对应）。
// typed 方法内部不做额外逻辑：能力/动作由服务端固定（mail-send/send），计量数由服务端按归一后的
// 收件人数确定（每封计 1），客户端只上送收件人/主题/正文/正文类型。
const mailSendPath = "/api/v1/apis/mail-send/send"

// Input - 邮件代发入参（能力契约：上送条数 1..20（剔除空白后、去重前，服务端据此声明计量数）、
// 主题 1..200 字符、正文 1..128 KiB）。计量数由服务端算：声明量 = 上送条数、实际计量 = 去重后投递人数。
type Input struct {
	// To - 收件人邮箱地址（上送条数 1..20，按剔除空白/空串后、去重前计；显示名会被服务端剥离，仅取裸地址并去重）
	To []string
	// Subject - 邮件主题（服务端去首尾空白后须为 1..200 字符）
	Subject string
	// Content - 邮件正文原文（原样投递，不做模板替换、不自动追加验证码）
	Content string
	// HTML - 正文类型：false（缺省）= text/plain; charset=UTF-8，true = text/html; charset=UTF-8
	HTML bool
}

// Result - 邮件代发结果：字段与 proto apis.v1.MailSendResult 及 licen-hub mailsend
// Provider 出参逐字对齐（HTTP data.result / gRPC MailSendResponse.result 同形）。
type Result struct {
	// Sent - 实际投递人数（全部成功才返回，等于去重后的收件人数，也是本次的实际计量数）
	Sent int `json:"sent"`
	// Recipients - 归一化收件人裸地址（去重、保持上送顺序）
	Recipients []string `json:"recipients"`
	// Subject - 主题（服务端去空白后的实际投递值）
	Subject string `json:"subject"`
}

// mailSendBody - Send 请求体（与平台 HTTP 侧 typed 入口同源：能力/动作/计量数不进请求体）
type mailSendBody struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Content string   `json:"content"`
	Html    bool     `json:"html"`
}

// Resource - 邮件代发能力资源（由 apis 根包 Client 挂载为 lic.Apis.MailSend）。
type Resource struct {
	// doer - 宿主注入的已认证调用能力
	doer core.Doer
}

// New - 创建邮件代发能力资源（仅装配宿主调用能力，不发起网络请求）
func New(doer core.Doer) *Resource {
	return &Resource{doer: doer}
}

// Send - 邮件代发（typed 便捷方法，按实际收件人数计量：每封计 1）。
//
// 交付语义（服务端口径）：全部收件人投递成功才算交付；任一失败返回 *apis.Error（UPSTREAM_ERROR，
// 预扣全额释放、不收费）。入参非法（地址不可解析、数量超 20、主题/正文超长）返回
// *apis.Error（INVALID_ARGUMENT），不触上游、不计量、不收费。
/**
 * @param ctx context.Context - 调用上下文
 * @param input Input - 收件人/主题/正文/正文类型
 * @param requestId ...string - 可选调用级幂等键（缺省自动生成 req_ 前缀键；重试必须复用同一值）
 * @return Result - 投递结果（sent/recipients/subject）
 * @return core.Receipt - 计量回执（Quantity = 实际收件人数）
 * @return error - 业务拒绝为 *apis.Error（如 ErrorCodeInvalidArgument / ErrorCodeUpstreamError）
 * @example：
 * 	sent, receipt, err := lic.Apis.Email.Send(ctx, email.Input{
 * 		To: []string{"user@example.com"}, Subject: "对账单", Content: "<p>正文</p>", HTML: true,
 * 	})
 * 	if err == nil {
 * 		fmt.Println(sent.Sent, receipt.Quantity)
 * 	}
 */
func (this *Resource) Send(ctx context.Context, input Input, requestId ...string) (Result, core.Receipt, error) {

	body, err := json.Marshal(mailSendBody{
		To: input.To, Subject: input.Subject, Content: input.Content, Html: input.HTML,
	})
	if err != nil {
		return Result{}, core.Receipt{}, err
	}
	raw, err := this.doer.Do(ctx, http.MethodPost, mailSendPath, body, core.ResolveRequestID(requestId...))
	if err != nil {
		return Result{}, core.Receipt{}, err
	}
	data, err := core.ParseEnvelope(raw)
	if err != nil {
		return Result{}, core.Receipt{}, err
	}
	var payload struct {
		Result  Result       `json:"result"`
		Receipt core.Receipt `json:"receipt"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		return Result{}, core.Receipt{}, fmt.Errorf("apis: 邮件代发响应解析失败：%w", err)
	}
	return payload.Result, payload.Receipt, nil
}
