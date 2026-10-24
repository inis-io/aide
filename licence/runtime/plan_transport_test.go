package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/licence/apis"
	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// 本文件覆盖项目套餐运行面 5 路径（plans/catalog、plan-orders/create、plan-orders/query、
// plan-orders/cancel、plan-orders/rows）的 SDK 传输绑定：同一组用例分别跑 HTTP httptest 与
// gRPC bufconn（licence/AGENTS.md 双协议同组用例纪律），断言目录/订单字段解析、幂等键
// （X-Request-Id / metadata x-request-id）一致性、业务错误等价、未激活闸门、参数校验、
// 取消/超时/网络故障在两种传输下逐项一致。
// 假平台按「服务端视角」自声明字段名（不复用 runtime 业务类型），避免客户端字段漂移自我掩盖。

// ============================= 假平台罐头数据 =============================

const (
	// planCannedOrderNo - 罐头订单号
	planCannedOrderNo = "PLO-2026-000001"
	// planCannedPayUrl - 罐头收银台链接
	planCannedPayUrl = "https://pay.example.com/cashier/" + planCannedOrderNo
	// planCannedExpiredAt - 罐头收银台过期时间（毫秒）
	planCannedExpiredAt = int64(1700000900000)
	// planCannedCreateAt - 罐头下单时间（毫秒）
	planCannedCreateAt = int64(1700000005000)
	// planCannedValidUntil - 罐头当前套餐有效期（RFC3339）
	planCannedValidUntil = "2027-01-01T00:00:00Z"
)

// planCannedCatalogPlans - 罐头在售套餐目录（字段名与服务端目录契约 / proto PlanCatalogItem 逐字对齐）
func planCannedCatalogPlans() []map[string]any {
	return []map[string]any{
		{
			"planCode": "free", "planName": "免费版", "grade": 0, "badge": "", "summary": "入门首选",
			"entitlements": map[string]any{"ai.chat": false, "max_users": 10},
			"options": []map[string]any{
				{"billing": "monthly", "price": 0.0, "salePrice": 0.0, "discountTag": "", "founderLeft": -1},
			},
		},
		{
			"planCode": "pro", "planName": "专业版", "grade": 100, "badge": "推荐", "summary": "团队进阶",
			"entitlements": map[string]any{"ai.chat": true, "max_users": 100},
			"options": []map[string]any{
				{"billing": "monthly", "price": 99.0, "salePrice": 79.2, "discountTag": "8折·试用期内", "founderLeft": -1},
				{"billing": "yearly", "price": 999.0, "salePrice": 999.0, "discountTag": "", "founderLeft": 37},
				{"billing": "buyout", "price": 2999.0, "salePrice": 2999.0, "discountTag": "", "founderLeft": -1},
			},
		},
	}
}

// planCannedOrder - 罐头订单（status 可变体；pending 带收银台链接，其余状态为空串）
func planCannedOrder(status string) map[string]any {
	payUrl := ""
	if status == "pending" {
		payUrl = planCannedPayUrl
	}
	return map[string]any{
		"orderNo": planCannedOrderNo, "orderType": "purchase", "planCode": "pro", "planName": "专业版",
		"billing": "yearly", "amount": 999.0, "status": status, "payUrl": payUrl,
		"paidAt": int64(0), "createAt": planCannedCreateAt,
	}
}

// ============================= 假平台模型（HTTP 与 gRPC 共用） =============================

// planFake - 套餐运行面假平台：罐头响应 + 请求留痕 + 失败态开关 + 幂等键防呆镜像。
type planFake struct {
	// t - 用例句柄（HTTP handler / gRPC 方法在服务端 goroutine 内运行，失败用 Errorf 记录）
	t *testing.T
	// mu - 状态与留痕读写锁
	mu sync.Mutex
	// clientPublicKey - SDK 客户端验签公钥（假平台复核请求签名，证明幂等键不进 canonical）
	clientPublicKey string
	// failCode/failMsg/failDetail - 非空时返回业务失败
	failCode   string
	failMsg    string
	failDetail map[string]any
	// omitCurrentPlan - 目录响应不携带 currentPlan（无付费套餐形态）
	omitCurrentPlan bool
	// requests - 请求留痕（幂等键为 HTTP 头 X-Request-Id / gRPC metadata x-request-id）
	requests []planFakeRequest
	// bodies - 请求体留痕（按路径最近一次；HTTP 取 JSON 原文解码，gRPC 由 proto 字段重建同形 map；
	// rows 的页码统一以 {"page": N} 落痕，HTTP 侧取自 query）
	bodies map[string]map[string]any
	// delay - 响应前人为延迟（超时用例专用，HTTP handler 与 gRPC 方法共用）
	delay time.Duration
}

