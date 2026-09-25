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

// ============================= 假宿主 =============================

// fakeDoer - 记录调用的假宿主（断言请求构造、幂等键透传与错误归一）
type fakeDoer struct {
	// calls - 调用次数
	calls int
	// method/path/body/requestId - 最近一次调用的请求参数
	method    string
	path      string
	body      []byte
	requestId string
	// response - 罐头响应（业务信封 JSON 原文）
	response []byte
	// err - 非空时直接返回该错误（模拟网络/传输失败）
	err error
}

func (this *fakeDoer) Do(ctx context.Context, method string, path string, body []byte, requestId string) ([]byte, error) {

	this.calls++
	this.method, this.path, this.body, this.requestId = method, path, body, requestId
	if this.err != nil {
		return nil, this.err
	}
	return this.response, nil
}

// TestNewAssemblesDoer - New 仅装配宿主调用能力与各能力子资源，不发起网络请求
func TestNewAssemblesDoer(t *testing.T) {

	doer := &fakeDoer{}
	client := New(doer)
	if client == nil || client.doer != doer {
		t.Fatalf("New 应原样装配 doer")
	}
	if client.IPLocate == nil {
		t.Fatalf("New 应挂载 IPLocate 能力子资源")
	}
	if client.Email == nil {
		t.Fatalf("New 应挂载 Email 能力子资源")
	}
	if doer.calls != 0 {
		t.Fatalf("New 不得发起调用，实际 %d 次", doer.calls)
	}
}

// TestSharedCoreReexport - 共享核心件以类型别名 re-export：与 apis/core 同一类型（别名同一性），
// 既有引用方（runtime 适配器、下游）零改动
func TestSharedCoreReexport(t *testing.T) {

	var err error = &Error{Code: ErrorCodeQuotaExceeded, Message: "额度耗尽"}
	if !errors.Is(ErrNotActivated, ErrNotActivated) || ErrNotActivated.Error() == "" {
		t.Fatalf("ErrNotActivated 应原样 re-export：%v", ErrNotActivated)
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "QUOTA_EXCEEDED" {
		t.Fatalf("Error 别名应可 errors.As 断言：%v", err)
	}
	if HTTPStatusByCode(ErrorCodeRateLimited) != http.StatusTooManyRequests {
		t.Fatalf("HTTPStatusByCode 应薄壳转发 core 映射：%d", HTTPStatusByCode(ErrorCodeRateLimited))
	}
	// 编译期别名同一性：Doer 与 core.Doer 同型（fakeDoer 实现即可赋值）
	var _ Doer = (*fakeDoer)(nil)
	if (Receipt{}).RequestId != "" {
		t.Fatalf("Receipt 别名应可用：%+v", Receipt{})
	}
}

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

// ============================= 业务失败（跨能力方法层） =============================

// TestCrossCapabilityBusinessError - 根包跨能力方法把失败信封统一归一为 *apis.Error（errors.As 断言）
func TestCrossCapabilityBusinessError(t *testing.T) {

	failure := []byte(`{"code":"QUOTA_EXCEEDED","msg":"订阅额度已耗尽","detail":{"scope":"month"}}`)
	client := New(&fakeDoer{response: failure})
	var apiErr *Error

	if _, _, err := client.Invoke(context.Background(), InvokeInput{Capability: "ip-locate"}); !errors.As(err, &apiErr) || apiErr.HTTPStatus != http.StatusForbidden {
		t.Fatalf("Invoke 业务失败应归一：%v", err)
	}
	if _, err := client.Usage(context.Background(), UsageQuery{}); !errors.As(err, &apiErr) || apiErr.Detail["scope"] != "month" {
		t.Fatalf("Usage 业务失败应归一：%v", err)
	}
}
