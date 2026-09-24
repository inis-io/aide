package apis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// ============================= Invoke - 通用兜底 =============================

// TestInvokeRequestConstruction - Invoke 请求构造：路径/方法/请求体字段，
// ProductId/Quantity 零值省略，幂等键不进消息体（走 Doer 参数）
func TestInvokeRequestConstruction(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"result":{"ok":true},"receipt":{"requestId":"req_x"}}}`)}
	client := New(doer)
	result, receipt, err := client.Invoke(context.Background(), InvokeInput{
		Capability: "mail-send", Action: "send", Params: map[string]any{"to": "user@example.com"},
		RequestId: "req_invoke_1",
	})
	if err != nil {
		t.Fatalf("Invoke 不应报错：%v", err)
	}
	if doer.method != http.MethodPost || doer.path != invokePath {
		t.Fatalf("请求路由不符：%s %s", doer.method, doer.path)
	}
	if doer.requestId != "req_invoke_1" {
		t.Fatalf("显式幂等键应原样透传，实际 %q", doer.requestId)
	}
	body := map[string]any{}
	if err = json.Unmarshal(doer.body, &body); err != nil {
		t.Fatalf("请求体应为 JSON 对象：%v", err)
	}
	if body["capability"] != "mail-send" || body["action"] != "send" {
		t.Fatalf("能力/动作不符：%v", body)
	}
	// 零值省略：productId/quantity 不下发（服务端零值即缺省语义）；requestId 不进消息体
	for _, key := range []string{"productId", "quantity", "requestId"} {
		if _, exists := body[key]; exists {
			t.Fatalf("请求体不应含 %s：%v", key, body)
		}
	}
	if string(result) != `{"ok":true}` || receipt.RequestId != "req_x" {
		t.Fatalf("结果/回执不符：%s %+v", string(result), receipt)
	}
}

// TestInvokeExplicitProductAndQuantity - 非零 ProductId/Quantity 随请求体下发（显式产品选择与计量数声明）
func TestInvokeExplicitProductAndQuantity(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"result":{},"receipt":{}}}`)}
	client := New(doer)
	if _, _, err := client.Invoke(context.Background(), InvokeInput{
		Capability: "ip-locate", ProductId: 12, Quantity: 5,
	}); err != nil {
		t.Fatalf("Invoke 不应报错：%v", err)
	}
	body := map[string]any{}
	if err := json.Unmarshal(doer.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["productId"] != float64(12) || body["quantity"] != float64(5) {
		t.Fatalf("显式产品/计量数应下发：%v", body)
	}
	// 未显式给幂等键：apis 包自动生成 req_ 前缀键并透传
	if !strings.HasPrefix(doer.requestId, "req_") || len(doer.requestId) != 36 {
		t.Fatalf("缺省幂等键应自动生成 req_ + 32 位：%q", doer.requestId)
	}
}

// ============================= IPLocate - typed 便捷方法 =============================

