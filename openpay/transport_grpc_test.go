package openpay

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	merchantv1 "github.com/inis-io/aide/openpay/proto/gen/openpay/merchant/v1"
)

// ============================= gRPC 链路测试 - 开始 =============================

// grpcRecord - 假服务捕获到的请求信息
type grpcRecord struct {
	// method - 完整 RPC 方法名
	method string
	// sign - x-openpay-sign 值
	sign string
	// mchId - x-openpay-mch-id 值
	mchId string
	// request - 请求消息
	request proto.Message
}

// fakeMerchantService - 假商户服务：校验 metadata 签名并回放响应
type fakeMerchantService struct {
	merchantv1.UnimplementedMerchantServiceServer

	// secret - 期望的商户密钥（故意配错即可触发 Unauthenticated）
	secret string
	// mutex - 保护 record（服务端 handler 并发执行）
	mutex sync.Mutex
	// record - 最近一次请求
	record grpcRecord
	// createTrade - 下单自定义响应（nil 走默认成功响应）
	createTrade func(ctx context.Context, req *merchantv1.CreateTradeRequest) (*merchantv1.CreateTradeResponse, error)
	// listChannels - 通道列表自定义响应（nil 走默认成功响应）
	listChannels func(ctx context.Context, req *merchantv1.ListChannelsRequest) (*merchantv1.ListChannelsResponse, error)
}

// CreateTrade - 统一下单
func (this *fakeMerchantService) CreateTrade(ctx context.Context, req *merchantv1.CreateTradeRequest) (*merchantv1.CreateTradeResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_CreateTrade_FullMethodName, req); err != nil {
		return nil, err
	}
	if this.createTrade != nil {
		return this.createTrade(ctx, req)
	}

	return &merchantv1.CreateTradeResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Trade: &merchantv1.Trade{
			TradeNo:    "202608151200000001",
			OutTradeNo: req.GetOutTradeNo(),
			MchId:      req.GetMchId(),
			Amount:     req.GetAmount(),
			Currency:   req.GetCurrency(),
			Status:     "created",
			ExpireAt:   1755245700000,
		},
		Gateways: []*merchantv1.Gateway{
			{Id: 1, Name: "支付宝PC", Scene: "alipay", Status: "normal", CostRate: 0.006},
		},
		Merchant: &merchantv1.MerchantBrief{Name: "演示商户", MchId: req.GetMchId()},
	}, nil
}

// QueryTrade - 商户查单
func (this *fakeMerchantService) QueryTrade(ctx context.Context, req *merchantv1.TradeQueryRequest) (*merchantv1.TradeQueryResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_QueryTrade_FullMethodName, req); err != nil {
		return nil, err
	}

	return &merchantv1.TradeQueryResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Trade: &merchantv1.Trade{
			TradeNo:  req.GetTradeNo(),
			Amount:   12.3,
			Currency: "CNY",
			Status:   "paid",
			CreateAt: 1755244800000,
		},
	}, nil
}

// GetCheckout - 收银/取支付链接
func (this *fakeMerchantService) GetCheckout(ctx context.Context, req *merchantv1.CheckoutRequest) (*merchantv1.CheckoutResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_GetCheckout_FullMethodName, req); err != nil {
		return nil, err
	}

	return &merchantv1.CheckoutResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Trade: &merchantv1.Trade{
			TradeNo: req.GetTradeNo(),
			Scene:   req.GetScene(),
			Way:     req.GetWay(),
			Url:     "https://pay.example.com/cash-desk/" + req.GetTradeNo(),
			Status:  "created",
		},
		Merchant: &merchantv1.MerchantBrief{MchId: req.GetMchId()},
	}, nil
}

// SendTransfer - 转账发起
func (this *fakeMerchantService) SendTransfer(ctx context.Context, req *merchantv1.TransferRequest) (*merchantv1.TransferResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_SendTransfer_FullMethodName, req); err != nil {
		return nil, err
	}

	return &merchantv1.TransferResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Trans: &merchantv1.Trans{
			TradeNo:      "202608151300000001",
			OutTradeNo:   req.GetOutTradeNo(),
			Amount:       req.GetAmount(),
			Currency:     req.GetCurrency(),
			Status:       "pending",
			PayeeAccount: req.GetPayeeAccount(),
			PayeeType:    req.GetPayeeType(),
		},
		Merchant: &merchantv1.MerchantBrief{MchId: req.GetMchId()},
	}, nil
}

// QueryTransfer - 转账查询
func (this *fakeMerchantService) QueryTransfer(ctx context.Context, req *merchantv1.TransferQueryRequest) (*merchantv1.TransferQueryResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_QueryTransfer_FullMethodName, req); err != nil {
		return nil, err
	}

	return &merchantv1.TransferQueryResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Trans: &merchantv1.Trans{
			TradeNo:    "202608151300000001",
			OutTradeNo: req.GetOutTradeNo(),
			Amount:     88.8,
			Status:     "success",
		},
	}, nil
}

