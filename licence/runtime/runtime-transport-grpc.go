package runtime

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// grpcRuntimeTransport - gRPC 运行面传输。
type grpcRuntimeTransport struct {
	client         *Client
	conn           *grpc.ClientConn
	license        licencev1.LicenseRuntimeServiceClient
	update         licencev1.UpdateRuntimeServiceClient
	saas           licencev1.SaasRuntimeServiceClient
	platformConfig licencev1.PlatformConfigRuntimeServiceClient
	event          licencev1.EventRuntimeServiceClient
	pushback       licencev1.ConfigPushbackRuntimeServiceClient
	closeOnce      sync.Once
	closeErr       error
}

func newGRPCRuntimeTransport(client *Client) (*grpcRuntimeTransport, error) {
	conn, err := NewGRPCConn(client.options.ServerURL, client.options.GRPC, client.options.HTTPTimeout)
	if err != nil {
		return nil, err
	}
	return &grpcRuntimeTransport{
		client: client, conn: conn,
		license:        licencev1.NewLicenseRuntimeServiceClient(conn),
		update:         licencev1.NewUpdateRuntimeServiceClient(conn),
		saas:           licencev1.NewSaasRuntimeServiceClient(conn),
		platformConfig: licencev1.NewPlatformConfigRuntimeServiceClient(conn),
		event:          licencev1.NewEventRuntimeServiceClient(conn),
		pushback:       licencev1.NewConfigPushbackRuntimeServiceClient(conn),
	}, nil
}

// NewGRPCConn 统一运行面与管理面的安全拨号默认值（管理面 admin 子包经本函数建连）。
func NewGRPCConn(serverURL string, options GRPCOptions, fallbackTimeout time.Duration) (*grpc.ClientConn, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("ServerURL 不是有效的平台 URI")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, errors.New("gRPC ServerURL 不支持路径前缀")
	}
	target := parsed.Host
	dialOptions := make([]grpc.DialOption, 0, 4)
	switch strings.ToLower(parsed.Scheme) {
	case "https", "grpcs":
		config := options.TLSConfig
		if config == nil {
			config = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()}
		} else {
			config = config.Clone()
			if config.ServerName == "" {
				config.ServerName = parsed.Hostname()
			}
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(credentials.NewTLS(config)))
	case "http", "grpc":
		if !options.AllowInsecure {
			return nil, errors.New("明文 gRPC 仅在 GRPC.AllowInsecure=true 时允许")
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	default:
		return nil, errors.New("gRPC ServerURL 仅支持 http/https/grpc/grpcs")
	}
	if options.Authority != "" {
		dialOptions = append(dialOptions, grpc.WithAuthority(options.Authority))
	}
	maxSize := options.MaxReceiveMessageSize
	if maxSize <= 0 {
		maxSize = 16 << 20
	}
	dialOptions = append(dialOptions, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxSize)))
	_ = fallbackTimeout // grpc.NewClient 惰性建连，单次 RPC 的 deadline 由调用层控制。
	return grpc.NewClient(target, dialOptions...)
}

