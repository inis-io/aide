package apisv1

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
)

// API 商城运行面契约的机器门禁（检查口径对照 proto/licence/v1/protocol_matrix_test.go）：
//
//	1) 矩阵行可解析、列数与取值合法（行级解析，不做 YAML 语法校验——为此引入 YAML 依赖
//	   不划算，矩阵头部注记与 defaults 段不参与校验）；
//	2) 矩阵 ↔ 生成代码双向一致：每个 rpc 有矩阵行、每行指向真实 rpc，且 gRPC full method
//	   与生成代码的 FullMethodName 常量逐字相同（真名 licenhub.apis.v1.ApisRuntimeService/*）；
//	3) HTTP 方法与路径与 licen-hub 04 设计文档一致，landed/pending 状态与服务端路由现实一致。
//
// 与 licence/v1 同名测试的差异：那里的绑定断言在 TestGeneratedRPCsAreBoundBySDKTransports，
// 本文件对应断言为 TestGeneratedRPCsAreBoundBySDKTransport（T19 SDK 传输绑定已落地，
// 除 stub 方法调用外额外要求引用 FullMethodName 常量与矩阵 landed 行的 HTTP 路径）。

const (
	// matrixColumns - 矩阵固定列数（列序见 protocol-matrix.yaml 头部注记）。
	matrixColumns = 12
	// matrixFile - 协议矩阵文件（与 proto 同目录维护，是唯一映射清单）。
	matrixFile = "protocol-matrix.yaml"
	// routePrefix - 运行面 HTTP 路径前缀（licen-hub GenRoute table = "v1/apis"）。
	routePrefix = "/api/v1/apis/"
	// routeTable - licen-hub 运行面路由表标识（GenRoute 返回的 table 字面量）。
	routeTable = `"v1/apis"`
	// clientTransportPath - SDK 侧 gRPC 运行面传输层（T19 落地物：apis 三路径 switch 与 typed stub）。
	clientTransportPath = "../../../runtime/runtime-transport-grpc.go"
	// designDocPath - licen-hub 04 设计文档（兄弟仓库；未检出时相关断言跳过）。
	designDocPath = "../../../../../licen-hub/docs/plan/apis/04-HTTP与gRPC双协议设计.md"
	// serverRouteDir - licen-hub 运行面 HTTP 适配层目录（landed 行的落地事实来源，按目录整体扫描）。
	serverRouteDir = "../../../../../licen-hub/backend/app/api/control"
	// wantGoPackage - 契约的生成代码归属（licen-hub backend 与 SDK 共同消费，改动即破坏下游）。
	wantGoPackage = "github.com/inis-io/aide/licence/proto/apis/v1;apisv1"
)

var (
	// matrixHTTPMethods / matrixActions / matrixRisks / matrixIdempotency / matrixStates - 列取值白名单。
	matrixHTTPMethods = map[string]bool{"GET": true, "POST": true}
	matrixActions     = map[string]bool{"read": true, "write": true}
	matrixRisks       = map[string]bool{"normal": true, "high": true, "critical": true}
	matrixIdempotency = map[string]bool{"true": true, "false": true, "read-only": true}
	matrixStates      = map[string]bool{"landed": true, "pending": true}
	// matrixBusinessCode - 关键业务码单元格内的单个码形态（大写 + 下划线）。
	matrixBusinessCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	// fullMethodConstants - 生成代码的 full method 常量（新增 rpc 必须在此登记，见 Test 2）。
	fullMethodConstants = map[string]string{
		"Invoke":   ApisRuntimeService_Invoke_FullMethodName,
		"IPLocate": ApisRuntimeService_IPLocate_FullMethodName,
		"Usage":    ApisRuntimeService_Usage_FullMethodName,
	}
)

// matrixRow - 协议矩阵单行（列序与 protocol-matrix.yaml 头部注记一致）。
type matrixRow struct {
	sdkMethod  string
	httpMethod string
	httpPath   string
	fullMethod string
	auth       string
	permission string
	domain     string
	action     string
	risk       string
	idempotent string
	keyErrors  string
	state      string
}

