package licence

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSaasMenuSaveAndPublish - 菜单清单：保存草稿（Id=0 新建）+ 发布，请求体与 {id,version} 结果解析
func TestSaasMenuSaveAndPublish(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-menus/save"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 5, "version": 2})
	}
	hub.routes["POST /api/saas-menus/publish"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 5, "version": 2, "impactReport": map[string]any{"removedCodes": []string{"legacy"}}})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	saved, err := client.SaasMenus.Save(ctx, SaasMenuSaveInput{
		ProjectId: 11, MenuKind: "tenant", Manifest: `{"menuKind":"tenant","version":2,"menus":[{"code":"root","type":"directory","route":{"path":"/"}}]}`,
	})
	if err != nil {
		t.Fatalf("保存清单失败: %v", err)
	}
	if saved.Id != 5 || saved.Version != 2 {
		t.Fatalf("保存结果解析不符: %+v", saved)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["projectId"] != float64(11) || !strings.Contains(sent["manifest"].(string), `"version":2`) {
		t.Fatalf("保存请求体不符: %s", string(hub.lastBody))
	}

	published, err := client.SaasMenus.Publish(ctx, 5, "tenant")
	if err != nil {
		t.Fatalf("发布清单失败: %v", err)
	}
	if published.Id != 5 || published.Version != 2 || published.ImpactReport == nil || len(published.ImpactReport.RemovedCodes) != 1 {
		t.Fatalf("发布结果解析不符: %+v", published)
	}
	if !strings.Contains(string(hub.lastBody), `"id":5`) || !strings.Contains(string(hub.lastBody), `"menuKind":"tenant"`) {
		t.Fatalf("发布请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasMenuFind - 菜单清单分页：{data,count,page} 解析 + projectId/status 查询参数
func TestSaasMenuFind(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-menus/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 5, "projectId": 11, "version": 2, "status": "published", "manifest": `{"version":2}`},
			},
			"count": 1,
			"page":  1,
		})
	}
	client := hub.newClient(t)

	page, err := client.SaasMenus.Find(context.Background(), &SaasMenuFindParams{ProjectId: 11, MenuKind: "tenant", Status: "published"})
	if err != nil {
		t.Fatalf("清单分页失败: %v", err)
	}
	if page.Count != 1 || len(page.Data) != 1 || page.Data[0].Version != 2 || page.Data[0].Status != "published" {
		t.Fatalf("分页解析不符: %+v", page)
	}
	if hub.lastQuery.Get("projectId") != "11" || hub.lastQuery.Get("menuKind") != "tenant" || hub.lastQuery.Get("status") != "published" {
		t.Fatalf("查询参数不符: %s", hub.lastQuery.Encode())
	}
}

