package iplocate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/inis-io/aide/licence/apis/core"
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

// TestNewAssemblesDoer - New 仅装配宿主调用能力，不发起网络请求
func TestNewAssemblesDoer(t *testing.T) {

	doer := &fakeDoer{}
	resource := New(doer)
	if resource == nil || resource.doer != doer {
		t.Fatalf("New 应原样装配 doer")
	}
	if doer.calls != 0 {
		t.Fatalf("New 不得发起调用，实际 %d 次", doer.calls)
	}
}

// TestIPLocateResultAlignment - Query 结果：10 个字段与 proto IPLocateResult / 后端 provider 逐字对齐
func TestIPLocateResultAlignment(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{
		"result":{"ip":"203.0.113.10","nation":"中国","province":"浙江省","city":"杭州市","adcode":"330100",
			"rectangle":"119.9,29.9;120.4,30.4","isp":"","source":"amap","cacheHit":true,"stale":false},
		"receipt":{"requestId":"req_loc_1","chargeMode":"subscription_quota","cacheHit":true,"quantity":1,
			"amount":0,"quotaRemaining":9832,"balanceAfter":0,"serverTime":1786000000000}}}`)}
	resource := New(doer)
	loc, receipt, err := resource.Query(context.Background(), "203.0.113.10", "req_loc_1")
	if err != nil {
		t.Fatalf("Query 不应报错：%v", err)
	}
	if doer.method != "POST" || doer.path != ipLocatePath {
		t.Fatalf("请求路由不符：%s %s", doer.method, doer.path)
	}
	if string(doer.body) != `{"ip":"203.0.113.10"}` {
		t.Fatalf("Query 请求体应仅含 ip：%s", string(doer.body))
	}
	want := Result{
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

// TestIPLocateNetworkErrorPassthrough - 网络/传输错误原样返回：不伪装业务码（errors.Is 可穿透）
func TestIPLocateNetworkErrorPassthrough(t *testing.T) {

	networkErr := errors.New("dial tcp 127.0.0.1:443: connect: connection refused")
	resource := New(&fakeDoer{err: networkErr})
	_, _, err := resource.Query(context.Background(), "203.0.113.10")
	if !errors.Is(err, networkErr) {
		t.Fatalf("网络错误应原样返回，实际 %v", err)
	}
	var apiErr *core.Error
	if errors.As(err, &apiErr) {
		t.Fatalf("网络错误不得伪装成业务码：%+v", apiErr)
	}
}

// TestIPLocateResponseShapes - 成功信封 data 缺 result/receipt 时的解析容错：
// 契约内字段缺失不 panic，零值返回（客户按零值判空）
func TestIPLocateResponseShapes(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{}}`)}
	resource := New(doer)
	loc, receipt, err := resource.Query(context.Background(), "203.0.113.10")
	if err != nil {
		t.Fatalf("空 data 应正常返回：%v", err)
	}
	if loc != (Result{}) || receipt != (core.Receipt{}) {
		t.Fatalf("空 data 应返回零值结果：%+v %+v", loc, receipt)
	}
	if !json.Valid(doer.body) {
		t.Fatalf("请求体应为合法 JSON：%s", string(doer.body))
	}
}

// TestIPLocateBusinessError - Query 把失败信封统一归一为 *apis.Error（errors.As 断言）
func TestIPLocateBusinessError(t *testing.T) {

	failure := []byte(`{"code":"QUOTA_EXCEEDED","msg":"订阅额度已耗尽","detail":{"scope":"month"}}`)
	resource := New(&fakeDoer{response: failure})
	var apiErr *core.Error
	if _, _, err := resource.Query(context.Background(), "203.0.113.10"); !errors.As(err, &apiErr) || apiErr.Code != core.ErrorCodeQuotaExceeded {
		t.Fatalf("Query 业务失败应归一：%v", err)
	}
}
