package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/inis-io/aide/licence/apis/core"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// 本文件承载项目套餐运行面的 SDK 公开方法：在售套餐目录查询（PlansCatalog）与套餐订单
// （PlanOrderCreate 购买/升级/续费下单、PlanOrderQuery 查单、PlanOrderCancel 取消、PlanOrderRows 分页）。
// licen-hub 为收款商户（inis-pay 收银台）：下单返回收银台链接，客户在收银台完成支付，
// 支付结果经回调通知（callback 事件 license.plan.changed）与信封重签生效，SDK 侧只负责下单/查单/取消。
//
// 契约形态与星链中枢同一范式（docs：proto/licence/v1/protocol-matrix.yaml 套餐运行面五行）：
// 业务语义只写一份（本文件），公开方法 → planCall → 根 Client doRequest（withSign=true，
// activation-sign-v1 凭证族）→ 传输层 switch 双侧 case（HTTP 直发 / gRPC 映射为同形信封）。
// 信封为 {code:0,msg:"ok",data:…} / {code:"业务码",msg,detail}，统一经 core.ParseEnvelope 解析——
// 业务拒绝返回 *apis.Error（errors.As 断言），传输/网络错误原样返回，不伪装业务码。
//
// 金额语义：线上一律 float64 元（服务端内部为万分，边界换算由服务端负责）。
// 幂等键：下单幂等键 = requestId（显式传入优先，缺省经 core.ResolveRequestID 自动生成 req_ 前缀键
// 并在结果中回显供对账），取消幂等键 = 订单号；均经 X-Request-Id 头 / metadata x-request-id 透传，
// 不进任何签名 canonical。

// 套餐计费周期（与 licen-hub 服务端逐字一致）
const (
	// PlanBillingMonthly - 包月
	PlanBillingMonthly = "monthly"
	// PlanBillingYearly - 包年
	PlanBillingYearly = "yearly"
	// PlanBillingBuyout - 买断
	PlanBillingBuyout = "buyout"
)

// 套餐订单类型（由服务端按当前套餐自动判定）
const (
	// PlanOrderTypePurchase - 新购（当前无付费套餐）
	PlanOrderTypePurchase = "purchase"
	// PlanOrderTypeUpgrade - 升级（目标套餐档位高于当前）
	PlanOrderTypeUpgrade = "upgrade"
	// PlanOrderTypeRenew - 续费（同套餐同周期）
	PlanOrderTypeRenew = "renew"
)

// 套餐订单状态机
const (
	// PlanOrderStatusPending - 待支付（仅该状态可取消）
	PlanOrderStatusPending = "pending"
	// PlanOrderStatusPaid - 已支付（套餐权益经信封重签生效）
	PlanOrderStatusPaid = "paid"
	// PlanOrderStatusCancelled - 已取消（仅 pending 可流转到此态）
	PlanOrderStatusCancelled = "cancelled"
	// PlanOrderStatusClosed - 已关闭（超期未支付由服务端关单）
	PlanOrderStatusClosed = "closed"
)

// ErrPlanNotActivated - 套餐运行面未激活闸门：根 Client 未完成激活（无 activation token）
// 或授权状态非放行态时，PlansCatalog/PlanOrder* 直接返回本错误（不发请求）；
// 服务端的凭证校验仍是最终边界。
var ErrPlanNotActivated = errors.New("plan: 许可证未激活或授权状态不可用")

// PlanCatalogOption - 套餐计费选项
type PlanCatalogOption struct {
	// Billing - 计费周期（PlanBillingMonthly / PlanBillingYearly / PlanBillingBuyout）
	Billing string `json:"billing"`
	// Price - 标价（元）
	Price float64 `json:"price"`
	// SalePrice - 生效价（元，已算入生效折扣/创始价；下单金额以服务端结算为准）
	SalePrice float64 `json:"salePrice"`
	// DiscountTag - 折扣标签（如 "8折·试用期内"，无折扣为空串）
	DiscountTag string `json:"discountTag"`
	// FounderLeft - 创始价剩余名额（-1 = 该选项非创始价）
	FounderLeft int `json:"founderLeft"`
}

