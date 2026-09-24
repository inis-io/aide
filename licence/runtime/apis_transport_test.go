package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/licence/apis"
	apisv1 "github.com/inis-io/aide/licence/proto/apis/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"github.com/spf13/cast"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// 本文件覆盖 API 商城三路径（invoke / ip-locate/query / usage）的 SDK 传输绑定：
// 同一组用例分别跑 HTTP httptest 与 gRPC bufconn（licence/AGENTS.md 双协议同组用例纪律），
// 断言结果字段、幂等键透传、业务错误等价与未激活闸门在两种传输下逐项一致。
// 假平台按「服务端视角」自声明字段名（不复用 apis 包类型），避免客户端字段漂移自我掩盖。

// ============================= 假平台罐头数据 =============================

const (
	// apisCannedChargeMode - 罐头回执计费模式
	apisCannedChargeMode = "subscription_quota"
	// apisCannedQuotaRemaining - 罐头回执订阅额度余量
	apisCannedQuotaRemaining = 9832
	// apisCannedServerTime - 罐头回执服务端时间（毫秒）
	apisCannedServerTime = 1786000000000
	// apisCannedCount - 罐头分页总条数
	apisCannedCount = 42
)

// apisCannedIPLocate - 假平台归属地出参（字段名与 proto IPLocateResult / 后端 provider 逐字对齐）
type apisCannedIPLocate struct {
	Ip        string `json:"ip"`
	Nation    string `json:"nation"`
	Province  string `json:"province"`
	City      string `json:"city"`
	Adcode    string `json:"adcode"`
	Rectangle string `json:"rectangle"`
	Isp       string `json:"isp"`
	Source    string `json:"source"`
	CacheHit  bool   `json:"cacheHit"`
	Stale     bool   `json:"stale"`
}

// apisCannedReceipt - 假平台计量回执（字段名与 proto Receipt / 后端 metering.Receipt 逐字对齐）
type apisCannedReceipt struct {
	RequestId      string `json:"requestId"`
	ChargeMode     string `json:"chargeMode"`
	CacheHit       bool   `json:"cacheHit"`
	Quantity       int64  `json:"quantity"`
	Amount         int64  `json:"amount"`
	QuotaRemaining int64  `json:"quotaRemaining"`
	BalanceAfter   int64  `json:"balanceAfter"`
	ServerTime     int64  `json:"serverTime"`
}

// apisCannedUsageRow - 假平台流水行（字段名与 proto UsageRecord / 平台 ApisUsageRecord 逐字对齐）
type apisCannedUsageRow struct {
	Id           int64  `json:"id"`
	RequestId    string `json:"requestId"`
	UserId       int64  `json:"userId"`
	Capability   string `json:"capability"`
	ProductId    int64  `json:"productId"`
	ActivationNo string `json:"activationNo"`
	SubId        int64  `json:"subId"`
	Quantity     int64  `json:"quantity"`
	ChargeMode   string `json:"chargeMode"`
	CacheHit     bool   `json:"cacheHit"`
	Amount       int64  `json:"amount"`
	Result       string `json:"result"`
	UpstreamMs   int64  `json:"upstreamMs"`
	CreateAt     int64  `json:"createAt"`
}

// apisCannedLocate - 罐头归属地（ip 回显请求值，其余固定）
func apisCannedLocate(ip string) apisCannedIPLocate {
	return apisCannedIPLocate{
		Ip: ip, Nation: "中国", Province: "浙江省", City: "杭州市", Adcode: "330100",
		Rectangle: "119.9,29.9;120.4,30.4", Isp: "", Source: "amap", CacheHit: true, Stale: false,
	}
}

// apisCannedReceiptOf - 罐头回执（requestId 原样回显调用级幂等键：证明键真实抵达服务端）
func apisCannedReceiptOf(requestId string) apisCannedReceipt {
	return apisCannedReceipt{
		RequestId: requestId, ChargeMode: apisCannedChargeMode, CacheHit: true, Quantity: 1,
		Amount: 0, QuotaRemaining: apisCannedQuotaRemaining, BalanceAfter: 0, ServerTime: apisCannedServerTime,
	}
}

// apisUsageFilter - 假平台解析出的 Usage 查询条件（HTTP 从 query、gRPC 从 UsageRequest 取，
// 同一结构保证两协议回显一致）
type apisUsageFilter struct {
	Page       int
	Capability string
	ProductId  int64
	ChargeMode string
	RequestId  string
	CreateAt   int64
}

// apisCannedUsageRowOf - 罐头流水行：把查询条件回显进行（断言筛选条件确实抵达服务端）
func apisCannedUsageRowOf(filter apisUsageFilter) apisCannedUsageRow {
	return apisCannedUsageRow{
		Id: 7, RequestId: "req_row_0001", UserId: 9, Capability: filter.Capability,
		ProductId: filter.ProductId, ActivationNo: "ACT-2026-000001", SubId: 0, Quantity: 1,
		ChargeMode: filter.ChargeMode, CacheHit: true, Amount: 0, Result: "success",
		UpstreamMs: 12, CreateAt: filter.CreateAt,
	}
}

