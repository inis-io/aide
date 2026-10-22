package transport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/inis-io/aide/openpay/types"

	merchantv1 "github.com/inis-io/aide/openpay/proto/gen/openpay/merchant/v1"
)

// ============================= gRPC 链路测试 - 开始 =============================

// TestGRPCCreateTrade - 下单：metadata 签名等于服务端黄金向量 + 响应转模型
func TestGRPCCreateTrade(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	result := &types.TradeCreateResult{}
	if err := link.Invoke(context.Background(), OpTradeCreate, newTradeCreateRequest(), result); err != nil {
		t.Fatalf("下单失败：%v", err)
	}

	record := fake.snapshot()
	if record.method != merchantv1.MerchantService_CreateTrade_FullMethodName {
		t.Fatalf("RPC 方法不符合预期：%s", record.method)
	}
	if record.mchId != testMchId {
		t.Fatalf("metadata 商户号不符合预期：%s", record.mchId)
	}
	if record.sign != goldenCreateTrade {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", record.sign, goldenCreateTrade)
	}

	request, ok := record.request.(*merchantv1.CreateTradeRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期")
	}
	if request.GetCurrency() != "CNY" || request.GetExpired() != 900 {
		t.Fatalf("默认值未注入消息体：%+v", request)
	}
	if request.GetMchId() != testMchId {
		t.Fatalf("消息体未携带商户号")
	}

	if result.Trade.TradeNo != "202608151200000001" || result.Trade.ExpireAt != 1755245700000 {
		t.Fatalf("订单行转换失败：%+v", result.Trade)
	}
	if len(result.Gateways) != 1 || result.Gateways[0].Name != "支付宝PC" {
		t.Fatalf("网关转换失败：%+v", result.Gateways)
	}
	if result.Merchant.MchId != testMchId {
		t.Fatalf("商户信息转换失败：%+v", result.Merchant)
	}
}

// TestGRPCCheckout - 收银：gRPC 链路要求签名（比 HTTP 更严），默认值进消息体
func TestGRPCCheckout(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	result := &types.CheckoutResult{}
	request := &types.CheckoutRequest{TradeNo: "202608151200000001"}
	if err := link.Invoke(context.Background(), OpCheckout, request, result); err != nil {
		t.Fatalf("收银失败：%v", err)
	}

	record := fake.snapshot()
	if record.sign == "" {
		t.Fatalf("gRPC 收银必须携带签名")
	}

	message, ok := record.request.(*merchantv1.CheckoutRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期")
	}
	if message.GetScene() != "alipay" || message.GetWay() != "auto" || !message.GetGenUrl() {
		t.Fatalf("默认值未注入消息体：%+v", message)
	}
	if result.Trade.Url == "" {
		t.Fatalf("支付链接转换失败：%+v", result.Trade)
	}
}

// TestGRPCOtherOperations - 查单/转账/转账查询/通道列表的签名与转换
func TestGRPCOtherOperations(t *testing.T) {

	ctx := context.Background()
	fake := &fakeMerchantService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	trade := &types.Trade{}
	if err := link.Invoke(ctx, OpTradeQuery, &types.TradeQueryRequest{TradeNo: "202608151200000001"}, trade); err != nil {
		t.Fatalf("查单失败：%v", err)
	}
	if record := fake.snapshot(); record.method != merchantv1.MerchantService_QueryTrade_FullMethodName {
		t.Fatalf("查单 RPC 方法不符合预期：%s", record.method)
	}
	if trade.Status != "paid" || trade.CreateAt != 1755244800000 {
		t.Fatalf("订单行转换失败：%+v", trade)
	}

	transfer := &types.TransferResult{}
	if err := link.Invoke(ctx, OpTransferSend, newTransferRequest(), transfer); err != nil {
		t.Fatalf("转账失败：%v", err)
	}
	if transfer.Trans.PayeeType != "ALIPAY_LOGON_ID" || transfer.Trans.Status != "pending" {
		t.Fatalf("转账单转换失败：%+v", transfer.Trans)
	}

	trans := &types.Trans{}
	if err := link.Invoke(ctx, OpTransferQuery, &types.TransferQueryRequest{OutTradeNo: "TOUT20260815001"}, trans); err != nil {
		t.Fatalf("转账查询失败：%v", err)
	}
	if trans.Status != "success" {
		t.Fatalf("转账单转换失败：%+v", trans)
	}

	channels := &[]types.Channel{}
	if err := link.Invoke(ctx, OpChannelList, nil, channels); err != nil {
		t.Fatalf("通道列表失败：%v", err)
	}
	if len(*channels) != 1 || (*channels)[0].Code != "ALIPAY_PC" || len((*channels)[0].Gateways) != 1 {
		t.Fatalf("通道转换失败：%+v", *channels)
	}
}

