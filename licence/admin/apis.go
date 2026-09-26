package admin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"

	"github.com/spf13/cast"
)

// ApisResource - API 商城资源（`/api/apis-market/*`、`/api/apis-products/*`（含能力上游管理
// upstream*）、`/api/apis-plans/*`、`/api/apis-orders/*`、`/api/apis-subscriptions/*`、
// `/api/apis-recharges/*`、`/api/apis-balances/*`、`/api/apis-bills/*`、`/api/apis-usage/*`、
// `/api/apis-monitor/*`）。
//
// 覆盖范围：licen-hub 商城管理面 **60 条受控路由**（7 个 gRPC 服务），方法名与平台登记表
// `backend/grpc/admin/v1/apis.go` 的 `ApisSpecs().Method` 逐字一致，便于协议矩阵式对账；
// 三向一致性（SDK 方法 ↔ 传输层 case ↔ 平台登记表）由 `apis_reconcile_test.go` 强制守护。
//
// 纪律要点：
//   - 归属与数据范围一律由平台服务层裁决（member 强制本人、platform 按 apis 域范围），SDK 只做 typed 传参；
//   - 幂等键（requestId）由调用方生成并显式传入，SDK 不代为生成，失败后不得跨协议自动重试；
//   - 金额单位为「分」，按量兜底单价为「万分/次」且落在产品层（ApisProduct.MeteredPrice，
//     商城浏览经 ApisOfferPlanItem.MeteredPrice 透出）；
//   - 套餐多产品化：订阅额度/限额逐产品配置（ApisPlanInput.Items 全量替换，billingMode 只剩 subscription），
//     商城浏览以套餐为中心（ApisOfferPlan.Items 携带全部产品明细）；
//   - 能力上游管理（upstream* 9 条）：密钥只进不出（响应只给掩码），数据文件上传 HTTP 走 multipart、
//     gRPC 走 JSON base64（由传输层各自适配，方法签名一致）；
//   - `Export*` 两条导出方法返回 (fileName, xlsx 原始字节, error)，base64 在方法内解码（与平台 HTTP/gRPC
//     两协议同形的 `{fileName, content}` 信封一一对应，消费方无需关心编码）。
type ApisResource struct {
	// client - 所属客户端
	client *AdminClient
}

// ============================= 超时与传输 =============================

// 商城管理面的传输差异（对调用方透明）：
//   - HTTP：`{code,msg,data}` 信封 + 平台参数中间件（GET 走 query、写路径走 JSON body）；
//   - gRPC：7 个 **proto-less** 服务（平台按 `ApisSpecs` 登记表装配 ServiceDesc，复用既有
//     licencev1.AdminRequest/AdminResponse 信封），SDK 侧经 conn.Invoke + fullMethod 常量调用，
//     无生成 stub；GET 的 query 由传输层统一折叠为 JSON 请求体（见 admin-transport-grpc.go 的 queryJSON）。
//
// full method 常量块是唯一来源：下表的服务前缀与常量即 SDK 侧全部商城 RPC 出口，
// 传输层 switch case 只引用常量名，禁止在别处再写字面量。

const (
	// apisMarketService - 商品浏览（在售目录，member 侧）
	apisMarketService = "licenhub.licence.v1.ApisMarketAdminService"
	// apisCatalogService - 目录维护（产品 + 套餐，平台侧）
	apisCatalogService = "licenhub.licence.v1.ApisCatalogAdminService"
	// apisOrderService - 订单（member 下单/支付 + 平台运营/退款审批）
	apisOrderService = "licenhub.licence.v1.ApisOrderAdminService"
	// apisSubscriptionService - 订阅（我的订阅与自助动作）
	apisSubscriptionService = "licenhub.licence.v1.ApisSubscriptionAdminService"
	// apisBalanceService - 余额账户与充值（自助 + 平台运营）
	apisBalanceService = "licenhub.licence.v1.ApisBalanceAdminService"
	// apisUsageService - 账务与用量只读视图（我的账单/用量）
	apisUsageService = "licenhub.licence.v1.ApisUsageAdminService"
	// apisMonitorService - 监控只读视图（平台全量账单/流水/用量）
	apisMonitorService = "licenhub.licence.v1.ApisMonitorAdminService"
)