// ============================= 客户端侧期望值（apis 包类型） =============================

// apisExpectedLocate - 客户端应解析出的归属地结果
func apisExpectedLocate(ip string) apis.IPLocateResult {
	canned := apisCannedLocate(ip)
	return apis.IPLocateResult{
		Ip: canned.Ip, Nation: canned.Nation, Province: canned.Province, City: canned.City,
		Adcode: canned.Adcode, Rectangle: canned.Rectangle, Isp: canned.Isp, Source: canned.Source,
		CacheHit: canned.CacheHit, Stale: canned.Stale,
	}
}

// apisExpectedReceipt - 客户端应解析出的计量回执
func apisExpectedReceipt(requestId string) apis.Receipt {
	canned := apisCannedReceiptOf(requestId)
	return apis.Receipt{
		RequestId: canned.RequestId, ChargeMode: canned.ChargeMode, CacheHit: canned.CacheHit,
		Quantity: canned.Quantity, Amount: canned.Amount, QuotaRemaining: canned.QuotaRemaining,
		BalanceAfter: canned.BalanceAfter, ServerTime: canned.ServerTime,
	}
}

// apisExpectedUsageRow - 客户端应解析出的流水行
func apisExpectedUsageRow(filter apisUsageFilter) apis.UsageRecord {
	canned := apisCannedUsageRowOf(filter)
	return apis.UsageRecord{
		Id: canned.Id, RequestId: canned.RequestId, UserId: canned.UserId, Capability: canned.Capability,
		ProductId: canned.ProductId, ActivationNo: canned.ActivationNo, SubId: canned.SubId,
		Quantity: canned.Quantity, ChargeMode: canned.ChargeMode, CacheHit: canned.CacheHit,
		Amount: canned.Amount, Result: canned.Result, UpstreamMs: canned.UpstreamMs, CreateAt: canned.CreateAt,
	}
}

// ============================= 假平台模型（HTTP 与 gRPC 共用） =============================

// apisFake - API 商城运行面假平台：三能力的罐头响应 + 请求留痕 + 失败态开关。
// HTTP handler 与 gRPC server 共用本模型，保证「同一组用例跑两协议」时服务端行为同源。
type apisFake struct {
	// t - 用例句柄（HTTP handler 在服务端 goroutine 内运行，失败用 Errorf 记录）
	t *testing.T
	// mu - 状态与留痕读写锁
	mu sync.Mutex
	// clientPublicKey - SDK 客户端验签公钥（假平台复核请求签名，证明幂等键不进 canonical）
	clientPublicKey string
	// failCode/failMsg/failDetail - 非空时返回业务失败
	failCode   string
	failMsg    string
	failDetail map[string]any
	// requests - 请求留痕（幂等键为 HTTP 头 X-Request-Id / gRPC metadata x-request-id）
	requests []apisFakeRequest
}

// apisFakeRequest - 单向请求留痕
type apisFakeRequest struct {
	method    string
	path      string
	requestId string
}

// newApisFake - 新建假平台模型
func newApisFake(t *testing.T) *apisFake {
	return &apisFake{t: t}
}

// setClientPublicKey - 注入 SDK 客户端验签公钥
func (this *apisFake) setClientPublicKey(publicKey string) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.clientPublicKey = publicKey
}

// publicKey - 读取验签公钥
func (this *apisFake) publicKey() string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.clientPublicKey
}

// record - 记录一次请求
func (this *apisFake) record(method, path, requestId string) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.requests = append(this.requests, apisFakeRequest{method: method, path: path, requestId: requestId})
}

// setFailure - 设置业务失败态（后续请求按该业务码拒绝）
func (this *apisFake) setFailure(code, message string, detail map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.failCode, this.failMsg, this.failDetail = code, message, detail
}

// snapshotFailure - 读取当前失败态
func (this *apisFake) snapshotFailure() (string, string, map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.failCode, this.failMsg, this.failDetail
}

// requestsSnapshot - 请求留痕副本
func (this *apisFake) requestsSnapshot() []apisFakeRequest {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]apisFakeRequest(nil), this.requests...)
}

// lastRequest - 最近一次请求（无请求即测试失败）
func (this *apisFake) lastRequest(t *testing.T) apisFakeRequest {
	t.Helper()
	requests := this.requestsSnapshot()
	if len(requests) == 0 {
		t.Fatalf("假平台未收到任何请求")
	}
	return requests[len(requests)-1]
}

// ============================= 假平台 HTTP 侧 =============================