// PlanCatalogItem - 在售套餐目录项
type PlanCatalogItem struct {
	// PlanCode - 套餐编码（下单入参）
	PlanCode string `json:"planCode"`
	// PlanName - 套餐名称
	PlanName string `json:"planName"`
	// Grade - 档位权重（0-1000，升降级判定依据）
	Grade int `json:"grade"`
	// Badge - 营销角标（如 "推荐"）
	Badge string `json:"badge"`
	// Summary - 一句话卖点
	Summary string `json:"summary"`
	// Entitlements - 统一权益表（模块编码 → bool / 数值，与许可证载荷 entitlements 同语义）
	Entitlements map[string]any `json:"entitlements"`
	// Options - 计费选项（仅含在售周期；下架周期不出现）
	Options []PlanCatalogOption `json:"options"`
}

// PlanCurrentSnapshot - 当前生效付费套餐快照
type PlanCurrentSnapshot struct {
	// PlanCode - 套餐编码
	PlanCode string `json:"planCode"`
	// PlanName - 套餐名称
	PlanName string `json:"planName"`
	// Billing - 计费周期
	Billing string `json:"billing"`
	// ValidUntil - 套餐有效期截止（RFC3339，与许可证载荷期限字段同格式）
	ValidUntil string `json:"validUntil"`
}

// PlansCatalogResult - 在售套餐目录回执
type PlansCatalogResult struct {
	// Plans - 在售套餐列表（按 sort/grade 服务端排序原样返回）
	Plans []PlanCatalogItem
	// CurrentPlan - 当前生效付费套餐快照（无付费套餐时为 nil；服务端返回空对象同样归一为 nil）
	CurrentPlan *PlanCurrentSnapshot
}

// plansCatalogData - 目录响应 data（双协议同形：HTTP 为服务端原文，gRPC 为传输层合成）
type plansCatalogData struct {
	Plans       []PlanCatalogItem     `json:"plans"`
	CurrentPlan *PlanCurrentSnapshot  `json:"currentPlan"`
}

// PlansCatalog - 在售套餐目录查询（只读，不带幂等键）。
/**
 * @param ctx context.Context - 调用上下文
 * @return PlansCatalogResult - 在售套餐列表 + 当前生效付费套餐快照
 * @example：
 * 	catalog, err := client.PlansCatalog(ctx)
 * 	for _, plan := range catalog.Plans { ... }
 */
func (this *Client) PlansCatalog(ctx context.Context) (PlansCatalogResult, error) {

	data, err := this.planCall(ctx, http.MethodGet, "/api/v1/plans/catalog", nil, "")
	if err != nil {
		return PlansCatalogResult{}, err
	}
	var payload plansCatalogData
	if err = json.Unmarshal(data, &payload); err != nil {
		return PlansCatalogResult{}, fmt.Errorf("plan: catalog 响应解析失败：%w", err)
	}
	// 无付费套餐时服务端可返回空对象或缺省，统一归一为 nil
	if payload.CurrentPlan != nil && payload.CurrentPlan.PlanCode == "" {
		payload.CurrentPlan = nil
	}
	return PlansCatalogResult{Plans: payload.Plans, CurrentPlan: payload.CurrentPlan}, nil
}

// PlanOrderCreateInput - 套餐下单入参
type PlanOrderCreateInput struct {
	// PlanCode - 套餐编码（必填，取自 PlansCatalog 目录项）
	PlanCode string
	// Billing - 计费周期（必填：monthly / yearly / buyout）
	Billing string
	// RequestId - 幂等键（可选；缺省自动生成 req_ 前缀键并在结果中回显。
	// 同一实例同一 requestId 重复下单返回既有订单，不重复建单）
	RequestId string
}

// PlanOrderCreateResult - 套餐下单回执
type PlanOrderCreateResult struct {
	// OrderNo - 订单号（后续 PlanOrderQuery / PlanOrderCancel 使用同一单号）
	OrderNo string
	// Amount - 应付金额（元）
	Amount float64
	// PayUrl - inis-pay 收银台链接（引导客户跳转完成支付）
	PayUrl string
	// ExpiredAt - 收银台过期时间（毫秒；超期未支付服务端先查单再关单）
	ExpiredAt int64
	// RequestId - 实际生效的幂等键（显式传入的原样回显，缺省的为自动生成值；对账依据）
	RequestId string
}

