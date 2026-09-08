package licence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