// planFakeRequest - 单向请求留痕
type planFakeRequest struct {
	method    string
	path      string
	requestId string
}

// newPlanFake - 新建假平台模型
func newPlanFake(t *testing.T) *planFake {
	return &planFake{t: t, bodies: map[string]map[string]any{}}
}

// setClientPublicKey - 注入 SDK 客户端验签公钥
func (this *planFake) setClientPublicKey(publicKey string) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.clientPublicKey = publicKey
}

// publicKey - 读取验签公钥
func (this *planFake) publicKey() string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.clientPublicKey
}

// record - 记录一次请求与请求体（body 键名统一 camelCase，双协议同形比对）
func (this *planFake) record(method, path, requestId string, body map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.requests = append(this.requests, planFakeRequest{method: method, path: path, requestId: requestId})
	if body != nil {
		this.bodies[path] = body
	}
}

// lastRequest - 最近一次请求（无请求即测试失败）
func (this *planFake) lastRequest(t *testing.T) planFakeRequest {
	t.Helper()
	this.mu.Lock()
	defer this.mu.Unlock()
	if len(this.requests) == 0 {
		t.Fatalf("套餐假平台未收到任何请求")
	}
	return this.requests[len(this.requests)-1]
}

// bodyOf - 指定路径最近一次请求体（无记录即测试失败）
func (this *planFake) bodyOf(t *testing.T, path string) map[string]any {
	t.Helper()
	this.mu.Lock()
	defer this.mu.Unlock()
	body, exists := this.bodies[path]
	if !exists {
		t.Fatalf("套餐假平台未收到 %s 请求体", path)
	}
	return body
}

// requestsSnapshot - 请求留痕副本
func (this *planFake) requestsSnapshot() []planFakeRequest {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]planFakeRequest(nil), this.requests...)
}

// setFailure - 设置业务失败态（后续请求按该业务码拒绝）
func (this *planFake) setFailure(code, message string, detail map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.failCode, this.failMsg, this.failDetail = code, message, detail
}

// snapshotFailure - 读取当前失败态
func (this *planFake) snapshotFailure() (string, string, map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.failCode, this.failMsg, this.failDetail
}

// setOmitCurrentPlan - 目录响应切换为无付费套餐形态（不携带 currentPlan）
func (this *planFake) setOmitCurrentPlan(omit bool) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.omitCurrentPlan = omit
}

// snapshotOmitCurrentPlan - 读取 currentPlan 省略开关
func (this *planFake) snapshotOmitCurrentPlan() bool {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.omitCurrentPlan
}

// setDelay - 设置人为响应延迟（超时用例）
func (this *planFake) setDelay(delay time.Duration) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.delay = delay
}

// maybeDelay - 按 delay 设置人为延迟响应（锁内读快照；模拟慢服务端触发调用方 deadline）
func (this *planFake) maybeDelay() {
	this.mu.Lock()
	delay := this.delay
	this.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
}

// checkRequestIdMatch - 幂等键防呆镜像（与服务端 requireRequestIdMatch 同口径）：
// 非空幂等键必须等于请求体内的业务值；不一致即服务端视角的 INVALID_ARGUMENT，用例随之失败。
func (this *planFake) checkRequestIdMatch(requestId, expected, label string) {
	if requestId != expected {
		this.t.Errorf("幂等键防呆：%s %q 与预期 %q 不一致（服务端应拒 INVALID_ARGUMENT）", label, requestId, expected)
	}
}

// ============================= 假平台 HTTP 侧 =============================

