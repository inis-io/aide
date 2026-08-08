package licence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inis-io/aide/utils"
)

// ============================= 管理面假平台 =============================

// fakeHub - 管理面假平台：{code,msg,data} 信封（HTTP 恒 200）+ Bearer token 校验，
// 镜像平台登录/鉴权行为；资源响应由各测试经 routes 注入。
type fakeHub struct {
	// mu - 状态读写锁
	mu sync.Mutex
	// server - httptest 实例
	server *httptest.Server

	// account/password - 认可的登录账密
	account  string
	password string
	// require2FA - 登录是否要求 TOTP（认可验证码 "123456"）
	require2FA bool
	// loginCalls - 登录调用次数
	loginCalls int
	// tokenSeq - 令牌签发序号（令牌值 token-N，便于区分重登）
	tokenSeq int
	// validToken - 当前认可的令牌
	validToken string
	// tokenTTL - 令牌有效期
	tokenTTL time.Duration

	// authGuard - 非登录接口是否校验 Bearer token
	authGuard bool
	// routes - 资源路由表（key = METHOD + " " + path）
	routes map[string]func(writer http.ResponseWriter, request *http.Request, body []byte)

	// auths - 历次请求的 Authorization 头
	auths []string
	// lastQuery - 最近一次请求的 query
	lastQuery url.Values
	// lastBody - 最近一次请求的 body
	lastBody []byte
	// lastRequest - 最近一次请求（multipart 解析用）
	lastRequest *http.Request
}

// newFakeHub - 创建假平台（默认账密 ops/secret，令牌有效期 1 小时）
func newFakeHub(t *testing.T) *fakeHub {

	t.Helper()
	hub := &fakeHub{
		account: "ops", password: "secret", tokenTTL: time.Hour,
		authGuard: true, routes: map[string]func(http.ResponseWriter, *http.Request, []byte){},
	}
	hub.server = httptest.NewServer(http.HandlerFunc(hub.handle))
	t.Cleanup(hub.server.Close)
	return hub
}

// handle - 路由分发：登录/登出/校验令牌内置，其余走 routes 注入
func (this *fakeHub) handle(writer http.ResponseWriter, request *http.Request) {

	body, _ := io.ReadAll(request.Body)
	// 恢复 body，供 multipart 等后续解析（平台参数中间件同款处理）
	request.Body = io.NopCloser(bytes.NewReader(body))

	this.mu.Lock()
	this.auths = append(this.auths, request.Header.Get("Authorization"))
	this.lastQuery = request.URL.Query()
	this.lastBody = body
	this.lastRequest = request
	this.mu.Unlock()

	switch request.Method + " " + request.URL.Path {
	case "POST /api/comm/sign-in":
		this.handleSignIn(writer, body)
		return
	case "DELETE /api/comm/sign-out":
		this.writeData(writer, nil)
		return
	}

	// 受控接口：校验 Bearer token
	this.mu.Lock()
	validToken := this.validToken
	guard := this.authGuard
	this.mu.Unlock()
	if guard && request.Header.Get("Authorization") != "Bearer "+validToken {
		this.writeError(writer, 401, "登录状态已失效，请重新登录！", nil)
		return
	}

	if route, ok := this.routes[request.Method+" "+request.URL.Path]; ok {
		route(writer, request, body)
		return
	}
	this.writeError(writer, 404, "接口不存在！", nil)
}

// handleSignIn - 登录：明文账密校验（平台在未携带四重签名头时按明文处理），可选 2FA 闸门
func (this *fakeHub) handleSignIn(writer http.ResponseWriter, body []byte) {

	var params struct {
		Account  string `json:"account"`
		Password string `json:"password"`
		Totp     string `json:"totp"`
	}
	if err := json.Unmarshal(body, &params); err != nil {
		this.writeError(writer, 400, "参数错误！", nil)
		return
	}

	this.mu.Lock()
	this.loginCalls++
	if params.Account != this.account || params.Password != this.password {
		this.mu.Unlock()
		this.writeError(writer, 400, "账号或密码错误！", nil)
		return
	}
	if this.require2FA && params.Totp == "" {
		this.mu.Unlock()
		this.writeError(writer, 400, "请提交 2FA 验证码！", map[string]any{"require2FA": true})
		return
	}
	if this.require2FA && params.Totp != "123456" {
		this.mu.Unlock()
		this.writeError(writer, 400, "2FA 验证码错误！", nil)
		return
	}
	this.tokenSeq++
	this.validToken = "token-" + strconv.Itoa(this.tokenSeq)
	token := this.validToken
	ttl := this.tokenTTL
	this.mu.Unlock()

	this.writeData(writer, map[string]any{
		"user": map[string]any{
			"id": 7, "userNo": "USR-000007", "account": "ops", "nickname": "运维",
			"userType": "member", "status": "normal", "qualificationStatus": "approved",
		},
		"token": map[string]any{
			"no": "session-no-" + token, "value": token,
			"expired": time.Now().Add(ttl).UnixMilli(),
		},
		"auth": map[string]any{"roles": []string{"SELF_USER"}},
	})
}

