package licence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

type httpAdminTransport struct {
	client *AdminClient
	http   *http.Client
}

func newHTTPAdminTransport(client *AdminClient) *httpAdminTransport {
	return &httpAdminTransport{client: client, http: &http.Client{Timeout: client.options.HTTPTimeout}}
}

func (this *httpAdminTransport) RoundTrip(ctx context.Context, call adminCall) (json.RawMessage, error) {
	addr := strings.TrimRight(this.client.options.ServerURL, "/") + call.Path
	if len(call.Query) > 0 {
		addr += "?" + call.Query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, call.Method, addr, bytes.NewReader(call.Body))
	if err != nil {
		return nil, err
	}
	if call.ContentType != "" {
		request.Header.Set("Content-Type", call.ContentType)
	}
	if call.Token != "" && call.Path != signInPath {
		request.Header.Set("Authorization", "Bearer "+call.Token)
	}
	return this.do(request)
}

func (this *httpAdminTransport) do(request *http.Request) (json.RawMessage, error) {
	response, err := this.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("licence: 请求发送失败: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("licence: 响应读取失败: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode, Body: string(raw)}
	}
	var envelope Response
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("licence: 响应信封解析失败（原文：%s）: %w", string(raw), err)
	}
	if envelope.Code != http.StatusOK {
		return nil, &APIError{Code: envelope.Code, Msg: envelope.Msg, Data: envelope.Data}
	}
	return envelope.Data, nil
}

func (this *httpAdminTransport) Upload(ctx context.Context, upload adminUpload) (json.RawMessage, error) {
	reader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(this.client.options.ServerURL, "/")+upload.Path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if upload.Token != "" {
		request.Header.Set("Authorization", "Bearer "+upload.Token)
	}
	writeResult := make(chan error, 1)
	go func() {
		var writeErr error
		for key, value := range upload.Fields {
			if writeErr = writer.WriteField(key, value); writeErr != nil {
				break
			}
		}
		if writeErr == nil {
			var part io.Writer
			part, writeErr = writer.CreateFormFile(upload.FileField, filepath.Base(upload.FileName))
			if writeErr == nil {
				_, writeErr = io.Copy(part, upload.Content)
			}
		}
		if closeErr := writer.Close(); writeErr == nil {
			writeErr = closeErr
		}
		_ = pipeWriter.CloseWithError(writeErr)
		writeResult <- writeErr
	}()
	data, requestErr := this.do(request)
	writeErr := <-writeResult
	if requestErr != nil {
		return nil, requestErr
	}
	if writeErr != nil {
		return nil, writeErr
	}
	return data, nil
}

func (this *httpAdminTransport) Close() error {
	this.http.CloseIdleConnections()
	return nil
}

var _ adminTransport = (*httpAdminTransport)(nil)