// loadMatrix - 读取并解析协议矩阵。
// 行格式为固定 matrixColumns 列的 `- [a, b, ...]`（行内逗号分隔，单元格可用双引号包裹，
// 以便容纳关键业务码这类含空格的值）；其它行（头部注记、空行、服务分组键）不参与解析。
func loadMatrix(t *testing.T) (rows []matrixRow, text string) {
	t.Helper()
	raw, err := os.ReadFile(matrixFile)
	if err != nil {
		t.Fatalf("读取协议矩阵失败：%v", err)
	}
	text = string(raw)
	for number, line := range strings.Split(text, "\n") {
		field := strings.TrimSpace(line)
		if !strings.HasPrefix(field, "- [") {
			continue
		}
		if !strings.HasSuffix(field, "]") {
			t.Fatalf("%s 第 %d 行不是合法的矩阵行（缺少结尾 ]）：%s", matrixFile, number+1, field)
		}
		cells := splitMatrixCells(strings.TrimSuffix(strings.TrimPrefix(field, "- ["), "]"))
		if len(cells) != matrixColumns {
			t.Fatalf("%s 第 %d 行列数 = %d，应为 %d：%s", matrixFile, number+1, len(cells), matrixColumns, field)
		}
		rows = append(rows, matrixRow{
			sdkMethod: cells[0], httpMethod: cells[1], httpPath: cells[2], fullMethod: cells[3],
			auth: cells[4], permission: cells[5], domain: cells[6], action: cells[7],
			risk: cells[8], idempotent: cells[9], keyErrors: cells[10], state: cells[11],
		})
	}
	if len(rows) == 0 {
		t.Fatalf("%s 未解析出任何矩阵行", matrixFile)
	}
	return rows, text
}

// splitMatrixCells - 按逗号切分行内单元格，双引号内的逗号不切分，并去掉包裹引号。
func splitMatrixCells(body string) []string {
	cells := []string{}
	var builder strings.Builder
	quoted := false
	for _, char := range body {
		switch {
		case char == '"':
			quoted = !quoted
		case char == ',' && !quoted:
			cells = append(cells, strings.TrimSpace(builder.String()))
			builder.Reset()
		default:
			builder.WriteRune(char)
		}
	}
	cells = append(cells, strings.TrimSpace(builder.String()))
	for index, cell := range cells {
		if len(cell) >= 2 && strings.HasPrefix(cell, `"`) && strings.HasSuffix(cell, `"`) {
			cells[index] = cell[1 : len(cell)-1]
		}
	}
	return cells
}

// generatedFullMethods - 生成代码注册的全部 rpc（full method 真名 → 服务名）。
func generatedFullMethods() map[string]string {
	methods := map[string]string{}
	services := File_apis_v1_runtime_proto.Services()
	for index := 0; index < services.Len(); index++ {
		service := services.Get(index)
		for methodIndex := 0; methodIndex < service.Methods().Len(); methodIndex++ {
			method := service.Methods().Get(methodIndex)
			methods[string(service.FullName())+"/"+string(method.Name())] = string(service.FullName())
		}
	}
	return methods
}

