package openpay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ============================= HTTP 链路测试 - 开始 =============================

const (
	// testMchId - 测试商户号（与黄金向量一致）
	testMchId = "M1000001"
	// testSecret - 测试商户密钥（与黄金向量一致）
	testSecret = goldenSecret
)

// httpCapture - 假平台捕获到的请求
type httpCapture struct {
	// method - HTTP 方法
	method string
	// path - 请求路径
	path string
	// query - 查询参数
	query url.Values
	// body - 解析后的 JSON 请求体
	body map[string]any
	// raw - 原始请求体
	raw []byte
}

// startHTTPPlatform - 启动假平台（httptest），reply 决定响应信封
func startHTTPPlatform(t *testing.T, reply func(capture *httpCapture) (int, string)) (*httptest.Server, *httpCapture) {

	t.Helper()

	capture := &httpCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("读取请求体失败：%v", err)
		}

		capture.method = request.Method
		capture.path = request.URL.Path
		capture.query = request.URL.Query()
		capture.raw = raw
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &capture.body); err != nil {
				t.Errorf("请求体不是合法 JSON：%v", err)
			}
		}

		code, body := reply(capture)
		// 平台约定：HTTP 状态恒 200，业务结果只看 body.code
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(code)
		_, _ = writer.Write([]byte(body))
	}))

	t.Cleanup(server.Close)

	return server, capture
}

// startHTTPClient - 构造 HTTP 链路客户端
func startHTTPClient(t *testing.T, endpoint string) *Client {

	t.Helper()

	client, err := NewClient(testMchId, testSecret, WithEndpoint(endpoint), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// jsonReply - 组装平台信封响应
func jsonReply(t *testing.T, code int, msg string, data any) string {

	t.Helper()

	text, err := json.Marshal(map[string]any{"code": code, "msg": msg, "data": data})
	if err != nil {
		t.Fatalf("构造响应失败：%v", err)
	}

	return string(text)
}

// queryParams - query 转签名参数集（剔除 sign）
func queryParams(query url.Values) map[string]any {

	params := make(map[string]any, len(query))
	for key := range query {
		if key == signField {
			continue
		}
		params[key] = query.Get(key)
	}

	return params
}

// TestHTTPTransportCreateTrade - 下单：默认值注入 + 签名等于服务端黄金向量 + row 映射
func TestHTTPTransportCreateTrade(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"row": map[string]any{
				"tradeNo":    "202608151200000001",
				"outTradeNo": "OUT20260815001",
				"amount":     12.3,
				"currency":   "CNY",
				"status":     "created",
				"expireAt":   1755245700000,
			},
			"gateways": []map[string]any{
				{"id": 1, "name": "支付宝PC", "scene": "alipay", "status": "normal", "isSandbox": 0, "costRate": 0.006},
			},
			"merchant": map[string]any{"name": "演示商户", "mchId": testMchId},
		})
	})

	client := startHTTPClient(t, server.URL)

	result, err := client.CreateTrade(context.Background(), &TradeCreateRequest{
		Amount:      12.30,
		ChannelCode: "ALIPAY_PC",
		Describe:    "测试订单",
		OutTradeNo:  "OUT20260815001",
		NotifyUrl:   "https://example.com/notify",
		ReturnUrl:   "https://example.com/return",
	})
	if err != nil {
		t.Fatalf("下单失败：%v", err)
	}

	if capture.method != http.MethodPost || capture.path != "/api/trade/unity" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.body["currency"] != "CNY" {
		t.Fatalf("未注入默认货币：%v", capture.body["currency"])
	}
	if expired, ok := capture.body["expired"].(float64); !ok || expired != 900 {
		t.Fatalf("未注入默认有效期：%v", capture.body["expired"])
	}

	sign, _ := capture.body["sign"].(string)
	if sign != goldenCreateTrade {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", sign, goldenCreateTrade)
	}
	if got := referenceSign(capture.body, testSecret); got != sign {
		t.Fatalf("服务端复算签名不一致：got=%s want=%s", got, sign)
	}

	if result.Trade.TradeNo != "202608151200000001" || result.Trade.Status != "created" {
		t.Fatalf("订单行映射失败：%+v", result.Trade)
	}
	if result.Trade.ExpireAt != 1755245700000 {
		t.Fatalf("毫秒时间戳映射失败：%d", result.Trade.ExpireAt)
	}
	if len(result.Gateways) != 1 || result.Gateways[0].Scene != "alipay" || result.Gateways[0].CostRate != 0.006 {
		t.Fatalf("网关映射失败：%+v", result.Gateways)
	}
	if result.Merchant.MchId != testMchId {
		t.Fatalf("商户映射失败：%+v", result.Merchant)
	}
}

