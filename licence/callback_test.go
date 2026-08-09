package licence

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func callbackRequest(t *testing.T, seed []byte, payload CallbackPayload) []byte {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化回调载荷失败: %v", err)
	}
	signature, err := signPayload(rawPayload, seed)
	if err != nil {
		t.Fatalf("签发回调失败: %v", err)
	}
	raw, err := json.Marshal(CallbackEnvelope{
		Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature,
	})
	if err != nil {
		t.Fatalf("序列化回调信封失败: %v", err)
	}
	return raw
}

func newCallbackTestHandler(t *testing.T) (*CallbackHandler, []byte) {
	t.Helper()
	seed, publicKey, err := generateKeyPair()
	if err != nil {
		t.Fatalf("生成测试密钥失败: %v", err)
	}
	return NewCallbackHandler(CallbackOptions{
		PublicKeys: map[string]string{"license-key-test": hex.EncodeToString(publicKey)},
	}), seed
}

func defaultCallbackPayload() CallbackPayload {
	return CallbackPayload{
		EventNo: "EVT-2026-000001", DeliveryNo: "DLV-2026-000001",
		Event: "saas.plan.updated", ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
		OccurredAt: time.Now().UTC().Format(time.RFC3339), Nonce: Licence.Nonce(),
		KeyVersion: "license-key-test", Data: json.RawMessage(`{"planCode":"pro"}`),
	}
}

// TestCallbackPayloadGolden - 回调载荷字段顺序与平台签发端保持一致
func TestCallbackPayloadGolden(t *testing.T) {
	payload := CallbackPayload{
		EventNo: "EVT-2026-000001", DeliveryNo: "DLV-2026-000001", Event: "project.config.updated",
		ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001", OccurredAt: "2026-08-09T12:00:00+08:00",
		Nonce: "00112233445566778899aabbccddeeff", KeyVersion: "license-key-test",
		Data: json.RawMessage(`{"configKey":"app.theme","version":3}`),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("序列化回调载荷失败: %v", err)
	}
	want := `{"eventNo":"EVT-2026-000001","deliveryNo":"DLV-2026-000001","event":"project.config.updated","projectId":"PRJ-2026-000001","instanceId":"INS-2026-000001","occurredAt":"2026-08-09T12:00:00+08:00","nonce":"00112233445566778899aabbccddeeff","keyVersion":"license-key-test","data":{"configKey":"app.theme","version":3}}`
	if string(raw) != want {
		t.Fatalf("回调载荷 canonical JSON 漂移:\n got: %s\nwant: %s", raw, want)
	}
}

func serveCallback(handler *CallbackHandler, body []byte) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/licence/callback", bytes.NewReader(body))
	handler.ServeHTTP(recorder, request)
	return recorder
}

// TestCallbackHandlerDispatch - 验签通过后按最具体的前缀通配分发
func TestCallbackHandlerDispatch(t *testing.T) {
	handler, seed := newCallbackTestHandler(t)
	called := 0
	handler.OnEvent("saas.*", func(context.Context, *CallbackEvent) (Ack, error) {
		t.Fatal("不应命中较宽的通配器")
		return AckRetry, nil
	})
	handler.OnEvent("saas.plan.*", func(_ context.Context, event *CallbackEvent) (Ack, error) {
		called++
		var data struct {
			PlanCode string `json:"planCode"`
		}
		event.MustData(&data)
		if data.PlanCode != "pro" {
			t.Fatalf("回调数据不符: %+v", data)
		}
		return AckSuccess, nil
	})
	recorder := serveCallback(handler, callbackRequest(t, seed, defaultCallbackPayload()))
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(AckSuccess) || called != 1 {
		t.Fatalf("回调分发结果不符: code=%d body=%q called=%d", recorder.Code, recorder.Body.String(), called)
	}
}

// TestCallbackHandlerRejectsInvalidRequests - 验签失败和超出时间窗的请求必须拒绝
func TestCallbackHandlerRejectsInvalidRequests(t *testing.T) {
	handler, seed := newCallbackTestHandler(t)
	payload := defaultCallbackPayload()
	raw := callbackRequest(t, seed, payload)
	var envelope CallbackEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("解析测试信封失败: %v", err)
	}
	last := len(envelope.Signature) - 1
	if envelope.Signature[last] == '0' {
		envelope.Signature = envelope.Signature[:last] + "1"
	} else {
		envelope.Signature = envelope.Signature[:last] + "0"
	}
	raw, _ = json.Marshal(envelope)
	if recorder := serveCallback(handler, raw); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("篡改签名应返回 401，实际 %d", recorder.Code)
	}
	payload = defaultCallbackPayload()
	payload.OccurredAt = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	if recorder := serveCallback(handler, callbackRequest(t, seed, payload)); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("过期回调应返回 401，实际 %d", recorder.Code)
	}
}

// TestCallbackHandlerDedup - nonce 重放被拒绝，deliveryNo 重复则重放原应答
func TestCallbackHandlerDedup(t *testing.T) {
	handler, seed := newCallbackTestHandler(t)
	called := 0
	handler.OnAny(func(context.Context, *CallbackEvent) (Ack, error) {
		called++
		return AckOk, nil
	})
	payload := defaultCallbackPayload()
	body := callbackRequest(t, seed, payload)
	if recorder := serveCallback(handler, body); recorder.Code != http.StatusOK {
		t.Fatalf("首次投递失败: %d", recorder.Code)
	}
	if recorder := serveCallback(handler, body); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("nonce 重放应返回 401，实际 %d", recorder.Code)
	}
	payload.Nonce = Licence.Nonce()
	if recorder := serveCallback(handler, callbackRequest(t, seed, payload)); recorder.Code != http.StatusOK || recorder.Body.String() != string(AckOk) {
		t.Fatalf("重复 deliveryNo 未重放原应答: code=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if called != 1 {
		t.Fatalf("重复投递不应重复执行业务回调，called=%d", called)
	}
}

// TestCallbackHandlerUnknownAndPanic - 未订阅事件忽略，业务 panic 恢复为 500
func TestCallbackHandlerUnknownAndPanic(t *testing.T) {
	handler, seed := newCallbackTestHandler(t)
	if recorder := serveCallback(handler, callbackRequest(t, seed, defaultCallbackPayload())); recorder.Body.String() != string(AckIgnored) {
		t.Fatalf("未订阅事件应 ignored，实际 %q", recorder.Body.String())
	}
	handler, seed = newCallbackTestHandler(t)
	handler.OnAny(func(context.Context, *CallbackEvent) (Ack, error) { panic("测试 panic") })
	if recorder := serveCallback(handler, callbackRequest(t, seed, defaultCallbackPayload())); recorder.Code != http.StatusInternalServerError {
		t.Fatalf("业务 panic 应返回 500，实际 %d", recorder.Code)
	}
}
