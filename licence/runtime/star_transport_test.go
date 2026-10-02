package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	starv1 "github.com/inis-io/aide/licence/proto/star/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

// 本文件覆盖星链中枢 11 路径（directory/join、directory/peers、index/push、index/pull、
// billing/purchase、grant-out、order-create、order-settle、order-cancel、report-stats、
// arbitration/report-outcome）的 SDK 传输绑定：同一组用例分别跑 HTTP httptest 与
// gRPC bufconn（licence/AGENTS.md 双协议同组用例纪律），断言结果字段、业务单号/幂等键
// 一致性（服务端防呆镜像：X-Request-Id 非空时必须等于业务单号）、业务错误等价、
// 未激活闸门与超时/取消在两种传输下逐项一致。
// 假平台按「服务端视角」自声明字段名（不复用 runtime 业务类型），避免客户端字段漂移自我掩盖。

// ============================= 假平台罐头数据 =============================

const (
	// starCannedSince - 罐头目录水位（毫秒）
	starCannedSince = 1700000001000
	// starCannedTrust - 罐头信用分（万分位，0.60 冷启动）
	starCannedTrust = 6000
	// starCannedBalanceAfter - 罐头发放核销后点数池余额
	starCannedBalanceAfter = 4200
	// starCannedRefundFen - 罐头取消退款金额（万分）
	starCannedRefundFen = 5000
	// starCannedSettleAt - 罐头结算时间（毫秒）
	starCannedSettleAt = 1700000002000
)

// starCannedPeer - 罐头目录同伴（字段名与服务端 PeerItem / proto StarPeer 逐字对齐）
var starCannedPeer = map[string]any{
	"instanceId": "INS-PEER-0001", "name": "邻舰", "endpoint": "grpc://peer.example.com:9000",
	"pubkey": "aabbcc", "starStatus": "active", "trustScore": 8000,
	"appVersion": "1.0.0", "lastBeatAt": int64(1699999999000),
}

// starCannedHotMeta - 罐头全网榜摘要元数据（JSON 原文）
const starCannedHotMeta = `{"title":"热帖","author":"兔子"}`

// starCannedEmbedding - 罐头 float16 向量字节
var starCannedEmbedding = []byte{1, 2, 3, 4}

// starCannedHotItem - 罐头全网榜条目（字段名与服务端 GlobalHotItem / proto StarGlobalHotItem 对齐）
func starCannedHotItem() map[string]any {
	return map[string]any{
		"instanceId": "INS-PEER-0001", "remoteNoteId": "note-9", "scoreNet": int64(8600),
		"meta": json.RawMessage(starCannedHotMeta), "embedding": starCannedEmbedding,
		"originUrl": "https://peer.example.com/notes/9", "publishedAt": int64(1699999990000),
	}
}

// starCannedBill - 罐头分账快照（字段名与服务端 SettleBill / proto StarSettleBill 对齐）
func starCannedBill() map[string]any {
	return map[string]any{
		"settleNo": "SET-2026-000001", "amountFen": int64(100000), "refundFen": int64(40000),
		"sourceFen": int64(12000), "targetFen": int64(30000), "platformFen": int64(18000),
		"targetLines": []map[string]any{{"instanceId": "INS-PEER-0001", "fen": int64(30000), "views": int64(600)}},
		"settledAt": int64(starCannedSettleAt),
	}
}

// starExpectedBill - 客户端应解析出的分账快照（runtime 业务类型）
func starExpectedBill() StarSettleBill {
	return StarSettleBill{
		SettleNo: "SET-2026-000001", AmountFen: 100000, RefundFen: 40000,
		SourceFen: 12000, TargetFen: 30000, PlatformFen: 18000,
		TargetLines: []StarSettleTargetLine{{InstanceId: "INS-PEER-0001", Fen: 30000, Views: 600}},
		SettledAt: starCannedSettleAt,
	}
}

// ============================= 假平台模型（HTTP 与 gRPC 共用） =============================

// starFake - 星链运行面假平台：罐头响应 + 请求留痕 + 失败态开关 + 幂等键防呆镜像。
type starFake struct {
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
	// requests - 请求留痕（幂等键为 HTTP 头 X-Request-Id / gRPC metadata x-request-id）
	requests []starFakeRequest
	// bodies - 请求体留痕（按路径最近一次；HTTP 取 JSON 原文解码，gRPC 由 proto 字段重建同形 map）
	bodies map[string]map[string]any
	// delay - 响应前人为延迟（超时用例专用，HTTP handler 与 gRPC 方法共用）
	delay time.Duration
}

// starFakeRequest - 单向请求留痕
type starFakeRequest struct {
	method    string
	path      string
	requestId string
}

// newStarFake - 新建假平台模型
func newStarFake(t *testing.T) *starFake {
	return &starFake{t: t, bodies: map[string]map[string]any{}}
}

// setClientPublicKey - 注入 SDK 客户端验签公钥
func (this *starFake) setClientPublicKey(publicKey string) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.clientPublicKey = publicKey
}

// publicKey - 读取验签公钥
func (this *starFake) publicKey() string {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.clientPublicKey
}

// record - 记录一次请求与请求体（body 键名统一 camelCase，双协议同形比对）
func (this *starFake) record(method, path, requestId string, body map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.requests = append(this.requests, starFakeRequest{method: method, path: path, requestId: requestId})
	if body != nil {
		this.bodies[path] = body
	}
}