// ListChannels - 商户通道列表
func (this *fakeMerchantService) ListChannels(ctx context.Context, req *merchantv1.ListChannelsRequest) (*merchantv1.ListChannelsResponse, error) {

	if err := this.capture(ctx, merchantv1.MerchantService_ListChannels_FullMethodName, req); err != nil {
		return nil, err
	}
	if this.listChannels != nil {
		return this.listChannels(ctx, req)
	}

	return &merchantv1.ListChannelsResponse{
		Envelope: &merchantv1.Envelope{Code: 200, Msg: "OK"},
		Channels: []*merchantv1.Channel{
			{
				Id:       1,
				Code:     "ALIPAY_PC",
				Name:     "支付宝PC",
				Currency: "CNY",
				FeeRate:  0.002,
				Status:   "normal",
				Gateways: []*merchantv1.Gateway{{Id: 1, Name: "支付宝", Scene: "alipay", Status: "normal"}},
			},
		},
	}, nil
}

// capture - 记录请求并复算 metadata 签名（不通过返回 Unauthenticated）
func (this *fakeMerchantService) capture(ctx context.Context, method string, message proto.Message) error {

	holder, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "缺少 metadata")
	}

	sign := firstValue(holder.Get(metadataSign))
	mchId := firstValue(holder.Get(metadataMchId))

	this.mutex.Lock()
	this.record = grpcRecord{method: method, sign: sign, mchId: mchId, request: message}
	this.mutex.Unlock()

	text, err := protojson.Marshal(message)
	if err != nil {
		return status.Error(codes.Internal, "请求序列化失败")
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return status.Error(codes.Internal, "请求解析失败")
	}

	if got := referenceSign(params, this.secret); got != sign {
		return status.Error(codes.Unauthenticated, "签名验证失败")
	}

	return nil
}

// snapshot - 读取最近一次请求快照
func (this *fakeMerchantService) snapshot() grpcRecord {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	return this.record
}

// firstValue - 取 metadata 首个值
func firstValue(values []string) string {

	if len(values) == 0 {
		return ""
	}

	return values[0]
}