// serveHTTP - 假平台 HTTP 适配：签名复核 → 失败态/成功态信封（{code,msg,data,detail}）
func (this *apisFake) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	// 注意：URL.Path（不含 query）与 gRPC 侧 record 的路径同形，便于两协议逐项对照
	if !this.verifyHTTP(request, body) {
		return
	}
	this.record(request.Method, request.URL.Path, request.Header.Get(apisRequestIDHeader))
	if code, message, detail := this.snapshotFailure(); code != "" {
		this.writeFailureEnvelope(writer, code, message, detail)
		return
	}
	query := request.URL.Query()
	switch request.URL.Path {
	case "/api/v1/apis/ip-locate/query":
		var input struct {
			Ip string `json:"ip"`
		}
		_ = json.Unmarshal(body, &input)
		this.writeSuccessEnvelope(writer, map[string]any{
			"result": apisCannedLocate(input.Ip), "receipt": apisCannedReceiptOf(request.Header.Get(apisRequestIDHeader)),
		})
	case "/api/v1/apis/invoke":
		var input struct {
			Capability string         `json:"capability"`
			Action     string         `json:"action"`
			Params     map[string]any `json:"params"`
			ProductId  int64          `json:"productId"`
			Quantity   int64          `json:"quantity"`
		}
		_ = json.Unmarshal(body, &input)
		params := input.Params
		if params == nil {
			params = map[string]any{}
		}
		this.writeSuccessEnvelope(writer, map[string]any{
			"result": map[string]any{
				"capability": input.Capability, "action": input.Action, "params": params,
				"productId": input.ProductId, "quantity": input.Quantity,
			},
			"receipt": apisCannedReceiptOf(request.Header.Get(apisRequestIDHeader)),
		})
	case "/api/v1/apis/usage":
		filter := apisUsageFilter{
			Page:       cast.ToInt(query.Get("page")),
			Capability: query.Get("capability"),
			ProductId:  cast.ToInt64(query.Get("productId")),
			ChargeMode: query.Get("chargeMode"),
			RequestId:  query.Get("requestId"),
		}
		if values := query["createAt[]"]; len(values) > 0 {
			filter.CreateAt = cast.ToInt64(values[0])
		}
		this.writeSuccessEnvelope(writer, apisCannedUsageData(filter))
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// writeSuccessEnvelope - 成功信封 {code:0,msg:"ok",data:…}（与平台 runtimeJSON/usageJSON 同形）
func (this *apisFake) writeSuccessEnvelope(writer http.ResponseWriter, data map[string]any) {
	raw, err := json.Marshal(map[string]any{"code": 0, "msg": "ok", "data": data})
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(raw)
}

// writeFailureEnvelope - 失败信封 {code:"业务码",msg,detail} + 业务码对应的 HTTP 状态码
func (this *apisFake) writeFailureEnvelope(writer http.ResponseWriter, code, message string, detail map[string]any) {
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
// 只用头族四要素 + 请求原文重算：X-Request-Id 不在 canonical 内（04 §2.2），
// 故带幂等键不影响验签，反之签名通过即证明幂等键未被塞进签名内容。
func (this *apisFake) verifyHTTP(request *http.Request, body []byte) bool {
	publicKey := this.publicKey()
	if publicKey == "" {
		return true
	}
	content := LicenceProtocol.HTTPContent(request.Method, request.URL.RequestURI(),
		request.Header.Get("X-License-Timestamp"), request.Header.Get("X-License-Nonce"), body)
	if !LicenceProtocol.Licence.VerifyRaw(content, request.Header.Get("X-License-Sign"), publicKey) {
		this.t.Errorf("apis 假平台验签失败：%s %s", request.Method, request.URL.RequestURI())
		request.Close = true
		return false
	}
	return true
}

// apisCannedUsageData - 罐头分页结果 {records,count,page}（page 按服务端归一：<1 取 1）
func apisCannedUsageData(filter apisUsageFilter) map[string]any {
	page := filter.Page
	if page < 1 {
		page = 1
	}
	return map[string]any{
		"records": []apisCannedUsageRow{apisCannedUsageRowOf(filter)},
		"count":   apisCannedCount, "page": page,
	}
}

// ============================= 假平台 gRPC 侧 =============================

// apisFakeGRPCServer - 假平台 gRPC 适配：与 HTTP 侧同一罐头模型；
// 业务失败按服务端口径返回 status + errdetails.ErrorInfo（Reason 逐字等于业务码，
// Metadata 为扁平化 detail），客户端据此还原与 HTTP 完全同形的失败信封。
type apisFakeGRPCServer struct {
	apisv1.UnimplementedApisRuntimeServiceServer
	t    *testing.T
	fake *apisFake
}

// check - 签名 metadata 齐备 + 请求签名复核 + 请求留痕（幂等键取自 metadata x-request-id）
func (this *apisFakeGRPCServer) check(ctx context.Context, method, path, fullMethod string, request proto.Message) {
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
	requestId := ""
	if values := md.Get(LicenceProtocol.MetadataRequestID); len(values) > 0 {
		requestId = values[0]
	}
	this.fake.record(method, path, requestId)
	// canonical 复核：GRPC\n{fullMethod 常量原值}\n{ts}\n{nonce}\n{sha256(确定性 protobuf)}，
	// 幂等键不在其中（不进 canonical，与 HTTP 头不入签名同口径）
	if publicKey := this.fake.publicKey(); publicKey != "" {
		content, err := LicenceProtocol.GRPCContent(fullMethod, metadataFirst(md, LicenceProtocol.MetadataTimestamp), metadataFirst(md, LicenceProtocol.MetadataNonce), request)
		if err != nil {
			this.t.Errorf("gRPC 签名原文构造失败：%v", err)
			return
		}
		if !LicenceProtocol.Licence.VerifyRaw(content, metadataFirst(md, LicenceProtocol.MetadataSignature), publicKey) {
			this.t.Errorf("gRPC 请求验签失败（%s）", fullMethod)
		}
	}
}

// metadataFirst - 读取首个同名 metadata 值（缺失返回空串）
func metadataFirst(md metadata.MD, key string) string {
	if values := md.Get(key); len(values) > 0 {
		return values[0]
	}
	return ""
}

// failure - 当前失败态 → gRPC status（业务码 → status code 为 04 §2.4 表的测试镜像）。
// 与 licen-hub grpc/apis/v1 的 serviceError 同口径：**恒挂 ErrorInfo**（Reason 逐字等于业务码，
// 无明细时 Metadata 为 nil），客户端因此总能取到与 HTTP 逐字一致的业务码。
func (this *apisFakeGRPCServer) failure() error {
	code, message, detail := this.fake.snapshotFailure()
	if code == "" {
		return nil
	}
	st := status.New(apisCannedGRPCCode(code), message)
	var flattened map[string]string
	if len(detail) > 0 {
		flattened = make(map[string]string, len(detail))
		for key, value := range detail {
			flattened[key] = fmt.Sprint(value)
		}
	}
	detailed, err := st.WithDetails(&errdetails.ErrorInfo{Reason: code, Metadata: flattened})
	if err != nil {
		return st.Err()
	}
	return detailed.Err()
}

// apisCannedGRPCCode - 业务码 → gRPC status code（04 §2.4 映射表的测试镜像，仅覆盖用例用到的码）
func apisCannedGRPCCode(code string) codes.Code {
	switch code {
	case apis.ErrorCodeInvalidArgument:
		return codes.InvalidArgument
	case apis.ErrorCodeUnauthorized:
		return codes.Unauthenticated
	case apis.ErrorCodeForbidden, apis.ErrorCodeNoEntitlement:
		return codes.PermissionDenied
	case apis.ErrorCodeAccountFrozen, apis.ErrorCodeConflict, apis.ErrorCodeInvalidState:
		return codes.FailedPrecondition
	case apis.ErrorCodeQuotaExceeded, apis.ErrorCodeInsufficientBalance,
		apis.ErrorCodeSpendLimitExceeded, apis.ErrorCodeConcurrencyLimited, apis.ErrorCodeRateLimited:
		return codes.ResourceExhausted
	case apis.ErrorCodeIPNotFound, apis.ErrorCodeCapabilityNotFound, apis.ErrorCodeNotFound:
		return codes.NotFound
	default:
		return codes.Internal
	}
}

// receiptProto - 罐头回执 → proto（逐字段映射）
func apisCannedReceiptProto(receipt apisCannedReceipt) *apisv1.Receipt {
	return &apisv1.Receipt{
		RequestId: receipt.RequestId, ChargeMode: receipt.ChargeMode, CacheHit: receipt.CacheHit,
		Quantity: receipt.Quantity, Amount: receipt.Amount, QuotaRemaining: receipt.QuotaRemaining,
		BalanceAfter: receipt.BalanceAfter, ServerTime: receipt.ServerTime,
	}
}

// Invoke - 通用调用：回显 capability/action/params/productId/quantity（断言入参抵达服务端）
func (this *apisFakeGRPCServer) Invoke(ctx context.Context, request *apisv1.InvokeRequest) (*apisv1.InvokeResponse, error) {
	this.check(ctx, http.MethodPost, "/api/v1/apis/invoke", apisv1.ApisRuntimeService_Invoke_FullMethodName, request)
	if err := this.failure(); err != nil {
		return nil, err
	}
	params := map[string]any{}
	if request.GetParams() != nil {
		params = request.GetParams().AsMap()
	}
	result, err := structpb.NewStruct(map[string]any{
		"capability": request.GetCapability(), "action": request.GetAction(), "params": params,
		"productId": request.GetProductId(), "quantity": request.GetQuantity(),
	})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	requestId := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		requestId = metadataFirst(md, LicenceProtocol.MetadataRequestID)
	}
	return &apisv1.InvokeResponse{Result: result, Receipt: apisCannedReceiptProto(apisCannedReceiptOf(requestId))}, nil
}

// IPLocate - typed IP 定位：能力/动作由服务端固定，只回显 ip
func (this *apisFakeGRPCServer) IPLocate(ctx context.Context, request *apisv1.IPLocateRequest) (*apisv1.IPLocateResponse, error) {
	this.check(ctx, http.MethodPost, "/api/v1/apis/ip-locate/query", apisv1.ApisRuntimeService_IPLocate_FullMethodName, request)
	if err := this.failure(); err != nil {
		return nil, err
	}
	canned := apisCannedLocate(request.GetIp())
	requestId := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		requestId = metadataFirst(md, LicenceProtocol.MetadataRequestID)
	}
	return &apisv1.IPLocateResponse{
		Result: &apisv1.IPLocateResult{
			Ip: canned.Ip, Nation: canned.Nation, Province: canned.Province, City: canned.City,
			Adcode: canned.Adcode, Rectangle: canned.Rectangle, Isp: canned.Isp, Source: canned.Source,
			CacheHit: canned.CacheHit, Stale: canned.Stale,
		},
		Receipt: apisCannedReceiptProto(apisCannedReceiptOf(requestId)),
	}, nil
}