// TestProtocolMatrixParsesAndCoversEveryRPC - 矩阵可解析、取值合法，且与生成代码双向覆盖。
func TestProtocolMatrixParsesAndCoversEveryRPC(t *testing.T) {
	rows, text := loadMatrix(t)

	// 契约归属：package 与 go_package 是下游（licen-hub backend 与 SDK）的编译期依赖。
	if want := "contract: " + string(File_apis_v1_runtime_proto.Package()); !strings.Contains(text, want) {
		t.Errorf("矩阵缺少契约声明（应为 %q）", want)
	}
	if got := protodesc.ToFileDescriptorProto(File_apis_v1_runtime_proto).GetOptions().GetGoPackage(); got != wantGoPackage {
		t.Errorf("go_package = %q，应为 %q", got, wantGoPackage)
	}

	// 逐行取值校验：列白名单、必填列、业务码形态。
	seenPath := map[string]string{}
	for _, row := range rows {
		if row.sdkMethod == "" || row.fullMethod == "" || row.domain == "" {
			t.Errorf("矩阵行 %s 缺少必填列（SDK 方法 / full method / 数据域）", row.fullMethod)
		}
		if !matrixHTTPMethods[row.httpMethod] {
			t.Errorf("矩阵行 %s 的 HTTP 方法 %q 非法（仅 GET / POST）", row.sdkMethod, row.httpMethod)
		}
		if !strings.HasPrefix(row.httpPath, routePrefix) {
			t.Errorf("矩阵行 %s 的 HTTP 路径 %q 不在运行面前缀 %s 下", row.sdkMethod, row.httpPath, routePrefix)
		}
		if row.auth != "activation-sign-v1" {
			t.Errorf("矩阵行 %s 的认证 %q 非法（运行面固定 activation-sign-v1）", row.sdkMethod, row.auth)
		}
		if row.permission != "" {
			t.Errorf("矩阵行 %s 登记了权限码 %q：运行面身份由许可证凭证推导，不进 IAM 权限码体系", row.sdkMethod, row.permission)
		}
		if !matrixActions[row.action] || !matrixRisks[row.risk] || !matrixIdempotency[row.idempotent] || !matrixStates[row.state] {
			t.Errorf("矩阵行 %s 的取值非法（操作 %q / 风险 %q / 幂等 %q / 状态 %q）",
				row.sdkMethod, row.action, row.risk, row.idempotent, row.state)
		}
		if row.keyErrors == "" {
			t.Errorf("矩阵行 %s 未登记关键业务码", row.sdkMethod)
		}
		for _, code := range strings.Fields(row.keyErrors) {
			if !matrixBusinessCode.MatchString(code) {
				t.Errorf("矩阵行 %s 的业务码 %q 形态非法（应为大写 + 下划线）", row.sdkMethod, code)
			}
		}
		if previous, exists := seenPath[row.httpPath]; exists {
			t.Errorf("矩阵行 %s 与 %s 共用 HTTP 路径 %s（每个能力一条独立路径）", row.sdkMethod, previous, row.httpPath)
		}
		seenPath[row.httpPath] = row.sdkMethod
	}

	// 双向覆盖：生成代码的每个 rpc 必须有矩阵行，矩阵每行必须指向真实 rpc。
	generated := generatedFullMethods()
	registered := map[string]string{}
	for _, row := range rows {
		if previous, exists := registered[row.fullMethod]; exists {
			t.Errorf("矩阵行 %s 与 %s 重复登记 full method %s", row.sdkMethod, previous, row.fullMethod)
		}
		registered[row.fullMethod] = row.sdkMethod
		if _, exists := generated[row.fullMethod]; !exists {
			t.Errorf("矩阵行 %s 指向生成代码不存在的 full method %s", row.sdkMethod, row.fullMethod)
		}
	}
	for fullMethod := range generated {
		if _, exists := registered[fullMethod]; !exists {
			t.Errorf("生成代码的 rpc %s 未登记进协议矩阵", fullMethod)
		}
	}
}

// TestGeneratedFullMethodNamesMatchMatrix - full method 真名在矩阵与生成代码间逐字一致。
func TestGeneratedFullMethodNamesMatchMatrix(t *testing.T) {
	rows, _ := loadMatrix(t)
	generated := generatedFullMethods()

	// 常量表必须覆盖生成代码的全部 rpc：新增 rpc 时忘记登记会在本断言失败。
	for fullMethod := range generated {
		service, method, ok := strings.Cut(fullMethod, "/")
		if !ok {
			t.Fatalf("生成代码 full method 形态异常：%s", fullMethod)
		}
		if _, exists := fullMethodConstants[method]; !exists {
			t.Errorf("生成代码新增 rpc %s/%s 未登记进 fullMethodConstants（本测试的常量表）", service, method)
		}
	}

	for _, row := range rows {
		service, method, ok := strings.Cut(row.fullMethod, "/")
		if !ok {
			t.Errorf("矩阵行 %s 的 full method %q 缺少服务名前缀", row.sdkMethod, row.fullMethod)
			continue
		}
		constant, exists := fullMethodConstants[method]
		if !exists {
			continue
		}
		if got := strings.TrimPrefix(constant, "/"); got != row.fullMethod {
			t.Errorf("矩阵行 %s 的 full method %q 与生成代码常量 %q 不一致", row.sdkMethod, row.fullMethod, got)
		}
		if name, exists := generated[row.fullMethod]; !exists || name != service {
			t.Errorf("矩阵行 %s 的 full method %q 未在生成代码注册", row.sdkMethod, row.fullMethod)
		}
	}
}