// TestSaasFeatureLifecycle - 功能字典：登记 → 禁用 → 删除（动作请求体均为 {id}）
func TestSaasFeatureLifecycle(t *testing.T) {

	hub := newFakeHub(t)
	respond := func(writer http.ResponseWriter) { hub.writeData(writer, map[string]any{"id": 8}) }
	hub.routes["POST /api/saas-features/save"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		respond(writer)
	}
	hub.routes["POST /api/saas-features/disable"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		respond(writer)
	}
	hub.routes["DELETE /api/saas-features/delete"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		respond(writer)
	}
	client := hub.newClient(t)
	ctx := context.Background()

	created, err := client.SaasFeatures.Save(ctx, SaasFeatureSaveInput{
		ProjectId: 11, FeatureCode: "ai.chat", FeatureName: "AI 对话",
	})
	if err != nil {
		t.Fatalf("登记功能失败: %v", err)
	}
	if created.Id != 8 {
		t.Fatalf("登记结果解析不符: %+v", created)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["featureCode"] != "ai.chat" || sent["featureName"] != "AI 对话" {
		t.Fatalf("登记请求体不符: %s", string(hub.lastBody))
	}

	if err = client.SaasFeatures.Disable(ctx, 8); err != nil {
		t.Fatalf("禁用功能失败: %v", err)
	}
	if !strings.Contains(string(hub.lastBody), `"id":8`) {
		t.Fatalf("禁用请求体不符: %s", string(hub.lastBody))
	}
	if err = client.SaasFeatures.Delete(ctx, 8); err != nil {
		t.Fatalf("删除功能失败: %v", err)
	}
	if !strings.Contains(string(hub.lastBody), `"id":8`) {
		t.Fatalf("删除请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasPlanLifecycle - 套餐：新建（含 features/menuCodes）→ 状态流转 → 删除
func TestSaasPlanLifecycle(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-plans/create"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 21, "planNo": "PLN-2026-000021"})
	}
	hub.routes["POST /api/saas-plans/status"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 21, "status": "enabled"})
	}
	hub.routes["DELETE /api/saas-plans/delete"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 21})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	created, err := client.SaasPlans.Create(ctx, SaasPlanSaveInput{
		ProjectId: 11, PlanCode: "pro", PlanName: "专业版",
		Features: map[string]bool{"ai.chat": true}, Limits: map[string]int64{"max_users": 100},
		MenuCodes: []string{"root", "root.ai"},
	})
	if err != nil {
		t.Fatalf("新建套餐失败: %v", err)
	}
	if created.Id != 21 || created.PlanNo != "PLN-2026-000021" {
		t.Fatalf("新建结果解析不符: %+v", created)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["planCode"] != "pro" || sent["features"].(map[string]any)["ai.chat"] != true {
		t.Fatalf("新建请求体不符: %s", string(hub.lastBody))
	}
	if menus, ok := sent["menuCodes"].([]any); !ok || len(menus) != 2 || menus[1] != "root.ai" {
		t.Fatalf("menuCodes 不符: %s", string(hub.lastBody))
	}

	status, err := client.SaasPlans.Status(ctx, 21, "enabled", "")
	if err != nil {
		t.Fatalf("套餐状态流转失败: %v", err)
	}
	if status.Id != 21 || status.Status != "enabled" {
		t.Fatalf("状态结果解析不符: %+v", status)
	}
	if !strings.Contains(string(hub.lastBody), `"status":"enabled"`) {
		t.Fatalf("状态请求体不符: %s", string(hub.lastBody))
	}

	if err = client.SaasPlans.Delete(ctx, 21); err != nil {
		t.Fatalf("删除套餐失败: %v", err)
	}
	if !strings.Contains(string(hub.lastBody), `"id":21`) {
		t.Fatalf("删除请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasTenantSubscribe - 租户开通申请：member 待审形态 {id=申请单ID, applyNo, tenantId} 解析 + 请求体嵌套字段
func TestSaasTenantSubscribe(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-tenants/subscribe"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 31, "applyNo": "SCA-2026-000031", "tenantId": 41})
	}
	client := hub.newClient(t)

	result, err := client.SaasTenants.Subscribe(context.Background(), SaasTenantSubscribeInput{
		ProjectId: 11, PlanId: 21, TenantCode: "acme", TenantName: "ACME 公司",
		SubscriptionType: "official", Environment: "production", ValidUntil: 1800000000000,
		Contact:   &SaasTenantContact{Name: "张三", Email: "ops@acme.example"},
		Overrides: &SaasOverrides{Features: map[string]bool{"ai.chat": true}},
		Reason:    "客户正式订阅",
	})
	if err != nil {
		t.Fatalf("开通申请失败: %v", err)
	}
	if result.Id != 31 || result.ApplyNo != "SCA-2026-000031" || result.TenantId != 41 || result.AutoApproved {
		t.Fatalf("开通结果解析不符: %+v", result)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["tenantCode"] != "acme" || sent["subscriptionType"] != "official" || sent["environment"] != "production" {
		t.Fatalf("开通请求体不符: %s", string(hub.lastBody))
	}
	overrides, ok := sent["overrides"].(map[string]any)
	if !ok || overrides["features"].(map[string]any)["ai.chat"] != true {
		t.Fatalf("overrides 嵌套字段不符: %s", string(hub.lastBody))
	}
	if contact, ok := sent["contact"].(map[string]any); !ok || contact["email"] != "ops@acme.example" {
		t.Fatalf("contact 嵌套字段不符: %s", string(hub.lastBody))
	}
}

// TestSaasTenantChange - 租户权益变更申请：{id=申请单ID, applyNo} 解析
func TestSaasTenantChange(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-tenants/change"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 32, "applyNo": "SCA-2026-000032"})
	}
	client := hub.newClient(t)

	result, err := client.SaasTenants.Change(context.Background(), SaasTenantChangeInput{
		TenantId: 41, PlanId: 22, SubscriptionType: "official", Environment: "production", Reason: "升级套餐",
	})
	if err != nil {
		t.Fatalf("变更申请失败: %v", err)
	}
	if result.Id != 32 || result.ApplyNo != "SCA-2026-000032" {
		t.Fatalf("变更结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"tenantId":41`) || !strings.Contains(string(hub.lastBody), `"planId":22`) {
		t.Fatalf("变更请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasTenantFindAndPayload - 租户分页 + 授权原文视图
func TestSaasTenantFindAndPayload(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-tenants/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 41, "tenantNo": "TEN-2026-000041", "tenantCode": "acme", "status": "active", "planId": 21},
			},
			"count": 1,
			"page":  1,
		})
	}
	hub.routes["GET /api/saas-tenants/take-payload"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"tenantNo": "TEN-2026-000041", "payload": `{"tenantId":"acme"}`,
			"signature": "ab12", "keyVersion": "license-key-2026-01",
		})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	page, err := client.SaasTenants.Find(ctx, &SaasTenantFindParams{ProjectId: 11, Status: "active", Environment: "production"})
	if err != nil {
		t.Fatalf("租户分页失败: %v", err)
	}
	if page.Count != 1 || page.Data[0].TenantCode != "acme" || page.Data[0].Status != "active" {
		t.Fatalf("分页解析不符: %+v", page)
	}
	query := hub.lastQuery
	if query.Get("projectId") != "11" || query.Get("status") != "active" || query.Get("environment") != "production" {
		t.Fatalf("查询参数不符: %s", query.Encode())
	}

	view, err := client.SaasTenants.TakePayload(ctx, 41)
	if err != nil {
		t.Fatalf("载荷请求失败: %v", err)
	}
	if view.TenantNo != "TEN-2026-000041" || view.KeyVersion != "license-key-2026-01" || !strings.Contains(view.Payload, "acme") {
		t.Fatalf("载荷视图解析不符: %+v", view)
	}
	if hub.lastQuery.Get("id") != "41" {
		t.Fatalf("take-payload 应携带 ?id=41，实际: %s", hub.lastQuery.Encode())
	}
}

// TestSaasTenantTransition - 租户状态机：suspend/resume/revoke 共用 {id,reason} 请求体，返回 {id,status}
func TestSaasTenantTransition(t *testing.T) {

	hub := newFakeHub(t)
	for _, action := range []string{"suspend", "resume", "revoke"} {
		act := action
		hub.routes["POST /api/saas-tenants/"+act] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
			target := map[string]string{"suspend": "suspended", "resume": "active", "revoke": "revoked"}[act]
			hub.writeData(writer, map[string]any{"id": 41, "status": target})
		}
	}
	client := hub.newClient(t)
	ctx := context.Background()

	cases := []struct {
		name   string
		call   func() (*StatusResult, error)
		target string
	}{
		{"暂停", func() (*StatusResult, error) { return client.SaasTenants.Suspend(ctx, 41, "欠费") }, "suspended"},
		{"恢复", func() (*StatusResult, error) { return client.SaasTenants.Resume(ctx, 41, "已补缴") }, "active"},
		{"吊销", func() (*StatusResult, error) { return client.SaasTenants.Revoke(ctx, 41, "违规") }, "revoked"},
	}
	for _, item := range cases {
		result, err := item.call()
		if err != nil {
			t.Fatalf("%s失败: %v", item.name, err)
		}
		if result.Id != 41 || result.Status != item.target {
			t.Fatalf("%s结果解析不符: %+v", item.name, result)
		}
		if !strings.Contains(string(hub.lastBody), `"id":41`) {
			t.Fatalf("%s请求体不符: %s", item.name, string(hub.lastBody))
		}
	}
}

