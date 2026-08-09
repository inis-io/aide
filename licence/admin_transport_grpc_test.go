package licence

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type artifactStreamServer struct {
	licencev1.UnimplementedArtifactAdminServiceServer
	t *testing.T
}

func (s artifactStreamServer) receive(stream grpc.ClientStreamingServer[licencev1.AdminFileChunk, licencev1.AdminResponse]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	if values := md.Get("authorization"); len(values) != 1 || values[0] != "Bearer jwt" {
		s.t.Fatalf("authorization=%v", values)
	}
	total := 0
	frames := 0
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		frames++
		total += len(chunk.GetChunk())
		if frames == 1 && (chunk.GetFileName() != "artifact.bin" || len(chunk.GetJson()) == 0) {
			s.t.Fatalf("首帧元数据不完整：%v", chunk)
		}
	}
	if frames < 3 {
		s.t.Fatalf("期望多帧流，实际 %d", frames)
	}
	raw, _ := json.Marshal(map[string]int{"size": total})
	return stream.SendAndClose(&licencev1.AdminResponse{Code: 200, Message: "ok", DataJson: raw})
}
func (s artifactStreamServer) UploadArtifact(stream grpc.ClientStreamingServer[licencev1.AdminFileChunk, licencev1.AdminResponse]) error {
	return s.receive(stream)
}
func (s artifactStreamServer) VerifyArtifactFile(stream grpc.ClientStreamingServer[licencev1.AdminFileChunk, licencev1.AdminResponse]) error {
	return s.receive(stream)
}

func TestGRPCAdminUploadUsesClientStreaming(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterArtifactAdminServiceServer(server, artifactStreamServer{t: t})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	transport := &grpcAdminTransport{client: &AdminClient{options: AdminOptions{}}, conn: conn, artifact: licencev1.NewArtifactAdminServiceClient(conn)}
	content := bytes.Repeat([]byte("a"), 200<<10)
	data, err := transport.Upload(context.Background(), adminUpload{Path: "/api/project-artifacts/upload", Fields: map[string]string{"versionId": "1"}, FileName: "artifact.bin", Content: bytes.NewReader(content), Token: "jwt"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Size int `json:"size"`
	}
	if json.Unmarshal(data, &result) != nil || result.Size != len(content) {
		t.Fatalf("result=%s", data)
	}
	_ = transport.Close()
}

func TestAdminGRPCStatusMapsToAPIErrorWithCause(t *testing.T) {
	source := status.Error(codes.Unauthenticated, "expired")
	mapped := adminGRPCError(source)
	apiError, ok := mapped.(*APIError)
	if !ok || apiError.Code != http.StatusUnauthorized {
		t.Fatalf("mapped=%T %#v", mapped, mapped)
	}
	if status.Code(apiError.Unwrap()) != codes.Unauthenticated {
		t.Fatal("gRPC cause 丢失")
	}
}
