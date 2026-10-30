package runtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func (this *fakePlatform) handleCustomerBindPrepare(writer http.ResponseWriter, request *http.Request, body []byte) {
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
	if this.customerBindStatus != "" && this.customerBindStatus != "OK" {
		out := map[string]any{
			"status": this.customerBindStatus, "serverTime": time.Now().UnixMilli(), "message": this.customerBindMessage,
		}
		if this.customerBindPrepareCustomer != nil {
			out["customer"] = this.customerBindPrepareCustomer
		}
		writeJson(writer, out)
		return
	}
	writeJson(writer, map[string]any{
		"status": "OK", "serverTime": time.Now().UnixMilli(),
		"requestNo": "REQ-ABCD-EFGH", "expiresAt": 1735689600000,
	})
}

func TestClientCustomerBindPrepareHTTP(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	const redirectURI = "https://im.example.com/admin/fed/bind-callback"
	const state = "0123456789abcdef0123456789abcdef"
	result, err := client.CustomerBindPrepare(t.Context(), redirectURI, state)
	if err != nil {
		t.Fatalf("预注册授权请求失败: %v", err)
	}
	if result.RequestNo != "REQ-ABCD-EFGH" || result.ExpiresAt != 1735689600000 {
		t.Fatalf("预注册响应映射不符: %+v", result)
	}
	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.customerBindRequests...)
	platform.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("预注册请求次数错误: %d", len(requests))
	}
	var body map[string]any
	if err = json.Unmarshal([]byte(requests[0]["body"]), &body); err != nil {
		t.Fatal(err)
	}
	if body["redirectUri"] != redirectURI || body["state"] != state {
		t.Fatalf("回跳与 state 未原样上送: %v", body)
	}
	for _, key := range []string{"token", "timestamp", "nonce", "signature"} {
		if requests[0][key] == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}

	platform.mu.Lock()
	platform.customerBindStatus = "BIND_INVALID"
	platform.customerBindMessage = "授权请求无效"
	platform.mu.Unlock()
	_, err = client.CustomerBindPrepare(t.Context(), redirectURI, state)
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "BIND_INVALID" {
		t.Fatalf("业务拒绝透传不符: %T %v", err, err)
	}
}

func TestClientCustomerBindPrepareNotActivated(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if _, err = client.CustomerBindPrepare(t.Context(), "https://im.example.com/callback", "state"); err == nil {
		t.Fatal("未激活不得发起预注册")
	}
	platform.mu.Lock()
	calls := platform.customerBindCalls
	platform.mu.Unlock()
	if calls != 0 {
		t.Fatalf("未激活不得触网，实际 %d 次", calls)
	}
}

func TestClientCustomerBindPrepareAlreadyBoundHTTP(t *testing.T) {
	platform := newFakePlatform(t)
	platform.customerBindStatus = "CUSTOMER_BOUND"
	platform.customerBindPrepareCustomer = &CustomerView{MaskAccount: "r***@x.com", BoundAt: 123}
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	result, err := client.CustomerBindPrepare(t.Context(), "https://im.example.com/callback", "state")
	if err != nil {
		t.Fatalf("已绑定预注册应返回归属: %v", err)
	}
	if result.RequestNo != "" || result.Customer == nil || result.Customer.MaskAccount != "r***@x.com" || result.Customer.BoundAt != 123 {
		t.Fatalf("已绑定响应映射不符: %+v", result)
	}
}

func TestClientCustomerBindPrepareAlreadyBoundRequiresCustomer(t *testing.T) {
	platform := newFakePlatform(t)
	platform.customerBindStatus = "CUSTOMER_BOUND"
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	_, err = client.CustomerBindPrepare(t.Context(), "https://im.example.com/callback", "state")
	if err == nil || err.Error() != "已绑定响应缺少客户信息" {
		t.Fatalf("已绑定响应缺客户视图必须明确报错: %v", err)
	}
}
