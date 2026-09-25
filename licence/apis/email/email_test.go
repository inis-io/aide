package email

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

// TestMailSendRequestConstruction - Send 请求构造：路径/方法/请求体（to 数组与 html 开关原样下发，
// 能力/动作/计量数不进消息体）、结果三字段与回执解析、数值边界语义（sent 为 int 而非字符串）
func TestMailSendRequestConstruction(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{
		"result":{"sent":2,"recipients":["a@example.com","b@example.com"],"subject":"对账单"},
		"receipt":{"requestId":"req_mail_1","chargeMode":"free_quota","cacheHit":false,"quantity":2,
			"amount":0,"quotaRemaining":48,"balanceAfter":0,"serverTime":1786000000000}}}`)}
	resource := New(doer)
	result, receipt, err := resource.Send(context.Background(), Input{
		To: []string{"a@example.com", "b@example.com"}, Subject: "对账单",
		Content: "<p>正文</p>", HTML: true,
	}, "req_mail_1")
	if err != nil {
		t.Fatalf("Send 不应报错：%v", err)
	}
	if doer.method != "POST" || doer.path != mailSendPath {
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
	expected := Result{
		Sent: 2, Recipients: []string{"a@example.com", "b@example.com"}, Subject: "对账单",
	}
	if !reflect.DeepEqual(result, expected) {
		t.Fatalf("代发结果不符：%+v（期望 %+v）", result, expected)
	}
	if receipt.Quantity != 2 || receipt.ChargeMode != "free_quota" || receipt.RequestId != "req_mail_1" {
		t.Fatalf("回执不符：%+v", receipt)
	}
	// 契约字段数守卫：Result 与 proto MailSendResult 同为 3 字段（新增字段必须同步 proto 与后端出参）
	encoded, err := json.Marshal(Result{})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]any{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 {
		t.Fatalf("Result 应为 3 字段，实际 %d：%s", len(fields), string(encoded))
	}
}

// TestMailSendEmptyRecipientsSerialized - to 为空切片时请求体仍带 "to":[]（服务端据此拒绝空收件人，
// 而不是把「未传」与「传了空数组」混为一谈）；html 缺省 false 原样序列化；缺省幂等键自动生成
func TestMailSendEmptyRecipientsSerialized(t *testing.T) {

	doer := &fakeDoer{response: []byte(`{"code":0,"msg":"ok","data":{"result":{"sent":0},"receipt":{}}}`)}
	resource := New(doer)
	if _, _, err := resource.Send(context.Background(), Input{
		To: []string{}, Subject: "对账单", Content: "正文",
	}); err != nil {
		t.Fatalf("Send 不应报错：%v", err)
	}
	if string(doer.body) != `{"to":[],"subject":"对账单","content":"正文","html":false}` {
		t.Fatalf("空收件人应序列化为空数组：%s", string(doer.body))
	}
	if !strings.HasPrefix(doer.requestId, "req_") || len(doer.requestId) != 36 {
		t.Fatalf("缺省幂等键应自动生成 req_ + 32 位：%q", doer.requestId)
	}
}

// TestMailSendBusinessError - Send 把失败信封统一归一为 *apis.Error（errors.As 断言）
func TestMailSendBusinessError(t *testing.T) {

	failure := []byte(`{"code":"QUOTA_EXCEEDED","msg":"订阅额度已耗尽","detail":{"scope":"month"}}`)
	resource := New(&fakeDoer{response: failure})
	var apiErr *core.Error
	if _, _, err := resource.Send(context.Background(), Input{To: []string{"user@example.com"}}); !errors.As(err, &apiErr) || apiErr.Code != core.ErrorCodeQuotaExceeded {
		t.Fatalf("Send 业务失败应归一：%v", err)
	}
}