func (this *grpcRuntimeTransport) signedContext(ctx context.Context, fullMethod string, request proto.Message) (context.Context, error) {
	this.client.mu.RLock()
	token := this.client.state.ActivationToken
	seedHex := this.client.state.ClientSeed
	this.client.mu.RUnlock()
	if token == "" || seedHex == "" {
		return nil, errors.New("凭证缺失，请先激活")
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(this.client.now(), 10)
	nonce := LicenceProtocol.Licence.Nonce()
	content, err := LicenceProtocol.GRPCContent(fullMethod, timestamp, nonce, request)
	if err != nil {
		return nil, err
	}
	signature, err := LicenceProtocol.SignPayload(content, seed)
	if err != nil {
		return nil, err
	}
	return metadata.AppendToOutgoingContext(ctx,
		LicenceProtocol.MetadataToken, token,
		LicenceProtocol.MetadataTimestamp, timestamp,
		LicenceProtocol.MetadataNonce, nonce,
		LicenceProtocol.MetadataSignature, signature,
		LicenceProtocol.MetadataSignVersion, LicenceProtocol.SignVersionV1,
	), nil
}

func (this *grpcRuntimeTransport) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := this.client.options.GRPC.DialTimeout
	if timeout <= 0 {
		timeout = this.client.options.HTTPTimeout
	}
	if _, exists := ctx.Deadline(); exists || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func grpcHTTPCode(err error) (int, error) {
	if status.Code(err) == codes.NotFound {
		return http.StatusNotFound, nil
	}
	return 0, err
}

func marshalMap(value map[string]any) (int, []byte, error) {
	raw, err := json.Marshal(value)
	return http.StatusOK, raw, err
}

func provisionMap(response *licencev1.ProvisionResponse) map[string]any {
	result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
	if response.GetLicenseNo() != "" {
		result["licenseNo"] = response.GetLicenseNo()
	}
	if response.GetSalt() != "" {
		result["salt"] = response.GetSalt()
	}
	if response.GetBindingPolicy() != "" {
		result["bindingPolicy"] = response.GetBindingPolicy()
	}
	if response.GetSeatLimit() > 0 {
		result["seatLimit"] = response.GetSeatLimit()
	}
	if response.GetExpiresAt() > 0 {
		result["expiresAt"] = response.GetExpiresAt()
	}
	if response.GetReissued() {
		result["reissued"] = true
	}
	if response.GetMessage() != "" {
		result["message"] = response.GetMessage()
	}
	return result
}

func runtimeMap(response *licencev1.RuntimeResponse) map[string]any {
	result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
	if len(response.GetEnvelopeJson()) > 0 {
		result["envelope"] = json.RawMessage(response.GetEnvelopeJson())
	}
	if response.GetActivationNo() != "" {
		result["activationNo"] = response.GetActivationNo()
	}
	if response.GetActivationToken() != "" {
		result["activationToken"] = response.GetActivationToken()
	}
	if response.GetSeatNo() != "" {
		result["seatNo"] = response.GetSeatNo()
	}
	if response.GetExpiresAt() > 0 {
		result["expiresAt"] = response.GetExpiresAt()
	}
	if response.GetMessage() != "" {
		result["message"] = response.GetMessage()
	}
	return result
}

// SubscribeEvents - 事件订阅 gRPC 服务端流实现：
// 带独立 deadline（hold + 10s，下限默认调用超时）打开流，Recv 到 EOF 收集成 slice。
func (this *grpcRuntimeTransport) SubscribeEvents(ctx context.Context, licenseNo string, sinceEventId int64, hold time.Duration) (subscribeResult, error) {
	timeout := this.client.options.HTTPTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if hold <= 0 {
		hold = 15 * time.Second
	}
	// 服务端对 timeout_ms 有 30000ms 硬上限，把 hold 收敛到 30s（deadline=hold+10s 随之收敛）
	if hold > 30*time.Second {
		hold = 30 * time.Second
	}
	deadline := hold + 10*time.Second
	if deadline < timeout {
		deadline = timeout
	}
	streamCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	request := &licencev1.EventSubscribeRequest{
		LicenseNo: licenseNo, SinceEventId: sinceEventId, TimeoutMs: int32(hold.Milliseconds()),
	}
	signed, err := this.signedContext(streamCtx, licencev1.EventRuntimeService_Subscribe_FullMethodName, request)
	if err != nil {
		return subscribeResult{}, err
	}
	stream, err := this.event.Subscribe(signed, request)
	if err != nil {
		return subscribeResult{}, this.mapSubscribeError(err)
	}
	// 服务端流正常结束即放行态（非放行态服务端直接报错，不会进流）
	result := subscribeResult{Status: LicenceProtocol.StatusValid}
	for {
		message, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return subscribeResult{}, this.mapSubscribeError(recvErr)
		}
		result.Events = append(result.Events, SubscribedEvent{EventId: message.GetEventId(), Envelope: message.GetEnvelopeJson()})
	}
	return result, nil
}

// mapSubscribeError - 订阅流错误映射到业务错误文本（与 HTTP 侧归一，保证公共方法跨协议一致）。
func (this *grpcRuntimeTransport) mapSubscribeError(err error) error {
	grpcStatus, _ := status.FromError(err)
	switch grpcStatus.Code() {
	case codes.NotFound:
		return errors.New("许可证或项目信息无效")
	case codes.FailedPrecondition:
		// 服务端消息已带"许可证非放行态：{status}"，直接透传保持双协议一致
		return errors.New(grpcStatus.Message())
	case codes.Internal:
		return errors.New("服务端故障，请稍后重试")
	default:
		return err
	}
}

