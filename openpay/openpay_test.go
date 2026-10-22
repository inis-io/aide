package openpay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/inis-io/aide/openpay/internal/transport"
	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"
)

// ============================= 别名等价性（编译期） - 开始 =============================

// 根包别名必须与子包类型完全等价：商户只 import 根包即可，任何破坏都会在编译期暴露
var (
	_ types.Trade                = Trade{}
	_ types.Gateway              = Gateway{}
	_ types.MerchantBrief        = MerchantBrief{}
	_ types.TradeCreateRequest   = TradeCreateRequest{}
	_ types.TradeCreateResult    = TradeCreateResult{}
	_ types.TradeQueryRequest    = TradeQueryRequest{}
	_ types.CheckoutRequest      = CheckoutRequest{}
	_ types.CheckoutResult       = CheckoutResult{}
	_ types.Trans                = Trans{}
	_ types.TransferRequest      = TransferRequest{}
	_ types.TransferResult       = TransferResult{}
	_ types.TransferQueryRequest = TransferQueryRequest{}
	_ types.Channel              = Channel{}
	_ types.NotifyPayload        = NotifyPayload{}
	_ *types.APIError            = (*APIError)(nil)
	_ Signer                     = sign.New()
)

// ============================= 别名等价性（编译期） - 结束 =============================

// ============================= 门面测试 - 开始 =============================

// newTradeCreateRequest - 门面层下单请求
func newTradeCreateRequest() *TradeCreateRequest {

	return &TradeCreateRequest{
		Amount:      12.30,
		ChannelCode: "ALIPAY_PC",
		Describe:    "测试订单",
		OutTradeNo:  "OUT20260815001",
		NotifyUrl:   "https://example.com/notify",
		ReturnUrl:   "https://example.com/return",
	}
}

