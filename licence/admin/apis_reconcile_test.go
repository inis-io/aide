package admin

import (
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// API 商城管理面三向对账自检。
//
// 三份事实必须逐条一致，任一方向漂移即测试失败：
//  1. SDK 资源层 `admin/apis.go`：每个 typed 方法的 HTTP 动词 + 路径（从源码解析，非人工清单）；
//  2. SDK 传输层 `admin/admin-transport-grpc.go`：`"动词 路径"` case 键引导的 full method 常量
//     （常量值再回到 apis.go 的常量块解析 `"/" + <服务前缀> + "/<方法>"`）；
//  3. 平台唯一事实源 licen-hub `backend/grpc/admin/v1/apis.go` 的 `ApisSpecs` 登记表
//     （Service/Method/HTTPMethod/HTTPPath 逐字）；兄弟仓库未检出时该向断言跳过（t.Skipf）。
//
// 反向覆盖：SDK 与传输层里多出来的商城方法/路由必须能在平台登记表找到（防臆造路由）；
// 用例表（apis_test.go 的 apisRouteCases）也必须与 SDK 源码逐条对齐（防表漏项造成假绿）。

const (
	// apisSDKSourcePath - SDK 资源层源码（typed 方法 ↔ HTTP 路由）
	apisSDKSourcePath = "apis.go"
	// apisTransportSourcePath - SDK gRPC 传输层源码（HTTP 路由 ↔ full method 常量）
	apisTransportSourcePath = "admin-transport-grpc.go"
	// apisServerRegistryPath - 平台登记表源码（兄弟仓库 licen-hub；未检出时跳过该向断言）
	apisServerRegistryPath = "../../../licen-hub/backend/grpc/admin/v1/apis.go"
	// apisServerPackage - 平台 proto-less 服务的权威包名（曾出现 lichenhub 笔误，此处回归守护）
	apisServerPackage = "licenhub.licence.v1"
	// apisRouteCount - 商城管理面受控路由条数（与平台登记表条数互为断言）
	apisRouteCount = 53
)

var (
	// apisSDKMethodPattern - SDK 资源层方法签名（只取导出方法；unexported 助手 exportBills 不参与对账）
	apisSDKMethodPattern = regexp.MustCompile(`(?m)^func \(this \*ApisResource\) ([A-Z]\w*)\(`)
	// apisSDKRoutePattern - 方法体内的路由声明：client.get/getWithQuery/post/put/del(ctx, "路径")
	// 或共用助手 this.exportBills(ctx, "路径")（助手内部固定走 GET）
	apisSDKRoutePattern = regexp.MustCompile(`this\.client\.(getWithQuery|get|post|put|del)\(ctx, "([^"]+)"|this\.exportBills\(ctx, "([^"]+)"`)
	// apisSDKVerbByCall - 方法名 → HTTP 动词
	apisSDKVerbByCall = map[string]string{
		"getWithQuery": http.MethodGet, "get": http.MethodGet, "post": http.MethodPost,
		"put": http.MethodPut, "del": http.MethodDelete, "exportBills": http.MethodGet,
	}
	// apisTransportVerbBySource - 传输层 case 键里的 http.MethodX 写法 → HTTP 动词字面量
	apisTransportVerbBySource = map[string]string{
		"http.MethodGet": http.MethodGet, "http.MethodPost": http.MethodPost,
		"http.MethodPut": http.MethodPut, "http.MethodDelete": http.MethodDelete,
	}
	// apisTransportCasePattern - 传输层 case 键（"动词 路径"）
	apisTransportCasePattern = regexp.MustCompile(`^case (http\.Method\w+) \+ " ([^"]+)":$`)
	// apisTransportInvokePattern - case 体内的 proto-less 调用（full method 常量必须出自常量块）
	apisTransportInvokePattern = regexp.MustCompile(`^response, err = this\.invokeApis\(callCtx, (\w+), request\)$`)
	// apisServiceConstantPattern - apis.go 的 gRPC 服务前缀常量
	apisServiceConstantPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"(licenhub\.licence\.v1\.\w+)"\s*$`)
	// apisFullMethodConstantPattern - apis.go 的 full method 常量（"/" + <服务前缀常量> + "/<方法>"）
	apisFullMethodConstantPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"/"\s*\+\s*(\w+)\s*\+\s*"/(\w+)"\s*$`)
	// apisServerPackagePattern - 平台登记表的包名常量（proto-less 服务与既有 proto 服务同包）
	apisServerPackagePattern = regexp.MustCompile(`(?m)^const apisAdminPackage = "([^"]+)"$`)
	// apisServerSpecPattern - 平台登记表条目（Risk 为空时字段省略）
	apisServerSpecPattern = regexp.MustCompile(`\{Service: "(\w+)", Method: "(\w+)", HTTPMethod: "(\w+)", HTTPPath: "([^"]+)", Permission: "([^"]+)"(?:, Risk: "(\w+)")?, handler: \w+\}`)
)

