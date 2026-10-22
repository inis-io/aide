// Package notify - 平台支付结果异步通知（Webhook）的报文解析与本地验签。
//
// 平台以 POST {NotifyUrl} 投递 JSON 报文（Content-Type: application/json，User-Agent:
// inis-pay-notify/1.0），报文字段见 types.NotifyPayload；sign 与请求签名同一算法，
// 由 sign 包负责复算。验签不可跳过：ParseNotify 失败即返回错误。
package notify

import (
	"encoding/json"
	"fmt"

	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"
)

// ============================= Webhook - 开始 =============================

// ParseNotify - 解析并验签 Webhook 报文
// 验签不通过一律返回错误，无跳过开关；商户必须以 TradeNo 做幂等（通知会多轮重试，
// 且平台不携带时间戳，无防重放能力）。
/**
 * @param body []byte - 原始请求体（不要先做任何改写）
 * @param secret string - 商户密钥
 * @return *types.NotifyPayload - 已验签的报文
 * @return error - ErrInvalidRequest（非法 JSON）/ ErrInvalidSign（缺 sign 或验签失败）
 * @example：
 * 	payload, err := notify.ParseNotify(body, secret)
 * 	if err != nil { http.Error(w, "fail", http.StatusBadRequest); return }
 * 	if err := save(payload); err != nil { http.Error(w, "fail", http.StatusInternalServerError); return }
 * 	_, _ = w.Write([]byte("success"))   // 必须纯文本 success，平台才判定通知成功
 */
func ParseNotify(body []byte, secret string) (*types.NotifyPayload, error) {

	payload := &types.NotifyPayload{}
	if err := json.Unmarshal(body, payload); err != nil {
		return nil, fmt.Errorf("%w：通知报文不是合法 JSON（%v）", types.ErrInvalidRequest, err)
	}
	if payload.Sign == "" {
		return nil, fmt.Errorf("%w：通知报文缺少 sign 字段", types.ErrInvalidSign)
	}

	params := make(map[string]any)
	if err := json.Unmarshal(body, &params); err != nil {
		return nil, fmt.Errorf("%w：通知报文不是合法 JSON（%v）", types.ErrInvalidRequest, err)
	}

	if !sign.New().Verify(params, secret, payload.Sign) {
		return nil, fmt.Errorf("%w：通知报文验签失败（请核对商户密钥与原始报文）", types.ErrInvalidSign)
	}

	return payload, nil
}

// VerifyNotify - 仅验签（需要自行解析报文时使用）
/**
 * @param body []byte - 原始请求体
 * @param secret string - 商户密钥
 * @return bool - 是否通过（报文非法或验签失败均为 false）
 */
func VerifyNotify(body []byte, secret string) bool {

	_, err := ParseNotify(body, secret)

	return err == nil
}

// ============================= Webhook - 结束 =============================