const (
	// ---------- ApisMarketAdminService ----------
	apisFindMarketProductsFullMethod = "/" + apisMarketService + "/FindMarketProducts"
	apisGetMarketProductFullMethod   = "/" + apisMarketService + "/GetMarketProduct"

	// ---------- ApisCatalogAdminService ----------
	apisFindProductsFullMethod  = "/" + apisCatalogService + "/FindProducts"
	apisGetProductFullMethod    = "/" + apisCatalogService + "/GetProduct"
	apisUpdateProductFullMethod = "/" + apisCatalogService + "/UpdateProduct"

	apisGetCapabilityUpstreamFullMethod = "/" + apisCatalogService + "/GetCapabilityUpstream"
	apisSetUpstreamKeyFullMethod        = "/" + apisCatalogService + "/SetUpstreamKey"
	apisVerifyUpstreamFullMethod        = "/" + apisCatalogService + "/VerifyUpstream"
	apisClearUpstreamKeyFullMethod      = "/" + apisCatalogService + "/ClearUpstreamKey"
	apisUploadUpstreamDataFullMethod    = "/" + apisCatalogService + "/UploadUpstreamData"
	apisResetUpstreamDataFullMethod     = "/" + apisCatalogService + "/ResetUpstreamData"
	apisSetUpstreamCacheFullMethod      = "/" + apisCatalogService + "/SetUpstreamCache"
	apisClearUpstreamCacheFullMethod    = "/" + apisCatalogService + "/ClearUpstreamCache"
	apisSetUpstreamOptionsFullMethod    = "/" + apisCatalogService + "/SetUpstreamOptions"

	apisFindPlansFullMethod  = "/" + apisCatalogService + "/FindPlans"
	apisGetPlanFullMethod    = "/" + apisCatalogService + "/GetPlan"
	apisCreatePlanFullMethod = "/" + apisCatalogService + "/CreatePlan"
	apisUpdatePlanFullMethod = "/" + apisCatalogService + "/UpdatePlan"
	apisRemovePlanFullMethod = "/" + apisCatalogService + "/RemovePlan"

	// ---------- ApisOrderAdminService ----------
	apisFindOrdersFullMethod       = "/" + apisOrderService + "/FindOrders"
	apisGetOrderFullMethod         = "/" + apisOrderService + "/GetOrder"
	apisCreateOrderFullMethod      = "/" + apisOrderService + "/CreateOrder"
	apisPayOrderFullMethod         = "/" + apisOrderService + "/PayOrder"
	apisCancelOrderFullMethod      = "/" + apisOrderService + "/CancelOrder"
	apisFindManageOrdersFullMethod = "/" + apisOrderService + "/FindManageOrders"
	apisGetManageOrderFullMethod   = "/" + apisOrderService + "/GetManageOrder"
	apisConfirmOrderFullMethod     = "/" + apisOrderService + "/ConfirmOrder"
	apisCloseOrderFullMethod       = "/" + apisOrderService + "/CloseOrder"
	apisApplyRefundFullMethod      = "/" + apisOrderService + "/ApplyRefund"
	apisReviewRefundFullMethod     = "/" + apisOrderService + "/ReviewRefund"

	// ---------- ApisSubscriptionAdminService ----------
	apisFindSubscriptionsFullMethod        = "/" + apisSubscriptionService + "/FindSubscriptions"
	apisGetSubscriptionFullMethod          = "/" + apisSubscriptionService + "/GetSubscription"
	apisCancelSubscriptionFullMethod       = "/" + apisSubscriptionService + "/CancelSubscription"
	apisSetSubscriptionAutoRenewFullMethod = "/" + apisSubscriptionService + "/SetSubscriptionAutoRenew"

	// ---------- ApisBalanceAdminService ----------
	apisFindRechargesFullMethod      = "/" + apisBalanceService + "/FindRecharges"
	apisGetRechargeFullMethod        = "/" + apisBalanceService + "/GetRecharge"
	apisCreateRechargeFullMethod     = "/" + apisBalanceService + "/CreateRecharge"
	apisCancelRechargeFullMethod     = "/" + apisBalanceService + "/CancelRecharge"
	apisConfirmRechargeFullMethod    = "/" + apisBalanceService + "/ConfirmRecharge"
	apisGetBalanceFullMethod         = "/" + apisBalanceService + "/GetBalance"
	apisGetBalanceSpentFullMethod    = "/" + apisBalanceService + "/GetBalanceSpent"
	apisFindBalanceLogsFullMethod    = "/" + apisBalanceService + "/FindBalanceLogs"
	apisSetBalanceLimitFullMethod    = "/" + apisBalanceService + "/SetBalanceLimit"
	apisAdjustBalanceFullMethod      = "/" + apisBalanceService + "/AdjustBalance"
	apisSetBalanceStatusFullMethod   = "/" + apisBalanceService + "/SetBalanceStatus"
	apisFindManageBalancesFullMethod = "/" + apisBalanceService + "/FindManageBalances"
	apisGetManageBalanceFullMethod   = "/" + apisBalanceService + "/GetManageBalance"

	// ---------- ApisUsageAdminService ----------
	apisFindBillsFullMethod            = "/" + apisUsageService + "/FindBills"
	apisGetBillFullMethod              = "/" + apisUsageService + "/GetBill"
	apisExportBillsFullMethod          = "/" + apisUsageService + "/ExportBills"
	apisFindUsageRecordsFullMethod     = "/" + apisUsageService + "/FindUsageRecords"
	apisFindUsageDailyFullMethod       = "/" + apisUsageService + "/FindUsageDaily"
	apisGetUsageDailySummaryFullMethod = "/" + apisUsageService + "/GetUsageDailySummary"

	// ---------- ApisMonitorAdminService ----------
	apisFindMonitorBillsFullMethod        = "/" + apisMonitorService + "/FindMonitorBills"
	apisGetMonitorBillFullMethod          = "/" + apisMonitorService + "/GetMonitorBill"
	apisExportMonitorBillsFullMethod      = "/" + apisMonitorService + "/ExportMonitorBills"
	apisFindMonitorBalanceLogsFullMethod  = "/" + apisMonitorService + "/FindMonitorBalanceLogs"
	apisFindMonitorUsageRecordsFullMethod = "/" + apisMonitorService + "/FindMonitorUsageRecords"
	apisFindMonitorUsageDailyFullMethod   = "/" + apisMonitorService + "/FindMonitorUsageDaily"
	apisGetMonitorUsageSummaryFullMethod  = "/" + apisMonitorService + "/GetMonitorUsageSummary"
)