// Usage - 流水自助查询：查询条件回显进流水行（断言筛选条件抵达服务端），page 按归一口径回显
func (this *apisFakeGRPCServer) Usage(ctx context.Context, request *apisv1.UsageRequest) (*apisv1.UsageResponse, error) {
	this.check(ctx, http.MethodGet, "/api/v1/apis/usage", apisv1.ApisRuntimeService_Usage_FullMethodName, request)
	if err := this.failure(); err != nil {
		return nil, err
	}
	filter := apisUsageFilter{
		Page: int(request.GetPage()), Capability: request.GetCapability(),
		ProductId: request.GetProductId(), ChargeMode: request.GetChargeMode(),
		RequestId: request.GetRequestId(),
	}
	if createAt := request.GetCreateAt(); len(createAt) > 0 {
		filter.CreateAt = createAt[0]
	}
	canned := apisCannedUsageRowOf(filter)
	page := request.GetPage()
	if page < 1 {
		page = 1
	}
	return &apisv1.UsageResponse{
		Records: []*apisv1.UsageRecord{{
			Id: canned.Id, RequestId: canned.RequestId, UserId: canned.UserId, Capability: canned.Capability,
			ProductId: canned.ProductId, ActivationNo: canned.ActivationNo, SubId: canned.SubId,
			Quantity: canned.Quantity, ChargeMode: canned.ChargeMode, CacheHit: canned.CacheHit,
			Amount: canned.Amount, Result: canned.Result, UpstreamMs: canned.UpstreamMs, CreateAt: canned.CreateAt,
		}},
		Count: apisCannedCount, Page: page,
	}, nil
}

