// 管理面客户端（AdminClient）：Licen Hub 授权平台「管理面」Go SDK（typed client）
//
// 管理面是平台后台登录态接口，协议为 {code,msg,data} JSON 信封（HTTP 状态码恒为 200，
// 业务结果看 code），与运行面（同包 Client，Ed25519 信封）完全不同。
// 使用方是商户自有运维系统/CI：账密登录换取 JWT，随后按资源调用受控接口。
//
// 平台事实（与 licen-hub/backend 逐一对齐）：
//   - 登录：POST /api/comm/sign-in，{account,password,totp?}，data 内 {user,token,auth}；
//     账密以明文 JSON 上送（平台在携带 X-Khronos/X-Ss-Stub/X-Helios/X-Medusa 四重签名头时
//     才走 AES 加密通道，本 SDK 未实现该通道，请务必经 HTTPS 部署）。
//   - 鉴权：请求头 Authorization: Bearer <token.value>（token 仅内存保管，不落盘）。
//   - 路由：统一 /api/{table}/{key}；GET 走 query，POST/PUT/DELETE 走 JSON body。
//   - 限制：平台若开启「API 签名验证」（safety.api.sign），所有请求需携带 X-* 签名头，
//     本 SDK 不支持该模式（默认关闭）。
package licence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cast"
)

// signInPath - 登录接口路径（公开接口，不携带 token，不参与 401 重登）
const signInPath = "/api/comm/sign-in"

// AdminOptions - 管理面客户端配置（ServerURL 为平台 URI 唯一入口）
type AdminOptions struct {
	// ServerURL - 平台地址（必填），如 "https://hub.example.com"
	ServerURL string
	// Account - 登录账号（自动登录与 401 重登的凭据；不填则只能 SetToken 注入令牌）
	Account string
	// Password - 登录密码（明文上送，依赖 HTTPS 保护，见包注释）
	Password string
	// TOTP - 2FA 验证码（可选；账号开启双因素认证且平台启用 2FA 时必填，
	// 验证码过期后登录返回 APIError.Require2FA=true，需更新后重试）
	TOTP string
	// HTTPTimeout - 单次请求超时（默认 15 秒）
	HTTPTimeout time.Duration
}

// AdminClient - 管理面客户端（登录态 + 各资源 typed 入口）
// 通过 New 创建；首次请求自动登录，401 时自动重登并重试一次。
type AdminClient struct {
	// options - 归一化后的配置
	options AdminOptions
	// http - HTTP 传输层
	http *http.Client

	// loginMu - 登录串行（多协程同时触发登录时只登录一次）
	loginMu sync.Mutex
	// mu - token 读写锁
	mu sync.RWMutex
	// token - 登录令牌（仅内存保管）
	token Token

	// Qualification - 资格审核（/api/qualification/*）
	Qualification *QualificationResource
	// Projects - 项目（/api/projects/*）
	Projects *ProjectsResource
	// Instances - 部署实例（/api/instances/*）
	Instances *InstancesResource
	// Licenses - 许可证与授权申请（/api/licenses/*）
	Licenses *LicensesResource
	// SigningKeys - 签名密钥（/api/signing-keys/*）
	SigningKeys *SigningKeysResource
	// Artifacts - 项目发布物（/api/project-artifacts/*）
	Artifacts *ArtifactsResource
	// Versions - 项目版本（/api/project-versions/*）
	Versions *VersionsResource
}

// New - 创建管理面客户端（归一化配置，不发起网络请求）
/**
 * @param options AdminOptions - 客户端配置（ServerURL 必填；Account/Password 用于自动登录）
 * @return *AdminClient - 客户端实例
 * @example：
 * 	client, err := licence.NewAdmin(licence.AdminOptions{
 * 		ServerURL: "https://hub.example.com", Account: "ops", Password: "secret",
 * 	})
 */
func NewAdmin(options AdminOptions) (*AdminClient, error) {

	if options.ServerURL == "" {
		return nil, errors.New("ServerURL 不能为空（平台 URI 入口）")
	}
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = 15 * time.Second
	}

	client := &AdminClient{options: options, http: &http.Client{Timeout: options.HTTPTimeout}}
	client.Qualification = &QualificationResource{client: client}
	client.Projects = &ProjectsResource{client: client}
	client.Instances = &InstancesResource{client: client}
	client.Licenses = &LicensesResource{client: client}
	client.SigningKeys = &SigningKeysResource{client: client}
	client.Artifacts = &ArtifactsResource{client: client}
	client.Versions = &VersionsResource{client: client}
	return client, nil
}