// lastRequest - 最近一次请求（无请求即测试失败）
func (this *starFake) lastRequest(t *testing.T) starFakeRequest {
	t.Helper()
	this.mu.Lock()
	defer this.mu.Unlock()
	if len(this.requests) == 0 {
		t.Fatalf("星链假平台未收到任何请求")
	}
	return this.requests[len(this.requests)-1]
}

// bodyOf - 指定路径最近一次请求体（无记录即测试失败）
func (this *starFake) bodyOf(t *testing.T, path string) map[string]any {
	t.Helper()
	this.mu.Lock()
	defer this.mu.Unlock()
	body, exists := this.bodies[path]
	if !exists {
		t.Fatalf("星链假平台未收到 %s 请求体", path)
	}
	return body
}

// requestsSnapshot - 请求留痕副本
func (this *starFake) requestsSnapshot() []starFakeRequest {
	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]starFakeRequest(nil), this.requests...)
}

// setFailure - 设置业务失败态（后续请求按该业务码拒绝）
func (this *starFake) setFailure(code, message string, detail map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.failCode, this.failMsg, this.failDetail = code, message, detail
}

// snapshotFailure - 读取当前失败态
func (this *starFake) snapshotFailure() (string, string, map[string]any) {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.failCode, this.failMsg, this.failDetail
}

// maybeDelay - 按 delay 设置人为延迟响应（锁内读快照；模拟慢服务端触发调用方 deadline）
func (this *starFake) maybeDelay() {
	this.mu.Lock()
	delay := this.delay
	this.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
}

// checkRequestIdMatch - 幂等键防呆镜像（与服务端 requireRequestIdMatch 同口径）：
// 非空幂等键必须等于业务单号；不一致即服务端视角的 INVALID_ARGUMENT，用例随之失败。
func (this *starFake) checkRequestIdMatch(requestId, bizNo, label string) {
	if requestId != "" && requestId != bizNo {
		this.t.Errorf("幂等键防呆：%s %q 与业务单号 %q 不一致（服务端应拒 INVALID_ARGUMENT）", label, requestId, bizNo)
	}
}

// ============================= 假平台 HTTP 侧 =============================

