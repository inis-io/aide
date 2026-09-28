package admin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
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
	// name - SDK 方法名（SDK 资源层逐字一致；钱包组经平台级钱包改名后与 gRPC 方法名不同，见 rpc 字段）
	name string
	// service - gRPC 服务短名（如 ApisMarketAdminService）
	service string
	// rpc - gRPC 方法名（full method 尾段，与平台 ApisSpecs.Method 逐字一致，gRPC 假服务端按此注册方法）；
	// 平台钱包升级为平台级钱包后 SDK 方法名破坏性改名，而 gRPC 传输层内部标识（服务名/方法名）不改名，
	// 两者仅钱包组 8 条路由不一致（充值组 5 条已随功能下线删除）；空串表示与 name 相同
	rpc string
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
	// wantBody - 期望出现在请求体中的子串（子集断言；multipart 用例仅 gRPC 侧断言 JSON 原文）
	wantBody []string
	// wantForm - 期望的 multipart 表单文本字段（仅 HTTP 侧断言；非 multipart 用例为 nil）
	wantForm map[string]string
	// wantFile - 期望的文件域内容（仅 HTTP 侧 multipart 用例；空串表示不断言）
	wantFile string
}

// apisPageData - 分页 data（{data,count,page}）
func apisPageData(rows []any, count int, page int) map[string]any {
	return map[string]any{"data": rows, "count": count, "page": page}
}