// apisSDKRoute - SDK 资源层的一条路由
type apisSDKRoute struct {
	// method - SDK 方法名（与平台 ApisSpecs.Method 逐字一致）
	method string
	// verb - HTTP 动词
	verb string
	// path - 管理面 HTTP 路径
	path string
}

// apisServerSpec - 平台登记表的一条登记项
type apisServerSpec struct {
	service    string
	method     string
	httpMethod string
	httpPath   string
	permission string
	risk       string
}

// key - 供对账使用的路由键（动词 + 空格 + 路径，与传输层 case 键同形）
func (this apisServerSpec) key() string { return this.httpMethod + " " + this.httpPath }

// readApisSource - 读取对账来源文件（不存在即 Fatal，调用方按需先行 Stat 判定跳过）
func readApisSource(t *testing.T, path string) string {

	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取对账来源失败（%s）：%v", path, err)
	}
	return string(raw)
}

// loadApisSDKRoutes - 解析 SDK 资源层的 typed 方法与其 HTTP 路由
func loadApisSDKRoutes(t *testing.T) []apisSDKRoute {

	t.Helper()
	source := readApisSource(t, apisSDKSourcePath)
	spans := apisSDKMethodPattern.FindAllStringSubmatchIndex(source, -1)
	if len(spans) == 0 {
		t.Fatalf("%s 未解析出任何 ApisResource 导出方法", apisSDKSourcePath)
	}
	routes := make([]apisSDKRoute, 0, len(spans))
	for index, span := range spans {
		name := source[span[2]:span[3]]
		end := len(source)
		if index+1 < len(spans) {
			end = spans[index+1][0]
		}
		matched := apisSDKRoutePattern.FindStringSubmatch(source[span[1]:end])
		if matched == nil {
			t.Errorf("SDK 方法 %s 未声明 HTTP 路由（应调用 client.get/post/put/del 或 exportBills）", name)
			continue
		}
		call, path := matched[1], matched[2]
		if call == "" {
			call, path = "exportBills", matched[3]
		}
		routes = append(routes, apisSDKRoute{method: name, verb: apisSDKVerbByCall[call], path: path})
	}
	return routes
}

// loadApisFullMethodConstants - 解析 apis.go 的 full method 常量块（常量名 → 完整 full method）
func loadApisFullMethodConstants(t *testing.T) map[string]string {

	t.Helper()
	source := readApisSource(t, apisSDKSourcePath)

	services := map[string]string{}
	for _, matched := range apisServiceConstantPattern.FindAllStringSubmatch(source, -1) {
		services[matched[1]] = matched[2]
	}
	if len(services) == 0 {
		t.Fatalf("%s 未解析出 gRPC 服务前缀常量", apisSDKSourcePath)
	}

	constants := map[string]string{}
	for _, matched := range apisFullMethodConstantPattern.FindAllStringSubmatch(source, -1) {
		name, prefix, method := matched[1], matched[2], matched[3]
		service, exists := services[prefix]
		if !exists {
			t.Errorf("full method 常量 %s 引用了未定义的服务前缀常量 %s", name, prefix)
			continue
		}
		if !strings.HasPrefix(service, apisServerPackage+".") {
			t.Errorf("full method 常量 %s 的服务前缀 %s 不在包 %s 下", name, service, apisServerPackage)
		}
		constants[name] = "/" + service + "/" + method
	}
	if len(constants) == 0 {
		t.Fatalf("%s 未解析出 full method 常量", apisSDKSourcePath)
	}
	return constants
}

