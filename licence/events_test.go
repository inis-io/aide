package licence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
)

// ============================= PullEvents（HTTP/gRPC 拉取原语） =============================
//
// 订阅器语义（前缀通配分发、水位推进、deliveryNo 去重、验签失败停摆）已随 EventSubscriber
// 迁往 callback 子包测试（callback/subscriber_test.go，假 EventPuller 注入信封）；
// 本文件只覆盖 Client.PullEvents 原语：事件批次拉取、sinceEventId 过滤、状态闸门与传输层未初始化。

// TestPullEventsHTTP - HTTP 长轮询拉取：事件按 eventId 升序返回、sinceEventId 过滤、信封原文可读
func TestPullEventsHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer client.Close()

	if _, err = platform.pushEvent("saas.tenant.created", map[string]any{"tenantNo": "T1"}); err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}
	second, err := platform.pushEvent("saas.plan.updated", map[string]any{"planCode": "pro"})
	if err != nil {
		t.Fatalf("pushEvent 失败: %v", err)
	}

	events, err := client.PullEvents(t.Context(), 0, time.Second)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 2 || events[0].EventId != 1 || events[1].EventId != second {
		t.Fatalf("事件批次异常: %+v", events)
	}
	// 信封原文可读（匿名结构解析，平台现场重签的 CallbackEnvelope JSON）
	var envelope struct {
		Payload struct {
			Event      string `json:"event"`
			DeliveryNo string `json:"deliveryNo"`
		} `json:"payload"`
	}
	if err = json.Unmarshal(events[0].Envelope, &envelope); err != nil {
		t.Fatalf("信封解析失败: %v", err)
	}
	if envelope.Payload.Event != "saas.tenant.created" || envelope.Payload.DeliveryNo != "SUB-EVT-2026-000001" {
		t.Fatalf("信封载荷异常: %+v", envelope.Payload)
	}

	// sinceEventId 过滤：只返回 > since 的事件
	events, err = client.PullEvents(t.Context(), 1, time.Second)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 1 || events[0].EventId != second {
		t.Fatalf("sinceEventId 过滤错误: %+v", events)
	}
}

// TestPullEventsHTTPNonPassThrough - 非放行态（宽限耗尽）错误文本归一
func TestPullEventsHTTPNonPassThrough(t *testing.T) {

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

	_, err = client.PullEvents(t.Context(), 0, time.Second)
	if err == nil || err.Error() != "许可证非放行态：EXPIRED" {
		t.Fatalf("期望'许可证非放行态：EXPIRED'，实际 %v", err)
	}
}

// TestPullEventsTransportNotReady - 未 Start（传输层未初始化）直接返回错误
func TestPullEventsTransportNotReady(t *testing.T) {

	client := &Client{}
	if _, err := client.PullEvents(context.Background(), 0, 0); err == nil || err.Error() != "运行面传输层未初始化" {
		t.Fatalf("期望'运行面传输层未初始化'，实际 %v", err)
	}
}

// TestPullEventsGRPC - gRPC 服务端流经 PullEvents 收集事件（传输细节见 TestGRPCRuntimeTransportSubscribeEvents）
func TestPullEventsGRPC(t *testing.T) {

	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	server := runtimeEventServer{t: t, seed: seed, events: []fakeEvent{
		{eventId: 1, eventNo: "EVT-2026-000001", event: "saas.tenant.created", data: json.RawMessage(`{"tenantNo":"T1"}`)},
		{eventId: 2, eventNo: "EVT-2026-000002", event: "saas.plan.updated", data: json.RawMessage(`{"planCode":"pro"}`)},
	}}
	transport, err := newTestEventTransport(t, server)
	if err != nil {
		t.Fatal(err)
	}
	clientSeed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{
		LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second,
		PublicKeys: map[string]string{"license-key-2026-01": "unused"},
	}}
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	transport.client = client
	client.transport = transport

	events, err := client.PullEvents(t.Context(), 0, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("PullEvents 失败: %v", err)
	}
	if len(events) != 2 || events[0].EventId != 1 || events[1].EventId != 2 {
		t.Fatalf("gRPC 事件收集异常: %+v", events)
	}
}

// TestPullEventsGRPCNonPassThrough - gRPC 非放行态错误归一（与 HTTP 侧同一错误文本）
func TestPullEventsGRPCNonPassThrough(t *testing.T) {

	seed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newTestEventTransport(t, runtimeEventServer{t: t, seed: seed, failCode: codes.FailedPrecondition})
	if err != nil {
		t.Fatal(err)
	}
	clientSeed, _, err := generateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{options: Options{LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second}}
	client.state.ActivationToken = "token"
	client.state.ClientSeed = hex.EncodeToString(clientSeed)
	transport.client = client
	client.transport = transport

	_, err = client.PullEvents(t.Context(), 0, 100*time.Millisecond)
	if err == nil || err.Error() != "许可证非放行态：SUSPENDED" {
		t.Fatalf("期望'许可证非放行态：SUSPENDED'，实际 %v", err)
	}
}