// TestNewClientDefaultTransport - 默认链路为 gRPC
func TestNewClientDefaultTransport(t *testing.T) {

	client, err := NewClient("M1000001", "secret-abc", WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if name := reflect.TypeOf(client.transport).String(); name != "*transport.grpcTransport" {
		t.Fatalf("默认链路应为 gRPC，实际：%s", name)
	}
	if client.mchId != "M1000001" || client.secret != "secret-abc" {
		t.Fatalf("商户凭证未装配")
	}
	if client.timeout != defaultTimeout {
		t.Fatalf("默认超时不符合预期：%v", client.timeout)
	}
	if client.userAgent != "openpay-go/0.1.0" {
		t.Fatalf("默认 User-Agent 不符合预期：%s", client.userAgent)
	}
}

// TestNewClientTransportHTTP - WithTransportHTTP 切换链路
func TestNewClientTransportHTTP(t *testing.T) {

	client, err := NewClient("M1000001", "secret-abc", WithEndpoint("http://127.0.0.1:2000/"), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if name := reflect.TypeOf(client.transport).String(); name != "*transport.httpTransport" {
		t.Fatalf("链路应为 HTTP，实际：%s", name)
	}
	if client.endpoint != "http://127.0.0.1:2000" {
		t.Fatalf("endpoint 未裁剪末尾斜杠：%s", client.endpoint)
	}
}

// TestNewClientValidation - 必填参数校验（构造不发起业务请求）
func TestNewClientValidation(t *testing.T) {

	if _, err := NewClient("M1000001", "secret-abc"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少 endpoint 应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient("M1000001", "secret-abc", WithEndpoint("   ")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("endpoint 为空白应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient("", "secret-abc", WithEndpoint("http://127.0.0.1:2000")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少商户号应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient("M1000001", "", WithEndpoint("http://127.0.0.1:2000")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少密钥应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// TestClientOptions - 超时/User-Agent/自定义签名器/自定义客户端的选项行为
func TestClientOptions(t *testing.T) {

	client, err := NewClient("M1000001", "secret-abc",
		WithEndpoint("http://127.0.0.1:2000"),
		WithTransportHTTP(),
		WithTimeout(3*time.Second),
		WithUserAgent(" merchant-agent/1.0.0 "),
		WithHTTPClient(&http.Client{}),
		nil,
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if client.timeout != 3*time.Second {
		t.Fatalf("超时选项未生效：%v", client.timeout)
	}
	if client.userAgent != "merchant-agent/1.0.0" {
		t.Fatalf("User-Agent 选项未生效：%q", client.userAgent)
	}
	if client.httpClient == nil {
		t.Fatalf("自定义 HTTP 客户端未生效")
	}
	if _, ok := client.signer.(*sign.MD5); !ok {
		t.Fatalf("默认签名器应为 MD5，实际：%T", client.signer)
	}

	// <= 0 的超时与空白 User-Agent 被忽略；nil 签名器被忽略
	client, err = NewClient("M1000001", "secret-abc",
		WithEndpoint("http://127.0.0.1:2000"),
		WithTimeout(0),
		WithUserAgent("  "),
		WithSigner(nil),
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if client.timeout != defaultTimeout || client.userAgent != defaultUserAgent {
		t.Fatalf("零值选项应回退默认值：timeout=%v ua=%s", client.timeout, client.userAgent)
	}
}

// TestClientBusinessValidation - 业务方法本地参数校验（不发请求）
func TestClientBusinessValidation(t *testing.T) {

	ctx := context.Background()
	client, err := NewClient("M1000001", "secret-abc", WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	checks := map[string]error{}

	_, checks["下单-nil"] = client.CreateTrade(ctx, nil)
	_, checks["下单-缺渠道"] = client.CreateTrade(ctx, &TradeCreateRequest{Amount: 1})
	_, checks["下单-金额过小"] = client.CreateTrade(ctx, &TradeCreateRequest{Amount: 0, ChannelCode: "ALIPAY_PC"})
	_, checks["查单-nil"] = client.QueryTrade(ctx, nil)
	_, checks["查单-无单号"] = client.QueryTrade(ctx, &TradeQueryRequest{})
	_, checks["收银-缺单号"] = client.GetCheckout(ctx, &CheckoutRequest{})
	_, checks["转账-nil"] = client.SendTransfer(ctx, nil)
	_, checks["转账-缺收款账号"] = client.SendTransfer(ctx, &TransferRequest{ChannelCode: "ALIPAY_TRANS", Amount: 1})
	_, checks["转账查询-nil"] = client.QueryTransfer(ctx, nil)

	for name, err := range checks {
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s：应返回 ErrInvalidRequest，实际：%v", name, err)
		}
	}
}

// TestClientClose - Close 为幂等操作（HTTP 链路同样安全）
func TestClientClose(t *testing.T) {

	client, err := NewClient("M1000001", "secret-abc", WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("首次关闭失败：%v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("重复关闭不应报错：%v", err)
	}

	var nilClient *Client
	if err = nilClient.Close(); err != nil {
		t.Fatalf("nil 客户端关闭不应报错：%v", err)
	}
	if err = nilClient.invoke(context.Background(), transport.OpChannelList, nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil 客户端调用应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// TestNotifyForwarding - ParseNotify / VerifyNotify 转发可用
func TestNotifyForwarding(t *testing.T) {

	if _, err := ParseNotify([]byte(`{"tradeNo":"T1"}`), "secret-abc"); !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("缺 sign 应返回 ErrInvalidSign，实际：%v", err)
	}
	if VerifyNotify([]byte("not-json"), "secret-abc") {
		t.Fatalf("非法报文 VerifyNotify 应返回 false")
	}
	if !errors.Is(ErrTransport, types.ErrTransport) {
		t.Fatalf("哨兵错误未正确转发")
	}
}

// TestClientEndToEndHTTP - 门面 → HTTP 链路端到端：六个业务方法全部走通
// （同时覆盖默认值注入、信封解析与 row → 模型映射）
func TestClientEndToEndHTTP(t *testing.T) {

	ctx := context.Background()
	body := make(map[string]any)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {

		body = make(map[string]any)
		if raw, _ := io.ReadAll(request.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}

		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/trade/unity":
			if request.Method == http.MethodPost {
				_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":{"row":{"tradeNo":"T1","status":"created"}}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":{"trade":{"tradeNo":"T1","url":"https://pay.example.com/cash/T1"}}}`))
		case "/api/trade/query":
			_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":{"tradeNo":"T1","status":"paid"}}`))
		case "/api/trans/send":
			_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":{"row":{"tradeNo":"TT1","status":"pending"}}}`))
		case "/api/trans/query":
			_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":{"tradeNo":"TT1","status":"success"}}`))
		case "/api/merchant/channels":
			_, _ = writer.Write([]byte(`{"code":200,"msg":"OK","data":[{"code":"ALIPAY_PC"}]}`))
		default:
			_, _ = writer.Write([]byte(`{"code":404,"msg":"路由不存在","data":null}`))
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient("M1000001", "secret-abc", WithEndpoint(server.URL), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	created, err := client.CreateTrade(ctx, newTradeCreateRequest())
	if err != nil {
		t.Fatalf("下单失败：%v", err)
	}
	if created.Trade.TradeNo != "T1" {
		t.Fatalf("订单行映射失败：%+v", created.Trade)
	}
	if body["currency"] != "CNY" || body["expired"] != float64(900) {
		t.Fatalf("默认值未注入：%v", body)
	}
	if signature, _ := body["sign"].(string); signature == "" {
		t.Fatalf("请求未携带签名")
	}

	if trade, err := client.QueryTrade(ctx, &TradeQueryRequest{TradeNo: "T1"}); err != nil || trade.Status != "paid" {
		t.Fatalf("查单失败：%v %+v", err, trade)
	}
	if checkout, err := client.GetCheckout(ctx, &CheckoutRequest{TradeNo: "T1"}); err != nil || checkout.Trade.Url == "" {
		t.Fatalf("收银失败：%v %+v", err, checkout)
	}
	if transfer, err := client.SendTransfer(ctx, &TransferRequest{
		ChannelCode: "ALIPAY_TRANS", Amount: 1, PayeeAccount: "payee@example.com",
	}); err != nil || transfer.Trans.TradeNo != "TT1" {
		t.Fatalf("转账失败：%v %+v", err, transfer)
	}
	if trans, err := client.QueryTransfer(ctx, &TransferQueryRequest{OutTradeNo: "TOUT1"}); err != nil || trans.Status != "success" {
		t.Fatalf("转账查询失败：%v %+v", err, trans)
	}
	channels, err := client.ListChannels(ctx)
	if err != nil || len(channels) != 1 || channels[0].Code != "ALIPAY_PC" {
		t.Fatalf("通道列表失败：%v %+v", err, channels)
	}
}

// TestClientAPIError - 平台业务错误经门面透出为根包 *APIError
func TestClientAPIError(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":400,"msg":"签名验证失败！","data":null}`))
	}))
	t.Cleanup(server.Close)

	client, err := NewClient("M1000001", "secret-abc", WithEndpoint(server.URL), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.QueryTrade(context.Background(), &TradeQueryRequest{TradeNo: "T1"})

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError，实际：%v", err)
	}
	if apiErr.Code != 400 || apiErr.Msg != "签名验证失败！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
}

// stubSigner - 自定义签名器桩（验证 WithSigner 注入）
type stubSigner struct{}

// Sign - 固定返回 STUB
func (this *stubSigner) Sign(params map[string]any, secret string) string { return "STUB" }

// Verify - 仅接受 STUB
func (this *stubSigner) Verify(params map[string]any, secret, digest string) bool {
	return digest == "STUB"
}

// TestClientSignerAndDialOpts - 自定义签名器与 gRPC 拨号选项可注入
func TestClientSignerAndDialOpts(t *testing.T) {

	signer := &stubSigner{}
	client, err := NewClient("M1000001", "secret-abc",
		WithEndpoint("http://127.0.0.1:2000"),
		WithSigner(signer),
		WithGRPCDialOpts(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if client.signer != Signer(signer) {
		t.Fatalf("自定义签名器未生效")
	}
	if len(client.dialOpts) != 1 {
		t.Fatalf("拨号选项未透传：%d", len(client.dialOpts))
	}
}

// ============================= 门面测试 - 结束 =============================