// TestSaasTenantReissue - 租户重签：覆盖字段上送（空值不上送），返回 {id,tenantNo}
func TestSaasTenantReissue(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-tenants/reissue"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 41, "tenantNo": "TEN-2026-000041"})
	}
	client := hub.newClient(t)

	result, err := client.SaasTenants.Reissue(context.Background(), SaasTenantReissueInput{
		Id: 41, Reason: "纠错", ValidUntil: 1900000000000,
		Features: map[string]bool{"ai.chat": false}, MenuCodes: []string{"root"},
	})
	if err != nil {
		t.Fatalf("重签失败: %v", err)
	}
	if result.Id != 41 || result.TenantNo != "TEN-2026-000041" {
		t.Fatalf("重签结果解析不符: %+v", result)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["validUntil"] != 1.9e+12 || sent["features"].(map[string]any)["ai.chat"] != false {
		t.Fatalf("重签请求体不符: %s", string(hub.lastBody))
	}
	// omitempty：未覆盖字段不应上送（空值沿用现载荷）
	if _, ok := sent["environment"]; ok {
		t.Fatalf("未覆盖字段不应上送: %s", string(hub.lastBody))
	}
}

func TestSaasTenantSyncMenus(t *testing.T) {
	hub := newFakeHub(t)
	hub.routes["POST /api/saas-tenants/sync-menus"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"rebased": []int{41}, "unchanged": []int{42}, "count": 1})
	}
	client := hub.newClient(t)
	result, err := client.SaasTenants.SyncMenus(context.Background(), 11, []int{41}, "auto")
	if err != nil {
		t.Fatalf("同步菜单失败: %v", err)
	}
	if result.Count != 1 || len(result.Rebased) != 1 || result.Rebased[0] != 41 || len(result.Unchanged) != 1 || result.Unchanged[0] != 42 {
		t.Fatalf("同步结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"projectId":11`) || !strings.Contains(string(hub.lastBody), `"tenantIds":[41]`) || !strings.Contains(string(hub.lastBody), `"mode":"auto"`) {
		t.Fatalf("同步请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasTenantBatchRenew - 批量续期：{results,summary} 结构解析
func TestSaasTenantBatchRenew(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["POST /api/saas-tenants/batch-renew"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"results": []map[string]any{
				{"tenantId": 41, "tenantNo": "TEN-2026-000041", "result": "submitted", "message": "续期申请已提交，待审批", "applyNo": "SCA-2026-000033"},
				{"tenantId": 42, "tenantNo": "TEN-2026-000042", "result": "skipped", "message": "待审核或已吊销租户不可续期"},
			},
			"summary": map[string]any{"applied": 0, "submitted": 1, "skipped": 1, "failed": 0},
		})
	}
	client := hub.newClient(t)

	result, err := client.SaasTenants.BatchRenew(context.Background(), SaasTenantBatchRenewInput{
		Ids: []int{41, 42}, ValidUntil: 1900000000000, Reason: "年度续期",
	})
	if err != nil {
		t.Fatalf("批量续期失败: %v", err)
	}
	if result.Summary.Submitted != 1 || result.Summary.Skipped != 1 || len(result.Results) != 2 {
		t.Fatalf("续期汇总解析不符: %+v", result.Summary)
	}
	if result.Results[0].Result != "submitted" || result.Results[0].ApplyNo != "SCA-2026-000033" {
		t.Fatalf("逐租户结果解析不符: %+v", result.Results[0])
	}
	if !strings.Contains(string(hub.lastBody), `"ids":[41,42]`) {
		t.Fatalf("续期请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasTenantUsage - 用量历史分页 + 用量水位（limit/value/reportedAt 可空指针）
func TestSaasTenantUsage(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-tenants/usage/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 91, "tenantId": 41, "projectId": 11, "tenantNo": "TEN-2026-000041", "tenantCode": "acme",
					"limitKey": "max_users", "limitValue": 57, "reportedAt": 1750000000000, "hourBucket": 1749999600000},
			},
			"count": 1,
			"page":  1,
		})
	}
	hub.routes["GET /api/saas-tenants/usage/summary"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"tenantId": 41, "tenantNo": "TEN-2026-000041",
			"items": []map[string]any{
				{"limitKey": "max_users", "limit": 100, "value": 57, "reportedAt": 1750000000000},
				{"limitKey": "ai_tokens", "limit": nil, "value": 900, "reportedAt": 1750000000000},
				{"limitKey": "storage", "limit": 1024, "value": nil, "reportedAt": nil},
			},
		})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	page, err := client.SaasTenants.UsageFind(ctx, &SaasTenantUsageFindParams{TenantId: 41, LimitKey: "max_users"})
	if err != nil {
		t.Fatalf("用量历史失败: %v", err)
	}
	if page.Count != 1 || page.Data[0].TenantCode != "acme" || page.Data[0].LimitValue != 57 {
		t.Fatalf("用量历史解析不符: %+v", page)
	}
	if hub.lastQuery.Get("tenantId") != "41" || hub.lastQuery.Get("limitKey") != "max_users" {
		t.Fatalf("用量查询参数不符: %s", hub.lastQuery.Encode())
	}

	summary, err := client.SaasTenants.UsageSummary(ctx, 41)
	if err != nil {
		t.Fatalf("用量水位失败: %v", err)
	}
	if summary.TenantId != 41 || len(summary.Items) != 3 {
		t.Fatalf("水位结构解析不符: %+v", summary)
	}
	// 三个分支：上限+上报值齐全 / 无上限 / 未上报
	if summary.Items[0].Limit == nil || *summary.Items[0].Limit != 100 || summary.Items[0].Value == nil || *summary.Items[0].Value != 57 {
		t.Fatalf("水位项（齐全）解析不符: %+v", summary.Items[0])
	}
	if summary.Items[1].Limit != nil || summary.Items[1].Value == nil {
		t.Fatalf("水位项（无上限）解析不符: %+v", summary.Items[1])
	}
	if summary.Items[2].Limit == nil || summary.Items[2].Value != nil || summary.Items[2].ReportedAt != nil {
		t.Fatalf("水位项（未上报）解析不符: %+v", summary.Items[2])
	}
	if hub.lastQuery.Get("id") != "41" {
		t.Fatalf("usage/summary 应携带 ?id=41，实际: %s", hub.lastQuery.Encode())
	}
}

