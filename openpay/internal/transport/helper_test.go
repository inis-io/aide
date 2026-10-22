package transport

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"

	merchantv1 "github.com/inis-io/aide/openpay/proto/gen/openpay/merchant/v1"
)

// ============================= 测试公共件 - 开始 =============================

const (
	// testMchId - 测试商户号（与 sign 包黄金向量一致）
	testMchId = "M1000001"
	// testSecret - 测试商户密钥（与 sign 包黄金向量一致）
	testSecret = "test-secret-0123456789abcdef"
	// goldenCreateTrade - 下单签名（拷贝自 sign 包由服务端实测生成的黄金向量，用于锚定链路实现）
	goldenCreateTrade = "13A5669628000EDA65E66ECA899DC1F1"
)

// referenceSign - 服务端签名算法的独立复算实现（测试基准，与被测 sign 包互为校验）
func referenceSign(params map[string]any, secret string) string {

	keys := make([]string, 0, len(params))
	for key := range params {
		if key == signField {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := referenceValue(params[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
	}

	sum := md5.Sum([]byte(strings.Join(parts, "&") + "&secret=" + secret))

	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// referenceValue - 服务端 cast.ToString 的极简镜像（测试基准）
func referenceValue(value any) string {

	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// newTradeCreateRequest - 与黄金向量一致的下单请求
func newTradeCreateRequest() *types.TradeCreateRequest {

	return &types.TradeCreateRequest{
		Amount:      12.30,
		ChannelCode: "ALIPAY_PC",
		Describe:    "测试订单",
		OutTradeNo:  "OUT20260815001",
		NotifyUrl:   "https://example.com/notify",
		ReturnUrl:   "https://example.com/return",
	}
}

// newTransferRequest - 与黄金向量一致的转账请求
func newTransferRequest() *types.TransferRequest {

	return &types.TransferRequest{
		ChannelCode:  "ALIPAY_TRANS",
		Scene:        "alipay",
		Amount:       88.80,
		PayeeAccount: "payee@example.com",
		PayeeName:    "张三",
		Describe:     "服务报酬",
		OutTradeNo:   "TOUT20260815001",
	}
}

// ============================= 测试公共件 - 结束 =============================

// ============================= HTTP 测试件 - 开始 =============================

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

// startHTTPTransport - 装配 HTTP 链路（Signer 留空以覆盖 MD5 兜底）
func startHTTPTransport(t *testing.T, endpoint string) Transport {

	t.Helper()

	link := NewHTTP(Config{
		Endpoint:  endpoint,
		MchId:     testMchId,
		Secret:    testSecret,
		Timeout:   5 * time.Second,
		UserAgent: "openpay-go/0.1.0",
	})
	t.Cleanup(func() { _ = link.Close() })

	return link
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

// ============================= HTTP 测试件 - 结束 =============================

// ============================= gRPC 测试件 - 开始 =============================

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

	signature := firstValue(holder.Get(metadataSign))
	mchId := firstValue(holder.Get(metadataMchId))

	this.mutex.Lock()
	this.record = grpcRecord{method: method, sign: signature, mchId: mchId, request: message}
	this.mutex.Unlock()

	text, err := protojson.Marshal(message)
	if err != nil {
		return status.Error(codes.Internal, "请求序列化失败")
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return status.Error(codes.Internal, "请求解析失败")
	}

	if got := referenceSign(params, this.secret); got != signature {
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

// startFakeGRPC - 用 bufconn 起内存 gRPC 服务并返回挂载真实链路实现的 Transport
func startFakeGRPC(t *testing.T, fake *fakeMerchantService) Transport {

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
	link := &grpcTransport{
		conn:       conn,
		mchId:      testMchId,
		secret:     testSecret,
		signer:     sign.New(),
		operations: grpcOperations(),
	}
	t.Cleanup(func() { _ = link.Close() })

	return link
}

// ============================= gRPC 测试件 - 结束 =============================
