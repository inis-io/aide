package starv1

import (
	"os"
	"strings"
	"testing"
)

// TestProtocolMatrixCoversEveryRPC - 矩阵覆盖自检（范式照 proto/licence/v1 同名测试）：
// star/v1 生成代码注册的每个 RPC 必须在 protocol-matrix.yaml 有对应行。
// 矩阵第 3 列书写 gRPC full method 真名（不含前导 /，如
// licenhub.star.v1.StarDirectoryService/Join），此处按服务全名 + 方法名拼针匹配。
func TestProtocolMatrixCoversEveryRPC(t *testing.T) {
	raw, err := os.ReadFile("protocol-matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	services := File_star_v1_star_proto.Services()
	for index := 0; index < services.Len(); index++ {
		service := services.Get(index)
		for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
			method := service.Methods().Get(methodIndex)
			needle := string(service.FullName()) + "/" + string(method.Name())
			if !strings.Contains(text, needle) {
				t.Errorf("矩阵缺少 %s/%s", service.Name(), method.Name())
			}
		}
	}
}

// TestGeneratedRPCsAreBoundBySDKTransport - SDK 传输绑定门禁（范式照 proto/apis/v1 同名测试，
// 星链无管理面、无独立 admin 传输文件，只守护 runtime 运行面传输）：
//
//  1. 每个 rpc 的生成代码客户端方法在 SDK gRPC 传输层被调用（typed stub 绑定，防漏绑）；
//  2. 每个 rpc 的 FullMethodName 常量被引用（gRPC 签名 canonical 必须取常量原值，禁止手拼）；
//  3. 矩阵中每条 landed 行的 HTTP 路径都出现在传输层路由 switch 中
//     （HTTP/gRPC 两协议对同一能力同批落地，任一侧缺失即未完成）。
func TestGeneratedRPCsAreBoundBySDKTransport(t *testing.T) {

	raw, err := os.ReadFile("../../../runtime/runtime-transport-grpc.go")
	if err != nil {
		t.Fatalf("读取 SDK gRPC 传输层失败：%v", err)
	}
	transport := string(raw)

	services := File_star_v1_star_proto.Services()
	for index := 0; index < services.Len(); index++ {
		service := services.Get(index)
		for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
			method := service.Methods().Get(methodIndex)
			name := string(method.Name())
			if !strings.Contains(transport, "."+name+"(") {
				t.Errorf("SDK gRPC transport 未绑定 %s/%s（缺少 typed stub 调用）", service.Name(), name)
			}
			if !strings.Contains(transport, name+"_FullMethodName") {
				t.Errorf("%s/%s 未引用生成代码 FullMethodName 常量（gRPC 签名 canonical 必须取常量原值）", service.Name(), name)
			}
		}
	}

	// landed 行的 HTTP 路径必须出现在传输层路由 switch（矩阵行格式：- [SDK 方法, POST, 路径, ...]）
	matrix, err := os.ReadFile("protocol-matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(matrix), "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "- [") || !strings.HasSuffix(field, ", landed]") {
			continue
		}
		start := strings.Index(field, "/api/v1/star/")
		end := strings.Index(field[start:], ",")
		if start < 0 || end <= 0 {
			t.Errorf("landed 矩阵行缺少 HTTP 路径：%s", field)
			continue
		}
		path := field[start : start+end]
		if !strings.Contains(transport, path) {
			t.Errorf("landed 行的 HTTP 路径 %s 未出现在 SDK gRPC 传输层路由 switch", path)
		}
	}
}