// TestSaasTenantHistoryExport - 留痕导出：{fileName,content} 解析 + tenantId/projectId 查询参数
func TestSaasTenantHistoryExport(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-tenants/history/export"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"fileName": "saas-tenant-history-20260808-120000.csv",
			"content":  "\xEF\xBB\xBF留痕ID,租户编号\n1,TEN-2026-000041\n",
		})
	}
	client := hub.newClient(t)

	result, err := client.SaasTenants.HistoryExport(context.Background(), 41, 11)
	if err != nil {
		t.Fatalf("留痕导出失败: %v", err)
	}
	if !strings.HasPrefix(result.FileName, "saas-tenant-history-") || !strings.Contains(result.Content, "TEN-2026-000041") {
		t.Fatalf("导出结果解析不符: %+v", result)
	}
	if hub.lastQuery.Get("tenantId") != "41" || hub.lastQuery.Get("projectId") != "11" {
		t.Fatalf("导出查询参数不符: %s", hub.lastQuery.Encode())
	}
}

// TestSaasTenantApplications - 我的申请分页 + 申请详情 + 撤回
func TestSaasTenantApplications(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-tenants/applications/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 31, "applyNo": "SCA-2026-000031", "bizType": "subscribe", "tenantId": 41, "status": "pending"},
			},
			"count": 1,
			"page":  1,
		})
	}
	hub.routes["GET /api/saas-tenants/applications/take"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"id": 31, "applyNo": "SCA-2026-000031", "bizType": "subscribe", "tenantId": 41, "status": "pending",
		})
	}
	hub.routes["POST /api/saas-tenants/cancel"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 31})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	page, err := client.SaasTenants.Applications(ctx, &SaasTenantApplicationFindParams{BizType: "subscribe", Status: "pending"})
	if err != nil {
		t.Fatalf("申请分页失败: %v", err)
	}
	if page.Count != 1 || page.Data[0].ApplyNo != "SCA-2026-000031" || page.Data[0].BizType != "subscribe" {
		t.Fatalf("申请分页解析不符: %+v", page)
	}
	if hub.lastQuery.Get("bizType") != "subscribe" || hub.lastQuery.Get("status") != "pending" {
		t.Fatalf("申请查询参数不符: %s", hub.lastQuery.Encode())
	}

	row, err := client.SaasTenants.ApplicationTake(ctx, 31)
	if err != nil {
		t.Fatalf("申请详情失败: %v", err)
	}
	if row.Id != 31 || row.TenantId != 41 {
		t.Fatalf("申请详情解析不符: %+v", row)
	}

	if err = client.SaasTenants.Cancel(ctx, 31); err != nil {
		t.Fatalf("撤回申请失败: %v", err)
	}
	if !strings.Contains(string(hub.lastBody), `"id":31`) {
		t.Fatalf("撤回请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasReview - 审批队列分页 + 审批通过（返回生效租户编号）
func TestSaasReview(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/saas-review/find"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{
			"data": []map[string]any{
				{"id": 31, "applyNo": "SCA-2026-000031", "bizType": "subscribe", "tenantId": 41, "status": "pending"},
			},
			"count": 1,
			"page":  1,
		})
	}
	hub.routes["POST /api/saas-review/review"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 41, "tenantNo": "TEN-2026-000041", "action": "approve"})
	}
	client := hub.newClient(t)
	ctx := context.Background()

	page, err := client.SaasReview.Find(ctx, &SaasTenantApplicationFindParams{Status: "pending", UserId: 7})
	if err != nil {
		t.Fatalf("审批队列失败: %v", err)
	}
	if page.Count != 1 || page.Data[0].Status != "pending" {
		t.Fatalf("审批队列解析不符: %+v", page)
	}
	if hub.lastQuery.Get("status") != "pending" || hub.lastQuery.Get("userId") != "7" {
		t.Fatalf("队列查询参数不符: %s", hub.lastQuery.Encode())
	}

	result, err := client.SaasReview.Review(ctx, SaasReviewInput{Id: 31, Action: "approve", ReviewNote: "符合准入"})
	if err != nil {
		t.Fatalf("审批失败: %v", err)
	}
	if result.Id != 41 || result.TenantNo != "TEN-2026-000041" || result.Action != "approve" {
		t.Fatalf("审批结果解析不符: %+v", result)
	}
	if !strings.Contains(string(hub.lastBody), `"action":"approve"`) {
		t.Fatalf("审批请求体不符: %s", string(hub.lastBody))
	}
}

