package callback

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	LicenceRuntime "github.com/inis-io/aide/licence/runtime"
)

// ============================= 假事件拉取方（EventPuller 镜像） =============================

// fakePullerEvent - 假平台事件（签名字段与 eventId 分离，信封每轮现场重签）
type fakePullerEvent struct {
	eventId int64
	eventNo string
	event   string
	data    json.RawMessage
}

// fakeEventPuller - 假事件拉取方：按 sinceEventId 过滤并现场重签（nonce 每次新鲜、occurredAt 当下、
// deliveryNo 稳定 SUB-{eventNo}，镜像平台订阅端点行为）；err 非空时模拟非放行态/传输错误
type fakeEventPuller struct {
	mu         sync.Mutex
	seed       []byte
	keyVersion string
	events     []fakePullerEvent
	err        error
	calls      int
	lastSince  int64
	lastHold   time.Duration
}

// PullEvents - 实现 EventPuller
func (this *fakeEventPuller) PullEvents(_ context.Context, sinceEventId int64, hold time.Duration) ([]LicenceRuntime.SubscribedEvent, error) {

	this.mu.Lock()
	defer this.mu.Unlock()
	this.calls++
	this.lastSince = sinceEventId
	this.lastHold = hold
	if this.err != nil {
		return nil, this.err
	}
	var events []LicenceRuntime.SubscribedEvent
	for _, item := range this.events {
		if item.eventId <= sinceEventId {
			continue
		}
		events = append(events, signSubscribedEvent(this.seed, this.keyVersion, item))
	}
	return events, nil
}

// fakeKeyPuller - 带公钥表的拉取方（实现 PublicKeys，订阅器应强制复用其公钥而非 options 传入值）
type fakeKeyPuller struct {
	*fakeEventPuller
	publicKey string
}

// PublicKeys - 实现公钥提供者（keyVersion → hex 公钥）
func (this *fakeKeyPuller) PublicKeys() map[string]string {

	return map[string]string{"license-key-test": this.publicKey}
}

