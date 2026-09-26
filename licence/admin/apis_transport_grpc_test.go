package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"github.com/spf13/cast"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// API 商城管理面 gRPC 侧用例（proto-less 服务）。
//
// 与 HTTP 用例共用同一张路由用例表（apis_test.go），逐条走真实 Invoke：
// ① 断言 SDK 分发的 full method 与平台登记表登记的服务/方法一致（打错即 Unimplemented 或走错方法，直接失败）；
// ② 断言请求体：GET 的 query 经传输层折叠为 JSON（数字/布尔还原为原生类型），写路径 body 原样透传；
// ③ 断言响应信封解析与 HTTP 侧同形（typed 方法与协议无关）。
// UploadUpstreamData 不经 RoundTrip，走 Upload() 的商城 early branch（uploadApis：JSON 承载 base64），
// 假服务同样按 full method 注册的 unary 方法处理，与平台装配方式一致。
//
// 假服务端按平台的装配方式注册 ServiceDesc（licencev1.AdminRequest/AdminResponse 信封，
// 无生成 stub），从而在 SDK 侧真正验证 conn.Invoke + fullMethod 常量链路。

// apisGRPCServer - proto-less 假服务端
type apisGRPCServer struct {
	// t - 测试上下文（用例内直接 Fatal 使用）
	t *testing.T
	// mu - 调用记录读写锁
	mu sync.Mutex
	// calls - 被调用的 full method → 请求体原文列表（同一方法应恰好一次）
	calls map[string][]string
	// byMethod - 方法名 → 用例（回写信封 data 用）
	byMethod map[string]apisRouteCase
}

// newApisGRPCServer - 按用例表装配 7 个 proto-less 服务
func newApisGRPCServer(t *testing.T, cases []apisRouteCase) (*apisGRPCServer, *grpc.Server) {

	t.Helper()
	server := &apisGRPCServer{t: t, calls: map[string][]string{}, byMethod: map[string]apisRouteCase{}}
	grouped := map[string][]string{}
	for _, one := range cases {
		server.byMethod[one.rpcName()] = one
		grouped[one.service] = append(grouped[one.service], one.rpcName())
	}

	grpcServer := grpc.NewServer()
	for service, methods := range grouped {
		grpcServer.RegisterService(server.serviceDesc(service, methods), server)
	}
	return server, grpcServer
}

// serviceDesc - 单个 proto-less 服务的 ServiceDesc（方法与平台 apisServiceDescs 同机制装配）
func (this *apisGRPCServer) serviceDesc(service string, methods []string) *grpc.ServiceDesc {

	desc := &grpc.ServiceDesc{
		ServiceName: apisGRPCPackage + "." + service,
		// 空接口占位：proto-less 服务无生成的服务接口，实现方恒满足（与平台 apisServer 同款）
		HandlerType: (*any)(nil),
		Methods:     make([]grpc.MethodDesc, 0, len(methods)),
		Streams:     []grpc.StreamDesc{},
		Metadata:    "grpc/admin/v1/apis.go",
	}
	for _, name := range methods {
		method := name
		desc.Methods = append(desc.Methods, grpc.MethodDesc{
			MethodName: method,
			Handler: func(handlerServer any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := new(licencev1.AdminRequest)
				if err := decode(request); err != nil {
					return nil, err
				}
				invoke := func(ctx context.Context, input any) (any, error) {
					return this.response(service, method, input.(*licencev1.AdminRequest)), nil
				}
				if interceptor == nil {
					return invoke(ctx, request)
				}
				fullMethod := "/" + apisGRPCPackage + "." + service + "/" + method
				return interceptor(ctx, request, &grpc.UnaryServerInfo{Server: handlerServer, FullMethod: fullMethod}, invoke)
			},
		})
	}
	return desc
}

// response - 记录调用并回写用例声明的信封 data
func (this *apisGRPCServer) response(service string, method string, request *licencev1.AdminRequest) *licencev1.AdminResponse {

	fullMethod := "/" + apisGRPCPackage + "." + service + "/" + method
	this.mu.Lock()
	this.calls[fullMethod] = append(this.calls[fullMethod], string(request.GetJson()))
	one := this.byMethod[method]
	this.mu.Unlock()

	raw, err := json.Marshal(one.data)
	if err != nil {
		this.t.Fatalf("用例 %s 的 data 无法序列化: %v", method, err)
	}
	return &licencev1.AdminResponse{Code: http.StatusOK, Message: "数据请求成功！", DataJson: raw}
}

// bodies - 指定 full method 的请求体原文列表
func (this *apisGRPCServer) bodies(fullMethod string) []string {

	this.mu.Lock()
	defer this.mu.Unlock()
	return append([]string(nil), this.calls[fullMethod]...)
}

// hitMethods - 已被调用的 full method 集合（排序）
func (this *apisGRPCServer) hitMethods() []string {

	this.mu.Lock()
	defer this.mu.Unlock()
	methods := make([]string, 0, len(this.calls))
	for fullMethod := range this.calls {
		methods = append(methods, fullMethod)
	}
	slices.Sort(methods)
	return methods
}

