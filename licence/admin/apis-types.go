package admin

// 本文件为 API 商城（apis 域）DTO，与平台逐一对齐（口径沿用 admin-types.go 头部约定）：
//   - 输出结构对齐 licen-hub/backend/app/models/basic/apis-*.go 各模型的 json tag，
//     以及 app/service/apis/*.go 的白名单视图（商品浏览 OfferProduct/OfferPlan、操作结果 RechargeResult/BalanceState）；
//   - 输入结构对齐 licen-hub/backend/app/service/apis/*.go 的 *Params（写路径入参）与 *Query（读路径筛选）；
//   - 时间戳除特别注明外均为毫秒（平台 autoCreateTime:milli）。
//
// 金额口径：余额与订单金额单位为「分」（affects 充值/调整/退款）；metered（付费制按量）套餐单价为
// 「万分/次」（ApisPlan.Price），两者不是同一标度，展示与换算由调用方自行处理。

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
	// CachePrice - 缓存命中按量单价（万分/次，0=与正价相同）
	CachePrice int64 `json:"cachePrice"`
	// CacheQuotaRatio - 订阅内缓存命中计额比例（百分比，0=订阅内命中缓存免费不占额度）
	CacheQuotaRatio int `json:"cacheQuotaRatio"`
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
// billingMode=subscription 时 Price 为每周期价格（分），quota* 为周期内额度；
// billingMode=metered 时 Price 为单价（万分/次），quota* 强制为 0（只受并发量与 QPS 约束）。
type ApisPlan struct {
	// Id - 主键
	Id int `json:"id"`
	// PlanNo - 套餐编号（PLN-{年}-%06d）
	PlanNo string `json:"planNo"`
	// ProductId - 所属产品ID
	ProductId int `json:"productId"`
	// Name - 套餐名称（如「包月-基础版」「按量-标准价」）
	Name string `json:"name"`
	// BillingMode - 计费模式（subscription 订阅制 / metered 付费制按量）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期价格（分）/ 付费制单价（万分/次）
	Price int64 `json:"price"`
	// Period - 订阅周期（monthly/quarterly/yearly），付费制为空
	Period string `json:"period"`
	// Quota - 订阅制周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 订阅制每日调用量上限（自然日 UTC+8，0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 订阅制每周调用量上限（自然周周一起，0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 订阅制每月调用量上限（自然月，0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（两种计费模式都生效，0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
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

// ApisOfferProduct - 商品浏览的产品白名单视图（平台 service/apis.OfferProduct）
// 剥离 upstreamConfig（上游端点/参数模板/密钥引用键名）、uid 与内部字段（version/deleteAt 等）。
type ApisOfferProduct struct {
	// Id - 主键
	Id int `json:"id"`
	// ProductNo - 产品编号
	ProductNo string `json:"productNo"`
	// Capability - 能力编码
	Capability string `json:"capability"`
	// Name - 产品名称
	Name string `json:"name"`
	// Summary - 产品摘要
	Summary string `json:"summary"`
	// Description - 产品详情（富文本）
	Description string `json:"description"`
	// Status - 状态（浏览视图恒为 on_sale）
	Status string `json:"status"`
	// FreeDailyQuota - 每日免费调用次数（0=无）
	FreeDailyQuota int64 `json:"freeDailyQuota"`
	// FreeMonthlyQuota - 每月免费调用次数（0=无）
	FreeMonthlyQuota int64 `json:"freeMonthlyQuota"`
	// TrialQuota - 一次性体验额度（0=无体验）
	TrialQuota int64 `json:"trialQuota"`
	// CachePrice - 缓存命中按量单价（万分/次，0=与正价相同）
	CachePrice int64 `json:"cachePrice"`
	// CacheQuotaRatio - 订阅内缓存命中计额比例（百分比）
	CacheQuotaRatio int `json:"cacheQuotaRatio"`
	// Sort - 商城展示排序
	Sort int `json:"sort"`
	// Tags - 商城展示标签
	Tags string `json:"tags"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
}

// ApisOfferPlan - 商品浏览的套餐白名单视图（平台 service/apis.OfferPlan）
type ApisOfferPlan struct {
	// Id - 主键
	Id int `json:"id"`
	// PlanNo - 套餐编号
	PlanNo string `json:"planNo"`
	// ProductId - 所属产品ID
	ProductId int `json:"productId"`
	// Name - 套餐名称
	Name string `json:"name"`
	// BillingMode - 计费模式（subscription/metered）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期价格（分）/ 付费制单价（万分/次）
	Price int64 `json:"price"`
	// Period - 订阅周期（月/季/年）
	Period string `json:"period"`
	// Quota - 周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 每日调用量上限（0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 每周调用量上限（0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 每月调用量上限（0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
	// Status - 状态（浏览视图恒为 on_sale）
	Status string `json:"status"`
}

// ApisProductOffer - 商品浏览视图（产品 + 在售套餐；平台 service/apis.ProductOffer）
type ApisProductOffer struct {
	// Product - 产品白名单视图
	Product ApisOfferProduct `json:"product"`
	// Plans - 在售套餐白名单视图
	Plans []ApisOfferPlan `json:"plans"`
}

// ApisProductInput - 产品写路径入参（平台 service/apis.ProductParams）
// 平台 Update 为**全量替换**（服务层按入参重建整行），因此本结构不使用 omitempty：
// 未显式赋值的数字字段会按 0 落库（如 cacheQuotaRatio=0 表示订阅内命中缓存免费）。
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
	// CachePrice - 缓存命中按量单价（万分/次，0=与正价相同）
	CachePrice int64 `json:"cachePrice"`
	// CacheQuotaRatio - 订阅内缓存命中计额比例（百分比 0~100）
	CacheQuotaRatio int `json:"cacheQuotaRatio"`
	// Sort - 商城展示排序
	Sort int `json:"sort"`
	// Tags - 商城展示标签
	Tags string `json:"tags"`
	// Version - 乐观锁版本（>0 且与当前行不一致时返回 409；0=跳过冲突校验）
	Version int `json:"version"`
}

// ApisPlanInput - 套餐写路径入参（平台 service/apis.PlanParams）
// 平台 Update 为**全量替换**（服务层 buildPlan 重建整行），因此本结构不使用 omitempty。
type ApisPlanInput struct {
	// Id - 0=新增，>0=修改（修改必填）
	Id int `json:"id"`
	// ProductId - 所属产品ID（修改时不可变更）
	ProductId int `json:"productId"`
	// Name - 套餐名称（新增必填）
	Name string `json:"name"`
	// BillingMode - 计费模式（subscription 订阅制 / metered 付费制按量）
	BillingMode string `json:"billingMode"`
	// Price - 订阅制每周期价格（分）/ 付费制单价（万分/次）
	Price int64 `json:"price"`
	// Period - 订阅周期（monthly/quarterly/yearly），付费制留空
	Period string `json:"period"`
	// Quota - 周期内包含调用次数（0=不限量）
	Quota int64 `json:"quota"`
	// QuotaDaily - 每日调用量上限（0=不限）
	QuotaDaily int64 `json:"quotaDaily"`
	// QuotaWeekly - 每周调用量上限（0=不限）
	QuotaWeekly int64 `json:"quotaWeekly"`
	// QuotaMonthly - 每月调用量上限（0=不限）
	QuotaMonthly int64 `json:"quotaMonthly"`
	// ConcurrencyLimit - 并发量上限（0=跟随平台默认）
	ConcurrencyLimit int `json:"concurrencyLimit"`
	// QpsLimit - 调用速率上限（0=跟随平台默认）
	QpsLimit int `json:"qpsLimit"`
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
	// BillingMode - 计费模式（subscription/metered）
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
	// Amount - 应付金额（分）
	Amount int64 `json:"amount"`
	// Status - 状态（pending/paid/cancelled/refunded/closed）
	Status string `json:"status"`
	// PayChannel - 支付通道（balance 余额账户 / offline 线下人工确认）
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
	// PayChannel - 支付通道（balance 余额即付 / offline 线下人工确认；空=balance）
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
type ApisSubscription struct {
	// Id - 主键
	Id int `json:"id"`
	// SubNo - 订阅编号（SUB-{年}-%06d）
	SubNo string `json:"subNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// PlanId - 套餐ID
	PlanId int `json:"planId"`
	// ProductId - 产品ID（冗余便于查询）
	ProductId int `json:"productId"`
	// OrderNo - 来源订单号（续费时刷新为最新订单）
	OrderNo string `json:"orderNo"`
	// Status - 状态（active/expired/cancelled/suspended）
	Status string `json:"status"`
	// CurrentPeriodStart - 当前计费周期开始（毫秒）
	CurrentPeriodStart int64 `json:"currentPeriodStart"`
	// CurrentPeriodEnd - 当前计费周期结束（毫秒）
	CurrentPeriodEnd int64 `json:"currentPeriodEnd"`
	// AutoRenew - 到期自动续费（从余额账户扣款生成新订单）
	AutoRenew bool `json:"autoRenew"`
	// PeriodUsed - 当前周期已用量（DB 账面值，实时值以平台缓存为准）
	PeriodUsed int64 `json:"periodUsed"`
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

// ============================= 余额账户与充值 =============================

// ApisBalanceAccount - 余额账户（平台 models/basic.ApisBalanceAccount）
// 余额只通过 apis_balance_logs 流水变更；frozen 为预扣冻结金额；monthlySpendLimit 只约束新消费。
type ApisBalanceAccount struct {
	// Id - 主键
	Id int `json:"id"`
	// UserId - 归属用户ID（一人一户）
	UserId int `json:"userId"`
	// Balance - 余额（分）
	Balance int64 `json:"balance"`
	// Frozen - 预扣冻结金额（分）
	Frozen int64 `json:"frozen"`
	// MonthlySpendLimit - 月度消费限制（分，0=不限制）
	MonthlySpendLimit int64 `json:"monthlySpendLimit"`
	// Status - 状态（normal 正常 / frozen 风控冻结，冻结后禁止消费与充值，仅可退款）
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

// ApisBalanceLog - 账户流水（平台 models/basic.ApisBalanceLog，只追加）
// txType：recharge/consume/hold/release/refund/subscribe/adjust；amount 收入为正、支出为负。
type ApisBalanceLog struct {
	// Id - 主键
	Id int `json:"id"`
	// LogNo - 流水号（BTX-{年}-%06d）
	LogNo string `json:"logNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// AccountId - 余额账户ID
	AccountId int `json:"accountId"`
	// TxType - 流水类型（recharge/consume/hold/release/refund/subscribe/adjust）
	TxType string `json:"txType"`
	// Amount - 变动金额（分），收入为正、支出为负
	Amount int64 `json:"amount"`
	// BalanceAfter - 变动后余额（对账快照）
	BalanceAfter int64 `json:"balanceAfter"`
	// RequestId - 幂等键（预扣/结算/充值/调整各自唯一）
	RequestId string `json:"requestId"`
	// RefNo - 关联单号（recharge_no/order_no/bill_no/调用 requestId）
	RefNo string `json:"refNo"`
	// Remark - 备注
	Remark string `json:"remark"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// ApisRecharge - 充值单（平台 models/basic.ApisRecharge）
// 状态机：pending →（事务内写 recharge 流水入账）paid / cancelled / closed（超时未支付）。
type ApisRecharge struct {
	// Id - 主键
	Id int `json:"id"`
	// RechargeNo - 充值单号（RCG-{年}-%06d）
	RechargeNo string `json:"rechargeNo"`
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// Amount - 充值金额（分）
	Amount int64 `json:"amount"`
	// PayChannel - 支付通道（offline 线下转账人工确认 / adjust 平台赠送调整）
	PayChannel string `json:"payChannel"`
	// Status - 状态（pending/paid/cancelled/closed 超时未支付）
	Status string `json:"status"`
	// RequestId - 幂等键
	RequestId string `json:"requestId"`
	// ReviewNote - 平台确认/调整说明（adjust 通道必填，写审计）
	ReviewNote string `json:"reviewNote"`
	// PaidAt - 到账时间（毫秒，0=未到账）
	PaidAt int64 `json:"paidAt"`
	// PayTxNo - 外部流水号
	PayTxNo string `json:"payTxNo"`
	// Version - 乐观锁版本
	Version int `json:"version"`
	// CreateAt - 创建时间（毫秒）
	CreateAt int64 `json:"createAt"`
	// UpdateAt - 更新时间（毫秒）
	UpdateAt int64 `json:"updateAt"`
	// DeleteAt - 删除时间（毫秒，0=未删除）
	DeleteAt int64 `json:"deleteAt"`
}

// ApisBalanceState - 余额变更结果（平台 service/apis.BalanceState，Adjust 接口的 data）
type ApisBalanceState struct {
	// UserId - 归属用户ID
	UserId int `json:"userId"`
	// AccountId - 余额账户ID
	AccountId int `json:"accountId"`
	// Balance - 变更后余额（分）
	Balance int64 `json:"balance"`
	// Frozen - 预扣冻结金额（分）
	Frozen int64 `json:"frozen"`
	// Available - 可用余额（分，balance - frozen）
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

// ApisRechargeResult - 充值单操作结果（平台 service/apis.RechargeResult）
type ApisRechargeResult struct {
	// Recharge - 充值单快照
	Recharge ApisRecharge `json:"recharge"`
	// Replayed - 是否命中幂等（本次未产生新的资金变动）
	Replayed bool `json:"replayed"`
	// Balance - 入账后的账户余额（分；创建申请时为 0）
	Balance int64 `json:"balance"`
	// LogNo - 入账流水号（创建申请时为空）
	LogNo string `json:"logNo"`
}

// ApisBalanceSpentResult - 本月已消费（平台 HTTP {monthSpent} / gRPC apisSpentEnvelope）
type ApisBalanceSpentResult struct {
	// MonthSpent - 本月已消费（分；权威口径为余额流水）
	MonthSpent int64 `json:"monthSpent"`
}

// ApisRechargeInput - 充值申请入参（平台 service/apis.RechargeParams）
type ApisRechargeInput struct {
	// UserId - 归属用户（member 侧须为本人或 0=取登录态，平台侧可代用户发起）
	UserId int `json:"userId,omitempty"`
	// Amount - 充值金额（分，下限为平台配置，默认 10 元）
	Amount int64 `json:"amount,omitempty"`
	// PayChannel - 支付通道（当前仅 offline；空=offline）
	PayChannel string `json:"payChannel,omitempty"`
	// RequestId - 幂等键（必填）
	RequestId string `json:"requestId,omitempty"`
}

// ApisRechargeConfirmInput - 平台确认充值入账入参（平台 service/apis.RechargeConfirmParams）
type ApisRechargeConfirmInput struct {
	// Id - 充值单ID（与 RechargeNo 二选一）
	Id int `json:"id,omitempty"`
	// RechargeNo - 充值单号（与 Id 二选一）
	RechargeNo string `json:"rechargeNo,omitempty"`
	// ReviewNote - 平台确认说明（必填，写审计与充值单）
	ReviewNote string `json:"reviewNote,omitempty"`
	// PayTxNo - 外部流水号（可选，如银行转账凭证号）
	PayTxNo string `json:"payTxNo,omitempty"`
}

// ApisRechargeQuery - 充值单查询（平台 service/apis.RechargeQuery；member 侧强制本人）
type ApisRechargeQuery struct {
	// Page - 页码（默认 1）
	Page int `json:"page,omitempty"`
	// Limit - 每页数量（默认 10）
	Limit int `json:"limit,omitempty"`
	// Order - 排序
	Order string `json:"order,omitempty"`
	// UserId - 归属用户（平台侧按读范围筛选；member 须为本人或 0）
	UserId int `json:"userId,omitempty"`
	// Status - 状态（pending/paid/cancelled/closed）
	Status string `json:"status,omitempty"`
	// PayChannel - 支付通道（offline/adjust）
	PayChannel string `json:"payChannel,omitempty"`
	// CreateAt - 创建时间区间（毫秒 [起,止]）
	CreateAt []int64 `json:"createAt,omitempty"`
	// No - 单号（充值单号精确匹配）
	No string `json:"no,omitempty"`
}

// ApisBalanceLogQuery - 余额流水查询（平台 service/apis.BalanceLogQuery）
type ApisBalanceLogQuery struct {
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

// ApisAccountQuery - 余额账户查询（平台 service/apis.AccountQuery，平台侧账户运营）
type ApisAccountQuery struct {
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

// ApisBalanceAdjustInput - 平台调整余额入参（平台 service/apis.AdjustParams）
// Amount 可正可负：正数赠送、负数扣减（平台按原始参数读取，不丢负号）；Reason 必填并写审计。
type ApisBalanceAdjustInput struct {
	// UserId - 目标用户（平台侧必须显式指定且落在读写范围内）
	UserId int `json:"userId,omitempty"`
	// Amount - 调整金额（分），正数赠送、负数扣减
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
	// TotalAmount - 账期总金额（分）
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
	// SubId - 命中的订阅ID（纯按量调用为 0）
	SubId int `json:"subId"`
	// Quantity - 本次计量数（通常 1，批量接口=实际条数）
	Quantity int64 `json:"quantity"`
	// ChargeMode - 计费模式（free_quota/trial/subscription_quota/metered_balance）
	ChargeMode string `json:"chargeMode"`
	// CacheHit - 是否命中平台缓存（命中按 cachePrice/cacheQuotaRatio 折扣计费）
	CacheHit bool `json:"cacheHit"`
	// Amount - 本次扣费（分；免费、体验与订阅额度内为 0）
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
	// TotalAmount - 日总扣费（分）
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
	// Amount - 区间总金额（分）
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
	// Amount - 当日金额（分）
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
	// Amount - 分组金额（分）
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

// apisBillExportEnvelope - 账单导出信封（平台 HTTP apisBillExportEnvelope / gRPC apisBillExportEnvelope）
// xlsx 为二进制，两协议均以 base64 文本承载 content；typed 方法内部解码后返回原始字节（见 ApisResource.ExportBills）。
type apisBillExportEnvelope struct {
	// FileName - 导出文件名（如 bills-202608.xlsx）
	FileName string `json:"fileName"`
	// Content - xlsx 内容（base64 文本）
	Content string `json:"content"`
}