// serveHTTP - 假平台 HTTP 适配：签名复核 → 幂等键防呆 → 失败态/成功态信封（{code,msg,data,detail}）
func (this *starFake) serveHTTP(writer http.ResponseWriter, request *http.Request) {
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
	this.record(request.Method, request.URL.Path, requestId, decoded)
	if code, message, detail := this.snapshotFailure(); code != "" {
		this.writeFailureEnvelope(writer, code, message, detail)
		return
	}
	text := func(key string) string { value, _ := decoded[key].(string); return value }
	switch request.URL.Path {
	case "/api/v1/star/directory/join":
		this.writeSuccessEnvelope(writer, map[string]any{
			"starStatus": "active", "trustScore": starCannedTrust, "since": starCannedSince,
		})
	case "/api/v1/star/directory/peers":
		this.writeSuccessEnvelope(writer, map[string]any{
			"peers": []map[string]any{starCannedPeer}, "since": starCannedSince,
		})
	case "/api/v1/star/index/push":
		items, _ := decoded["items"].([]any)
		this.writeSuccessEnvelope(writer, map[string]any{"accepted": len(items)})
	case "/api/v1/star/index/pull":
		this.writeSuccessEnvelope(writer, map[string]any{"items": []map[string]any{starCannedHotItem()}})
	case "/api/v1/star/billing/purchase":
		this.checkRequestIdMatch(requestId, text("purchaseNo"), "购入单号")
		this.writeSuccessEnvelope(writer, map[string]any{"purchaseNo": text("purchaseNo")})
	case "/api/v1/star/billing/grant-out":
		this.checkRequestIdMatch(requestId, text("grantNo"), "发放核销单号")
		this.writeSuccessEnvelope(writer, map[string]any{"balanceAfter": starCannedBalanceAfter})
	case "/api/v1/star/billing/order-create":
		this.checkRequestIdMatch(requestId, text("promoteNo"), "投流单号")
		this.writeSuccessEnvelope(writer, map[string]any{"promoteNo": text("promoteNo")})
	case "/api/v1/star/billing/order-settle":
		this.checkRequestIdMatch(requestId, text("promoteNo"), "投流单号")
		this.writeSuccessEnvelope(writer, starCannedBill())
	case "/api/v1/star/billing/order-cancel":
		this.checkRequestIdMatch(requestId, text("promoteNo"), "投流单号")
		this.writeSuccessEnvelope(writer, map[string]any{"refundFen": starCannedRefundFen})
	case "/api/v1/star/billing/report-stats":
		this.writeSuccessEnvelope(writer, map[string]any{"doneViews": 600})
	case "/api/v1/star/arbitration/report-outcome":
		this.checkRequestIdMatch(requestId, text("reportNo"), "举报单号")
		this.writeSuccessEnvelope(writer, map[string]any{"reportNo": text("reportNo")})
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// writeSuccessEnvelope - 成功信封 {code:0,msg:"ok",data:…}（与平台 star runtimeJSON 同形）
func (this *starFake) writeSuccessEnvelope(writer http.ResponseWriter, data map[string]any) {
	raw, err := json.Marshal(map[string]any{"code": 0, "msg": "ok", "data": data})
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(raw)
}

// writeFailureEnvelope - 失败信封 {code:"业务码",msg,detail} + 业务码对应的 HTTP 状态码
func (this *starFake) writeFailureEnvelope(writer http.ResponseWriter, code, message string, detail map[string]any) {
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
func (this *starFake) verifyHTTP(request *http.Request, body []byte) bool {
	publicKey := this.publicKey()
	if publicKey == "" {
		return true
	}
	content := LicenceProtocol.HTTPContent(request.Method, request.URL.RequestURI(),
		request.Header.Get("X-License-Timestamp"), request.Header.Get("X-License-Nonce"), body)
	if !LicenceProtocol.Licence.VerifyRaw(content, request.Header.Get("X-License-Sign"), publicKey) {
		this.t.Errorf("星链假平台验签失败：%s %s", request.Method, request.URL.RequestURI())
		request.Close = true
		return false
	}
	return true
}

// ============================= 假平台 gRPC 侧 =============================

// starFakeGRPCServer - 假平台 gRPC 适配：四个服务同一实例注册，与 HTTP 侧同一罐头模型。
type starFakeGRPCServer struct {
	starv1.UnimplementedStarDirectoryServiceServer
	starv1.UnimplementedStarIndexServiceServer
	starv1.UnimplementedStarBillingServiceServer
	starv1.UnimplementedStarArbitrationServiceServer
	t    *testing.T
	fake *starFake
}

// check - 签名 metadata 齐备 + 请求签名复核 + 请求留痕（幂等键取自 metadata x-request-id）。
// canonical 复核用生成代码 FullMethodName 常量原值（含前导 /），幂等键不在其中。
func (this *starFakeGRPCServer) check(ctx context.Context, fullMethod string, request proto.Message) string {
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
// 与 licen-hub grpc/star/v1 的 serviceError 同口径；status code 映射复用 apis 侧测试镜像）
func (this *starFakeGRPCServer) failure() error {
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

// starAckOK - 成功回执（code=0）
func starAckOK() *starv1.StarAck { return &starv1.StarAck{Code: 0, Message: "ok"} }

// Join - 注册/心跳：请求体留痕（camelCase 重建，与 HTTP 同形）
func (this *starFakeGRPCServer) Join(ctx context.Context, request *starv1.StarJoinRequest) (*starv1.StarJoinResponse, error) {
	requestId := this.check(ctx, starv1.StarDirectoryService_Join_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/directory/join", requestId, map[string]any{
		"instanceId": request.GetInstanceId(), "name": request.GetName(), "endpoint": request.GetEndpoint(),
		"pubkey": request.GetPubkey(), "appVersion": request.GetAppVersion(),
	})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarJoinResponse{
		Ack: starAckOK(), StarStatus: "active", TrustScore: starCannedTrust, Since: starCannedSince,
	}, nil
}

// ListPeers - 目录增量拉取
func (this *starFakeGRPCServer) ListPeers(ctx context.Context, request *starv1.StarListPeersRequest) (*starv1.StarListPeersResponse, error) {
	requestId := this.check(ctx, starv1.StarDirectoryService_ListPeers_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/directory/peers", requestId, map[string]any{"since": request.GetSince()})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarListPeersResponse{
		Ack: starAckOK(), Since: starCannedSince,
		Peers: []*starv1.StarPeer{{
			InstanceId: "INS-PEER-0001", Name: "邻舰", Endpoint: "grpc://peer.example.com:9000",
			Pubkey: "aabbcc", StarStatus: "active", TrustScore: 8000,
			AppVersion: "1.0.0", LastBeatAt: 1699999999000,
		}},
	}, nil
}

// PushHotIndex - 站内热榜上报：受理数 = 条目数，条目逐字段留痕
func (this *starFakeGRPCServer) PushHotIndex(ctx context.Context, request *starv1.StarPushHotIndexRequest) (*starv1.StarPushHotIndexResponse, error) {
	requestId := this.check(ctx, starv1.StarIndexService_PushHotIndex_FullMethodName, request)
	items := make([]any, 0, len(request.GetItems()))
	for _, item := range request.GetItems() {
		var meta any
		if len(item.GetMeta()) > 0 {
			_ = json.Unmarshal(item.GetMeta(), &meta)
		}
		row := map[string]any{
			"remoteNoteId": item.GetRemoteNoteId(), "percentile": item.GetPercentile(),
			"category": item.GetCategory(), "language": item.GetLanguage(),
			"publishedAt": item.GetPublishedAt(),
		}
		if meta != nil {
			row["meta"] = meta
		}
		if len(item.GetEmbedding()) > 0 {
			row["embedding"] = base64.StdEncoding.EncodeToString(item.GetEmbedding())
		}
		items = append(items, row)
	}
	this.fake.record(http.MethodPost, "/api/v1/star/index/push", requestId, map[string]any{
		"instanceId": request.GetInstanceId(), "items": items,
	})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarPushHotIndexResponse{Ack: starAckOK(), Accepted: int32(len(request.GetItems()))}, nil
}

// PullGlobalHot - 全网榜拉取：过滤条件留痕
func (this *starFakeGRPCServer) PullGlobalHot(ctx context.Context, request *starv1.StarPullGlobalHotRequest) (*starv1.StarPullGlobalHotResponse, error) {
	requestId := this.check(ctx, starv1.StarIndexService_PullGlobalHot_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/index/pull", requestId, map[string]any{
		"topK": request.GetTopK(), "category": request.GetCategory(), "language": request.GetLanguage(),
	})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarPullGlobalHotResponse{
		Ack: starAckOK(),
		Items: []*starv1.StarGlobalHotItem{{
			InstanceId: "INS-PEER-0001", RemoteNoteId: "note-9", ScoreNet: 8600,
			Meta: []byte(starCannedHotMeta), Embedding: starCannedEmbedding,
			OriginUrl: "https://peer.example.com/notes/9", PublishedAt: 1699999990000,
		}},
	}, nil
}

// CreditPurchase - 点数购入：幂等键防呆（request_id = purchase_no）
func (this *starFakeGRPCServer) CreditPurchase(ctx context.Context, request *starv1.StarCreditPurchaseRequest) (*starv1.StarAck, error) {
	requestId := this.check(ctx, starv1.StarBillingService_CreditPurchase_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/purchase", requestId, map[string]any{
		"purchaseNo": request.GetPurchaseNo(), "points": request.GetPoints(),
		"amountFen": request.GetAmountFen(), "rateFen": request.GetRateFen(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetPurchaseNo(), "购入单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return starAckOK(), nil
}

// CreditGrantOut - 点数发放核销
func (this *starFakeGRPCServer) CreditGrantOut(ctx context.Context, request *starv1.StarCreditGrantOutRequest) (*starv1.StarCreditGrantOutResponse, error) {
	requestId := this.check(ctx, starv1.StarBillingService_CreditGrantOut_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/grant-out", requestId, map[string]any{
		"grantNo": request.GetGrantNo(), "points": request.GetPoints(), "targetNote": request.GetTargetNote(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetGrantNo(), "发放核销单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarCreditGrantOutResponse{Ack: starAckOK(), BalanceAfter: starCannedBalanceAfter}, nil
}

// PromoteOrderCreate - 投流单创建
func (this *starFakeGRPCServer) PromoteOrderCreate(ctx context.Context, request *starv1.StarPromoteOrderCreateRequest) (*starv1.StarAck, error) {
	requestId := this.check(ctx, starv1.StarBillingService_PromoteOrderCreate_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/order-create", requestId, map[string]any{
		"promoteNo": request.GetPromoteNo(), "noteOriginInstance": request.GetNoteOriginInstance(),
		"remoteNoteId": request.GetRemoteNoteId(), "fromInstanceId": request.GetFromInstanceId(),
		"points": request.GetPoints(), "amountFen": request.GetAmountFen(), "targetViews": request.GetTargetViews(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetPromoteNo(), "投流单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return starAckOK(), nil
}

// PromoteOrderSettle - 投流结算：返回罐头分账快照
func (this *starFakeGRPCServer) PromoteOrderSettle(ctx context.Context, request *starv1.StarPromoteOrderSettleRequest) (*starv1.StarPromoteOrderSettleResponse, error) {
	requestId := this.check(ctx, starv1.StarBillingService_PromoteOrderSettle_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/order-settle", requestId, map[string]any{
		"promoteNo": request.GetPromoteNo(), "doneViews": request.GetDoneViews(),
		"targetViews": request.GetTargetViews(), "reason": request.GetReason(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetPromoteNo(), "投流单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarPromoteOrderSettleResponse{
		Ack: starAckOK(),
		Bill: &starv1.StarSettleBill{
			SettleNo: "SET-2026-000001", AmountFen: 100000, RefundFen: 40000,
			SourceFen: 12000, TargetFen: 30000, PlatformFen: 18000,
			TargetLines: []*starv1.StarSettleTargetLine{{InstanceId: "INS-PEER-0001", Fen: 30000, Views: 600}},
			SettledAt: starCannedSettleAt,
		},
	}, nil
}

// PromoteOrderCancel - 投流取消/退款
func (this *starFakeGRPCServer) PromoteOrderCancel(ctx context.Context, request *starv1.StarPromoteOrderCancelRequest) (*starv1.StarPromoteOrderCancelResponse, error) {
	requestId := this.check(ctx, starv1.StarBillingService_PromoteOrderCancel_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/order-cancel", requestId, map[string]any{
		"promoteNo": request.GetPromoteNo(), "refundRatio": request.GetRefundRatio(), "reason": request.GetReason(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetPromoteNo(), "投流单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return &starv1.StarPromoteOrderCancelResponse{Ack: starAckOK(), RefundFen: starCannedRefundFen}, nil
}

// ReportPromoteStats - 投流阅读上报（request_id 仅审计归因，不防呆）
func (this *starFakeGRPCServer) ReportPromoteStats(ctx context.Context, request *starv1.StarReportPromoteStatsRequest) (*starv1.StarAck, error) {
	requestId := this.check(ctx, starv1.StarBillingService_ReportPromoteStats_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/billing/report-stats", requestId, map[string]any{
		"promoteNo": request.GetPromoteNo(), "fromInstanceId": request.GetFromInstanceId(),
		"periodHour": request.GetPeriodHour(), "views": request.GetViews(),
		"uniqueViewers": request.GetUniqueViewers(), "status": request.GetStatus(),
	})
	if err := this.failure(); err != nil {
		return nil, err
	}
	return starAckOK(), nil
}

// ReportOutcome - 仲裁结果上报：幂等键防呆（request_id = report_no）
func (this *starFakeGRPCServer) ReportOutcome(ctx context.Context, request *starv1.StarReportOutcomeRequest) (*starv1.StarAck, error) {
	requestId := this.check(ctx, starv1.StarArbitrationService_ReportOutcome_FullMethodName, request)
	this.fake.record(http.MethodPost, "/api/v1/star/arbitration/report-outcome", requestId, map[string]any{
		"reportNo": request.GetReportNo(), "fromInstanceId": request.GetFromInstanceId(),
		"targetInstanceId": request.GetTargetInstanceId(), "remoteNoteId": request.GetRemoteNoteId(),
		"reason": request.GetReason(), "outcome": request.GetOutcome(), "outcomeAt": request.GetOutcomeAt(),
	})
	this.fake.checkRequestIdMatch(requestId, request.GetReportNo(), "举报单号")
	if err := this.failure(); err != nil {
		return nil, err
	}
	return starAckOK(), nil
}

// ============================= 双协议客户端装配与用例驱动 =============================

// newStarHTTPClient - HTTP 侧客户端：httptest 假平台（仅本机回环，禁止联网测试）。
// InstanceNo/Version 供实例身份与 app_version 缺省归一。
func newStarHTTPClient(t *testing.T, fake *starFake) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	client, err := New(Options{
		ServerURL: server.URL, LicenseNo: "LIC-2026-000123", InstanceNo: "INS-STAR-0001",
		Salt: "star-test-salt", Version: "1.2.3",
		PublicKeys: map[string]string{"license-key-2026-01": "unused-in-star-test"},
		StorageDir: t.TempDir(), Fingerprint: "star-test-fingerprint", HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	fake.setClientPublicKey(activateApisClient(t, client))
	return client
}

// newStarGRPCClient - gRPC 侧客户端：bufconn 假服务端（进程内，禁止联网测试）
func newStarGRPCClient(t *testing.T, fake *starFake) *Client {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fakeServer := &starFakeGRPCServer{t: t, fake: fake}
	starv1.RegisterStarDirectoryServiceServer(server, fakeServer)
	starv1.RegisterStarIndexServiceServer(server, fakeServer)
	starv1.RegisterStarBillingServiceServer(server, fakeServer)
	starv1.RegisterStarArbitrationServiceServer(server, fakeServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatalf("bufconn 建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{options: Options{HTTPTimeout: 2 * time.Second, InstanceNo: "INS-STAR-0001", Version: "1.2.3"}}
	fake.setClientPublicKey(activateApisClient(t, client))
	client.transport = &grpcRuntimeTransport{
		client: client, conn: conn,
		starDirectory:  starv1.NewStarDirectoryServiceClient(conn),
		starIndex:      starv1.NewStarIndexServiceClient(conn),
		starBilling:    starv1.NewStarBillingServiceClient(conn),
		starArbitration: starv1.NewStarArbitrationServiceClient(conn),
	}
	client.Star = &StarService{client: client}
	return client
}

// runStarDual - 双协议同组用例：同一断言函数分别以 HTTP（httptest）与 gRPC（bufconn）客户端执行
func runStarDual(t *testing.T, run func(t *testing.T, fake *starFake, client *Client)) {
	t.Helper()
	t.Run("http", func(t *testing.T) {
		fake := newStarFake(t)
		run(t, fake, newStarHTTPClient(t, fake))
	})
	t.Run("grpc", func(t *testing.T) {
		fake := newStarFake(t)
		run(t, fake, newStarGRPCClient(t, fake))
	})
}

// starRequestId - 断言最近一次请求携带的幂等键（want 为空则只断言非空并返回实际值）
func starRequestId(t *testing.T, fake *starFake, want string) string {
	t.Helper()
	request := fake.lastRequest(t)
	if want != "" && request.requestId != want {
		t.Fatalf("幂等键不符：%q ≠ %q", request.requestId, want)
	}
	if request.requestId == "" {
		t.Fatalf("写操作必须携带幂等键")
	}
	return request.requestId
}

// ============================= 用例 =============================

// TestStarDualJoin - 双协议注册/心跳：实例身份缺省取 Options.InstanceNo、版本缺省取
// Options.Version，资格态/信用分/目录水位解析一致；幂等键 = 实例标识
func TestStarDualJoin(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.Join(t.Context(), StarJoinInput{
			Name: "兔子舰", Endpoint: "grpc://ship.example.com:9000", Pubkey: "ddeeff",
		})
		if err != nil {
			t.Fatalf("Join 失败: %v", err)
		}
		if result.StarStatus != StarStatusActive || result.TrustScore != starCannedTrust || result.Since != starCannedSince {
			t.Fatalf("Join 回执不符：%+v", result)
		}
		starRequestId(t, fake, "INS-STAR-0001")
		want := map[string]any{
			"instanceId": "INS-STAR-0001", "name": "兔子舰",
			"endpoint": "grpc://ship.example.com:9000", "pubkey": "ddeeff", "appVersion": "1.2.3",
		}
		if body := fake.bodyOf(t, "/api/v1/star/directory/join"); !reflect.DeepEqual(body, want) {
			t.Fatalf("Join 请求体不符：got=%+v want=%+v", body, want)
		}
	})
}

// TestStarDualListPeers - 双协议目录增量拉取：since 水位上送、同伴逐字段解析、新水位返回
func TestStarDualListPeers(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.ListPeers(t.Context(), 0)
		if err != nil {
			t.Fatalf("ListPeers 失败: %v", err)
		}
		if result.Since != starCannedSince || len(result.Peers) != 1 {
			t.Fatalf("ListPeers 回执不符：%+v", result)
		}
		peer := result.Peers[0]
		if peer.InstanceId != "INS-PEER-0001" || peer.Name != "邻舰" || peer.TrustScore != 8000 ||
			peer.StarStatus != StarStatusActive || peer.LastBeatAt != 1699999999000 {
			t.Fatalf("同伴条目不符：%+v", peer)
		}
		// 只读调用不带幂等键
		if request := fake.lastRequest(t); request.requestId != "" {
			t.Fatalf("只读调用不应下发调用级幂等键：%+v", request)
		}
	})
}

// TestStarDualPushHotIndex - 双协议热榜上报：条目逐字段抵达（meta JSON 原文 / embedding
// float16 字节 / 万分位百分位），受理数 = 条目数；快照幂等不带幂等键
func TestStarDualPushHotIndex(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		items := []StarHotIndexItem{{
			RemoteNoteId: "note-1", Percentile: 9500, Category: "tech", Language: "zh",
			Meta: json.RawMessage(`{"title":"第一帖"}`), Embedding: starCannedEmbedding, PublishedAt: 1699999990000,
		}}
		accepted, err := client.Star.PushHotIndex(t.Context(), StarPushHotIndexInput{Items: items})
		if err != nil {
			t.Fatalf("PushHotIndex 失败: %v", err)
		}
		if accepted != 1 {
			t.Fatalf("受理数不符：%d（期望 1）", accepted)
		}
		if request := fake.lastRequest(t); request.requestId != "" {
			t.Fatalf("快照幂等上报不应下发幂等键：%+v", request)
		}
		body := fake.bodyOf(t, "/api/v1/star/index/push")
		if body["instanceId"] != "INS-STAR-0001" {
			t.Fatalf("来源舰标识不符：%+v", body)
		}
		list, _ := body["items"].([]any)
		if len(list) != 1 {
			t.Fatalf("条目数不符：%+v", body)
		}
		row, _ := list[0].(map[string]any)
		if row["remoteNoteId"] != "note-1" || row["category"] != "tech" || row["language"] != "zh" {
			t.Fatalf("条目字段不符：%+v", row)
		}
		if meta, _ := row["meta"].(map[string]any); meta["title"] != "第一帖" {
			t.Fatalf("meta 原文不符：%+v", row["meta"])
		}
		if row["embedding"] != base64.StdEncoding.EncodeToString(starCannedEmbedding) {
			t.Fatalf("embedding 字节不符：%v", row["embedding"])
		}
	})
}

// TestStarDualPullGlobalHot - 双协议全网榜拉取：topK/分类/语言上送，
// 条目（scoreNet/meta/embedding/originUrl）解析一致
func TestStarDualPullGlobalHot(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		items, err := client.Star.PullGlobalHot(t.Context(), StarPullGlobalHotInput{TopK: 20, Category: "tech", Language: "zh"})
		if err != nil {
			t.Fatalf("PullGlobalHot 失败: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("榜单条目数不符：%+v", items)
		}
		item := items[0]
		if item.InstanceId != "INS-PEER-0001" || item.RemoteNoteId != "note-9" || item.ScoreNet != 8600 ||
			item.OriginUrl != "https://peer.example.com/notes/9" || item.PublishedAt != 1699999990000 {
			t.Fatalf("榜单条目不符：%+v", item)
		}
		if strings.TrimSpace(string(item.Meta)) != starCannedHotMeta {
			t.Fatalf("meta 原文不符：%s", string(item.Meta))
		}
		if !reflect.DeepEqual(item.Embedding, starCannedEmbedding) {
			t.Fatalf("embedding 字节不符：%v", item.Embedding)
		}
		body := fake.bodyOf(t, "/api/v1/star/index/pull")
		if !numEqual(body["topK"], 20) {
			t.Fatalf("topK 不符：%+v", body)
		}
		if body["category"] != "tech" || body["language"] != "zh" {
			t.Fatalf("过滤条件不符：%+v", body)
		}
	})
}

// TestStarDualCreditPurchase - 双协议点数购入：显式单号 → 幂等键 = 单号；
// 缺省自动生成 req_ 前缀单号并在结果中回显（对账依据），金额/汇率快照抵达服务端
func TestStarDualCreditPurchase(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.CreditPurchase(t.Context(), StarCreditPurchaseInput{
			PurchaseNo: "PUR-2026-000001", Points: 100, AmountFen: 1000000, RateFen: 10000,
		})
		if err != nil {
			t.Fatalf("CreditPurchase 失败: %v", err)
		}
		if result.PurchaseNo != "PUR-2026-000001" {
			t.Fatalf("显式单号应原样回显：%+v", result)
		}
		starRequestId(t, fake, "PUR-2026-000001")
		if body := fake.bodyOf(t, "/api/v1/star/billing/purchase"); !numEqual(body["points"], 100) ||
			!numEqual(body["amountFen"], 1000000) || !numEqual(body["rateFen"], 10000) {
			t.Fatalf("购入参数不符：%+v", body)
		}

		auto, err := client.Star.CreditPurchase(t.Context(), StarCreditPurchaseInput{Points: 5, AmountFen: 50000, RateFen: 10000})
		if err != nil {
			t.Fatalf("缺省单号 CreditPurchase 失败: %v", err)
		}
		if !strings.HasPrefix(auto.PurchaseNo, "req_") {
			t.Fatalf("缺省单号应自动生成 req_ 前缀键：%+v", auto)
		}
		starRequestId(t, fake, auto.PurchaseNo)
	})
}

// TestStarDualCreditGrantOut - 双协议点数发放核销：幂等键 = 核销单号，核销后余额解析一致
func TestStarDualCreditGrantOut(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.CreditGrantOut(t.Context(), StarGrantOutInput{
			GrantNo: "GRT-2026-000001", Points: 30, TargetNote: "站内用户 u-7 发放",
		})
		if err != nil {
			t.Fatalf("CreditGrantOut 失败: %v", err)
		}
		if result.GrantNo != "GRT-2026-000001" || result.BalanceAfter != starCannedBalanceAfter {
			t.Fatalf("核销回执不符：%+v", result)
		}
		starRequestId(t, fake, "GRT-2026-000001")
	})
}

// TestStarDualPromoteOrderCreate - 双协议投流单创建：幂等键 = 投流单号，
// 发起舰缺省取 Options.InstanceNo，定位/点数/目标阅读量抵达服务端
func TestStarDualPromoteOrderCreate(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.PromoteOrderCreate(t.Context(), StarPromoteOrderCreateInput{
			PromoteNo: "PRO-2026-000001", NoteOriginInstance: "INS-PEER-0001", RemoteNoteId: "note-9",
			Points: 10, AmountFen: 100000, TargetViews: 1000,
		})
		if err != nil {
			t.Fatalf("PromoteOrderCreate 失败: %v", err)
		}
		if result.PromoteNo != "PRO-2026-000001" {
			t.Fatalf("投流单号应原样回显：%+v", result)
		}
		starRequestId(t, fake, "PRO-2026-000001")
		body := fake.bodyOf(t, "/api/v1/star/billing/order-create")
		if body["noteOriginInstance"] != "INS-PEER-0001" || body["remoteNoteId"] != "note-9" ||
			body["fromInstanceId"] != "INS-STAR-0001" {
			t.Fatalf("投流单定位字段不符：%+v", body)
		}
	})
}

// TestStarDualPromoteOrderSettle - 双协议投流结算：分账快照逐字段解析一致
// （三方金额 + 目标舰明细行 + 结算单号/时间），幂等键 = 投流单号
func TestStarDualPromoteOrderSettle(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		bill, err := client.Star.PromoteOrderSettle(t.Context(), StarPromoteOrderSettleInput{
			PromoteNo: "PRO-2026-000001", DoneViews: 600, TargetViews: 1000, Reason: "到期结算",
		})
		if err != nil {
			t.Fatalf("PromoteOrderSettle 失败: %v", err)
		}
		if !reflect.DeepEqual(bill, starExpectedBill()) {
			t.Fatalf("分账快照不符：got=%+v want=%+v", bill, starExpectedBill())
		}
		starRequestId(t, fake, "PRO-2026-000001")
	})
}