// TestIPLocateResultAlignment - IPLocate 结果：10 个字段与 proto IPLocateResult / 后端 provider 逐字对齐
func TestIPLocateResultAlignment(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{
		"result":{"ip":"203.0.113.10","nation":"中国","province":"浙江省","city":"杭州市","adcode":"330100",
			"rectangle":"119.9,29.9;120.4,30.4","isp":"","source":"amap","cacheHit":true,"stale":false},
		"receipt":{"requestId":"req_loc_1","chargeMode":"subscription_quota","cacheHit":true,"quantity":1,
			"amount":0,"quotaRemaining":9832,"balanceAfter":0,"serverTime":1786000000000}}}`)}
	client := New(doer)
	loc, receipt, err := client.IPLocate(context.Background(), "203.0.113.10", "req_loc_1")
	if err != nil {
		t.Fatalf("IPLocate 不应报错：%v", err)
	}
	if doer.method != http.MethodPost || doer.path != ipLocatePath {
		t.Fatalf("请求路由不符：%s %s", doer.method, doer.path)
	}
	if string(doer.body) != `{"ip":"203.0.113.10"}` {
		t.Fatalf("IPLocate 请求体应仅含 ip：%s", string(doer.body))
	}
	want := IPLocateResult{
		Ip: "203.0.113.10", Nation: "中国", Province: "浙江省", City: "杭州市", Adcode: "330100",
		Rectangle: "119.9,29.9;120.4,30.4", Isp: "", Source: "amap", CacheHit: true, Stale: false,
	}
	if loc != want {
		t.Fatalf("归属地结果不符：%+v", loc)
	}
	if receipt.QuotaRemaining != 9832 || receipt.ChargeMode != "subscription_quota" || receipt.ServerTime != 1786000000000 {
		t.Fatalf("回执不符：%+v", receipt)
	}
}

// ============================= MailSend - typed 便捷方法 =============================

// TestMailSendRequestConstruction - MailSend 请求构造：路径/方法/请求体（to 数组与 html 开关原样下发，
// 能力/动作/计量数不进消息体）、结果三字段与回执解析、数值边界语义（sent 为 int 而非字符串）
func TestMailSendRequestConstruction(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{
		"result":{"sent":2,"recipients":["a@example.com","b@example.com"],"subject":"对账单"},
		"receipt":{"requestId":"req_mail_1","chargeMode":"free_quota","cacheHit":false,"quantity":2,
			"amount":0,"quotaRemaining":48,"balanceAfter":0,"serverTime":1786000000000}}}`)}
	client := New(doer)
	result, receipt, err := client.MailSend(context.Background(), MailSendInput{
		To: []string{"a@example.com", "b@example.com"}, Subject: "对账单",
		Content: "<p>正文</p>", HTML: true,
	}, "req_mail_1")
	if err != nil {
		t.Fatalf("MailSend 不应报错：%v", err)
	}
	if doer.method != http.MethodPost || doer.path != mailSendPath {
		t.Fatalf("请求路由不符：%s %s", doer.method, doer.path)
	}
	if doer.requestId != "req_mail_1" {
		t.Fatalf("显式幂等键应原样透传，实际 %q", doer.requestId)
	}
	// 请求体字段与 JSON 序列化口径：`json.Marshal` 默认把 `<`/`>`/`&` 转义为 \u00XX（服务端 Unmarshal
	// 原样还原，正文语义不变），故这里按转义后的字节比对；能力/动作/计量数/requestId 都不进消息体。
	want := `{"to":["a@example.com","b@example.com"],"subject":"对账单","content":"\u003cp\u003e正文\u003c/p\u003e","html":true}`
	if string(doer.body) != want {
		t.Fatalf("请求体应仅含四个业务参数：%s", string(doer.body))
	}
	expected := MailSendResult{
		Sent: 2, Recipients: []string{"a@example.com", "b@example.com"}, Subject: "对账单",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("代发结果不符：%+v（期望 %+v）", result, expected)
	}
	if receipt.Quantity != 2 || receipt.ChargeMode != "free_quota" || receipt.RequestId != "req_mail_1" {
		t.Fatalf("回执不符：%+v", receipt)
	}
	// 契约字段数守卫：MailSendResult 与 proto MailSendResult 同为 3 字段（新增字段必须同步 proto 与后端出参）
	encoded, err := json.Marshal(MailSendResult{})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("MailSendResult 应为 3 字段，实际 %d：%s", len(fields), string(encoded))
	}
}

// TestMailSendEmptyRecipientsSerialized - to 为空切片时请求体仍带 "to":[]（服务端据此拒绝空收件人，
// 而不是把「未传」与「传了空数组」混为一谈）；html 缺省 false 原样序列化；缺省幂等键自动生成
func TestMailSendEmptyRecipientsSerialized(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"result":{"sent":0},"receipt":{}}}`)}
	client := New(doer)
	if _, _, err := client.MailSend(context.Background(), MailSendInput{
		To: []string{}, Subject: "对账单", Content: "正文",
	}); err != nil {
		t.Fatalf("MailSend 不应报错：%v", err)
	}
	if string(doer.body) != `{"to":[],"subject":"对账单","content":"正文","html":false}` {
		t.Fatalf("空收件人应序列化为空数组：%s", string(doer.body))
	}
	if !strings.HasPrefix(doer.requestId, "req_") || len(doer.requestId) != 36 {
		t.Fatalf("缺省幂等键应自动生成 req_ + 32 位：%q", doer.requestId)
	}
}

// ============================= Usage - 只读自助对账 =============================

// TestUsageQuerySerialization - Usage 走 GET query：标量非零/非空才下发，
// 数组按平台约定序列化为 createAt[]=v 重复键；只读调用不下发调用级幂等键
func TestUsageQuerySerialization(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"records":[{"id":7}],"count":1,"page":2}}`)}
	client := New(doer)
	page, err := client.Usage(context.Background(), UsageQuery{
		Page: 2, Limit: 20, Order: "id desc", Capability: "ip-locate", ProductId: 3,
		ChargeMode: "subscription_quota", Result: "success", CacheHit: "true",
		RequestId: "req_usage_row_1", CreateAt: []int64{1700000000000, 1800000000000},
	})
	if err != nil {
		t.Fatalf("Usage 不应报错：%v", err)
	}
	if doer.method != http.MethodGet || !strings.HasPrefix(doer.path, usagePath+"?") {
		t.Fatalf("Usage 应为 GET + query：%s %s", doer.method, doer.path)
	}
	if doer.requestId != "" {
		t.Fatalf("只读调用不应下发调用级幂等键，实际 %q", doer.requestId)
	}
	if len(doer.body) != 0 {
		t.Fatalf("GET 请求不应有请求体：%s", string(doer.body))
	}
	parsed, err := url.Parse(doer.path)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"page": "2", "limit": "20", "order": "id desc", "capability": "ip-locate",
		"productId": "3", "chargeMode": "subscription_quota", "result": "success",
		"cacheHit": "true", "requestId": "req_usage_row_1",
	} {
		if got := query.Get(key); got != want {
			t.Fatalf("查询参数 %s = %q，期望 %q（%s）", key, got, want, doer.path)
		}
	}
	if got := query["createAt[]"]; len(got) != 2 || got[0] != "1700000000000" || got[1] != "1800000000000" {
		t.Fatalf("createAt 数组应序列化为 createAt[]=v 重复键：%v（%s）", got, doer.path)
	}
	if page.Count != 1 || page.Page != 2 || len(page.Records) != 1 || page.Records[0].Id != 7 {
		t.Fatalf("分页结果不符：%+v", page)
	}
}