// ============================= 双协议客户端装配与用例驱动 =============================

// activateApisClient - 置入已激活状态（本测试只验证 apis 三路径与闸门，不重跑激活链路），
// 返回客户端验签公钥（假平台据此复核请求签名）
func activateApisClient(t *testing.T, client *Client) string {
	t.Helper()
	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	client.state = runtimeState{
		ActivationToken: "token", ClientSeed: hex.EncodeToString(seed), Status: LicenceProtocol.StatusValid,
	}
	client.mu.Unlock()
	return hex.EncodeToString(LicenceProtocol.Ed25519PublicKey(seed))
}

// newApisHTTPClient - HTTP 侧客户端：httptest 假平台（仅本机回环，禁止联网测试）
func newApisHTTPClient(t *testing.T, fake *apisFake) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	client, err := New(Options{
		ServerURL: server.URL, LicenseNo: "LIC-2026-000123", Salt: "apis-test-salt",
		PublicKeys: map[string]string{"license-key-2026-01": "unused-in-apis-test"},
		StorageDir: t.TempDir(), Fingerprint: "apis-test-fingerprint", HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	fake.setClientPublicKey(activateApisClient(t, client))
	return client
}

// newApisGRPCClient - gRPC 侧客户端：bufconn 假服务端（进程内，禁止联网测试）
func newApisGRPCClient(t *testing.T, fake *apisFake) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	apisv1.RegisterApisRuntimeServiceServer(server, &apisFakeGRPCServer{t: t, fake: fake})
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
	client.transport = &grpcRuntimeTransport{client: client, conn: conn, apis: apisv1.NewApisRuntimeServiceClient(conn)}
	client.Apis = apis.New(apisDoer{client: client})
	return client
}

// runApisDual - 双协议同组用例：同一断言函数分别以 HTTP（httptest）与 gRPC（bufconn）客户端执行，
// 每协议独立一套假平台与客户端。
func runApisDual(t *testing.T, run func(t *testing.T, fake *apisFake, client *Client)) {
	t.Helper()
	t.Run("http", func(t *testing.T) {
		fake := newApisFake(t)
		run(t, fake, newApisHTTPClient(t, fake))
	})
	t.Run("grpc", func(t *testing.T) {
		fake := newApisFake(t)
		run(t, fake, newApisGRPCClient(t, fake))
	})
}

// ============================= 用例 =============================

