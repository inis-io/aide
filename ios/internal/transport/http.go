package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/inis-io/aide/ios/sign"
	"github.com/inis-io/aide/ios/types"
)

// ============================= HTTP 链路 - 开始 =============================

// signField - 签名键名（body/sign 字段或 query 的 sign 参数）
const signField = "sign"

// httpRoute - HTTP 链路路由描述
type httpRoute struct {
	// method - HTTP 方法
	method string
	// path - 平台开放端点路径
	path string
}

// httpRoutes - 操作 → 平台开放端点映射（端点以 object-storage 后端 GenRoute 实际路由为准，不可自造）
// 约定：GET/DELETE 参数走 query，POST/PUT 参数走 JSON body；全部端点必签。
var httpRoutes = map[Operation]httpRoute{
	OpAccessKeyVerify:  {method: http.MethodGet, path: "/api/open-accesskey/verify"},
	OpBucketInfoList:   {method: http.MethodGet, path: "/api/open-bucket/find"},
	OpBucketEmpty:      {method: http.MethodDelete, path: "/api/open-bucket/empty"},
	OpShareCreate:      {method: http.MethodPost, path: "/api/open-share/create"},
	OpShareList:        {method: http.MethodGet, path: "/api/open-share/find"},
	OpShareRevoke:      {method: http.MethodDelete, path: "/api/open-share/remove"},
	OpRecycleList:      {method: http.MethodGet, path: "/api/open-object/recycle"},
	OpRecycleRestore:   {method: http.MethodPut, path: "/api/open-object/restore"},
	OpRecyclePurge:     {method: http.MethodDelete, path: "/api/open-object/purge"},
	OpRecycleClear:     {method: http.MethodDelete, path: "/api/open-object/clear"},
	OpUsageOverview:    {method: http.MethodGet, path: "/api/open-usage/overview"},
	OpUsageBucketList:  {method: http.MethodGet, path: "/api/open-usage/find"},
	OpUsageEgressTrend: {method: http.MethodGet, path: "/api/open-usage/trend"},
}

// httpTransport - HTTP 链路实现
// 平台响应恒为 HTTP 200 + 信封 {code,msg,data}，业务成败只看 body.code。
type httpTransport struct {
	// endpoint - 平台地址（已裁剪末尾 /）
	endpoint string
	// accessKey - 访问密钥 AK（进 query/body 与签名）
	accessKey string
	// secret - 密钥 SK
	secret string
	// signer - 签名器
	signer sign.Signer
	// client - HTTP 客户端
	client *http.Client
	// userAgent - 请求 User-Agent
	userAgent string
	// now - 时间戳来源（测试可注入）
	now func() time.Time
}

// NewHTTP - 装配 HTTP 链路（零业务请求，纯结构装配）
func NewHTTP(config Config) Transport {

	signer := config.Signer
	if signer == nil {
		signer = sign.New()
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}

	return &httpTransport{
		endpoint:  strings.TrimRight(config.Endpoint, "/"),
		accessKey: config.AccessKey,
		secret:    config.Secret,
		signer:    signer,
		client:    httpClient,
		userAgent: config.UserAgent,
		now:       time.Now,
	}
}

// Invoke - 发起 HTTP 调用并解析信封
func (this *httpTransport) Invoke(ctx context.Context, op Operation, req, resp any) error {

	route, ok := httpRoutes[op]
	if !ok {
		return fmt.Errorf("%w：未知操作 %s", types.ErrInvalidRequest, op)
	}

	params, err := this.payload(op, req)
	if err != nil {
		return err
	}

	// 签名写入 body 的 sign 字段（POST/PUT）或 query 的 sign 参数（GET/DELETE）
	params[signField] = this.signer.Sign(params, this.secret)

	request, err := this.request(ctx, route, params)
	if err != nil {
		return err
	}

	response, err := this.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w：HTTP 请求失败（%v）", types.ErrTransport, err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("%w：读取响应失败（%v）", types.ErrTransport, err)
	}

	return decodeEnvelope(raw, resp)
}

// Close - HTTP 链路无需释放资源
func (this *httpTransport) Close() error { return nil }