// signSubscribedEvent - 现场重签一条订阅事件信封并组装 SubscribedEvent（夹具代码，出错直接 panic）
func signSubscribedEvent(seed []byte, keyVersion string, event fakePullerEvent) LicenceRuntime.SubscribedEvent {

	payload := CallbackPayload{
		EventNo: event.eventNo, DeliveryNo: "SUB-" + event.eventNo, Event: event.event,
		ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		OccurredAt: time.Now().UTC().Format(time.RFC3339), Nonce: LicenceProtocol.Licence.Nonce(),
		KeyVersion: keyVersion, Data: event.data,
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	signature, err := LicenceProtocol.Licence.Seed(seed).Sign(rawPayload)
	if err != nil {
		panic(err)
	}
	envelope, err := json.Marshal(CallbackEnvelope{
		Version: LicenceProtocol.EnvelopeVersion, Algorithm: LicenceProtocol.Algorithm, Payload: payload, Signature: signature,
	})
	if err != nil {
		panic(err)
	}
	return LicenceRuntime.SubscribedEvent{EventId: event.eventId, Envelope: envelope}
}

// newTestPuller - 创建假拉取方（返回其验签公钥）
func newTestPuller(t *testing.T, events ...fakePullerEvent) (*fakeEventPuller, string) {

	t.Helper()
	seed, publicKey := LicenceProtocol.Licence.GenerateKeyPair().KeyPair()
	if len(seed) == 0 || publicKey == "" {
		t.Fatal("生成测试密钥失败")
	}
	return &fakeEventPuller{seed: seed, keyVersion: "license-key-test", events: events}, publicKey
}

// TestEventSubscriberDispatchAndWatermark - 前缀通配分发、水位推进、deliveryNo 去重重放、增量推进
func TestEventSubscriberDispatchAndWatermark(t *testing.T) {

	puller, publicKey := newTestPuller(t,
		fakePullerEvent{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
		fakePullerEvent{eventId: 2, eventNo: "EVT-2026-000002", event: "saas.plan.updated", data: json.RawMessage(`{"planCode":"pro"}`)},
	)
	received := make(chan *CallbackEvent, 8)
	subscriber := NewEventSubscriber(puller, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": publicKey},
	}).OnEvent("saas.*", func(ctx context.Context, event *CallbackEvent) (Ack, error) {
		received <- event
		return AckSuccess, nil
	})

	delivered, err := subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("Poll 失败: %v", err)
	}
	if delivered != 2 {
		t.Fatalf("一轮应分发 2 条，实际 %d", delivered)
	}
	if subscriber.Watermark() != 2 {
		t.Fatalf("水位应推进到 2，实际 %d", subscriber.Watermark())
	}
	// 默认 hold 15s、水位原样透传
	if puller.lastHold != 15*time.Second || puller.lastSince != 0 {
		t.Fatalf("拉取参数异常: since=%d hold=%v", puller.lastSince, puller.lastHold)
	}

	// 前缀通配命中 + data 摘要解码
	e1 := <-received
	if e1.Payload.Event != "saas.tenant.created" {
		t.Fatalf("事件类型=%s 期望 saas.tenant.created", e1.Payload.Event)
	}
	var data struct {
		TenantNo string `json:"tenantNo"`
	}
	e1.MustData(&data)
	if data.TenantNo != "T1" {
		t.Fatalf("data.tenantNo=%s 期望 T1", data.TenantNo)
	}
	<-received

	// 去重：水位回退到 0 重拉（平台现场重签 fresh nonce），deliveryNo 去重 TTL 内不重复分发但计入水位
	subscriber.SetWatermark(0)
	delivered, err = subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("重拉 Poll 失败: %v", err)
	}
	if delivered != 2 {
		t.Fatalf("重拉应投递 2 条（去重应答），实际 %d", delivered)
	}
	if len(received) != 0 {
		t.Fatalf("重拉不应重复分发业务回调，handler 收到 %d 条", len(received))
	}
	if subscriber.Watermark() != 2 {
		t.Fatalf("重拉后水位=%d 期望 2", subscriber.Watermark())
	}

	// 新事件增量推进水位
	puller.mu.Lock()
	puller.events = append(puller.events, fakePullerEvent{
		eventId: 3, eventNo: "EVT-2026-000003", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T2"}`),
	})
	puller.mu.Unlock()
	delivered, err = subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("增量 Poll 失败: %v", err)
	}
	if delivered != 1 || subscriber.Watermark() != 3 {
		t.Fatalf("增量 Poll delivered=%d watermark=%d，期望 1/3", delivered, subscriber.Watermark())
	}
	<-received
}

// TestEventSubscriberSignatureFailure - 订阅信封验签失败不推进水位
func TestEventSubscriberSignatureFailure(t *testing.T) {

	puller, _ := newTestPuller(t,
		fakePullerEvent{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
	)
	// 篡改验签公钥表：订阅信封验签必然失败
	subscriber := NewEventSubscriber(puller, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": hex.EncodeToString(make([]byte, 32))},
	}).OnEvent("saas.tenant.created", func(ctx context.Context, event *CallbackEvent) (Ack, error) { return AckSuccess, nil })

	if _, err := subscriber.Poll(t.Context()); err == nil || err.Error() != "callback signature verification failed" {
		t.Fatalf("期望验签失败错误，实际 %v", err)
	}
	if subscriber.Watermark() != 0 {
		t.Fatalf("验签失败不应推进水位，实际 %d", subscriber.Watermark())
	}
}