// TestApisDualIPLocate - 双协议 IPLocate 成功：结果字段 + 计量回执 + 幂等键真实下发并回显
func TestApisDualIPLocate(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		loc, receipt, err := client.Apis.IPLocate(t.Context(), "203.0.113.10")
		if err != nil {
			t.Fatalf("IPLocate 失败: %v", err)
		}
		if want := apisExpectedLocate("203.0.113.10"); loc != want {
			t.Fatalf("归属地结果不符：%+v（期望 %+v）", loc, want)
		}
		request := fake.lastRequest(t)
		if request.method != http.MethodPost || request.path != "/api/v1/apis/ip-locate/query" {
			t.Fatalf("请求路由不符：%+v", request)
		}
		if !strings.HasPrefix(request.requestId, "req_") {
			t.Fatalf("SDK 应自动生成 req_ 前缀幂等键，实际 %q", request.requestId)
		}
		if receipt.RequestId != request.requestId {
			t.Fatalf("回执 requestId 应回显真实用键：%q ≠ %q", receipt.RequestId, request.requestId)
		}
		if want := apisExpectedReceipt(request.requestId); receipt != want {
			t.Fatalf("计量回执不符：%+v（期望 %+v）", receipt, want)
		}
	})
}

// TestApisDualInvokeGeneral - 双协议通用 Invoke：参数/产品/计量数抵达服务端，result 原文返回
func TestApisDualInvokeGeneral(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		result, receipt, err := client.Apis.Invoke(t.Context(), apis.InvokeInput{
			Capability: "ip-locate", Action: "query",
			Params:    map[string]any{"ip": "203.0.113.10", "batch": 3},
			ProductId: 12, Quantity: 5, RequestId: "req_invoke_dual",
		})
		if err != nil {
			t.Fatalf("Invoke 失败: %v", err)
		}
		decoded := map[string]any{}
		if err = json.Unmarshal(result, &decoded); err != nil {
			t.Fatalf("result 应为 JSON 对象：%v（%s）", err, string(result))
		}
		want := map[string]any{
			"capability": "ip-locate", "action": "query",
			"params":    map[string]any{"ip": "203.0.113.10", "batch": float64(3)},
			"productId": float64(12), "quantity": float64(5),
		}
		if !reflect.DeepEqual(decoded, want) {
			t.Fatalf("通用调用回显不符：%s", string(result))
		}
		request := fake.lastRequest(t)
		if request.method != http.MethodPost || request.path != "/api/v1/apis/invoke" {
			t.Fatalf("请求路由不符：%+v", request)
		}
		if request.requestId != "req_invoke_dual" {
			t.Fatalf("显式幂等键应原样下发：%+v", request)
		}
		if receipt.RequestId != "req_invoke_dual" {
			t.Fatalf("回执应回显显式幂等键：%+v", receipt)
		}
	})
}

// TestApisDualUsagePagination - 双协议 Usage：筛选条件抵达服务端（回显进流水行）+ 分页字段 +
// 只读调用不下发调用级幂等键
func TestApisDualUsagePagination(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		filter := apisUsageFilter{
			Page: 2, Capability: "ip-locate", ProductId: 3,
			ChargeMode: apisCannedChargeMode, RequestId: "req_row_0001", CreateAt: 1700000000000,
		}
		page, err := client.Apis.Usage(t.Context(), apis.UsageQuery{
			Page: filter.Page, Limit: 20, Capability: filter.Capability, ProductId: filter.ProductId,
			ChargeMode: filter.ChargeMode, RequestId: filter.RequestId,
			CreateAt: []int64{filter.CreateAt, 1800000000000},
		})
		if err != nil {
			t.Fatalf("Usage 失败: %v", err)
		}
		if page.Count != apisCannedCount || page.Page != filter.Page || len(page.Records) != 1 {
			t.Fatalf("分页结果不符：%+v", page)
		}
		if want := apisExpectedUsageRow(filter); page.Records[0] != want {
			t.Fatalf("流水行不符：%+v（期望 %+v）", page.Records[0], want)
		}
		request := fake.lastRequest(t)
		if request.method != http.MethodGet || request.path != "/api/v1/apis/usage" {
			t.Fatalf("请求路由不符：%+v", request)
		}
		if request.requestId != "" {
			t.Fatalf("只读调用不应下发调用级幂等键：%+v", request)
		}
	})
}