// serveHTTP - 假平台 HTTP 适配：签名复核 → 幂等键防呆 → 失败态/成功态信封（{code,msg,data,detail}）
func (this *planFake) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	if !this.verifyHTTP(request, body) {
		return
	}
	this.maybeDelay()
	requestId := request.Header.Get(apisRequestIDHeader)
	var decoded map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &decoded)
	}
	text := func(key string) string { value, _ := decoded[key].(string); return value }
	switch request.URL.Path {
	case "/api/v1/plans/catalog":
		this.record(request.Method, request.URL.Path, requestId, nil)
		if code, message, detail := this.snapshotFailure(); code != "" {
			this.writeFailureEnvelope(writer, code, message, detail)
			return
		}
		data := map[string]any{"plans": planCannedCatalogPlans()}
		if !this.snapshotOmitCurrentPlan() {
			data["currentPlan"] = map[string]any{
				"planCode": "pro", "planName": "专业版", "billing": "yearly", "validUntil": planCannedValidUntil,
			}
		}
		this.writeSuccessEnvelope(writer, data)
	case "/api/v1/plan-orders/create":
		this.record(request.Method, request.URL.Path, requestId, decoded)
		this.checkRequestIdMatch(requestId, text("requestId"), "下单幂等键")
		if code, message, detail := this.snapshotFailure(); code != "" {
			this.writeFailureEnvelope(writer, code, message, detail)
			return
		}
		this.writeSuccessEnvelope(writer, map[string]any{
			"orderNo": planCannedOrderNo, "amount": 999.0, "payUrl": planCannedPayUrl, "expiredAt": planCannedExpiredAt,
		})
	case "/api/v1/plan-orders/query":
		this.record(request.Method, request.URL.Path, requestId, decoded)
		if code, message, detail := this.snapshotFailure(); code != "" {
			this.writeFailureEnvelope(writer, code, message, detail)
			return
		}
		this.writeSuccessEnvelope(writer, planCannedOrder("pending"))
	case "/api/v1/plan-orders/cancel":
		this.record(request.Method, request.URL.Path, requestId, decoded)
		this.checkRequestIdMatch(requestId, text("orderNo"), "取消幂等键")
		if code, message, detail := this.snapshotFailure(); code != "" {
			this.writeFailureEnvelope(writer, code, message, detail)
			return
		}
		this.writeSuccessEnvelope(writer, planCannedOrder("cancelled"))
	case "/api/v1/plan-orders/rows":
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		this.record(request.Method, request.URL.Path, requestId, map[string]any{"page": page})
		if code, message, detail := this.snapshotFailure(); code != "" {
			this.writeFailureEnvelope(writer, code, message, detail)
			return
		}
		this.writeSuccessEnvelope(writer, map[string]any{
			"records": []map[string]any{planCannedOrder("paid")}, "count": 1, "page": page,
		})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// writeSuccessEnvelope - 成功信封 {code:0,msg:"ok",data:…}（与平台套餐运行面 runtimeJSON 同形）
func (this *planFake) writeSuccessEnvelope(writer http.ResponseWriter, data map[string]any) {
	raw, err := json.Marshal(map[string]any{"code": 0, "msg": "ok", "data": data})
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(raw)
}

// writeFailureEnvelope - 失败信封 {code:"业务码",msg,detail} + 业务码对应的 HTTP 状态码
func (this *planFake) writeFailureEnvelope(writer http.ResponseWriter, code, message string, detail map[string]any) {
	payload := map[string]any{"code": code, "msg": message}
	if len(detail) > 0 {
		payload["detail"] = detail
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(apis.HTTPStatusByCode(code))
	_, _ = writer.Write(raw)
}

// verifyHTTP - 请求签名复核（契约 §2.4 canonical：METHOD\nuri\nts\nnonce\nsha256hex(body)）。
// X-Request-Id 不在 canonical 内：带幂等键不影响验签，签名通过即证明幂等键未被塞进签名内容。
func (this *planFake) verifyHTTP(request *http.Request, body []byte) bool {
	publicKey := this.publicKey()
	if publicKey == "" {
		return true
	}
	content := LicenceProtocol.HTTPContent(request.Method, request.URL.RequestURI(),
		request.Header.Get("X-License-Timestamp"), request.Header.Get("X-License-Nonce"), body)
	if !LicenceProtocol.Licence.VerifyRaw(content, request.Header.Get("X-License-Sign"), publicKey) {
		this.t.Errorf("套餐假平台验签失败：%s %s", request.Method, request.URL.RequestURI())
		request.Close = true
		return false
	}
	return true
}

// ============================= 假平台 gRPC 侧 =============================

// planFakeGRPCServer - 假平台 gRPC 适配：PlanRuntimeService 单服务注册，与 HTTP 侧同一罐头模型。
type planFakeGRPCServer struct {
	licencev1.UnimplementedPlanRuntimeServiceServer
	t    *testing.T
	fake *planFake
}

// check - 签名 metadata 齐备 + 请求签名复核 + 读取幂等键（取自 metadata x-request-id）。
// canonical 复核用生成代码 FullMethodName 常量原值（含前导 /），幂等键不在其中。
func (this *planFakeGRPCServer) check(ctx context.Context, fullMethod string, request proto.Message) string {
	this.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{
		LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce,
		LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion,
	} {
		if len(md.Get(key)) == 0 {
			this.t.Errorf("gRPC 请求缺少签名 metadata %s", key)
		}
	}
	requestId := metadataFirst(md, LicenceProtocol.MetadataRequestID)
	this.fake.maybeDelay()
	if publicKey := this.fake.publicKey(); publicKey != "" {
		content, err := LicenceProtocol.GRPCContent(fullMethod, metadataFirst(md, LicenceProtocol.MetadataTimestamp), metadataFirst(md, LicenceProtocol.MetadataNonce), request)
		if err != nil {
			this.t.Errorf("gRPC 签名原文构造失败：%v", err)
		} else if !LicenceProtocol.Licence.VerifyRaw(content, metadataFirst(md, LicenceProtocol.MetadataSignature), publicKey) {
			this.t.Errorf("gRPC 请求验签失败（%s）", fullMethod)
		}
	}
	return requestId
}

// failure - 当前失败态 → gRPC status（恒挂 errdetails.ErrorInfo，Reason 逐字等于业务码，
// 与 licen-hub 服务端 serviceError 同口径；status code 映射复用 apis 侧测试镜像）
func (this *planFakeGRPCServer) failure() error {
	code, message, detail := this.fake.snapshotFailure()
	if code == "" {
		return nil
	}
	st := status.New(apisCannedGRPCCode(code), message)
	var flattened map[string]string
	if len(detail) > 0 {
		flattened = make(map[string]string, len(detail))
		for key, value := range detail {
			flattened[key] = value.(string)
		}
	}
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: code, Metadata: flattened})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}