// loadApisTransportCases - 解析传输层商城 case（"动词 路径" → full method 常量解析值）
func loadApisTransportCases(t *testing.T) map[string]string {

	t.Helper()
	source := readApisSource(t, apisTransportSourcePath)
	constants := loadApisFullMethodConstants(t)

	lines := strings.Split(source, "\n")
	cases := map[string]string{}
	for index, line := range lines {
		matched := apisTransportCasePattern.FindStringSubmatch(strings.TrimSpace(line))
		if matched == nil || !strings.HasPrefix(matched[2], "/api/apis-") {
			continue
		}
		if index+1 >= len(lines) {
			t.Fatalf("传输层 case %s 后缺少调用行", matched[2])
		}
		invoke := apisTransportInvokePattern.FindStringSubmatch(strings.TrimSpace(lines[index+1]))
		if invoke == nil {
			t.Fatalf("传输层 case %s 的下一行不是 invokeApis 调用：%s", matched[2], strings.TrimSpace(lines[index+1]))
		}
		value, exists := constants[invoke[1]]
		if !exists {
			t.Fatalf("传输层 case %s 引用了未定义的 full method 常量 %s", matched[2], invoke[1])
		}
		verb, exists := apisTransportVerbBySource[matched[1]]
		if !exists {
			t.Fatalf("传输层 case %s 使用了未登记的 HTTP 动词写法 %s（新增动词需同步对账表）", matched[2], matched[1])
		}
		key := verb + " " + matched[2]
		if _, duplicated := cases[key]; duplicated {
			t.Fatalf("传输层出现重复的商城 case：%s", key)
		}
		cases[key] = value
	}
	return cases
}

// loadApisServerSpecs - 解析平台登记表（仅 apisSpecs 字面量块）与包名常量
func loadApisServerSpecs(t *testing.T) ([]apisServerSpec, string) {

	t.Helper()
	source := readApisSource(t, apisServerRegistryPath)

	packages := apisServerPackagePattern.FindAllStringSubmatch(source, -1)
	if len(packages) != 1 {
		t.Fatalf("%s 应恰好声明一个 apisAdminPackage 常量，实际 %d 个", apisServerRegistryPath, len(packages))
	}
	if got := packages[0][1]; got != apisServerPackage {
		t.Fatalf("平台 proto-less 服务包名应为 %q，实际 %q（lichenhub 笔误已修正，勿回归）", apisServerPackage, got)
	}

	const blockStart = "var apisSpecs = []ApisSpec{"
	start := strings.Index(source, blockStart)
	if start < 0 {
		t.Fatalf("%s 未找到 apisSpecs 登记表", apisServerRegistryPath)
	}
	rest := source[start+len(blockStart):]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("%s 的 apisSpecs 登记表未正常结束", apisServerRegistryPath)
	}

	specs := []apisServerSpec{}
	for _, matched := range apisServerSpecPattern.FindAllStringSubmatch(rest[:end], -1) {
		risk := ""
		if len(matched) > 6 {
			risk = matched[6]
		}
		specs = append(specs, apisServerSpec{
			service: matched[1], method: matched[2], httpMethod: matched[3],
			httpPath: matched[4], permission: matched[5], risk: risk,
		})
	}
	if len(specs) == 0 {
		t.Fatalf("%s 未解析出任何 ApisSpec 登记项", apisServerRegistryPath)
	}
	return specs, source
}