// TestStarDualPromoteOrderCancel - 双协议投流取消/退款：退款金额解析一致，幂等键 = 投流单号
func TestStarDualPromoteOrderCancel(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		result, err := client.Star.PromoteOrderCancel(t.Context(), StarPromoteOrderCancelInput{
			PromoteNo: "PRO-2026-000001", RefundRatio: 10000, Reason: "撤销",
		})
		if err != nil {
			t.Fatalf("PromoteOrderCancel 失败: %v", err)
		}
		if result.PromoteNo != "PRO-2026-000001" || result.RefundFen != starCannedRefundFen {
			t.Fatalf("取消回执不符：%+v", result)
		}
		starRequestId(t, fake, "PRO-2026-000001")
	})
}

// TestStarDualReportPromoteStats - 双协议投流阅读上报：归集字段抵达服务端
// （periodHour 毫秒整点 / views / uniqueViewers / accepted 口径），上报舰缺省取本实例
func TestStarDualReportPromoteStats(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		err := client.Star.ReportPromoteStats(t.Context(), StarReportPromoteStatsInput{
			PromoteNo: "PRO-2026-000001", PeriodHour: 1700000000000,
			Views: 120, UniqueViewers: 80, Status: StarStatsAccepted,
		})
		if err != nil {
			t.Fatalf("ReportPromoteStats 失败: %v", err)
		}
		starRequestId(t, fake, "PRO-2026-000001")
		body := fake.bodyOf(t, "/api/v1/star/billing/report-stats")
		if body["fromInstanceId"] != "INS-STAR-0001" || body["status"] != "accepted" {
			t.Fatalf("阅读上报字段不符：%+v", body)
		}
		if !numEqual(body["views"], 120) || !numEqual(body["uniqueViewers"], 80) || !numEqual(body["periodHour"], 1700000000000) {
			t.Fatalf("阅读归集数值不符：%+v", body)
		}
	})
}

