package licence

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
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

func (this *httpRuntimeTransport) Close() error {
	this.http.CloseIdleConnections()
	return nil
}