// ============================= 登录态 =============================

// Login - 显式登录：POST /api/comm/sign-in，成功后将 token 内存保管
// 一般无需调用——首次请求会自动登录；账号开启 2FA 且 AdminOptions.TOTP 失效时，
// 返回 *APIError（Require2FA=true），更新 AdminOptions.TOTP 后重试。
func (this *AdminClient) Login(ctx context.Context) (*SignInResult, error) {

	this.loginMu.Lock()
	defer this.loginMu.Unlock()
	return this.loginLocked(ctx)
}

// SignOut - 退出登录：DELETE /api/comm/sign-out（无论平台结果如何都清除本地令牌）
func (this *AdminClient) SignOut(ctx context.Context) error {

	_, err := this.doRequest(ctx, http.MethodDelete, "/api/comm/sign-out", nil, nil, "", false)
	this.SetToken(Token{})
	return err
}

// CheckToken - 校验/刷新令牌：POST /api/comm/check-token
// refresh=true 时平台续期并返回新令牌，本方法同步更新本地令牌。
func (this *AdminClient) CheckToken(ctx context.Context, refresh bool) (*SignInResult, error) {

	body, err := json.Marshal(map[string]any{"refresh": refresh})
	if err != nil {
		return nil, err
	}
	data, err := this.doRequest(ctx, http.MethodPost, "/api/comm/check-token", nil, body, "application/json", false)
	if err != nil {
		return nil, err
	}

	var result SignInResult
	if err = decodeData(data, &result); err != nil {
		return nil, err
	}
	if refresh && result.Token.Value != "" {
		this.SetToken(result.Token)
	}
	return &result, nil
}

// Token - 当前登录令牌（内存副本；Expired 为毫秒时间戳——平台代码按 UnixMilli 签发，
// 平台 types.TokenResp 注释标注的「秒」与实际不符，以毫秒为准）
func (this *AdminClient) Token() Token {

	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.token
}

// SetToken - 注入外部保管的令牌（如 CI 复用已有会话；注入后跳过自动登录）
func (this *AdminClient) SetToken(token Token) {

	this.mu.Lock()
	defer this.mu.Unlock()
	this.token = token
}

// loginLocked - 登录核心（调用前必须持有 loginMu）
func (this *AdminClient) loginLocked(ctx context.Context) (*SignInResult, error) {

	if !this.canLogin() {
		return nil, errors.New("Account/Password 不能为空（登录凭据缺失）")
	}

	body, err := json.Marshal(map[string]any{
		"account": this.options.Account, "password": this.options.Password, "totp": this.options.TOTP,
	})
	if err != nil {
		return nil, err
	}

	data, err := this.doRequest(ctx, http.MethodPost, signInPath, nil, body, "application/json", true)
	if err != nil {
		// 平台要求 2FA 时返回 code=400 且 data.require2FA=true，如实透出
		var apiErr *APIError
		if errors.As(err, &apiErr) && len(apiErr.Data) > 0 {
			var extra struct {
				Require2FA bool `json:"require2FA"`
			}
			if json.Unmarshal(apiErr.Data, &extra) == nil && extra.Require2FA {
				apiErr.Require2FA = true
			}
		}
		return nil, err
	}

	var result SignInResult
	if err = decodeData(data, &result); err != nil {
		return nil, err
	}
	if result.Token.Value == "" {
		return nil, errors.New("登录响应缺少 token.value")
	}
	this.SetToken(result.Token)
	return &result, nil
}

// canLogin - 是否具备登录凭据
func (this *AdminClient) canLogin() bool {
	return this.options.Account != "" && this.options.Password != ""
}

// needLogin - 是否需要（重新）登录：无令牌或令牌已过有效期（Expired 为毫秒时间戳）
func (this *AdminClient) needLogin() bool {

	this.mu.RLock()
	defer this.mu.RUnlock()
	if this.token.Value == "" {
		return true
	}
	return this.token.Expired > 0 && time.Now().UnixMilli() >= this.token.Expired
}

// ensureLogin - 请求前确保登录态可用（双重检查，避免并发重复登录）
func (this *AdminClient) ensureLogin(ctx context.Context) error {

	if !this.needLogin() || !this.canLogin() {
		return nil
	}
	this.loginMu.Lock()
	defer this.loginMu.Unlock()
	if !this.needLogin() {
		return nil
	}
	_, err := this.loginLocked(ctx)
	return err
}

// ============================= 请求出口 =============================