// TestApisRoutesGRPC - 60 条商城路由逐条经真实 gRPC 调用（bufconn）校验
func TestApisRoutesGRPC(t *testing.T) {

	cases := apisRouteCases()
	server, grpcServer := newApisGRPCServer(t, cases)

	listener := bufconn.Listen(1 << 20)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatalf("建立 bufconn 连接失败: %v", err)
	}
	defer conn.Close()

	client := &AdminClient{options: AdminOptions{}}
	client.transport = &grpcAdminTransport{client: client, conn: conn}
	client.Apis = &ApisResource{client: client}

	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {

			one.invoke(t, client)

			fullMethod := "/" + apisGRPCPackage + "." + one.service + "/" + one.rpcName()
			bodies := server.bodies(fullMethod)
			if len(bodies) != 1 {
				t.Fatalf("%s 应恰好调用一次，实际 %d 次（已调用：%v）", fullMethod, len(bodies), server.hitMethods())
			}
			body := bodies[0]

			// 写路径：请求体原样透传，逐个子串断言（与 HTTP 侧同口径）
			for _, needle := range one.wantBody {
				if !strings.Contains(body, needle) {
					t.Errorf("请求体缺少 %q：%s", needle, body)
				}
			}

			// GET：query 经 queryJSON 折叠为 JSON（数字还原为数字、数组还原为数组）
			if one.method != http.MethodGet {
				return
			}
			parsed := map[string]any{}
			if err := json.Unmarshal([]byte(body), &parsed); err != nil {
				t.Fatalf("GET 请求体应是 JSON: %v（%s）", err, body)
			}
			for key, want := range one.wantQuery {
				if got := cast.ToString(parsed[key]); got != want {
					t.Errorf("JSON %s = %q，期望 %q（%s）", key, got, want, body)
				}
			}
			for key, want := range one.wantMultiQuery {
				name := strings.TrimSuffix(key, "[]")
				list, ok := parsed[name].([]any)
				if !ok || len(list) != len(want) {
					t.Errorf("JSON %s = %v，期望 %v（%s）", name, parsed[name], want, body)
					continue
				}
				for index, item := range list {
					if got := cast.ToString(item); got != want[index] {
						t.Errorf("JSON %s[%d] = %q，期望 %q（%s）", name, index, got, want[index], body)
					}
				}
			}
		})
	}

	// 全局闭环：被调用的 full method 集合与用例表声明的集合逐字相同（不多不少，无跨用例串线）
	expected := make([]string, 0, len(cases))
	for _, one := range cases {
		expected = append(expected, "/"+apisGRPCPackage+"."+one.service+"/"+one.rpcName())
	}
	slices.Sort(expected)
	if got := server.hitMethods(); !slices.Equal(got, expected) {
		t.Fatalf("被调用的 full method 集合与用例表不一致：\n实际 %v\n期望 %v", got, expected)
	}
}

// TestApisGRPCBusinessErrorEnvelope - gRPC 侧业务错误（code != 200 的成功响应）映射为 *APIError，
// 且结构化明细（data_json）原样保留——商城服务不抛 status error，因此不受传输层错误映射影响
func TestApisGRPCBusinessErrorEnvelope(t *testing.T) {

	grpcServer := grpc.NewServer()
	raw := []byte(`{"bizCode":"RECHARGE_BELOW_MIN","minAmount":10000}`)
	grpcServer.RegisterService(&grpc.ServiceDesc{
		ServiceName: apisGRPCPackage + ".ApisBalanceAdminService",
		HandlerType: (*any)(nil),
		Streams:     []grpc.StreamDesc{},
		Metadata:    "grpc/admin/v1/apis.go",
		Methods: []grpc.MethodDesc{{
			MethodName: "CreateRecharge",
			Handler: func(any, context.Context, func(any) error, grpc.UnaryServerInterceptor) (any, error) {
				return &licencev1.AdminResponse{Code: http.StatusBadRequest, Message: "充值金额不得低于平台下限（10 元）！", DataJson: raw}, nil
			},
		}},
	}, &apisGRPCServer{t: t})

	listener := bufconn.Listen(1 << 20)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	if err != nil {
		t.Fatalf("建立 bufconn 连接失败: %v", err)
	}
	defer conn.Close()

	client := &AdminClient{options: AdminOptions{}}
	client.transport = &grpcAdminTransport{client: client, conn: conn}
	client.Apis = &ApisResource{client: client}

	_, err = client.Apis.CreateWalletRecharge(context.Background(), WalletRechargeInput{Amount: 100, RequestId: "rcg-1"})
	var apiErr *APIError
	if err == nil || !strings.Contains(err.Error(), "下限") {
		t.Fatalf("业务错误应返回 *APIError: %T %v", err, err)
	}
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest {
		t.Fatalf("业务错误码应映射为 400: %+v", apiErr)
	}
	if !strings.Contains(string(apiErr.Data), "RECHARGE_BELOW_MIN") {
		t.Fatalf("业务错误明细应原样保留: %s", string(apiErr.Data))
	}
}