// TestEventSubscriberRetryStopsWatermark - 业务应答 retry：批次在该事件中断且水位不落盘（下轮从头重拉）；
// 重拉时前面事件与 retry 事件均走 deliveryNo 去重重放应答，不重复执行业务回调
func TestEventSubscriberRetryStopsWatermark(t *testing.T) {

	puller, publicKey := newTestPuller(t,
		fakePullerEvent{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
		fakePullerEvent{eventId: 2, eventNo: "EVT-2026-000002", event: "saas.plan.updated", data: json.RawMessage(`{"planCode":"pro"}`)},
	)
	retried := 0
	subscriber := NewEventSubscriber(puller, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": publicKey},
	}).OnEvent("saas.plan.updated", func(ctx context.Context, event *CallbackEvent) (Ack, error) {
		retried++
		return AckRetry, nil
	})

	delivered, err := subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("Poll 失败: %v", err)
	}
	// 事件 1（未注册处理器 → ignored）计入分发；事件 2 retry → 批次中断，advance 不落盘（水位保持 0）
	if delivered != 1 || subscriber.Watermark() != 0 {
		t.Fatalf("retry 应中断批次且水位不落盘，实际 delivered=%d watermark=%d", delivered, subscriber.Watermark())
	}
	// 重拉：事件 1 重放 ignored 应答、事件 2 重放 retry 应答（fresh nonce + 同 deliveryNo），均不重复执行业务回调
	delivered, err = subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("重拉 Poll 失败: %v", err)
	}
	if delivered != 1 || subscriber.Watermark() != 0 {
		t.Fatalf("重拉语义应一致，实际 delivered=%d watermark=%d", delivered, subscriber.Watermark())
	}
	if retried != 1 {
		t.Fatalf("retry 应答重放不应重复执行业务回调，实际执行 %d 次", retried)
	}
}

// TestEventSubscriberPullError - 拉取方错误（非放行态/服务端故障/传输错误）原样透出且不推进水位
func TestEventSubscriberPullError(t *testing.T) {

	puller, publicKey := newTestPuller(t)
	puller.err = errors.New("许可证非放行态：EXPIRED")
	subscriber := NewEventSubscriber(puller, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": publicKey},
	})
	delivered, err := subscriber.Poll(t.Context())
	if err == nil || err.Error() != "许可证非放行态：EXPIRED" {
		t.Fatalf("拉取错误应原样透出，实际 %v", err)
	}
	if delivered != 0 || subscriber.Watermark() != 0 {
		t.Fatalf("拉取失败不应分发/推进，实际 delivered=%d watermark=%d", delivered, subscriber.Watermark())
	}
}

// TestEventSubscriberPanicBecomesError - 业务回调 panic 经 dispatch 转为 error（不推进水位、不崩溃进程）
func TestEventSubscriberPanicBecomesError(t *testing.T) {

	puller, publicKey := newTestPuller(t,
		fakePullerEvent{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
	)
	subscriber := NewEventSubscriber(puller, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": publicKey},
	}).OnEvent("saas.tenant.created", func(ctx context.Context, event *CallbackEvent) (Ack, error) { panic("测试 panic") })

	if _, err := subscriber.Poll(t.Context()); err == nil || err.Error() != "订阅事件分发 panic" {
		t.Fatalf("panic 应转为 error，实际 %v", err)
	}
	if subscriber.Watermark() != 0 {
		t.Fatalf("panic 不应推进水位，实际 %d", subscriber.Watermark())
	}
}

// TestNewEventSubscriberInheritsPullerPublicKeys - 拉取方实现 PublicKeys() 时订阅器强制复用其公钥
// （options 传入的错误公钥表被覆盖——镜像旧 Client.Subscribe 语义）
func TestNewEventSubscriberInheritsPullerPublicKeys(t *testing.T) {

	puller, publicKey := newTestPuller(t,
		fakePullerEvent{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
	)
	keyed := &fakeKeyPuller{fakeEventPuller: puller, publicKey: publicKey}
	received := 0
	subscriber := NewEventSubscriber(keyed, CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": hex.EncodeToString(make([]byte, 32))}, // 故意传错
	}).OnAny(func(ctx context.Context, event *CallbackEvent) (Ack, error) {
		received++
		return AckSuccess, nil
	})

	delivered, err := subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("复用拉取方公钥应验签通过，实际 %v", err)
	}
	if delivered != 1 || received != 1 || subscriber.Watermark() != 1 {
		t.Fatalf("分发结果异常: delivered=%d received=%d watermark=%d", delivered, received, subscriber.Watermark())
	}
}