// planCannedProtoOrder - 罐头订单 proto（与 planCannedOrder 字段值逐一对齐）
func planCannedProtoOrder(orderStatus string) *licencev1.PlanOrderItem {
	payUrl := ""
	if orderStatus == "pending" {
		payUrl = planCannedPayUrl
	}
	return &licencev1.PlanOrderItem{
		OrderNo: planCannedOrderNo, OrderType: "purchase", PlanCode: "pro", PlanName: "专业版",
		Billing: "yearly", Amount: 999, Status: orderStatus, PayUrl: payUrl,
		PaidAt: 0, CreateAt: planCannedCreateAt,
	}
}

// Catalog - 在售套餐目录（currentPlan 省略开关与 HTTP 侧同语义）
func (this *planFakeGRPCServer) PlanCatalog(ctx context.Context, request *licencev1.PlanCatalogRequest) (*licencev1.PlanCatalogResponse, error) {
	requestId := this.check(ctx, licencev1.PlanRuntimeService_PlanCatalog_FullMethodName, request)
	this.fake.record(http.MethodGet, "/api/v1/plans/catalog", requestId, nil)
	if err := this.failure(); err != nil {
		return nil, err
	}
	freeEntitlements, _ := json.Marshal(map[string]any{"ai.chat": false, "max_users": 10})
	proEntitlements, _ := json.Marshal(map[string]any{"ai.chat": true, "max_users": 100})
	response := &licencev1.PlanCatalogResponse{
		Plans: []*licencev1.PlanCatalogItem{
			{
				PlanCode: "free", PlanName: "免费版", Grade: 0, Badge: "", Summary: "入门首选",
				EntitlementsJson: freeEntitlements,
				Options: []*licencev1.PlanCatalogOption{
					{Billing: "monthly", Price: 0, SalePrice: 0, DiscountTag: "", FounderLeft: -1},
				},
			},
			{
				PlanCode: "pro", PlanName: "专业版", Grade: 100, Badge: "推荐", Summary: "团队进阶",
				EntitlementsJson: proEntitlements,
				Options: []*licencev1.PlanCatalogOption{
					{Billing: "monthly", Price: 99, SalePrice: 79.2, DiscountTag: "8折·试用期内", FounderLeft: -1},
					{Billing: "yearly", Price: 999, SalePrice: 999, DiscountTag: "", FounderLeft: 37},
					{Billing: "buyout", Price: 2999, SalePrice: 2999, DiscountTag: "", FounderLeft: -1},
				},
			},
		},
	}
	if !this.fake.snapshotOmitCurrentPlan() {
		response.CurrentPlan = &licencev1.PlanCurrentSnapshot{
			PlanCode: "pro", PlanName: "专业版", Billing: "yearly", ValidUntil: planCannedValidUntil,
		}
	}
	return response, nil
}

