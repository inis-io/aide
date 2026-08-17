package licence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grpcAdminTransport struct {
	client        *AdminClient
	conn          *grpc.ClientConn
	auth          licencev1.AdminAuthServiceClient
	qualification licencev1.QualificationAdminServiceClient
	project       licencev1.ProjectAdminServiceClient
	instance      licencev1.InstanceAdminServiceClient
	license       licencev1.LicenseAdminServiceClient
	signingKey    licencev1.SigningKeyAdminServiceClient
	artifact      licencev1.ArtifactAdminServiceClient
	version       licencev1.VersionAdminServiceClient
	module        licencev1.ProjectModuleAdminServiceClient
	saasMenu      licencev1.SaasMenuAdminServiceClient
	saasFeature   licencev1.SaasFeatureAdminServiceClient
	saasPlan      licencev1.SaasPlanAdminServiceClient
	saasTenant    licencev1.SaasTenantAdminServiceClient
	saasReview    licencev1.SaasReviewAdminServiceClient
	closeOnce     sync.Once
	closeErr      error
}

func newGRPCAdminTransport(client *AdminClient) (*grpcAdminTransport, error) {
	conn, err := newGRPCConn(client.options.ServerURL, client.options.GRPC, client.options.HTTPTimeout)
	if err != nil {
		return nil, adminGRPCError(err)
	}
	return &grpcAdminTransport{
		client: client, conn: conn,
		auth:          licencev1.NewAdminAuthServiceClient(conn),
		qualification: licencev1.NewQualificationAdminServiceClient(conn),
		project:       licencev1.NewProjectAdminServiceClient(conn),
		instance:      licencev1.NewInstanceAdminServiceClient(conn),
		license:       licencev1.NewLicenseAdminServiceClient(conn),
		signingKey:    licencev1.NewSigningKeyAdminServiceClient(conn),
		artifact:      licencev1.NewArtifactAdminServiceClient(conn),
		version:       licencev1.NewVersionAdminServiceClient(conn),
		module:        licencev1.NewProjectModuleAdminServiceClient(conn),
		saasMenu:      licencev1.NewSaasMenuAdminServiceClient(conn),
		saasFeature:   licencev1.NewSaasFeatureAdminServiceClient(conn),
		saasPlan:      licencev1.NewSaasPlanAdminServiceClient(conn),
		saasTenant:    licencev1.NewSaasTenantAdminServiceClient(conn),
		saasReview:    licencev1.NewSaasReviewAdminServiceClient(conn),
	}, nil
}

