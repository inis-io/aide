package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"google.golang.org/protobuf/proto"
)

func TestHTTPContent(t *testing.T) {
	body := []byte(`{"licenseNo":"LIC-2026-000001"}`)
	sum := sha256.Sum256(body)
	want := "POST\n/api/v1/licenses/validate\n1000\nnonce\n" + hex.EncodeToString(sum[:])
	if got := string(HTTPContent("post", "/api/v1/licenses/validate", "1000", "nonce", body)); got != want {
		t.Fatalf("HTTP canonical 不一致：\nwant=%s\ngot=%s", want, got)
	}
}

func TestGRPCContent(t *testing.T) {
	request := &licencev1.ValidateRequest{
		LicenseNo: "LIC-2026-000001",
		Usage:     map[string]int64{"b": 2, "a": 1},
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	method := licencev1.LicenseRuntimeService_Validate_FullMethodName
	want := "GRPC\n" + method + "\n1000\nnonce\n" + hex.EncodeToString(sum[:])
	got, err := GRPCContent(method, "1000", "nonce", request)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("gRPC canonical 不一致：\nwant=%s\ngot=%s", want, got)
	}
}