// TestApisReconcileSDKSourceTransportAndCases - SDK 资源层 ↔ 传输层 ↔ 用例表三向一致
// （不依赖兄弟仓库；约束不变式：typed 方法数 = 传输 case 数 = 用例表条数 = 53）
func TestApisReconcileSDKSourceTransportAndCases(t *testing.T) {

	routes := loadApisSDKRoutes(t)
	cases := loadApisTransportCases(t)

	if len(routes) != apisRouteCount {
		t.Errorf("SDK 商城 typed 方法应为 %d 个，实际 %d 个", apisRouteCount, len(routes))
	}
	if len(cases) != apisRouteCount {
		t.Errorf("传输层商城 case 应为 %d 条，实际 %d 条", apisRouteCount, len(cases))
	}

	// SDK 资源层 → 传输层：每个方法的「动词 + 路径」必须有对应 case，且 full method 命名自洽
	for _, route := range routes {
		key := route.verb + " " + route.path
		fullMethod, exists := cases[key]
		if !exists {
			t.Errorf("SDK 方法 %s 的路由 %s 未在传输层登记 case", route.method, key)
			continue
		}
		if want := "/" + apisServerPackage + "."; !strings.HasPrefix(fullMethod, want) {
			t.Errorf("SDK 方法 %s 的 full method %q 应以 %q 开头", route.method, fullMethod, want)
		}
		if !strings.HasSuffix(fullMethod, "/"+route.method) {
			t.Errorf("SDK 方法 %s 的 full method %q 未以方法名结尾（命名不自洽）", route.method, fullMethod)
		}
	}

	// 传输层 → SDK 资源层：case 键必须指向真实存在的 SDK 路由（防孤儿 case / 路径笔误）
	sdk := map[string]string{}
	for _, route := range routes {
		sdk[route.verb+" "+route.path] = route.method
	}
	for key, fullMethod := range cases {
		if _, exists := sdk[key]; !exists {
			t.Errorf("传输层 case %s（%s）在 SDK 资源层找不到对应路由", key, fullMethod)
		}
	}

	// 用例表 → SDK 资源层：表必须与源码逐条对齐（防用例表漏项造成假绿）
	table := map[string]string{}
	for _, one := range apisRouteCases() {
		key := one.method + " " + one.path
		if previous, duplicated := table[key]; duplicated {
			t.Errorf("用例表出现重复路由 %s（用例 %s 与 %s）", key, previous, one.name)
		}
		table[key] = one.name
	}
	if len(table) != len(routes) {
		t.Errorf("用例表条数 %d 与 SDK 商城方法数 %d 不一致", len(table), len(routes))
	}
	for _, route := range routes {
		key := route.verb + " " + route.path
		if name, exists := table[key]; !exists {
			t.Errorf("SDK 方法 %s 的路由 %s 未进入用例表（覆盖缺口）", route.method, key)
		} else if name != route.method {
			t.Errorf("用例表路由 %s 对应方法 %s，SDK 源码为 %s", key, name, route.method)
		}
	}
}

