package admin

// 本文件为 API 商城（apis 域）DTO，与平台逐一对齐（口径沿用 admin-types.go 头部约定）：
//   - 输出结构对齐 licen-hub/backend/app/models/basic/apis-*.go 各模型的 json tag，
//     以及 app/service/apis/*.go 的白名单视图（商品浏览套餐中心 OfferPlan/OfferPlanItem、
//     操作结果 BalanceState、能力上游状态 UpstreamStatus 族）；
//   - 输入结构对齐 licen-hub/backend/app/service/apis/*.go 的 *Params（写路径入参）与 *Query（读路径筛选）；
//   - 时间戳除特别注明外均为毫秒（平台 autoCreateTime:milli）。
//
// 金额口径：钱包余额与订单金额单位为「万分」（affects 调整/退款）；按量兜底单价（meteredPrice）为
// 「万分/次」，落在产品层（ApisProduct.MeteredPrice，商城浏览经 ApisOfferPlanItem.MeteredPrice
// 透出），两者同标度，展示与换算由调用方自行处理。
//
// 套餐多产品化（设计 02 §2.2）：套餐不再绑定单一产品，订阅额度/限额逐产品落在 ApisPlanItem
// 明细行（四维 quota + 并发/QPS）；按量计费已自套餐明细下沉产品层（ApisProduct 的
// meteredPrice / meteredConcurrencyLimit / meteredQpsLimit），套餐 billingMode 只剩 subscription。
// 订阅的周期用量按订阅 × 产品分维记账（平台 apis_subscription_usages），订阅主表不再携带产品与已用量字段。

// ============================= 商城目录（产品 / 套餐） =============================

