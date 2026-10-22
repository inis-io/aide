package types

import (
	"encoding/json"
	"fmt"
)

// ============================= 交易 - 开始 =============================

// 平台侧默认值 - 服务端 SmartVerify 绑定参数时按 default 标签注入，并由服务端对注入后的
// 结构体验签；SDK 必须在签名前补齐同样的默认值，否则必然验签失败。
const (
	// defaultCurrency - 默认货币（下单、转账共用）
	defaultCurrency = "CNY"
	// defaultExpired - 默认订单有效期（秒）
	defaultExpired = int64(900)
	// defaultScene - 默认收银场景
	defaultScene = "alipay"
	// defaultWay - 默认收银支付方式
	defaultWay = "auto"
)

// Trade - 订单（仅商户可见字段；平台内部字段如 ip/agent/costRate/funding 不下发）
type Trade struct {
	// TradeNo - 平台订单号
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户订单号
	OutTradeNo string `json:"outTradeNo"`
	// GatewayTradeNo - 支付网关订单号
	GatewayTradeNo string `json:"gatewayTradeNo"`
	// MchId - 商户号
	MchId string `json:"mchId"`
	// Scene - 支付场景（alipay、wechat、paypal）
	Scene string `json:"scene"`
	// Way - 支付方式（scan、url 等）
	Way string `json:"way"`
	// Amount - 金额（元）
	Amount float64 `json:"amount"`
	// Currency - 货币
	Currency string `json:"currency"`
	// Status - 订单状态（created/pending/paid/fail/closed/refunding/refunded/refund-part）
	Status string `json:"status"`
	// Url - 支付链接（下单时为空，GetCheckout 生成后才存在）
	Url string `json:"url"`
	// Describe - 订单描述
	Describe string `json:"describe"`
	// Remark - 备注
	Remark string `json:"remark"`
	// NotifyUrl - 支付结果异步通知地址
	NotifyUrl string `json:"notifyUrl"`
	// ReturnUrl - 支付完成跳转地址
	ReturnUrl string `json:"returnUrl"`
	// ExpireAt - 过期时间（毫秒时间戳，0 表示永不过期）
	ExpireAt int64 `json:"expireAt"`
	// CreateAt - 创建时间（毫秒时间戳）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒时间戳）
	UpdateAt int64 `json:"updateAt"`
}

// Gateway - 支付网关（商户可见字段子集）
type Gateway struct {
	// Id - 网关ID
	Id int32 `json:"id"`
	// Name - 网关名称
	Name string `json:"name"`
	// Scene - 网关平台（alipay、wechat、paypal）
	Scene string `json:"scene"`
	// Status - 网关状态
	Status string `json:"status"`
	// IsSandbox - 是否沙箱网关（0 生产、1 沙箱）
	IsSandbox int32 `json:"isSandbox"`
	// CostRate - 网关成本费率（如 0.0020 表示 0.20%）
	CostRate float64 `json:"costRate"`
}

// MerchantBrief - 商户展示信息（限制字段）
type MerchantBrief struct {
	// Name - 商户名称
	Name string `json:"name"`
	// MchId - 商户号
	MchId string `json:"mchId"`
	// Avatar - 头像
	Avatar string `json:"avatar"`
	// Describe - 描述
	Describe string `json:"describe"`
	// Status - 商户状态
	Status string `json:"status"`
}

// TradeCreateRequest - 统一下单请求
// 平台按「两阶段」语义：本请求只落库返回订单行，不含支付链接；取链接须再调 GetCheckout。
type TradeCreateRequest struct {
	// Amount - 金额（元，≥0.01；按 float64 语义参与签名，12.30 签成 "12.3"）
	Amount float64 `json:"amount"`
	// ChannelCode - 渠道编码（必填，取 ListChannels 返回的 code）
	ChannelCode string `json:"channelCode"`
	// Currency - 货币（默认 CNY，参与签名）
	Currency string `json:"currency"`
	// Describe - 订单描述（≤256）
	Describe string `json:"describe"`
	// Expired - 订单有效期（秒，默认 900，参与签名）
	Expired int64 `json:"expired"`
	// OutTradeNo - 商户订单号（≤32，商户内唯一，平台以此做幂等）
	OutTradeNo string `json:"outTradeNo"`
	// NotifyUrl - 支付结果异步通知地址（公网可达 HTTPS）
	NotifyUrl string `json:"notifyUrl"`
	// ReturnUrl - 支付完成跳转地址
	ReturnUrl string `json:"returnUrl"`
	// Remark - 备注（≤256）
	Remark string `json:"remark"`
}

// Normalize - 注入服务端默认值（签名前必须执行）
func (this *TradeCreateRequest) Normalize() {

	if this.Currency == "" {
		this.Currency = defaultCurrency
	}
	if this.Expired <= 0 {
		this.Expired = defaultExpired
	}
}

// TradeCreateResult - 统一下单结果
type TradeCreateResult struct {
	// Trade - 订单行（状态 created/pending，无支付链接）
	Trade Trade `json:"trade"`
	// Gateways - 渠道下可用网关
	Gateways []Gateway `json:"gateways"`
	// Merchant - 商户展示信息
	Merchant MerchantBrief `json:"merchant"`
}

// UnmarshalData - HTTP 链路适配：下单接口的信封 data 以 row 承载订单行
func (this *TradeCreateResult) UnmarshalData(raw []byte) error {

	var wire struct {
		Row      Trade         `json:"row"`
		Gateways []Gateway     `json:"gateways"`
		Merchant MerchantBrief `json:"merchant"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return fmt.Errorf("%w：下单响应解析失败（%v）", ErrTransport, err)
	}

	this.Trade = wire.Row
	this.Gateways = wire.Gateways
	this.Merchant = wire.Merchant

	return nil
}

// TradeQueryRequest - 商户查单请求
// TradeNo 与 OutTradeNo 至少提供一个；两者同时提供时平台以 TradeNo 为准。
type TradeQueryRequest struct {
	// TradeNo - 平台订单号
	TradeNo string `json:"tradeNo"`
	// OutTradeNo - 商户订单号
	OutTradeNo string `json:"outTradeNo"`
}

// CheckoutRequest - 收银请求（取支付链接）
type CheckoutRequest struct {
	// TradeNo - 平台订单号（必填）
	TradeNo string `json:"tradeNo"`
	// Scene - 支付场景（默认 alipay）
	Scene string `json:"scene"`
	// Way - 支付方式（默认 auto）
	Way string `json:"way"`
	// GenUrl - 是否生成支付链接（平台默认 true；本方法语义即「取支付链接」，SDK 恒按 true 上送与签名）
	GenUrl bool `json:"genUrl"`
}

// Normalize - 注入服务端默认值（签名前必须执行）
func (this *CheckoutRequest) Normalize() {

	if this.Scene == "" {
		this.Scene = defaultScene
	}
	if this.Way == "" {
		this.Way = defaultWay
	}
	// 收银方法语义即取支付链接，与平台默认值 true 一致；
	// bool 零值与显式 false 不可区分，故统一置 true 上送与签名。
	this.GenUrl = true
}

// CheckoutResult - 收银结果（支付链接在 Trade.Url）
type CheckoutResult struct {
	// Trade - 订单行（支付链接在 url 字段）
	Trade Trade `json:"trade"`
	// Gateways - 渠道下可用网关
	Gateways []Gateway `json:"gateways"`
	// Merchant - 商户展示信息
	Merchant MerchantBrief `json:"merchant"`
}

// ============================= 交易 - 结束 =============================
