package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// API 商城管理面（ApisResource）路由用例表。
//
// 该表是 HTTP 假平台用例（TestApisRoutesHTTP）与 gRPC bufconn 用例（TestApisRoutesGRPC）的**共用**事实表：
// 同一条 invoke 必须在两种协议下都命中同一业务动作，避免双协议用例各写一套而漂移。
// 表内容与平台登记表（licen-hub backend/grpc/admin/v1/apis.go 的 ApisSpecs）的逐条对账由
// apis_reconcile_test.go 守护，本表只负责「调用 → 请求形态 → 响应解析」的行为断言。

// apisGRPCPackage - 商城 proto-less 服务包名（与 LicenceAdminService 等同包；
// 曾出现 lichenhub 笔误，包名回归由 apis_reconcile_test.go 断言平台登记表与 SDK 常量双侧一致）。
const apisGRPCPackage = "licenhub.licence.v1"

// apisRouteCase - 单条商城路由用例
type apisRouteCase struct {
	// name - SDK 方法名（与平台 ApisSpecs.Method 逐字一致，gRPC 假服务端按此注册方法）
	name string
	// service - gRPC 服务短名（如 ApisMarketAdminService）
	service string
	// method - HTTP 动词；path - 管理面 HTTP 路径（gRPC 侧仅用于定位同一条业务动作）
	method string
	path   string
	// data - 假平台回写的信封 data
	data any
	// invoke - 调用被测 typed 方法（内部断言返回值解析）
	invoke func(t *testing.T, client *AdminClient)
	// wantQuery - 期望的单值 query 参数（子集断言）
	wantQuery map[string]string
	// wantMultiQuery - 期望的重复键 query 参数（数组序列化 key[]=v）
	wantMultiQuery map[string][]string
	// wantBody - 期望出现在请求体中的子串（子集断言）
	wantBody []string
}

// apisPageData - 分页 data（{data,count,page}）
func apisPageData(rows []any, count int, page int) map[string]any {
	return map[string]any{"data": rows, "count": count, "page": page}
}

// apisOrderRow - 订单行（字段覆盖订单模型全量 json tag，用于两类订单路由）
func apisOrderRow() map[string]any {
	return map[string]any{
		"id": 9, "orderNo": "APO-2026-000009", "userId": 7, "orderType": "subscription",
		"planId": 3, "amount": 9900, "status": "pending", "payChannel": "balance",
		"paidAt": 0, "payTxNo": "", "requestId": "req-9", "remark": "备注",
		"reviewNote": "", "refundOrderNo": "", "detail": "", "version": 1,
		"createAt": 1780000000000, "updateAt": 1780000000000, "deleteAt": 0,
	}
}

// apisBillRow - 账单行
func apisBillRow() map[string]any {
	return map[string]any{
		"id": 5, "billNo": "BILL-2026-000005", "userId": 7, "billType": "metered",
		"periodStart": 1780000000000, "periodEnd": 1783000000000, "subId": 3,
		"orderNo": "APO-2026-000009", "idemKey": "metered:7:2026-08", "totalQuantity": 120,
		"totalAmount": 3600, "detail": `[{"capability":"ip-locate","quantity":120}]`,
		"status": "settled", "version": 1, "createAt": 1783000000000, "updateAt": 1783000000000,
	}
}

// apisBillExportData - 账单导出信封 data（content 为 xlsx 的 base64 文本）
func apisBillExportData(fileName string) map[string]any {
	return map[string]any{
		"fileName": fileName,
		"content":  base64.StdEncoding.EncodeToString([]byte("xlsx-bytes")),
	}
}

