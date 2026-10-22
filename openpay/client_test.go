package openpay

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// ============================= 客户端测试 - 开始 =============================

// newTradeCreateRequest - 与黄金向量一致的下单请求
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

// newTransferRequest - 与黄金向量一致的转账请求
func newTransferRequest() *TransferRequest {

	return &TransferRequest{
		ChannelCode:  "ALIPAY_TRANS",
		Scene:        "alipay",
		Amount:       88.80,
		PayeeAccount: "payee@example.com",
		PayeeName:    "张三",
		Describe:     "服务报酬",
		OutTradeNo:   "TOUT20260815001",
	}
}

// TestNewClientDefaultTransport - 默认链路为 gRPC
func TestNewClientDefaultTransport(t *testing.T) {

	client, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, ok := client.transport.(*grpcTransport); !ok {
		t.Fatalf("默认链路应为 gRPC，实际：%T", client.transport)
	}
	if client.mchId != testMchId || client.secret != testSecret {
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

	client, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000/"), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if _, ok := client.transport.(*httpTransport); !ok {
		t.Fatalf("链路应为 HTTP，实际：%T", client.transport)
	}
	if client.endpoint != "http://127.0.0.1:2000" {
		t.Fatalf("endpoint 未裁剪末尾斜杠：%s", client.endpoint)
	}
}

// TestNewClientValidation - 必填参数校验（构造不发起业务请求）
func TestNewClientValidation(t *testing.T) {

	if _, err := NewClient(testMchId, testSecret); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少 endpoint 应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient(testMchId, testSecret, WithEndpoint("   ")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("endpoint 为空白应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient("", testSecret, WithEndpoint("http://127.0.0.1:2000")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少商户号应返回 ErrInvalidRequest，实际：%v", err)
	}
	if _, err := NewClient(testMchId, "", WithEndpoint("http://127.0.0.1:2000")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("缺少密钥应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// TestClientOptions - 超时/User-Agent/自定义客户端的选项行为
func TestClientOptions(t *testing.T) {

	client, err := NewClient(testMchId, testSecret,
		WithEndpoint("http://127.0.0.1:2000"),
		WithTransportHTTP(),
		WithTimeout(3_000_000_000),
		WithUserAgent(" merchant-agent/1.0.0 "),
		WithHTTPClient(&http.Client{}),
		nil,
	)
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if client.timeout != 3_000_000_000 {
		t.Fatalf("超时选项未生效：%v", client.timeout)
	}
	if client.userAgent != "merchant-agent/1.0.0" {
		t.Fatalf("User-Agent 选项未生效：%q", client.userAgent)
	}
	if client.httpClient == nil {
		t.Fatalf("自定义 HTTP 客户端未生效")
	}

	// <= 0 的超时与空白 User-Agent 被忽略
	client, err = NewClient(testMchId, testSecret,
		WithEndpoint("http://127.0.0.1:2000"),
		WithTimeout(0),
		WithUserAgent("  "),
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
	client, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	checks := map[string]error{}

	_, checks["下单-nil"] = client.CreateTrade(ctx, nil)
	_, checks["下单-缺渠道"] = client.CreateTrade(ctx, &TradeCreateRequest{Amount: 1})
	_, checks["下单-金额过小"] = client.CreateTrade(ctx, &TradeCreateRequest{Amount: 0, ChannelCode: "ALIPAY_PC"})
	_, checks["查单-空条件"] = client.QueryTrade(ctx, nil)
	_, checks["查单-无单号"] = client.QueryTrade(ctx, &TradeQueryRequest{})
	_, checks["收银-缺单号"] = client.GetCheckout(ctx, &CheckoutRequest{})
	_, checks["转账-nil"] = client.SendTransfer(ctx, nil)
	_, checks["转账-缺收款账号"] = client.SendTransfer(ctx, &TransferRequest{ChannelCode: "ALIPAY_TRANS", Amount: 1})
	_, checks["转账查询-空条件"] = client.QueryTransfer(ctx, nil)

	for name, err := range checks {
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%s：应返回 ErrInvalidRequest，实际：%v", name, err)
		}
	}
}

// TestClientClose - Close 为幂等操作（HTTP 链路同样安全）
func TestClientClose(t *testing.T) {

	client, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("首次关闭失败：%v", err)
	}
	if err = client.Close(); err != nil {
		t.Fatalf("重复关闭不应报错：%v", err)
	}

	httpClient, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000"), WithTransportHTTP())
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}
	if err = httpClient.Close(); err != nil {
		t.Fatalf("HTTP 链路关闭不应报错：%v", err)
	}

	var nilClient *Client
	if err = nilClient.Close(); err != nil {
		t.Fatalf("nil 客户端关闭不应报错：%v", err)
	}
	if err = nilClient.invoke(context.Background(), OpChannelList, nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil 客户端调用应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// ============================= 客户端测试 - 结束 =============================