// CreateOrder - 套餐下单：幂等键防呆（x-request-id = 请求体 request_id）
func (this *planFakeGRPCServer) PlanOrderCreate(ctx context.Context, request *licencev1.PlanOrderCreateRequest) (*licencev1.PlanOrderCreateResponse, error) {
	requestId := this.check(ctx, licencev1.PlanRuntimeService_PlanOrderCreate_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/plan-orders/create", requestId, map[string]any{
		"planCode": request.GetPlanCode(), "billing": request.GetBilling(), "requestId": request.GetRequestId(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetRequestId(), "下单幂等键")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &licencev1.PlanOrderCreateResponse{
		OrderNo: planCannedOrderNo, Amount: 999, PayUrl: planCannedPayUrl, ExpiredAt: planCannedExpiredAt,
	}, nil
}

// QueryOrder - 查单
func (this *planFakeGRPCServer) PlanOrderQuery(ctx context.Context, request *licencev1.PlanOrderQueryRequest) (*licencev1.PlanOrderResponse, error) {
	requestId := this.check(ctx, licencev1.PlanRuntimeService_PlanOrderQuery_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/plan-orders/query", requestId, map[string]any{"orderNo": request.GetOrderNo()})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &licencev1.PlanOrderResponse{Order: planCannedProtoOrder("pending")}, nil
}

// CancelOrder - 取消订单：幂等键 = 订单号
func (this *planFakeGRPCServer) PlanOrderCancel(ctx context.Context, request *licencev1.PlanOrderCancelRequest) (*licencev1.PlanOrderResponse, error) {
	requestId := this.check(ctx, licencev1.PlanRuntimeService_PlanOrderCancel_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/plan-orders/cancel", requestId, map[string]any{"orderNo": request.GetOrderNo()})
	this.fake.checkRequestIdMatch(requestId, request.GetOrderNo(), "取消幂等键")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &licencev1.PlanOrderResponse{Order: planCannedProtoOrder("cancelled")}, nil
}

// OrderRows - 订单分页：页码留痕并回显
func (this *planFakeGRPCServer) PlanOrderRows(ctx context.Context, request *licencev1.PlanOrderRowsRequest) (*licencev1.PlanOrderRowsResponse, error) {
	requestId := this.check(ctx, licencev1.PlanRuntimeService_PlanOrderRows_FullMethodName, request)
	this.fake.record(http.MethodGet, "/api/v1/plan-orders/rows", requestId, map[string]any{"page": request.GetPage()})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &licencev1.PlanOrderRowsResponse{
		Records: []*licencev1.PlanOrderItem{planCannedProtoOrder("paid")}, Count: 1, Page: request.GetPage(),
	}, nil
}

// ============================= 双协议客户端装配与用例驱动 =============================

// newPlanHTTPClient - HTTP 侧客户端：httptest 假平台（仅本机回环，禁止联网测试）
func newPlanHTTPClient(t *testing.T, fake *planFake) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	client, err := New(Options{
		ServerURL: server.URL, LicenseNo: "LIC-2026-000123", Salt: "plan-test-salt",
		PublicKeys: map[string]string{"license-key-2026-01": "unused-in-plan-test"},
		StorageDir: t.TempDir(), Fingerprint: "plan-test-fingerprint", HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	fake.setClientPublicKey(activateApisClient(t, client))
	return client, server
}

// newPlanGRPCClient - gRPC 侧客户端：bufconn 假服务端（进程内，禁止联网测试）
func newPlanGRPCClient(t *testing.T, fake *planFake) (*Client, *grpc.ClientConn) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterPlanRuntimeServiceServer(server, &planFakeGRPCServer{t: t, fake: fake})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatalf("bufconn 建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{options: Options{HTTPTimeout: 2 * time.Second}}
	fake.setClientPublicKey(activateApisClient(t, client))
	client.transport = &grpcRuntimeTransport{
		client: client, conn: conn,
		plan: licencev1.NewPlanRuntimeServiceClient(conn),
	}
	return client, conn
}

// runPlanDual - 双协议同组用例：同一断言函数分别以 HTTP（httptest）与 gRPC（bufconn）客户端执行
func runPlanDual(t *testing.T, run func(t *testing.T, fake *planFake, client *Client)) {
	t.Helper()
	t.Run("http", func(t *testing.T) {
		fake := newPlanFake(t)
		client, _ := newPlanHTTPClient(t, fake)
		run(t, fake, client)
	})
	t.Run("grpc", func(t *testing.T) {
		fake := newPlanFake(t)
		client, _ := newPlanGRPCClient(t, fake)
		run(t, fake, client)
	})
}

// ============================= 用例 =============================

// TestPlanDualCatalog - 双协议在售目录：套餐/计费选项逐字段解析一致（含折扣标签与创始价名额），
// 当前付费套餐快照解析；只读调用不带幂等键
func TestPlanDualCatalog(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		result, err := client.PlansCatalog(t.Context())
		if err != nil {
			t.Fatalf("PlansCatalog 失败: %v", err)
		}
		if len(result.Plans) != 2 {
			t.Fatalf("目录套餐数不符：%+v", result.Plans)
		}
		free, pro := result.Plans[0], result.Plans[1]
		if free.PlanCode != "free" || free.Grade != 0 || len(free.Options) != 1 {
			t.Fatalf("免费版目录项不符：%+v", free)
		}
		if pro.PlanCode != "pro" || pro.PlanName != "专业版" || pro.Grade != 100 ||
			pro.Badge != "推荐" || pro.Summary != "团队进阶" {
			t.Fatalf("专业版目录项不符：%+v", pro)
		}
		if entitled, _ := pro.Entitlements["ai.chat"].(bool); !entitled {
			t.Fatalf("权益表布尔值不符：%+v", pro.Entitlements)
		}
		if !numEqual(pro.Entitlements["max_users"], 100) {
			t.Fatalf("权益表数值不符：%+v", pro.Entitlements)
		}
		if len(pro.Options) != 3 {
			t.Fatalf("计费选项数不符：%+v", pro.Options)
		}
		monthly := pro.Options[0]
		if monthly.Billing != "monthly" || monthly.Price != 99 || monthly.SalePrice != 79.2 ||
			monthly.DiscountTag != "8折·试用期内" || monthly.FounderLeft != -1 {
			t.Fatalf("包月选项不符：%+v", monthly)
		}
		if pro.Options[1].FounderLeft != 37 {
			t.Fatalf("创始价剩余名额不符：%+v", pro.Options[1])
		}
		if result.CurrentPlan == nil || result.CurrentPlan.PlanCode != "pro" ||
			result.CurrentPlan.Billing != "yearly" || result.CurrentPlan.ValidUntil != planCannedValidUntil {
			t.Fatalf("当前套餐快照不符：%+v", result.CurrentPlan)
		}
		if request := fake.lastRequest(t); request.requestId != "" || request.method != http.MethodGet {
			t.Fatalf("只读目录调用不应下发幂等键且应为 GET：%+v", request)
		}

		// 无付费套餐形态：currentPlan 缺省 → 归一为 nil，目录本身不受影响
		fake.setOmitCurrentPlan(true)
		again, err := client.PlansCatalog(t.Context())
		if err != nil {
			t.Fatalf("无付费套餐 PlansCatalog 失败: %v", err)
		}
		if again.CurrentPlan != nil || len(again.Plans) != 2 {
			t.Fatalf("无付费套餐目录不符：%+v", again)
		}
	})
}

// TestPlanDualOrderCreate - 双协议下单：显式幂等键 → X-Request-Id 与请求体 requestId 同值；
// 缺省自动生成 req_ 前缀键并在结果中回显；金额/收银台链接/过期时间解析一致
func TestPlanDualOrderCreate(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		result, err := client.PlanOrderCreate(t.Context(), PlanOrderCreateInput{
			PlanCode: "pro", Billing: PlanBillingYearly, RequestId: "req-plan-1",
		})
		if err != nil {
			t.Fatalf("PlanOrderCreate 失败: %v", err)
		}
		if result.OrderNo != planCannedOrderNo || result.Amount != 999 ||
			result.PayUrl != planCannedPayUrl || result.ExpiredAt != planCannedExpiredAt {
			t.Fatalf("下单回执不符：%+v", result)
		}
		if result.RequestId != "req-plan-1" {
			t.Fatalf("显式幂等键应原样回显：%+v", result)
		}
		if request := fake.lastRequest(t); request.requestId != "req-plan-1" {
			t.Fatalf("下单幂等键未透传：%+v", request)
		}
		if body := fake.bodyOf(t, "/api/v1/plan-orders/create"); body["planCode"] != "pro" ||
			body["billing"] != "yearly" || body["requestId"] != "req-plan-1" {
			t.Fatalf("下单请求体不符：%+v", body)
		}

		auto, err := client.PlanOrderCreate(t.Context(), PlanOrderCreateInput{PlanCode: "pro", Billing: PlanBillingMonthly})
		if err != nil {
			t.Fatalf("缺省幂等键 PlanOrderCreate 失败: %v", err)
		}
		if !strings.HasPrefix(auto.RequestId, "req_") {
			t.Fatalf("缺省幂等键应自动生成 req_ 前缀键：%+v", auto)
		}
		if request := fake.lastRequest(t); request.requestId != auto.RequestId {
			t.Fatalf("自动幂等键应同步透传：%+v ≠ %+v", request, auto)
		}
	})
}

