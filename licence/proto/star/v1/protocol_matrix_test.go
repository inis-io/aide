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
