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
// `/api/wallet-accounts/*`、`/api/apis-bills/*`、`/api/apis-usage/*`、`/api/apis-monitor/*`、
// `/api/apis-llm-channels/*`、`/api/apis-llm-logs/*`、`/api/apis-keys/*`）。
//
// 覆盖范围：licen-hub 商城管理面 **73 条受控路由**（10 个 gRPC 服务），方法名与平台登记表
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
//   - LLM 统一网关（llm-channels 9 / llm-logs 3 / apis-keys 5）：渠道 apiKey 与 sk-key 明文只进不出
//     （读视图绝不回传，sk-key 明文完整 key 仅创建响应返回一次）；usage 探测与上游模型列表的回显
//     为平台动态装配（map[string]any），typed 方法原样透出；
//   - `Export*` 三条导出方法返回 (fileName, xlsx 原始字节, error)，base64 在方法内解码（与平台 HTTP/gRPC
//     两协议同形的 `{fileName, content}` 信封一一对应，消费方无需关心编码）。
type ApisResource struct {
	// client - 所属客户端
	client *AdminClient
}

// ============================= 超时与传输 =============================

// 商城管理面的传输差异（对调用方透明）：
//   - HTTP：`{code,msg,data}` 信封 + 平台参数中间件（GET 走 query、写路径走 JSON body）；
//   - gRPC：10 个 **proto-less** 服务（平台按 `ApisSpecs` 登记表装配 ServiceDesc，复用既有
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
	// apisBalanceService - 钱包账户（自助 + 平台运营 + 调账用户选项；gRPC 传输层内部标识，
	// 不随 SDK 钱包改名而变化——服务名与 full method 保持原值）
	apisBalanceService = "licenhub.licence.v1.ApisBalanceAdminService"
	// apisUsageService - 账务与用量只读视图（我的账单/用量）
	apisUsageService = "licenhub.licence.v1.ApisUsageAdminService"
	// apisMonitorService - 监控只读视图（平台全量账单/流水/用量）
	apisMonitorService = "licenhub.licence.v1.ApisMonitorAdminService"
	// apisLlmChannelService - LLM 统一网关上游渠道（管理视图 + usage 探测 + 模型列表 + 定价抓取）
	apisLlmChannelService = "licenhub.licence.v1.ApisLlmChannelAdminService"
	// apisLlmLogService - LLM 调用观测明细（只读 + 导出）
	apisLlmLogService = "licenhub.licence.v1.ApisLlmLogAdminService"
	// apisKeyService - sk- API Key 生命周期（分页/详情/签发/吊销/删除）
	apisKeyService = "licenhub.licence.v1.ApisKeyAdminService"
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
	apisGetBalanceFullMethod            = "/" + apisBalanceService + "/GetBalance"
	apisGetBalanceSpentFullMethod       = "/" + apisBalanceService + "/GetBalanceSpent"
	apisFindBalanceLogsFullMethod       = "/" + apisBalanceService + "/FindBalanceLogs"
	apisSetBalanceLimitFullMethod       = "/" + apisBalanceService + "/SetBalanceLimit"
	apisAdjustBalanceFullMethod         = "/" + apisBalanceService + "/AdjustBalance"
	apisSetBalanceStatusFullMethod      = "/" + apisBalanceService + "/SetBalanceStatus"
	apisFindManageBalancesFullMethod    = "/" + apisBalanceService + "/FindManageBalances"
	apisGetManageBalanceFullMethod      = "/" + apisBalanceService + "/GetManageBalance"
	apisFindAdjustUserOptionsFullMethod = "/" + apisBalanceService + "/FindAdjustUserOptions"

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

	// ---------- ApisLlmChannelAdminService ----------
	apisFindLlmChannelsFullMethod       = "/" + apisLlmChannelService + "/FindLlmChannels"
	apisGetLlmChannelFullMethod         = "/" + apisLlmChannelService + "/GetLlmChannel"
	apisRowsLlmChannelsFullMethod       = "/" + apisLlmChannelService + "/RowsLlmChannels"
	apisMarketLlmModelsFullMethod       = "/" + apisLlmChannelService + "/MarketLlmModels"
	apisSaveLlmChannelFullMethod        = "/" + apisLlmChannelService + "/SaveLlmChannel"
	apisVerifyLlmChannelUsageFullMethod = "/" + apisLlmChannelService + "/VerifyLlmChannelUsage"
	apisFetchLlmChannelModelsFullMethod = "/" + apisLlmChannelService + "/FetchLlmChannelModels"
	apisScanLlmChannelPricingFullMethod = "/" + apisLlmChannelService + "/ScanLlmChannelPricing"
	apisRemoveLlmChannelFullMethod      = "/" + apisLlmChannelService + "/RemoveLlmChannel"

	// ---------- ApisLlmLogAdminService ----------
	apisFindLlmLogsFullMethod   = "/" + apisLlmLogService + "/FindLlmLogs"
	apisGetLlmLogFullMethod     = "/" + apisLlmLogService + "/GetLlmLog"
	apisExportLlmLogsFullMethod = "/" + apisLlmLogService + "/ExportLlmLogs"

	// ---------- ApisKeyAdminService ----------
	apisFindApisKeysFullMethod  = "/" + apisKeyService + "/FindApisKeys"
	apisGetApisKeyFullMethod    = "/" + apisKeyService + "/GetApisKey"
	apisCreateApisKeyFullMethod = "/" + apisKeyService + "/CreateApisKey"
	apisRevokeApisKeyFullMethod = "/" + apisKeyService + "/RevokeApisKey"
	apisRemoveApisKeyFullMethod = "/" + apisKeyService + "/RemoveApisKey"
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