// TestPlanDualOrderQuery - 双协议查单：订单十字段解析一致；只读调用不带幂等键
func TestPlanDualOrderQuery(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		order, err := client.PlanOrderQuery(t.Context(), planCannedOrderNo)
		if err != nil {
			t.Fatalf("PlanOrderQuery 失败: %v", err)
		}
		if order.OrderNo != planCannedOrderNo || order.OrderType != PlanOrderTypePurchase ||
			order.PlanCode != "pro" || order.PlanName != "专业版" || order.Billing != "yearly" ||
			order.Amount != 999 || order.Status != PlanOrderStatusPending ||
			order.PayUrl != planCannedPayUrl || order.PaidAt != 0 || order.CreateAt != planCannedCreateAt {
			t.Fatalf("订单解析不符：%+v", order)
		}
		if request := fake.lastRequest(t); request.requestId != "" {
			t.Fatalf("查单为只读调用，不应下发幂等键：%+v", request)
		}
		if body := fake.bodyOf(t, "/api/v1/plan-orders/query"); body["orderNo"] != planCannedOrderNo {
			t.Fatalf("查单请求体不符：%+v", body)
		}
	})
}

// TestPlanDualOrderCancel - 双协议取消：返回已取消订单快照（收银台链接清空）；幂等键 = 订单号
func TestPlanDualOrderCancel(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		order, err := client.PlanOrderCancel(t.Context(), planCannedOrderNo)
		if err != nil {
			t.Fatalf("PlanOrderCancel 失败: %v", err)
		}
		if order.OrderNo != planCannedOrderNo || order.Status != PlanOrderStatusCancelled || order.PayUrl != "" {
			t.Fatalf("取消回执不符：%+v", order)
		}
		if request := fake.lastRequest(t); request.requestId != planCannedOrderNo {
			t.Fatalf("取消幂等键应为订单号：%+v", request)
		}
		if body := fake.bodyOf(t, "/api/v1/plan-orders/cancel"); body["orderNo"] != planCannedOrderNo {
			t.Fatalf("取消请求体不符：%+v", body)
		}
	})
}