// doRequest - 统一请求出口：自动登录 → 携带 Bearer token → 解包 {code,msg,data} 信封；
// 业务 401（登录失效）时自动重登并原样重试一次。
func (this *AdminClient) doRequest(ctx context.Context, method, path string, query url.Values, body []byte, contentType string, retried bool) (json.RawMessage, error) {

	// 登录接口自身不携带 token，也不参与自动登录
	if path != signInPath {
		if err := this.ensureLogin(ctx); err != nil {
			return nil, err
		}
	}

	data, err := this.round(ctx, method, path, query, body, contentType)
	if err == nil {
		return data, nil
	}

	// 登录失效：重登后原样重试一次（仅一次，防止循环）
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusUnauthorized && !retried &&
		path != signInPath && this.canLogin() {
		if _, err = this.Login(ctx); err != nil {
			return nil, err
		}
		return this.doRequest(ctx, method, path, query, body, contentType, true)
	}
	return nil, err
}

// round - 单轮 HTTP 请求：拼 URL、带 token、读响应、拆信封
func (this *AdminClient) round(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) (json.RawMessage, error) {

	addr := strings.TrimRight(this.options.ServerURL, "/") + path
	if len(query) > 0 {
		addr += "?" + query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, method, addr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	// 登录接口为公开接口，不携带 token
	if token := this.Token().Value; token != "" && path != signInPath {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := this.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("licence: 请求发送失败: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("licence: 响应读取失败: %w", err)
	}
	// 平台业务响应恒为 200；非 200 属传输层异常
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode, Body: string(raw)}
	}

	var envelope Response
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("licence: 响应信封解析失败（原文：%s）: %w", string(raw), err)
	}
	if envelope.Code != 200 {
		return nil, &APIError{Code: envelope.Code, Msg: envelope.Msg, Data: envelope.Data}
	}
	return envelope.Data, nil
}

// get - GET 请求（参数走 query；数组参数按平台约定序列化为 key[]=v1&key[]=v2）
func (this *AdminClient) get(ctx context.Context, path string, params any, out any) error {

	data, err := this.doRequest(ctx, http.MethodGet, path, toQuery(params), nil, "", false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}

// post - POST 请求（参数走 JSON body）
func (this *AdminClient) post(ctx context.Context, path string, params any, out any) error {

	body, err := marshalBody(params)
	if err != nil {
		return err
	}
	data, err := this.doRequest(ctx, http.MethodPost, path, nil, body, "application/json", false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}

// put - PUT 请求（参数走 JSON body）
func (this *AdminClient) put(ctx context.Context, path string, params any, out any) error {

	body, err := marshalBody(params)
	if err != nil {
		return err
	}
	data, err := this.doRequest(ctx, http.MethodPut, path, nil, body, "application/json", false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}

// del - DELETE 请求（参数走 JSON body，平台参数中间件对 DELETE 同样解析 body）
func (this *AdminClient) del(ctx context.Context, path string, params any, out any) error {

	body, err := marshalBody(params)
	if err != nil {
		return err
	}
	data, err := this.doRequest(ctx, http.MethodDelete, path, nil, body, "application/json", false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}

// postMultipart - multipart/form-data POST（发布物上传/带文件验签）：
// 文本字段写入表单，文件写入 fileField 指定的文件字段（平台固定为 "file"）
func (this *AdminClient) postMultipart(ctx context.Context, path string, fields map[string]string, fileField string, fileName string, content io.Reader, out any) error {

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	part, err := writer.CreateFormFile(fileField, filepath.Base(fileName))
	if err != nil {
		return err
	}
	if _, err = io.Copy(part, content); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}

	data, err := this.doRequest(ctx, http.MethodPost, path, nil, buf.Bytes(), writer.FormDataContentType(), false)
	if err != nil {
		return err
	}
	return decodeData(data, out)
}

// marshalBody - JSON body 序列化（nil 参数序列化为空 body）
func marshalBody(params any) ([]byte, error) {

	if params == nil {
		return nil, nil
	}
	return json.Marshal(params)
}

// toQuery - 查询参数转 url.Values：标量直接写入，切片按平台约定序列化为 key[]=v（可重复）
func toQuery(params any) url.Values {

	values := url.Values{}
	if params == nil {
		return values
	}

	raw, err := json.Marshal(params)
	if err != nil {
		return values
	}
	item := map[string]any{}
	if err = json.Unmarshal(raw, &item); err != nil {
		return values
	}
	for key, value := range item {
		if list, ok := value.([]any); ok {
			for _, one := range list {
				values.Add(key+"[]", cast.ToString(one))
			}
			continue
		}
		values.Set(key, cast.ToString(value))
	}
	return values
}
