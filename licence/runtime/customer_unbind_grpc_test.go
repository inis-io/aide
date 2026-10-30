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
	code string
}

func (this *runtimeCustomerUnbindServer) CustomerUnbind(ctx context.Context, request *licencev1.CustomerUnbindRequest) (*licencev1.CustomerUnbindResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			this.missing = append(this.missing, key)
		}
	}
	this.code = request.GetTwoFaCode()
	if this.failCode != codes.OK {
		return nil, status.Error(this.failCode, "许可证或实例信息无效")
	}
	code := this.status
	if code == "" {
		code = "OK"
	}
	return &licencev1.CustomerUnbindResponse{Status: code, Message: this.message, ServerTime: time.Now().UnixMilli()}, nil
}

func TestGRPCCustomerUnbind(t *testing.T) {
	server := &runtimeCustomerUnbindServer{}
	client := newCustomerBindGRPCClient(t, server)
	if err := client.CustomerUnbind(context.Background(), "123456"); err != nil {
		t.Fatalf("gRPC 解绑失败: %v", err)
	}
	if server.code != "123456" {
		t.Fatal("gRPC 解绑未透传验证码")
	}
	if len(server.missing) > 0 {
		t.Fatalf("缺少签名 metadata: %v", server.missing)
	}
}

func TestGRPCCustomerUnbindRejected(t *testing.T) {
	server := &runtimeCustomerUnbindServer{runtimeCustomerBindServer: runtimeCustomerBindServer{status: "INVALID_ARGUMENT", message: "两步验证码错误或已过期"}}
	client := newCustomerBindGRPCClient(t, server)
	err := client.CustomerUnbind(context.Background(), "000000")
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "INVALID_ARGUMENT" || provisionErr.Message != "两步验证码错误或已过期" {
		t.Fatalf("gRPC 解绑业务拒绝透传不符: %T %v", err, err)
	}
}