// ============================= 平台钱包（wallet-accounts，9） =============================
// 充值申请/充值审核已整体下线（平台 wallet_recharges 表删除），只保留管理员人工调账 adjust：
// 账户余额只经 AdjustWallet 变更并记钱包流水（txType=adjust）。

// GetWallet - 我的钱包账户（惰性建户，归属取登录态）：GET /api/wallet-accounts/take
// 权限码 wallet.account.read。
func (this *ApisResource) GetWallet(ctx context.Context) (*WalletAccount, error) {

	var result WalletAccount
	if err := this.client.get(ctx, "/api/wallet-accounts/take", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetWalletSpent - 本月已消费（分；权威口径为钱包流水）：GET /api/wallet-accounts/spent
// 权限码 wallet.account.read；与 monthlySpendLimit 配套展示。
func (this *ApisResource) GetWalletSpent(ctx context.Context) (*WalletSpentResult, error) {

	var result WalletSpentResult
	if err := this.client.get(ctx, "/api/wallet-accounts/spent", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindWalletLogs - 钱包流水分页（member 强制本人；平台侧按数据范围）：GET /api/wallet-accounts/logs
// 权限码 wallet.account.read。
func (this *ApisResource) FindWalletLogs(ctx context.Context, params *WalletLogQuery) (*Page[WalletLog], error) {

	var result Page[WalletLog]
	if err := this.client.get(ctx, "/api/wallet-accounts/logs", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetWalletLimit - 设置月度消费限制（0=关闭；不得低于本月已消费）：POST /api/wallet-accounts/limit
// 权限码 wallet.account.manage；负数由平台拒绝（参数按原始值读取）。
func (this *ApisResource) SetWalletLimit(ctx context.Context, userId int, limit int64) (*WalletAccount, error) {

	var result WalletAccount
	body := map[string]any{"userId": userId, "limit": limit}
	if err := this.client.post(ctx, "/api/wallet-accounts/limit", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AdjustWallet - 平台调整钱包余额（正数赠送、负数扣减；说明必填并写审计）：POST /api/wallet-accounts/adjust
// 权限码 wallet.adjust（风险级别 high）；返回调整后的钱包状态（幂等命中时 replayed=true）。
func (this *ApisResource) AdjustWallet(ctx context.Context, input WalletAdjustInput) (*WalletState, error) {

	var result WalletState
	if err := this.client.post(ctx, "/api/wallet-accounts/adjust", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SetWalletStatus - 钱包账户风控状态流转（normal/frozen；冻结后禁止消费与充值，仅可退款）：
// POST /api/wallet-accounts/status
// 权限码 wallet.account.manage（风险级别 high）。
func (this *ApisResource) SetWalletStatus(ctx context.Context, userId int, status string, reason string) (*WalletAccount, error) {

	var result WalletAccount
	body := map[string]any{"userId": userId, "status": status, "reason": reason}
	if err := this.client.post(ctx, "/api/wallet-accounts/status", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindManageWallets - 钱包账户运营分页（平台侧，按用户/状态筛选）：GET /api/wallet-accounts/manage/find
// 权限码 wallet.account.manage。
func (this *ApisResource) FindManageWallets(ctx context.Context, params *WalletAccountQuery) (*Page[WalletAccount], error) {

	var result Page[WalletAccount]
	if err := this.client.get(ctx, "/api/wallet-accounts/manage/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetManageWallet - 钱包账户运营详情（按 userId；不存在则惰性建户）：GET /api/wallet-accounts/manage/take?userId=N
// 权限码 wallet.account.manage；userId 必填。
func (this *ApisResource) GetManageWallet(ctx context.Context, userId int) (*WalletAccount, error) {

	var result WalletAccount
	if err := this.client.get(ctx, "/api/wallet-accounts/manage/take", map[string]any{"userId": userId}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FindAdjustUserOptions - 人工调账目标用户选项（钱包账户控制器 user-options，调账弹窗用户搜索选择器）：
// GET /api/wallet-accounts/user-options
// 权限码 wallet.account.manage；仅返回有钱包账户的正常用户（未开户用户无法调账，选项阶段即过滤），
// 关键词按 UID（数值精确）/账号/邮箱/手机号/昵称模糊匹配；SDK 方法名与 gRPC 方法名逐字一致。
func (this *ApisResource) FindAdjustUserOptions(ctx context.Context, params *WalletAdjustUserQuery) (*[]WalletAdjustUserOption, error) {

	var result []WalletAdjustUserOption
	if err := this.client.get(ctx, "/api/wallet-accounts/user-options", params, &result); err != nil {
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

// FindMonitorBalanceLogs - 钱包流水监控分页（平台全量，按用户/类型/单号筛选）：
// GET /api/apis-monitor/logs/find
// 权限码 apis.monitor.read。
func (this *ApisResource) FindMonitorBalanceLogs(ctx context.Context, params *WalletLogQuery) (*Page[WalletLog], error) {

	var result Page[WalletLog]
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

// ============================= LLM 渠道（apis-llm-channels，9） =============================
//
// LLM 统一网关上游渠道（设计 09 §6.3）：主表 + llm_channel_models 映射明细（保存时全量替换，
// 照套餐 plan_items 事务模式）；模型按名直接挂在映射行（独立模型目录已下线），报价/上下文/
// 输出上限随明细维护。密钥红线：apiKey 任何读接口不回传（apisOK 出口前显式置空 + 模型 json:"-"）。

// FindLlmChannels - 上游渠道分页（平台管理视图，含映射明细）：GET /api/apis-llm-channels/find
// 权限码 apis.llmChannel.read；ApiKey 已置空绝不回传。
func (this *ApisResource) FindLlmChannels(ctx context.Context, params *ApisLlmChannelQuery) (*Page[ApisLlmChannelView], error) {

	var result Page[ApisLlmChannelView]
	if err := this.client.get(ctx, "/api/apis-llm-channels/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetLlmChannel - 上游渠道详情（含映射明细；不回传 apiKey 明文）：GET /api/apis-llm-channels/take?id=N
// 权限码 apis.llmChannel.read。
func (this *ApisResource) GetLlmChannel(ctx context.Context, id int) (*ApisLlmChannelView, error) {

	var result ApisLlmChannelView
	if err := this.client.getWithQuery(ctx, "/api/apis-llm-channels/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RowsLlmChannels - 上游渠道全量（平台管理视图；不分页，口径同 find）：GET /api/apis-llm-channels/rows
// 权限码 apis.llmChannel.read。
func (this *ApisResource) RowsLlmChannels(ctx context.Context, params *ApisLlmChannelQuery) (*[]ApisLlmChannelView, error) {

	var result []ApisLlmChannelView
	if err := this.client.get(ctx, "/api/apis-llm-channels/rows", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// MarketLlmModels - 在售模型价格表（member 选购页/sk-key 白名单选项：聚合口径，只回已定价模型）：
// GET /api/apis-llm-channels/market-models
// 权限码 apis.market.read；平台经「启用映射 × 启用渠道」目录聚合（ListOnSaleModels）内联装配。
func (this *ApisResource) MarketLlmModels(ctx context.Context) (*[]ApisLlmModelOffer, error) {

	var result []ApisLlmModelOffer
	if err := this.client.get(ctx, "/api/apis-llm-channels/market-models", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SaveLlmChannel - 保存渠道（主表 + 明细全量替换；apiKey 留空 = 不变更；启用受探测闸门约束）：
// POST /api/apis-llm-channels/save
// 权限码 apis.llmChannel.update；端点/协议族/密钥变更会使既有 usage 探测结论失效，需重新探测后方可启用。
func (this *ApisResource) SaveLlmChannel(ctx context.Context, input ApisLlmChannelInput) (*ApisLlmChannelView, error) {

	var result ApisLlmChannelView
	if err := this.client.post(ctx, "/api/apis-llm-channels/save", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// VerifyLlmChannelUsage - usage 探测（真实调用上游，结果落 usage_probe / usage_verified_at）：
// POST /api/apis-llm-channels/verify-usage
// 权限码 apis.llmChannel.update；纯候选探测（id = 0，保存前强制场景）不落库、结果原样回显。
// 平台回显为动态装配信封（ok/detail/sample/inputTokens/outputTokens/streamOk[/id][/channel]），
// typed 方法原样透出（与 SetUpstreamOptions 的 map 形态同口径）。
func (this *ApisResource) VerifyLlmChannelUsage(ctx context.Context, input ApisLlmChannelVerifyInput) (map[string]any, error) {

	var result map[string]any
	if err := this.client.post(ctx, "/api/apis-llm-channels/verify-usage", input, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchLlmChannelModels - 上游模型列表（平台侧）：POST /api/apis-llm-channels/models
// 权限码 apis.llmChannel.update；凭据解析与 usage 探测同源（apiKey 留空取库中已存密钥解密值）；
// ollama/cohere 协议族无清单端点，平台拒绝。回显 {models:[{model,displayName}]}，typed 方法原样透出。
func (this *ApisResource) FetchLlmChannelModels(ctx context.Context, input ApisLlmChannelVerifyInput) (map[string]any, error) {

	var result map[string]any
	if err := this.client.post(ctx, "/api/apis-llm-channels/models", input, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ScanLlmChannelPricing - 官方定价抓取（平台侧）：POST /api/apis-llm-channels/pricing-scan
// 权限码 apis.llmChannel.update；服务层统一编排（定价页解析 → 网页抓取管道 → 托管模型提取 → 24h 缓存），
// 结果不落库，回显由运营勾选回填。
func (this *ApisResource) ScanLlmChannelPricing(ctx context.Context, input ApisLlmPricingScanInput) (*ApisLlmPricingScanResult, error) {

	var result ApisLlmPricingScanResult
	if err := this.client.post(ctx, "/api/apis-llm-channels/pricing-scan", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RemoveLlmChannel - 删除渠道（软删，明细随同软删）：DELETE /api/apis-llm-channels/remove
// 权限码 apis.llmChannel.delete（风险级别 high，平台写审计）。
func (this *ApisResource) RemoveLlmChannel(ctx context.Context, id int) (*IdResult, error) {

	var result IdResult
	if err := this.client.del(ctx, "/api/apis-llm-channels/remove", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= LLM 调用观测（apis-llm-logs，3） =============================
//
// LLM 调用观测明细（设计 09 §6.5，只追加）：take/find/export 只读，不提供写接口；
// 平台观测表（member 不可见），导出与 find 同口径（操作口径取 apis 域 export）。

// FindLlmLogs - 调用观测明细分页（平台观测视图）：GET /api/apis-llm-logs/find
// 权限码 apis.llmLog.read。
func (this *ApisResource) FindLlmLogs(ctx context.Context, params *ApisLlmLogQuery) (*Page[ApisLlmLog], error) {

	var result Page[ApisLlmLog]
	if err := this.client.get(ctx, "/api/apis-llm-logs/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetLlmLog - 观测明细详情：GET /api/apis-llm-logs/take?id=N
// 权限码 apis.llmLog.read。
func (this *ApisResource) GetLlmLog(ctx context.Context, id int) (*ApisLlmLog, error) {

	var result ApisLlmLog
	if err := this.client.getWithQuery(ctx, "/api/apis-llm-logs/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExportLlmLogs - 观测明细导出（xlsx 字节流以 base64 随信封返回）：GET /api/apis-llm-logs/export
// 权限码 apis.llmLog.export；筛选口径与 FindLlmLogs 一致并忽略分页（单次导出上限由平台把关）。
// 返回文件名与 xlsx 原始字节（base64 在方法内解码，与 ExportBills 同口径）。
func (this *ApisResource) ExportLlmLogs(ctx context.Context, params *ApisLlmLogQuery) (string, []byte, error) {

	return this.exportBills(ctx, "/api/apis-llm-logs/export", params)
}

// ============================= sk- API Key（apis-keys，5） =============================
//
// sk- API Key 生命周期（设计 09 §5.2/§6.4）：member 自助签发/吊销/删除，明文完整 key 仅创建响应返回一次，
// key_hash 绝不出现在任何读响应；吊销/删除后平台使 sk-key 认证缓存立即失效。

// FindApisKeys - sk-key 分页（member 强制本人；平台侧按 apis 域读范围）：GET /api/apis-keys/find
// 权限码 apis.key.read。
func (this *ApisResource) FindApisKeys(ctx context.Context, params *ApisKeyQuery) (*Page[ApisKey], error) {

	var result Page[ApisKey]
	if err := this.client.get(ctx, "/api/apis-keys/find", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetApisKey - sk-key 详情（白名单投影，key_hash 绝不外泄）：GET /api/apis-keys/take?id=N
// 权限码 apis.key.read；member 硬边界：他人 key 归一为 404，不泄露存在性。
func (this *ApisResource) GetApisKey(ctx context.Context, id int) (*ApisKey, error) {

	var result ApisKey
	if err := this.client.getWithQuery(ctx, "/api/apis-keys/take", idQuery(id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateApisKey - 签发 sk-key（明文完整 key 仅本次响应返回一次）：POST /api/apis-keys/create
// 权限码 apis.key.create；AllowedModels 全部须已接入（存在于渠道映射），MonthlySpendLimit 不能为负。
func (this *ApisResource) CreateApisKey(ctx context.Context, input ApisKeyInput) (*ApisKeyCreated, error) {

	var result ApisKeyCreated
	if err := this.client.post(ctx, "/api/apis-keys/create", input, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RevokeApisKey - 吊销 sk-key（status → revoked，行保留可审计；即时生效）：PUT /api/apis-keys/revoke
// 权限码 apis.key.revoke；member 只能吊销本人 key，重复吊销平台返回 409。
func (this *ApisResource) RevokeApisKey(ctx context.Context, id int) (*ApisKey, error) {

	var result ApisKey
	if err := this.client.put(ctx, "/api/apis-keys/revoke", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RemoveApisKey - 删除 sk-key（软删进回收站）：DELETE /api/apis-keys/remove
// 权限码 apis.key.revoke；member 只能删除本人 key，删除即时生效。
func (this *ApisResource) RemoveApisKey(ctx context.Context, id int) (*IdResult, error) {

	var result IdResult
	if err := this.client.del(ctx, "/api/apis-keys/remove", map[string]any{"id": id}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================= 资源层内部助手 =============================

// apisTargetQuery - 「id 与单号二选一」的查询参数（空值不上送，与平台 apisNeedTarget 口径一致）。
// key 为单号参数名（当前仅订单用 orderNo）。
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

// exportBills - 三条导出路由的共用实现（我的账单 / 监控账单 / 观测明细）：
// 平台返回 {fileName, content(base64)} 信封，此处解码 base64 并把解码失败包装为可定位的错误。
func (this *ApisResource) exportBills(ctx context.Context, path string, params any) (string, []byte, error) {

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