// TestProtocolMatrixHTTPMatchesDesignAndServer - HTTP 方法与路径与 04 设计文档一致，
// 且 HTTP 路由落地状态（landed / pending）与 licen-hub 服务端路由表现实一致。
func TestProtocolMatrixHTTPMatchesDesignAndServer(t *testing.T) {
	rows, _ := loadMatrix(t)

	designDoc, err := os.ReadFile(designDocPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("兄弟仓库 licen-hub 未检出（%s 不存在），跳过 HTTP 路径与设计文档/服务端落地一致性断言", designDocPath)
		}
		t.Fatalf("读取 04 设计文档失败：%v", err)
	}
	serverRoute := loadRuntimeRouteSource(t)

	for _, row := range rows {
		needle := row.httpMethod + " " + row.httpPath
		if !strings.Contains(string(designDoc), needle) {
			t.Errorf("04 设计文档缺少矩阵行 %s 的 HTTP 契约 %q", row.sdkMethod, needle)
		}
		if !strings.Contains(string(designDoc), row.fullMethod) {
			t.Errorf("04 设计文档缺少矩阵行 %s 的 gRPC full method %q", row.sdkMethod, row.fullMethod)
		}
		if !strings.HasPrefix(row.httpPath, routePrefix) {
			continue
		}
		key := strings.TrimPrefix(row.httpPath, routePrefix)
		registered := strings.Contains(serverRoute, `{Key: "`+key+`"`)
		switch row.state {
		case "landed":
			if !registered {
				t.Errorf("矩阵行 %s 标记 landed，但运行面路由目录 %s 内无 Key=%q 登记", row.sdkMethod, serverRouteDir, key)
			}
		case "pending":
			// pending = HTTP 路由尚未落地（T18 范围），只校验与 04 设计文档一致，不校验服务端实现。
			if registered {
				t.Errorf("矩阵行 %s 标记 pending，但运行面路由已登记 Key=%q：落地后必须把 state 改为 landed", row.sdkMethod, key)
			}
			t.Logf("矩阵行 %s 的 HTTP 路由 %s 尚未落地（T18 范围），本测试只校验设计文档一致性", row.sdkMethod, row.httpPath)
		}
	}
}

// loadRuntimeRouteSource - 汇总 licen-hub 运行面 HTTP 适配层源码：landed/pending 门禁以
// 「声明 routeTable（"v1/apis"）的路由表 + 其登记文件」整体为证据源，而不是绑定单个文件——
// T18 把 /api/v1/apis/usage 落到新文件或并入现有文件都能被覆盖；只扫声明该表的文件，
// 避免误采其它域的同名 Key（如管理面 apis-usage 表的 rows / find）。
func loadRuntimeRouteSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(serverRouteDir)
	if err != nil {
		t.Fatalf("licen-hub 已检出但运行面路由目录不可读（%s）：%v", serverRouteDir, err)
	}
	var builder strings.Builder
	files := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(serverRouteDir, name))
		if err != nil {
			t.Fatalf("读取路由文件 %s 失败：%v", name, err)
		}
		if !strings.Contains(string(raw), routeTable) {
			continue
		}
		builder.Write(raw)
		builder.WriteString("\n")
		files++
	}
	if files == 0 {
		t.Fatalf("运行面路由目录 %s 下没有声明 %s 表的文件（运行面 HTTP 适配层缺失或已迁移）", serverRouteDir, routeTable)
	}
	return builder.String()
}

// TestGeneratedRPCsAreBoundBySDKTransport - T19 SDK 传输绑定门禁（对照 licence/v1 的
// TestGeneratedRPCsAreBoundBySDKTransports，本契约的三项额外要求）：
//
//  1. 每个 rpc 的生成代码客户端方法在 SDK gRPC 传输层被调用（typed stub 绑定，防漏绑）；
//  2. 每个 rpc 的 FullMethodName 常量被引用（gRPC 签名 canonical 必须取常量原值，禁止手拼）；
//  3. 矩阵中每条 landed 行的 HTTP 路径都出现在传输层路由 switch 中
//     （HTTP/gRPC 两协议对同一能力同批落地，任一侧缺失即未完成）。
func TestGeneratedRPCsAreBoundBySDKTransport(t *testing.T) {

	raw, err := os.ReadFile(clientTransportPath)
	if err != nil {
		t.Fatalf("读取 SDK gRPC 传输层失败（%s，T19 落地物）：%v", clientTransportPath, err)
	}
	transport := string(raw)

	services := File_apis_v1_runtime_proto.Services()
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

	rows, _ := loadMatrix(t)
	for _, row := range rows {
		if row.state != "landed" {
			continue
		}
		if !strings.Contains(transport, row.httpPath) {
			t.Errorf("矩阵行 %s 标记 landed，但 SDK gRPC 传输未映射 HTTP 路径 %s", row.sdkMethod, row.httpPath)
		}
	}
}
