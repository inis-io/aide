package openpay

import (
	"encoding/json"
	"fmt"
)

// ============================= Webhook - 开始 =============================

// NotifyPayload - 支付结果异步通知报文
// 平台以 POST {NotifyUrl} 投递（Content-Type: application/json），报文字段如下，
// sign 与请求签名同一算法（剔除 sign 与空值 → 字典序拼串 → &secret=<商户密钥> → MD5 大写）。
type NotifyPayload struct {
	// TradeNo - 平台订单号（商户侧幂等去重键）
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户订单号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 支付网关订单号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// MchId - 商户号
	MchId string `json:"mchId"`
	// Amount - 金额（元）
	Amount float64 `json:"amount"`
	// Currency - 货币
	Currency string `json:"currency"`
	// Scene - 支付场景
	Scene string `json:"scene"`
	// Way - 支付方式
	Way string `json:"way"`
	// Status - 订单状态（paid 表示支付成功）
	Status string `json:"status"`
	// CreateAt - 订单创建时间（毫秒时间戳）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 订单更新时间（毫秒时间戳）
	UpdateAt int64 `json:"updateAt"`
	// Sign - 报文签名（参与验签后即可丢弃）
	Sign string `json:"sign"`
}

// ParseNotify - 解析并验签 Webhook 报文
// 验签不通过一律返回错误，无跳过开关；商户必须以 TradeNo 做幂等（通知会多轮重试，
// 且平台不携带时间戳，无防重放能力）。
/**
 * @param body []byte - 原始请求体（不要先做任何改写）
 * @param secret string - 商户密钥
 * @return *NotifyPayload - 已验签的报文
 * @return error - ErrInvalidRequest（非法 JSON）/ ErrInvalidSign（缺 sign 或验签失败）
 * @example：
 * 	payload, err := openpay.ParseNotify(body, secret)
 * 	if err != nil { http.Error(w, "fail", http.StatusBadRequest); return }
 * 	if err := save(payload); err != nil { http.Error(w, "fail", http.StatusInternalServerError); return }
 * 	_, _ = w.Write([]byte("success"))   // 必须纯文本 success，平台才判定通知成功
 */
func ParseNotify(body []byte, secret string) (*NotifyPayload, error) {

	payload := &NotifyPayload{}
	if err := json.Unmarshal(body, payload); err != nil {
		return nil, fmt.Errorf("%w：通知报文不是合法 JSON（%v）", ErrInvalidRequest, err)
	}
	if payload.Sign == "" {
		return nil, fmt.Errorf("%w：通知报文缺少 sign 字段", ErrInvalidSign)
	}

	params := make(map[string]any)
	if err := json.Unmarshal(body, &params); err != nil {
		return nil, fmt.Errorf("%w：通知报文不是合法 JSON（%v）", ErrInvalidRequest, err)
	}

	signer := &md5Signer{}
	if !signer.Verify(params, secret, payload.Sign) {
		return nil, fmt.Errorf("%w：通知报文验签失败（请核对商户密钥与原始报文）", ErrInvalidSign)
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
