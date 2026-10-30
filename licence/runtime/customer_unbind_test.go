package runtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func (this *fakePlatform) handleCustomerUnbind(writer http.ResponseWriter, request *http.Request, body []byte) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.customerBindCalls++
	if !this.credential(writer, request, body) {
		return
	}
	this.customerBindRequests = append(this.customerBindRequests, map[string]string{
		"body": string(body), "token": request.Header.Get("X-License-Token"),
		"timestamp": request.Header.Get("X-License-Timestamp"),
		"nonce":     request.Header.Get("X-License-Nonce"), "signature": request.Header.Get("X-License-Sign"),
	})
	status := this.customerBindStatus
	if status == "" {
		status = "OK"
	}
	writeJson(writer, map[string]any{
		"status": status, "serverTime": time.Now().UnixMilli(), "message": this.customerBindMessage,
	})
}

func TestClientCustomerUnbindHTTP(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if err = client.CustomerUnbind(t.Context(), "123456"); err != nil {
		t.Fatalf("解绑请求失败: %v", err)
	}
	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.customerBindRequests...)
	platform.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("解绑请求次数错误: %d", len(requests))
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(requests[0]["body"]), &body); err != nil {
		t.Fatal(err)
	}
	if body["twoFACode"] != "123456" {
		t.Fatal("解绑请求未携带 twoFACode")
	}
	for _, key := range []string{"token", "timestamp", "nonce", "signature"} {
		if requests[0][key] == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}
}

func TestClientCustomerUnbindHTTPRejected(t *testing.T) {
	platform := newFakePlatform(t)
	platform.customerBindStatus = "INVALID_ARGUMENT"
	platform.customerBindMessage = "两步验证码错误或已过期"
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	err = client.CustomerUnbind(t.Context(), "000000")
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "INVALID_ARGUMENT" || provisionErr.Message != "两步验证码错误或已过期" {
		t.Fatalf("解绑业务拒绝透传不符: %T %v", err, err)
	}
}

func TestClientCustomerUnbindValidation(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.CustomerUnbind(t.Context(), "123456"); err == nil {
		t.Fatal("未激活不得解绑")
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if err = client.CustomerUnbind(t.Context(), " "); err == nil {
		t.Fatal("空验证码不得解绑")
	}
	platform.mu.Lock()
	calls := platform.customerBindCalls
	platform.mu.Unlock()
	if calls != 0 {
		t.Fatalf("本地校验失败不得触网，实际 %d 次", calls)
	}
}