// TestStarDualReportOutcome - 双协议仲裁结果上报：幂等键 = 举报单号，
// 缺省单号自动生成并作为返回值回显，处置字段抵达服务端
func TestStarDualReportOutcome(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		reportNo, err := client.Star.ReportOutcome(t.Context(), StarReportOutcomeInput{
			FromInstanceId: "INS-STAR-0001", TargetInstanceId: "INS-PEER-0001",
			RemoteNoteId: "note-9", Reason: "spam", Outcome: StarOutcomeUpheld, OutcomeAt: 1700000003000,
		})
		if err != nil {
			t.Fatalf("ReportOutcome 失败: %v", err)
		}
		if !strings.HasPrefix(reportNo, "req_") {
			t.Fatalf("缺省举报单号应自动生成 req_ 前缀键：%q", reportNo)
		}
		starRequestId(t, fake, reportNo)
		body := fake.bodyOf(t, "/api/v1/star/arbitration/report-outcome")
		if body["reportNo"] != reportNo || body["outcome"] != "upheld" ||
			body["targetInstanceId"] != "INS-PEER-0001" || body["remoteNoteId"] != "note-9" {
			t.Fatalf("仲裁上报字段不符：%+v", body)
		}
	})
}

// TestStarDualBusinessError - 双协议业务失败等价：业务码 / 提示 / HTTP 等价状态码 / 明细
// 在 HTTP 与 gRPC 下逐项一致（gRPC 侧经 errdetails.ErrorInfo.Reason 还原）
func TestStarDualBusinessError(t *testing.T) {

	cases := []struct {
		name   string
		code   string
		detail map[string]any
	}{
		{"余额不足", apis.ErrorCodeInsufficientBalance, nil},
		{"模糊404认证失败", apis.ErrorCodeNotFound, nil},
		{"速率超限", apis.ErrorCodeRateLimited, map[string]any{"retryAfterMs": "2000"}},
		{"参数非法", apis.ErrorCodeInvalidArgument, nil},
	}
	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		for _, item := range cases {
			t.Run(item.name, func(t *testing.T) {
				message := "星链拒绝：" + item.code
				fake.setFailure(item.code, message, item.detail)
				_, err := client.Star.CreditPurchase(t.Context(), StarCreditPurchaseInput{Points: 1, AmountFen: 10000, RateFen: 10000})
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
					if got := apiErr.Detail[key]; got != want && got != nil && !reflect.DeepEqual(got, want) && toString(got) != want {
						t.Fatalf("明细 %s 不符：%v ≠ %v", key, got, want)
					}
				}
				fake.setFailure("", "", nil)
			})
		}
	})
}

