package apis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// mailSendPath - 邮件代发路径（protocol-matrix.yaml 的 MailSend 行；HTTP ↔ gRPC MailSend 一一对应）。
// typed 方法内部不做额外逻辑：能力/动作由服务端固定（mail-send/send），计量数由服务端按归一后的
// 收件人数确定（每封计 1），客户端只上送收件人/主题/正文/正文类型。
const mailSendPath = "/api/v1/apis/mail-send/send"

// MailSendInput - 邮件代发入参（能力契约：上送条数 1..20（剔除空白后、去重前，服务端据此声明计量数）、
// 主题 1..200 字符、正文 1..128 KiB）。计量数由服务端算：声明量 = 上送条数、实际计量 = 去重后投递人数。
type MailSendInput struct {
	// To - 收件人邮箱地址（上送条数 1..20，按剔除空白/空串后、去重前计；显示名会被服务端剥离，仅取裸地址并去重）
	To []string
	// Subject - 邮件主题（服务端去首尾空白后须为 1..200 字符）
	Subject string
	// Content - 邮件正文原文（原样投递，不做模板替换、不自动追加验证码）
	Content string
	// HTML - 正文类型：false（缺省）= text/plain; charset=UTF-8，true = text/html; charset=UTF-8
	HTML bool
}

// MailSendResult - 邮件代发结果：字段与 proto apis.v1.MailSendResult 及 licen-hub mailsend
// Provider 出参逐字对齐（HTTP data.result / gRPC MailSendResponse.result 同形）。
type MailSendResult struct {
	// Sent - 实际投递人数（全部成功才返回，等于去重后的收件人数，也是本次的实际计量数）
	Sent int `json:"sent"`
	// Recipients - 归一化收件人裸地址（去重、保持上送顺序）
	Recipients []string `json:"recipients"`
	// Subject - 主题（服务端去空白后的实际投递值）
	Subject string `json:"subject"`
}

// mailSendBody - MailSend 请求体（与平台 HTTP 侧 typed 入口同源：能力/动作/计量数不进请求体）
type mailSendBody struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Content string   `json:"content"`
	Html    bool     `json:"html"`
}

// MailSend - 邮件代发（typed 便捷方法，按实际收件人数计量：每封计 1）。
//
// 交付语义（服务端口径）：全部收件人投递成功才算交付；任一失败返回 *apis.Error（UPSTREAM_ERROR，
// 预扣全额释放、不收费）。入参非法（地址不可解析、数量超 20、主题/正文超长）返回
// *apis.Error（INVALID_ARGUMENT），不触上游、不计量、不收费。
/**
 * @param ctx context.Context - 调用上下文
 * @param input MailSendInput - 收件人/主题/正文/正文类型
 * @param requestId ...string - 可选调用级幂等键（缺省自动生成 req_ 前缀键；重试必须复用同一值）
 * @return MailSendResult - 投递结果（sent/recipients/subject）
 * @return Receipt - 计量回执（Quantity = 实际收件人数）
 * @return error - 业务拒绝为 *apis.Error（如 ErrorCodeInvalidArgument / ErrorCodeUpstreamError）
 * @example：
 * 	sent, receipt, err := lic.Apis.MailSend(ctx, apis.MailSendInput{
 * 		To: []string{"user@example.com"}, Subject: "对账单", Content: "<p>正文</p>", HTML: true,
 * 	})
 * 	if err == nil {
 * 		fmt.Println(sent.Sent, receipt.Quantity)
 * 	}
 */
func (this *Client) MailSend(ctx context.Context, input MailSendInput, requestId ...string) (MailSendResult, Receipt, error) {

	body, err := json.Marshal(mailSendBody{
		To: input.To, Subject: input.Subject, Content: input.Content, Html: input.HTML,
	})
	if err != nil {
		return MailSendResult{}, Receipt{}, err
	}
	data, err := this.do(ctx, http.MethodPost, mailSendPath, body, resolveRequestID(requestId...))
	if err != nil {
		return MailSendResult{}, Receipt{}, err
	}
	var payload struct {
		Result  MailSendResult `json:"result"`
		Receipt Receipt        `json:"receipt"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		return MailSendResult{}, Receipt{}, fmt.Errorf("apis: 邮件代发响应解析失败：%w", err)
	}
	return payload.Result, payload.Receipt, nil
}