// apisRouteCases - 53 条路由用例（顺序与平台 ApisSpecs 登记顺序一致，便于逐行对读）
func apisRouteCases() []apisRouteCase {
	autoRenew := false
	return []apisRouteCase{
		// ---------- ApisMarketAdminService（2） ----------
		{
			name: "FindMarketProducts", service: "ApisMarketAdminService",
			method: http.MethodGet, path: "/api/apis-market/find",
			data: apisPageData([]any{map[string]any{
				"id": 3, "productNo": "APD-2026-000003", "capability": "ip-locate", "name": "IP 定位",
				"summary": "查 IP 归属地", "status": "on_sale", "freeDailyQuota": 100,
				"cachePrice": 0, "cacheQuotaRatio": 100, "sort": 1, "tags": "网络", "updateAt": 1780000000000,
			}}, 1, 2),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMarketProducts(context.Background(), &ApisProductQuery{
					Page: 2, Limit: 10, Capability: "ip-locate", Status: "on_sale", Keyword: "IP",
				})
				if err != nil {
					t.Fatalf("在售商品分页失败: %v", err)
				}
				if page.Count != 1 || page.Page != 2 || len(page.Data) != 1 {
					t.Fatalf("分页结构解析不符: %+v", page)
				}
				if page.Data[0].Capability != "ip-locate" || page.Data[0].Status != "on_sale" {
					t.Fatalf("浏览视图解析不符: %+v", page.Data[0])
				}
			},
			wantQuery: map[string]string{"page": "2", "limit": "10", "capability": "ip-locate", "status": "on_sale", "keyword": "IP"},
		},
		{
			name: "GetMarketProduct", service: "ApisMarketAdminService",
			method: http.MethodGet, path: "/api/apis-market/take",
			data: map[string]any{
				"product": map[string]any{"id": 3, "productNo": "APD-2026-000003", "capability": "ip-locate", "status": "on_sale"},
				"plans": []any{map[string]any{
					"id": 8, "planNo": "PLN-2026-000008", "productId": 3, "name": "包月-基础版",
					"billingMode": "subscription", "price": 9900, "period": "monthly", "quota": 10000, "status": "on_sale",
				}},
			},
			invoke: func(t *testing.T, client *AdminClient) {
				offer, err := client.Apis.GetMarketProduct(context.Background(), 3)
				if err != nil {
					t.Fatalf("在售商品详情失败: %v", err)
				}
				if offer.Product.ProductNo != "APD-2026-000003" || len(offer.Plans) != 1 {
					t.Fatalf("商品视图解析不符: %+v", offer)
				}
				if offer.Plans[0].Price != 9900 || offer.Plans[0].BillingMode != "subscription" {
					t.Fatalf("在售套餐解析不符: %+v", offer.Plans[0])
				}
			},
			wantQuery: map[string]string{"id": "3"},
		},

		// ---------- ApisCatalogAdminService（10） ----------
		{
			name: "FindProducts", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-products/find",
			data: apisPageData([]any{map[string]any{
				"id": 3, "productNo": "APD-2026-000003", "capability": "ip-locate", "name": "IP 定位",
				"upstreamConfig": `{"endpoint":"https://upstream"}`, "status": "draft", "freeMonthlyQuota": 5000,
				"trialQuota": 50, "version": 2, "uid": 1, "createAt": 1780000000000, "updateAt": 1780000000000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindProducts(context.Background(), &ApisProductQuery{Page: 1, Limit: 10})
				if err != nil {
					t.Fatalf("产品分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Status != "draft" || page.Data[0].Version != 2 {
					t.Fatalf("管理视图解析不符: %+v", page.Data)
				}
				if page.Data[0].UpstreamConfig == "" || page.Data[0].FreeMonthlyQuota != 5000 {
					t.Fatalf("产品内部字段解析不符: %+v", page.Data[0])
				}
			},
			wantQuery: map[string]string{"page": "1", "limit": "10"},
		},
		{
			name: "GetProduct", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-products/take",
			data: map[string]any{"id": 3, "productNo": "APD-2026-000003", "capability": "ip-locate", "name": "IP 定位", "status": "draft"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetProduct(context.Background(), 3)
				if err != nil {
					t.Fatalf("产品详情失败: %v", err)
				}
				if row.Id != 3 || row.ProductNo != "APD-2026-000003" {
					t.Fatalf("产品详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "3"},
		},
		{
			name: "CreateProduct", service: "ApisCatalogAdminService",
			method: http.MethodPost, path: "/api/apis-products/create",
			data: map[string]any{"id": 11, "productNo": "APD-2026-000011", "name": "邮件代发", "capability": "mail-send", "status": "draft", "version": 1},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CreateProduct(context.Background(), ApisProductInput{
					Capability: "mail-send", Name: "邮件代发", Status: "draft", FreeDailyQuota: 100, CacheQuotaRatio: 100,
				})
				if err != nil {
					t.Fatalf("新建产品失败: %v", err)
				}
				if row.Id != 11 || row.Status != "draft" {
					t.Fatalf("新建产品解析不符: %+v", row)
				}
			},
			wantBody: []string{`"capability":"mail-send"`, `"name":"邮件代发"`, `"freeDailyQuota":100`, `"cacheQuotaRatio":100`},
		},
		{
			name: "UpdateProduct", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-products/update",
			data: map[string]any{"id": 11, "productNo": "APD-2026-000011", "status": "on_sale", "version": 3},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.UpdateProduct(context.Background(), ApisProductInput{
					Id: 11, Capability: "mail-send", Name: "邮件代发", Status: "on_sale", Version: 2,
				})
				if err != nil {
					t.Fatalf("修改产品失败: %v", err)
				}
				if row.Version != 3 || row.Status != "on_sale" {
					t.Fatalf("修改产品解析不符: %+v", row)
				}
			},
			// 全量替换：未赋值的数字字段仍须上送（平台按入参重建整行）
			wantBody: []string{`"id":11`, `"version":2`, `"status":"on_sale"`, `"cacheQuotaRatio":0`},
		},
		{
			name: "RemoveProduct", service: "ApisCatalogAdminService",
			method: http.MethodDelete, path: "/api/apis-products/remove",
			data: map[string]any{"id": 11},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.RemoveProduct(context.Background(), 11)
				if err != nil {
					t.Fatalf("删除产品失败: %v", err)
				}
				if result.Id != 11 {
					t.Fatalf("删除结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"id":11`},
		},
		{
			name: "FindPlans", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-plans/find",
			data: apisPageData([]any{map[string]any{
				"id": 8, "planNo": "PLN-2026-000008", "productId": 3, "name": "按量-标准价",
				"billingMode": "metered", "price": 120, "period": "", "concurrencyLimit": 10, "qpsLimit": 20,
				"status": "off_sale", "version": 1,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindPlans(context.Background(), &ApisPlanQuery{Page: 1, ProductId: 3, BillingMode: "metered"})
				if err != nil {
					t.Fatalf("套餐分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].BillingMode != "metered" || page.Data[0].Price != 120 {
					t.Fatalf("套餐解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "productId": "3", "billingMode": "metered"},
		},
		{
			name: "GetPlan", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-plans/take",
			data: map[string]any{"id": 8, "planNo": "PLN-2026-000008", "status": "off_sale", "quotaMonthly": 30000},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetPlan(context.Background(), 8)
				if err != nil {
					t.Fatalf("套餐详情失败: %v", err)
				}
				if row.PlanNo != "PLN-2026-000008" || row.QuotaMonthly != 30000 {
					t.Fatalf("套餐详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "8"},
		},
		{
			name: "CreatePlan", service: "ApisCatalogAdminService",
			method: http.MethodPost, path: "/api/apis-plans/create",
			data: map[string]any{"id": 12, "planNo": "PLN-2026-000012", "productId": 3, "status": "off_sale"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CreatePlan(context.Background(), ApisPlanInput{
					ProductId: 3, Name: "包年-专业版", BillingMode: "subscription", Price: 99000, Period: "yearly", Quota: 200000,
				})
				if err != nil {
					t.Fatalf("新建套餐失败: %v", err)
				}
				if row.Id != 12 || row.PlanNo != "PLN-2026-000012" {
					t.Fatalf("新建套餐解析不符: %+v", row)
				}
			},
			wantBody: []string{`"productId":3`, `"billingMode":"subscription"`, `"period":"yearly"`, `"quota":200000`},
		},
		{
			name: "UpdatePlan", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-plans/update",
			data: map[string]any{"id": 12, "planNo": "PLN-2026-000012", "status": "on_sale", "version": 2},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.UpdatePlan(context.Background(), ApisPlanInput{
					Id: 12, ProductId: 3, Name: "包年-专业版", BillingMode: "subscription", Status: "on_sale", Version: 1,
				})
				if err != nil {
					t.Fatalf("修改套餐失败: %v", err)
				}
				if row.Status != "on_sale" || row.Version != 2 {
					t.Fatalf("修改套餐解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":12`, `"version":1`, `"status":"on_sale"`},
		},
		{
			name: "RemovePlan", service: "ApisCatalogAdminService",
			method: http.MethodDelete, path: "/api/apis-plans/remove",
			data: map[string]any{"id": 12},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.RemovePlan(context.Background(), 12)
				if err != nil {
					t.Fatalf("删除套餐失败: %v", err)
				}
				if result.Id != 12 {
					t.Fatalf("删除结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"id":12`},
		},

		// ---------- ApisOrderAdminService（11） ----------
		{
			name: "FindOrders", service: "ApisOrderAdminService",
			method: http.MethodGet, path: "/api/apis-orders/find",
			data: apisPageData([]any{apisOrderRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindOrders(context.Background(), &ApisOrderQuery{
					Page: 1, Status: "pending", OrderType: "subscription", PlanId: 3,
					CreateAt: []int64{1780000000000, 1783000000000},
				})
				if err != nil {
					t.Fatalf("订单分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].OrderNo != "APO-2026-000009" || page.Data[0].Amount != 9900 {
					t.Fatalf("订单解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"page": "1", "status": "pending", "orderType": "subscription", "planId": "3"},
			wantMultiQuery: map[string][]string{"createAt[]": {"1780000000000", "1783000000000"}},
		},
		{
			name: "GetOrder", service: "ApisOrderAdminService",
			method: http.MethodGet, path: "/api/apis-orders/take",
			data: apisOrderRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetOrder(context.Background(), 9, "")
				if err != nil {
					t.Fatalf("订单详情失败: %v", err)
				}
				if row.OrderNo != "APO-2026-000009" || row.UserId != 7 {
					t.Fatalf("订单详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "9"},
		},
		{
			name: "CreateOrder", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/create",
			data: apisOrderRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CreateOrder(context.Background(), ApisOrderInput{
					PlanId: 3, PayChannel: "balance", RequestId: "req-9", Remark: "备注", AutoRenew: &autoRenew,
				})
				if err != nil {
					t.Fatalf("下单失败: %v", err)
				}
				if row.Status != "pending" || row.RequestId != "req-9" {
					t.Fatalf("下单结果解析不符: %+v", row)
				}
			},
			// AutoRenew 为 *bool：显式 false 必须原样上送（平台据此在新建订阅时关闭自动续费）
			wantBody: []string{`"planId":3`, `"payChannel":"balance"`, `"requestId":"req-9"`, `"autoRenew":false`},
		},
		{
			name: "PayOrder", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/pay",
			data: map[string]any{"id": 9, "orderNo": "APO-2026-000009", "status": "paid", "payChannel": "balance", "paidAt": 1780000001000},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.PayOrder(context.Background(), ApisOrderTarget{OrderNo: "APO-2026-000009"})
				if err != nil {
					t.Fatalf("余额支付失败: %v", err)
				}
				if row.Status != "paid" || row.PaidAt != 1780000001000 {
					t.Fatalf("支付结果解析不符: %+v", row)
				}
			},
			// id 未提供（0）不上送，单号二选一
			wantBody: []string{`"orderNo":"APO-2026-000009"`},
		},
		{
			name: "CancelOrder", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/cancel",
			data: map[string]any{"id": 9, "orderNo": "APO-2026-000009", "status": "cancelled"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CancelOrder(context.Background(), ApisOrderCancelInput{Id: 9, Reason: "不需要了"})
				if err != nil {
					t.Fatalf("取消订单失败: %v", err)
				}
				if row.Status != "cancelled" {
					t.Fatalf("取消结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":9`, `"reason":"不需要了"`},
		},
		{
			name: "FindManageOrders", service: "ApisOrderAdminService",
			method: http.MethodGet, path: "/api/apis-orders/manage/find",
			data: apisPageData([]any{apisOrderRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindManageOrders(context.Background(), &ApisOrderQuery{Page: 1, UserId: 7, PayChannel: "balance", No: "APO-2026-000009"})
				if err != nil {
					t.Fatalf("订单运营分页失败: %v", err)
				}
				if page.Count != 1 || page.Data[0].PayChannel != "balance" {
					t.Fatalf("运营分页解析不符: %+v", page)
				}
			},
			wantQuery: map[string]string{"userId": "7", "payChannel": "balance", "no": "APO-2026-000009"},
		},
		{
			name: "GetManageOrder", service: "ApisOrderAdminService",
			method: http.MethodGet, path: "/api/apis-orders/manage/take",
			data: apisOrderRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetManageOrder(context.Background(), 0, "APO-2026-000009")
				if err != nil {
					t.Fatalf("订单运营详情失败: %v", err)
				}
				if row.OrderNo != "APO-2026-000009" {
					t.Fatalf("运营详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"orderNo": "APO-2026-000009"},
		},
		{
			name: "ConfirmOrder", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/manage/confirm",
			data: map[string]any{"id": 9, "orderNo": "APO-2026-000009", "status": "paid", "reviewNote": "已收到转账"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.ConfirmOrder(context.Background(), ApisOrderConfirmInput{
					Id: 9, ReviewNote: "已收到转账", PayTxNo: "BANK-001",
				})
				if err != nil {
					t.Fatalf("确认线下收款失败: %v", err)
				}
				if row.Status != "paid" || row.ReviewNote != "已收到转账" {
					t.Fatalf("确认结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":9`, `"reviewNote":"已收到转账"`, `"payTxNo":"BANK-001"`},
		},
		{
			name: "CloseOrder", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/manage/close",
			data: map[string]any{"id": 9, "orderNo": "APO-2026-000009", "status": "closed"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CloseOrder(context.Background(), ApisOrderCancelInput{OrderNo: "APO-2026-000009", Reason: "风控拦截"})
				if err != nil {
					t.Fatalf("关闭订单失败: %v", err)
				}
				if row.Status != "closed" {
					t.Fatalf("关闭结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"orderNo":"APO-2026-000009"`, `"reason":"风控拦截"`},
		},
		{
			name: "ApplyRefund", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/refund-apply",
			data: map[string]any{
				"id": 21, "orderNo": "APO-2026-000021", "orderType": "refund", "status": "pending",
				"refundOrderNo": "APO-2026-000009", "amount": 9900,
			},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.ApplyRefund(context.Background(), ApisRefundApplyInput{
					Id: 9, RequestId: "refund-req-21", Reason: "功能不符预期",
				})
				if err != nil {
					t.Fatalf("申请退款失败: %v", err)
				}
				if row.OrderType != "refund" || row.RefundOrderNo != "APO-2026-000009" {
					t.Fatalf("退款单解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":9`, `"requestId":"refund-req-21"`, `"reason":"功能不符预期"`},
		},
		{
			name: "ReviewRefund", service: "ApisOrderAdminService",
			method: http.MethodPost, path: "/api/apis-orders/manage/refund-review",
			data: map[string]any{"id": 21, "orderNo": "APO-2026-000021", "status": "cancelled", "reviewNote": "不符合退款条件"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.ReviewRefund(context.Background(), ApisRefundReviewInput{
					Id: 21, ReviewNote: "不符合退款条件",
				})
				if err != nil {
					t.Fatalf("退款审批失败: %v", err)
				}
				if row.Status != "cancelled" {
					t.Fatalf("审批结果解析不符: %+v", row)
				}
			},
			// false（驳回）必须显式上送，不得依赖平台零值兜底
			wantBody: []string{`"id":21`, `"approve":false`, `"reviewNote":"不符合退款条件"`},
		},

		// ---------- ApisSubscriptionAdminService（4） ----------
		{
			name: "FindSubscriptions", service: "ApisSubscriptionAdminService",
			method: http.MethodGet, path: "/api/apis-subscriptions/find",
			data: apisPageData([]any{map[string]any{
				"id": 3, "subNo": "SUB-2026-000003", "userId": 7, "planId": 8, "productId": 3,
				"orderNo": "APO-2026-000009", "status": "active", "currentPeriodStart": 1780000000000,
				"currentPeriodEnd": 1782600000000, "autoRenew": true, "periodUsed": 12, "version": 1,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindSubscriptions(context.Background(), &ApisSubscriptionQuery{Page: 1, Status: "active", ProductId: 3})
				if err != nil {
					t.Fatalf("订阅分页失败: %v", err)
				}
				if len(page.Data) != 1 || !page.Data[0].AutoRenew || page.Data[0].SubNo != "SUB-2026-000003" {
					t.Fatalf("订阅解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "status": "active", "productId": "3"},
		},
		{
			name: "GetSubscription", service: "ApisSubscriptionAdminService",
			method: http.MethodGet, path: "/api/apis-subscriptions/take",
			data: map[string]any{"id": 3, "subNo": "SUB-2026-000003", "status": "active", "periodUsed": 12},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetSubscription(context.Background(), 3)
				if err != nil {
					t.Fatalf("订阅详情失败: %v", err)
				}
				if row.SubNo != "SUB-2026-000003" || row.PeriodUsed != 12 {
					t.Fatalf("订阅详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "3"},
		},
		{
			name: "CancelSubscription", service: "ApisSubscriptionAdminService",
			method: http.MethodPost, path: "/api/apis-subscriptions/cancel",
			data: map[string]any{"id": 3, "subNo": "SUB-2026-000003", "status": "cancelled", "autoRenew": false, "cancelReason": "改换方案"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CancelSubscription(context.Background(), 3, "改换方案")
				if err != nil {
					t.Fatalf("退订失败: %v", err)
				}
				if row.Status != "cancelled" || row.CancelReason != "改换方案" {
					t.Fatalf("退订结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":3`, `"reason":"改换方案"`},
		},
		{
			name: "SetSubscriptionAutoRenew", service: "ApisSubscriptionAdminService",
			method: http.MethodPost, path: "/api/apis-subscriptions/auto-renew",
			data: map[string]any{"id": 3, "subNo": "SUB-2026-000003", "status": "active", "autoRenew": false},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SetSubscriptionAutoRenew(context.Background(), 3, false)
				if err != nil {
					t.Fatalf("调整自动续费失败: %v", err)
				}
				if row.AutoRenew {
					t.Fatalf("自动续费应被关闭: %+v", row)
				}
			},
			// autoRenew=false 必须上送（平台据此关闭偏好）
			wantBody: []string{`"id":3`, `"autoRenew":false`},
		},

		// ---------- ApisBalanceAdminService（13） ----------
		{
			name: "FindRecharges", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-recharges/find",
			data: apisPageData([]any{map[string]any{
				"id": 4, "rechargeNo": "RCG-2026-000004", "userId": 7, "amount": 100000,
				"payChannel": "offline", "status": "pending", "requestId": "rcg-req-4", "paidAt": 0, "version": 1,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindRecharges(context.Background(), &ApisRechargeQuery{Page: 1, Status: "pending", No: "RCG-2026-000004"})
				if err != nil {
					t.Fatalf("充值单分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Amount != 100000 {
					t.Fatalf("充值单解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "status": "pending", "no": "RCG-2026-000004"},
		},
		{
			name: "GetRecharge", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-recharges/take",
			data: map[string]any{"id": 4, "rechargeNo": "RCG-2026-000004", "status": "pending", "amount": 100000},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetRecharge(context.Background(), 4)
				if err != nil {
					t.Fatalf("充值单详情失败: %v", err)
				}
				if row.RechargeNo != "RCG-2026-000004" || row.Status != "pending" {
					t.Fatalf("充值单详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "4"},
		},
		{
			name: "CreateRecharge", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-recharges/create",
			data: map[string]any{
				"recharge": map[string]any{"id": 4, "rechargeNo": "RCG-2026-000004", "status": "pending", "amount": 100000},
				"replayed": false, "balance": 0, "logNo": "",
			},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.CreateRecharge(context.Background(), ApisRechargeInput{
					Amount: 100000, PayChannel: "offline", RequestId: "rcg-req-4",
				})
				if err != nil {
					t.Fatalf("提交充值申请失败: %v", err)
				}
				if result.Recharge.RechargeNo != "RCG-2026-000004" || result.Replayed {
					t.Fatalf("充值申请结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"amount":100000`, `"payChannel":"offline"`, `"requestId":"rcg-req-4"`},
		},
		{
			name: "CancelRecharge", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-recharges/cancel",
			data: map[string]any{"id": 4, "rechargeNo": "RCG-2026-000004", "status": "cancelled"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CancelRecharge(context.Background(), 4, "已改走其它通道")
				if err != nil {
					t.Fatalf("取消充值单失败: %v", err)
				}
				if row.Status != "cancelled" {
					t.Fatalf("取消结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":4`, `"reason":"已改走其它通道"`},
		},
		{
			name: "ConfirmRecharge", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-recharges/confirm",
			data: map[string]any{
				"recharge": map[string]any{"id": 4, "rechargeNo": "RCG-2026-000004", "status": "paid", "paidAt": 1780000002000},
				"replayed": false, "balance": 100000, "logNo": "BTX-2026-000011",
			},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.ConfirmRecharge(context.Background(), ApisRechargeConfirmInput{
					RechargeNo: "RCG-2026-000004", ReviewNote: "银行到账", PayTxNo: "BANK-002",
				})
				if err != nil {
					t.Fatalf("确认充值入账失败: %v", err)
				}
				if result.Balance != 100000 || result.LogNo != "BTX-2026-000011" {
					t.Fatalf("入账结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"rechargeNo":"RCG-2026-000004"`, `"reviewNote":"银行到账"`, `"payTxNo":"BANK-002"`},
		},
		{
			name: "GetBalance", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-balances/take",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8600, "frozen": 600, "monthlySpendLimit": 50000, "status": "normal", "version": 4},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetBalance(context.Background())
				if err != nil {
					t.Fatalf("我的余额账户失败: %v", err)
				}
				if row.Balance != 8600 || row.Frozen != 600 || row.MonthlySpendLimit != 50000 {
					t.Fatalf("余额账户解析不符: %+v", row)
				}
			},
		},
		{
			name: "GetBalanceSpent", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-balances/spent",
			data: map[string]any{"monthSpent": 4300},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.GetBalanceSpent(context.Background())
				if err != nil {
					t.Fatalf("本月已消费失败: %v", err)
				}
				if result.MonthSpent != 4300 {
					t.Fatalf("本月已消费解析不符: %+v", result)
				}
			},
		},
		{
			name: "FindBalanceLogs", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-balances/logs",
			data: apisPageData([]any{map[string]any{
				"id": 31, "logNo": "BTX-2026-000031", "userId": 7, "accountId": 2, "txType": "consume",
				"amount": -400, "balanceAfter": 8600, "requestId": "call-req-31", "refNo": "BILL-2026-000005", "createAt": 1780000003000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindBalanceLogs(context.Background(), &ApisBalanceLogQuery{
					Page: 1, TxType: []string{"consume", "hold"}, RefNo: "BILL-2026-000005",
				})
				if err != nil {
					t.Fatalf("余额流水分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Amount != -400 || page.Data[0].BalanceAfter != 8600 {
					t.Fatalf("余额流水解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"page": "1", "refNo": "BILL-2026-000005"},
			wantMultiQuery: map[string][]string{"txType[]": {"consume", "hold"}},
		},
		{
			name: "SetBalanceLimit", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-balances/limit",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8600, "monthlySpendLimit": 50000, "status": "normal"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SetBalanceLimit(context.Background(), 7, 50000)
				if err != nil {
					t.Fatalf("设置月度消费限制失败: %v", err)
				}
				if row.MonthlySpendLimit != 50000 || row.UserId != 7 {
					t.Fatalf("限额结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"userId":7`, `"limit":50000`},
		},
		{
			name: "AdjustBalance", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-balances/adjust",
			data: map[string]any{
				"userId": 7, "accountId": 2, "balance": 8100, "frozen": 0, "available": 8100,
				"version": 5, "requestId": "adj-req-1", "logNo": "BTX-2026-000032", "replayed": false,
			},
			invoke: func(t *testing.T, client *AdminClient) {
				state, err := client.Apis.AdjustBalance(context.Background(), ApisBalanceAdjustInput{
					UserId: 7, Amount: -500, RequestId: "adj-req-1", Reason: "误计费回退",
				})
				if err != nil {
					t.Fatalf("平台调整余额失败: %v", err)
				}
				if state.Balance != 8100 || state.Available != 8100 || state.LogNo != "BTX-2026-000032" {
					t.Fatalf("调整结果解析不符: %+v", state)
				}
			},
			// 负数必须原样上送（扣减不得被平台参数绑定器丢负号——由调用方保真传输）
			wantBody: []string{`"userId":7`, `"amount":-500`, `"requestId":"adj-req-1"`, `"reason":"误计费回退"`},
		},
		{
			name: "SetBalanceStatus", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/apis-balances/status",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "frozen"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SetBalanceStatus(context.Background(), 7, "frozen", "异常调用")
				if err != nil {
					t.Fatalf("账户风控状态流转失败: %v", err)
				}
				if row.Status != "frozen" {
					t.Fatalf("状态流转结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"userId":7`, `"status":"frozen"`, `"reason":"异常调用"`},
		},
		{
			name: "FindManageBalances", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-balances/manage/find",
			data: apisPageData([]any{map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "frozen"}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindManageBalances(context.Background(), &ApisAccountQuery{Page: 1, UserId: 7, Status: "frozen"})
				if err != nil {
					t.Fatalf("账户运营分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Status != "frozen" {
					t.Fatalf("账户运营分页解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "userId": "7", "status": "frozen"},
		},
		{
			name: "GetManageBalance", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/apis-balances/manage/take",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "normal"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetManageBalance(context.Background(), 7)
				if err != nil {
					t.Fatalf("账户运营详情失败: %v", err)
				}
				if row.UserId != 7 || row.Balance != 8100 {
					t.Fatalf("账户运营详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"userId": "7"},
		},

		// ---------- ApisUsageAdminService（6） ----------
		{
			name: "FindBills", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-bills/find",
			data: apisPageData([]any{apisBillRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindBills(context.Background(), &ApisBillQuery{Page: 1, BillType: "metered", Status: "settled"})
				if err != nil {
					t.Fatalf("我的账单分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].BillNo != "BILL-2026-000005" || page.Data[0].TotalAmount != 3600 {
					t.Fatalf("账单解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "billType": "metered", "status": "settled"},
		},
		{
			name: "GetBill", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-bills/take",
			data: apisBillRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetBill(context.Background(), 5)
				if err != nil {
					t.Fatalf("账单详情失败: %v", err)
				}
				if row.IdemKey != "metered:7:2026-08" || row.TotalQuantity != 120 {
					t.Fatalf("账单详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "5"},
		},
		{
			name: "ExportBills", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-bills/export",
			data: apisBillExportData("bills-202608.xlsx"),
			invoke: func(t *testing.T, client *AdminClient) {
				fileName, content, err := client.Apis.ExportBills(context.Background(), &ApisBillQuery{UserId: 7})
				if err != nil {
					t.Fatalf("账单导出失败: %v", err)
				}
				if fileName != "bills-202608.xlsx" {
					t.Fatalf("导出文件名不符: %s", fileName)
				}
				// 信封 content 为 base64，typed 方法内部解码后返回原始字节
				if string(content) != "xlsx-bytes" {
					t.Fatalf("导出内容解码不符: %q", content)
				}
			},
			wantQuery: map[string]string{"userId": "7"},
		},
		{
			name: "FindUsageRecords", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-usage/find",
			data: apisPageData([]any{map[string]any{
				"id": 41, "requestId": "call-req-41", "userId": 7, "capability": "mail-send", "productId": 11,
				"activationNo": "ACT-2026-000009", "subId": 3, "quantity": 3, "chargeMode": "subscription_quota",
				"cacheHit": true, "amount": 0, "result": "success", "upstreamMs": 42, "createAt": 1780000004000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindUsageRecords(context.Background(), &ApisUsageRecordQuery{
					Page: 1, Capability: "mail-send", CacheHit: "true", Result: "success",
					CreateAt: []int64{1780000000000, 1783000000000},
				})
				if err != nil {
					t.Fatalf("用量流水分页失败: %v", err)
				}
				if len(page.Data) != 1 || !page.Data[0].CacheHit || page.Data[0].Quantity != 3 {
					t.Fatalf("用量流水解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"capability": "mail-send", "cacheHit": "true", "result": "success"},
			wantMultiQuery: map[string][]string{"createAt[]": {"1780000000000", "1783000000000"}},
		},
		{
			name: "FindUsageDaily", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-usage/daily",
			data: apisPageData([]any{map[string]any{
				"id": 51, "statDate": "2026-08-17", "userId": 7, "capability": "mail-send",
				"chargeMode": "metered_balance", "cacheHit": false, "totalQuantity": 120, "totalAmount": 3600,
				"successCount": 118, "rejectCount": 1, "errorCount": 1, "createAt": 1783000000000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindUsageDaily(context.Background(), &ApisUsageDailyQuery{
					Page: 1, Capability: "mail-send", StatDate: []string{"2026-08-01", "2026-08-17"},
				})
				if err != nil {
					t.Fatalf("用量日聚合分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].TotalQuantity != 120 || page.Data[0].SuccessCount != 118 {
					t.Fatalf("日聚合解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"capability": "mail-send"},
			wantMultiQuery: map[string][]string{"statDate[]": {"2026-08-01", "2026-08-17"}},
		},
		{
			name: "GetUsageDailySummary", service: "ApisUsageAdminService",
			method: http.MethodGet, path: "/api/apis-usage/daily/summary",
			data: map[string]any{
				"range": []any{"2026-08-01", "2026-08-17"},
				"totals": map[string]any{
					"quantity": 120, "amount": 3600, "successCount": 118, "rejectCount": 1, "errorCount": 1,
					"cacheHitQuantity": 20, "cacheMissQuantity": 100,
				},
				"days":         []any{map[string]any{"statDate": "2026-08-17", "quantity": 120, "amount": 3600, "cacheHitQuantity": 20}},
				"capabilities": []any{map[string]any{"key": "mail-send", "quantity": 120, "amount": 3600}},
				"chargeModes":  []any{map[string]any{"key": "metered_balance", "quantity": 120, "amount": 3600}},
				"todayRealtime": []any{
					map[string]any{"key": "mail-send", "quantity": 12, "amount": 0},
				},
			},
			invoke: func(t *testing.T, client *AdminClient) {
				summary, err := client.Apis.GetUsageDailySummary(context.Background(), &ApisUsageDailySummaryQuery{Capability: "mail-send"})
				if err != nil {
					t.Fatalf("用量看板汇总失败: %v", err)
				}
				if summary.Range[0] != "2026-08-01" || summary.Range[1] != "2026-08-17" {
					t.Fatalf("区间解析不符: %+v", summary.Range)
				}
				if summary.Totals.Quantity != 120 || summary.Totals.CacheHitQuantity+summary.Totals.CacheMissQuantity != summary.Totals.Quantity {
					t.Fatalf("合计解析不符: %+v", summary.Totals)
				}
				if len(summary.Days) != 1 || len(summary.Capabilities) != 1 || len(summary.ChargeModes) != 1 {
					t.Fatalf("分布解析不符: %+v", summary)
				}
				if len(summary.TodayRealtime) != 1 || summary.TodayRealtime[0].Key != "mail-send" {
					t.Fatalf("今日实时条带解析不符: %+v", summary.TodayRealtime)
				}
			},
			wantQuery: map[string]string{"capability": "mail-send"},
		},

		// ---------- ApisMonitorAdminService（7） ----------
		{
			name: "FindMonitorBills", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/bills/find",
			data: apisPageData([]any{apisBillRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMonitorBills(context.Background(), &ApisBillQuery{Page: 1, UserId: 7})
				if err != nil {
					t.Fatalf("账单监控分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].UserId != 7 {
					t.Fatalf("监控账单解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "userId": "7"},
		},
		{
			name: "GetMonitorBill", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/bills/take",
			data: apisBillRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetMonitorBill(context.Background(), 5)
				if err != nil {
					t.Fatalf("账单监控详情失败: %v", err)
				}
				if row.BillNo != "BILL-2026-000005" {
					t.Fatalf("监控账单详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "5"},
		},
		{
			name: "ExportMonitorBills", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/bills/export",
			data: apisBillExportData("monitor-bills-202608.xlsx"),
			invoke: func(t *testing.T, client *AdminClient) {
				fileName, content, err := client.Apis.ExportMonitorBills(context.Background(), &ApisBillQuery{BillType: "metered"})
				if err != nil {
					t.Fatalf("账单监控导出失败: %v", err)
				}
				if fileName != "monitor-bills-202608.xlsx" || string(content) != "xlsx-bytes" {
					t.Fatalf("监控导出结果不符: %s / %q", fileName, content)
				}
			},
			wantQuery: map[string]string{"billType": "metered"},
		},
		{
			name: "FindMonitorBalanceLogs", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/logs/find",
			data: apisPageData([]any{map[string]any{
				"id": 31, "logNo": "BTX-2026-000031", "userId": 7, "txType": "adjust", "amount": -500, "balanceAfter": 8100,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMonitorBalanceLogs(context.Background(), &ApisBalanceLogQuery{
					Page: 1, UserId: 7, TxType: []string{"adjust"},
				})
				if err != nil {
					t.Fatalf("余额流水监控分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].TxType != "adjust" || page.Data[0].Amount != -500 {
					t.Fatalf("监控流水解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"page": "1", "userId": "7"},
			wantMultiQuery: map[string][]string{"txType[]": {"adjust"}},
		},
		{
			name: "FindMonitorUsageRecords", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/usage/find",
			data: apisPageData([]any{map[string]any{
				"id": 41, "requestId": "call-req-41", "userId": 7, "capability": "ip-locate", "quantity": 1,
				"chargeMode": "free_quota", "result": "success", "createAt": 1780000004000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMonitorUsageRecords(context.Background(), &ApisUsageRecordQuery{
					Page: 1, UserId: 7, ChargeMode: "free_quota", ActivationNo: "ACT-2026-000009", RequestId: "call-req-41",
				})
				if err != nil {
					t.Fatalf("用量监控流水失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].ChargeMode != "free_quota" {
					t.Fatalf("监控用量流水解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"userId": "7", "chargeMode": "free_quota", "activationNo": "ACT-2026-000009", "requestId": "call-req-41"},
		},
		{
			name: "FindMonitorUsageDaily", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/usage/daily",
			data: apisPageData([]any{map[string]any{
				"id": 51, "statDate": "2026-08-17", "userId": 7, "capability": "ip-locate",
				"chargeMode": "free_quota", "cacheHit": true, "totalQuantity": 80, "totalAmount": 0,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMonitorUsageDaily(context.Background(), &ApisUsageDailyQuery{
					Page: 1, UserId: 7, CacheHit: "false", StatDate: []string{"2026-08-17"},
				})
				if err != nil {
					t.Fatalf("用量监控日聚合失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].StatDate != "2026-08-17" {
					t.Fatalf("监控日聚合解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"userId": "7", "cacheHit": "false"},
			wantMultiQuery: map[string][]string{"statDate[]": {"2026-08-17"}},
		},
		{
			name: "GetMonitorUsageSummary", service: "ApisMonitorAdminService",
			method: http.MethodGet, path: "/api/apis-monitor/usage/summary",
			data: map[string]any{
				"range":  []any{"2026-08-01", "2026-08-17"},
				"totals": map[string]any{"quantity": 120, "amount": 3600},
				"days":   []any{map[string]any{"statDate": "2026-08-17", "quantity": 120, "amount": 3600}},
				// 平台全量视图无今日实时条带（键空间无界，禁止扫键）
				"todayRealtime": nil,
			},
			invoke: func(t *testing.T, client *AdminClient) {
				summary, err := client.Apis.GetMonitorUsageSummary(context.Background(), &ApisUsageDailySummaryQuery{
					UserId: 7, StatDate: []string{"2026-08-01", "2026-08-17"},
				})
				if err != nil {
					t.Fatalf("用量看板汇总（监控）失败: %v", err)
				}
				if summary.Totals.Quantity != 120 || len(summary.Days) != 1 {
					t.Fatalf("监控汇总解析不符: %+v", summary)
				}
				if summary.TodayRealtime != nil {
					t.Fatalf("平台全量视图不应有今日实时条带: %+v", summary.TodayRealtime)
				}
			},
			wantQuery:      map[string]string{"userId": "7"},
			wantMultiQuery: map[string][]string{"statDate[]": {"2026-08-01", "2026-08-17"}},
		},
	}
}

// TestApisRoutesHTTP - 53 条商城路由逐条经 HTTP 假平台校验：
// ① 请求打到登记表登记的「动词 + 路径」（未登记即 404，用例直接失败）；
// ② query 按平台约定序列化（标量直写、数组 key[]=v 重复）；
// ③ 写路径请求体字段与平台入参结构逐字对齐（含显式 false / 负数等不可省略的取值）；
// ④ typed 返回值解析（分页、白名单视图、base64 导出解码等）。
func TestApisRoutesHTTP(t *testing.T) {

	cases := apisRouteCases()
	if len(cases) != 53 {
		t.Fatalf("商城路由用例应为 53 条，实际 %d 条", len(cases))
	}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {

			hub := newFakeHub(t)
			data := one.data
			hub.routes[one.method+" "+one.path] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
				hub.writeData(writer, data)
			}
			client := hub.newClient(t)
			one.invoke(t, client)

			for key, want := range one.wantQuery {
				if got := hub.lastQuery.Get(key); got != want {
					t.Errorf("query %s = %q，期望 %q（%s）", key, got, want, hub.lastQuery.Encode())
				}
			}
			for key, want := range one.wantMultiQuery {
				if got := hub.lastQuery[key]; !slices.Equal(got, want) {
					t.Errorf("query %s = %v，期望 %v（%s）", key, got, want, hub.lastQuery.Encode())
				}
			}
			for _, needle := range one.wantBody {
				if !strings.Contains(string(hub.lastBody), needle) {
					t.Errorf("请求体缺少 %q：%s", needle, string(hub.lastBody))
				}
			}
		})
	}
}

// TestApisExportBillsDecodeError - 导出信封 content 非合法 base64 时报错可定位（不静默返回空内容）
func TestApisExportBillsDecodeError(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes[http.MethodGet+" /api/apis-bills/export"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"fileName": "bills.xlsx", "content": "not-base64!!"})
	}
	client := hub.newClient(t)

	_, _, err := client.Apis.ExportBills(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("非法 base64 应返回解码错误: %v", err)
	}
}

// TestApisBusinessErrorEnvelope - 商城路由的业务错误仍走管理面统一错误分层（*APIError，含结构化 data）
func TestApisBusinessErrorEnvelope(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes[http.MethodPost+" /api/apis-orders/create"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		// 平台业务错误：信封 code=400 + 结构化明细（bizCode/limit/monthSpent）
		hub.writeError(writer, 400, "余额不足！", map[string]any{
			"bizCode": "INSUFFICIENT_BALANCE", "limit": 9900, "monthSpent": 500,
		})
	}
	client := hub.newClient(t)

	_, err := client.Apis.CreateOrder(context.Background(), ApisOrderInput{PlanId: 3, RequestId: "req-1"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 || !strings.Contains(apiErr.Msg, "余额不足") {
		t.Fatalf("业务错误映射不符: %T %v", err, err)
	}
	var detail struct {
		BizCode    string `json:"bizCode"`
		Limit      int64  `json:"limit"`
		MonthSpent int64  `json:"monthSpent"`
	}
	if err = json.Unmarshal(apiErr.Data, &detail); err != nil {
		t.Fatalf("业务错误明细不是 JSON: %v（%s）", err, string(apiErr.Data))
	}
	if detail.BizCode != "INSUFFICIENT_BALANCE" || detail.Limit != 9900 || detail.MonthSpent != 500 {
		t.Fatalf("业务错误明细解析不符: %+v", detail)
	}
}
