package paypal

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	paypalv2 "github.com/go-pay/gopay/paypal"

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

// TestReasonMapping - 验证 PayPal 错误名到标准分类的映射与网关错误 Reason 填充
func TestReasonMapping(t *testing.T) {
	cases := map[string]pay.Reason{
		"RESOURCE_NOT_FOUND":   pay.ReasonOrderNotFound,
		"RATE_LIMIT_REACHED":   pay.ReasonRateLimited,
		"UNPROCESSABLE_ENTITY": pay.ReasonInvalidRequest,
		"UNKNOWN":              pay.ReasonNone,
	}
	for code, expected := range cases {
		if reasonFor(code) != expected {
			t.Fatalf("错误名 %s 映射错误：%s", code, reasonFor(code))
		}
	}
	err := checkResponse(404, &paypalv2.ErrorResponse{Name: "RESOURCE_NOT_FOUND", Message: "资源不存在"})
	var gateway *pay.GatewayError
	if !errors.As(err, &gateway) || gateway.Reason != pay.ReasonOrderNotFound {
		t.Fatalf("checkResponse 未填充 Reason：%v", err)
	}
	if pay.ReasonOf(checkResponse(400, &paypalv2.ErrorResponse{Name: "MALFORMED_REQUEST"})) != pay.ReasonNone {
		t.Fatal("未知错误名应返回 ReasonNone")
	}
}