// planOrderCreateData - 下单响应 data
type planOrderCreateData struct {
	OrderNo   string  `json:"orderNo"`
	Amount    float64 `json:"amount"`
	PayUrl    string  `json:"payUrl"`
	ExpiredAt int64   `json:"expiredAt"`
}

// PlanOrderCreate - 套餐下单：创建订单并返回 inis-pay 收银台链接。
// 订单类型（purchase/upgrade/renew）由服务端按当前生效套餐自动判定；幂等键 requestId
// 同时写入请求体与 X-Request-Id 头 / metadata（服务端防呆：两者不一致即 INVALID_ARGUMENT）。
/**
 * @param ctx context.Context - 调用上下文
 * @param input PlanOrderCreateInput - 套餐编码 / 计费周期 / 幂等键
 * @return PlanOrderCreateResult - 订单号 / 金额（元）/ 收银台链接 / 过期时间
 * @example：
 * 	result, err := client.PlanOrderCreate(ctx, licence.PlanOrderCreateInput{PlanCode: "pro", Billing: licence.PlanBillingYearly})
 */
func (this *Client) PlanOrderCreate(ctx context.Context, input PlanOrderCreateInput) (PlanOrderCreateResult, error) {

	planCode := strings.TrimSpace(input.PlanCode)
	if planCode == "" {
		return PlanOrderCreateResult{}, errors.New("plan: 套餐编码不能为空")
	}
	billing := strings.TrimSpace(input.Billing)
	if billing != PlanBillingMonthly && billing != PlanBillingYearly && billing != PlanBillingBuyout {
		return PlanOrderCreateResult{}, errors.New("plan: 计费周期必须为 monthly / yearly / buyout")
	}
	requestId := core.ResolveRequestID(input.RequestId)
	body, err := json.Marshal(map[string]any{"planCode": planCode, "billing": billing, "requestId": requestId})
	if err != nil {
		return PlanOrderCreateResult{}, err
	}
	data, err := this.planCall(ctx, http.MethodPost, "/api/v1/plan-orders/create", body, requestId)
	if err != nil {
		return PlanOrderCreateResult{}, err
	}
	var payload planOrderCreateData
	if err = json.Unmarshal(data, &payload); err != nil {
		return PlanOrderCreateResult{}, fmt.Errorf("plan: create 响应解析失败：%w", err)
	}
	return PlanOrderCreateResult{
		OrderNo: payload.OrderNo, Amount: payload.Amount, PayUrl: payload.PayUrl,
		ExpiredAt: payload.ExpiredAt, RequestId: requestId,
	}, nil
}

// PlanOrder - 套餐订单条目
type PlanOrder struct {
	// OrderNo - 订单号
	OrderNo string `json:"orderNo"`
	// OrderType - 订单类型（purchase / upgrade / renew）
	OrderType string `json:"orderType"`
	// PlanCode - 套餐编码
	PlanCode string `json:"planCode"`
	// PlanName - 套餐名称
	PlanName string `json:"planName"`
	// Billing - 计费周期
	Billing string `json:"billing"`
	// Amount - 金额（元）
	Amount float64 `json:"amount"`
	// Status - 订单状态（pending / paid / cancelled / closed）
	Status string `json:"status"`
	// PayUrl - 收银台链接（仅 pending 有值）
	PayUrl string `json:"payUrl"`
	// PaidAt - 支付时间（毫秒，0 = 未支付）
	PaidAt int64 `json:"paidAt"`
	// CreateAt - 下单时间（毫秒）
	CreateAt int64 `json:"createAt"`
}

// PlanOrderQuery - 查单：按订单号查询本实例订单（支付后轮询用；他实例订单按 NOT_FOUND 模糊处理）。
/**
 * @param ctx context.Context - 调用上下文
 * @param orderNo string - 订单号（PlanOrderCreate 返回值）
 * @return PlanOrder - 订单快照
 * @example：
 * 	order, err := client.PlanOrderQuery(ctx, "PLO-2026-000001")
 */
