package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inis-io/aide/openpay/types"
)

// ============================= HTTP 链路测试 - 开始 =============================

// TestHTTPCreateTrade - 下单：默认值注入 + 签名等于服务端黄金向量 + row 映射
func TestHTTPCreateTrade(t *testing.T) {

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

	link := startHTTPTransport(t, server.URL)

	result := &types.TradeCreateResult{}
	if err := link.Invoke(context.Background(), OpTradeCreate, newTradeCreateRequest(), result); err != nil {
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

	signature, _ := capture.body[signField].(string)
	if signature != goldenCreateTrade {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", signature, goldenCreateTrade)
	}
	if got := referenceSign(capture.body, testSecret); got != signature {
		t.Fatalf("服务端复算签名不一致：got=%s want=%s", got, signature)
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

// TestHTTPQueryTrade - 查单：GET query 携带 mchId 与 sign，data 即订单行
func TestHTTPQueryTrade(t *testing.T) {

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

	link := startHTTPTransport(t, server.URL)

	trade := &types.Trade{}
	request := &types.TradeQueryRequest{TradeNo: "202608151200000001"}
	if err := link.Invoke(context.Background(), OpTradeQuery, request, trade); err != nil {
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
	if got := referenceSign(queryParams(capture.query), testSecret); got != capture.query.Get(signField) {
		t.Fatalf("服务端复算签名不一致：got=%s want=%s", got, capture.query.Get(signField))
	}

	if trade.Status != "paid" || trade.CreateAt != 1755244800000 {
		t.Fatalf("订单行映射失败：%+v", trade)
	}
}

// TestHTTPCheckout - 收银：公开端点不签名、不携带 mchId，默认值注入 scene/way/genUrl
func TestHTTPCheckout(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "获取支付地址成功！", map[string]any{
			"trade":    map[string]any{"tradeNo": "202608151200000001", "url": "https://pay.example.com/cash-desk/202608151200000001"},
			"gateways": []map[string]any{},
			"merchant": map[string]any{"mchId": testMchId, "name": "演示商户"},
		})
	})

	link := startHTTPTransport(t, server.URL)

	result := &types.CheckoutResult{}
	request := &types.CheckoutRequest{TradeNo: "202608151200000001"}
	if err := link.Invoke(context.Background(), OpCheckout, request, result); err != nil {
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

// TestHTTPSendTransfer - 转账发起：默认值 payeeType + row 映射
func TestHTTPSendTransfer(t *testing.T) {

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

	link := startHTTPTransport(t, server.URL)

	result := &types.TransferResult{}
	if err := link.Invoke(context.Background(), OpTransferSend, newTransferRequest(), result); err != nil {
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
	if got := referenceSign(capture.body, testSecret); got != capture.body[signField] {
		t.Fatalf("服务端复算签名不一致：got=%s", got)
	}

	if result.Trans.TradeNo != "202608151300000001" || result.Trans.PayeeAccount != "payee@example.com" {
		t.Fatalf("转账单映射失败：%+v", result.Trans)
	}
	if result.Merchant.MchId != testMchId {
		t.Fatalf("商户映射失败：%+v", result.Merchant)
	}
}

// TestHTTPQueryTransfer - 转账查询：空 tradeNo 被剔除
func TestHTTPQueryTransfer(t *testing.T) {

	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{
			"tradeNo":    "202608151300000001",
			"outTradeNo": "TOUT20260815001",
			"status":     "success",
			"amount":     88.8,
		})
	})

	link := startHTTPTransport(t, server.URL)

	trans := &types.Trans{}
	request := &types.TransferQueryRequest{OutTradeNo: "TOUT20260815001"}
	if err := link.Invoke(context.Background(), OpTransferQuery, request, trans); err != nil {
		t.Fatalf("转账查询失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/trans/query" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if capture.query.Has("tradeNo") {
		t.Fatalf("空值参数不应出现在 query 中")
	}
	if got := referenceSign(queryParams(capture.query), testSecret); got != capture.query.Get(signField) {
		t.Fatalf("服务端复算签名不一致：got=%s", got)
	}
	if trans.Status != "success" {
		t.Fatalf("转账单映射失败：%+v", trans)
	}
}

// TestHTTPListChannels - 通道列表：data 为数组，签名仅含 mchId
func TestHTTPListChannels(t *testing.T) {

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

	link := startHTTPTransport(t, server.URL)

	channels := &[]types.Channel{}
	if err := link.Invoke(context.Background(), OpChannelList, nil, channels); err != nil {
		t.Fatalf("通道列表失败：%v", err)
	}

	if capture.method != http.MethodGet || capture.path != "/api/merchant/channels" {
		t.Fatalf("端点不符合预期：%s %s", capture.method, capture.path)
	}
	if got := referenceSign(queryParams(capture.query), testSecret); got != capture.query.Get(signField) {
		t.Fatalf("服务端复算签名不一致：got=%s", got)
	}
	if len(*channels) != 1 || (*channels)[0].Code != "ALIPAY_PC" || (*channels)[0].FeeRate != 0.002 {
		t.Fatalf("通道映射失败：%+v", *channels)
	}
	if len((*channels)[0].Gateways) != 1 || (*channels)[0].Gateways[0].Scene != "alipay" {
		t.Fatalf("通道网关映射失败：%+v", (*channels)[0].Gateways)
	}
}

// TestHTTPAPIError - code!=200 → *types.APIError（HTTP 状态仍为 200）
func TestHTTPAPIError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 400, "签名验证失败！", nil)
	})

	link := startHTTPTransport(t, server.URL)

	request := &types.TradeQueryRequest{OutTradeNo: "OUT20260815001"}
	err := link.Invoke(context.Background(), OpTradeQuery, request, &types.Trade{})

	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *types.APIError，实际：%v", err)
	}
	if apiErr.Code != 400 || apiErr.Msg != "签名验证失败！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
	if len(apiErr.Raw) == 0 {
		t.Fatalf("原始响应未保留")
	}
}