// TestUsageEmptyQuery - 全零值查询不带 query 串（服务端默认语义），且 UsageRecord 字段与 proto 对齐
func TestUsageEmptyQuery(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"records":[],"count":0,"page":1}}`)}
	client := New(doer)
	page, err := client.Usage(context.Background(), UsageQuery{})
	if err != nil {
		t.Fatalf("Usage 不应报错：%v", err)
	}
	if doer.path != usagePath {
		t.Fatalf("全零值查询不应带 query 串：%s", doer.path)
	}
	if page.Count != 0 || page.Page != 1 || len(page.Records) != 0 {
		t.Fatalf("空结果不符：%+v", page)
	}
}

// TestUsageRecordFieldAlignment - 流水行 14 个字段与 proto UsageRecord / 平台 ApisUsageRecord JSON 一一对应
func TestUsageRecordFieldAlignment(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"records":[{
		"id":7,"requestId":"req_row_1","userId":9,"capability":"ip-locate","productId":3,
		"activationNo":"ACT-2026-000001","subId":5,"quantity":1,"chargeMode":"subscription_quota",
		"cacheHit":true,"amount":0,"result":"success","upstreamMs":12,"createAt":1786000000123}],"count":1,"page":1}}`)}
	client := New(doer)
	page, err := client.Usage(context.Background(), UsageQuery{})
	if err != nil {
		t.Fatalf("Usage 不应报错：%v", err)
	}
	want := UsageRecord{
		Id: 7, RequestId: "req_row_1", UserId: 9, Capability: "ip-locate", ProductId: 3,
		ActivationNo: "ACT-2026-000001", SubId: 5, Quantity: 1, ChargeMode: "subscription_quota",
		CacheHit: true, Amount: 0, Result: "success", UpstreamMs: 12, CreateAt: 1786000000123,
	}
	if len(page.Records) != 1 || !reflect.DeepEqual(page.Records[0], want) {
		t.Fatalf("流水行字段不符：%+v", page.Records)
	}
	// 字段数护栏：新增/删改字段必须同步 proto UsageRecord（14 列）
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 14 {
		t.Fatalf("UsageRecord 应为 14 字段，实际 %d：%s", len(fields), string(encoded))
	}
}

// ============================= 业务失败（typed 方法层） =============================

// TestTypedMethodBusinessError - typed 方法把失败信封统一归一为 *apis.Error（errors.As 断言，逐方法）
func TestTypedMethodBusinessError(t *testing.T) {

	failure := []byte(`{"code":"QUOTA_EXCEEDED","msg":"订阅额度已耗尽","detail":{"scope":"month"}}`)
	client := New(&fakeDoer{response: failure})
	var apiErr *Error

	if _, _, err := client.IPLocate(context.Background(), "203.0.113.10"); !errors.As(err, &apiErr) || apiErr.Code != ErrorCodeQuotaExceeded {
		t.Fatalf("IPLocate 业务失败应归一：%v", err)
	}
	if _, _, err := client.Invoke(context.Background(), InvokeInput{Capability: "ip-locate"}); !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("Invoke 业务失败应归一：%v", err)
	}
	if _, _, err := client.MailSend(context.Background(), MailSendInput{To: []string{"user@example.com"}}); !errors.As(err, &apiErr) || apiErr.Code != ErrorCodeQuotaExceeded {
		t.Fatalf("MailSend 业务失败应归一：%v", err)
	}
	if _, err := client.Usage(context.Background(), UsageQuery{}); !errors.As(err, &apiErr) || apiErr.Detail["scope"] != "month" {
		t.Fatalf("Usage 业务失败应归一：%v", err)
	}
}
