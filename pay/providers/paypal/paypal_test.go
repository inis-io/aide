package paypal

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/pay"
)

type fixedClock struct{ now time.Time }

func (this fixedClock) Now() time.Time { return this.now }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (this roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return this(request)
}

// TestOfflineCreateAndVerifiedWebhook - 用假 HTTP 验证下单、Webhook 验签及 APPROVED 不触发 Capture
func TestOfflineCreateAndVerifiedWebhook(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	paths := make([]string, 0, 4)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		mu.Unlock()
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		switch {
		case strings.Contains(request.URL.Path, "/v2/checkout/orders") && request.Method == http.MethodPost:
			status, body = http.StatusCreated, `{"id":"P-1","status":"CREATED","purchase_units":[{"custom_id":"T-1","amount":{"currency_code":"USD","value":"10.00"}}],"links":[{"href":"https://www.paypal.com/checkoutnow?token=P-1","rel":"approve","method":"GET"}]}`
		case strings.Contains(request.URL.Path, "verify-webhook-signature"):
			body = `{"verification_status":"SUCCESS"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	registry := pay.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
	if err != nil {
		t.Fatalf("构造 PayPal Driver 失败：%v", err)
	}
	defer driver.Close()
	request := pay.NewTradeCreateRequest("T-1", pay.TradeModePC, "商品", pay.NewMoneyMinor(1000, "USD"))
	request.ReturnURL, request.CancelURL = "https://merchant.test/ok", "https://merchant.test/cancel"
	result, err := driver.CreateTrade(context.Background(), request)
	if err != nil {
		t.Fatalf("离线下单失败：%v", err)
	}
	if result.GatewayTradeNo != "P-1" || result.Action == nil || result.Action.Redirect == nil {
		t.Fatalf("下单结果错误：%+v", result)
	}
	webhook := `{"id":"EV-1","create_time":"2026-08-08T12:00:00Z","resource_type":"checkout-order","event_type":"CHECKOUT.ORDER.APPROVED","resource":{"id":"P-1","status":"APPROVED","purchase_units":[{"custom_id":"T-1","amount":{"currency_code":"USD","value":"10.00"}}]}}`
	event, err := driver.ParseNotify(context.Background(), pay.NotifyRequest{Kind: pay.NotifyKindWebhook, Method: http.MethodPost, Headers: http.Header{"Paypal-Transmission-Time": []string{"2026-08-08T12:00:00Z"}, "Paypal-Cert-Url": []string{"https://api.paypal.com/cert.pem"}, "Paypal-Auth-Algo": []string{"SHA256withRSA"}, "Paypal-Transmission-Id": []string{"TX-1"}, "Paypal-Transmission-Sig": []string{"signature"}}, Body: []byte(webhook)})
	if err != nil {
		t.Fatalf("Webhook 验签解析失败：%v", err)
	}
	if event.Type != pay.EventTradeApproved || event.Trade == nil || event.Trade.OutTradeNo != "T-1" {
		t.Fatalf("Webhook 事件错误：%+v", event)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		if strings.Contains(path, "/capture") {
			t.Fatal("APPROVED 解析阶段不得自动 Capture")
		}
	}
}

// TestStrictRawConfig - 验证 PayPal 动态配置拒绝未知字段
func TestStrictRawConfig(t *testing.T) {
	registry := pay.NewRegistry()
	_ = Register(registry)
	if _, err := registry.OpenRaw(context.Background(), "paypal", []byte(`{"unknown":true}`)); err == nil {
		t.Fatal("未知配置字段必须拒绝")
	}
}