// TestHTTPInvalidJSON - 平台返回非 JSON（如网关错误页）
func TestHTTPInvalidJSON(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, "<html>502 Bad Gateway</html>"
	})

	link := startHTTPTransport(t, server.URL)

	err := link.Invoke(context.Background(), OpChannelList, nil, &[]types.Channel{})
	if err == nil {
		t.Fatalf("非法 JSON 响应应返回错误")
	}
	if !strings.Contains(err.Error(), "响应不是合法 JSON") {
		t.Fatalf("错误文案不符合预期：%v", err)
	}
	if errors.Is(err, types.ErrTransport) {
		t.Fatalf("非法 JSON 按设计应返回原始错误而非 ErrTransport")
	}
	var apiErr *types.APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("非法 JSON 不应返回 *types.APIError")
	}
}

// TestHTTPNetworkError - 连接失败归一为 ErrTransport
func TestHTTPNetworkError(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) { return 200, `{"code":200}` })
	endpoint := server.URL
	server.Close()

	link := startHTTPTransport(t, endpoint)

	err := link.Invoke(context.Background(), OpChannelList, nil, &[]types.Channel{})
	if !errors.Is(err, types.ErrTransport) {
		t.Fatalf("网络错误应包装 ErrTransport，实际：%v", err)
	}
}

// TestHTTPCustomClient - 注入的 http.Client 生效（请求确实走它）
func TestHTTPCustomClient(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", []map[string]any{})
	})

	link := NewHTTP(Config{
		Endpoint:   server.URL,
		MchId:      testMchId,
		Secret:     testSecret,
		UserAgent:  "custom-agent/9.9.9",
		HTTPClient: &http.Client{Timeout: 3_000_000_000},
	})
	t.Cleanup(func() { _ = link.Close() })

	if err := link.Invoke(context.Background(), OpChannelList, nil, &[]types.Channel{}); err != nil {
		t.Fatalf("请求失败：%v", err)
	}
}

// TestHTTPUnknownOperation - 未知操作/请求类型不匹配归为 ErrInvalidRequest
func TestHTTPUnknownOperation(t *testing.T) {

	server, _ := startHTTPPlatform(t, func(*httpCapture) (int, string) { return 200, `{"code":200}` })
	link := startHTTPTransport(t, server.URL)

	if err := link.Invoke(context.Background(), Operation("unknown"), nil, nil); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("未知操作应返回 ErrInvalidRequest，实际：%v", err)
	}
	if err := link.Invoke(context.Background(), OpTradeCreate, &types.TransferRequest{}, &types.TradeCreateResult{}); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("请求类型不匹配应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// TestDecodeEnvelopeNilData - 成功但无 data 时保留零值
func TestDecodeEnvelopeNilData(t *testing.T) {

	resp := &types.Trade{}
	if err := decodeEnvelope([]byte(`{"code":200,"msg":"OK","data":null}`), resp); err != nil {
		t.Fatalf("data 为 null 时不应报错：%v", err)
	}
	if resp.TradeNo != "" {
		t.Fatalf("data 为 null 时应保留零值")
	}

	if err := decodeEnvelope([]byte(`{"code":200,"msg":"OK"}`), resp); err != nil {
		t.Fatalf("缺 data 时不应报错：%v", err)
	}
}

// TestHTTPUserAgent - 请求携带配置的 User-Agent
func TestHTTPUserAgent(t *testing.T) {

	var userAgent string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		userAgent = request.Header.Get("User-Agent")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":[]}`))
	}))
	t.Cleanup(server.Close)

	link := NewHTTP(Config{
		Endpoint:  server.URL,
		MchId:     testMchId,
		Secret:    testSecret,
		UserAgent: "custom-agent/9.9.9",
	})
	t.Cleanup(func() { _ = link.Close() })

	if err := link.Invoke(context.Background(), OpChannelList, nil, &[]types.Channel{}); err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	if userAgent != "custom-agent/9.9.9" {
		t.Fatalf("User-Agent 未透传：%s", userAgent)
	}
}

// ============================= HTTP 链路测试 - 结束 =============================
