package licencev1

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestProtocolMatrixCoversEveryRPC(t *testing.T) {
	raw, err := os.ReadFile("protocol-matrix.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	check := func(services protoreflect.ServiceDescriptors, runtime bool) {
		for index := 0; index < services.Len(); index++ {
			service := services.Get(index)
			if !runtime {
				marker := "  " + string(service.Name()) + ":"
				if !strings.Contains(text, marker) {
					t.Errorf("矩阵缺少服务 %s", service.Name())
					continue
				}
			}
			for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
				method := service.Methods().Get(methodIndex)
				needle := "[" + string(method.Name()) + ","
				if runtime {
					needle = string(service.Name()) + "/" + string(method.Name())
				}
				if !strings.Contains(text, needle) {
					t.Errorf("矩阵缺少 %s/%s", service.Name(), method.Name())
				}
			}
		}
	}
	check(File_licence_v1_runtime_proto.Services(), true)
	check(File_licence_v1_admin_proto.Services(), false)
}

func TestGeneratedRPCsAreBoundBySDKTransports(t *testing.T) {
	adminSource, err := os.ReadFile("../../../admin-transport-grpc.go")
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, err := os.ReadFile("../../../runtime-transport-grpc.go")
	if err != nil {
		t.Fatal(err)
	}
	extendedSource, err := os.ReadFile("../../../runtime-transport-grpc-extended.go")
	if err != nil {
		t.Fatal(err)
	}
	assertMethods := func(services protoreflect.ServiceDescriptors, source string) {
		for index := 0; index < services.Len(); index++ {
			service := services.Get(index)
			for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
				method := service.Methods().Get(methodIndex)
				if !strings.Contains(source, "."+string(method.Name())+"(") {
					t.Errorf("SDK gRPC transport 未绑定 %s/%s", service.Name(), method.Name())
				}
			}
		}
	}
	assertMethods(File_licence_v1_admin_proto.Services(), string(adminSource))
	assertMethods(File_licence_v1_runtime_proto.Services(), string(runtimeSource)+string(extendedSource))
}