// TestPlanDualOrderRows - 双协议订单分页：页码上送（HTTP query / gRPC 字段）并回显，
// 记录逐字段解析一致；只读调用不带幂等键
func TestPlanDualOrderRows(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		rows, err := client.PlanOrderRows(t.Context(), 2)
		if err != nil {
			t.Fatalf("PlanOrderRows 失败: %v", err)
		}
		if rows.Page != 2 || rows.Count != 1 || len(rows.Records) != 1 {
			t.Fatalf("订单分页不符：%+v", rows)
		}
		record := rows.Records[0]
		if record.OrderNo != planCannedOrderNo || record.Status != PlanOrderStatusPaid {
			t.Fatalf("订单记录不符：%+v", record)
		}
		if request := fake.lastRequest(t); request.requestId != "" || request.method != http.MethodGet {
			t.Fatalf("订单分页为只读 GET，不应下发幂等键：%+v", request)
		}
		if body := fake.bodyOf(t, "/api/v1/plan-orders/rows"); !numEqual(body["page"], 2) {
			t.Fatalf("分页页码不符：%+v", body)
		}
	})
}

// TestPlanDualBusinessError - 双协议业务失败等价：业务码 / 提示 / HTTP 等价状态码 / 明细
// 在 HTTP 与 gRPC 下逐项一致（gRPC 侧经 errdetails.ErrorInfo.Reason 还原）
func TestPlanDualBusinessError(t *testing.T) {

	cases := []struct {
		name   string
		code   string
		detail map[string]any
	}{
		{"参数错误", apis.ErrorCodeInvalidArgument, nil},
		{"模糊404认证失败", apis.ErrorCodeNotFound, nil},
		{"取消非待支付订单", apis.ErrorCodeInvalidState, nil},
		{"速率超限", apis.ErrorCodeRateLimited, map[string]any{"retryAfterMs": "2000"}},
	}
	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				message := "套餐拒绝：" + item.code
				fake.setFailure(item.code, message, item.detail)
				_, err := client.PlanOrderCancel(t.Context(), planCannedOrderNo)
				var apiErr *apis.Error
				if !errors.As(err, &apiErr) {
					t.Fatalf("业务失败应归一为 *apis.Error：%v", err)
				}
				if apiErr.Code != item.code || apiErr.Message != message {
					t.Fatalf("业务码/提示不符：%+v（期望 %s / %s）", apiErr, item.code, message)
				}
				if apiErr.HTTPStatus != apis.HTTPStatusByCode(item.code) {
					t.Fatalf("HTTP 等价状态码不符：%d（期望 %d）", apiErr.HTTPStatus, apis.HTTPStatusByCode(item.code))
				}
				for key, want := range item.detail {
					if got := apiErr.Detail[key]; got != want && got != nil && toString(got) != want {
						t.Fatalf("明细 %s 不符：%v ≠ %v", key, got, want)
					}
				}
				fake.setFailure("", "", nil)
			})
		}
	})
}