// TestProjectModules - 项目功能模块：rows 列表 / create 请求体 / sort 排序
func TestProjectModules(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/project-modules/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, []map[string]any{
			{"id": 61, "projectId": 11, "moduleCode": "report", "moduleName": "报表中心", "sort": 10},
		})
	}
	hub.routes["POST /api/project-modules/create"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, map[string]any{"id": 62})
	}
	hub.routes["PUT /api/project-modules/sort"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, nil)
	}
	client := hub.newClient(t)
	ctx := context.Background()

	rows, err := client.Modules.Rows(ctx, &ProjectModuleFindParams{ProjectId: []int{11}})
	if err != nil {
		t.Fatalf("模块列表失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ModuleCode != "report" || rows[0].Sort != 10 {
		t.Fatalf("模块列表解析不符: %+v", rows)
	}
	if got := hub.lastQuery["projectId[]"]; len(got) != 1 || got[0] != "11" {
		t.Fatalf("数组查询参数不符: %s", hub.lastQuery.Encode())
	}

	created, err := client.Modules.Create(ctx, ProjectModuleInput{
		ProjectId: 11, ModuleCode: "dashboard", ModuleName: "驾驶舱", ParentCode: "", Sort: 20,
	})
	if err != nil {
		t.Fatalf("新增模块失败: %v", err)
	}
	if created.Id != 62 {
		t.Fatalf("新增结果解析不符: %+v", created)
	}
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if sent["moduleCode"] != "dashboard" || sent["projectId"] != float64(11) {
		t.Fatalf("新增请求体不符: %s", string(hub.lastBody))
	}

	if err = client.Modules.Sort(ctx, 62, "up"); err != nil {
		t.Fatalf("修改排序失败: %v", err)
	}
	if !strings.Contains(string(hub.lastBody), `"mode":"up"`) {
		t.Fatalf("排序请求体不符: %s", string(hub.lastBody))
	}
}

// TestSaasOverridesMenusRemoveOnly - overrides 菜单裁剪只保留 remove：
// SDK 侧 SaasOverrideMenus 已无 Add 字段，序列化输出不应包含 add、仅含 remove
func TestSaasOverridesMenusRemoveOnly(t *testing.T) {

	raw, err := json.Marshal(SaasOverrides{Menus: SaasOverrideMenus{Remove: []string{"legacy.menu"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"add"`) {
		t.Fatalf("overrides 序列化不应包含 add 键: %s", raw)
	}
	var decoded struct {
		Menus struct {
			Remove []string `json:"remove"`
		} `json:"menus"`
	}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("overrides 反序列化失败: %v", err)
	}
	if len(decoded.Menus.Remove) != 1 || decoded.Menus.Remove[0] != "legacy.menu" {
		t.Fatalf("remove 序列化不符: %s", raw)
	}
}