func (this *Client) PlanOrderQuery(ctx context.Context, orderNo string) (PlanOrder, error) {

	orderNo = strings.TrimSpace(orderNo)
	if orderNo == "" {
		return PlanOrder{}, errors.New("plan: 订单号不能为空")
	}
	body, err := json.Marshal(map[string]any{"orderNo": orderNo})
	if err != nil {
		return PlanOrder{}, err
	}
	data, err := this.planCall(ctx, http.MethodPost, "/api/v1/plan-orders/query", body, "")
	if err != nil {
		return PlanOrder{}, err
	}
	var order PlanOrder
	if err = json.Unmarshal(data, &order); err != nil {
		return PlanOrder{}, fmt.Errorf("plan: query 响应解析失败：%w", err)
	}
	return order, nil
}

// PlanOrderCancel - 取消订单（仅 pending 可取消；非 pending 返回 *apis.Error{INVALID_STATE}）。
// 幂等键取订单号：重复取消返回既有订单快照，不产生二次副作用。
/**
 * @param ctx context.Context - 调用上下文
 * @param orderNo string - 订单号（必须指向既有的本实例订单）
 * @return PlanOrder - 取消后的订单快照（status=cancelled）
 * @example：
 * 	order, err := client.PlanOrderCancel(ctx, "PLO-2026-000001")
 */
func (this *Client) PlanOrderCancel(ctx context.Context, orderNo string) (PlanOrder, error) {

	orderNo = strings.TrimSpace(orderNo)
	if orderNo == "" {
		return PlanOrder{}, errors.New("plan: 订单号不能为空")
	}
	body, err := json.Marshal(map[string]any{"orderNo": orderNo})
	if err != nil {
		return PlanOrder{}, err
	}
	data, err := this.planCall(ctx, http.MethodPost, "/api/v1/plan-orders/cancel", body, orderNo)
	if err != nil {
		return PlanOrder{}, err
	}
	var order PlanOrder
	if err = json.Unmarshal(data, &order); err != nil {
		return PlanOrder{}, fmt.Errorf("plan: cancel 响应解析失败：%w", err)
	}
	return order, nil
}

// PlanOrderRowsResult - 订单分页回执
type PlanOrderRowsResult struct {
	// Records - 本页订单（createAt 倒序）
	Records []PlanOrder
	// Count - 本实例订单总数
	Count int64
	// Page - 当前页码（回显）
	Page int
}

// planOrderRowsData - 订单分页响应 data
type planOrderRowsData struct {
	Records []PlanOrder `json:"records"`
	Count   int64       `json:"count"`
	Page    int         `json:"page"`
}

// PlanOrderRows - 本实例订单分页（只读，不带幂等键；createAt 倒序，每页条数取服务端默认）。
/**
 * @param ctx context.Context - 调用上下文
 * @param page int - 页码（1 起）
 * @return PlanOrderRowsResult - 订单页 + 总数
 * @example：
 * 	rows, err := client.PlanOrderRows(ctx, 1)
 */
func (this *Client) PlanOrderRows(ctx context.Context, page int) (PlanOrderRowsResult, error) {

	if page < 1 {
		return PlanOrderRowsResult{}, errors.New("plan: 页码必须大于 0")
	}
	data, err := this.planCall(ctx, http.MethodGet, "/api/v1/plan-orders/rows?page="+strconv.Itoa(page), nil, "")
	if err != nil {
		return PlanOrderRowsResult{}, err
	}
	var payload planOrderRowsData
	if err = json.Unmarshal(data, &payload); err != nil {
		return PlanOrderRowsResult{}, fmt.Errorf("plan: rows 响应解析失败：%w", err)
	}
	return PlanOrderRowsResult{Records: payload.Records, Count: payload.Count, Page: payload.Page}, nil
}

// planCall - 套餐运行面统一调用出口：未激活闸门 → doRequest（withSign=true + 幂等键注入 context）
// → 信封解析（core.ParseEnvelope，成功返回 data 原文，业务失败返回 *apis.Error）。
// requestId 为空串表示本次调用不带幂等键（只读调用，如 catalog/query/rows）。
func (this *Client) planCall(ctx context.Context, method, path string, body []byte, requestId string) (json.RawMessage, error) {

	this.mu.RLock()
	token := this.state.ActivationToken
	status := this.state.Status
	this.mu.RUnlock()
	if token == "" || !LicenceProtocol.PassThrough(status) {
		return nil, ErrPlanNotActivated
	}
	_, raw, err := this.doRequest(withApisRequestID(ctx, requestId), method, path, body, true)
	if err != nil {
		return nil, err
	}
	return core.ParseEnvelope(raw)
}