// writeData - 成功信封（HTTP 200 + code 200，与平台 facade.Comm.Json 一致）
func (this *fakeHub) writeData(writer http.ResponseWriter, data any) {
	this.writeEnvelope(writer, 200, "数据请求成功！", data)
}

// writeError - 业务错误信封（HTTP 200 + code != 200，与平台一致）
func (this *fakeHub) writeError(writer http.ResponseWriter, code int, msg string, data any) {
	this.writeEnvelope(writer, code, msg, data)
}

// writeEnvelope - 按平台 utils.Resp 结构写信封
func (this *fakeHub) writeEnvelope(writer http.ResponseWriter, code int, msg string, data any) {

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	raw, _ := json.Marshal(utils.Resp{Msg: msg, Code: code, Data: data})
	_, _ = writer.Write(raw)
}

// newClient - 创建连接假平台的客户端
func (this *fakeHub) newClient(t *testing.T) *AdminClient {

	t.Helper()
	client, err := NewAdmin(AdminOptions{ServerURL: this.server.URL, Account: this.account, Password: this.password})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	return client
}

// ============================= 登录与令牌 =============================

// TestNewRequireServerURL - ServerURL 必填校验
func TestNewRequireServerURL(t *testing.T) {

	if _, err := NewAdmin(AdminOptions{}); err == nil {
		t.Fatal("ServerURL 为空时应返回错误")
	}
}

// TestLogin - 显式登录：明文账密上送，token 内存保管
func TestLogin(t *testing.T) {

	hub := newFakeHub(t)
	client := hub.newClient(t)

	result, err := client.Login(context.Background())
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if result.Token.Value != "token-1" {
		t.Fatalf("令牌值不符: %s", result.Token.Value)
	}
	if result.User.Id != 7 || result.User.UserNo != "USR-000007" {
		t.Fatalf("用户信息解析不符: %+v", result.User)
	}
	if got := client.Token().Value; got != "token-1" {
		t.Fatalf("客户端保管令牌不符: %s", got)
	}

	// 账密以明文 JSON 上送（平台在未携带四重签名头时按明文校验）
	var sent map[string]any
	if err = json.Unmarshal(hub.lastBody, &sent); err != nil {
		t.Fatalf("登录请求体不是 JSON: %v", err)
	}
	if sent["account"] != "ops" || sent["password"] != "secret" {
		t.Fatalf("登录请求体不符: %s", string(hub.lastBody))
	}
}

// TestLoginRequire2FA - 平台要求 2FA 时返回 APIError.Require2FA=true
func TestLoginRequire2FA(t *testing.T) {

	hub := newFakeHub(t)
	hub.require2FA = true
	client := hub.newClient(t)

	_, err := client.Login(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("应返回 *APIError，实际: %T %v", err, err)
	}
	if !apiErr.Require2FA {
		t.Fatalf("Require2FA 标记应为 true: %+v", apiErr)
	}
}

// TestAutoLoginAndBearer - 未显式登录时首个请求自动登录，且自动携带 Bearer token
func TestAutoLoginAndBearer(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/projects/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, []map[string]any{{"id": 1, "projectNo": "PRJ-000001", "projectName": "演示项目"}})
	}
	client := hub.newClient(t)

	rows, err := client.Projects.Rows(context.Background(), nil)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	if len(rows) != 1 || rows[0].ProjectNo != "PRJ-000001" {
		t.Fatalf("列表解析不符: %+v", rows)
	}
	if hub.loginCalls != 1 {
		t.Fatalf("应自动登录一次，实际 %d 次", hub.loginCalls)
	}
	if len(hub.auths) < 2 || hub.auths[len(hub.auths)-1] != "Bearer token-1" {
		t.Fatalf("未自动携带 Bearer token: %v", hub.auths)
	}
}

