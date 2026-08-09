package licence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpRuntimeTransport - HTTP JSON 运行面传输。
type httpRuntimeTransport struct {
	client *Client
	http   *http.Client
}

func newHTTPRuntimeTransport(client *Client) *httpRuntimeTransport {
	return &httpRuntimeTransport{client: client, http: &http.Client{Timeout: client.options.HTTPTimeout}}
}

func (this *httpRuntimeTransport) RoundTrip(ctx context.Context, method, requestURI string, body []byte, withSign bool) (int, []byte, error) {
	addr := strings.TrimRight(this.client.options.ServerURL, "/") + requestURI
	request, err := http.NewRequestWithContext(ctx, method, addr, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if withSign {
		headers, signErr := this.client.signHeaders(method, requestURI, body)
		if signErr != nil {
			return 0, nil, signErr
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
	}
	response, err := this.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	return response.StatusCode, raw, err
}

// SubscribeEvents - 事件订阅 HTTP 长轮询实现。
// hold 上界收敛到专用长轮询客户端超时 −5s，避免被默认短超时掐断。
func (this *httpRuntimeTransport) SubscribeEvents(ctx context.Context, licenseNo string, sinceEventId int64, hold time.Duration) (subscribeResult, error) {
	timeout := this.client.options.HTTPTimeout + 30*time.Second
	maxHold := timeout - 5*time.Second
	if maxHold <= 0 {
		maxHold = time.Second
	}
	if hold <= 0 {
		hold = 15 * time.Second
	}
	if hold > maxHold {
		hold = maxHold
	}
	// 服务端对 timeout_ms 有 30000ms 硬上限，显式收敛（当前 hold 默认 15s 不触顶，防御未来可配置化）
	if hold > 30*time.Second {
		hold = 30 * time.Second
	}
	body, err := json.Marshal(map[string]any{
		"licenseNo": licenseNo, "sinceEventId": sinceEventId, "timeoutMs": hold.Milliseconds(),
	})
	if err != nil {
		return subscribeResult{}, err
	}
	const uri = "/api/v1/events/subscribe"
	addr := strings.TrimRight(this.client.options.ServerURL, "/") + uri
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, bytes.NewReader(body))
	if err != nil {
		return subscribeResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	headers, signErr := this.client.signHeaders(http.MethodPost, uri, body)
	if signErr != nil {
		return subscribeResult{}, signErr
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	// 长轮询专用客户端：单次请求超时 = HTTPTimeout + 30s，hold 上界已收敛到其 −5s
	client := &http.Client{Timeout: timeout}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return subscribeResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return subscribeResult{}, err
	}
	if response.StatusCode == http.StatusNotFound {
		return subscribeResult{}, errors.New("许可证或项目信息无效")
	}
	var result struct {
		Status     string             `json:"status"`
		ServerTime int64              `json:"serverTime"`
		Events     []SubscribedEvent  `json:"events"`
		Message    string             `json:"message"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return subscribeResult{}, err
	}
	return subscribeResult{
		Status: result.Status, ServerTime: result.ServerTime, Message: result.Message, Events: result.Events,
	}, nil
}

func (this *httpRuntimeTransport) Close() error {
	this.http.CloseIdleConnections()
	return nil
}

var _ runtimeTransport = (*httpRuntimeTransport)(nil)