// TestCaptureIDAndRefundLifecycle - 用假 HTTP 验证 capture id 回填、退款与退款查询闭环
func TestCaptureIDAndRefundLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	requests := make([]*http.Request, 0, 8)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		path := request.URL.Path
		switch {
		case strings.Contains(path, "/v2/checkout/orders/") && strings.Contains(path, "/capture"):
			status, body = http.StatusCreated, `{"id":"P-1","status":"COMPLETED","purchase_units":[{"custom_id":"T-1","payments":{"captures":[{"id":"C-1","status":"COMPLETED","amount":{"currency_code":"USD","value":"10.00"}}]}}]}`
		case strings.Contains(path, "/v2/checkout/orders/") && request.Method == http.MethodGet:
			body = `{"id":"P-1","status":"COMPLETED","purchase_units":[{"custom_id":"T-1","payments":{"captures":[{"id":"C-1","status":"COMPLETED"}]}}]}`
		case strings.Contains(path, "/v2/payments/captures/") && strings.HasSuffix(path, "/refund"):
			status, body = http.StatusCreated, `{"id":"R-1","status":"COMPLETED","invoice_id":"RF-1","amount":{"currency_code":"USD","value":"10.00"}}`
		case strings.Contains(path, "/v2/payments/refunds/"):
			body = `{"id":"R-1","status":"COMPLETED","invoice_id":"RF-1","amount":{"currency_code":"USD","value":"10.00"}}`
		case strings.Contains(path, "verify-webhook-signature"):
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
	if !driver.Supports(pay.CapRefund) || !driver.Supports(pay.CapRefundQuery) {
		t.Fatal("PayPal 应声明退款能力")
	}
	capture := pay.NewTradeCaptureRequest("T-1", "P-1", "idem-1", pay.NewMoneyMinor(1000, "USD"))
	result, err := driver.CaptureTrade(context.Background(), capture)
	if err != nil {
		t.Fatalf("捕获失败：%v", err)
	}
	if result.GatewayCaptureNo != "C-1" {
		t.Fatalf("capture id 未回填：%+v", result)
	}
	query, err := driver.QueryTrade(context.Background(), pay.TradeQueryRequest{GatewayTradeNo: "P-1"})
	if err != nil || query.GatewayCaptureNo != "C-1" {
		t.Fatalf("查单 capture id 未回填：%+v %v", query, err)
	}
	refund := pay.NewRefundRequest("T-1", "RF-1", pay.NewMoneyMinor(1000, "USD"), pay.NewMoneyMinor(1000, "USD"))
	refund.GatewayTradeNo = "C-1"
	refund.IdempotencyKey = "idem-refund"
	refundResult, err := driver.Refund(context.Background(), refund)
	if err != nil {
		t.Fatalf("退款失败：%v", err)
	}
	if refundResult.GatewayRefundNo != "R-1" || refundResult.Status != pay.RefundStatusSucceeded {
		t.Fatalf("退款结果错误：%+v", refundResult)
	}
	refundQuery := pay.RefundQueryRequest{GatewayRefundNo: "R-1"}
	refundResult, err = driver.QueryRefund(context.Background(), refundQuery)
	if err != nil || refundResult.GatewayRefundNo != "R-1" || refundResult.Amount.Minor != 1000 {
		t.Fatalf("退款查询结果错误：%+v %v", refundResult, err)
	}
	if _, err = driver.Refund(context.Background(), pay.NewRefundRequest("T-1", "RF-2", pay.NewMoneyMinor(1000, "USD"), pay.NewMoneyMinor(500, "USD"))); !errors.Is(err, pay.ErrInvalidRequest) {
		t.Fatalf("空 capture id 应拒绝：%v", err)
	}
	if _, err = driver.QueryRefund(context.Background(), pay.NewRefundQueryRequest("RF-3")); !errors.Is(err, pay.ErrInvalidRequest) {
		t.Fatalf("空网关退款号应拒绝：%v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	var refundRequest *http.Request
	for _, item := range requests {
		if strings.Contains(item.URL.Path, "/refund") && item.Method == http.MethodPost {
			refundRequest = item
		}
	}
	if refundRequest == nil {
		t.Fatal("未发现退款请求")
	}
	if refundRequest.URL.Path != "/v2/payments/captures/C-1/refund" {
		t.Fatalf("退款路径错误：%s", refundRequest.URL.Path)
	}
	if refundRequest.Header.Get("PayPal-Request-Id") != "idem-refund" {
		t.Fatalf("幂等头缺失：%s", refundRequest.Header.Get("PayPal-Request-Id"))
	}
	body, _ := io.ReadAll(refundRequest.Body)
	if !strings.Contains(string(body), `"invoice_id":"RF-1"`) || !strings.Contains(string(body), `"value":"10.00"`) {
		t.Fatalf("退款请求体错误：%s", body)
	}
}

// TestCaptureResponseWithoutCaptureID - 捕获成功却缺 captures 的响应必须按异常响应拒绝
func TestCaptureResponseWithoutCaptureID(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		if strings.Contains(request.URL.Path, "/capture") {
			status, body = http.StatusCreated, `{"id":"P-1","status":"COMPLETED","purchase_units":[{"custom_id":"T-1"}]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	registry := pay.NewRegistry()
	_ = Register(registry)
	driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	capture := pay.NewTradeCaptureRequest("T-1", "P-1", "idem-1", pay.NewMoneyMinor(1000, "USD"))
	_, err = driver.CaptureTrade(context.Background(), capture)
	var gatewayError *pay.GatewayError
	if !errors.As(err, &gatewayError) || gatewayError.Outcome != pay.OutcomeUnknown {
		t.Fatalf("缺 capture id 应返回 OutcomeUnknown 异常响应：%v", err)
	}
}

// TestCaptureWebhookCarriesCaptureID - PAYMENT.CAPTURE.COMPLETED 事件须携带 capture id 与 order id
func TestCaptureWebhookCarriesCaptureID(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		if strings.Contains(request.URL.Path, "verify-webhook-signature") {
			body = `{"verification_status":"SUCCESS"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	registry := pay.NewRegistry()
	_ = Register(registry)
	driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	webhook := `{"id":"EV-2","create_time":"2026-08-08T12:00:00Z","resource_type":"capture","event_type":"PAYMENT.CAPTURE.COMPLETED","resource":{"id":"C-9","status":"COMPLETED","custom_id":"T-1","amount":{"currency_code":"USD","value":"10.00"},"supplementary_data":{"related_ids":{"order_id":"P-9"}}}}`
	event, err := driver.ParseNotify(context.Background(), pay.NotifyRequest{Kind: pay.NotifyKindWebhook, Method: http.MethodPost, Headers: http.Header{"Paypal-Transmission-Time": []string{"2026-08-08T12:00:00Z"}, "Paypal-Cert-Url": []string{"https://api.paypal.com/cert.pem"}, "Paypal-Auth-Algo": []string{"SHA256withRSA"}, "Paypal-Transmission-Id": []string{"TX-2"}, "Paypal-Transmission-Sig": []string{"signature"}}, Body: []byte(webhook)})
	if err != nil {
		t.Fatalf("Webhook 解析失败：%v", err)
	}
	if event.Trade == nil || event.Trade.GatewayCaptureNo != "C-9" || event.Trade.GatewayTradeNo != "P-9" {
		t.Fatalf("捕获事件字段错误：%+v", event.Trade)
	}
	if event.DedupeKey != "T-1|trade.succeeded" {
		t.Fatalf("Driver 未派生去重键：%s", event.DedupeKey)
	}
	again, err := driver.ParseNotify(context.Background(), pay.NotifyRequest{Kind: pay.NotifyKindWebhook, Method: http.MethodPost, Headers: http.Header{"Paypal-Transmission-Time": []string{"2026-08-08T12:00:00Z"}, "Paypal-Cert-Url": []string{"https://api.paypal.com/cert.pem"}, "Paypal-Auth-Algo": []string{"SHA256withRSA"}, "Paypal-Transmission-Id": []string{"TX-2"}, "Paypal-Transmission-Sig": []string{"signature"}}, Body: []byte(webhook)})
	if err != nil || again.DedupeKey != event.DedupeKey {
		t.Fatalf("同一载荷重复解析键应相同：%s vs %s", again.DedupeKey, event.DedupeKey)
	}
}

// TestQueryTradeOrderNotFound - 查无此单统一识别为 order-not-found
func TestQueryTradeOrderNotFound(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		if strings.Contains(request.URL.Path, "/v2/checkout/orders/") {
			status, body = http.StatusNotFound, `{"name":"RESOURCE_NOT_FOUND","message":"order not found","debug_id":"D-1"}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	registry := pay.NewRegistry()
	_ = Register(registry)
	driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	_, err = driver.QueryTrade(context.Background(), pay.TradeQueryRequest{GatewayTradeNo: "P-404"})
	if pay.ReasonOf(err) != pay.ReasonOrderNotFound {
		t.Fatalf("查无此单应统一识别：%v", err)
	}
}

// TestRefundStatusMappings - COMPLETED/PENDING/CANCELLED 三种网关状态在退款与退款查询中的映射
func TestRefundStatusMappings(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		gateway  string
		expected pay.RefundStatus
	}{
		{"COMPLETED", pay.RefundStatusSucceeded},
		{"PENDING", pay.RefundStatusProcessing},
		{"CANCELLED", pay.RefundStatusClosed},
	}
	for _, item := range cases {
		client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
			switch {
			case strings.Contains(request.URL.Path, "/v2/payments/captures/") && strings.HasSuffix(request.URL.Path, "/refund"):
				status, body = http.StatusCreated, `{"id":"R-1","status":"` + item.gateway + `","invoice_id":"RF-1","amount":{"currency_code":"USD","value":"10.00"}}`
			case strings.Contains(request.URL.Path, "/v2/payments/refunds/"):
				body = `{"id":"R-1","status":"` + item.gateway + `","invoice_id":"RF-1","amount":{"currency_code":"USD","value":"10.00"}}`
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}
		registry := pay.NewRegistry()
		_ = Register(registry)
		driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
		if err != nil {
			t.Fatal(err)
		}
		refund := pay.NewRefundRequest("T-1", "RF-1", pay.NewMoneyMinor(1000, "USD"), pay.NewMoneyMinor(1000, "USD"))
		refund.GatewayTradeNo = "C-1"
		refund.IdempotencyKey = "idem-1"
		result, err := driver.Refund(context.Background(), refund)
		if err != nil || result.Status != item.expected {
			t.Fatalf("退款状态 %s 映射错误：%+v %v", item.gateway, result, err)
		}
		queried, err := driver.QueryRefund(context.Background(), pay.RefundQueryRequest{GatewayRefundNo: "R-1"})
		if err != nil || queried.Status != item.expected {
			t.Fatalf("退款查询状态 %s 映射错误：%+v %v", item.gateway, queried, err)
		}
		driver.Close()
	}
}

// TestQueryTradeWithoutCaptureID - 订单未捕获是正常状态：无 captures 时 capture id 为空且不报错
func TestQueryTradeWithoutCaptureID(t *testing.T) {
	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"access_token":"token","app_id":"app","expires_in":3600}`
		if strings.Contains(request.URL.Path, "/v2/checkout/orders/") && request.Method == http.MethodGet {
			body = `{"id":"P-1","status":"APPROVED","purchase_units":[{"custom_id":"T-1","amount":{"currency_code":"USD","value":"10.00"}}]}`
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	registry := pay.NewRegistry()
	_ = Register(registry)
	driver, err := registry.New(context.Background(), "paypal", Config{ClientID: "client", Secret: pay.NewSensitiveString("canary-secret"), WebhookID: "WH-1"}, pay.WithSandbox(true), pay.WithHTTPClient(client), pay.WithClock(fixedClock{now}))
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()
	result, err := driver.QueryTrade(context.Background(), pay.TradeQueryRequest{GatewayTradeNo: "P-1"})
	if err != nil {
		t.Fatalf("未捕获订单查询不应报错：%v", err)
	}
	if result.GatewayCaptureNo != "" || result.GatewayTradeNo != "P-1" || result.Status != pay.TradeStatusProcessing {
		t.Fatalf("未捕获订单回填错误：%+v", result)
	}
}