// TestApisDualBusinessErrorEquivalence - 跨协议错误等价：6 个业务码在双协议下
// Error.Code 逐字一致、HTTP 等价状态码一致、明细键值与提示文本一致
// （gRPC 侧经 errdetails.ErrorInfo.Reason 还原，HTTP 侧直接取失败信封 code）
func TestApisDualBusinessErrorEquivalence(t *testing.T) {

	cases := []struct {
		name   string
		code   string
		detail map[string]any
	}{
		{"额度耗尽", apis.ErrorCodeQuotaExceeded, map[string]any{"scope": "month", "resetAt": "2026-10-01T00:00:00+08:00"}},
		{"余额不足", apis.ErrorCodeInsufficientBalance, nil},
		{"IP空结果", apis.ErrorCodeIPNotFound, nil},
		{"模糊404认证失败", apis.ErrorCodeNotFound, nil},
		{"速率超限", apis.ErrorCodeRateLimited, map[string]any{"retryAfterMs": 2000}},
		{"账户冻结", apis.ErrorCodeAccountFrozen, nil},
	}
	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				message := "商城拒绝：" + item.code
				fake.setFailure(item.code, message, item.detail)
				_, _, err := client.Apis.IPLocate(t.Context(), "203.0.113.10")
				var apiErr *apis.Error
				if !errors.As(err, &apiErr) {
					t.Fatalf("业务失败应归一为 *apis.Error：%v", err)
				}
				if apiErr.Code != item.code {
					t.Fatalf("业务码应逐字一致：%q ≠ %q", apiErr.Code, item.code)
				}
				if apiErr.Message != message {
					t.Fatalf("提示文本不符：%q ≠ %q", apiErr.Message, message)
				}
				if apiErr.HTTPStatus != apis.HTTPStatusByCode(item.code) {
					t.Fatalf("HTTP 等价状态码不符：%d（期望 %d）", apiErr.HTTPStatus, apis.HTTPStatusByCode(item.code))
				}
				if got, want := apisNormalizeDetail(apiErr.Detail), apisNormalizeDetail(item.detail); !reflect.DeepEqual(got, want) {
					t.Fatalf("明细键值不符：%+v ≠ %+v", apiErr.Detail, item.detail)
				}
			})
		}
	})
}

// apisNormalizeDetail - 明细归一为字符串后比较：键名与值语义跨协议一致，类型随协议
// （HTTP 保持 JSON 原始类型，gRPC 侧服务端把明细扁平化为字符串，04 §2.4）
func apisNormalizeDetail(detail map[string]any) map[string]string {
	normalized := make(map[string]string, len(detail))
	for key, value := range detail {
		normalized[key] = fmt.Sprint(value)
	}
	return normalized
}

// TestApisDualGateNotActivated - 未激活闸门：无 token 或非放行状态在双协议下都本地闸住，
// 假平台收不到任何请求（服务端凭证校验仍是最终边界）
func TestApisDualGateNotActivated(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		client.mu.Lock()
		client.state.ActivationToken = ""
		client.mu.Unlock()
		if _, _, err := client.Apis.IPLocate(t.Context(), "203.0.113.10"); !errors.Is(err, apis.ErrNotActivated) {
			t.Fatalf("无 token 应返回 ErrNotActivated，实际 %v", err)
		}
		for _, status := range []string{
			LicenceProtocol.StatusExpired, LicenceProtocol.StatusRevoked,
			LicenceProtocol.StatusSuspended, LicenceProtocol.StatusSeatReleased,
		} {
			client.mu.Lock()
			client.state.ActivationToken = "token"
			client.state.Status = status
			client.mu.Unlock()
			if _, err := client.Apis.Usage(t.Context(), apis.UsageQuery{}); !errors.Is(err, apis.ErrNotActivated) {
				t.Fatalf("状态 %s 应返回 ErrNotActivated，实际 %v", status, err)
			}
		}
		if requests := fake.requestsSnapshot(); len(requests) != 0 {
			t.Fatalf("未激活闸门不得发出任何请求，实际 %d 次：%+v", len(requests), requests)
		}
	})
}

// ============================= gRPC 传输层直测（合成信封与降级反推） =============================

// apisBareFailureServer - 只按 gRPC status 报错的假服务端（不挂 errdetails.ErrorInfo）：
// 覆盖「服务端未挂业务码」的降级分支
type apisBareFailureServer struct {
	apisv1.UnimplementedApisRuntimeServiceServer
	code codes.Code
}

func (this apisBareFailureServer) IPLocate(context.Context, *apisv1.IPLocateRequest) (*apisv1.IPLocateResponse, error) {
	return nil, status.Error(this.code, "服务端未挂业务码")
}

// apisBareFailureTransport - 构建只报 status 的 bufconn 传输层
func apisBareFailureTransport(t *testing.T, code codes.Code) *grpcRuntimeTransport {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	apisv1.RegisterApisRuntimeServiceServer(server, apisBareFailureServer{code: code})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatalf("bufconn 建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{options: Options{HTTPTimeout: 2 * time.Second}}
	activateApisClient(t, client)
	return &grpcRuntimeTransport{client: client, conn: conn, apis: apisv1.NewApisRuntimeServiceClient(conn)}
}

// TestGRPCApisFailureEnvelopeSynthesis - gRPC 失败 → 合成信封的传输层直证：
// RoundTrip 返回 HTTP 等价状态码 + 与 HTTP 失败信封同形的 {code,msg,detail} JSON
func TestGRPCApisFailureEnvelopeSynthesis(t *testing.T) {

	transport := apisBareFailureTransport(t, codes.NotFound)

	// 无 detail 的失败：业务码来自 status 反推，HTTP 等价码来自 04 §2.4 表
	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost,
		"/api/v1/apis/ip-locate/query", []byte(`{"ip":"203.0.113.10"}`), true)
	if err != nil {
		t.Fatalf("gRPC 业务失败应合成信封而非返回传输错误：%v", err)
	}
	if code != http.StatusNotFound {
		t.Fatalf("HTTP 等价状态码不符：%d（期望 404）", code)
	}
	envelope := map[string]any{}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("合成信封应为 JSON：%v（%s）", err, string(raw))
	}
	if envelope["code"] != apis.ErrorCodeNotFound || envelope["msg"] != "服务端未挂业务码" {
		t.Fatalf("合成信封字段不符：%s", string(raw))
	}
	if _, exists := envelope["detail"]; exists {
		t.Fatalf("无明细时不应凭空生成 detail：%s", string(raw))
	}
}