// request - 组装 HTTP 请求（GET/DELETE 拼 query，POST/PUT 拼 JSON body）
func (this *httpTransport) request(ctx context.Context, route httpRoute, params map[string]any) (*http.Request, error) {

	address := this.endpoint + route.path

	if route.method == http.MethodGet || route.method == http.MethodDelete {
		query := make(url.Values, len(params))
		for key, value := range params {
			if text := sign.Value(value); text != "" {
				query.Set(key, text)
			}
		}
		if encoded := query.Encode(); encoded != "" {
			address += "?" + encoded
		}

		request, err := http.NewRequestWithContext(ctx, route.method, address, nil)
		if err != nil {
			return nil, fmt.Errorf("%w：构造 %s 请求失败（%v）", types.ErrTransport, route.method, err)
		}
		request.Header.Set("User-Agent", this.userAgent)

		return request, nil
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%w：请求序列化失败（%v）", types.ErrInvalidRequest, err)
	}

	request, err := http.NewRequestWithContext(ctx, route.method, address, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w：构造 %s 请求失败（%v）", types.ErrTransport, route.method, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", this.userAgent)

	return request, nil
}

// payload - 组装签名参数集（按操作注入服务端默认值；签名由调用方在组装后追加）
// 与 gRPC 链路共用同一批 Normalize() 方法与同一个签名器，保证两链路签名语义一致。
func (this *httpTransport) payload(op Operation, req any) (map[string]any, error) {

	switch op {

	case OpAccessKeyVerify, OpBucketInfoList, OpUsageOverview, OpUsageBucketList, OpUsageEgressTrend:
		return this.signedParams(nil)

	case OpBucketEmpty:
		in, ok := req.(*types.EmptyBucketRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：清空桶请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)

	case OpShareCreate:
		in, ok := req.(*types.ShareCreateRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：签发分享请求类型不匹配", types.ErrInvalidRequest)
		}
		value := *in
		value.Normalize()
		return this.signedParams(value)

	case OpShareList:
		in, ok := req.(*types.ShareListRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：分享列表请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)

	case OpShareRevoke:
		in, ok := req.(*types.ShareRevokeRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：吊销分享请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)

	case OpRecycleList:
		in, ok := req.(*types.RecycleListRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：回收站列表请求类型不匹配", types.ErrInvalidRequest)
		}
		value := *in
		value.Normalize()
		return this.signedParams(value)

	case OpRecycleRestore:
		in, ok := req.(*types.RecycleRestoreRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：回收站还原请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(map[string]any{"ids": types.JoinIds(in.Ids)})

	case OpRecyclePurge:
		in, ok := req.(*types.RecyclePurgeRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：回收站释放请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(map[string]any{"ids": types.JoinIds(in.Ids)})

	case OpRecycleClear:
		in, ok := req.(*types.RecycleClearRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：回收站清空请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)
	}

	return nil, fmt.Errorf("%w：未知操作 %s", types.ErrInvalidRequest, op)
}

// signedParams - 请求结构体展开为签名参数集并注入 AK 与毫秒时间戳
func (this *httpTransport) signedParams(req any) (map[string]any, error) {

	var params map[string]any
	if req == nil {
		params = make(map[string]any)
	} else if raw, ok := req.(map[string]any); ok {
		params = raw
	} else {
		var err error
		params, err = structToMap(req)
		if err != nil {
			return nil, err
		}
	}

	params[accessKeyField] = this.accessKey
	params[timestampField] = this.now().UnixMilli()

	return params, nil
}

// structToMap - 按 JSON tag 将结构体展开为签名参数集
// 与服务端「结构体 → JSON → map」路径一致，保证签名键名（camelCase）与值形态完全相同。
func structToMap(value any) (map[string]any, error) {

	text, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w：请求参数序列化失败（%v）", types.ErrInvalidRequest, err)
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return nil, fmt.Errorf("%w：请求参数解析失败（%v）", types.ErrInvalidRequest, err)
	}

	return params, nil
}

// decodeEnvelope - 解析平台信封 {code,msg,data}；code!=200 归一为 *types.APIError
func decodeEnvelope(raw []byte, resp any) error {

	var envelope struct {
		// Code - 业务码（200 成功）
		Code int `json:"code"`
		// Msg - 中文文案
		Msg string `json:"msg"`
		// Data - 业务数据
		Data json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("ios: 响应不是合法 JSON（%v）", err)
	}

	if envelope.Code != 200 {
		return &types.APIError{Code: envelope.Code, Msg: envelope.Msg, Raw: raw}
	}

	if resp == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}

	if decoder, ok := resp.(dataUnmarshaler); ok {
		return decoder.UnmarshalData(envelope.Data)
	}

	if err := json.Unmarshal(envelope.Data, resp); err != nil {
		return fmt.Errorf("ios: 响应 data 解析失败（%v）", err)
	}

	return nil
}

// ============================= HTTP 链路 - 结束 =============================