// ============================= 商品浏览（apis-market，2） =============================

// FindMarketProducts - 在售套餐分页（商城以套餐为中心：on_sale 套餐及其产品明细）：
// GET /api/apis-market/find
// 权限码 apis.market.read；页元素是**套餐浏览视图**（ApisOfferPlan，Items 携带逐产品
// 订阅额度/限额、产品摘要与按量兜底单价），经白名单投影（剥离 upstreamConfig/uid 等内部字段）。
func (this *ApisResource) FindMarketProducts(ctx context.Context, params *ApisPlanQuery) (*Page[ApisOfferPlan], error) {

	var result Page[ApisOfferPlan]
	if err := this.client.get(ctx, "/api/apis-market/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMarketProduct - 在售套餐详情（含产品明细；套餐未上架返回 404）：GET /api/apis-market/take?id=N
// 权限码 apis.market.read。
func (this *ApisResource) GetMarketProduct(ctx context.Context, id int) (*ApisOfferPlan, error) {

	var result ApisOfferPlan
	if err := this.client.getWithQuery(ctx, "/api/apis-market/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 目录维护（apis-products / apis-plans，17） =============================

// FindProducts - 产品分页（平台管理视图，含 draft）：GET /api/apis-products/find
// 权限码 apis.catalog.read。
func (this *ApisResource) FindProducts(ctx context.Context, params *ApisProductQuery) (*Page[ApisProduct], error) {

	var result Page[ApisProduct]
	if err := this.client.get(ctx, "/api/apis-products/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetProduct - 产品详情：GET /api/apis-products/take?id=N
// 权限码 apis.catalog.read。
func (this *ApisResource) GetProduct(ctx context.Context, id int) (*ApisProduct, error) {

	var result ApisProduct
	if err := this.client.getWithQuery(ctx, "/api/apis-products/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdateProduct - 修改产品（全量替换；version 冲突返回 409）：PUT /api/apis-products/update
// 权限码 apis.catalog.update；能力是代码定制的固定目录（平台不提供新建/删除路由），
// 能力编码不可变更，上架状态流转按平台白名单。
func (this *ApisResource) UpdateProduct(ctx context.Context, input ApisProductInput) (*ApisProduct, error) {

	var result ApisProduct
	if err := this.client.put(ctx, "/api/apis-products/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindPlans - 套餐分页（平台管理视图，含产品明细）：GET /api/apis-plans/find
// 权限码 apis.catalog.read；productId 筛选按「套餐明细包含该产品」匹配。
func (this *ApisResource) FindPlans(ctx context.Context, params *ApisPlanQuery) (*Page[ApisPlanView], error) {

	var result Page[ApisPlanView]
	if err := this.client.get(ctx, "/api/apis-plans/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPlan - 套餐详情（含产品明细）：GET /api/apis-plans/take?id=N
// 权限码 apis.catalog.read。
func (this *ApisResource) GetPlan(ctx context.Context, id int) (*ApisPlanView, error) {

	var result ApisPlanView
	if err := this.client.getWithQuery(ctx, "/api/apis-plans/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreatePlan - 新建套餐（status 缺省 off_sale；明细随套餐同事务写入）：POST /api/apis-plans/create
// 权限码 apis.catalog.create；明细至少一条且同产品不重复；billingMode 只接受 subscription
// （留空按 subscription 处理，按量计费已下沉产品层），订阅周期须为 monthly/quarterly/yearly（平台校验）。
func (this *ApisResource) CreatePlan(ctx context.Context, input ApisPlanInput) (*ApisPlan, error) {

	var result ApisPlan
	if err := this.client.post(ctx, "/api/apis-plans/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// UpdatePlan - 修改套餐（主表整行 + 明细全量替换；version 冲突返回 409）：PUT /api/apis-plans/update
// 权限码 apis.catalog.update；明细校验口径与新建一致。
func (this *ApisResource) UpdatePlan(ctx context.Context, input ApisPlanInput) (*ApisPlan, error) {

	var result ApisPlan
	if err := this.client.put(ctx, "/api/apis-plans/update", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RemovePlan - 删除套餐（软删，同事务软删全部明细；上架中的套餐须先下架）：DELETE /api/apis-plans/remove
// 权限码 apis.catalog.delete（风险级别 high，平台写审计）。
func (this *ApisResource) RemovePlan(ctx context.Context, id int) (*IdResult, error) {

	var result IdResult
	if err := this.client.del(ctx, "/api/apis-plans/remove", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ---------- 能力上游管理（apis-products/upstream*，9） ----------
//
// 上游是「能力级平台共享单例」：同一能力可挂多个产品，密钥与数据文件全平台只有一份，
// 改动对同能力所有产品即时生效；密钥只进不出（状态接口只回掩码），数据文件上传
// HTTP 走 multipart、 gRPC 走 JSON base64（传输层各自适配，本组方法签名一致）。

// GetCapabilityUpstream - 能力上游状态（密钥只出掩码；无数据/缓存概念的能力对应字段为 nil）：
// GET /api/apis-products/upstream?capability=xx
// 权限码 apis.catalog.read。
func (this *ApisResource) GetCapabilityUpstream(ctx context.Context, capability string) (*ApisUpstreamStatus, error) {

	var result ApisUpstreamStatus
	if err := this.client.get(ctx, "/api/apis-products/upstream", map[string]any{"capability": capability}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetUpstreamKey - 设置能力上游密钥（机器绑定加密入库，即时生效；明文只存在于本次请求调用栈）：
// PUT /api/apis-products/upstream-key
// 权限码 apis.catalog.update。
func (this *ApisResource) SetUpstreamKey(ctx context.Context, input ApisUpstreamKeyInput) error {

	return this.client.put(ctx, "/api/apis-products/upstream-key", input, nil)
}

// VerifyUpstream - 验证候选上游配置有效性（scene = key 密钥 / endpoint 接口地址；
// 真实探测上游，不持久化；「变更先验证，有效才允许保存」）：POST /api/apis-products/upstream-verify
// 权限码 apis.catalog.update。
func (this *ApisResource) VerifyUpstream(ctx context.Context, input ApisUpstreamVerifyInput) error {

	return this.client.post(ctx, "/api/apis-products/upstream-verify", input, nil)
}

// ClearUpstreamKey - 清除能力上游密钥：DELETE /api/apis-products/upstream-key
// 权限码 apis.catalog.update。
func (this *ApisResource) ClearUpstreamKey(ctx context.Context, capability string) error {

	return this.client.del(ctx, "/api/apis-products/upstream-key", map[string]any{"capability": capability}, nil)
}

// UploadUpstreamData - 上传能力上游数据文件（能力包校验后落盘热切换，64MB 上限）：
// POST /api/apis-products/upstream-data
// 权限码 apis.catalog.update；HTTP 走 multipart（文件字段 file），gRPC 由传输层转 JSON base64。
/**
 * @param capability string - 能力编码（如 ip-locate）
 * @param fileName string - 原始文件名（仅作留痕，不参与落盘路径）
 * @param content io.Reader - 数据文件内容
 * @example：
 * 	status, err := client.Apis.UploadUpstreamData(ctx, "ip-locate", "ip2region_v4.xdb", file)
 */
func (this *ApisResource) UploadUpstreamData(ctx context.Context, capability string, fileName string, content io.Reader) (*ApisUpstreamDataStatus, error) {

	var result ApisUpstreamDataStatus
	fields := map[string]string{"capability": capability}
	if err := this.client.postMultipart(ctx, "/api/apis-products/upstream-data", fields, "file", fileName, content, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ResetUpstreamData - 恢复能力上游数据为内嵌默认：PUT /api/apis-products/upstream-data-reset
// 权限码 apis.catalog.update。
func (this *ApisResource) ResetUpstreamData(ctx context.Context, capability string) (*ApisUpstreamDataStatus, error) {

	var result ApisUpstreamDataStatus
	if err := this.client.put(ctx, "/api/apis-products/upstream-data-reset", map[string]any{"capability": capability}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetUpstreamCache - 保存能力缓存策略（缓存时间 + 库刷新天数，持久化并即时生效）：
// PUT /api/apis-products/upstream-cache
// 权限码 apis.catalog.update。
func (this *ApisResource) SetUpstreamCache(ctx context.Context, input ApisUpstreamCacheInput) (*ApisUpstreamCacheStatus, error) {

	var result ApisUpstreamCacheStatus
	if err := this.client.put(ctx, "/api/apis-products/upstream-cache", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ClearUpstreamCache - 清空能力平台缓存（只清缓存不动库，下次查询回库/回源后重建）：
// DELETE /api/apis-products/upstream-cache
// 权限码 apis.catalog.update。
func (this *ApisResource) ClearUpstreamCache(ctx context.Context, capability string) error {

	return this.client.del(ctx, "/api/apis-products/upstream-cache", map[string]any{"capability": capability}, nil)
}

// SetUpstreamOptions - 保存能力上游高级参数（能力包逐项校验 + 持久化 + 热生效），返回最新快照：
// PUT /api/apis-products/upstream-options
// 权限码 apis.catalog.update。
func (this *ApisResource) SetUpstreamOptions(ctx context.Context, input ApisUpstreamOptionsInput) (map[string]any, error) {

	var result map[string]any
	if err := this.client.put(ctx, "/api/apis-products/upstream-options", input, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================= 订单（apis-orders，11） =============================

// FindOrders - 我的订单分页（member 强制本人）：GET /api/apis-orders/find
// 权限码 apis.order.read；平台侧请用 FindManageOrders。
func (this *ApisResource) FindOrders(ctx context.Context, params *ApisOrderQuery) (*Page[ApisOrder], error) {

	var result Page[ApisOrder]
	if err := this.client.get(ctx, "/api/apis-orders/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetOrder - 我的订单详情（id 与单号二选一）：GET /api/apis-orders/take
// 权限码 apis.order.read。
func (this *ApisResource) GetOrder(ctx context.Context, id int, orderNo string) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.getWithQuery(ctx, "/api/apis-orders/take", apisTargetQuery(id, "orderNo", orderNo), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateOrder - 商城下单（订阅购买/续费）：POST /api/apis-orders/create
// 权限码 apis.order.create；requestId 为幂等键（重复提交返回原单），
// payChannel=balance 时平台同事务扣款并交付订阅（余额不足下单即拒绝）。
func (this *ApisResource) CreateOrder(ctx context.Context, input ApisOrderInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PayOrder - 余额支付订单（仅余额通道待支付订单；已支付幂等返回原单）：POST /api/apis-orders/pay
// 权限码 apis.order.pay。
func (this *ApisResource) PayOrder(ctx context.Context, input ApisOrderTarget) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/pay", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelOrder - 取消订单（仅待支付的订阅类订单）：POST /api/apis-orders/cancel
// 权限码 apis.order.cancel；退款单只能经退款审批终结。
func (this *ApisResource) CancelOrder(ctx context.Context, input ApisOrderCancelInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/cancel", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindManageOrders - 订单运营分页（平台全量 + 按用户/状态/通道/套餐筛选）：GET /api/apis-orders/manage/find
// 权限码 apis.order.manage。
func (this *ApisResource) FindManageOrders(ctx context.Context, params *ApisOrderQuery) (*Page[ApisOrder], error) {

	var result Page[ApisOrder]
	if err := this.client.get(ctx, "/api/apis-orders/manage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManageOrder - 订单运营详情（id 与单号二选一）：GET /api/apis-orders/manage/take
// 权限码 apis.order.manage。
func (this *ApisResource) GetManageOrder(ctx context.Context, id int, orderNo string) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.getWithQuery(ctx, "/api/apis-orders/manage/take", apisTargetQuery(id, "orderNo", orderNo), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ConfirmOrder - 确认线下收款（pending → paid，同事务交付订阅）：POST /api/apis-orders/manage/confirm
// 权限码 apis.order.manage（风险级别 high）；reviewNote 必填。
func (this *ApisResource) ConfirmOrder(ctx context.Context, input ApisOrderConfirmInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/manage/confirm", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CloseOrder - 关闭订单（平台侧；仅待支付订阅类订单，等价于取消流转）：POST /api/apis-orders/manage/close
// 权限码 apis.order.manage。
func (this *ApisResource) CloseOrder(ctx context.Context, input ApisOrderCancelInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/manage/close", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ApplyRefund - 申请退款（对已支付订阅订单创建退款单，等待平台审批）：POST /api/apis-orders/refund-apply
// 权限码 apis.order.refund；requestId 幂等，首版为全额退款。
func (this *ApisResource) ApplyRefund(ctx context.Context, input ApisRefundApplyInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/refund-apply", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReviewRefund - 退款审批（通过则单事务落地退款并终止订阅，驳回只翻退款单状态）：
// POST /api/apis-orders/manage/refund-review
// 权限码 apis.order.manage（风险级别 high）；approve 与 reviewNote 由入参显式上送。
func (this *ApisResource) ReviewRefund(ctx context.Context, input ApisRefundReviewInput) (*ApisOrder, error) {

	var result ApisOrder
	if err := this.client.post(ctx, "/api/apis-orders/manage/refund-review", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 订阅（apis-subscriptions，4） =============================

// FindSubscriptions - 我的订阅分页（member 强制本人）：GET /api/apis-subscriptions/find
// 权限码 apis.subscription.read。
func (this *ApisResource) FindSubscriptions(ctx context.Context, params *ApisSubscriptionQuery) (*Page[ApisSubscription], error) {

	var result Page[ApisSubscription]
	if err := this.client.get(ctx, "/api/apis-subscriptions/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetSubscription - 订阅详情：GET /api/apis-subscriptions/take?id=N
// 权限码 apis.subscription.read。
func (this *ApisResource) GetSubscription(ctx context.Context, id int) (*ApisSubscription, error) {

	var result ApisSubscription
	if err := this.client.getWithQuery(ctx, "/api/apis-subscriptions/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelSubscription - 退订（仅生效中订阅；状态即时翻转并关闭自动续费，已付费用不退）：
// POST /api/apis-subscriptions/cancel
// 权限码 apis.subscription.cancel；如需退款走订单退款单（ApplyRefund）。
func (this *ApisResource) CancelSubscription(ctx context.Context, id int, reason string) (*ApisSubscription, error) {

	var result ApisSubscription
	if err := this.client.post(ctx, "/api/apis-subscriptions/cancel", map[string]any{"id": id, "reason": reason}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetSubscriptionAutoRenew - 调整自动续费偏好（仅生效中订阅）：POST /api/apis-subscriptions/auto-renew
// 权限码 apis.subscription.manage；autoRenew 显式上送（false 亦必须送达，平台据此关闭自动续费）。
func (this *ApisResource) SetSubscriptionAutoRenew(ctx context.Context, id int, autoRenew bool) (*ApisSubscription, error) {

	var result ApisSubscription
	if err := this.client.post(ctx, "/api/apis-subscriptions/auto-renew", map[string]any{"id": id, "autoRenew": autoRenew}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 余额与充值（apis-recharges / apis-balances，13） =============================

// FindRecharges - 充值单分页（member 强制本人；平台侧按数据范围）：GET /api/apis-recharges/find
// 权限码 apis.balance.read。
func (this *ApisResource) FindRecharges(ctx context.Context, params *ApisRechargeQuery) (*Page[ApisRecharge], error) {

	var result Page[ApisRecharge]
	if err := this.client.get(ctx, "/api/apis-recharges/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRecharge - 充值单详情：GET /api/apis-recharges/take?id=N
// 权限码 apis.balance.read。
func (this *ApisResource) GetRecharge(ctx context.Context, id int) (*ApisRecharge, error) {

	var result ApisRecharge
	if err := this.client.getWithQuery(ctx, "/api/apis-recharges/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateRecharge - 提交线下充值申请（requestId 幂等，重复提交返回原单）：POST /api/apis-recharges/create
// 权限码 apis.balance.recharge；金额下限与账户状态由平台校验。
func (this *ApisResource) CreateRecharge(ctx context.Context, input ApisRechargeInput) (*ApisRechargeResult, error) {

	var result ApisRechargeResult
	if err := this.client.post(ctx, "/api/apis-recharges/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CancelRecharge - 取消充值单（仅待支付）：POST /api/apis-recharges/cancel
// 权限码 apis.balance.recharge。
func (this *ApisResource) CancelRecharge(ctx context.Context, id int, reason string) (*ApisRecharge, error) {

	var result ApisRecharge
	if err := this.client.post(ctx, "/api/apis-recharges/cancel", map[string]any{"id": id, "reason": reason}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ConfirmRecharge - 确认充值入账（同事务写 recharge 流水，重复确认幂等返回 replayed=true）：
// POST /api/apis-recharges/confirm
// 权限码 apis.balance.adjust（风险级别 high）；reviewNote 必填。
func (this *ApisResource) ConfirmRecharge(ctx context.Context, input ApisRechargeConfirmInput) (*ApisRechargeResult, error) {

	var result ApisRechargeResult
	if err := this.client.post(ctx, "/api/apis-recharges/confirm", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBalance - 我的余额账户（惰性建户，归属取登录态）：GET /api/apis-balances/take
// 权限码 apis.balance.read。
func (this *ApisResource) GetBalance(ctx context.Context) (*ApisBalanceAccount, error) {

	var result ApisBalanceAccount
	if err := this.client.get(ctx, "/api/apis-balances/take", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBalanceSpent - 本月已消费（分；权威口径为余额流水）：GET /api/apis-balances/spent
// 权限码 apis.balance.read；与 monthlySpendLimit 配套展示。
func (this *ApisResource) GetBalanceSpent(ctx context.Context) (*ApisBalanceSpentResult, error) {

	var result ApisBalanceSpentResult
	if err := this.client.get(ctx, "/api/apis-balances/spent", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindBalanceLogs - 余额流水分页（member 强制本人；平台侧按数据范围）：GET /api/apis-balances/logs
// 权限码 apis.balance.read。
func (this *ApisResource) FindBalanceLogs(ctx context.Context, params *ApisBalanceLogQuery) (*Page[ApisBalanceLog], error) {

	var result Page[ApisBalanceLog]
	if err := this.client.get(ctx, "/api/apis-balances/logs", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetBalanceLimit - 设置月度消费限制（0=关闭；不得低于本月已消费）：POST /api/apis-balances/limit
// 权限码 apis.balance.manage；负数由平台拒绝（参数按原始值读取）。
func (this *ApisResource) SetBalanceLimit(ctx context.Context, userId int, limit int64) (*ApisBalanceAccount, error) {

	var result ApisBalanceAccount
	body := map[string]any{"userId": userId, "limit": limit}
	if err := this.client.post(ctx, "/api/apis-balances/limit", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AdjustBalance - 平台调整余额（正数赠送、负数扣减；说明必填并写审计）：POST /api/apis-balances/adjust
// 权限码 apis.balance.adjust（风险级别 high）；返回调整后的余额状态（幂等命中时 replayed=true）。
func (this *ApisResource) AdjustBalance(ctx context.Context, input ApisBalanceAdjustInput) (*ApisBalanceState, error) {

	var result ApisBalanceState
	if err := this.client.post(ctx, "/api/apis-balances/adjust", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetBalanceStatus - 账户风控状态流转（normal/frozen；冻结后禁止消费与充值，仅可退款）：
// POST /api/apis-balances/status
// 权限码 apis.balance.manage（风险级别 high）。
func (this *ApisResource) SetBalanceStatus(ctx context.Context, userId int, status string, reason string) (*ApisBalanceAccount, error) {

	var result ApisBalanceAccount
	body := map[string]any{"userId": userId, "status": status, "reason": reason}
	if err := this.client.post(ctx, "/api/apis-balances/status", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindManageBalances - 账户运营分页（平台侧，按用户/状态筛选）：GET /api/apis-balances/manage/find
// 权限码 apis.balance.manage。
func (this *ApisResource) FindManageBalances(ctx context.Context, params *ApisAccountQuery) (*Page[ApisBalanceAccount], error) {

	var result Page[ApisBalanceAccount]
	if err := this.client.get(ctx, "/api/apis-balances/manage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManageBalance - 账户运营详情（按 userId；不存在则惰性建户）：GET /api/apis-balances/manage/take?userId=N
// 权限码 apis.balance.manage；userId 必填。
func (this *ApisResource) GetManageBalance(ctx context.Context, userId int) (*ApisBalanceAccount, error) {

	var result ApisBalanceAccount
	if err := this.client.get(ctx, "/api/apis-balances/manage/take", map[string]any{"userId": userId}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 账务与用量只读视图（apis-bills / apis-usage，6） =============================

// FindBills - 我的账单分页（member 强制本人）：GET /api/apis-bills/find
// 权限码 apis.usage.read。
func (this *ApisResource) FindBills(ctx context.Context, params *ApisBillQuery) (*Page[ApisBill], error) {

	var result Page[ApisBill]
	if err := this.client.get(ctx, "/api/apis-bills/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBill - 账单详情：GET /api/apis-bills/take?id=N
// 权限码 apis.usage.read。
func (this *ApisResource) GetBill(ctx context.Context, id int) (*ApisBill, error) {

	var result ApisBill
	if err := this.client.getWithQuery(ctx, "/api/apis-bills/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExportBills - 我的账单导出（xlsx 双 sheet：账单概览 + 明细平铺）：GET /api/apis-bills/export
// 权限码 apis.usage.export；筛选口径与 FindBills 一致并忽略分页。
//
// 平台两协议均返回 `{fileName, content(base64)}` 信封，本方法在内部解码 base64，
// 返回文件名与 xlsx 原始字节（调用方直接落盘，无需关心编码）。
func (this *ApisResource) ExportBills(ctx context.Context, params *ApisBillQuery) (string, []byte, error) {

	return this.exportBills(ctx, "/api/apis-bills/export", params)
}

// FindUsageRecords - 我的用量流水分页（member 强制本人）：GET /api/apis-usage/find
// 权限码 apis.usage.read；数据由运行面计量管道写入。
func (this *ApisResource) FindUsageRecords(ctx context.Context, params *ApisUsageRecordQuery) (*Page[ApisUsageRecord], error) {

	var result Page[ApisUsageRecord]
	if err := this.client.get(ctx, "/api/apis-usage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindUsageDaily - 我的用量日聚合分页（账单与看板的数据源）：GET /api/apis-usage/daily
// 权限码 apis.usage.read。
func (this *ApisResource) FindUsageDaily(ctx context.Context, params *ApisUsageDailyQuery) (*Page[ApisUsageDaily], error) {

	var result Page[ApisUsageDaily]
	if err := this.client.get(ctx, "/api/apis-usage/daily", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetUsageDailySummary - 我的用量看板汇总（日聚合 GROUP BY + 今日实时镜像条带）：GET /api/apis-usage/daily/summary
// 权限码 apis.usage.read；一次响应供全部卡片与图表使用。
func (this *ApisResource) GetUsageDailySummary(ctx context.Context, params *ApisUsageDailySummaryQuery) (*ApisUsageDailySummary, error) {

	var result ApisUsageDailySummary
	if err := this.client.get(ctx, "/api/apis-usage/daily/summary", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 监控只读视图（apis-monitor，7） =============================

// FindMonitorBills - 账单监控分页（平台全量，按用户/账期/状态筛选）：GET /api/apis-monitor/bills/find
// 权限码 apis.monitor.read。
func (this *ApisResource) FindMonitorBills(ctx context.Context, params *ApisBillQuery) (*Page[ApisBill], error) {

	var result Page[ApisBill]
	if err := this.client.get(ctx, "/api/apis-monitor/bills/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMonitorBill - 账单监控详情：GET /api/apis-monitor/bills/take?id=N
// 权限码 apis.monitor.read。
func (this *ApisResource) GetMonitorBill(ctx context.Context, id int) (*ApisBill, error) {

	var result ApisBill
	if err := this.client.getWithQuery(ctx, "/api/apis-monitor/bills/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExportMonitorBills - 账单监控导出（平台全量/按用户筛选）：GET /api/apis-monitor/bills/export
// 权限码 apis.monitor.export；服务端筛选口径与 FindMonitorBills 一致并忽略分页。
// 返回文件名与 xlsx 原始字节（base64 在方法内解码，与 ExportBills 同口径）。
func (this *ApisResource) ExportMonitorBills(ctx context.Context, params *ApisBillQuery) (string, []byte, error) {

	return this.exportBills(ctx, "/api/apis-monitor/bills/export", params)
}

// FindMonitorBalanceLogs - 余额流水监控分页（平台全量，按用户/类型/单号筛选）：
// GET /api/apis-monitor/logs/find
// 权限码 apis.monitor.read。
func (this *ApisResource) FindMonitorBalanceLogs(ctx context.Context, params *ApisBalanceLogQuery) (*Page[ApisBalanceLog], error) {

	var result Page[ApisBalanceLog]
	if err := this.client.get(ctx, "/api/apis-monitor/logs/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindMonitorUsageRecords - 用量监控流水（平台全量，按用户/能力/计费模式/结果筛选）：
// GET /api/apis-monitor/usage/find
// 权限码 apis.monitor.read。
func (this *ApisResource) FindMonitorUsageRecords(ctx context.Context, params *ApisUsageRecordQuery) (*Page[ApisUsageRecord], error) {

	var result Page[ApisUsageRecord]
	if err := this.client.get(ctx, "/api/apis-monitor/usage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindMonitorUsageDaily - 用量监控日聚合（平台全量，按用户/能力/计费模式/统计日期筛选）：
// GET /api/apis-monitor/usage/daily
// 权限码 apis.monitor.read。
func (this *ApisResource) FindMonitorUsageDaily(ctx context.Context, params *ApisUsageDailyQuery) (*Page[ApisUsageDaily], error) {

	var result Page[ApisUsageDaily]
	if err := this.client.get(ctx, "/api/apis-monitor/usage/daily", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetMonitorUsageSummary - 用量看板汇总（平台全量，可按用户/能力/日期区间筛选）：
// GET /api/apis-monitor/usage/summary
// 权限码 apis.monitor.read；平台全量视图无今日实时条带（todayRealtime 为 null）。
func (this *ApisResource) GetMonitorUsageSummary(ctx context.Context, params *ApisUsageDailySummaryQuery) (*ApisUsageDailySummary, error) {

	var result ApisUsageDailySummary
	if err := this.client.get(ctx, "/api/apis-monitor/usage/summary", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 资源层内部助手 =============================

// apisTargetQuery - 「id 与单号二选一」的查询参数（空值不上送，与平台 apisNeedTarget 口径一致）。
// key 为单号参数名（订单用 orderNo、充值单用 rechargeNo）。
func apisTargetQuery(id int, key string, no string) url.Values {
	query := url.Values{}
	if id > 0 {
		query.Set("id", cast.ToString(id))
	}
	if no != "" {
		query.Set(key, no)
	}
	return query
}

// exportBills - 两条账单导出路由的共用实现（我的账单 / 监控账单）：
// 平台返回 {fileName, content(base64)} 信封，此处解码 base64 并把解码失败包装为可定位的错误。
func (this *ApisResource) exportBills(ctx context.Context, path string, params *ApisBillQuery) (string, []byte, error) {

	var envelope apisBillExportEnvelope
	if err := this.client.get(ctx, path, params, &envelope); err != nil {
		return "", nil, err
	}
	content, err := base64.StdEncoding.DecodeString(envelope.Content)
	if err != nil {
		return "", nil, fmt.Errorf("licence: 账单导出内容 base64 解码失败（%s）: %w", path, err)
	}
	return envelope.FileName, content, nil
}
