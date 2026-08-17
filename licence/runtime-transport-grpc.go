package licence

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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
	closeOnce      sync.Once
	closeErr       error
}

func newGRPCRuntimeTransport(client *Client) (*grpcRuntimeTransport, error) {
	conn, err := newGRPCConn(client.options.ServerURL, client.options.GRPC, client.options.HTTPTimeout)
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
	}, nil
}

// newGRPCConn 统一运行面与管理面的安全拨号默认值。
func newGRPCConn(serverURL string, options GRPCOptions, fallbackTimeout time.Duration) (*grpc.ClientConn, error) {
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
	nonce := Licence.Nonce()
	content, err := LicenceProtocol.GRPCContent(fullMethod, timestamp, nonce, request)
	if err != nil {
		return nil, err
	}
	signature, err := signPayload(content, seed)
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
	result := subscribeResult{Status: StatusValid}
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