// startFakeGRPC - 用 bufconn 起内存 gRPC 服务并返回挂载真实链路实现的客户端
func startFakeGRPC(t *testing.T, fake *fakeMerchantService) *Client {

	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	merchantv1.RegisterMerchantServiceServer(server, fake)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("建立内存连接失败：%v", err)
	}

	// 客户端恒用正确密钥签名；假服务按自身 secret 验签（故意配错即可模拟平台拒绝）
	client, err := NewClient(testMchId, testSecret, WithEndpoint("http://127.0.0.1:2000"))
	if err != nil {
		t.Fatalf("构造客户端失败：%v", err)
	}

	// 用内存连接替换默认拨号（endpoint 仅用于构造校验）
	client.transport = &grpcTransport{
		conn:       conn,
		mchId:      testMchId,
		secret:     testSecret,
		signer:     &md5Signer{},
		operations: grpcOperations(),
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// TestGRPCTransportCreateTrade - 下单：metadata 签名等于服务端黄金向量 + 响应转模型
func TestGRPCTransportCreateTrade(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	client := startFakeGRPC(t, fake)

	result, err := client.CreateTrade(context.Background(), newTradeCreateRequest())
	if err != nil {
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

// TestGRPCTransportCheckout - 收银：gRPC 链路要求签名（比 HTTP 更严），默认值进消息体
func TestGRPCTransportCheckout(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	client := startFakeGRPC(t, fake)

	result, err := client.GetCheckout(context.Background(), &CheckoutRequest{TradeNo: "202608151200000001"})
	if err != nil {
		t.Fatalf("收银失败：%v", err)
	}

	record := fake.snapshot()
	if record.sign != goldenCheckout {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", record.sign, goldenCheckout)
	}

	request, ok := record.request.(*merchantv1.CheckoutRequest)
	if !ok {
		t.Fatalf("请求消息类型不符合预期")
	}
	if request.GetScene() != "alipay" || request.GetWay() != "auto" || !request.GetGenUrl() {
		t.Fatalf("默认值未注入消息体：%+v", request)
	}
	if result.Trade.Url == "" {
		t.Fatalf("支付链接转换失败：%+v", result.Trade)
	}
}

// TestGRPCTransportOtherOperations - 查单/转账/转账查询/通道列表的签名与转换
func TestGRPCTransportOtherOperations(t *testing.T) {

	ctx := context.Background()
	fake := &fakeMerchantService{secret: testSecret}
	client := startFakeGRPC(t, fake)

	trade, err := client.QueryTrade(ctx, &TradeQueryRequest{TradeNo: "202608151200000001"})
	if err != nil {
		t.Fatalf("查单失败：%v", err)
	}
	if record := fake.snapshot(); record.sign != goldenTradeQueryNo {
		t.Fatalf("查单签名不一致：got=%s want=%s", record.sign, goldenTradeQueryNo)
	}
	if trade.Status != "paid" || trade.CreateAt != 1755244800000 {
		t.Fatalf("订单行转换失败：%+v", trade)
	}

	transfer, err := client.SendTransfer(ctx, newTransferRequest())
	if err != nil {
		t.Fatalf("转账失败：%v", err)
	}
	if record := fake.snapshot(); record.sign != goldenTransferSend {
		t.Fatalf("转账签名不一致：got=%s want=%s", record.sign, goldenTransferSend)
	}
	if transfer.Trans.PayeeType != "ALIPAY_LOGON_ID" || transfer.Trans.Status != "pending" {
		t.Fatalf("转账单转换失败：%+v", transfer.Trans)
	}

	trans, err := client.QueryTransfer(ctx, &TransferQueryRequest{OutTradeNo: "TOUT20260815001"})
	if err != nil {
		t.Fatalf("转账查询失败：%v", err)
	}
	if record := fake.snapshot(); record.sign != goldenTransferQuery {
		t.Fatalf("转账查询签名不一致：got=%s want=%s", record.sign, goldenTransferQuery)
	}
	if trans.Status != "success" {
		t.Fatalf("转账单转换失败：%+v", trans)
	}

	channels, err := client.ListChannels(ctx)
	if err != nil {
		t.Fatalf("通道列表失败：%v", err)
	}
	if record := fake.snapshot(); record.sign != goldenChannelList {
		t.Fatalf("通道列表签名不一致：got=%s want=%s", record.sign, goldenChannelList)
	}
	if len(channels) != 1 || channels[0].Code != "ALIPAY_PC" || len(channels[0].Gateways) != 1 {
		t.Fatalf("通道转换失败：%+v", channels)
	}
}

// TestGRPCTransportUnauthenticated - 平台拒绝签名 → ErrInvalidSign（文案带 codes.Code）
func TestGRPCTransportUnauthenticated(t *testing.T) {

	// 假服务用错误密钥验签，模拟平台拒绝
	fake := &fakeMerchantService{secret: testSecret + "-wrong"}
	client := startFakeGRPC(t, fake)

	_, err := client.CreateTrade(context.Background(), newTradeCreateRequest())
	if !errors.Is(err, ErrInvalidSign) {
		t.Fatalf("应包装 ErrInvalidSign，实际：%v", err)
	}
	if !strings.Contains(err.Error(), codes.Unauthenticated.String()) {
		t.Fatalf("错误文案应带 codes.Code：%v", err)
	}
	if errors.Is(err, ErrTransport) {
		t.Fatalf("签名失败不应归为 ErrTransport")
	}
}

// TestGRPCTransportAPIError - 信封 code!=200 → *APIError
func TestGRPCTransportAPIError(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	fake.createTrade = func(ctx context.Context, req *merchantv1.CreateTradeRequest) (*merchantv1.CreateTradeResponse, error) {
		return &merchantv1.CreateTradeResponse{Envelope: &merchantv1.Envelope{Code: 409, Msg: "商户订单号重复！"}}, nil
	}
	client := startFakeGRPC(t, fake)

	_, err := client.CreateTrade(context.Background(), newTradeCreateRequest())

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError，实际：%v", err)
	}
	if apiErr.Code != 409 || apiErr.Msg != "商户订单号重复！" {
		t.Fatalf("业务错误字段不符合预期：%+v", apiErr)
	}
	if len(apiErr.Raw) != 0 {
		t.Fatalf("gRPC 链路不应携带原始报文")
	}
}

// TestGRPCTransportStatusError - 非 Unauthenticated 的 RPC 失败 → ErrTransport
func TestGRPCTransportStatusError(t *testing.T) {

	fake := &fakeMerchantService{secret: testSecret}
	fake.listChannels = func(ctx context.Context, req *merchantv1.ListChannelsRequest) (*merchantv1.ListChannelsResponse, error) {
		return nil, status.Error(codes.Internal, "内部错误")
	}
	client := startFakeGRPC(t, fake)

	_, err := client.ListChannels(context.Background())
	if !errors.Is(err, ErrTransport) {
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
		if got := grpcTarget(test.endpoint); got != test.want {
			t.Fatalf("%s：got=%s want=%s", test.endpoint, got, test.want)
		}
	}
}

// ============================= gRPC 链路测试 - 结束 =============================
