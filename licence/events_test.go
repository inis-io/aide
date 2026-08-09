package licence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

// ============================= HTTP 传输事件订阅 =============================

// TestEventSubscriberHTTP - HTTP 长轮询订阅：前缀通配分发、水位推进、deliveryNo 去重不重复分发
func TestEventSubscriberHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Close()

	received := make(chan *CallbackEvent, 8)
	subscriber := client.Subscribe(CallbackOptions{}).
		OnEvent("saas.*", func(ctx context.Context, event *CallbackEvent) (Ack, error) {
			received <- event
			return AckSuccess, nil
		})

	_, err = platform.pushEvent(EventSaasTenantCreated, map[string]any{"tenantNo": "T1", "planCode": "pro", "environment": "production"})
	if err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}
	second, err := platform.pushEvent(EventSaasPlanUpdated, map[string]any{"planCode": "pro", "manifestVersion": 2})
	if err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}

	delivered, err := subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("Poll 失败: %v", err)
	}
	if delivered != 2 {
		t.Fatalf("一轮应分发 2 条，实际 %d", delivered)
	}
	if subscriber.Watermark() != second {
		t.Fatalf("水位应推进到 %d，实际 %d", second, subscriber.Watermark())
	}

	// 前缀通配命中 + data 摘要解码
	e1 := <-received
	if e1.Payload.Event != EventSaasTenantCreated {
		t.Fatalf("事件类型=%s 期望 %s", e1.Payload.Event, EventSaasTenantCreated)
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
	if subscriber.Watermark() != second {
		t.Fatalf("重拉后水位=%d 期望 %d", subscriber.Watermark(), second)
	}

	// 新事件增量推进水位
	third, err := platform.pushEvent(EventSaasTenantCreated, map[string]any{"tenantNo": "T2"})
	if err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}
	delivered, err = subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("增量 Poll 失败: %v", err)
	}
	if delivered != 1 || subscriber.Watermark() != third {
		t.Fatalf("增量 Poll delivered=%d watermark=%d，期望 1/%d", delivered, subscriber.Watermark(), third)
	}
	<-received
}

// TestEventSubscriberHTTPNonPassThrough - 非放行态（宽限耗尽）错误文本归一
func TestEventSubscriberHTTPNonPassThrough(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	// 先以有效状态激活（EXPIRED 时 Start 会拒绝），随后让许可证宽限耗尽
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Close()
	platform.mu.Lock()
	platform.validUntil = time.Now().Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339)
	platform.mu.Unlock()

	subscriber := client.Subscribe(CallbackOptions{}).
		OnEvent(EventSaasTenantCreated, func(ctx context.Context, event *CallbackEvent) (Ack, error) { return AckSuccess, nil })
	_, err = subscriber.Poll(t.Context())
	if err == nil || err.Error() != "许可证非放行态：EXPIRED" {
		t.Fatalf("期望'许可证非放行态：EXPIRED'，实际 %v", err)
	}
}

// TestEventSubscriberSignatureFailure - 订阅信封验签失败不推进水位
// （不 Start 以避免后台刷新循环与公钥篡改竞态；手动注入凭证走 HTTP 传输）
func TestEventSubscriberSignatureFailure(t *testing.T) {

	platform := newFakePlatform(t)
	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("test-token"))
	platform.mu.Lock()
	platform.tokenHash = hex.EncodeToString(sum[:])
	platform.clientPublicKey = hex.EncodeToString(ed25519PublicKey(seed))
	platform.mu.Unlock()

	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer client.Close()
	client.mu.Lock()
	client.state.ActivationToken = "test-token"
	client.state.ClientSeed = hex.EncodeToString(seed)
	client.mu.Unlock()

	// 篡改验签公钥表：订阅信封验签必然失败
	client.options.PublicKeys["license-key-2026-01"] = hex.EncodeToString(make([]byte, 32))
	subscriber := client.Subscribe(CallbackOptions{}).
		OnEvent(EventSaasTenantCreated, func(ctx context.Context, event *CallbackEvent) (Ack, error) { return AckSuccess, nil })

	if _, err = platform.pushEvent(EventSaasTenantCreated, map[string]any{"tenantNo": "T1"}); err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}
	if _, err = subscriber.Poll(t.Context()); err == nil || err.Error() != "callback signature verification failed" {
		t.Fatalf("期望验签失败错误，实际 %v", err)
	}
}

// ============================= gRPC 传输事件订阅 =============================

// TestEventSubscriberGRPC - gRPC 服务端流订阅：精确事件注册、水位推进、deliveryNo 去重
func TestEventSubscriberGRPC(t *testing.T) {

	seed, publicKey, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	server := runtimeEventServer{t: t, seed: seed, events: []fakeEvent{
		{eventId: 1, eventNo: "EVT-2026-000001", event: EventSaasTenantCreated, data: []byte(`{"tenantNo":"T1"}`)},
		{eventId: 2, eventNo: "EVT-2026-000002", event: EventSaasPlanUpdated, data: []byte(`{"planCode":"pro"}`)},
	}}
	client := &Client{options: Options{
		LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second,
		PublicKeys: map[string]string{"license-key-2026-01": hex.EncodeToString(publicKey)},
	}}
	transport, err := newTestEventTransport(t, server)
	if err != nil {
		t.Fatal(err)
	}
	transport.client = client
	client.transport = transport
	clientSeed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	client.mu.Unlock()

	received := make(chan *CallbackEvent, 8)
	subscriber := client.Subscribe(CallbackOptions{}).
		OnEvent(EventSaasTenantCreated, func(ctx context.Context, event *CallbackEvent) (Ack, error) {
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
	e1 := <-received
	if e1.Payload.Event != EventSaasTenantCreated {
		t.Fatalf("精确事件注册应命中 %s，实际 %s", EventSaasTenantCreated, e1.Payload.Event)
	}
	// 第二条 saas.plan.updated 未注册处理器 → AckIgnored，不进入 received（但计入 delivered）

	// 去重：水位回退重拉，handler 不应重复执行
	subscriber.SetWatermark(0)
	delivered, err = subscriber.Poll(t.Context())
	if err != nil {
		t.Fatalf("重拉 Poll 失败: %v", err)
	}
	if delivered != 2 || len(received) != 0 {
		t.Fatalf("重拉 delivered=%d 且不应重复分发（received=%d）", delivered, len(received))
	}
	if subscriber.Watermark() != 2 {
		t.Fatalf("重拉后水位=%d 期望 2", subscriber.Watermark())
	}
}

// TestEventSubscriberGRPCNonPassThrough - gRPC 非放行态错误归一（与 HTTP 侧同一错误文本）
func TestEventSubscriberGRPCNonPassThrough(t *testing.T) {

	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{
		LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second,
		PublicKeys: map[string]string{"license-key-2026-01": hex.EncodeToString(make([]byte, 32))},
	}}
	transport, err := newTestEventTransport(t, runtimeEventServer{t: t, seed: seed, failCode: codes.FailedPrecondition})
	if err != nil {
		t.Fatal(err)
	}
	transport.client = client
	client.transport = transport
	clientSeed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	client.mu.Unlock()

	subscriber := client.Subscribe(CallbackOptions{}).
		OnEvent(EventSaasTenantCreated, func(ctx context.Context, event *CallbackEvent) (Ack, error) { return AckSuccess, nil })
	_, err = subscriber.Poll(t.Context())
	if err == nil || err.Error() != "许可证非放行态：SUSPENDED" {
		t.Fatalf("期望'许可证非放行态：SUSPENDED'，实际 %v", err)
	}
}