// TestApisReconcileServerRegistry - SDK 三份事实 ↔ 平台 ApisSpecs 登记表逐条对账
// （兄弟仓库 licen-hub 未检出时跳过：本 SDK 可独立构建，但登记表对账是跨仓库唯一事实源校验）
func TestApisReconcileServerRegistry(t *testing.T) {

	if _, err := os.Stat(apisServerRegistryPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("兄弟仓库 licen-hub 未检出（%s 不存在），跳过与平台 ApisSpecs 登记表的对账", apisServerRegistryPath)
		}
		t.Fatalf("检查平台登记表失败：%v", err)
	}

	routes := loadApisSDKRoutes(t)
	cases := loadApisTransportCases(t)
	specs, _ := loadApisServerSpecs(t)

	if len(routes) != len(specs) {
		t.Fatalf("SDK 商城方法数 %d 与平台登记表条数 %d 不一致", len(routes), len(specs))
	}
	if len(cases) != len(specs) {
		t.Fatalf("传输层商城 case 数 %d 与平台登记表条数 %d 不一致", len(cases), len(specs))
	}

	byMethod := map[string]apisSDKRoute{}
	for _, route := range routes {
		byMethod[route.method] = route
	}
	registered := map[string]bool{}
	for _, spec := range specs {
		registered[spec.key()] = true

		// ① SDK 资源层：方法名逐字一致，动词与路径与登记表一致
		route, exists := byMethod[spec.method]
		if !exists {
			t.Errorf("平台登记 %s.%s/%s 在 SDK 商城资源层没有对应方法", spec.service, spec.method, spec.httpPath)
			continue
		}
		if route.verb != spec.httpMethod || route.path != spec.httpPath {
			t.Errorf("SDK 方法 %s 路由 = %s %s，平台登记为 %s %s",
				spec.method, route.verb, route.path, spec.httpMethod, spec.httpPath)
		}

		// ② 传输层：case 键与 full method 逐字一致（含服务名与包名）
		fullMethod, exists := cases[spec.key()]
		if !exists {
			t.Errorf("平台登记 %s 在 SDK 传输层没有对应 case", spec.key())
			continue
		}
		want := "/" + apisServerPackage + "." + spec.service + "/" + spec.method
		if fullMethod != want {
			t.Errorf("SDK 传输层 %s 的 full method = %q，平台登记为 %q", spec.key(), fullMethod, want)
		}
	}

	// ③ 反向覆盖：SDK 方法不得臆造（每个都必须能在登记表找到，且路由键一致）
	for _, route := range routes {
		key := route.verb + " " + route.path
		if !registered[key] {
			t.Errorf("SDK 方法 %s 的路由 %s 未在平台登记表登记（疑似臆造）", route.method, key)
		}
	}
	for key, fullMethod := range cases {
		if !registered[key] {
			t.Errorf("SDK 传输层 case %s（%s）未在平台登记表登记（疑似臆造）", key, fullMethod)
		}
	}

	// SDK 侧 full method 常量集合与登记表推导的集合应逐条相同（常量块无多余项）
	constants := loadApisFullMethodConstants(t)
	declared := map[string]string{}
	for name, value := range constants {
		declared[value] = name
	}
	expected := map[string]string{}
	for _, spec := range specs {
		expected["/"+apisServerPackage+"."+spec.service+"/"+spec.method] = spec.method
	}
	if len(declared) != len(expected) {
		t.Errorf("SDK full method 常量 %d 个，登记表推导 %d 个", len(declared), len(expected))
	}
	for fullMethod := range expected {
		if _, exists := declared[fullMethod]; !exists {
			t.Errorf("登记表 full method %s 在 SDK 常量块中缺失", fullMethod)
		}
	}
}

// TestApisReconcileRiskAndPermission - 平台登记表的权限码与风险级别在 SDK 侧有对应文档表征：
// 每条登记项的权限码必须出现在 SDK 的 ApisResource 文档注记中（高风险写路径另行标注），
// 防止 SDK 文档与平台稳定权限码漂移。
func TestApisReconcileRiskAndPermission(t *testing.T) {

	if _, err := os.Stat(apisServerRegistryPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("兄弟仓库 licen-hub 未检出（%s 不存在），跳过权限码文档对账", apisServerRegistryPath)
		}
		t.Fatalf("检查平台登记表失败：%v", err)
	}

	specs, _ := loadApisServerSpecs(t)
	source := readApisSource(t, apisSDKSourcePath)

	permissions := map[string]bool{}
	for _, spec := range specs {
		permissions[spec.permission] = true
	}
	missing := []string{}
	for permission := range permissions {
		if !strings.Contains(source, permission) {
			missing = append(missing, permission)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("以下平台权限码未在 %s 的商城方法注释中记录：%v", apisSDKSourcePath, missing)
	}

	for _, spec := range specs {
		if spec.risk != "high" {
			continue
		}
		if !strings.Contains(source, "权限码 "+spec.permission+"（风险级别 high") {
			t.Errorf("高风险路由 %s（%s）未在 SDK 注释中标注风险级别", spec.method, spec.permission)
		}
	}
}