// rpcName - 用例的 gRPC 方法名（缺省与 SDK 方法名一致；仅钱包组用例显式声明旧名）
func (this apisRouteCase) rpcName() string {

	if this.rpc == "" {
		return this.name
	}
	return this.rpc
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

// apisOfferPlanItemRow - 套餐中心明细行（平台 service/apis.OfferPlanItem 的白名单投影：
// 产品摘要 + 该产品在套餐内的订阅额度/限额与产品按量兜底单价，无 upstreamConfig/uid/version 等内部字段）
func apisOfferPlanItemRow() map[string]any {
	return map[string]any{
		"productId": 3, "productNo": "APD-2026-000003", "capability": "ip-locate",
		"productName": "IP 定位", "summary": "查 IP 归属地",
		"freeDailyQuota": 100, "freeMonthlyQuota": 5000, "trialQuota": 50, "cacheDiscount": 100,
		"meteredPrice": 120, "quota": 10000, "quotaDaily": 0, "quotaWeekly": 0, "quotaMonthly": 0,
		"concurrencyLimit": 10, "qpsLimit": 20,
	}
}

// apisOfferPlanRow - 套餐中心浏览行（平台 service/apis.OfferPlan 的白名单投影：套餐字段 + 产品明细）。
// 商城浏览的两条路由（find 的页元素、take 的整体）共用该形状。
func apisOfferPlanRow() map[string]any {
	return map[string]any{
		"id": 8, "planNo": "PLN-2026-000008", "name": "包月-基础版",
		"billingMode": "subscription", "price": 9900, "period": "monthly", "status": "on_sale",
		"items": []any{apisOfferPlanItemRow()},
	}
}

// apisPlanItemRow - 套餐明细行（ApisPlanItem 全量 json tag，PlanView.Items 的元素；订阅语义）
func apisPlanItemRow() map[string]any {
	return map[string]any{
		"id": 81, "planId": 8, "productId": 3,
		"quota": 10000, "quotaDaily": 0, "quotaWeekly": 0, "quotaMonthly": 0,
		"concurrencyLimit": 10, "qpsLimit": 20,
		"createAt": 1780000000000, "updateAt": 1780000000000, "deleteAt": 0,
	}
}

// apisPlanViewRow - 平台管理视图套餐行（平台 service/apis.PlanView：套餐主表字段 + items 明细）
func apisPlanViewRow() map[string]any {
	return map[string]any{
		"id": 8, "planNo": "PLN-2026-000008", "name": "包月-基础版",
		"billingMode": "subscription", "price": 9900, "period": "monthly", "status": "off_sale", "version": 1,
		"createAt": 1780000000000, "updateAt": 1780000000000, "deleteAt": 0,
		"items": []any{apisPlanItemRow()},
	}
}

// apisLlmChannelItemRow - 渠道模型映射明细行（ApisLlmChannelModel 全量 json tag，LlmChannelView.Items 元素）
func apisLlmChannelItemRow() map[string]any {
	return map[string]any{
		"id": 61, "channelId": 12, "model": "gpt-5", "upstreamModel": "gpt-5-2026",
		"priceInput": 3000, "priceOutput": 12000, "priceCacheRead": 1500, "priceReasoning": 0,
		"rate": 0, "costInput": 2000, "costOutput": 8000, "costCacheRead": 1000, "costReasoning": 0,
		"contextWindow": 256000, "maxOutputTokens": 8192, "status": "enabled",
		"priority": 0, "weight": 0, "uid": 1,
		"createAt": 1780000000000, "updateAt": 1780000000000, "deleteAt": 0,
	}
}

// apisLlmChannelRow - 渠道管理视图行（ApisLlmChannelView = 渠道主表字段平铺 + items 明细；
// apiKey 平台任何读接口不回传，json:"-" 恒缺省，用例行不含该键）
func apisLlmChannelRow() map[string]any {
	return map[string]any{
		"id": 12, "name": "官方中转", "protocol": "openai", "baseUrl": "https://api.example.com/v1",
		"pricingUrl": "https://example.com/pricing", "priority": 10, "weight": 5,
		"qpsLimit": 0, "concurrencyLimit": 0, "status": "enabled", "healthStatus": "up",
		"healthCheckedAt": 1780000000000, "usageProbe": `{"ok":true}`, "usageVerifiedAt": 1780000000000,
		"timeoutMs": 60000, "remark": "主力渠道", "uid": 1,
		"createAt": 1780000000000, "updateAt": 1780000000000, "deleteAt": 0,
		"items": []any{apisLlmChannelItemRow()},
	}
}

// apisLlmModelOfferRow - 在售模型价格表行（ApisLlmModelOffer 7 个白名单键：
// 平台 llmgateway.ListOnSaleModels 聚合 + 处理器白名单投影，只回已定价模型）
func apisLlmModelOfferRow() map[string]any {
	return map[string]any{
		"model": "gpt-5", "contextWindow": 256000, "maxOutputTokens": 8192,
		"priceInput": 3000, "priceOutput": 12000, "priceCacheRead": 1500, "priceReasoning": 0,
	}
}

// apisLlmLogRow - 调用观测明细行（ApisLlmLog 全量 json tag，平台 models/basic.LlmUsageDetail）
func apisLlmLogRow() map[string]any {
	return map[string]any{
		"id": 501, "requestId": "llm-req-501", "userId": 7, "keyId": 21, "model": "gpt-5",
		"channelId": 12, "protocolIn": "openai", "protocolOut": "openai", "converted": false, "stream": true,
		"promptTokens": 1200, "completionTokens": 300, "cachedTokens": 200, "reasoningTokens": 0,
		"estimated": false, "ttftMs": 320, "durationMs": 2100, "upstreamStatus": 200,
		"errorType": "", "amount": 72, "costAmount": 48, "createAt": 1780000005000,
	}
}

// apisKeyRow - sk-key 读视图行（ApisKey 全量 json tag，平台 service/apis.LlmKeyView 白名单投影；
// key_hash 绝不外泄，用例行不含该键）
func apisKeyRow() map[string]any {
	return map[string]any{
		"id": 21, "keyNo": "KEY-2026-000021", "userId": 7, "productId": 5, "name": "本地开发",
		"prefix": "sk-ab12cd3", "allowedModels": []any{"gpt-5"}, "monthlySpendLimit": 10000,
		"expiresAt": 0, "status": "active", "lastUsedAt": 1780000000000, "createAt": 1780000000000,
	}
}

// apisPricingScanData - 定价抓取结果 data（ApisLlmPricingScanResult：url/provider/cached/truncated + items）
func apisPricingScanData() map[string]any {
	return map[string]any{
		"url": "https://example.com/pricing", "provider": "http", "cached": false, "truncated": false,
		"items": []any{map[string]any{
			"model": "gpt-5", "input": 2000, "output": 8000, "cacheRead": 1000, "reasoning": 0,
			"contextWindow": 256000, "maxOutputTokens": 8192,
		}},
	}
}

// apisAdjustUserOptionRow - 人工调账目标用户选项行（WalletAdjustUserOption：仅选择器所需字段）
func apisAdjustUserOptionRow() map[string]any {
	return map[string]any{"id": 7, "account": "member", "nickname": "兔子", "avatar": "/avatar.png"}
}

// apisRouteCases - 73 条路由用例（顺序与平台 ApisSpecs 登记顺序一致，便于逐行对读）
func apisRouteCases() []apisRouteCase {
	autoRenew := false
	return []apisRouteCase{
		// ---------- ApisMarketAdminService（2） ----------
		{
			name: "FindMarketProducts", service: "ApisMarketAdminService",
			method: http.MethodGet, path: "/api/apis-market/find",
			// 页元素是套餐浏览视图（套餐字段 + 产品明细），与平台 service/apis.BrowsePlans 组装形状一致
			data: apisPageData([]any{apisOfferPlanRow()}, 1, 2),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindMarketProducts(context.Background(), &ApisPlanQuery{
					Page: 2, Limit: 10, ProductId: 3, BillingMode: "subscription", Status: "on_sale",
				})
				if err != nil {
					t.Fatalf("在售套餐分页失败: %v", err)
				}
				if page.Count != 1 || page.Page != 2 || len(page.Data) != 1 {
					t.Fatalf("分页结构解析不符: %+v", page)
				}
				offer := page.Data[0]
				if offer.PlanNo != "PLN-2026-000008" || offer.BillingMode != "subscription" || offer.Price != 9900 {
					t.Fatalf("套餐视图解析不符: %+v", offer)
				}
				if len(offer.Items) != 1 || offer.Items[0].ProductId != 3 || offer.Items[0].Quota != 10000 {
					t.Fatalf("套餐明细解析不符: %+v", offer.Items)
				}
			},
			wantQuery: map[string]string{"page": "2", "limit": "10", "productId": "3", "billingMode": "subscription", "status": "on_sale"},
		},
		{
			name: "GetMarketProduct", service: "ApisMarketAdminService",
			method: http.MethodGet, path: "/api/apis-market/take",
			data: apisOfferPlanRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				offer, err := client.Apis.GetMarketProduct(context.Background(), 3)
				if err != nil {
					t.Fatalf("在售套餐详情失败: %v", err)
				}
				if offer.PlanNo != "PLN-2026-000008" || offer.Price != 9900 {
					t.Fatalf("套餐视图解析不符: %+v", offer)
				}
				if len(offer.Items) != 1 || offer.Items[0].Capability != "ip-locate" || offer.Items[0].QpsLimit != 20 {
					t.Fatalf("套餐明细解析不符: %+v", offer.Items)
				}
			},
			wantQuery: map[string]string{"id": "3"},
		},

		// ---------- ApisCatalogAdminService（17） ----------
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
			wantBody: []string{`"id":11`, `"version":2`, `"status":"on_sale"`, `"cacheDiscount":0`},
		},

		// ---------- 能力上游管理（apis-products/upstream*，9） ----------
		{
			name: "GetCapabilityUpstream", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-products/upstream",
			// 上游状态视图：密钥只出掩码；data/cache 按能力有无对应概念可空
			data: map[string]any{
				"capability": "ip-locate",
				"key":        map[string]any{"name": "IP_LOCATE_KEY", "configured": true, "hint": "abcd****"},
				"data":       map[string]any{"source": "external", "path": "/data/ip2region_v4.xdb", "size": 4096, "modTime": 1780000000000, "available": true},
				"cache":      map[string]any{"cacheTtl": "12h", "cacheHours": 12, "dbRefreshDays": 7},
				"extra":      map[string]any{"endpoint": "https://upstream.example.com"},
			},
			invoke: func(t *testing.T, client *AdminClient) {
				status, err := client.Apis.GetCapabilityUpstream(context.Background(), "ip-locate")
				if err != nil {
					t.Fatalf("能力上游状态失败: %v", err)
				}
				if status.Capability != "ip-locate" || !status.Key.Configured || status.Key.Hint != "abcd****" {
					t.Fatalf("上游密钥状态解析不符: %+v", status.Key)
				}
				if status.Data == nil || status.Data.Source != "external" || status.Data.Size != 4096 {
					t.Fatalf("上游数据状态解析不符: %+v", status.Data)
				}
				if status.Cache == nil || status.Cache.CacheHours != 12 || status.Cache.DbRefreshDays != 7 {
					t.Fatalf("上游缓存状态解析不符: %+v", status.Cache)
				}
			},
			wantQuery: map[string]string{"capability": "ip-locate"},
		},
		{
			name: "SetUpstreamKey", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-products/upstream-key",
			data: nil, // 平台 apisOK(nil,...) 空数据
			invoke: func(t *testing.T, client *AdminClient) {
				if err := client.Apis.SetUpstreamKey(context.Background(), ApisUpstreamKeyInput{Capability: "ip-locate", Value: "ak-123456"}); err != nil {
					t.Fatalf("设置上游密钥失败: %v", err)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`, `"value":"ak-123456"`},
		},
		{
			name: "VerifyUpstream", service: "ApisCatalogAdminService",
			method: http.MethodPost, path: "/api/apis-products/upstream-verify",
			data: nil, // 平台 apisOK(nil,...) 空数据
			invoke: func(t *testing.T, client *AdminClient) {
				if err := client.Apis.VerifyUpstream(context.Background(), ApisUpstreamVerifyInput{
					Capability: "ip-locate", Scene: "key", Value: "ak-123456",
				}); err != nil {
					t.Fatalf("验证上游配置失败: %v", err)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`, `"scene":"key"`, `"value":"ak-123456"`},
		},
		{
			name: "ClearUpstreamKey", service: "ApisCatalogAdminService",
			method: http.MethodDelete, path: "/api/apis-products/upstream-key",
			data: nil, // 平台 apisOK(nil,...) 空数据
			invoke: func(t *testing.T, client *AdminClient) {
				if err := client.Apis.ClearUpstreamKey(context.Background(), "ip-locate"); err != nil {
					t.Fatalf("清除上游密钥失败: %v", err)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`},
		},
		{
			name: "UploadUpstreamData", service: "ApisCatalogAdminService",
			method: http.MethodPost, path: "/api/apis-products/upstream-data",
			data: map[string]any{"source": "external", "path": "/data/ip2region_v4.xdb", "size": 9, "modTime": 1780000000000, "available": true},
			invoke: func(t *testing.T, client *AdminClient) {
				status, err := client.Apis.UploadUpstreamData(context.Background(), "ip-locate", "ip2region_v4.xdb", strings.NewReader("xdb-bytes"))
				if err != nil {
					t.Fatalf("上传上游数据失败: %v", err)
				}
				if status.Source != "external" || status.Size != 9 || !status.Available {
					t.Fatalf("上游数据状态解析不符: %+v", status)
				}
			},
			// gRPC 侧走 Upload() early branch：uploadApis 的 JSON base64 请求体；
			// HTTP 侧为 multipart 表单，经 wantForm/wantFile 断言（wantBody 子串不落 multipart 原文）
			wantBody: []string{`"capability":"ip-locate"`, `"filename":"ip2region_v4.xdb"`, `"content":"eGRiLWJ5dGVz"`},
			wantForm: map[string]string{"capability": "ip-locate"},
			wantFile: "xdb-bytes",
		},
		{
			name: "ResetUpstreamData", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-products/upstream-data-reset",
			data: map[string]any{"source": "embedded", "path": "", "size": 0, "modTime": 0, "available": true},
			invoke: func(t *testing.T, client *AdminClient) {
				status, err := client.Apis.ResetUpstreamData(context.Background(), "ip-locate")
				if err != nil {
					t.Fatalf("恢复内嵌默认数据失败: %v", err)
				}
				if status.Source != "embedded" || !status.Available {
					t.Fatalf("恢复结果解析不符: %+v", status)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`},
		},
		{
			name: "SetUpstreamCache", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-products/upstream-cache",
			data: map[string]any{"cacheTtl": "12h", "cacheHours": 12, "dbRefreshDays": 7},
			invoke: func(t *testing.T, client *AdminClient) {
				cache, err := client.Apis.SetUpstreamCache(context.Background(), ApisUpstreamCacheInput{
					Capability: "ip-locate", CacheHours: 12, DbRefreshDays: 7,
				})
				if err != nil {
					t.Fatalf("保存缓存策略失败: %v", err)
				}
				if cache.CacheHours != 12 || cache.DbRefreshDays != 7 {
					t.Fatalf("缓存策略解析不符: %+v", cache)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`, `"cacheHours":12`, `"dbRefreshDays":7`},
		},
		{
			name: "ClearUpstreamCache", service: "ApisCatalogAdminService",
			method: http.MethodDelete, path: "/api/apis-products/upstream-cache",
			data: nil, // 平台 apisOK(nil,...) 空数据
			invoke: func(t *testing.T, client *AdminClient) {
				if err := client.Apis.ClearUpstreamCache(context.Background(), "ip-locate"); err != nil {
					t.Fatalf("清空上游缓存失败: %v", err)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`},
		},
		{
			name: "SetUpstreamOptions", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-products/upstream-options",
			data: map[string]any{"endpoint": "https://upstream.example.com", "timeoutMs": 3000},
			invoke: func(t *testing.T, client *AdminClient) {
				options, err := client.Apis.SetUpstreamOptions(context.Background(), ApisUpstreamOptionsInput{
					Capability: "ip-locate",
					Options:    map[string]any{"endpoint": "https://upstream.example.com", "timeoutMs": 3000},
				})
				if err != nil {
					t.Fatalf("保存上游高级参数失败: %v", err)
				}
				if len(options) != 2 || options["endpoint"] != "https://upstream.example.com" {
					t.Fatalf("高级参数快照解析不符: %+v", options)
				}
			},
			wantBody: []string{`"capability":"ip-locate"`, `"options":{"endpoint":"https://upstream.example.com","timeoutMs":3000}`},
		},
		{
			name: "FindPlans", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-plans/find",
			// 页元素是平台管理视图 PlanView（套餐主表 + items 明细）
			data: apisPageData([]any{apisPlanViewRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindPlans(context.Background(), &ApisPlanQuery{Page: 1, ProductId: 3, BillingMode: "subscription"})
				if err != nil {
					t.Fatalf("套餐分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].BillingMode != "subscription" || page.Data[0].Status != "off_sale" {
					t.Fatalf("套餐解析不符: %+v", page.Data)
				}
				if len(page.Data[0].Items) != 1 || page.Data[0].Items[0].ProductId != 3 || page.Data[0].Items[0].Quota != 10000 {
					t.Fatalf("套餐明细解析不符: %+v", page.Data[0].Items)
				}
			},
			wantQuery: map[string]string{"page": "1", "productId": "3", "billingMode": "subscription"},
		},
		{
			name: "GetPlan", service: "ApisCatalogAdminService",
			method: http.MethodGet, path: "/api/apis-plans/take",
			data: apisPlanViewRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetPlan(context.Background(), 8)
				if err != nil {
					t.Fatalf("套餐详情失败: %v", err)
				}
				if row.PlanNo != "PLN-2026-000008" || len(row.Items) != 1 || row.Items[0].Quota != 10000 {
					t.Fatalf("套餐详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "8"},
		},
		{
			name: "CreatePlan", service: "ApisCatalogAdminService",
			method: http.MethodPost, path: "/api/apis-plans/create",
			data: map[string]any{"id": 12, "planNo": "PLN-2026-000012", "name": "包年-专业版", "status": "off_sale"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.CreatePlan(context.Background(), ApisPlanInput{
					Name: "包年-专业版", BillingMode: "subscription", Price: 99000, Period: "yearly",
					Items: []ApisPlanItemInput{{ProductId: 3, Quota: 200000}},
				})
				if err != nil {
					t.Fatalf("新建套餐失败: %v", err)
				}
				if row.Id != 12 || row.PlanNo != "PLN-2026-000012" {
					t.Fatalf("新建套餐解析不符: %+v", row)
				}
			},
			// 明细随套餐整体提交：未赋值的数字字段仍须上送（平台按入参全量替换）
			wantBody: []string{
				`"name":"包年-专业版"`, `"billingMode":"subscription"`, `"period":"yearly"`,
				`"items":[{"productId":3,"quota":200000,"quotaDaily":0,"quotaWeekly":0,"quotaMonthly":0,"concurrencyLimit":0,"qpsLimit":0}]`,
			},
		},
		{
			name: "UpdatePlan", service: "ApisCatalogAdminService",
			method: http.MethodPut, path: "/api/apis-plans/update",
			data: map[string]any{"id": 12, "planNo": "PLN-2026-000012", "status": "on_sale", "version": 2},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.UpdatePlan(context.Background(), ApisPlanInput{
					Id: 12, Name: "包年-专业版", BillingMode: "subscription", Status: "on_sale", Version: 1,
					Items: []ApisPlanItemInput{{ProductId: 3, Quota: 200000}},
				})
				if err != nil {
					t.Fatalf("修改套餐失败: %v", err)
				}
				if row.Status != "on_sale" || row.Version != 2 {
					t.Fatalf("修改套餐解析不符: %+v", row)
				}
			},
			wantBody: []string{
				`"id":12`, `"version":1`, `"status":"on_sale"`,
				`"items":[{"productId":3,"quota":200000,"quotaDaily":0,"quotaWeekly":0,"quotaMonthly":0,"concurrencyLimit":0,"qpsLimit":0}]`,
			},
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
				"id": 3, "subNo": "SUB-2026-000003", "userId": 7, "planId": 8,
				"orderNo": "APO-2026-000009", "status": "active", "currentPeriodStart": 1780000000000,
				"currentPeriodEnd": 1782600000000, "autoRenew": true, "version": 1,
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
			data: map[string]any{"id": 3, "subNo": "SUB-2026-000003", "status": "active", "planId": 8},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetSubscription(context.Background(), 3)
				if err != nil {
					t.Fatalf("订阅详情失败: %v", err)
				}
				if row.SubNo != "SUB-2026-000003" || row.PlanId != 8 {
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

		// ---------- ApisBalanceAdminService（9；平台级钱包，API 商城为首个消费方；
		// 充值申请/充值审核已整体下线，只保留管理员人工调账 adjust 与调账用户选项） ----------
		{
			name: "GetWallet", rpc: "GetBalance", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/take",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8600, "frozen": 600, "monthlySpendLimit": 50000, "status": "normal", "version": 4},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetWallet(context.Background())
				if err != nil {
					t.Fatalf("我的钱包账户失败: %v", err)
				}
				if row.Balance != 8600 || row.Frozen != 600 || row.MonthlySpendLimit != 50000 {
					t.Fatalf("钱包账户解析不符: %+v", row)
				}
			},
		},
		{
			name: "GetWalletSpent", rpc: "GetBalanceSpent", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/spent",
			data: map[string]any{"monthSpent": 4300},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.GetWalletSpent(context.Background())
				if err != nil {
					t.Fatalf("本月已消费失败: %v", err)
				}
				if result.MonthSpent != 4300 {
					t.Fatalf("本月已消费解析不符: %+v", result)
				}
			},
		},
		{
			name: "FindWalletLogs", rpc: "FindBalanceLogs", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/logs",
			data: apisPageData([]any{map[string]any{
				"id": 31, "logNo": "BTX-2026-000031", "userId": 7, "accountId": 2, "txType": "consume",
				"amount": -400, "balanceAfter": 8600, "requestId": "call-req-31", "refNo": "BILL-2026-000005", "operatorId": 0, "createAt": 1780000003000,
			}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindWalletLogs(context.Background(), &WalletLogQuery{
					Page: 1, TxType: []string{"consume", "hold"}, RefNo: "BILL-2026-000005",
				})
				if err != nil {
					t.Fatalf("钱包流水分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Amount != -400 || page.Data[0].BalanceAfter != 8600 {
					t.Fatalf("钱包流水解析不符: %+v", page.Data)
				}
			},
			wantQuery:      map[string]string{"page": "1", "refNo": "BILL-2026-000005"},
			wantMultiQuery: map[string][]string{"txType[]": {"consume", "hold"}},
		},
		{
			name: "SetWalletLimit", rpc: "SetBalanceLimit", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/wallet-accounts/limit",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8600, "monthlySpendLimit": 50000, "status": "normal"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SetWalletLimit(context.Background(), 7, 50000)
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
			name: "AdjustWallet", rpc: "AdjustBalance", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/wallet-accounts/adjust",
			data: map[string]any{
				"userId": 7, "accountId": 2, "balance": 8100, "frozen": 0, "available": 8100,
				"version": 5, "requestId": "adj-req-1", "logNo": "BTX-2026-000032", "replayed": false,
			},
			invoke: func(t *testing.T, client *AdminClient) {
				state, err := client.Apis.AdjustWallet(context.Background(), WalletAdjustInput{
					UserId: 7, Amount: -500, RequestId: "adj-req-1", Reason: "误计费回退",
				})
				if err != nil {
					t.Fatalf("平台调整钱包余额失败: %v", err)
				}
				if state.Balance != 8100 || state.Available != 8100 || state.LogNo != "BTX-2026-000032" {
					t.Fatalf("调整结果解析不符: %+v", state)
				}
			},
			// 负数必须原样上送（扣减不得被平台参数绑定器丢负号——由调用方保真传输）
			wantBody: []string{`"userId":7`, `"amount":-500`, `"requestId":"adj-req-1"`, `"reason":"误计费回退"`},
		},
		{
			name: "SetWalletStatus", rpc: "SetBalanceStatus", service: "ApisBalanceAdminService",
			method: http.MethodPost, path: "/api/wallet-accounts/status",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "frozen"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SetWalletStatus(context.Background(), 7, "frozen", "异常调用")
				if err != nil {
					t.Fatalf("钱包账户风控状态流转失败: %v", err)
				}
				if row.Status != "frozen" {
					t.Fatalf("状态流转结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"userId":7`, `"status":"frozen"`, `"reason":"异常调用"`},
		},
		{
			name: "FindManageWallets", rpc: "FindManageBalances", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/manage/find",
			data: apisPageData([]any{map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "frozen"}}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindManageWallets(context.Background(), &WalletAccountQuery{Page: 1, UserId: 7, Status: "frozen"})
				if err != nil {
					t.Fatalf("钱包账户运营分页失败: %v", err)
				}
				if len(page.Data) != 1 || page.Data[0].Status != "frozen" {
					t.Fatalf("钱包账户运营分页解析不符: %+v", page.Data)
				}
			},
			wantQuery: map[string]string{"page": "1", "userId": "7", "status": "frozen"},
		},
		{
			name: "GetManageWallet", rpc: "GetManageBalance", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/manage/take",
			data: map[string]any{"id": 2, "userId": 7, "balance": 8100, "status": "normal"},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetManageWallet(context.Background(), 7)
				if err != nil {
					t.Fatalf("钱包账户运营详情失败: %v", err)
				}
				if row.UserId != 7 || row.Balance != 8100 {
					t.Fatalf("钱包账户运营详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"userId": "7"},
		},
		{
			// 人工调账用户搜索选择器：SDK 方法名与 gRPC 方法名逐字一致（不经钱包组 rpc 改名映射）
			name: "FindAdjustUserOptions", service: "ApisBalanceAdminService",
			method: http.MethodGet, path: "/api/wallet-accounts/user-options",
			data: []any{apisAdjustUserOptionRow()},
			invoke: func(t *testing.T, client *AdminClient) {
				rows, err := client.Apis.FindAdjustUserOptions(context.Background(), &WalletAdjustUserQuery{Keyword: "兔子", Limit: 20})
				if err != nil {
					t.Fatalf("调账用户选项失败: %v", err)
				}
				if rows == nil || len(*rows) != 1 || (*rows)[0].Id != 7 || (*rows)[0].Account != "member" {
					t.Fatalf("调账用户选项解析不符: %+v", rows)
				}
			},
			wantQuery: map[string]string{"keyword": "兔子", "limit": "20"},
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
				page, err := client.Apis.FindMonitorBalanceLogs(context.Background(), &WalletLogQuery{
					Page: 1, UserId: 7, TxType: []string{"adjust"},
				})
				if err != nil {
					t.Fatalf("钱包流水监控分页失败: %v", err)
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

		// ---------- ApisLlmChannelAdminService（9；LLM 统一网关上游渠道，设计 09 §6.3） ----------
		{
			name: "FindLlmChannels", service: "ApisLlmChannelAdminService",
			method: http.MethodGet, path: "/api/apis-llm-channels/find",
			// 页元素是渠道管理视图（主表字段平铺 + items 映射明细），apiKey 恒缺省
			data: apisPageData([]any{apisLlmChannelRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindLlmChannels(context.Background(), &ApisLlmChannelQuery{
					Page: 1, Status: "enabled", Protocol: "openai", Keyword: "中转",
				})
				if err != nil {
					t.Fatalf("渠道分页失败: %v", err)
				}
				if page.Count != 1 || len(page.Data) != 1 || page.Data[0].Id != 12 || page.Data[0].Protocol != "openai" {
					t.Fatalf("渠道分页解析不符: %+v", page.Data)
				}
				if page.Data[0].ApiKey != "" || len(page.Data[0].Items) != 1 || page.Data[0].Items[0].Model != "gpt-5" {
					t.Fatalf("渠道明细解析不符: %+v", page.Data[0])
				}
			},
			wantQuery: map[string]string{"page": "1", "status": "enabled", "protocol": "openai", "keyword": "中转"},
		},
		{
			name: "GetLlmChannel", service: "ApisLlmChannelAdminService",
			method: http.MethodGet, path: "/api/apis-llm-channels/take",
			data: apisLlmChannelRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetLlmChannel(context.Background(), 12)
				if err != nil {
					t.Fatalf("渠道详情失败: %v", err)
				}
				if row.Id != 12 || row.Name != "官方中转" || row.UsageVerifiedAt != 1780000000000 {
					t.Fatalf("渠道详情解析不符: %+v", row)
				}
				if len(row.Items) != 1 || row.Items[0].PriceOutput != 12000 || row.Items[0].Rate != 0 {
					t.Fatalf("渠道映射明细解析不符: %+v", row.Items)
				}
			},
			wantQuery: map[string]string{"id": "12"},
		},
		{
			name: "RowsLlmChannels", service: "ApisLlmChannelAdminService",
			method: http.MethodGet, path: "/api/apis-llm-channels/rows",
			// 全量列表（不分页，data 为数组）
			data: []any{apisLlmChannelRow()},
			invoke: func(t *testing.T, client *AdminClient) {
				rows, err := client.Apis.RowsLlmChannels(context.Background(), &ApisLlmChannelQuery{Status: "enabled"})
				if err != nil {
					t.Fatalf("渠道全量失败: %v", err)
				}
				if rows == nil || len(*rows) != 1 || (*rows)[0].Id != 12 || len((*rows)[0].Items) != 1 {
					t.Fatalf("渠道全量解析不符: %+v", rows)
				}
			},
			wantQuery: map[string]string{"status": "enabled"},
		},
		{
			name: "MarketLlmModels", service: "ApisLlmChannelAdminService",
			method: http.MethodGet, path: "/api/apis-llm-channels/market-models",
			// 在售模型价格表：白名单投影（model/报价/规格），无入参
			data: []any{apisLlmModelOfferRow()},
			invoke: func(t *testing.T, client *AdminClient) {
				offers, err := client.Apis.MarketLlmModels(context.Background())
				if err != nil {
					t.Fatalf("在售模型价格表失败: %v", err)
				}
				if offers == nil || len(*offers) != 1 || (*offers)[0].Model != "gpt-5" || (*offers)[0].PriceOutput != 12000 {
					t.Fatalf("在售模型价格表解析不符: %+v", offers)
				}
			},
		},
		{
			name: "SaveLlmChannel", service: "ApisLlmChannelAdminService",
			method: http.MethodPost, path: "/api/apis-llm-channels/save",
			data: apisLlmChannelRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.SaveLlmChannel(context.Background(), ApisLlmChannelInput{
					Id: 12, Name: "官方中转", Protocol: "openai", BaseUrl: "https://api.example.com/v1",
					ApiKey: "", Status: "enabled", TimeoutMs: 60000,
					Items: []ApisLlmChannelItemInput{{
						Model: "gpt-5", UpstreamModel: "gpt-5-2026",
						PriceInput: 3000, PriceOutput: 12000, PriceCacheRead: 1500,
						ContextWindow: 256000, MaxOutputTokens: 8192, Status: "enabled",
					}},
				})
				if err != nil {
					t.Fatalf("保存渠道失败: %v", err)
				}
				if row.Id != 12 || row.Status != "enabled" || len(row.Items) != 1 {
					t.Fatalf("保存渠道解析不符: %+v", row)
				}
			},
			// apiKey 留空 = 不变更（上送空串）；明细随渠道全量替换
			wantBody: []string{
				`"id":12`, `"name":"官方中转"`, `"protocol":"openai"`, `"baseUrl":"https://api.example.com/v1"`,
				`"apiKey":""`, `"status":"enabled"`, `"timeoutMs":60000`,
				`"items":[{`, `"model":"gpt-5"`, `"upstreamModel":"gpt-5-2026"`, `"priceInput":3000`, `"rate":0`,
			},
		},
		{
			name: "VerifyLlmChannelUsage", service: "ApisLlmChannelAdminService",
			method: http.MethodPost, path: "/api/apis-llm-channels/verify-usage",
			// 探测回显为平台动态装配信封（纯候选探测无 id/channel）
			data: map[string]any{"ok": true, "detail": "usage 实测通过", "sample": `{"total_tokens":1500}`, "inputTokens": 1200, "outputTokens": 300, "streamOk": true},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.VerifyLlmChannelUsage(context.Background(), ApisLlmChannelVerifyInput{
					Id: 0, BaseUrl: "https://api.example.com/v1", ApiKey: "ak-candidate", Model: "gpt-5",
				})
				if err != nil {
					t.Fatalf("usage 探测失败: %v", err)
				}
				if result["ok"] != true || result["inputTokens"] != float64(1200) || result["streamOk"] != true {
					t.Fatalf("探测回显解析不符: %+v", result)
				}
			},
			wantBody: []string{`"id":0`, `"baseUrl":"https://api.example.com/v1"`, `"apiKey":"ak-candidate"`, `"model":"gpt-5"`},
		},
		{
			name: "FetchLlmChannelModels", service: "ApisLlmChannelAdminService",
			method: http.MethodPost, path: "/api/apis-llm-channels/models",
			data: map[string]any{"models": []any{map[string]any{"model": "gpt-5", "displayName": "GPT-5"}}},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.FetchLlmChannelModels(context.Background(), ApisLlmChannelVerifyInput{Id: 12})
				if err != nil {
					t.Fatalf("上游模型列表失败: %v", err)
				}
				models, ok := result["models"].([]any)
				if !ok || len(models) != 1 {
					t.Fatalf("模型列表解析不符: %+v", result)
				}
				item := models[0].(map[string]any)
				if item["model"] != "gpt-5" || item["displayName"] != "GPT-5" {
					t.Fatalf("模型列表元素解析不符: %+v", item)
				}
			},
			// id > 0 时其余字段留空取渠道已存值
			wantBody: []string{`"id":12`, `"baseUrl":""`, `"apiKey":""`},
		},
		{
			name: "ScanLlmChannelPricing", service: "ApisLlmChannelAdminService",
			method: http.MethodPost, path: "/api/apis-llm-channels/pricing-scan",
			data: apisPricingScanData(),
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.ScanLlmChannelPricing(context.Background(), ApisLlmPricingScanInput{
					Id: 12, Refresh: true, ExchangeRate: 7.2,
				})
				if err != nil {
					t.Fatalf("定价抓取失败: %v", err)
				}
				if result.Url != "https://example.com/pricing" || result.Provider != "http" || result.Cached {
					t.Fatalf("定价抓取解析不符: %+v", result)
				}
				if len(result.Items) != 1 || result.Items[0].Model != "gpt-5" || result.Items[0].Output != 8000 {
					t.Fatalf("定价条目解析不符: %+v", result.Items)
				}
			},
			wantBody: []string{`"id":12`, `"refresh":true`, `"exchangeRate":7.2`},
		},
		{
			name: "RemoveLlmChannel", service: "ApisLlmChannelAdminService",
			method: http.MethodDelete, path: "/api/apis-llm-channels/remove",
			data: map[string]any{"id": 12},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.RemoveLlmChannel(context.Background(), 12)
				if err != nil {
					t.Fatalf("删除渠道失败: %v", err)
				}
				if result.Id != 12 {
					t.Fatalf("删除渠道结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"id":12`},
		},

		// ---------- ApisLlmLogAdminService（3；LLM 调用观测明细，只读 + 导出） ----------
		{
			name: "FindLlmLogs", service: "ApisLlmLogAdminService",
			method: http.MethodGet, path: "/api/apis-llm-logs/find",
			data: apisPageData([]any{apisLlmLogRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindLlmLogs(context.Background(), &ApisLlmLogQuery{
					Page: 1, Model: "gpt-5", ChannelId: 12, Stream: "true",
				})
				if err != nil {
					t.Fatalf("观测明细分页失败: %v", err)
				}
				if page.Count != 1 || len(page.Data) != 1 || page.Data[0].RequestId != "llm-req-501" {
					t.Fatalf("观测明细分页解析不符: %+v", page.Data)
				}
				if page.Data[0].CostAmount == nil || *page.Data[0].CostAmount != 48 || page.Data[0].CachedTokens != 200 {
					t.Fatalf("观测明细 token 分项解析不符: %+v", page.Data[0])
				}
			},
			wantQuery: map[string]string{"page": "1", "model": "gpt-5", "channelId": "12", "stream": "true"},
		},
		{
			name: "GetLlmLog", service: "ApisLlmLogAdminService",
			method: http.MethodGet, path: "/api/apis-llm-logs/take",
			data: apisLlmLogRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetLlmLog(context.Background(), 501)
				if err != nil {
					t.Fatalf("观测明细详情失败: %v", err)
				}
				if row.Id != 501 || row.Amount != 72 || row.UpstreamStatus != 200 {
					t.Fatalf("观测明细详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "501"},
		},
		{
			name: "ExportLlmLogs", service: "ApisLlmLogAdminService",
			method: http.MethodGet, path: "/api/apis-llm-logs/export",
			// 与账单导出同信封：{fileName, content(base64)}，typed 方法内解码
			data: apisBillExportData("llm-logs-202608.xlsx"),
			invoke: func(t *testing.T, client *AdminClient) {
				fileName, content, err := client.Apis.ExportLlmLogs(context.Background(), &ApisLlmLogQuery{Model: "gpt-5"})
				if err != nil {
					t.Fatalf("观测明细导出失败: %v", err)
				}
				if fileName != "llm-logs-202608.xlsx" || string(content) != "xlsx-bytes" {
					t.Fatalf("观测明细导出结果不符: %s / %q", fileName, content)
				}
			},
			wantQuery: map[string]string{"model": "gpt-5"},
		},

		// ---------- ApisKeyAdminService（5；sk- API Key 生命周期，设计 09 §5.2/§6.4） ----------
		{
			name: "FindApisKeys", service: "ApisKeyAdminService",
			method: http.MethodGet, path: "/api/apis-keys/find",
			data: apisPageData([]any{apisKeyRow()}, 1, 1),
			invoke: func(t *testing.T, client *AdminClient) {
				page, err := client.Apis.FindApisKeys(context.Background(), &ApisKeyQuery{
					Page: 1, Status: "active", Keyword: "本地",
				})
				if err != nil {
					t.Fatalf("sk-key 分页失败: %v", err)
				}
				if page.Count != 1 || len(page.Data) != 1 || page.Data[0].KeyNo != "KEY-2026-000021" {
					t.Fatalf("sk-key 分页解析不符: %+v", page.Data)
				}
				if len(page.Data[0].AllowedModels) != 1 || page.Data[0].AllowedModels[0] != "gpt-5" || page.Data[0].MonthlySpendLimit != 10000 {
					t.Fatalf("sk-key 白名单解析不符: %+v", page.Data[0])
				}
			},
			wantQuery: map[string]string{"page": "1", "status": "active", "keyword": "本地"},
		},
		{
			name: "GetApisKey", service: "ApisKeyAdminService",
			method: http.MethodGet, path: "/api/apis-keys/take",
			data: apisKeyRow(),
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.GetApisKey(context.Background(), 21)
				if err != nil {
					t.Fatalf("sk-key 详情失败: %v", err)
				}
				if row.Id != 21 || row.Prefix != "sk-ab12cd3" || row.Status != "active" {
					t.Fatalf("sk-key 详情解析不符: %+v", row)
				}
			},
			wantQuery: map[string]string{"id": "21"},
		},
		{
			name: "CreateApisKey", service: "ApisKeyAdminService",
			method: http.MethodPost, path: "/api/apis-keys/create",
			// 明文完整 key 仅创建响应返回一次（内嵌读视图 + key）
			data: map[string]any{
				"id": 22, "keyNo": "KEY-2026-000022", "userId": 7, "productId": 5, "name": "CI 流水线",
				"prefix": "sk-ef45gh6", "allowedModels": []any{}, "monthlySpendLimit": 0,
				"expiresAt": 1783000000000, "status": "active", "lastUsedAt": 0, "createAt": 1780000000000,
				"key": "sk-ef45gh6789abcdef0123456789abcdef0123456789abcd",
			},
			invoke: func(t *testing.T, client *AdminClient) {
				created, err := client.Apis.CreateApisKey(context.Background(), ApisKeyInput{
					Name: "CI 流水线", AllowedModels: []string{}, ExpiresAt: 1783000000000,
				})
				if err != nil {
					t.Fatalf("签发 sk-key 失败: %v", err)
				}
				if created.Id != 22 || created.Key != "sk-ef45gh6789abcdef0123456789abcdef0123456789abcd" {
					t.Fatalf("签发结果解析不符: %+v", created)
				}
				if created.Status != "active" || created.ProductId != 5 {
					t.Fatalf("签发读视图解析不符: %+v", created.ApisKey)
				}
			},
			wantBody: []string{`"name":"CI 流水线"`, `"allowedModels":[]`, `"monthlySpendLimit":0`, `"expiresAt":1783000000000`},
		},
		{
			name: "RevokeApisKey", service: "ApisKeyAdminService",
			method: http.MethodPut, path: "/api/apis-keys/revoke",
			data: map[string]any{
				"id": 21, "keyNo": "KEY-2026-000021", "userId": 7, "productId": 5, "name": "本地开发",
				"prefix": "sk-ab12cd3", "allowedModels": []any{"gpt-5"}, "monthlySpendLimit": 10000,
				"expiresAt": 0, "status": "revoked", "lastUsedAt": 1780000000000, "createAt": 1780000000000,
			},
			invoke: func(t *testing.T, client *AdminClient) {
				row, err := client.Apis.RevokeApisKey(context.Background(), 21)
				if err != nil {
					t.Fatalf("吊销 sk-key 失败: %v", err)
				}
				if row.Id != 21 || row.Status != "revoked" {
					t.Fatalf("吊销结果解析不符: %+v", row)
				}
			},
			wantBody: []string{`"id":21`},
		},
		{
			name: "RemoveApisKey", service: "ApisKeyAdminService",
			method: http.MethodDelete, path: "/api/apis-keys/remove",
			data: map[string]any{"id": 21},
			invoke: func(t *testing.T, client *AdminClient) {
				result, err := client.Apis.RemoveApisKey(context.Background(), 21)
				if err != nil {
					t.Fatalf("删除 sk-key 失败: %v", err)
				}
				if result.Id != 21 {
					t.Fatalf("删除 sk-key 结果解析不符: %+v", result)
				}
			},
			wantBody: []string{`"id":21`},
		},
	}
}

// TestApisRoutesHTTP - 73 条商城路由逐条经 HTTP 假平台校验：
// ① 请求打到登记表登记的「动词 + 路径」（未登记即 404，用例直接失败）；
// ② query 按平台约定序列化（标量直写、数组 key[]=v 重复）；
// ③ 写路径请求体字段与平台入参结构逐字对齐（含显式 false / 负数等不可省略的取值）；
// ④ typed 返回值解析（分页、白名单视图、base64 导出解码等）；
// ⑤ multipart 上传（UploadUpstreamData）按表单字段与文件域断言。
func TestApisRoutesHTTP(t *testing.T) {

	cases := apisRouteCases()
	if len(cases) != 73 {
		t.Fatalf("商城路由用例应为 73 条，实际 %d 条", len(cases))
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
			if len(one.wantForm) > 0 || one.wantFile != "" {
				// multipart 用例：JSON 子串不落原文，解析表单后断言（与 artifacts 上传用例同口径）
				if err := hub.lastRequest.ParseMultipartForm(32 << 20); err != nil {
					t.Fatalf("multipart 解析失败: %v", err)
				}
				for key, want := range one.wantForm {
					if got := hub.lastRequest.PostFormValue(key); got != want {
						t.Errorf("表单字段 %s = %q，期望 %q", key, got, want)
					}
				}
				if one.wantFile != "" {
					file, _, err := hub.lastRequest.FormFile("file")
					if err != nil {
						t.Fatalf("文件域缺失: %v", err)
					}
					content, err := io.ReadAll(file)
					if err != nil {
						t.Fatalf("文件域读取失败: %v", err)
					}
					if string(content) != one.wantFile {
						t.Errorf("文件域内容 = %q，期望 %q", string(content), one.wantFile)
					}
				}
				return
			}
			for _, needle := range one.wantBody {
				if !strings.Contains(string(hub.lastBody), needle) {
					t.Errorf("请求体缺少 %q：%s", needle, string(hub.lastBody))
				}
			}
		})
	}
}

// TestApisFindMarketProductsPlatformShape - 商城分页的页元素是平台真实形状（套餐视图 OfferPlan，含产品明细 items）：
// 用平台 `BrowsePlans` 投影结果的 JSON 原文（含 items 为空数组的行）走完整信封链路反序列化，
// 断言套餐字段与 items 真的被解析。
//
// 回归 B1：页元素若被声明成错误类型（曾用嵌套商品视图 `{product, plans}`），反序列化**不报错**，
// 但整行是零值空壳（静默数据丢失）；本用例的 items 空数组分支同时钉住平台「items 恒为数组、不为 null」的契约。
func TestApisFindMarketProductsPlatformShape(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes[http.MethodGet+" /api/apis-market/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeEnvelope(writer, 200, "数据请求成功！", json.RawMessage(`{
			"data": [
				{
					"id": 8, "planNo": "PLN-2026-000008", "name": "包月-基础版",
					"billingMode": "subscription", "price": 9900, "period": "monthly", "status": "on_sale",
					"items": [
						{
							"productId": 3, "productNo": "APD-2026-000003", "capability": "ip-locate",
							"productName": "IP 定位", "summary": "查 IP 归属地",
							"freeDailyQuota": 100, "freeMonthlyQuota": 5000, "trialQuota": 50, "cacheDiscount": 100,
							"meteredPrice": 120, "quota": 10000, "quotaDaily": 0, "quotaWeekly": 0, "quotaMonthly": 0,
							"concurrencyLimit": 10, "qpsLimit": 20
						}
					]
				},
				{
					"id": 9, "planNo": "PLN-2026-000009", "name": "包年-专业版",
					"billingMode": "subscription", "price": 99000, "period": "yearly", "status": "on_sale",
					"items": []
				}
			],
			"count": 2,
			"page": 1
		}`))
	}
	client := hub.newClient(t)

	page, err := client.Apis.FindMarketProducts(context.Background(), &ApisPlanQuery{Page: 1})
	if err != nil {
		t.Fatalf("在售套餐分页失败: %v", err)
	}
	if page.Count != 2 || page.Page != 1 || len(page.Data) != 2 {
		t.Fatalf("分页结构解析不符: %+v", page)
	}

	first := page.Data[0]
	if first.Id != 8 || first.PlanNo != "PLN-2026-000008" || first.BillingMode != "subscription" || first.Price != 9900 {
		t.Fatalf("套餐未按平台形状解析（疑似退回旧商品视图）: %+v", first)
	}
	if len(first.Items) != 1 || first.Items[0].ProductId != 3 || first.Items[0].Capability != "ip-locate" ||
		first.Items[0].Quota != 10000 || first.Items[0].MeteredPrice != 120 {
		t.Fatalf("items 未按平台形状解析: %+v", first.Items)
	}

	// 平台对无明细的套餐输出空数组（BrowsePlans 用 []OfferPlanItem{} 兜底），不是 null
	if page.Data[1].Items == nil || len(page.Data[1].Items) != 0 {
		t.Fatalf("空明细应为非 nil 空切片: %#v", page.Data[1].Items)
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
