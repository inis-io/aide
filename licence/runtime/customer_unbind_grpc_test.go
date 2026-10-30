package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type runtimeCustomerUnbindServer struct {
	runtimeCustomerBindServer
	code        string
	method      string
	challengeNo string
	password    string
	channel     string
}

func (this *runtimeCustomerUnbindServer) verifyMetadata(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			this.missing = append(this.missing, key)
		}
	}
}

func (this *runtimeCustomerUnbindServer) CustomerUnbind(ctx context.Context, request *licencev1.CustomerUnbindRequest) (*licencev1.CustomerUnbindResponse, error) {
	this.verifyMetadata(ctx)
	this.code = request.GetCode()
	this.method = request.GetMethod()
	this.challengeNo = request.GetChallengeNo()
	this.password = request.GetPassword()
	if this.failCode != codes.OK {
		return nil, status.Error(this.failCode, "许可证或实例信息无效")
	}
	code := this.status
	if code == "" {
		code = "OK"
	}
	return &licencev1.CustomerUnbindResponse{Status: code, Message: this.message, ServerTime: time.Now().UnixMilli()}, nil
}

func (this *runtimeCustomerUnbindServer) CustomerUnbindOptions(ctx context.Context, request *licencev1.CustomerUnbindOptionsRequest) (*licencev1.CustomerUnbindOptionsResponse, error) {
	this.verifyMetadata(ctx)
	return &licencev1.CustomerUnbindOptionsResponse{
		Status: "OK", Preferred: "email", Methods: []string{"email", "sms", "password"},
		Destination: &licencev1.CustomerUnbindDestination{Email: "t***@example.com", Phone: "138****8000"},
		ServerTime:  time.Now().UnixMilli(),
	}, nil
}

func (this *runtimeCustomerUnbindServer) CustomerUnbindChallenge(ctx context.Context, request *licencev1.CustomerUnbindChallengeRequest) (*licencev1.CustomerUnbindChallengeResponse, error) {
	this.verifyMetadata(ctx)
	this.channel = request.GetChannel()
	return &licencev1.CustomerUnbindChallengeResponse{
		Status: "OK", ChallengeNo: "challenge-sms", Destination: "138****8000",
		ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(), ServerTime: time.Now().UnixMilli(),
	}, nil
}

func TestGRPCCustomerUnbind(t *testing.T) {
	server := &runtimeCustomerUnbindServer{}
	client := newCustomerBindGRPCClient(t, server)
	if err := client.CustomerUnbind(context.Background(), CustomerUnbindInput{Code: "123456"}); err != nil {
		t.Fatalf("gRPC 解绑失败: %v", err)
	}
	if server.code != "123456" || server.method != "totp" {
		t.Fatal("gRPC 解绑未透传默认 TOTP 凭据")
	}
	if len(server.missing) > 0 {
		t.Fatalf("缺少签名 metadata: %v", server.missing)
	}
}

func TestGRPCCustomerUnbindVerification(t *testing.T) {
	server := &runtimeCustomerUnbindServer{}
	client := newCustomerBindGRPCClient(t, server)
	options, err := client.CustomerUnbindOptions(context.Background())
	if err != nil || options.Preferred != "email" || len(options.Methods) != 3 || options.Destination.Email != "t***@example.com" {
		t.Fatalf("gRPC 解绑选项映射不符: %+v err=%v", options, err)
	}
	challenge, err := client.CustomerUnbindChallenge(context.Background(), "sms")
	if err != nil || challenge.ChallengeNo != "challenge-sms" || challenge.Destination != "138****8000" || server.channel != "sms" {
		t.Fatalf("gRPC 短信挑战映射不符: %+v err=%v", challenge, err)
	}
	if err = client.CustomerUnbind(context.Background(), CustomerUnbindInput{Method: "email", ChallengeNo: "challenge-email", Code: "123456"}); err != nil {
		t.Fatal(err)
	}
	if server.method != "email" || server.challengeNo != "challenge-email" || server.code != "123456" {
		t.Fatalf("gRPC 邮箱凭据映射不符: method=%s challenge=%s", server.method, server.challengeNo)
	}
	if err = client.CustomerUnbind(context.Background(), CustomerUnbindInput{Method: "password", Password: "secret-password"}); err != nil {
		t.Fatal(err)
	}
	if server.method != "password" || server.password != "secret-password" {
		t.Fatal("gRPC 密码凭据未透传")
	}
}

func TestGRPCCustomerUnbindRejected(t *testing.T) {
	server := &runtimeCustomerUnbindServer{runtimeCustomerBindServer: runtimeCustomerBindServer{status: "INVALID_ARGUMENT", message: "两步验证码错误或已过期"}}
	client := newCustomerBindGRPCClient(t, server)
	err := client.CustomerUnbind(context.Background(), CustomerUnbindInput{Code: "000000"})
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "INVALID_ARGUMENT" || provisionErr.Message != "两步验证码错误或已过期" {
		t.Fatalf("gRPC 解绑业务拒绝透传不符: %T %v", err, err)
	}
}