// TestHTTPTransportQueryTrade - 查单：GET query 携带 mchId 与 sign，data 即订单行
func TestHTTPTransportQueryTrade(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"tradeNo":    "202608151200000001",
			"outTradeNo": "OUT20260815001",
			"amount":     12.3,
			"currency":   "CNY",
			"status":     "paid",
			"createAt":   1755244800000,
		})
	})

	client := startHTTPClient(t, server.URL)

	trade, err := client.QueryTrade(context.Background(), &TradeQueryRequest{TradeNo: "202608151200000001"})
	if err != nil {
		t.Fatalf("查单失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/trade/query" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Get(mchIdField) != testMchId {
		t.Fatalf("query 缺少商户号")
	}
	if capture.query.Has("outTradeNo") {
		t.Fatalf("空值参数不应出现在 query 中")
	}
	if sign := capture.query.Get(signField); sign != goldenTradeQueryNo {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", sign, goldenTradeQueryNo)
	}
	if got := referenceSign(queryParams(capture.query), testSecret); got != capture.query.Get(signField) {
		t.Fatalf("服务端复算签名不一致：got=%s", got)
	}

	if trade.Status != "paid" || trade.CreateAt != 1755244800000 {
		t.Fatalf("订单行映射失败：%+v", trade)
	}
}

// TestHTTPTransportCheckout - 收银：公开端点不签名、不携带 mchId，默认值注入 scene/way/genUrl
func TestHTTPTransportCheckout(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "获取支付地址成功！", map[string]any{
			"trade":    map[string]any{"tradeNo": "202608151200000001", "url": "https://pay.example.com/cash-desk/202608151200000001"},
			"gateways": []map[string]any{},
			"merchant": map[string]any{"mchId": testMchId, "name": "演示商户"},
		})
	})

	client := startHTTPClient(t, server.URL)

	result, err := client.GetCheckout(context.Background(), &CheckoutRequest{TradeNo: "202608151200000001"})
	if err != nil {
		t.Fatalf("收银失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/trade/unity" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Has(signField) {
		t.Fatalf("收银端点为公开端点，不应携带签名")
	}
	if capture.query.Has(mchIdField) {
		t.Fatalf("收银端点请求体无 mchId 字段，不应携带商户号")
	}
	if capture.query.Get("scene") != "alipay" || capture.query.Get("way") != "auto" || capture.query.Get("genUrl") != "true" {
		t.Fatalf("默认值未注入：%v", capture.query)
	}
	if result.Trade.Url == "" {
		t.Fatalf("支付链接映射失败：%+v", result.Trade)
	}
}

// TestHTTPTransportSendTransfer - 转账发起：默认值 payeeType + row 映射
func TestHTTPTransportSendTransfer(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "转账请求已受理！", map[string]any{
			"row": map[string]any{
				"tradeNo":      "202608151300000001",
				"outTradeNo":   "TOUT20260815001",
				"amount":       88.8,
				"currency":     "CNY",
				"status":       "pending",
				"payeeAccount": "payee@example.com",
			},
			"merchant": map[string]any{"mchId": testMchId, "name": "演示商户"},
		})
	})

	client := startHTTPClient(t, server.URL)

	result, err := client.SendTransfer(context.Background(), &TransferRequest{
		ChannelCode:  "ALIPAY_TRANS",
		Scene:        "alipay",
		Amount:       88.80,
		PayeeAccount: "payee@example.com",
		PayeeName:    "张三",
		Describe:     "服务报酬",
		OutTradeNo:   "TOUT20260815001",
	})
	if err != nil {
		t.Fatalf("转账失败：%v", err)
	}

	if capture.method != http.MethodPost || capture.path != "/api/trans/send" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.body["payeeType"] != "ALIPAY_LOGON_ID" {
		t.Fatalf("未注入默认收款方类型：%v", capture.body["payeeType"])
	}
	if capture.body["currency"] != "CNY" {
		t.Fatalf("未注入默认货币：%v", capture.body["currency"])
	}

	sign, _ := capture.body["sign"].(string)
	if sign != goldenTransferSend {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", sign, goldenTransferSend)
	}
	if got := referenceSign(capture.body, testSecret); got != sign {
		t.Fatalf("服务端复算签名不一致：got=%s", got)
	}

	if result.Trans.TradeNo != "202608151300000001" || result.Trans.PayeeAccount != "payee@example.com" {
		t.Fatalf("转账单映射失败：%+v", result.Trans)
	}
	if result.Merchant.MchId != testMchId {
		t.Fatalf("商户映射失败：%+v", result.Merchant)
	}
}

