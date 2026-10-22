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

	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"
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
	// signed - 是否需要签名（收银端点为面向收银页的公开端点，平台不验签）
	signed bool
}

// httpRoutes - 操作 → 平台开放端点映射（端点以 pay 后端 GenRoute 实际路由为准，不可自造）
var httpRoutes = map[Operation]httpRoute{
	OpTradeCreate:   {method: http.MethodPost, path: "/api/trade/unity", signed: true},
	OpTradeQuery:    {method: http.MethodGet, path: "/api/trade/query", signed: true},
	OpCheckout:      {method: http.MethodGet, path: "/api/trade/unity", signed: false},
	OpTransferSend:  {method: http.MethodPost, path: "/api/trans/send", signed: true},
	OpTransferQuery: {method: http.MethodGet, path: "/api/trans/query", signed: true},
	OpChannelList:   {method: http.MethodGet, path: "/api/merchant/channels", signed: true},
}

// httpTransport - HTTP 链路实现
// 平台响应恒为 HTTP 200 + 信封 {code,msg,data}，业务成败只看 body.code。
type httpTransport struct {
	// endpoint - 平台地址（已裁剪末尾 /）
	endpoint string
	// mchId - 商户号（进 query 与签名）
	mchId string
	// secret - 商户密钥
	secret string
	// signer - 签名器
	signer sign.Signer
	// client - HTTP 客户端
	client *http.Client
	// userAgent - 请求 User-Agent
	userAgent string
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
		mchId:     config.MchId,
		secret:    config.Secret,
		signer:    signer,
		client:    httpClient,
		userAgent: config.UserAgent,
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

	// 签名写入 body 的 sign 字段（POST）或 query 的 sign 参数（GET）
	if route.signed {
		params[signField] = this.signer.Sign(params, this.secret)
	}

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

// request - 组装 HTTP 请求（GET 拼 query，POST 拼 JSON body）
func (this *httpTransport) request(ctx context.Context, route httpRoute, params map[string]any) (*http.Request, error) {

	address := this.endpoint + route.path

	if route.method == http.MethodGet {
		query := make(url.Values, len(params))
		for key, value := range params {
			if text := sign.Value(value); text != "" {
				query.Set(key, text)
			}
		}
		if encoded := query.Encode(); encoded != "" {
			address += "?" + encoded
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, fmt.Errorf("%w：构造 GET 请求失败（%v）", types.ErrTransport, err)
		}
		request.Header.Set("User-Agent", this.userAgent)

		return request, nil
	}

	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%w：请求序列化失败（%v）", types.ErrInvalidRequest, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w：构造 POST 请求失败（%v）", types.ErrTransport, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", this.userAgent)

	return request, nil
}

// payload - 组装签名参数集（按操作注入服务端默认值；签名由调用方在组装后追加）
// 与 gRPC 链路共用同一批 Normalize() 方法与同一个签名器，保证两链路签名语义一致。
func (this *httpTransport) payload(op Operation, req any) (map[string]any, error) {

	switch op {

	case OpTradeCreate:
		in, ok := req.(*types.TradeCreateRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：下单请求类型不匹配", types.ErrInvalidRequest)
		}
		value := *in
		value.Normalize()
		return this.signedParams(value)

	case OpTradeQuery:
		in, ok := req.(*types.TradeQueryRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：查单请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)

	case OpCheckout:
		in, ok := req.(*types.CheckoutRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：收银请求类型不匹配", types.ErrInvalidRequest)
		}
		value := *in
		value.Normalize()
		// 收银端点为公开端点、平台不验签，且请求体无 mchId 字段，故不注入商户号
		return structToMap(value)

	case OpTransferSend:
		in, ok := req.(*types.TransferRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：转账请求类型不匹配", types.ErrInvalidRequest)
		}
		value := *in
		value.Normalize()
		return this.signedParams(value)

	case OpTransferQuery:
		in, ok := req.(*types.TransferQueryRequest)
		if !ok || in == nil {
			return nil, fmt.Errorf("%w：转账查询请求类型不匹配", types.ErrInvalidRequest)
		}
		return this.signedParams(*in)

	case OpChannelList:
		return map[string]any{mchIdField: this.mchId}, nil
	}

	return nil, fmt.Errorf("%w：未知操作 %s", types.ErrInvalidRequest, op)
}

// signedParams - 请求结构体展开为签名参数集并注入商户号
func (this *httpTransport) signedParams(req any) (map[string]any, error) {

	params, err := structToMap(req)
	if err != nil {
		return nil, err
	}
	params[mchIdField] = this.mchId

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
		return fmt.Errorf("openpay: 响应不是合法 JSON（%v）", err)
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
		return fmt.Errorf("openpay: 响应 data 解析失败（%v）", err)
	}

	return nil
}

// ============================= HTTP 链路 - 结束 =============================
