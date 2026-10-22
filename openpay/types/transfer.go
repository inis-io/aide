package types

import (
	"encoding/json"
	"fmt"
)

// ============================= 转账 - 开始 =============================

// defaultPayeeType - 默认收款方类型（服务端 default 标签，参与签名）
const defaultPayeeType = "ALIPAY_LOGON_ID"

// Trans - 转账单（仅商户可见字段）
type Trans struct {
	// TradeNo - 平台转账单号
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户转账单号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 支付网关转账单号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// MchId - 商户号
	MchId string `json:"mchId"`
	// Scene - 支付场景（alipay、wechat）
	Scene string `json:"scene"`
	// Way - 转账方式（direct、card）
	Way string `json:"way"`
	// Amount - 金额（元）
	Amount float64 `json:"amount"`
	// Currency - 货币
	Currency string `json:"currency"`
	// Status - 转账状态（pending/processing/success/fail/closed）
	Status string `json:"status"`
	// PayeeAccount - 收款账号
	PayeeAccount string `json:"payeeAccount"`
	// PayeeName - 收款人姓名
	PayeeName string `json:"payeeName"`
	// PayeeType - 收款方类型（如 ALIPAY_LOGON_ID）
	PayeeType string `json:"payeeType"`
	// Describe - 转账描述
	Describe string `json:"describe"`
	// Remark - 备注
	Remark string `json:"remark"`
	// ExpireAt - 过期时间（毫秒时间戳）
	ExpireAt int64 `json:"expireAt"`
	// CreateAt - 创建时间（毫秒时间戳）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒时间戳）
	UpdateAt int64 `json:"updateAt"`
}

// TransferRequest - 转账发起请求
type TransferRequest struct {
	// ChannelCode - 渠道编码（必填，取 ListChannels 返回的 code）
	ChannelCode string `json:"channelCode"`
	// Scene - 支付场景（必填，如 alipay、wechat）
	Scene string `json:"scene"`
	// Amount - 金额（元，≥0.01；按 float64 语义参与签名）
	Amount float64 `json:"amount"`
	// Currency - 货币（默认 CNY，参与签名）
	Currency string `json:"currency"`
	// PayeeAccount - 收款账号（必填）
	PayeeAccount string `json:"payeeAccount"`
	// PayeeName - 收款人姓名（支付宝必填；微信单笔≥2000 元必填）
	PayeeName string `json:"payeeName"`
	// PayeeType - 收款方类型（默认 ALIPAY_LOGON_ID，参与签名）
	PayeeType string `json:"payeeType"`
	// Describe - 转账描述（≤256）
	Describe string `json:"describe"`
	// OutTradeNo - 商户转账单号（≤32，商户内唯一，平台以此做幂等）
	OutTradeNo string `json:"outTradeNo"`
	// NotifyUrl - 异步通知地址（平台当前不消费，预留）
	NotifyUrl string `json:"notifyUrl"`
	// Remark - 备注（≤256）
	Remark string `json:"remark"`
	// TransferSceneName - 转账场景名称（需与支付宝企业账户配置一致）
	TransferSceneName string `json:"transferSceneName"`
	// TransferSceneReportInfo - 转账场景报备说明（如「8月家政服务报酬」）
	TransferSceneReportInfo string `json:"transferSceneReportInfo"`
}

// Normalize - 注入服务端默认值（签名前必须执行）
func (this *TransferRequest) Normalize() {

	if this.Currency == "" {
		this.Currency = defaultCurrency
	}
	if this.PayeeType == "" {
		this.PayeeType = defaultPayeeType
	}
}

// TransferResult - 转账发起结果
type TransferResult struct {
	// Trans - 转账单行
	Trans Trans `json:"trans"`
	// Merchant - 商户展示信息
	Merchant MerchantBrief `json:"merchant"`
}

// UnmarshalData - HTTP 链路适配：转账接口的信封 data 以 row 承载转账单行
func (this *TransferResult) UnmarshalData(raw []byte) error {

	var wire struct {
		Row      Trans         `json:"row"`
		Merchant MerchantBrief `json:"merchant"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("%w：转账响应解析失败（%v）", ErrTransport, err)
	}

	this.Trans = wire.Row
	this.Merchant = wire.Merchant

	return nil
}

// TransferQueryRequest - 转账查询请求
// TradeNo 与 OutTradeNo 至少提供一个。
type TransferQueryRequest struct {
	// TradeNo - 平台转账单号
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户转账单号
	OutTradeNo string `json:"outTradeNo"`
}

// ============================= 转账 - 结束 =============================