// TestPlanDualGateNotActivated - 未激活闸门：无 token 或非放行状态在双协议下都本地闸住，
// 假平台收不到任何请求（服务端凭证校验仍是最终边界）
func TestPlanDualGateNotActivated(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		client.mu.Lock()
		client.state.ActivationToken = ""
		client.mu.Unlock()
		if _, err := client.PlansCatalog(t.Context()); !errors.Is(err, ErrPlanNotActivated) {
			t.Fatalf("无 token 应返回 ErrPlanNotActivated，实际 %v", err)
		}
		for _, status := range []string{
			LicenceProtocol.StatusExpired, LicenceProtocol.StatusRevoked,
			LicenceProtocol.StatusSuspended, LicenceProtocol.StatusSeatReleased,
		} {
			client.mu.Lock()
			client.state.ActivationToken = "token"
			client.state.Status = status
			client.mu.Unlock()
			if _, err := client.PlanOrderCreate(t.Context(), PlanOrderCreateInput{PlanCode: "pro", Billing: PlanBillingMonthly}); !errors.Is(err, ErrPlanNotActivated) {
				t.Fatalf("状态 %s 应返回 ErrPlanNotActivated，实际 %v", status, err)
			}
		}
		if requests := fake.requestsSnapshot(); len(requests) != 0 {
			t.Fatalf("未激活闸门不得发出任何请求，实际 %d 次", len(requests))
		}
	})
}

// TestPlanDualValidation - 入参本地校验：非法入参直接返回错误且不发出任何请求（双协议一致）
func TestPlanDualValidation(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		if _, err := client.PlanOrderCreate(t.Context(), PlanOrderCreateInput{Billing: PlanBillingMonthly}); err == nil {
			t.Fatalf("空套餐编码应本地报错")
		}
		if _, err := client.PlanOrderCreate(t.Context(), PlanOrderCreateInput{PlanCode: "pro", Billing: "weekly"}); err == nil {
			t.Fatalf("非法计费周期应本地报错")
		}
		if _, err := client.PlanOrderQuery(t.Context(), "  "); err == nil {
			t.Fatalf("空订单号查单应本地报错")
		}
		if _, err := client.PlanOrderCancel(t.Context(), ""); err == nil {
			t.Fatalf("空订单号取消应本地报错")
		}
		if _, err := client.PlanOrderRows(t.Context(), 0); err == nil {
			t.Fatalf("页码 0 应本地报错")
		}
		if requests := fake.requestsSnapshot(); len(requests) != 0 {
			t.Fatalf("本地校验失败不得发出任何请求，实际 %d 次", len(requests))
		}
	})
}

// TestPlanDualContextCancellation - 调用方取消上下文：双协议都返回可 errors.Is(context.Canceled)
// 的错误且都不是业务错误（客户端取消不是服务端拒绝，严禁伪装成业务码）
func TestPlanDualContextCancellation(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := client.PlanOrderQuery(ctx, planCannedOrderNo)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消上下文应返回 context.Canceled，实际 %v", err)
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("客户端取消不得伪装成业务码：%+v", apiErr)
		}
	})
}

// TestPlanDualTimeout - 调用方 deadline 到期：双协议都返回可 errors.Is(context.DeadlineExceeded)
// 的错误且都不是业务错误（gRPC 侧 DeadlineExceeded 原样透传，与 HTTP 侧传输错误分层一致）
func TestPlanDualTimeout(t *testing.T) {

	runPlanDual(t, func(t *testing.T, fake *planFake, client *Client) {
		fake.setDelay(600 * time.Millisecond)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err := client.PlansCatalog(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("超时应返回 context.DeadlineExceeded，实际 %v", err)
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("超时不得伪装成业务码：%+v", apiErr)
		}
	})
}

// TestPlanNetworkError - 网络故障：假平台不可达时双协议都返回传输错误（非业务错误、非闸门错误）
func TestPlanNetworkError(t *testing.T) {

	assertTransportError := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("平台不可达应返回错误")
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("网络故障不得伪装成业务码：%+v", apiErr)
		}
		if errors.Is(err, ErrPlanNotActivated) {
			t.Fatalf("网络故障不得误报未激活闸门：%v", err)
		}
	}

	t.Run("http", func(t *testing.T) {
		fake := newPlanFake(t)
		client, server := newPlanHTTPClient(t, fake)
		server.Close() // 立即关停，后续请求连接被拒
		_, err := client.PlansCatalog(t.Context())
		assertTransportError(t, err)
	})
	t.Run("grpc", func(t *testing.T) {
		fake := newPlanFake(t)
		client, conn := newPlanGRPCClient(t, fake)
		_ = conn.Close() // 关闭底层连接，后续 RPC 直接失败
		_, err := client.PlansCatalog(t.Context())
		assertTransportError(t, err)
	})
}