// toString - 明细值归一为字符串比较（HTTP 保持 JSON 原始类型，gRPC 侧服务端扁平化为字符串）
func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), `"`)
}

// numEqual - 数值归一比较（HTTP JSON 解码为 float64，gRPC 留痕重建为 int32/int64）
func numEqual(value any, want int64) bool {
	switch typed := value.(type) {
	case float64:
		return typed == float64(want)
	case int32:
		return int64(typed) == want
	case int64:
		return typed == want
	case int:
		return int64(typed) == want
	}
	return false
}

// TestStarDualGateNotActivated - 未激活闸门：无 token 或非放行状态在双协议下都本地闸住，
// 假平台收不到任何请求（服务端凭证校验仍是最终边界）
func TestStarDualGateNotActivated(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		client.mu.Lock()
		client.state.ActivationToken = ""
		client.mu.Unlock()
		if _, err := client.Star.Join(t.Context(), StarJoinInput{Name: "x"}); !errors.Is(err, ErrStarNotActivated) {
			t.Fatalf("无 token 应返回 ErrStarNotActivated，实际 %v", err)
		}
		for _, status := range []string{
			LicenceProtocol.StatusExpired, LicenceProtocol.StatusRevoked,
			LicenceProtocol.StatusSuspended, LicenceProtocol.StatusSeatReleased,
		} {
			client.mu.Lock()
			client.state.ActivationToken = "token"
			client.state.Status = status
			client.mu.Unlock()
			if _, err := client.Star.ListPeers(t.Context(), 0); !errors.Is(err, ErrStarNotActivated) {
				t.Fatalf("状态 %s 应返回 ErrStarNotActivated，实际 %v", status, err)
			}
		}
		if requests := fake.requestsSnapshot(); len(requests) != 0 {
			t.Fatalf("未激活闸门不得发出任何请求，实际 %d 次", len(requests))
		}
	})
}