// TestGRPCApisErrorCodeFallbackByStatus - 服务端未挂 ErrorInfo.Reason 时的反推表
// （映射表反转；Unknown/Internal 一律 INTERNAL_ERROR）
func TestGRPCApisErrorCodeFallbackByStatus(t *testing.T) {

	cases := []struct {
		code codes.Code
		want string
	}{
		{codes.InvalidArgument, apis.ErrorCodeInvalidArgument},
		{codes.Unauthenticated, apis.ErrorCodeUnauthorized},
		{codes.PermissionDenied, apis.ErrorCodeForbidden},
		{codes.FailedPrecondition, apis.ErrorCodeInvalidState},
		{codes.ResourceExhausted, apis.ErrorCodeQuotaExceeded},
		{codes.NotFound, apis.ErrorCodeNotFound},
		{codes.Internal, apis.ErrorCodeInternal},
		{codes.Unknown, apis.ErrorCodeInternal},
	}
	for _, item := range cases {
		t.Run(item.code.String(), func(t *testing.T) {
			transport := apisBareFailureTransport(t, item.code)
			_, raw, err := transport.RoundTrip(context.Background(), http.MethodPost,
				"/api/v1/apis/ip-locate/query", []byte(`{"ip":"203.0.113.10"}`), true)
			if err != nil {
				t.Fatalf("应合成信封：%v", err)
			}
			envelope := map[string]any{}
			if err = json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope["code"] != item.want {
				t.Fatalf("status %s 应反推业务码 %s，实际 %v", item.code, item.want, envelope["code"])
			}
		})
	}
}

// TestApisDualMountedOnClientApis - 挂载点自检：两协议客户端都经类型化 Client.Apis 调用
// （避免测试直接摸适配器，保证用例走的是下游真实调用形态）
func TestApisDualMountedOnClientApis(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		if client.Apis == nil {
			t.Fatalf("Client.Apis 挂载点缺失")
		}
		if _, _, err := client.Apis.IPLocate(t.Context(), "203.0.113.10"); err != nil {
			t.Fatalf("经 Client.Apis 调用失败：%v", err)
		}
		if got := len(fake.requestsSnapshot()); got != 1 {
			t.Fatalf("应恰好 1 次请求，实际 %d 次", got)
		}
	})
}

// TestApisDualContextCancellation - 调用方取消上下文：双协议都返回可 errors.Is(context.Canceled)
// 的错误且都不是业务错误——客户端取消不是服务端拒绝，严禁伪装成 INTERNAL_ERROR 业务码
// （gRPC 侧 Canceled/DeadlineExceeded/Unavailable 原样透传，与 HTTP 侧传输错误分层一致）
func TestApisDualContextCancellation(t *testing.T) {

	runApisDual(t, func(t *testing.T, fake *apisFake, client *Client) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := client.Apis.IPLocate(ctx, "203.0.113.10")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消上下文应返回 context.Canceled，实际 %v", err)
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("客户端取消不得伪装成业务码：%+v", apiErr)
		}
	})
}

// TestGRPCApisTransportFailurePassthrough - gRPC 传输层故障（拨号失败 → Unavailable）
// 不合成业务信封：与 HTTP 侧连接失败同为传输错误，apis 包不伪装业务码
func TestGRPCApisTransportFailurePassthrough(t *testing.T) {

	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return nil, errors.New("dial refused")
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{options: Options{HTTPTimeout: time.Second}}
	activateApisClient(t, client)
	transport := &grpcRuntimeTransport{client: client, conn: conn, apis: apisv1.NewApisRuntimeServiceClient(conn)}

	code, raw, err := transport.RoundTrip(context.Background(), http.MethodPost,
		"/api/v1/apis/ip-locate/query", []byte(`{"ip":"203.0.113.10"}`), true)
	if err == nil {
		t.Fatalf("拨号失败应返回传输错误而非合成信封：code=%d raw=%s", code, string(raw))
	}
	if code != 0 || raw != nil {
		t.Fatalf("传输错误不应带状态码/信封：code=%d raw=%s", code, string(raw))
	}
	if status.Convert(err).Code() != codes.Unavailable {
		t.Fatalf("拨号失败应映射为 Unavailable，实际 %v", err)
	}
}