// ApisProduct - API 产品（平台 models/basic.ApisProduct）
type ApisProduct struct {
	// Id - 主键
	Id int `json:"id"`
	// ProductNo - 产品编号（APD-{年}-%06d）
	ProductNo string `json:"productNo"`
	// Capability - 能力编码（ip-locate / mail-send，关联 Provider 注册表）
	Capability string `json:"capability"`
	// Name - 产品名称（同能力下唯一）
	Name string `json:"name"`
	// Summary - 产品摘要
	Summary string `json:"summary"`
	// Description - 产品详情（富文本）
	Description string `json:"description"`
	// UpstreamConfig - 上游非敏感配置 JSON（端点/超时/参数模板；密钥只存 env 配置键名引用）
	UpstreamConfig string `json:"upstreamConfig"`
	// Status - 状态（draft 草稿 / on_sale 上架 / off_sale 下架 / archived 归档）
	Status string `json:"status"`
	// FreeDailyQuota - 每日免费调用次数（0=无），自然日重置
	FreeDailyQuota int64 `json:"freeDailyQuota"`
	// FreeMonthlyQuota - 每月免费调用次数（0=无），自然月重置
	FreeMonthlyQuota int64 `json:"freeMonthlyQuota"`
	// TrialQuota - 一次性体验额度（0=无体验），用完即止不重置
	TrialQuota int64 `json:"trialQuota"`
	// CacheDiscount - 缓存命中折扣率（百分比 0~100，100=无折扣，0=缓存命中免费：订阅内累积折算额度，按量按折扣价结算）
	CacheDiscount int `json:"cacheDiscount"`
	// MeterUnit - 计量单位（call=次/token=token，默认 call；llm-gateway 固定 token，
	// 产品免费三层、套餐明细四维 quota、流水 Quantity 对该产品一律按本单位解释）
	MeterUnit string `json:"meterUnit"`
	// MeteredPrice - 按量兜底单价（万分/次，0=不提供按量计费；订阅超量/无订阅时按钱包按量扣费）
	MeteredPrice int64 `json:"meteredPrice"`
	// MeteredConcurrencyLimit - 按量并发上限（0=平台默认）
	MeteredConcurrencyLimit int64 `json:"meteredConcurrencyLimit"`
	// MeteredQpsLimit - 按量 QPS 上限（0=平台默认）
	MeteredQpsLimit int64 `json:"meteredQpsLimit"`
	// Sort - 商城展示排序
	Sort int `json:"sort"`
	// Tags - 商城展示标签
	Tags string `json:"tags"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// Uid - 创建人用户ID
	Uid int `json:"uid"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisPlan - API 套餐 / 价格方案（平台 models/basic.ApisPlan）
// 套餐为「产品明细的载体」：billingMode 只剩 subscription（订阅制），Price 为每周期总价（万分），
// 计费与限额逐产品落在明细行（ApisPlanItem）；按量计费已下沉产品层（ApisProduct 的 metered 三字段）。
type ApisPlan struct {
	// Id - 主键
	Id int `json:"id"`
	// PlanNo - 套餐编号（PLN-{年}-%06d）
	PlanNo string `json:"planNo"`
	// Name - 套餐名称（如「包月-基础版」）
	Name string `json:"name"`
	// BillingMode - 计费模式（恒为 subscription 订阅制；按量计费见产品 meteredPrice）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期总价（万分）
	Price int64 `json:"price"`
	// Period - 订阅周期（monthly/quarterly/yearly）
	Period string `json:"period"`
	// Status - 状态（on_sale 上架 / off_sale 下架 / archived 归档）
	Status string `json:"status"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisPlanItem - 套餐 × 产品明细（平台 models/basic.ApisPlanItem）：
// 四维 quota 为订阅制额度（0=不限）；concurrencyLimit / qpsLimit 为订阅明细限额（0=跟随平台默认）；
// 按量单价已下沉 apis_products.metered_price（price 列已随迁移删除）。
type ApisPlanItem struct {
	// Id - 主键
	Id int `json:"id"`
	// PlanId - 套餐ID
	PlanId int `json:"planId"`
	// ProductId - 产品ID
	ProductId int `json:"productId"`
	// Quota - 订阅制周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 订阅制每日调用量上限（自然日 UTC+8，0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 订阅制每周调用量上限（自然周周一起，0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 订阅制每月调用量上限（自然月，0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（订阅明细限额，0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（订阅明细限额，0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisPlanView - 平台管理视图的套餐（含产品明细；平台 service/apis.PlanView）
type ApisPlanView struct {
	// ApisPlan - 套餐主表字段（内嵌平铺）
	ApisPlan
	// Items - 产品明细（逐产品订阅额度/限额）
	Items []ApisPlanItem `json:"items"`
}

// ApisOfferPlan - 商品浏览的套餐白名单视图（平台 service/apis.OfferPlan）：
// 商城以套餐为中心（浏览的两条路由返回套餐分页/详情），套餐卡片携带全部产品明细；
// 白名单字段——剥离产品 upstreamConfig/uid/version 等内部字段。
type ApisOfferPlan struct {
	// Id - 主键
	Id int `json:"id"`
	// PlanNo - 套餐编号
	PlanNo string `json:"planNo"`
	// Name - 套餐名称
	Name string `json:"name"`
	// BillingMode - 计费模式（恒为 subscription 订阅制）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期总价（万分）
	Price int64 `json:"price"`
	// Period - 订阅周期（月/季/年）
	Period string `json:"period"`
	// Status - 状态（浏览视图恒为 on_sale）
	Status string `json:"status"`
	// Items - 产品明细（逐产品订阅额度/限额 + 产品摘要与按量兜底单价）
	Items []ApisOfferPlanItem `json:"items"`
}

// ApisOfferPlanItem - 商品浏览的套餐明细白名单视图（平台 service/apis.OfferPlanItem）：
// 产品摘要只带展示与定价所需信息 + 该产品在套餐内的订阅额度/限额；
// meteredPrice 为产品按量兜底单价（订阅超量/无订阅按钱包按量扣费价，0=不提供按量计费）。
type ApisOfferPlanItem struct {
	// ProductId - 产品ID
	ProductId int `json:"productId"`
	// ProductNo - 产品编号
	ProductNo string `json:"productNo"`
	// Capability - 能力编码
	Capability string `json:"capability"`
	// ProductName - 产品名称
	ProductName string `json:"productName"`
	// Summary - 产品摘要
	Summary string `json:"summary"`
	// FreeDailyQuota - 每日免费调用次数（0=无）
	FreeDailyQuota int64 `json:"freeDailyQuota"`
	// FreeMonthlyQuota - 每月免费调用次数（0=无）
	FreeMonthlyQuota int64 `json:"freeMonthlyQuota"`
	// TrialQuota - 一次性体验额度（0=无体验）
	TrialQuota int64 `json:"trialQuota"`
	// CacheDiscount - 缓存命中折扣率（百分比 0~100，100=无折扣，0=缓存命中免费）
	CacheDiscount int `json:"cacheDiscount"`
	// MeteredPrice - 产品按量兜底单价（万分/次，0=不提供按量计费；订阅超量/无订阅按钱包按量扣费）
	MeteredPrice int64 `json:"meteredPrice"`
	// Quota - 订阅制周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 订阅制每日调用量上限（0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 订阅制每周调用量上限（0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 订阅制每月调用量上限（0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
}

// ApisProductInput - 产品写路径入参（平台 service/apis.ProductParams）
// 平台 Update 为**全量替换**（服务层按入参重建整行），因此本结构不使用 omitempty：
// 未显式赋值的数字字段会按 0 落库（如 cacheDiscount=0 表示缓存命中免费、
// meteredPrice=0 表示不提供按量计费、meteredConcurrencyLimit/meteredQpsLimit=0 表示跟随平台默认）。
type ApisProductInput struct {
	// Id - 0=新增，>0=修改（修改必填）
	Id int `json:"id"`
	// Capability - 能力编码（ip-locate / mail-send …；修改时不可变更）
	Capability string `json:"capability"`
	// Name - 产品名称（新增必填，同能力下唯一）
	Name string `json:"name"`
	// Summary - 产品摘要
	Summary string `json:"summary"`
	// Description - 产品详情（富文本）
	Description string `json:"description"`
	// UpstreamConfig - 上游非敏感配置 JSON（端点/超时/参数模板；密钥只存 env 配置键名引用）
	UpstreamConfig string `json:"upstreamConfig"`
	// Status - 状态（draft/on_sale/off_sale/archived；空=保持原状态）
	Status string `json:"status"`
	// FreeDailyQuota - 每日免费调用次数（0=无）
	FreeDailyQuota int64 `json:"freeDailyQuota"`
	// FreeMonthlyQuota - 每月免费调用次数（0=无）
	FreeMonthlyQuota int64 `json:"freeMonthlyQuota"`
	// TrialQuota - 一次性体验额度（0=无体验）
	TrialQuota int64 `json:"trialQuota"`
	// CacheDiscount - 缓存命中折扣率（百分比 0~100，100=无折扣，0=缓存命中免费）
	CacheDiscount int `json:"cacheDiscount"`
	// MeteredPrice - 按量兜底单价（万分/次，0=不提供按量计费；订阅超量/无订阅时按钱包按量扣费）
	MeteredPrice int64 `json:"meteredPrice"`
	// MeteredConcurrencyLimit - 按量并发上限（0=平台默认）
	MeteredConcurrencyLimit int64 `json:"meteredConcurrencyLimit"`
	// MeteredQpsLimit - 按量 QPS 上限（0=平台默认）
	MeteredQpsLimit int64 `json:"meteredQpsLimit"`
	// Sort - 商城展示排序
	Sort int `json:"sort"`
	// Tags - 商城展示标签
	Tags string `json:"tags"`
	// Version - 乐观锁版本（>0 且与当前行不一致时返回 409；0=跳过冲突校验）
	Version int `json:"version"`
}

// ApisPlanItemInput - 套餐产品明细入参（平台 service/apis.PlanItemParams）；
// 平台明细随套餐整体提交、**全量替换**，本结构不使用 omitempty：
// 四维 quota 为订阅制额度（0=不限），concurrencyLimit / qpsLimit 为订阅明细限额（0=跟随平台默认）。
type ApisPlanItemInput struct {
	// ProductId - 明细产品（必填，同套餐内不重复）
	ProductId int `json:"productId"`
	// Quota - 订阅制周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 订阅制每日调用量上限（0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 订阅制每周调用量上限（0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 订阅制每月调用量上限（0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（订阅明细限额，0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（订阅明细限额，0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
}

// ApisPlanInput - 套餐写路径入参（平台 service/apis.PlanParams）
// 平台 Update 为**全量替换**（套餐主表整行 + 明细整体替换），因此本结构不使用 omitempty。
type ApisPlanInput struct {
	// Id - 0=新增，>0=修改（修改必填）
	Id int `json:"id"`
	// Name - 套餐名称（新增必填）
	Name string `json:"name"`
	// BillingMode - 计费模式（平台只接受 subscription 订阅制，留空按 subscription 处理；按量计费已下沉产品层）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期总价（万分）
	Price int64 `json:"price"`
	// Period - 订阅周期（monthly/quarterly/yearly）
	Period string `json:"period"`
	// Items - 产品明细（至少一条，同产品不重复）
	Items []ApisPlanItemInput `json:"items"`
	// Status - 状态（on_sale/off_sale/archived）
	Status string `json:"status"`
	// Version - 乐观锁版本（>0 且与当前行不一致时返回 409；0=跳过冲突校验）
	Version int `json:"version"`
}

// ApisProductQuery - 产品查询（平台 service/apis.ProductQuery；平台管理列表与商品浏览共用）
type ApisProductQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序（如 "sort asc, id desc"）
	Order string `json:"order,omitempty"`
	// Capability - 能力编码
	Capability string `json:"capability,omitempty"`
	// Status - 状态（draft/on_sale/off_sale/archived）
	Status string `json:"status,omitempty"`
	// Keyword - 名称/摘要模糊匹配
	Keyword string `json:"keyword,omitempty"`
}

// ApisPlanQuery - 套餐查询（平台 service/apis.PlanQuery）
type ApisPlanQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// ProductId - 所属产品ID
	ProductId int `json:"productId,omitempty"`
	// BillingMode - 计费模式（平台套餐只剩 subscription 订阅制）
	BillingMode string `json:"billingMode,omitempty"`
	// Status - 状态（on_sale/off_sale/archived）
	Status string `json:"status,omitempty"`
}

// ============================= 订单与退款 =============================

// ApisOrder - 商城订单（平台 models/basic.ApisOrder）
// 状态机：pending →（余额支付/线下确认）paid →（退款审批）refunded；pending →（用户取消）cancelled；
// pending →（超时）closed。orderType=refund 的退款单通过 refundOrderNo 指向原订单。
type ApisOrder struct {
	// Id - 主键
	Id int `json:"id"`
	// OrderNo - 订单编号（APO-{年}-%06d）
	OrderNo string `json:"orderNo"`
	// UserId - 购买人用户ID
	UserId int `json:"userId"`
	// OrderType - 订单类型（subscription 购买/续费订阅，refund 退款单）
	OrderType string `json:"orderType"`
	// PlanId - 订阅类订单关联套餐ID
	PlanId int `json:"planId"`
	// Amount - 应付金额（万分）
	Amount int64 `json:"amount"`
	// Status - 状态（pending/paid/cancelled/refunded/closed）
	Status string `json:"status"`
	// PayChannel - 支付通道（balance 钱包账户 / offline 线下人工确认）
	PayChannel string `json:"payChannel"`
	// PaidAt - 支付时间（毫秒，0=未支付）
	PaidAt int64 `json:"paidAt"`
	// PayTxNo - 支付流水号
	PayTxNo string `json:"payTxNo"`
	// RequestId - 幂等键（客户端下单时生成，重复提交返回原单）
	RequestId string `json:"requestId"`
	// Remark - 用户备注
	Remark string `json:"remark"`
	// ReviewNote - 平台处理意见（线下确认/退款审批必填）
	ReviewNote string `json:"reviewNote"`
	// RefundOrderNo - 退款单指向的原订单号
	RefundOrderNo string `json:"refundOrderNo"`
	// Detail - 退款单负向调整快照 JSON（订阅类订单留空）
	Detail string `json:"detail"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisOrderInput - 下单入参（平台 service/apis.OrderParams）
type ApisOrderInput struct {
	// UserId - 归属用户（member 侧须为本人或 0=取登录态，平台侧可代下单）
	UserId int `json:"userId,omitempty"`
	// PlanId - 购买的套餐（必填）
	PlanId int `json:"planId,omitempty"`
	// PayChannel - 支付通道（balance 钱包余额即付 / offline 线下人工确认；空=balance）
	PayChannel string `json:"payChannel,omitempty"`
	// RequestId - 幂等键（必填；重复提交返回原单）
	RequestId string `json:"requestId,omitempty"`
	// Remark - 用户备注
	Remark string `json:"remark,omitempty"`
	// AutoRenew - 自动续费偏好（*bool：nil=不设置，false=显式关闭；仅在新建订阅时生效）
	AutoRenew *bool `json:"autoRenew,omitempty"`
}

// ApisOrderQuery - 订单查询（平台 service/apis.OrderQuery；member 侧强制本人）
type ApisOrderQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// Status - 状态（pending/paid/cancelled/refunded/closed）
	Status string `json:"status,omitempty"`
	// OrderType - 订单类型（subscription/refund）
	OrderType string `json:"orderType,omitempty"`
	// PayChannel - 支付通道（balance/offline）
	PayChannel string `json:"payChannel,omitempty"`
	// PlanId - 套餐ID
	PlanId int `json:"planId,omitempty"`
	// No - 单号（订单号精确匹配）
	No string `json:"no,omitempty"`
	// CreateAt - 创建时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// ApisOrderTarget - 订单目标入参（平台 service/apis.OrderPayParams：id 与单号二选一）
type ApisOrderTarget struct {
	// Id - 订单ID（与 OrderNo 二选一）
	Id int `json:"id,omitempty"`
	// OrderNo - 订单号（与 Id 二选一）
	OrderNo string `json:"orderNo,omitempty"`
}

// ApisOrderCancelInput - 取消/关闭订单入参（平台 service/apis.OrderCancelParams）
type ApisOrderCancelInput struct {
	// Id - 订单ID（与 OrderNo 二选一）
	Id int `json:"id,omitempty"`
	// OrderNo - 订单号（与 Id 二选一）
	OrderNo string `json:"orderNo,omitempty"`
	// Reason - 取消/关闭原因
	Reason string `json:"reason,omitempty"`
}

// ApisOrderConfirmInput - 平台确认线下收款入参（平台 service/apis.OrderConfirmParams）
type ApisOrderConfirmInput struct {
	// Id - 订单ID（与 OrderNo 二选一）
	Id int `json:"id,omitempty"`
	// OrderNo - 订单号（与 Id 二选一）
	OrderNo string `json:"orderNo,omitempty"`
	// ReviewNote - 平台处理意见（必填，落 review_note 并写审计）
	ReviewNote string `json:"reviewNote,omitempty"`
	// PayTxNo - 外部收款流水号（可选）
	PayTxNo string `json:"payTxNo,omitempty"`
}

// ApisRefundApplyInput - 退款申请入参（平台 service/apis.RefundApplyParams）
type ApisRefundApplyInput struct {
	// Id - 原订单ID（与 OrderNo 二选一）
	Id int `json:"id,omitempty"`
	// OrderNo - 原订单号（与 Id 二选一）
	OrderNo string `json:"orderNo,omitempty"`
	// RequestId - 退款单幂等键（必填；重复提交返回原退款单）
	RequestId string `json:"requestId,omitempty"`
	// Reason - 退款原因（必填）
	Reason string `json:"reason,omitempty"`
}

// ApisRefundReviewInput - 退款审批入参（平台 service/apis.RefundReviewParams）
// Approve 不使用 omitempty：false（驳回）必须显式上送。
type ApisRefundReviewInput struct {
	// Id - 退款单ID（与 OrderNo 二选一）
	Id int `json:"id,omitempty"`
	// OrderNo - 退款单号（与 Id 二选一）
	OrderNo string `json:"orderNo,omitempty"`
	// Approve - 审批结论（true 通过并落地退款 + 终止订阅；false 驳回，原单与余额不动）
	Approve bool `json:"approve"`
	// ReviewNote - 平台处理意见（必填）
	ReviewNote string `json:"reviewNote,omitempty"`
}

// ============================= 订阅 =============================

// ApisSubscription - 订阅实例（平台 models/basic.ApisSubscription）
// 状态机：active →（到期未续）expired；active →（退订/退款审批通过）cancelled。
// 套餐多产品化后订阅只挂套餐（产品集合由套餐明细决定），周期用量按订阅 × 产品
// 分维记账（平台 apis_subscription_usages），主表不再携带产品与已用量字段。
type ApisSubscription struct {
	// Id - 主键
	Id int `json:"id"`
	// SubNo - 订阅编号（SUB-{年}-%06d）
	SubNo string `json:"subNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// PlanId - 套餐ID
	PlanId int `json:"planId"`
	// OrderNo - 来源订单号（续费时刷新为最新订单）
	OrderNo string `json:"orderNo"`
	// Status - 状态（active/expired/cancelled/suspended）
	Status string `json:"status"`
	// CurrentPeriodStart - 当前计费周期开始（毫秒）
	CurrentPeriodStart int64 `json:"currentPeriodStart"`
	// CurrentPeriodEnd - 当前计费周期结束（毫秒）
	CurrentPeriodEnd int64 `json:"currentPeriodEnd"`
	// AutoRenew - 到期自动续费（从钱包账户扣款生成新订单）
	AutoRenew bool `json:"autoRenew"`
	// CancelledAt - 退订时间（毫秒，0=未退订）
	CancelledAt int64 `json:"cancelledAt"`
	// CancelReason - 退订原因
	CancelReason string `json:"cancelReason"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisSubscriptionQuery - 订阅查询（平台 service/apis.SubscriptionQuery；member 侧强制本人）
type ApisSubscriptionQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// Status - 状态（active/expired/cancelled/suspended）
	Status string `json:"status,omitempty"`
	// ProductId - 产品ID
	ProductId int `json:"productId,omitempty"`
	// PlanId - 套餐ID
	PlanId int `json:"planId,omitempty"`
	// SubNo - 订阅编号（精确匹配）
	SubNo string `json:"subNo,omitempty"`
	// CreateAt - 创建时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// ============================= 平台钱包 =============================
//
// 平台级钱包（一人一户），API 商城为首个消费方：钱包余额只通过钱包流水变更，apis 商城的
// 按量扣费/订阅扣款都记在同一钱包上；后续业务域消费同一钱包时复用本组 DTO 与路由。
// 充值申请/充值审核已整体下线（平台 wallet_recharges 表删除），余额只经管理员人工调账
// adjust 变更（txType=adjust，operator_id 溯源操作人）。

// WalletAccount - 平台钱包账户（平台 models/basic.WalletAccount）
// 余额只通过钱包流水变更；frozen 为预扣冻结金额；monthlySpendLimit 只约束新消费。
type WalletAccount struct {
	// Id - 主键
	Id int `json:"id"`
	// UserId - 归属用户ID（一人一户）
	UserId int `json:"userId"`
	// Balance - 余额（万分）
	Balance int64 `json:"balance"`
	// Frozen - 预扣冻结金额（万分）
	Frozen int64 `json:"frozen"`
	// MonthlySpendLimit - 月度消费限制（万分，0=不限制）
	MonthlySpendLimit int64 `json:"monthlySpendLimit"`
	// Status - 状态（normal 正常 / frozen 风控冻结，冻结后禁止消费，仅可退款）
	Status string `json:"status"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// WalletLog - 钱包流水（平台 models/basic.WalletLog，只追加）
// txType：recharge/consume/hold/release/refund/subscribe/adjust；amount 收入为正、支出为负。
type WalletLog struct {
	// Id - 主键
	Id int `json:"id"`
	// LogNo - 流水号（BTX-{年}-%06d）
	LogNo string `json:"logNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// AccountId - 钱包账户ID
	AccountId int `json:"accountId"`
	// TxType - 流水类型（recharge/consume/hold/release/refund/subscribe/adjust）
	TxType string `json:"txType"`
	// Amount - 变动金额（万分），收入为正、支出为负
	Amount int64 `json:"amount"`
	// BalanceAfter - 变动后余额（对账快照）
	BalanceAfter int64 `json:"balanceAfter"`
	// RequestId - 幂等键（预扣/结算/充值/调整各自唯一）
	RequestId string `json:"requestId"`
	// RefNo - 关联单号（recharge_no/order_no/bill_no/调用 requestId）
	RefNo string `json:"refNo"`
	// Remark - 备注
	Remark string `json:"remark"`
	// OperatorId - 操作人用户ID（平台侧操作人；用户自助产生的流水为 0）
	OperatorId int `json:"operatorId"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// WalletState - 钱包变更结果（平台 service/apis.BalanceState，Adjust 接口的 data）
type WalletState struct {
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// AccountId - 余额账户ID
	AccountId int `json:"accountId"`
	// Balance - 变更后余额（万分）
	Balance int64 `json:"balance"`
	// Frozen - 预扣冻结金额（万分）
	Frozen int64 `json:"frozen"`
	// Available - 可用余额（万分，balance - frozen）
	Available int64 `json:"available"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// RequestId - 幂等键
	RequestId string `json:"requestId"`
	// LogNo - 本次流水号（命中幂等时为空）
	LogNo string `json:"logNo"`
	// Replayed - 是否命中幂等（本次未产生新的资金变动）
	Replayed bool `json:"replayed"`
}

// WalletSpentResult - 本月已消费（平台 HTTP {monthSpent} / gRPC apisSpentEnvelope）
type WalletSpentResult struct {
	// MonthSpent - 本月已消费（万分；权威口径为钱包流水）
	MonthSpent int64 `json:"monthSpent"`
}

// WalletLogQuery - 钱包流水查询（平台 service/apis.BalanceLogQuery）
type WalletLogQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// TxType - 流水类型（IN，多值；序列化为 txType[]=v 重复键）
	TxType []string `json:"txType,omitempty"`
	// RefNo - 关联单号（精确匹配）
	RefNo string `json:"refNo,omitempty"`
	// CreateAt - 创建时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// WalletAccountQuery - 钱包账户查询（平台 service/apis.AccountQuery，平台侧账户运营）
type WalletAccountQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户
	UserId int `json:"userId,omitempty"`
	// Status - 账户状态（normal/frozen）
	Status string `json:"status,omitempty"`
}

// WalletAdjustInput - 平台调整钱包余额入参（平台 service/apis.AdjustParams）
// Amount 可正可负：正数赠送、负数扣减（平台按原始参数读取，不丢负号）；Reason 必填并写审计。
type WalletAdjustInput struct {
	// UserId - 目标用户（平台侧必须显式指定且落在读写范围内）
	UserId int `json:"userId,omitempty"`
	// Amount - 调整金额（万分），正数赠送、负数扣减
	Amount int64 `json:"amount,omitempty"`
	// RequestId - 幂等键
	RequestId string `json:"requestId,omitempty"`
	// Reason - 调整说明（必填）
	Reason string `json:"reason,omitempty"`
}

// ============================= 账单与用量 =============================

// ApisBill - 账单（平台 models/basic.ApisBill，只追加）
// billType：subscription 订阅周期账单 / metered 按量结算账单。
type ApisBill struct {
	// Id - 主键
	Id int `json:"id"`
	// BillNo - 账单编号（BILL-{年}-%06d）
	BillNo string `json:"billNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// BillType - 账单类型（subscription/metered）
	BillType string `json:"billType"`
	// PeriodStart - 账期开始（毫秒）
	PeriodStart int64 `json:"periodStart"`
	// PeriodEnd - 账期结束（毫秒）
	PeriodEnd int64 `json:"periodEnd"`
	// SubId - 关联订阅ID
	SubId int `json:"subId"`
	// OrderNo - 关联订单号
	OrderNo string `json:"orderNo"`
	// IdemKey - 出账幂等键（sub:{subId}:{orderNo} / metered:{userId}:{YYYY-MM}）
	IdemKey string `json:"idemKey"`
	// TotalQuantity - 账期总调用量
	TotalQuantity int64 `json:"totalQuantity"`
	// TotalAmount - 账期总金额（万分）
	TotalAmount int64 `json:"totalAmount"`
	// Detail - 明细快照 JSON（[{capability,charge_mode,cache_hit,quantity,unit_price,amount}]），生成后不可变
	Detail string `json:"detail"`
	// Status - 状态（settled 已结清 / pending 待支付 / overdue 逾期未付）
	Status string `json:"status"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
}

// ApisBillQuery - 账单查询（平台 service/apis.BillQuery；导出接口按同口径忽略分页）
type ApisBillQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// BillType - 账单类型（subscription/metered）
	BillType string `json:"billType,omitempty"`
	// Status - 状态（settled/pending/overdue）
	Status string `json:"status,omitempty"`
	// No - 单号（账单号精确匹配）
	No string `json:"no,omitempty"`
	// CreateAt - 创建时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// ApisUsageRecord - 调用流水（平台 models/basic.ApisUsageRecord，只追加）
// 数据由运行面计量管道写入（taskx 批量落库），管理面只读。
type ApisUsageRecord struct {
	// Id - 主键
	Id int `json:"id"`
	// RequestId - 调用级幂等键（拆分计量派生 :a/:b 后缀）
	RequestId string `json:"requestId"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// Capability - 能力编码
	Capability string `json:"capability"`
	// ProductId - 产品ID
	ProductId int `json:"productId"`
	// ActivationNo - 激活记录编号（排障定位、分实例统计）
	ActivationNo string `json:"activationNo"`
	// KeyId - sk-key 凭证归因（apis_keys.id；sk-key 凭证调用 >0，许可证凭证 = 0）
	KeyId int `json:"keyId"`
	// SubId - 命中的订阅ID（纯按量调用为 0）
	SubId int `json:"subId"`
	// Quantity - 本次计量数（通常 1，批量接口=实际条数）
	Quantity int64 `json:"quantity"`
	// ChargeMode - 计费模式（free_quota/trial/subscription_quota/metered_balance）
	ChargeMode string `json:"chargeMode"`
	// CacheHit - 是否命中平台缓存（命中按 cacheDiscount 折扣计费）
	CacheHit bool `json:"cacheHit"`
	// Amount - 本次扣费（万分；免费、体验与订阅额度内为 0）
	Amount int64 `json:"amount"`
	// Result - 结果（success/provider_error/rejected/settle_error）
	Result string `json:"result"`
	// UpstreamMs - 上游耗时（毫秒）
	UpstreamMs int `json:"upstreamMs"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ApisUsageRecordQuery - 调用流水查询（平台 service/apis.UsageRecordQuery）
type ApisUsageRecordQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// Capability - 能力编码
	Capability string `json:"capability,omitempty"`
	// ProductId - 产品ID
	ProductId int `json:"productId,omitempty"`
	// ChargeMode - 计费模式（free_quota/trial/subscription_quota/metered_balance）
	ChargeMode string `json:"chargeMode,omitempty"`
	// Result - 调用结果（success/provider_error/rejected/settle_error）
	Result string `json:"result,omitempty"`
	// CacheHit - 缓存命中三态（"" 不限 / "true" 仅命中 / "false" 仅未命中）
	CacheHit string `json:"cacheHit,omitempty"`
	// SubId - 命中的订阅ID
	SubId int `json:"subId,omitempty"`
	// ActivationNo - 激活记录编号
	ActivationNo string `json:"activationNo,omitempty"`
	// RequestId - 调用级幂等键（精确匹配）
	RequestId string `json:"requestId,omitempty"`
	// CreateAt - 调用时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// ApisUsageDaily - 用量日聚合（平台 models/basic.ApisUsageDaily）
// 账单生成与看板只读本表，不扫流水；cacheHit 入聚合维度，折扣用量独立成行。
type ApisUsageDaily struct {
	// Id - 主键
	Id int `json:"id"`
	// StatDate - 统计日期（YYYY-MM-DD）
	StatDate string `json:"statDate"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// Capability - 能力编码
	Capability string `json:"capability"`
	// ChargeMode - 计费模式（free_quota/trial/subscription_quota/metered_balance）
	ChargeMode string `json:"chargeMode"`
	// CacheHit - 是否命中平台缓存
	CacheHit bool `json:"cacheHit"`
	// TotalQuantity - 日总量
	TotalQuantity int64 `json:"totalQuantity"`
	// TotalAmount - 日总扣费（万分）
	TotalAmount int64 `json:"totalAmount"`
	// SuccessCount - 成功次数
	SuccessCount int64 `json:"successCount"`
	// RejectCount - 被拒次数（未过闸门）
	RejectCount int64 `json:"rejectCount"`
	// ErrorCount - 上游错误次数
	ErrorCount int64 `json:"errorCount"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisUsageDailyQuery - 用量日聚合查询（平台 service/apis.UsageDailyQuery）
type ApisUsageDailyQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// Capability - 能力编码
	Capability string `json:"capability,omitempty"`
	// ChargeMode - 计费模式
	ChargeMode string `json:"chargeMode,omitempty"`
	// CacheHit - 缓存命中三态（"" 不限 / "true" / "false"）
	CacheHit string `json:"cacheHit,omitempty"`
	// StatDate - 统计日期区间（YYYY-MM-DD [起,止]，字典序即日期序）
	StatDate []string `json:"statDate,omitempty"`
}

// ApisUsageDailySummaryQuery - 用量看板汇总查询（平台 service/apis.UsageDailySummaryQuery）
type ApisUsageDailySummaryQuery struct {
	// UserId - 归属用户（平台侧按读范围筛选；member 强制本人）
	UserId int `json:"userId,omitempty"`
	// Capability - 能力编码（空=全部能力）
	Capability string `json:"capability,omitempty"`
	// StatDate - 统计日期区间（YYYY-MM-DD；缺省按最近 30 天补齐，跨度上限 92 天）
	StatDate []string `json:"statDate,omitempty"`
}

// ApisUsageDailySummaryTotals - 看板区间合计（平台 UsageDailySummary 内嵌 totals）
// 由同一批分组行推导，恒等于 ApisUsageDailySummary.Days 求和；
// cacheHitQuantity + cacheMissQuantity == quantity。
type ApisUsageDailySummaryTotals struct {
	// Quantity - 区间总计量数
	Quantity int64 `json:"quantity"`
	// Amount - 区间总金额（万分）
	Amount int64 `json:"amount"`
	// SuccessCount - 成功次数
	SuccessCount int64 `json:"successCount"`
	// RejectCount - 被拒次数
	RejectCount int64 `json:"rejectCount"`
	// ErrorCount - 上游错误次数
	ErrorCount int64 `json:"errorCount"`
	// CacheHitQuantity - 缓存命中计量数
	CacheHitQuantity int64 `json:"cacheHitQuantity"`
	// CacheMissQuantity - 缓存未命中计量数
	CacheMissQuantity int64 `json:"cacheMissQuantity"`
}

// ApisUsageDailySummaryDay - 看板稠密日序列的一行（平台 service/apis.UsageDailySummaryDay）
// 区间内每一天都有行（缺日补零），供折线与柱状按日对齐。
type ApisUsageDailySummaryDay struct {
	// StatDate - 统计日期（YYYY-MM-DD）
	StatDate string `json:"statDate"`
	// Quantity - 当日计量数
	Quantity int64 `json:"quantity"`
	// Amount - 当日金额（万分）
	Amount int64 `json:"amount"`
	// SuccessCount - 当日成功次数
	SuccessCount int64 `json:"successCount"`
	// RejectCount - 当日被拒次数
	RejectCount int64 `json:"rejectCount"`
	// ErrorCount - 当日上游错误次数
	ErrorCount int64 `json:"errorCount"`
	// CacheHitQuantity - 当日缓存命中计量数（未命中 = Quantity − 本值）
	CacheHitQuantity int64 `json:"cacheHitQuantity"`
}

// ApisUsageDailySummaryGroup - 看板分组分布（平台 service/apis.UsageDailySummaryGroup）
// 能力分布 / 计费模式分布 / 今日实时条带共用；实时条带恒为能力编码且 Amount 为 0。
type ApisUsageDailySummaryGroup struct {
	// Key - 分组键（能力编码 / 计费模式编码）
	Key string `json:"key"`
	// Quantity - 分组计量数
	Quantity int64 `json:"quantity"`
	// Amount - 分组金额（万分）
	Amount int64 `json:"amount"`
}

// ApisUsageDailySummary - 用量看板汇总（平台 service/apis.UsageDailySummary）
type ApisUsageDailySummary struct {
	// Range - 生效区间 [起,止]（经默认补齐与 92 天钳制后的实际区间）
	Range [2]string `json:"range"`
	// Totals - 区间合计
	Totals ApisUsageDailySummaryTotals `json:"totals"`
	// Days - 稠密日序列（升序，含零值日）
	Days []ApisUsageDailySummaryDay `json:"days"`
	// Capabilities - 能力分布（按计量数降序、同量按编码升序）
	Capabilities []ApisUsageDailySummaryGroup `json:"capabilities"`
	// ChargeModes - 计费模式分布（排序口径同上）
	ChargeModes []ApisUsageDailySummaryGroup `json:"chargeModes"`
	// TodayRealtime - 今日实时调用量（单用户视角才有值，平台全量视图为 null）
	TodayRealtime []ApisUsageDailySummaryGroup `json:"todayRealtime"`
}

// ============================= 能力上游管理 =============================

// ApisUpstreamKeyStatus - 上游密钥状态（平台 service/apis.UpstreamKeyStatus；面板展示用，绝不回传明文）
type ApisUpstreamKeyStatus struct {
	// Name - 键名引用（config 表 no）
	Name string `json:"name"`
	// Configured - 是否已配置
	Configured bool `json:"configured"`
	// Hint - 掩码提示（前 4 位 + ****；未配置为空串）
	Hint string `json:"hint"`
}

// ApisUpstreamDataStatus - 上游数据文件状态（平台 service/apis.UpstreamDataStatus，如 ip2region 离线库）
type ApisUpstreamDataStatus struct {
	// Source - 数据来源：embedded（内嵌默认）/ external（外部文件）/ off（显式关闭）
	Source string `json:"source"`
	// Path - 外部文件路径（source = external 时有值）
	Path string `json:"path"`
	// Size - 文件字节数（external）
	Size int64 `json:"size"`
	// ModTime - 文件更新时间（毫秒，external）
	ModTime int64 `json:"modTime"`
	// Available - 兜底源当前是否可用（加载成功）
	Available bool `json:"available"`
}

// ApisUpstreamCacheStatus - 上游缓存状态（平台 service/apis.UpstreamCacheStatus）
type ApisUpstreamCacheStatus struct {
	// CacheTtl - 平台缓存有效期（duration 字符串，如 "12h"）
	CacheTtl string `json:"cacheTtl"`
	// CacheHours - 平台缓存有效期（小时，便于数字输入）
	CacheHours int `json:"cacheHours"`
	// DbRefreshDays - 库记录回源阈值（天）
	DbRefreshDays int `json:"dbRefreshDays"`
}

// ApisUpstreamStatus - 能力上游状态视图（平台 service/apis.UpstreamStatus）
type ApisUpstreamStatus struct {
	// Capability - 能力编码
	Capability string `json:"capability"`
	// Key - 密钥状态
	Key ApisUpstreamKeyStatus `json:"key"`
	// Data - 数据文件状态（无数据文件概念的能力为 nil）
	Data *ApisUpstreamDataStatus `json:"data"`
	// Cache - 缓存状态（无缓存概念的能力为 nil）
	Cache *ApisUpstreamCacheStatus `json:"cache"`
	// Extra - 能力自定义补充（如上游端点、超时毫秒）
	Extra map[string]any `json:"extra,omitempty"`
}

// ApisUpstreamKeyInput - 上游密钥设置入参（平台 service/apis.UpstreamKeyParams；明文只存在于本次请求调用栈，绝不回显）
type ApisUpstreamKeyInput struct {
	// Capability - 能力编码
	Capability string `json:"capability"`
	// Value - 密钥明文
	Value string `json:"value"`
}

// ApisUpstreamVerifyInput - 上游配置有效性验证入参（平台 service/apis.UpstreamVerifyParams；
// 「变更先验证，有效才允许保存」，真实探测上游，不持久化）
type ApisUpstreamVerifyInput struct {
	// Capability - 能力编码
	Capability string `json:"capability"`
	// Scene - 验证场景：key（候选密钥）/ endpoint（候选接口地址）
	Scene string `json:"scene"`
	// Value - 候选值（密钥明文或接口地址）
	Value string `json:"value"`
}

// ApisUpstreamCacheInput - 上游缓存策略设置入参（平台 service/apis.UpstreamCacheParams）
type ApisUpstreamCacheInput struct {
	// Capability - 能力编码
	Capability string `json:"capability"`
	// CacheHours - 平台缓存有效期（小时）
	CacheHours int `json:"cacheHours"`
	// DbRefreshDays - 库记录回源阈值（天）
	DbRefreshDays int `json:"dbRefreshDays"`
}

// ApisUpstreamOptionsInput - 上游高级参数设置入参（平台 service/apis.UpstreamOptionsParams；
// 键值形态由能力自定义，平台能力包逐项校验）
type ApisUpstreamOptionsInput struct {
	// Capability - 能力编码
	Capability string `json:"capability"`
	// Options - 高级参数键值对
	Options map[string]any `json:"options"`
}

// apisBillExportEnvelope - 账单导出信封（平台 HTTP apisBillExportEnvelope / gRPC apisBillExportEnvelope）
// xlsx 为二进制，两协议均以 base64 文本承载 content；typed 方法内部解码后返回原始字节（见 ApisResource.ExportBills）。
type apisBillExportEnvelope struct {
	// FileName - 导出文件名（如 bills-202608.xlsx）
	FileName string `json:"fileName"`
	// Content - xlsx 内容（base64 文本）
	Content string `json:"content"`
}

// ============================= LLM 统一网关（apis-llm-channels / apis-llm-logs / apis-keys，18） =============================
//
// LLM 统一网关管理面（设计 09）：上游渠道（含模型映射明细）/ 调用观测明细 / sk- API Key 三组路由，
// 另有人工调账目标用户选项 1 条并入钱包组（wallet-accounts/user-options）。
// 输出结构对齐平台 models/basic/llm-gateway.go 各模型与 service/apis 的白名单视图
// （LlmChannelView / LlmKeyView / LlmKeyCreated），输入对齐 service/apis 的 *Params / *Query。
// 密钥红线：渠道 apiKey 任何读接口不回传（模型 json:"-" 之外的显式置空），sk-key 只存哈希、
// 明文完整 key 仅创建响应返回一次。

// ApisLlmChannel - LLM 上游渠道（平台 models/basic.LlmChannel）
// 协议族决定适配器分派；apiKey 为机器绑定加密串，禁止出现在日志与接口响应（json:"-"）。
type ApisLlmChannel struct {
	// Id - 主键
	Id int `json:"id"`
	// Name - 渠道名
	Name string `json:"name"`
	// Protocol - 南向协议族（openai/openai-responses/anthropic/google-genai/vertex/ollama/cohere）
	Protocol string `json:"protocol"`
	// BaseURL - 上游端点地址（可配代理）
	BaseURL string `json:"baseUrl"`
	// PricingUrl - 官方定价文档地址（空=抓取时按内置厂商注册表识别）
	PricingUrl string `json:"pricingUrl"`
	// ApiKey - 渠道密钥（AES 加密串，平台任何读接口不回传，JSON 恒缺省）
	ApiKey string `json:"-"`
	// Priority - 调度优先级（小者优先）
	Priority int `json:"priority"`
	// Weight - 调度权重（同优先级加权随机）
	Weight int `json:"weight"`
	// QpsLimit - QPS 上限（渠道侧自我保护，0=不限）
	QpsLimit int `json:"qpsLimit"`
	// ConcurrencyLimit - 并发上限（渠道侧自我保护，0=不限）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// Status - 状态（enabled/disabled，启用受 usage 探测闸门约束）
	Status string `json:"status"`
	// HealthStatus - 健康状态（一期手动探测：up/down，空串=未探测）
	HealthStatus string `json:"healthStatus"`
	// HealthCheckedAt - 健康检查时间（毫秒，0=未检查）
	HealthCheckedAt int64 `json:"healthCheckedAt"`
	// UsageProbe - usage 探测结果（实测字段路径摘要+样本 JSON）
	UsageProbe string `json:"usageProbe"`
	// UsageVerifiedAt - usage 探测通过时间（毫秒，0=未通过）
	UsageVerifiedAt int64 `json:"usageVerifiedAt"`
	// TimeoutMs - 上游超时（毫秒，0=平台默认，长连接预扣占用硬钳）
	TimeoutMs int `json:"timeoutMs"`
	// Remark - 备注
	Remark string `json:"remark"`
	// Uid - 创建人用户ID
	Uid int `json:"uid"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisLlmChannelModel - 渠道 × 模型映射（平台 models/basic.LlmChannelModel）
// 统一模型按名直接挂载（独立模型目录已下线）；报价为对外报价（万分/百万 token，0=未定价按 D2 拒绝调用），
// cost_* 为渠道成本价（同单位，0=未维护）；rate 为定价倍率（万分比，10500=105%，0=手工报价模式）。
type ApisLlmChannelModel struct {
	// Id - 主键
	Id int `json:"id"`
	// ChannelId - 渠道ID
	ChannelId int `json:"channelId"`
	// Model - 统一模型名（客户端按此名调用）
	Model string `json:"model"`
	// UpstreamModel - 渠道侧模型别名（空=与统一模型同名）
	UpstreamModel string `json:"upstreamModel"`
	// PriceInput - 报价输入价（非缓存命中部分，万分/百万 token，0=未定价按 D2 拒绝）
	PriceInput int64 `json:"priceInput"`
	// PriceOutput - 报价输出价（含推理 token，万分/百万 token，0=未定价按 D2 拒绝）
	PriceOutput int64 `json:"priceOutput"`
	// PriceCacheRead - 报价缓存命中输入价（万分/百万 token，0=未配置按 priceInput 计）
	PriceCacheRead int64 `json:"priceCacheRead"`
	// PriceReasoning - 报价推理 token 单独价（万分/百万 token，0=未配置并入 completion 按 priceOutput 计）
	PriceReasoning int64 `json:"priceReasoning"`
	// Rate - 定价倍率（万分比，10500=105%，0=手工报价模式；>0 时有效报价=成本价×倍率，成本价必填）
	Rate int `json:"rate"`
	// CostInput - 成本输入价（万分/百万 token，0=未维护）
	CostInput int64 `json:"costInput"`
	// CostOutput - 成本输出价（万分/百万 token，0=未维护）
	CostOutput int64 `json:"costOutput"`
	// CostCacheRead - 成本缓存命中价（万分/百万 token，0=未维护）
	CostCacheRead int64 `json:"costCacheRead"`
	// CostReasoning - 成本推理价（万分/百万 token，0=未维护）
	CostReasoning int64 `json:"costReasoning"`
	// ContextWindow - 上下文窗口（token，预扣估算与钳制依据，0=不钳制）
	ContextWindow int `json:"contextWindow"`
	// MaxOutputTokens - 最大输出 token（预扣估算与钳制依据，0=不限）
	MaxOutputTokens int `json:"maxOutputTokens"`
	// Status - 状态（enabled/disabled）
	Status string `json:"status"`
	// Priority - 调度优先级覆盖（0=跟随渠道默认）
	Priority int `json:"priority"`
	// Weight - 调度权重覆盖（0=跟随渠道默认）
	Weight int `json:"weight"`
	// Uid - 创建人用户ID
	Uid int `json:"uid"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisLlmChannelView - 渠道管理视图（平台 service/apis.LlmChannelView，含模型映射明细）
// ApiKey 已置空，绝不回传；Items 恒为数组（平台无明细时回空数组）。
type ApisLlmChannelView struct {
	// ApisLlmChannel - 渠道主表字段（内嵌平铺）
	ApisLlmChannel
	// Items - 渠道模型映射明细
	Items []ApisLlmChannelModel `json:"items"`
}

// ApisLlmModelOffer - 在售模型价格表行（平台 llmgateway.LlmModelRow 经处理器白名单投影：
// 聚合口径「启用映射 × 启用渠道」，只回已定价模型；报价为有效报价，万分/百万 token）
type ApisLlmModelOffer struct {
	// Model - 统一模型名（客户端按此名调用）
	Model string `json:"model"`
	// ContextWindow - 上下文窗口（token，0=不钳制/不限）
	ContextWindow int `json:"contextWindow"`
	// MaxOutputTokens - 最大输出 token（0=不限）
	MaxOutputTokens int `json:"maxOutputTokens"`
	// PriceInput - 有效报价输入价（万分/百万 token，倍率模式已折算）
	PriceInput int64 `json:"priceInput"`
	// PriceOutput - 有效报价输出价（万分/百万 token）
	PriceOutput int64 `json:"priceOutput"`
	// PriceCacheRead - 有效报价缓存命中输入价（0=计费时回退输入价）
	PriceCacheRead int64 `json:"priceCacheRead"`
	// PriceReasoning - 有效报价推理 token 单独价（0=并入输出价）
	PriceReasoning int64 `json:"priceReasoning"`
}

// ApisLlmChannelQuery - 渠道查询（平台 service/apis.LlmChannelQuery；平台管理列表）
type ApisLlmChannelQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// Status - enabled / disabled（空 = 不限）
	Status string `json:"status,omitempty"`
	// Protocol - 协议族过滤
	Protocol string `json:"protocol,omitempty"`
	// Keyword - 渠道名 / 端点模糊匹配
	Keyword string `json:"keyword,omitempty"`
}

// ApisLlmChannelItemInput - 渠道模型映射明细入参（平台 service/apis.LlmChannelItemParams；
// 随渠道整体提交，保存时全量替换）
type ApisLlmChannelItemInput struct {
	// Model - 统一模型名（按名直接挂载，同渠道内不重复）
	Model string `json:"model"`
	// UpstreamModel - 渠道侧模型别名（空 = 与统一模型同名）
	UpstreamModel string `json:"upstreamModel"`
	// PriceInput / PriceOutput / PriceCacheRead / PriceReasoning - 对外报价（万分/百万 token，0 = 未定价按 D2 拒绝调用）
	PriceInput     int64 `json:"priceInput"`
	PriceOutput    int64 `json:"priceOutput"`
	PriceCacheRead int64 `json:"priceCacheRead"`
	PriceReasoning int64 `json:"priceReasoning"`
	// Rate - 定价倍率（万分比，10500 = 105%，0 = 手工报价模式；> 0 时有效报价 = 成本价 × 倍率）
	Rate int `json:"rate"`
	// CostInput / CostOutput / CostCacheRead / CostReasoning - 渠道成本价（万分/百万 token，0 = 未维护）
	CostInput     int64 `json:"costInput"`
	CostOutput    int64 `json:"costOutput"`
	CostCacheRead int64 `json:"costCacheRead"`
	CostReasoning int64 `json:"costReasoning"`
	// ContextWindow / MaxOutputTokens - 预扣估算与钳制依据（0 = 不钳制/不限）
	ContextWindow   int `json:"contextWindow"`
	MaxOutputTokens int `json:"maxOutputTokens"`
	// Status - enabled / disabled（默认 enabled）
	Status string `json:"status"`
	// Priority / Weight - 模型级调度覆盖（0 = 跟随渠道默认）
	Priority int `json:"priority"`
	Weight   int `json:"weight"`
}

// ApisLlmChannelInput - 渠道保存入参（平台 service/apis.LlmChannelParams；id = 0 新增，> 0 修改）
// apiKey 留空 = 不变更（任何读接口不回传明文）；Items 为映射明细全量替换。
type ApisLlmChannelInput struct {
	// Id - 0=新增，>0=修改
	Id int `json:"id"`
	// Name - 渠道名
	Name string `json:"name"`
	// Protocol - 南向协议族
	Protocol string `json:"protocol"`
	// BaseUrl - 上游端点地址（可配代理）
	BaseUrl string `json:"baseUrl"`
	// PricingUrl - 官方定价文档地址（选填；留空时定价抓取按内置厂商注册表从 baseUrl 识别）
	PricingUrl string `json:"pricingUrl"`
	// ApiKey - 渠道密钥（留空 = 不变更；明文只存在于本次请求调用栈）
	ApiKey string `json:"apiKey"`
	// Priority / Weight - 调度：priority 小者优先，同优先级按 weight 加权随机
	Priority int `json:"priority"`
	Weight   int `json:"weight"`
	// QpsLimit / ConcurrencyLimit - 渠道侧自我保护（0 = 不限）
	QpsLimit         int `json:"qpsLimit"`
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// Status - enabled / disabled（默认 disabled；启用受 usage 探测闸门约束）
	Status string `json:"status"`
	// TimeoutMs - 上游超时（毫秒，0 = 平台默认，长连接预扣占用硬钳）
	TimeoutMs int `json:"timeoutMs"`
	// Remark - 备注
	Remark string `json:"remark"`
	// Items - 渠道模型映射明细（全量替换）
	Items []ApisLlmChannelItemInput `json:"items"`
}

// ApisLlmChannelVerifyInput - usage 探测 / 上游模型列表入参（平台 service/apis.LlmChannelVerifyParams；
// verify-usage 与 models 两路由共用；id > 0 时各字段留空取渠道已存值）
type ApisLlmChannelVerifyInput struct {
	// Id - 目标渠道（0 = 纯候选探测，结果不落库——渠道接入保存前的强制探测场景）
	Id int `json:"id"`
	// BaseUrl - 候选端点（留空取渠道已存值）
	BaseUrl string `json:"baseUrl"`
	// ApiKey - 候选密钥（留空取渠道已存密钥解密值；解密失败需显式传值）
	ApiKey string `json:"apiKey"`
	// Protocol - 协议族（留空取渠道已存值；一期仅 openai 支持探测）
	Protocol string `json:"protocol"`
	// Model - 探测模型：渠道侧模型名（留空取渠道首条启用映射的模型别名）
	Model string `json:"model"`
	// TimeoutMs - 单次调用超时（0 = 探测默认 30s）
	TimeoutMs int `json:"timeoutMs"`
}

// ApisLlmPricingScanInput - 官方定价抓取入参（平台 service/apis.LlmPricingScanParams；Id = 0 为候选渠道场景）
type ApisLlmPricingScanInput struct {
	// Id - 渠道 ID（> 0 时 pricingUrl/baseUrl 留空取渠道已存值）
	Id int `json:"id"`
	// PricingUrl - 定价文档地址（留空 = 渠道已存 / 注册表按 baseUrl 识别）
	PricingUrl string `json:"pricingUrl"`
	// BaseUrl - 渠道端点（注册表识别依据；留空取渠道已存值）
	BaseUrl string `json:"baseUrl"`
	// ExchangeRate - 外币折算人民币汇率（0 = 默认 7.2，仅页面为外币定价时传入提取提示词）
	ExchangeRate float64 `json:"exchangeRate"`
	// Refresh - 跳过缓存强制重新抓取与提取
	Refresh bool `json:"refresh"`
}

// ApisLlmPricingScanItem - 定价抓取的单模型定价（平台 service/apis.LlmPricingScanItem；
// 价格为万分/百万 token，与映射行成本价同单位，0 = 文档未提供）
type ApisLlmPricingScanItem struct {
	// Model - 模型 ID（文档中 API 调用名）
	Model string `json:"model"`
	// Input / Output / CacheRead / Reasoning - 成本价（万分/百万 token，0 = 文档未提供）
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	CacheRead int64 `json:"cacheRead"`
	Reasoning int64 `json:"reasoning"`
	// ContextWindow / MaxOutputTokens - 规格（0 = 文档未提供）
	ContextWindow   int `json:"contextWindow"`
	MaxOutputTokens int `json:"maxOutputTokens"`
}

// ApisLlmPricingScanResult - 定价抓取结果（平台 service/apis.LlmPricingScanResult；
// 结果不落库，回显由运营勾选回填；cached = 命中 24h 缓存）
type ApisLlmPricingScanResult struct {
	// Url - 实际抓取的定价页地址
	Url string `json:"url"`
	// Provider - 抓取提供方（http/jina/cdp）
	Provider string `json:"provider"`
	// Cached - 是否命中缓存（未重新抓取与提取）
	Cached bool `json:"cached"`
	// Truncated - 抓取正文是否被截断（截断可能导致尾部模型缺失，运营确认时注意）
	Truncated bool `json:"truncated"`
	// Items - 提取出的模型定价清单
	Items []ApisLlmPricingScanItem `json:"items"`
}

// ApisLlmLog - LLM 调用观测明细（平台 models/basic.LlmUsageDetail，只追加）
// 数据范围：平台观测表，member 不可见；token 分项全量留痕，cached 为 prompt 子集禁止重复相加。
type ApisLlmLog struct {
	// Id - 主键
	Id int `json:"id"`
	// RequestId - 调用级幂等键（关联 apis_usage_records.request_id）
	RequestId string `json:"requestId"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// KeyId - apis_keys.id（0=无）
	KeyId int `json:"keyId"`
	// Model - 统一模型名
	Model string `json:"model"`
	// ChannelId - 路由渠道ID（0=未路由出去）
	ChannelId int `json:"channelId"`
	// ProtocolIn - 北向协议族
	ProtocolIn string `json:"protocolIn"`
	// ProtocolOut - 南向协议族
	ProtocolOut string `json:"protocolOut"`
	// Converted - 是否跨协议转换（同协议直通=0）
	Converted bool `json:"converted"`
	// Stream - 是否 SSE 流式
	Stream bool `json:"stream"`
	// PromptTokens - 输入 token（全量，含缓存命中部分）
	PromptTokens int64 `json:"promptTokens"`
	// CompletionTokens - 输出 token（含推理 token）
	CompletionTokens int64 `json:"completionTokens"`
	// CachedTokens - 缓存命中输入 token（prompt 子集，禁止重复相加）
	CachedTokens int64 `json:"cachedTokens"`
	// ReasoningTokens - 推理 token 分项（取不到按 0 计，不可用其反推 completion）
	ReasoningTokens int64 `json:"reasoningTokens"`
	// Estimated - usage 缺失按字符估算兜底标记（fail-closed 不免费）
	Estimated bool `json:"estimated"`
	// TtftMs - 首 token 延迟（毫秒，0=未记录）
	TtftMs int64 `json:"ttftMs"`
	// DurationMs - 整次耗时（毫秒，0=未记录）
	DurationMs int64 `json:"durationMs"`
	// UpstreamStatus - 上游 HTTP 状态码（最终渠道结果）
	UpstreamStatus int `json:"upstreamStatus"`
	// ErrorType - 错误类型（failover 多段尝试以 JSON 数组留痕）
	ErrorType string `json:"errorType"`
	// Amount - 报价金额（万分，按调度序第一名渠道映射行的报价结算）
	Amount int64 `json:"amount"`
	// CostAmount - 渠道成本（万分，null=未核算，0=渠道免费）
	CostAmount *int64 `json:"costAmount"`
	// CreateAt - 调用时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ApisLlmLogQuery - 调用观测明细分页查询（平台 service/apis.LlmLogQuery；平台观测视图）
type ApisLlmLogQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// RequestId - 调用级幂等键（精确匹配，排障定位）
	RequestId string `json:"requestId,omitempty"`
	// Model - 统一模型名（精确匹配）
	Model string `json:"model,omitempty"`
	// ChannelId / KeyId - 渠道 / sk-key 过滤
	ChannelId int `json:"channelId,omitempty"`
	KeyId     int `json:"keyId,omitempty"`
	// UserId - 平台侧按归属用户筛选；member 侧只能为本人或 0
	UserId int `json:"userId,omitempty"`
	// ProtocolIn / ProtocolOut - 北向 / 南向协议族过滤
	ProtocolIn  string `json:"protocolIn,omitempty"`
	ProtocolOut string `json:"protocolOut,omitempty"`
	// Stream - 三态字符串："" 不限 / "true" 仅流式 / "false" 仅非流式
	Stream string `json:"stream,omitempty"`
	// ErrorType - 错误类型模糊匹配
	ErrorType string `json:"errorType,omitempty"`
	// CreateAt - 调用时间区间 [起, 止]（毫秒戳；只传一侧或 0 均按不限处理）
	CreateAt []int64 `json:"createAt,omitempty"`
}

// ApisKey - sk- API Key 读视图（平台 service/apis.LlmKeyView，白名单投影：key_hash 绝不外泄）
// 形态 sk- + 48 位随机 hex；库中只存 SHA-256 哈希与展示前缀。
type ApisKey struct {
	// Id - 主键
	Id int `json:"id"`
	// KeyNo - 密钥编号（KEY-{年}-%06d）
	KeyNo string `json:"keyNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// ProductId - 归属产品ID（固定 llm-gateway 产品）
	ProductId int `json:"productId"`
	// Name - 备注名
	Name string `json:"name"`
	// Prefix - 展示前缀（sk-ab12…，日志脱敏用）
	Prefix string `json:"prefix"`
	// AllowedModels - 模型白名单（空 = 不限）
	AllowedModels []string `json:"allowedModels"`
	// MonthlySpendLimit - 月度消费限额（万分，0 = 不限）
	MonthlySpendLimit int64 `json:"monthlySpendLimit"`
	// ExpiresAt - 过期时间（毫秒戳，0 = 永不过期）
	ExpiresAt int64 `json:"expiresAt"`
	// Status - 状态（active/revoked）
	Status string `json:"status"`
	// LastUsedAt - 最近使用时间（毫秒，0=未使用）
	LastUsedAt int64 `json:"lastUsedAt"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ApisKeyCreated - sk-key 创建响应（平台 service/apis.LlmKeyCreated）
// Key 为明文完整 sk-key，仅本次响应返回一次，绝不落库/再回显，调用方须立即保存。
type ApisKeyCreated struct {
	// ApisKey - 读视图字段（内嵌平铺）
	ApisKey
	// Key - 明文完整 sk-key（sk- + 48 位随机 hex）
	Key string `json:"key"`
}

// ApisKeyInput - sk-key 新建入参（平台 service/apis.LlmKeyParams；member 自助签发）
type ApisKeyInput struct {
	// Name - 备注名（如「本地开发」）
	Name string `json:"name"`
	// AllowedModels - 模型白名单（空 = 不限；创建时校验全部存在于模型目录）
	AllowedModels []string `json:"allowedModels"`
	// MonthlySpendLimit - 月度消费限额（万分，0 = 不限）
	MonthlySpendLimit int64 `json:"monthlySpendLimit"`
	// ExpiresAt - 过期时间（毫秒戳，0 = 永不过期）
	ExpiresAt int64 `json:"expiresAt"`
}

// ApisKeyQuery - sk-key 查询（平台 service/apis.LlmKeyQuery；member 强制本人；平台侧可按 userId 筛选）
type ApisKeyQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// Status - active / revoked（空 = 不限）
	Status string `json:"status,omitempty"`
	// Keyword - 备注名 / 密钥编号 / 前缀模糊匹配
	Keyword string `json:"keyword,omitempty"`
	// UserId - 平台侧按归属用户筛选；member 侧只能为本人或 0（取登录态）
	UserId int `json:"userId,omitempty"`
}

// WalletAdjustUserQuery - 人工调账目标用户选项查询（平台 service/apis.AdjustUserOptionQuery）
type WalletAdjustUserQuery struct {
	// Keyword - 关键词（按 UID 精确 / 账号 / 邮箱 / 手机号 / 昵称模糊匹配）
	Keyword string `json:"keyword"`
	// Limit - 返回条数（平台默认 20，上限 50）
	Limit int `json:"limit"`
}

// WalletAdjustUserOption - 人工调账目标用户选项（平台 service/apis.AdjustUserOption；
// 仅暴露选择器所需字段，手机号/邮箱只参与匹配、不下发）
type WalletAdjustUserOption struct {
	// Id - 用户ID
	Id int `json:"id"`
	// Account - 账号
	Account string `json:"account"`
	// Nickname - 昵称
	Nickname string `json:"nickname"`
	// Avatar - 头像
	Avatar string `json:"avatar"`
}