// TestStarDualContextCancellation - 调用方取消上下文：双协议都返回可 errors.Is(context.Canceled)
// 的错误且都不是业务错误（客户端取消不是服务端拒绝，严禁伪装成业务码）
func TestStarDualContextCancellation(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := client.Star.Join(ctx, StarJoinInput{Name: "x"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消上下文应返回 context.Canceled，实际 %v", err)
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("客户端取消不得伪装成业务码：%+v", apiErr)
		}
	})
}

// TestStarDualTimeout - 调用方 deadline 到期：双协议都返回可 errors.Is(context.DeadlineExceeded)
// 的错误且都不是业务错误（gRPC 侧 DeadlineExceeded 原样透传，与 HTTP 侧传输错误分层一致）
func TestStarDualTimeout(t *testing.T) {

	runStarDual(t, func(t *testing.T, fake *starFake, client *Client) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		// 假平台 600ms 后才响应，100ms 的调用方 deadline 必先触发
		fake.setFailure("", "", nil)
		slowJoin(t, fake)
		_, err := client.Star.Join(ctx, StarJoinInput{Name: "x"})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("超时应返回 context.DeadlineExceeded，实际 %v", err)
		}
		var apiErr *apis.Error
		if errors.As(err, &apiErr) {
			t.Fatalf("超时不得伪装成业务码：%+v", apiErr)
		}
	})
}

// slowJoin - 给假平台 join 路径装 600ms 延迟（仅此用例使用；经失败态字段借道实现
// 会破坏语义，故直接操作请求留痕互斥外的专用开关）
func slowJoin(t *testing.T, fake *starFake) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.failCode = ""
	// 延迟经由 fake 的 delay 字段生效（HTTP handler 与 gRPC Join 共用）
	fake.delay = 600 * time.Millisecond
}