func (this *grpcAdminTransport) callContext(ctx context.Context, token string) (context.Context, context.CancelFunc) {
	timeout := this.client.options.GRPC.DialTimeout
	if timeout <= 0 {
		timeout = this.client.options.HTTPTimeout
	}
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	if _, exists := ctx.Deadline(); exists || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func queryJSON(call adminCall) ([]byte, error) {
	if len(call.Body) > 0 {
		return call.Body, nil
	}
	values := map[string]any{}
	for rawKey, items := range call.Query {
		key := strings.TrimSuffix(rawKey, "[]")
		converted := make([]any, 0, len(items))
		for _, item := range items {
			converted = append(converted, queryScalar(item))
		}
		if strings.HasSuffix(rawKey, "[]") || len(converted) > 1 {
			values[key] = converted
		} else if len(converted) == 1 {
			values[key] = converted[0]
		}
	}
	return json.Marshal(values)
}

func queryScalar(value string) any {
	if value == "true" || value == "false" {
		parsed, _ := strconv.ParseBool(value)
		return parsed
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && strings.Contains(value, ".") {
		return parsed
	}
	return value
}

func adminData(response *licencev1.AdminResponse) (json.RawMessage, error) {
	if response == nil {
		return nil, errors.New("licence: gRPC 管理面响应为空")
	}
	if response.GetCode() != http.StatusOK {
		return nil, &APIError{Code: int(response.GetCode()), Msg: response.GetMessage(), Data: response.GetDataJson()}
	}
	return json.RawMessage(response.GetDataJson()), nil
}

func adminGRPCError(err error) error {
	if err == nil {
		return nil
	}
	code := http.StatusInternalServerError
	switch status.Code(err) {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.Unauthenticated:
		code = http.StatusUnauthorized
	case codes.PermissionDenied:
		code = http.StatusForbidden
	case codes.NotFound:
		code = http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted, codes.FailedPrecondition:
		code = http.StatusConflict
	case codes.DeadlineExceeded:
		code = http.StatusGatewayTimeout
	case codes.Unavailable:
		code = http.StatusServiceUnavailable
	}
	return &APIError{Code: code, Msg: status.Convert(err).Message(), Cause: err}
}

func (this *grpcAdminTransport) RoundTrip(ctx context.Context, call adminCall) (json.RawMessage, error) {
	payload, err := queryJSON(call)
	if err != nil {
		return nil, adminGRPCError(err)
	}
	request := &licencev1.AdminRequest{Json: payload}
	callCtx, cancel := this.callContext(ctx, call.Token)
	defer cancel()

	var response *licencev1.AdminResponse
	switch call.Method + " " + call.Path {
	case http.MethodPost + " /api/comm/sign-in":
		response, err = this.auth.SignIn(callCtx, request)
	case http.MethodDelete + " /api/comm/sign-out":
		response, err = this.auth.SignOut(callCtx, request)
	case http.MethodPost + " /api/comm/check-token":
		response, err = this.auth.CheckToken(callCtx, request)

	case http.MethodPost + " /api/qualification/apply":
		response, err = this.qualification.ApplyQualification(callCtx, request)
	case http.MethodGet + " /api/qualification/current":
		response, err = this.qualification.GetCurrentQualification(callCtx, request)
	case http.MethodGet + " /api/qualification/mine":
		response, err = this.qualification.FindMyQualifications(callCtx, request)
	case http.MethodGet + " /api/qualification/rows":
		response, err = this.qualification.RowsQualifications(callCtx, request)
	case http.MethodGet + " /api/qualification/find":
		response, err = this.qualification.FindQualifications(callCtx, request)
	case http.MethodGet + " /api/qualification/take":
		response, err = this.qualification.GetQualification(callCtx, request)
	case http.MethodPost + " /api/qualification/review":
		response, err = this.qualification.ReviewQualification(callCtx, request)
	case http.MethodPost + " /api/qualification/revoke":
		response, err = this.qualification.RevokeQualification(callCtx, request)

	case http.MethodGet + " /api/projects/rows":
		response, err = this.project.RowsProjects(callCtx, request)
	case http.MethodGet + " /api/projects/find":
		response, err = this.project.FindProjects(callCtx, request)
	case http.MethodGet + " /api/projects/take":
		response, err = this.project.GetProject(callCtx, request)
	case http.MethodPost + " /api/projects/create":
		response, err = this.project.CreateProject(callCtx, request)
	case http.MethodPut + " /api/projects/update":
		response, err = this.project.UpdateProject(callCtx, request)
	case http.MethodDelete + " /api/projects/remove":
		response, err = this.project.RemoveProjects(callCtx, request)
	case http.MethodDelete + " /api/projects/delete":
		response, err = this.project.DeleteProjects(callCtx, request)
	case http.MethodPut + " /api/projects/restore":
		response, err = this.project.RestoreProjects(callCtx, request)

	case http.MethodGet + " /api/instances/rows":
		response, err = this.instance.RowsInstances(callCtx, request)
	case http.MethodGet + " /api/instances/find":
		response, err = this.instance.FindInstances(callCtx, request)
	case http.MethodGet + " /api/instances/take":
		response, err = this.instance.GetInstance(callCtx, request)
	case http.MethodPost + " /api/instances/create":
		response, err = this.instance.CreateInstance(callCtx, request)
	case http.MethodPut + " /api/instances/update":
		response, err = this.instance.UpdateInstance(callCtx, request)
	case http.MethodDelete + " /api/instances/remove":
		response, err = this.instance.RemoveInstances(callCtx, request)
	case http.MethodDelete + " /api/instances/delete":
		response, err = this.instance.DeleteInstances(callCtx, request)
	case http.MethodPut + " /api/instances/restore":
		response, err = this.instance.RestoreInstances(callCtx, request)

	case http.MethodGet + " /api/licenses/rows":
		response, err = this.license.RowsLicenses(callCtx, request)
	case http.MethodGet + " /api/licenses/find":
		response, err = this.license.FindLicenses(callCtx, request)
	case http.MethodGet + " /api/licenses/take":
		response, err = this.license.GetLicense(callCtx, request)
	case http.MethodGet + " /api/licenses/take-payload":
		response, err = this.license.GetLicensePayload(callCtx, request)
	case http.MethodGet + " /api/licenses/public-key":
		response, err = this.license.GetLicensePublicKey(callCtx, request)
	case http.MethodPost + " /api/licenses/apply":
		response, err = this.license.ApplyLicense(callCtx, request)
	case http.MethodPost + " /api/licenses/cancel":
		response, err = this.license.CancelLicenseApplication(callCtx, request)
	case http.MethodGet + " /api/licenses/applications/rows":
		response, err = this.license.RowsLicenseApplications(callCtx, request)
	case http.MethodGet + " /api/licenses/applications/take":
		response, err = this.license.GetLicenseApplication(callCtx, request)
	case http.MethodPost + " /api/licenses/review":
		response, err = this.license.ReviewLicenseApplication(callCtx, request)
	case http.MethodPost + " /api/licenses/renew":
		response, err = this.license.RenewLicense(callCtx, request)
	case http.MethodPost + " /api/licenses/suspend":
		response, err = this.license.SuspendLicense(callCtx, request)
	case http.MethodPost + " /api/licenses/revoke":
		response, err = this.license.RevokeLicense(callCtx, request)
	case http.MethodPost + " /api/licenses/reissue":
		response, err = this.license.ReissueLicense(callCtx, request)
	case http.MethodGet + " /api/licenses/history/rows":
		response, err = this.license.RowsLicenseHistory(callCtx, request)
	case http.MethodGet + " /api/licenses/history/take":
		response, err = this.license.GetLicenseHistory(callCtx, request)
	case http.MethodGet + " /api/licenses/activations/rows":
		response, err = this.license.RowsActivations(callCtx, request)
	case http.MethodGet + " /api/licenses/activations/take":
		response, err = this.license.GetActivation(callCtx, request)
	case http.MethodGet + " /api/licenses/seats/find":
		response, err = this.license.FindLicenseSeats(callCtx, request)
	case http.MethodGet + " /api/licenses/seats/take":
		response, err = this.license.GetLicenseSeat(callCtx, request)
	case http.MethodPost + " /api/licenses/seats/release":
		response, err = this.license.ReleaseLicenseSeat(callCtx, request)

	case http.MethodGet + " /api/signing-keys/public":
		response, err = this.signingKey.GetSigningPublicKey(callCtx, request)
	case http.MethodPost + " /api/signing-keys/rotate":
		response, err = this.signingKey.RotateSigningKey(callCtx, request)

	case http.MethodGet + " /api/project-artifacts/rows":
		response, err = this.artifact.RowsArtifacts(callCtx, request)
	case http.MethodGet + " /api/project-artifacts/find":
		response, err = this.artifact.FindArtifacts(callCtx, request)
	case http.MethodGet + " /api/project-artifacts/take":
		response, err = this.artifact.GetArtifact(callCtx, request)
	case http.MethodPut + " /api/project-artifacts/update":
		response, err = this.artifact.UpdateArtifact(callCtx, request)
	case http.MethodPost + " /api/project-artifacts/verify":
		response, err = this.artifact.VerifyArtifact(callCtx, request)
	case http.MethodDelete + " /api/project-artifacts/remove":
		response, err = this.artifact.RemoveArtifacts(callCtx, request)
	case http.MethodDelete + " /api/project-artifacts/delete":
		response, err = this.artifact.DeleteArtifacts(callCtx, request)

	case http.MethodGet + " /api/project-versions/rows":
		response, err = this.version.RowsVersions(callCtx, request)
	case http.MethodGet + " /api/project-versions/find":
		response, err = this.version.FindVersions(callCtx, request)
	case http.MethodGet + " /api/project-versions/take":
		response, err = this.version.GetVersion(callCtx, request)
	case http.MethodPost + " /api/project-versions/create":
		response, err = this.version.CreateVersion(callCtx, request)
	case http.MethodPut + " /api/project-versions/update":
		response, err = this.version.UpdateVersion(callCtx, request)
	case http.MethodPost + " /api/project-versions/release":
		response, err = this.version.ReleaseVersion(callCtx, request)
	case http.MethodPut + " /api/project-versions/archive":
		response, err = this.version.ArchiveVersion(callCtx, request)
	case http.MethodDelete + " /api/project-versions/remove":
		response, err = this.version.RemoveVersions(callCtx, request)
	case http.MethodDelete + " /api/project-versions/delete":
		response, err = this.version.DeleteVersions(callCtx, request)
	case http.MethodPut + " /api/project-versions/restore":
		response, err = this.version.RestoreVersions(callCtx, request)

	case http.MethodGet + " /api/project-upgrade-records/rows":
		response, err = this.version.RowsUpgradeRecords(callCtx, request)
	case http.MethodGet + " /api/project-upgrade-records/find":
		response, err = this.version.FindUpgradeRecords(callCtx, request)
	case http.MethodGet + " /api/project-upgrade-records/take":
		response, err = this.version.GetUpgradeRecord(callCtx, request)

	case http.MethodGet + " /api/project-modules/rows":
		response, err = this.module.RowsProjectModules(callCtx, request)
	case http.MethodGet + " /api/project-modules/find":
		response, err = this.module.FindProjectModules(callCtx, request)
	case http.MethodGet + " /api/project-modules/take":
		response, err = this.module.GetProjectModule(callCtx, request)
	case http.MethodPost + " /api/project-modules/create":
		response, err = this.module.CreateProjectModule(callCtx, request)
	case http.MethodPut + " /api/project-modules/update":
		response, err = this.module.UpdateProjectModule(callCtx, request)
	case http.MethodPut + " /api/project-modules/sort":
		response, err = this.module.SortProjectModules(callCtx, request)
	case http.MethodDelete + " /api/project-modules/remove":
		response, err = this.module.RemoveProjectModules(callCtx, request)
	case http.MethodDelete + " /api/project-modules/delete":
		response, err = this.module.DeleteProjectModules(callCtx, request)
	case http.MethodPut + " /api/project-modules/restore":
		response, err = this.module.RestoreProjectModules(callCtx, request)

	case http.MethodGet + " /api/saas-menus/find":
		response, err = this.saasMenu.FindSaasMenus(callCtx, request)
	case http.MethodGet + " /api/saas-menus/take":
		response, err = this.saasMenu.GetSaasMenu(callCtx, request)
	case http.MethodPost + " /api/saas-menus/save":
		response, err = this.saasMenu.SaveSaasMenu(callCtx, request)
	case http.MethodPost + " /api/saas-menus/publish":
		response, err = this.saasMenu.PublishSaasMenu(callCtx, request)
	case http.MethodPost + " /api/saas-menus/archive":
		response, err = this.saasMenu.ArchiveSaasMenu(callCtx, request)

	case http.MethodGet + " /api/saas-features/find":
		response, err = this.saasFeature.FindSaasFeatures(callCtx, request)
	case http.MethodGet + " /api/saas-features/take":
		response, err = this.saasFeature.GetSaasFeature(callCtx, request)
	case http.MethodPost + " /api/saas-features/save":
		response, err = this.saasFeature.SaveSaasFeature(callCtx, request)
	case http.MethodPost + " /api/saas-features/disable":
		response, err = this.saasFeature.DisableSaasFeature(callCtx, request)
	case http.MethodDelete + " /api/saas-features/delete":
		response, err = this.saasFeature.DeleteSaasFeature(callCtx, request)

	case http.MethodGet + " /api/saas-plans/find":
		response, err = this.saasPlan.FindSaasPlans(callCtx, request)
	case http.MethodGet + " /api/saas-plans/take":
		response, err = this.saasPlan.GetSaasPlan(callCtx, request)
	case http.MethodPost + " /api/saas-plans/create":
		response, err = this.saasPlan.CreateSaasPlan(callCtx, request)
	case http.MethodPost + " /api/saas-plans/update":
		response, err = this.saasPlan.UpdateSaasPlan(callCtx, request)
	case http.MethodPost + " /api/saas-plans/status":
		response, err = this.saasPlan.SetSaasPlanStatus(callCtx, request)
	case http.MethodDelete + " /api/saas-plans/delete":
		response, err = this.saasPlan.DeleteSaasPlan(callCtx, request)

	case http.MethodGet + " /api/saas-tenants/find":
		response, err = this.saasTenant.FindSaasTenants(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/take":
		response, err = this.saasTenant.GetSaasTenant(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/take-payload":
		response, err = this.saasTenant.GetSaasTenantPayload(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/subscribe":
		response, err = this.saasTenant.SubscribeSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/change":
		response, err = this.saasTenant.ChangeSaasTenantPlan(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/update-info":
		response, err = this.saasTenant.UpdateSaasTenantInfo(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/cancel":
		response, err = this.saasTenant.CancelSaasTenant(callCtx, request)
	case http.MethodDelete + " /api/saas-tenants/delete":
		response, err = this.saasTenant.DeleteSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/suspend":
		response, err = this.saasTenant.SuspendSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/resume":
		response, err = this.saasTenant.ResumeSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/revoke":
		response, err = this.saasTenant.RevokeSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/reissue":
		response, err = this.saasTenant.ReissueSaasTenant(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/sync-menus":
		response, err = this.saasTenant.SyncSaasTenantMenus(callCtx, request)
	case http.MethodPost + " /api/saas-tenants/batch-renew":
		response, err = this.saasTenant.BatchRenewSaasTenants(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/applications/find":
		response, err = this.saasTenant.FindSaasTenantApplications(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/applications/take":
		response, err = this.saasTenant.GetSaasTenantApplication(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/usage/find":
		response, err = this.saasTenant.FindSaasTenantUsage(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/usage/summary":
		response, err = this.saasTenant.GetSaasTenantUsageSummary(callCtx, request)
	case http.MethodGet + " /api/saas-tenants/history/export":
		response, err = this.saasTenant.ExportSaasTenantHistory(callCtx, request)

	case http.MethodGet + " /api/saas-review/find":
		response, err = this.saasReview.FindSaasReviews(callCtx, request)
	case http.MethodGet + " /api/saas-review/take":
		response, err = this.saasReview.GetSaasReview(callCtx, request)
	case http.MethodPost + " /api/saas-review/review":
		response, err = this.saasReview.ReviewSaasTenantApplication(callCtx, request)
	default:
		return nil, errors.New("licence: gRPC 管理面未声明该操作：" + call.Method + " " + call.Path)
	}
	if err != nil {
		return nil, adminGRPCError(err)
	}
	return adminData(response)
}

type adminFileStream interface {
	Send(*licencev1.AdminFileChunk) error
	CloseAndRecv() (*licencev1.AdminResponse, error)
}

func (this *grpcAdminTransport) Upload(ctx context.Context, upload adminUpload) (json.RawMessage, error) {
	fields, err := json.Marshal(upload.Fields)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := this.callContext(ctx, upload.Token)
	defer cancel()

	var stream adminFileStream
	switch upload.Path {
	case "/api/project-artifacts/upload":
		stream, err = this.artifact.UploadArtifact(callCtx)
	case "/api/project-artifacts/verify":
		stream, err = this.artifact.VerifyArtifactFile(callCtx)
	default:
		return nil, errors.New("licence: gRPC 不支持该文件操作：" + upload.Path)
	}
	if err != nil {
		return nil, adminGRPCError(err)
	}

	buffer := make([]byte, 64<<10)
	first := true
	for {
		count, readErr := upload.Content.Read(buffer)
		if count > 0 || first {
			chunk := &licencev1.AdminFileChunk{Chunk: append([]byte(nil), buffer[:count]...)}
			if first {
				chunk.Json = fields
				chunk.FileName = filepath.Base(upload.FileName)
			}
			if err = stream.Send(chunk); err != nil {
				return nil, adminGRPCError(err)
			}
			first = false
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		return nil, adminGRPCError(err)
	}
	return adminData(response)
}

func (this *grpcAdminTransport) Close() error {
	this.closeOnce.Do(func() { this.closeErr = this.conn.Close() })
	return this.closeErr
}

var _ adminTransport = (*grpcAdminTransport)(nil)
