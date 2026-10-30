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

func (this *fakePlatform) handleCustomerUnbindOptions(writer http.ResponseWriter, request *http.Request, body []byte) {
	if !this.credential(writer, request, body) {
		return
	}
	writeJson(writer, map[string]any{
		"status": "OK", "preferred": "email", "methods": []string{"email", "sms", "password"},
		"destination": map[string]string{"email": "t***@example.com", "phone": "138****8000"},
		"serverTime":  time.Now().UnixMilli(),
	})
}

func (this *fakePlatform) handleCustomerUnbindChallenge(writer http.ResponseWriter, request *http.Request, body []byte) {
	if !this.credential(writer, request, body) {
		return
	}
	writeJson(writer, map[string]any{
		"status": "OK", "challengeNo": "challenge-email", "expiresAt": time.Now().Add(10 * time.Minute).UnixMilli(),
		"destination": "t***@example.com", "serverTime": time.Now().UnixMilli(),
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
	if err = client.CustomerUnbind(t.Context(), CustomerUnbindInput{Code: "123456"}); err != nil {
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
	if body["twoFACode"] != "123456" || body["code"] != "123456" || body["method"] != "totp" {
		t.Fatal("默认 TOTP 解绑请求未携带兼容字段")
	}
	for _, key := range []string{"token", "timestamp", "nonce", "signature"} {
		if requests[0][key] == "" {
			t.Fatalf("缺少签名头 %s", key)
		}
	}
}

func TestClientCustomerUnbindHTTPVerification(t *testing.T) {
	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	options, err := client.CustomerUnbindOptions(t.Context())
	if err != nil || options.Preferred != "email" || len(options.Methods) != 3 || options.Destination.Phone != "138****8000" {
		t.Fatalf("解绑选项映射不符: %+v err=%v", options, err)
	}
	challenge, err := client.CustomerUnbindChallenge(t.Context(), "email")
	if err != nil || challenge.ChallengeNo != "challenge-email" || challenge.Destination != "t***@example.com" || challenge.ExpiresAt == 0 {
		t.Fatalf("邮箱挑战映射不符: %+v err=%v", challenge, err)
	}
	if err = client.CustomerUnbind(t.Context(), CustomerUnbindInput{
		Method: "email", ChallengeNo: challenge.ChallengeNo, Code: "123456",
	}); err != nil {
		t.Fatal(err)
	}
	if err = client.CustomerUnbind(t.Context(), CustomerUnbindInput{Method: "password", Password: "secret-password"}); err != nil {
		t.Fatal(err)
	}
	platform.mu.Lock()
	requests := append([]map[string]string(nil), platform.customerBindRequests...)
	platform.mu.Unlock()
	var emailBody, passwordBody map[string]any
	if err = json.Unmarshal([]byte(requests[0]["body"]), &emailBody); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(requests[1]["body"]), &passwordBody); err != nil {
		t.Fatal(err)
	}
	if emailBody["method"] != "email" || emailBody["challengeNo"] != "challenge-email" || emailBody["code"] != "123456" {
		t.Fatalf("邮箱解绑字段不符: %+v", emailBody)
	}
	if passwordBody["method"] != "password" || passwordBody["password"] != "secret-password" {
		t.Fatalf("密码解绑字段不符: %+v", passwordBody)
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
	err = client.CustomerUnbind(t.Context(), CustomerUnbindInput{Code: "000000"})
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
	if err = client.CustomerUnbind(t.Context(), CustomerUnbindInput{Code: "123456"}); err == nil {
		t.Fatal("未激活不得解绑")
	}
	if _, err = client.CustomerUnbindOptions(t.Context()); err == nil {
		t.Fatal("未激活不得读取解绑选项")
	}
	if _, err = client.CustomerUnbindChallenge(t.Context(), "email"); err == nil {
		t.Fatal("未激活不得发码")
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	for _, input := range []CustomerUnbindInput{{Code: " "}, {Method: "password"}, {Method: "email", Code: "123456"}, {Method: "other", Code: "123456"}} {
		if err = client.CustomerUnbind(t.Context(), input); err == nil {
			t.Fatalf("无效凭据不得解绑: %+v", input)
		}
	}
	if _, err = client.CustomerUnbindChallenge(t.Context(), "password"); err == nil {
		t.Fatal("密码不得走发码端点")
	}
	platform.mu.Lock()
	calls := platform.customerBindCalls
	platform.mu.Unlock()
	if calls != 0 {
		t.Fatalf("本地校验失败不得触网，实际 %d 次", calls)
	}
}