func (this *grpcRuntimeTransport) invokeContext(ctx context.Context, fullMethod string, request proto.Message, withSign bool) (context.Context, context.CancelFunc, error) {
	callCtx, cancel := this.callContext(ctx)
	if !withSign {
		return callCtx, cancel, nil
	}
	signed, err := this.signedContext(callCtx, fullMethod, request)
	if err != nil {
		cancel()
		return nil, func() {}, err
	}
	return signed, cancel, nil
}

func (this *grpcRuntimeTransport) RoundTrip(ctx context.Context, method, requestURI string, body []byte, withSign bool) (int, []byte, error) {
	path := requestURI
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	switch method + " " + path {
	case http.MethodPost + " /api/v1/licenses/activate":
		var input activateBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.ActivateRequest{
			LicenseNo: input.LicenseNo, InstanceNo: input.InstanceNo, FingerprintHash: input.FingerprintHash,
			ClientPublicKey: input.ClientPublicKey, ClientTime: input.ClientTime, DeviceName: input.DeviceName,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.LicenseRuntimeService_Activate_FullMethodName, request, false)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.license.Activate(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(runtimeMap(response))

	case http.MethodPost + " /api/v1/licenses/validate":
		var input validateBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.ValidateRequest{
			LicenseNo: input.LicenseNo, FingerprintHash: input.FingerprintHash, Version: input.Version,
			Feature: input.Feature, Usage: input.Usage, ClientTime: input.ClientTime,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.LicenseRuntimeService_Validate_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.license.Validate(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(runtimeMap(response))

	case http.MethodGet + " /api/v1/licenses/current":
		parsed, err := url.ParseRequestURI(requestURI)
		if err != nil {
			return 0, nil, err
		}
		request := &licencev1.CurrentLicenseRequest{LicenseNo: parsed.Query().Get("licenseNo")}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.LicenseRuntimeService_Current_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.license.Current(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(runtimeMap(response))

	case http.MethodPost + " /api/v1/licenses/provision":
		var input provisionBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.ProvisionRequest{
			TemplateCode: input.TemplateCode, ProvisionToken: input.ProvisionToken,
			InstallSn: input.InstallSN, FingerprintHash: input.FingerprintHash,
			DeviceName: input.DeviceName, ClientTime: input.ClientTime,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.LicenseRuntimeService_Provision_FullMethodName, request, false)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.license.Provision(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(provisionMap(response))

	case http.MethodPost + " /api/v1/licenses/redeem":
		var input redeemBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.RedeemRequest{
			Code: input.Code, InstallSn: input.InstallSN,
			FingerprintHash: input.FingerprintHash, DeviceName: input.DeviceName,
			ClientTime: input.ClientTime,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.LicenseRuntimeService_Redeem_FullMethodName, request, false)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.license.Redeem(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(provisionMap(response))

	case http.MethodPost + " /api/v1/platform/configs/consume":
		var input platformConfigConsumeBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		items := make([]*licencev1.ConfigConsumptionItem, 0, len(input.Items))
		for _, item := range input.Items {
			items = append(items, &licencev1.ConfigConsumptionItem{Key: item.Key, Count: int32(item.Count)})
		}
		request := &licencev1.ConfigConsumptionReportRequest{LicenseNo: input.LicenseNo, Items: items}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.PlatformConfigRuntimeService_ReportConfigConsumption_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.platformConfig.ReportConfigConsumption(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
		if response.GetMessage() != "" {
			result["message"] = response.GetMessage()
		}
		return marshalMap(result)
	}
	return this.roundTripExtended(ctx, method, path, requestURI, body, withSign)
}

func (this *grpcRuntimeTransport) Close() error {
	this.closeOnce.Do(func() { this.closeErr = this.conn.Close() })
	return this.closeErr
}

var _ runtimeTransport = (*grpcRuntimeTransport)(nil)

// ============================= gRPC 扩展传输（配置/租户/菜单/回推） =============================

type tenantSyncBody struct {
	LicenseNo string `json:"licenseNo"`
	SinceTime int64  `json:"sinceTime"`
}

type tenantSearchBody struct {
	LicenseNo string `json:"licenseNo"`
	Prefix    string `json:"prefix"`
}

type tenantValidateBody struct {
	LicenseNo  string           `json:"licenseNo"`
	TenantCode string           `json:"tenantCode"`
	Version    string           `json:"version"`
	Feature    string           `json:"feature"`
	Usage      map[string]int64 `json:"usage"`
}

type updateReportBody struct {
	LicenseNo     string `json:"licenseNo"`
	RecordNo      string `json:"recordNo"`
	FromVersion   string `json:"fromVersion"`
	TargetVersion string `json:"targetVersion"`
	ArtifactNo    string `json:"artifactNo"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	ClientTime    int64  `json:"clientTime"`
}

type updateLogsBody struct {
	LicenseNo string   `json:"licenseNo"`
	RecordNo  string   `json:"recordNo"`
	Lines     []string `json:"lines"`
}

func (this *grpcRuntimeTransport) roundTripExtended(ctx context.Context, method, path, requestURI string, body []byte, withSign bool) (int, []byte, error) {
	switch method + " " + path {
	case http.MethodPost + " /api/v1/platform/configs/sync":
		var input platformConfigSyncBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.PlatformConfigSyncRequest{
			LicenseNo: input.LicenseNo, ProjectId: int32(input.ProjectId), SinceVersion: int32(input.SinceVersion),
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.PlatformConfigRuntimeService_Sync_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.platformConfig.Sync(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
		if len(response.GetEnvelopeJson()) > 0 {
			result["envelope"] = json.RawMessage(response.GetEnvelopeJson())
		}
		if response.GetMessage() != "" {
			result["message"] = response.GetMessage()
		}
		return marshalMap(result)

	case http.MethodPost + " /api/v1/updates/check":
		var input checkUpdateBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.UpdateCheckRequest{
			LicenseNo: input.LicenseNo, FingerprintHash: input.FingerprintHash, OsArch: input.OsArch,
			Version: input.Version, ClientTime: input.ClientTime,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.UpdateRuntimeService_Check_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.update.Check(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		result := map[string]any{
			"status": response.GetStatus(), "serverTime": response.GetServerTime(), "update": response.GetUpdate(),
		}
		if len(response.GetManifestJson()) > 0 {
			result["manifest"] = json.RawMessage(response.GetManifestJson())
		}
		if response.GetMessage() != "" {
			result["message"] = response.GetMessage()
		}
		return marshalMap(result)

	case http.MethodPost + " /api/v1/updates/report":
		var input updateReportBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.UpdateReportRequest{
			LicenseNo: input.LicenseNo, RecordNo: input.RecordNo, FromVersion: input.FromVersion,
			TargetVersion: input.TargetVersion, ArtifactNo: input.ArtifactNo, Status: input.Status,
			Message: input.Message, ClientTime: input.ClientTime,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.UpdateRuntimeService_Report_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.update.Report(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(updateReportMap(response))

	case http.MethodPost + " /api/v1/updates/logs":
		var input updateLogsBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.UpdateLogsRequest{LicenseNo: input.LicenseNo, RecordNo: input.RecordNo, Lines: input.Lines}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.UpdateRuntimeService_AppendLogs_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.update.AppendLogs(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(updateReportMap(response))

	case http.MethodPost + " /api/v1/saas/tenants/sync":
		var input tenantSyncBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.TenantSyncRequest{LicenseNo: input.LicenseNo, SinceTime: input.SinceTime}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.SaasRuntimeService_Sync_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.saas.Sync(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		result := map[string]any{
			"status": response.GetStatus(), "serverTime": response.GetServerTime(), "syncTime": response.GetSyncTime(),
		}
		manifests := map[string]any{"platform": nil, "tenant": nil}
		if response.GetPlatformManifest() != nil {
			manifests["platform"] = map[string]any{
				"version": response.GetPlatformManifest().GetVersion(), "menus": json.RawMessage(response.GetPlatformManifest().GetMenusJson()),
			}
		}
		if response.GetTenantManifest() != nil {
			manifests["tenant"] = map[string]any{
				"version": response.GetTenantManifest().GetVersion(), "menus": json.RawMessage(response.GetTenantManifest().GetMenusJson()),
			}
		}
		result["manifests"] = manifests
		items := make([]map[string]any, 0, len(response.GetTenants()))
		for _, item := range response.GetTenants() {
			row := map[string]any{"tenantCode": item.GetTenantCode(), "status": item.GetStatus()}
			if len(item.GetEnvelopeJson()) > 0 {
				row["envelope"] = json.RawMessage(item.GetEnvelopeJson())
			}
			items = append(items, row)
		}
		result["tenants"] = items
		if response.GetMessage() != "" {
			result["message"] = response.GetMessage()
		}
		return marshalMap(result)

	case http.MethodPost + " /api/v1/saas/tenants/search":
		var input tenantSearchBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.TenantSearchRequest{LicenseNo: input.LicenseNo, Prefix: input.Prefix}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.SaasRuntimeService_Search_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.saas.Search(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		items := make([]map[string]any, 0, len(response.GetTenants()))
		for _, item := range response.GetTenants() {
			items = append(items, map[string]any{"tenantCode": item.GetTenantCode(), "tenantName": item.GetTenantName()})
		}
		result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime(), "tenants": items}
		if response.GetMessage() != "" {
			result["message"] = response.GetMessage()
		}
		return marshalMap(result)

	case http.MethodPost + " /api/v1/saas/tenants/validate":
		var input tenantValidateBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.TenantValidateRequest{
			LicenseNo: input.LicenseNo, TenantCode: input.TenantCode, Version: input.Version,
			Feature: input.Feature, Usage: input.Usage,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.SaasRuntimeService_Validate_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.saas.Validate(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(tenantResponseMap(response))

	case http.MethodGet + " /api/v1/saas/tenants/current":
		parsed, err := url.ParseRequestURI(requestURI)
		if err != nil {
			return 0, nil, err
		}
		request := &licencev1.TenantCurrentRequest{
			LicenseNo: parsed.Query().Get("licenseNo"), TenantCode: parsed.Query().Get("tenantCode"),
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.SaasRuntimeService_Current_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.saas.Current(callCtx, request)
		if err != nil {
			code, mapped := grpcHTTPCode(err)
			return code, nil, mapped
		}
		return marshalMap(tenantResponseMap(response))
	case http.MethodPost + " /api/v1/pushback/configs":
		var input pushbackBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		request := &licencev1.ConfigPushbackPushRequest{
			TenantId: input.TenantID, Items: input.Items, ClientPushId: input.ClientPushID,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.ConfigPushbackRuntimeService_Push_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.pushback.Push(callCtx, request)
		if err != nil {
			if code, mapped := grpcHTTPCode(err); mapped == nil {
				return code, nil, mapped // NotFound → 404 模糊语义
			}
			// 校验失败/签名闸门错误映射为对应 HTTP 状态码 + message，与 HTTP 侧错误分层一致
			if grpcStatus, ok := status.FromError(err); ok {
				switch grpcStatus.Code() {
				case codes.InvalidArgument:
					raw, marshalErr := json.Marshal(map[string]any{"message": grpcStatus.Message()})
					if marshalErr != nil {
						return 0, nil, marshalErr
					}
					return http.StatusBadRequest, raw, nil
				case codes.Unauthenticated:
					return http.StatusUnauthorized, nil, nil
				}
			}
			return 0, nil, err
		}
		return marshalMap(map[string]any{
			"batch_id": response.GetBatchId(), "created": response.GetCreated(), "updated": response.GetUpdated(),
			"deleted": response.GetDeleted(), "unchanged": response.GetUnchanged(), "pushed_at": response.GetPushedAt(),
		})
	case http.MethodPost + " /api/v1/pushback/config-definitions":
		var input pushbackDefinitionsBody
		if err := json.Unmarshal(body, &input); err != nil {
			return 0, nil, err
		}
		groups := make([]*licencev1.ConfigDefinitionGroup, 0, len(input.Groups))
		for _, group := range input.Groups {
			groups = append(groups, &licencev1.ConfigDefinitionGroup{
				Name: group.Name, Label: group.Label, LabelEn: group.LabelEn,
				Icon: group.Icon, Sort: int32(group.Sort), Parent: group.Parent,
			})
		}
		configs := make([]*licencev1.ConfigDefinitionItem, 0, len(input.Configs))
		for _, item := range input.Configs {
			configs = append(configs, &licencev1.ConfigDefinitionItem{
				Key: item.Key, Label: item.Label, Type: item.Type, GroupPath: item.GroupPath,
				Options: rawJSONText(item.Options), Rules: rawJSONText(item.Rules),
				Placeholder: item.Placeholder, Remark: item.Remark, DefaultValue: item.DefaultValue,
				Sensitive: item.Sensitive, Sort: int32(item.Sort),
			})
		}
		request := &licencev1.ConfigPushbackDefinitionsRequest{
			Groups: groups, Configs: configs, ClientPushId: input.ClientPushID,
		}
		callCtx, cancel, err := this.invokeContext(ctx, licencev1.ConfigPushbackRuntimeService_PushDefinitions_FullMethodName, request, withSign)
		if err != nil {
			return 0, nil, err
		}
		defer cancel()
		response, err := this.pushback.PushDefinitions(callCtx, request)
		if err != nil {
			if code, mapped := grpcHTTPCode(err); mapped == nil {
				return code, nil, mapped // NotFound → 404 模糊语义
			}
			// 开关闸门/校验失败/签名错误映射为对应 HTTP 状态码 + message，与 HTTP 侧错误分层一致；
			// gRPC status 只能携带汇总 message，逐条 errors 明细以 HTTP 响应为准
			if grpcStatus, ok := status.FromError(err); ok {
				switch grpcStatus.Code() {
				case codes.InvalidArgument:
					raw, marshalErr := json.Marshal(map[string]any{"message": grpcStatus.Message()})
					if marshalErr != nil {
						return 0, nil, marshalErr
					}
					return http.StatusBadRequest, raw, nil
				case codes.PermissionDenied:
					raw, marshalErr := json.Marshal(map[string]any{"message": grpcStatus.Message()})
					if marshalErr != nil {
						return 0, nil, marshalErr
					}
					return http.StatusForbidden, raw, nil
				case codes.Unauthenticated:
					return http.StatusUnauthorized, nil, nil
				}
			}
			return 0, nil, err
		}
		return marshalMap(map[string]any{
			"batch_id":  response.GetBatchId(),
			"groups":    definitionDiffStatsMap(response.GetGroups()),
			"configs":   definitionDiffStatsMap(response.GetConfigs()),
			"pushed_at": response.GetPushedAt(),
		})
	}
	return 0, nil, errorsNewUnsupported(method, requestURI)
}

func updateReportMap(response *licencev1.UpdateReportResponse) map[string]any {
	result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
	if response.GetRecordNo() != "" {
		result["recordNo"] = response.GetRecordNo()
	}
	if response.GetMessage() != "" {
		result["message"] = response.GetMessage()
	}
	return result
}

func tenantResponseMap(response *licencev1.TenantResponse) map[string]any {
	result := map[string]any{"status": response.GetStatus(), "serverTime": response.GetServerTime()}
	if len(response.GetEnvelopeJson()) > 0 {
		result["envelope"] = json.RawMessage(response.GetEnvelopeJson())
	}
	if response.GetMessage() != "" {
		result["message"] = response.GetMessage()
	}
	return result
}

// definitionDiffStatsMap - proto diff 计数 → map（gRPC 分支回填与 HTTP 同构的响应体）
func definitionDiffStatsMap(stats *licencev1.ConfigPushbackDiffStats) map[string]any {

	if stats == nil {
		return map[string]any{"created": 0, "updated": 0, "deleted": 0, "unchanged": 0}
	}
	return map[string]any{
		"created": stats.GetCreated(), "updated": stats.GetUpdated(),
		"deleted": stats.GetDeleted(), "unchanged": stats.GetUnchanged(),
	}
}

func errorsNewUnsupported(method, requestURI string) error {
	return &unsupportedRuntimeRouteError{method: method, requestURI: requestURI}
}

type unsupportedRuntimeRouteError struct {
	method     string
	requestURI string
}

func (this *unsupportedRuntimeRouteError) Error() string {
	return "gRPC 运行面未映射请求：" + this.method + " " + this.requestURI
}