// TestReLoginOn401 - 业务 401 时自动重登并原样重试一次
func TestReLoginOn401(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/projects/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, []map[string]any{{"id": 2, "projectNo": "PRJ-000002"}})
	}
	client := hub.newClient(t)
	// 注入平台不认可的旧令牌（未过期，绕过本地过期预判）
	client.SetToken(Token{Value: "stale", Expired: time.Now().Add(time.Hour).UnixMilli()})

	rows, err := client.Projects.Rows(context.Background(), nil)
	if err != nil {
		t.Fatalf("401 重登后请求应成功: %v", err)
	}
	if len(rows) != 1 || rows[0].Id != 2 {
		t.Fatalf("列表解析不符: %+v", rows)
	}
	if hub.loginCalls != 1 {
		t.Fatalf("应重登一次，实际 %d 次", hub.loginCalls)
	}
	// 首次携带 stale；登录请求不携带 token；重试携带重登后的新令牌
	if len(hub.auths) != 3 || hub.auths[0] != "Bearer stale" || hub.auths[1] != "" || hub.auths[2] != "Bearer token-1" {
		t.Fatalf("重登重试的令牌序列不符: %v", hub.auths)
	}
}

// TestTokenExpiredRelogin - 本地判定令牌过期（毫秒）时先重登再请求
func TestTokenExpiredRelogin(t *testing.T) {

	hub := newFakeHub(t)
	hub.routes["GET /api/projects/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeData(writer, []map[string]any{})
	}
	client := hub.newClient(t)
	// 注入已过期的令牌
	client.SetToken(Token{Value: "expired", Expired: time.Now().Add(-time.Minute).UnixMilli()})

	if _, err := client.Projects.Rows(context.Background(), nil); err != nil {
		t.Fatalf("过期重登后请求应成功: %v", err)
	}
	if hub.loginCalls != 1 || hub.auths[len(hub.auths)-1] != "Bearer token-1" {
		t.Fatalf("过期令牌应触发重登: calls=%d auths=%v", hub.loginCalls, hub.auths)
	}
}

// TestSignOut - 退出登录后本地令牌清除
func TestSignOut(t *testing.T) {

	hub := newFakeHub(t)
	client := hub.newClient(t)
	if _, err := client.Login(context.Background()); err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if err := client.SignOut(context.Background()); err != nil {
		t.Fatalf("退出失败: %v", err)
	}
	if client.Token().Value != "" {
		t.Fatalf("退出后令牌应清空，实际: %s", client.Token().Value)
	}
}

// ============================= 错误分层 =============================

// TestErrorKinds - HTTP 层错误与业务错误分开表达（表驱动）
func TestErrorKinds(t *testing.T) {

	hub := newFakeHub(t)
	// 业务错误：信封 code=403（HTTP 仍为 200）
	hub.routes["GET /api/projects/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		hub.writeError(writer, 403, "无权限！", nil)
	}
	// 传输层错误：网关级 HTTP 500（非信封）
	hub.routes["GET /api/instances/rows"] = func(writer http.ResponseWriter, request *http.Request, body []byte) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte("upstream broken"))
	}
	client := hub.newClient(t)
	ctx := context.Background()

	// 业务错误 → *APIError（code/msg 原样透出）
	_, err := client.Projects.Rows(ctx, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 403 || !strings.Contains(apiErr.Msg, "无权限") {
		t.Fatalf("业务错误应为 *APIError(403): %T %v", err, err)
	}

	// 传输层错误 → *HTTPError（状态码与原文透出）
	_, err = client.Instances.Rows(ctx, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 500 || !strings.Contains(httpErr.Body, "upstream") {
		t.Fatalf("传输错误应为 *HTTPError(500): %T %v", err, err)
	}
	if errors.As(err, &apiErr) {
		t.Fatal("传输错误不应误判为 *APIError")
	}

	// 网络错误（服务不可达）→ 原始包装错误，非 APIError/HTTPError
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close()
	deadClient, err := NewAdmin(AdminOptions{ServerURL: dead.URL, Account: "a", Password: "b"})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	_, err = deadClient.Login(ctx)
	if err == nil || errors.As(err, &apiErr) || errors.As(err, &httpErr) {
		t.Fatalf("网络错误不应归类为业务/HTTP错误: %T %v", err, err)
	}
}