// TestGRPCUnauthenticated - 平台拒绝签名 → ErrInvalidSign（文案带 codes.Code）
func TestGRPCUnauthenticated(t *testing.T) {

	// 假服务用错误密钥验签，模拟平台拒绝
	fake := &fakeMerchantService{secret: testSecret + "-wrong"}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpTradeCreate, newTradeCreateRequest(), &types.TradeCreateResult{})
	if !errors.Is(err, types.ErrInvalidSign) {
		t.Fatalf("应包装 ErrInvalidSign，实际：%v", err)
	}
	if !strings.Contains(err.Error(), codes.Unauthenticated.String()) {
		t.Fatalf("错误文案应带 codes.Code：%v", err)
	}
	if errors.Is(err, types.ErrTransport) {
		t.Fatalf("签名失败不应归为 ErrTransport")
	}
}

// TestGRPCAPIError - 信封 code!=200 → *types.APIError
func TestGRPCAPIError(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	fake.createTrade = func(ctx context.Context, req *merchantv1.CreateTradeRequest) (*merchantv1.CreateTradeResponse, error) {
		return &merchantv1.CreateTradeResponse{Envelope: &merchantv1.Envelope{Code: 409, Msg: "商户订单号重复！"}}, nil
	}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpTradeCreate, newTradeCreateRequest(), &types.TradeCreateResult{})

	var apiErr *types.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *types.APIError，实际：%v", err)
	}
	if apiErr.Code != 409 || apiErr.Msg != "商户订单号重复！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
	if len(apiErr.Raw) != 0 {
		t.Fatalf("gRPC 链路不应携带原始报文")
	}
}

// TestGRPCStatusError - 非 Unauthenticated 的 RPC 失败 → ErrTransport
func TestGRPCStatusError(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	fake.listChannels = func(ctx context.Context, req *merchantv1.ListChannelsRequest) (*merchantv1.ListChannelsResponse, error) {
		return nil, status.Error(codes.Internal, "内部错误")
	}
	link := startFakeGRPC(t, fake)

	err := link.Invoke(context.Background(), OpChannelList, nil, &[]types.Channel{})
	if !errors.Is(err, types.ErrTransport) {
		t.Fatalf("应包装 ErrTransport，实际：%v", err)
	}
	if !strings.Contains(err.Error(), codes.Internal.String()) {
		t.Fatalf("错误文案应带 codes.Code：%v", err)
	}
}

// TestGRPCTargetNormalization - 平台地址归一为拨号目标
func TestGRPCTargetNormalization(t *testing.T) {

	tests := []struct {
		// endpoint - 平台地址
		endpoint string
		// want - 拨号目标
		want string
	}{
		{endpoint: "http://127.0.0.1:2000", want: "127.0.0.1:2000"},
		{endpoint: "http://127.0.0.1:2000/", want: "127.0.0.1:2000"},
		{endpoint: "https://pay.example.com", want: "pay.example.com:443"},
		{endpoint: "https://pay.example.com/", want: "pay.example.com:443"},
		{endpoint: "https://pay.example.com/some/path", want: "pay.example.com:443"},
		{endpoint: "127.0.0.1:2000", want: "127.0.0.1:2000"},
	}

	for _, test := range tests {
		if got := GRPCTarget(test.endpoint); got != test.want {
			t.Fatalf("%s：got=%s want=%s", test.endpoint, got, test.want)
		}
	}
}

// TestGRPCUnknownOperation - 未知操作/请求类型不匹配归为 ErrInvalidRequest
func TestGRPCUnknownOperation(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	if err := link.Invoke(context.Background(), Operation("unknown"), nil, nil); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("未知操作应返回 ErrInvalidRequest，实际：%v", err)
	}
	if err := link.Invoke(context.Background(), OpTradeQuery, &types.CheckoutRequest{}, &types.Trade{}); !errors.Is(err, types.ErrInvalidRequest) {
		t.Fatalf("请求类型不匹配应返回 ErrInvalidRequest，实际：%v", err)
	}
}

// TestGRPCCloseIdempotent - 连接关闭幂等
func TestGRPCCloseIdempotent(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	link := startFakeGRPC(t, fake)

	if err := link.Close(); err != nil {
		t.Fatalf("首次关闭失败：%v", err)
	}
	if err := link.Close(); err != nil {
		t.Fatalf("重复关闭不应报错：%v", err)
	}
}

// TestNewGRPC - 按配置装配（拨号惰性、可正常关闭）
func TestNewGRPC(t *testing.T) {

	link, err := NewGRPC(Config{
		Endpoint:  "http://127.0.0.1:2000",
		MchId:     testMchId,
		Secret:    testSecret,
		UserAgent: "openpay-go/0.1.0",
	})
	if err != nil {
		t.Fatalf("装配失败：%v", err)
	}
	if err = link.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
}

// ============================= gRPC 链路测试 - 结束 =============================