// TestHTTPTransportQueryTransfer - 转账查询：空 tradeNo 被剔除
func TestHTTPTransportQueryTransfer(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"tradeNo":    "202608151300000001",
			"outTradeNo": "TOUT20260815001",
			"status":     "success",
			"amount":     88.8,
		})
	})

	client := startHTTPClient(t, server.URL)

	trans, err := client.QueryTransfer(context.Background(), &TransferQueryRequest{OutTradeNo: "TOUT20260815001"})
	if err != nil {
		t.Fatalf("转账查询失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/trans/query" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Has("tradeNo") {
		t.Fatalf("空值参数不应出现在 query 中")
	}
	if sign := capture.query.Get(signField); sign != goldenTransferQuery {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", sign, goldenTransferQuery)
	}
	if trans.Status != "success" {
		t.Fatalf("转账单映射失败：%+v", trans)
	}
}

// TestHTTPTransportListChannels - 通道列表：data 为数组，签名仅含 mchId
func TestHTTPTransportListChannels(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "查询成功！", []map[string]any{
			{
				"id":       1,
				"code":     "ALIPAY_PC",
				"name":     "支付宝PC",
				"currency": "CNY",
				"feeRate":  0.002,
				"status":   "normal",
				"gateways": []map[string]any{{"id": 1, "name": "支付宝", "scene": "alipay", "status": "normal"}},
			},
		})
	})

	client := startHTTPClient(t, server.URL)

	channels, err := client.ListChannels(context.Background())
	if err != nil {
		t.Fatalf("通道列表失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/merchant/channels" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if sign := capture.query.Get(signField); sign != goldenChannelList {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", sign, goldenChannelList)
	}
	if len(channels) != 1 || channels[0].Code != "ALIPAY_PC" || channels[0].FeeRate != 0.002 {
		t.Fatalf("通道映射失败：%+v", channels)
	}
	if len(channels[0].Gateways) != 1 || channels[0].Gateways[0].Scene != "alipay" {
		t.Fatalf("通道网关映射失败：%+v", channels[0].Gateways)
	}
}

// TestHTTPTransportAPIError - code!=200 → *APIError（HTTP 状态仍为 200）
func TestHTTPTransportAPIError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 400, "签名验证失败！", nil)
	})

	client := startHTTPClient(t, server.URL)

	_, err := client.QueryTrade(context.Background(), &TradeQueryRequest{OutTradeNo: "OUT20260815001"})

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError，实际：%v", err)
	}
	if apiErr.Code != 400 || apiErr.Msg != "签名验证失败！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
	if len(apiErr.Raw) == 0 {
		t.Fatalf("原始响应未保留")
	}
}

// TestHTTPTransportInvalidJSON - 平台返回非 JSON（如网关错误页）
func TestHTTPTransportInvalidJSON(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, "<html>502 Bad Gateway</html>"
	})

	client := startHTTPClient(t, server.URL)

	_, err := client.ListChannels(context.Background())
	if err == nil {
		t.Fatalf("非法 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "响应不是合法 JSON") {
		t.Fatalf("错误文案不符合预期：%v", err)
	}
}

// TestHTTPTransportNetworkError - 连接失败归一为 ErrTransport
func TestHTTPTransportNetworkError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) { return 200, `{"code":200}` })
	endpoint := server.URL
	server.Close()

	client := startHTTPClient(t, endpoint)

	_, err := client.ListChannels(context.Background())
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("网络错误应包装 ErrTransport，实际：%v", err)
	}
}

// TestHTTPTransportCustomClient - WithHTTPClient 注入的客户端生效（User-Agent 覆盖 + 超时）
func TestHTTPTransportCustomClient(t *testing.T) {

	var userAgent string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgent = request.Header.Get("User-Agent")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":[]}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(testMchId, testSecret,
		WithEndpoint(server.URL),
		WithTransportHTTP(),
		WithHTTPClient(&http.Client{Timeout: 3 * 1e9}),
		WithUserAgent("custom-agent/9.9.9"),
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, err = client.ListChannels(context.Background()); err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	if userAgent != "custom-agent/9.9.9" {
		t.Fatalf("User-Agent 未透传：%s", userAgent)
	}
}

// ============================= HTTP 链路测试 - 结束 =============================
