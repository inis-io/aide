package admin

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// API 商城管理面对账自检（路由对账 + 形态对账）。
//
// 一、三向路由对账：三份事实必须逐条一致，任一方向漂移即测试失败：
//  1. SDK 资源层 `admin/apis.go`：每个 typed 方法的 HTTP 动词 + 路径（从源码解析，非人工清单）；
//  2. SDK 传输层 `admin/admin-transport-grpc.go`：`"动词 路径"` case 键引导的 full method 常量
//     （常量值再回到 apis.go 的常量块解析 `"/" + <服务前缀> + "/<方法>"`）；
//  3. 平台唯一事实源 licen-hub `backend/grpc/admin/v1/apis.go` 的 `ApisSpecs` 登记表
//     （Service/Method/HTTPMethod/HTTPPath 逐字）；兄弟仓库未检出时该向断言跳过（t.Skipf）。
//
// 二、形态对账（防止「路由对、形态错」漏网）：
//  4. 路由 → 平台返回元素类型：从平台 gRPC 薄壳的处理器体 + 服务层方法签名机械推导每条路由的
//     载荷形态（page / one / export）与元素类型，与 SDK 源码声明的返回形态逐条比对；
//  5. DTO 逐字段对账：SDK 类型（`apis-types.go` / `admin-types.go`）与平台出处类型
//     （`models/basic/apis-*.go`、`service/apis/*.go`、登记表的信封类型）按显式映射表
//     比对字段名、顺序、json tag 与 Go 类型（`soft_delete.DeletedAt ↔ int64` 是已声明的唯一例外）。
//
// 反向覆盖：SDK 与传输层里多出来的商城方法/路由必须能在平台登记表找到（防臆造路由）；
// 用例表（apis_test.go 的 apisRouteCases）也必须与 SDK 源码逐条对齐（防表漏项造成假绿）。

const (
	// apisSDKSourcePath - SDK 资源层源码（typed 方法 ↔ HTTP 路由）
	apisSDKSourcePath = "apis.go"
	// apisSDKTypesPath - SDK 商城 DTO 源码（形态对账的 SDK 侧事实源）
	apisSDKTypesPath = "apis-types.go"
	// apisSDKSharedTypesPath - SDK 共用结果类型源码（IdResult 等跨资源组 DTO）
	apisSDKSharedTypesPath = "admin-types.go"
	// apisTransportSourcePath - SDK gRPC 传输层源码（HTTP 路由 ↔ full method 常量）
	apisTransportSourcePath = "admin-transport-grpc.go"
	// apisSiblingRepoPath - 兄弟仓库 licen-hub 根目录（平台侧事实源；未检出时相关断言跳过）
	apisSiblingRepoPath = "../../../licen-hub"
	// apisServerRegistryPath - 平台登记表源码（兄弟仓库 licen-hub；未检出时跳过该向断言）
	apisServerRegistryPath = apisSiblingRepoPath + "/backend/grpc/admin/v1/apis.go"
	// apisServerServiceDir - 平台业务层源码目录（服务层方法签名与白名单视图的出处）
	apisServerServiceDir = apisSiblingRepoPath + "/backend/app/service/apis"
	// apisServerPackage - 平台 proto-less 服务的权威包名（曾出现 lichenhub 笔误，此处回归守护）
	apisServerPackage = "licenhub.licence.v1"
	// apisRouteCount - 商城管理面受控路由条数（与平台登记表条数互为断言）
	apisRouteCount = 60
)

var (
	// apisSDKMethodPattern - SDK 资源层方法签名（只取导出方法；unexported 助手 exportBills 不参与对账）
	apisSDKMethodPattern = regexp.MustCompile(`(?m)^func \(this \*ApisResource\) ([A-Z]\w*)\(`)
	// apisSDKRoutePattern - 方法体内的路由声明：client.get/getWithQuery/postMultipart/post/put/del(ctx, "路径")
	// 或共用助手 this.exportBills(ctx, "路径")（助手内部固定走 GET）
	apisSDKRoutePattern = regexp.MustCompile(`this\.client\.(getWithQuery|get|postMultipart|post|put|del)\(ctx, "([^"]+)"|this\.exportBills\(ctx, "([^"]+)"`)
	// apisSDKResultPattern - SDK 方法签名的返回列表（跨行签名由 (?s) 与非贪婪参数段覆盖）；
	// 两种形态：带括号的 `(*T, error)` 落在捕获组 2，上游 void 方法的裸 `error` 落在捕获组 3
	apisSDKResultPattern = regexp.MustCompile(`(?s)^func \(this \*ApisResource\) (\w+)\(.*?\) (?:\(([^)]*)\)|(\w+)) \{`)
	// apisSDKVerbByCall - 方法名 → HTTP 动词
	apisSDKVerbByCall = map[string]string{
		"getWithQuery": http.MethodGet, "get": http.MethodGet, "postMultipart": http.MethodPost,
		"post": http.MethodPost, "put": http.MethodPut, "del": http.MethodDelete, "exportBills": http.MethodGet,
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
	// apisTransportUploadPathPattern / apisTransportUploadInvokePattern - Upload() 里商城 proto-less 上传的
	// early branch（`if upload.Path == "<路径>" {` 的下一行必须是 uploadApis 调用；upstream-data 不经 RoundTrip）
	apisTransportUploadPathPattern   = regexp.MustCompile(`^if upload\.Path == "(/api/apis-[^"]+)" \{$`)
	apisTransportUploadInvokePattern = regexp.MustCompile(`^response, err := this\.uploadApis\(callCtx, (\w+), upload\)$`)
	// apisServiceConstantPattern - apis.go 的 gRPC 服务前缀常量
	apisServiceConstantPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"(licenhub\.licence\.v1\.\w+)"\s*$`)
	// apisFullMethodConstantPattern - apis.go 的 full method 常量（"/" + <服务前缀常量> + "/<方法>"）
	apisFullMethodConstantPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"/"\s*\+\s*(\w+)\s*\+\s*"/(\w+)"\s*$`)
	// apisServerPackagePattern - 平台登记表的包名常量（proto-less 服务与既有 proto 服务同包）
	apisServerPackagePattern = regexp.MustCompile(`(?m)^const apisAdminPackage = "([^"]+)"\r?$`)
	// apisServerSpecPattern - 平台登记表条目（Risk 为空时字段省略；handler 供形态对账定位处理器体）
	apisServerSpecPattern = regexp.MustCompile(`\{Service: "(\w+)", Method: "(\w+)", HTTPMethod: "(\w+)", HTTPPath: "([^"]+)", Permission: "([^"]+)"(?:, Risk: "(\w+)")?, handler: (\w+)\}`)
	// apisServerServiceMethodPattern - 平台业务层方法定义（逐函数截取签名；用 (?s) 直接匹配整个签名会被
	// 「无返回值」的签名（如 `RemoveProduct(...) error {`）吞掉后续函数，故按函数边界截取后单独解析返回列表）
	apisServerServiceMethodPattern = regexp.MustCompile(`(?m)^func \(this \*Service\) (\w+)\(`)
	// apisServerInstanceCallPattern - 平台 gRPC 薄壳内的服务层调用（形态对账据此回溯服务层返回类型）
	apisServerInstanceCallPattern = regexp.MustCompile(`apis\.Instance\.(\w+)\(`)
	// apisServerEnvelopeLiteralPattern - 平台薄壳直接构造的信封（id / monthSpent / 账单导出）
	apisServerEnvelopeLiteralPattern = regexp.MustCompile(`apisOK\((apisIdEnvelope|apisSpentEnvelope|apisBillExportEnvelope)\{`)
	// apisTopFieldPattern - 结构体**顶层**字段行（去掉一层缩进后匹配：字段名 / 类型 / json tag 名）
	apisTopFieldPattern = regexp.MustCompile("^([A-Z][A-Za-z0-9]*)\\s+([^\\s`]+)\\s+`[^`]*json:\"([^\",]+)[^\"]*\"[^`]*`$")
	// apisInlineStructOpenPattern / apisInlineStructClosePattern - 内联匿名结构体字段
	// （`Totals struct {` … `} `json:"totals"``）：单行正则表达不了其类型，单独识别并记为 struct，
	// 其内部字段由调用方按 SDK 具名类型单独比对（见 TestApisReconcileTypeShapes 的 totals 段）。
	apisInlineStructOpenPattern  = regexp.MustCompile(`^([A-Z][A-Za-z0-9]*) struct \{$`)
	apisInlineStructClosePattern = regexp.MustCompile("^\\t\\} `json:\"([^\",]+)\"`$")
	// apisAnyIndentFieldPattern - 任意缩进的字段行（仅用于已限定范围的内联结构体体）
	apisAnyIndentFieldPattern = regexp.MustCompile("(?m)^[ \\t]*([A-Z][A-Za-z0-9]*)\\s+([^\\s`]+)\\s+`[^`]*json:\"([^\",]+)[^\"]*\"[^`]*`\\s*$")
	// apisPlatformTypeAliases - 平台类型名 → SDK 类型名（形态对账的显式映射，命名差异都是有意的）；
	// `soft_delete.DeletedAt ↔ int64` 是唯一的类型等价例外（平台软删列在 SDK 侧按毫秒整数暴露）。
	apisPlatformTypeAliases = map[string]string{
		"OfferPlan": "ApisOfferPlan", "OfferPlanItem": "ApisOfferPlanItem", "PlanView": "ApisPlanView",
		"BalanceState": "ApisBalanceState", "RechargeResult": "ApisRechargeResult",
		"UsageDailySummary":      "ApisUsageDailySummary",
		"UsageDailySummaryDay":   "ApisUsageDailySummaryDay",
		"UsageDailySummaryGroup": "ApisUsageDailySummaryGroup",
		"PlanItemParams":         "ApisPlanItemInput",
		"UpstreamStatus":         "ApisUpstreamStatus",
		"UpstreamKeyStatus":      "ApisUpstreamKeyStatus",
		"UpstreamDataStatus":     "ApisUpstreamDataStatus",
		"UpstreamCacheStatus":    "ApisUpstreamCacheStatus",
		"UpstreamKeyParams":      "ApisUpstreamKeyInput",
		"UpstreamVerifyParams":   "ApisUpstreamVerifyInput",
		"UpstreamCacheParams":    "ApisUpstreamCacheInput",
		"UpstreamOptionsParams":  "ApisUpstreamOptionsInput",
		"apisIdEnvelope":         "IdResult", "apisSpentEnvelope": "ApisBalanceSpentResult",
		"soft_delete.DeletedAt": "int64",
	}
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
	// handler - 平台 gRPC 薄壳处理器函数名（形态对账据此定位处理器体）
	handler string
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

// loadApisTransportCases - 解析传输层商城 case（"动词 路径" → full method 常量解析值）：
// RoundTrip 的 `case 动词 + " 路径":` 与 Upload() 的商城 early branch（uploadApis 调用）两路并入同一表
func loadApisTransportCases(t *testing.T) map[string]string {

	t.Helper()
	source := readApisSource(t, apisTransportSourcePath)
	constants := loadApisFullMethodConstants(t)

	lines := strings.Split(source, "\n")
	cases := map[string]string{}
	// register - 登记一条商城 case（RoundTrip 与 Upload early branch 共用去重口径）
	register := func(key, constantValue string) {
		if _, duplicated := cases[key]; duplicated {
			t.Fatalf("传输层出现重复的商城 case：%s", key)
		}
		cases[key] = constantValue
	}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)

		// ① RoundTrip 的商城 case（"动词 路径" → invokeApis）
		if matched := apisTransportCasePattern.FindStringSubmatch(trimmed); matched != nil && strings.HasPrefix(matched[2], "/api/apis-") {
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
			register(verb+" "+matched[2], value)
			continue
		}

		// ② Upload() 的商城 early branch（proto-less 上传不经 RoundTrip）：
		// `if upload.Path == "<路径>" {` 的下一行必须是 uploadApis 调用，键统一为 "POST <路径>"
		if matched := apisTransportUploadPathPattern.FindStringSubmatch(trimmed); matched != nil {
			if index+1 >= len(lines) {
				t.Fatalf("传输层 Upload early branch %s 后缺少调用行", matched[1])
			}
			invoke := apisTransportUploadInvokePattern.FindStringSubmatch(strings.TrimSpace(lines[index+1]))
			if invoke == nil {
				t.Fatalf("传输层 Upload early branch %s 的下一行不是 uploadApis 调用：%s", matched[1], strings.TrimSpace(lines[index+1]))
			}
			value, exists := constants[invoke[1]]
			if !exists {
				t.Fatalf("传输层 Upload early branch %s 引用了未定义的 full method 常量 %s", matched[1], invoke[1])
			}
			register(http.MethodPost+" "+matched[1], value)
		}
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
		handler := ""
		if len(matched) > 7 {
			handler = matched[7]
		}
		specs = append(specs, apisServerSpec{
			service: matched[1], method: matched[2], httpMethod: matched[3],
			httpPath: matched[4], permission: matched[5], risk: risk, handler: handler,
		})
	}
	if len(specs) == 0 {
		t.Fatalf("%s 未解析出任何 ApisSpec 登记项（登记项格式可能已变化：字段顺序/字段增删都会导致 0 条）", apisServerRegistryPath)
	}
	return specs, source
}

// apisRequireSiblingRepo - 平台侧事实源（兄弟仓库 licen-hub）缺失时跳过依赖它的断言：
// 本 SDK 可独立构建，但登记表与形态对账是跨仓库唯一事实源校验，仓库检出即必须真跑（不允许静默降级）。
func apisRequireSiblingRepo(t *testing.T) {

	t.Helper()
	if _, err := os.Stat(apisSiblingRepoPath); err != nil {
		if os.IsNotExist(err) {
			t.Skipf("兄弟仓库 licen-hub 未检出（%s 不存在），跳过平台侧对账断言", apisSiblingRepoPath)
		}
		t.Fatalf("检查兄弟仓库 licen-hub 失败：%v", err)
	}
}

// TestApisReconcileSDKSourceTransportAndCases - SDK 资源层 ↔ 传输层 ↔ 用例表三向一致
// （不依赖兄弟仓库；约束不变式：typed 方法数 = 传输 case 数 = 用例表条数 = 60）
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

	apisRequireSiblingRepo(t)

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

	apisRequireSiblingRepo(t)

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

// ============================= 形态对账（返回形态 + DTO 逐字段） =============================

// apisSDKResult - SDK typed 方法的返回形态（kind：page / one / export）
type apisSDKResult struct {
	kind    string
	element string
}

// apisStructField - 结构体字段（名字 / Go 类型 / json tag 名）
type apisStructField struct {
	name   string
	goType string
	tag    string
}

// apisShapeFile - 形态对账条目：SDK 类型 ↔ 平台类型（+ 平台文件，相对 licen-hub 根）
type apisShapeFile struct {
	sdkType    string
	serverType string
	serverFile string
}

// apisShapeFiles - SDK 商城 DTO ↔ 平台出处（输出对齐 models/basic、白名单视图与结果/入参对齐 service/apis、
// 信封类结果对齐登记表）。新增 DTO 必须在此登记，否则不进形态对账。
var apisShapeFiles = []apisShapeFile{
	// 输出：平台模型
	{sdkType: "ApisProduct", serverType: "ApisProduct", serverFile: "backend/app/models/basic/apis-catalog.go"},
	{sdkType: "ApisPlan", serverType: "ApisPlan", serverFile: "backend/app/models/basic/apis-catalog.go"},
	{sdkType: "ApisOrder", serverType: "ApisOrder", serverFile: "backend/app/models/basic/apis-billing.go"},
	{sdkType: "ApisSubscription", serverType: "ApisSubscription", serverFile: "backend/app/models/basic/apis-billing.go"},
	{sdkType: "ApisBill", serverType: "ApisBill", serverFile: "backend/app/models/basic/apis-billing.go"},
	{sdkType: "ApisBalanceAccount", serverType: "ApisBalanceAccount", serverFile: "backend/app/models/basic/apis-balance.go"},
	{sdkType: "ApisBalanceLog", serverType: "ApisBalanceLog", serverFile: "backend/app/models/basic/apis-balance.go"},
	{sdkType: "ApisRecharge", serverType: "ApisRecharge", serverFile: "backend/app/models/basic/apis-balance.go"},
	{sdkType: "ApisUsageRecord", serverType: "ApisUsageRecord", serverFile: "backend/app/models/basic/apis-usage.go"},
	{sdkType: "ApisUsageDaily", serverType: "ApisUsageDaily", serverFile: "backend/app/models/basic/apis-usage.go"},

	// 输出：商品浏览白名单视图（套餐中心：套餐字段 + 产品明细）
	{sdkType: "ApisOfferPlan", serverType: "OfferPlan", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisOfferPlanItem", serverType: "OfferPlanItem", serverFile: "backend/app/service/apis/catalog.go"},

	// 输出：平台管理视图与套餐明细模型
	{sdkType: "ApisPlanItem", serverType: "ApisPlanItem", serverFile: "backend/app/models/basic/apis-catalog.go"},
	{sdkType: "ApisPlanView", serverType: "PlanView", serverFile: "backend/app/service/apis/catalog.go"},

	// 输出：能力上游状态族
	{sdkType: "ApisUpstreamStatus", serverType: "UpstreamStatus", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamKeyStatus", serverType: "UpstreamKeyStatus", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamDataStatus", serverType: "UpstreamDataStatus", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamCacheStatus", serverType: "UpstreamCacheStatus", serverFile: "backend/app/service/apis/upstream.go"},

	// 输出：操作结果与看板
	{sdkType: "ApisBalanceState", serverType: "BalanceState", serverFile: "backend/app/service/apis/balance.go"},
	{sdkType: "ApisRechargeResult", serverType: "RechargeResult", serverFile: "backend/app/service/apis/recharge.go"},
	{sdkType: "ApisBalanceSpentResult", serverType: "apisSpentEnvelope", serverFile: "backend/grpc/admin/v1/apis.go"},
	{sdkType: "IdResult", serverType: "apisIdEnvelope", serverFile: "backend/grpc/admin/v1/apis.go"},
	{sdkType: "apisBillExportEnvelope", serverType: "apisBillExportEnvelope", serverFile: "backend/grpc/admin/v1/apis.go"},
	{sdkType: "ApisUsageDailySummary", serverType: "UsageDailySummary", serverFile: "backend/app/service/apis/usage-summary.go"},
	{sdkType: "ApisUsageDailySummaryDay", serverType: "UsageDailySummaryDay", serverFile: "backend/app/service/apis/usage-summary.go"},
	{sdkType: "ApisUsageDailySummaryGroup", serverType: "UsageDailySummaryGroup", serverFile: "backend/app/service/apis/usage-summary.go"},

	// 输入：写路径入参
	{sdkType: "ApisProductInput", serverType: "ProductParams", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisPlanInput", serverType: "PlanParams", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisPlanItemInput", serverType: "PlanItemParams", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisUpstreamKeyInput", serverType: "UpstreamKeyParams", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamVerifyInput", serverType: "UpstreamVerifyParams", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamCacheInput", serverType: "UpstreamCacheParams", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisUpstreamOptionsInput", serverType: "UpstreamOptionsParams", serverFile: "backend/app/service/apis/upstream.go"},
	{sdkType: "ApisOrderInput", serverType: "OrderParams", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisOrderTarget", serverType: "OrderPayParams", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisOrderCancelInput", serverType: "OrderCancelParams", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisOrderConfirmInput", serverType: "OrderConfirmParams", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisRefundApplyInput", serverType: "RefundApplyParams", serverFile: "backend/app/service/apis/refund.go"},
	{sdkType: "ApisRefundReviewInput", serverType: "RefundReviewParams", serverFile: "backend/app/service/apis/refund.go"},
	{sdkType: "ApisRechargeInput", serverType: "RechargeParams", serverFile: "backend/app/service/apis/recharge.go"},
	{sdkType: "ApisRechargeConfirmInput", serverType: "RechargeConfirmParams", serverFile: "backend/app/service/apis/recharge.go"},
	{sdkType: "ApisBalanceAdjustInput", serverType: "AdjustParams", serverFile: "backend/app/service/apis/balance.go"},

	// 输入：读路径筛选
	{sdkType: "ApisProductQuery", serverType: "ProductQuery", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisPlanQuery", serverType: "PlanQuery", serverFile: "backend/app/service/apis/catalog.go"},
	{sdkType: "ApisOrderQuery", serverType: "OrderQuery", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisBillQuery", serverType: "BillQuery", serverFile: "backend/app/service/apis/order.go"},
	{sdkType: "ApisSubscriptionQuery", serverType: "SubscriptionQuery", serverFile: "backend/app/service/apis/subscription.go"},
	{sdkType: "ApisRechargeQuery", serverType: "RechargeQuery", serverFile: "backend/app/service/apis/recharge.go"},
	{sdkType: "ApisBalanceLogQuery", serverType: "BalanceLogQuery", serverFile: "backend/app/service/apis/balance.go"},
	{sdkType: "ApisAccountQuery", serverType: "AccountQuery", serverFile: "backend/app/service/apis/balance.go"},
	{sdkType: "ApisUsageRecordQuery", serverType: "UsageRecordQuery", serverFile: "backend/app/service/apis/usage.go"},
	{sdkType: "ApisUsageDailyQuery", serverType: "UsageDailyQuery", serverFile: "backend/app/service/apis/usage.go"},
	{sdkType: "ApisUsageDailySummaryQuery", serverType: "UsageDailySummaryQuery", serverFile: "backend/app/service/apis/usage-summary.go"},
}

// apisNormalizeType - 归一 Go 类型用于跨仓库比对：去指针/切片修饰前缀、去 `BasicModel.` 包限定、
// 套用 apisPlatformTypeAliases 的显式命名映射（两侧都套，映射键只命中平台侧命名）。
func apisNormalizeType(value string) string {

	normalized := strings.TrimSpace(value)
	prefix := ""
	for {
		if strings.HasPrefix(normalized, "*") {
			prefix, normalized = prefix+"*", strings.TrimPrefix(normalized, "*")
			continue
		}
		if strings.HasPrefix(normalized, "[]") {
			prefix, normalized = prefix+"[]", strings.TrimPrefix(normalized, "[]")
			continue
		}
		break
	}
	normalized = strings.TrimPrefix(normalized, "BasicModel.")
	if mapped, exists := apisPlatformTypeAliases[normalized]; exists {
		normalized = mapped
	}
	return prefix + normalized
}

// apisSDKResultKind - SDK 返回列表 → 形态与元素类型（`*Page[T]` / `*T` / 导出三元组 /
// 上游 void 方法的裸 error / SetUpstreamOptions 的 map[string]any）
func apisSDKResultKind(returns string) (string, string) {

	trimmed := strings.TrimSpace(returns)
	if trimmed == "error" {
		// 上游 void 方法只返回裸 error（平台 apisOK(nil,...) 空数据）
		return "void", ""
	}
	// error 是管理面方法的固定尾返回，形态由剩余返回列表决定
	trimmed = strings.TrimSuffix(trimmed, ", error")
	if trimmed == "map[string]any" {
		return "one", "map[string]any"
	}
	if strings.HasPrefix(trimmed, "*Page[") && strings.HasSuffix(trimmed, "]") {
		return "page", strings.TrimSuffix(strings.TrimPrefix(trimmed, "*Page["), "]")
	}
	if strings.HasPrefix(trimmed, "*") {
		return "one", strings.TrimPrefix(trimmed, "*")
	}
	if trimmed == "string, []byte" {
		// 两条导出方法的返回是 (fileName, xlsx 原始字节, error)，元素类型不适用于此形态
		return "export", ""
	}
	return "unknown", trimmed
}

// loadApisSDKResults - 解析 SDK 资源层每个 typed 方法的返回形态
func loadApisSDKResults(t *testing.T) map[string]apisSDKResult {

	t.Helper()
	source := readApisSource(t, apisSDKSourcePath)
	spans := apisSDKMethodPattern.FindAllStringSubmatchIndex(source, -1)
	results := make(map[string]apisSDKResult, len(spans))
	for index, span := range spans {
		name := source[span[2]:span[3]]
		end := len(source)
		if index+1 < len(spans) {
			end = spans[index+1][0]
		}
		matched := apisSDKResultPattern.FindStringSubmatch(source[span[0]:end])
		if matched == nil {
			t.Errorf("SDK 方法 %s 的返回列表无法解析（签名格式可能已变化）", name)
			continue
		}
		returns := matched[2]
		if returns == "" {
			// 裸 error 返回（上游 void 方法）落在捕获组 3
			returns = matched[3]
		}
		kind, element := apisSDKResultKind(returns)
		results[name] = apisSDKResult{kind: kind, element: element}
	}
	return results
}

// loadApisServiceReturns - 解析平台业务层全部方法签名的返回类型列表（方法名 → 返回列表原文）。
// 逐函数截取签名（到第一个 " {\n" 为止），再取最后一个 ") (" 之后的返回列表：
// 无返回值的方法不登记（其载荷必然是薄壳自造信封，不参与元素类型推导）。
func loadApisServiceReturns(t *testing.T) map[string]string {

	t.Helper()
	entries, err := os.ReadDir(apisServerServiceDir)
	if err != nil {
		t.Fatalf("读取平台业务层目录失败（%s）：%v", apisServerServiceDir, err)
	}
	returns := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source := readApisSource(t, filepath.Join(apisServerServiceDir, name))
		spans := apisServerServiceMethodPattern.FindAllStringSubmatchIndex(source, -1)
		for index, span := range spans {
			method := source[span[2]:span[3]]
			end := len(source)
			if index+1 < len(spans) {
				end = spans[index+1][0]
			}
			chunk := source[span[0]:end]
			brace := apisFuncBodyIndex(chunk)
			if brace < 0 {
				t.Fatalf("%s 的方法 %s 未找到函数体起始（签名格式可能已变化）", name, method)
			}
			signature := strings.TrimSpace(chunk[:brace])
			close := strings.LastIndex(signature, ") (")
			if close < 0 {
				continue
			}
			returns[method] = strings.TrimSuffix(strings.TrimSpace(signature[close+3:]), ")")
		}
	}
	if len(returns) == 0 {
		t.Fatalf("%s 未解析出任何业务层方法签名（签名格式可能已变化）", apisServerServiceDir)
	}
	return returns
}

// apisHandlerBody - 截取平台 gRPC 薄壳处理器体（登记表 handler 字段指向的函数）
func apisHandlerBody(t *testing.T, source, handler string) string {

	t.Helper()
	start := strings.Index(source, "func "+handler+"(")
	if start < 0 {
		t.Fatalf("平台登记表的处理器 %s 未找到（handler 命名或签名格式可能已变化）", handler)
	}
	rest := source[start:]
	brace := apisFuncBodyIndex(rest)
	if brace < 0 {
		t.Fatalf("平台处理器 %s 的函数体未找到（格式可能已变化）", handler)
	}
	lineEnd := strings.Index(rest[brace:], "\n")
	if lineEnd < 0 {
		t.Fatalf("平台处理器 %s 的函数体未正常开始（格式可能已变化）", handler)
	}
	body := rest[brace+lineEnd+1:]
	end := strings.Index(body, "\n}")
	if end < 0 {
		t.Fatalf("平台处理器 %s 的函数体未正常结束（格式可能已变化）", handler)
	}
	return body[:end]
}

// apisPlatformPayload - 平台处理器的载荷形态与元素类型：
// `apisOK(nil,...)` → 空数据（上游 void 方法）；信封字面量（id/monthSpent/导出）→ 字面量类型；
// `apisOK(apisPage(...))` → 分页（元素取服务层首个返回类型）；其余单对象 → 服务层首个返回类型。
// 全部机械推导，元素类型再经 apisNormalizeType 映射到 SDK 命名。
func apisPlatformPayload(t *testing.T, body string, spec apisServerSpec, returns map[string]string) (string, string) {

	t.Helper()
	if strings.Contains(body, "apisOK(nil,") {
		// 平台空数据响应（上游 Set/Clear/Verify 四条），SDK 侧对应裸 error 返回
		return "void", ""
	}
	if matched := apisServerEnvelopeLiteralPattern.FindStringSubmatch(body); matched != nil {
		if matched[1] == "apisBillExportEnvelope" {
			return "export", matched[1]
		}
		return "one", apisNormalizeType(matched[1])
	}
	element := apisServiceElement(t, body, spec, returns)
	if strings.Contains(body, "apisOK(apisPage(") {
		return "page", element
	}
	return "one", element
}

// apisServiceElement - 处理器内的服务层调用 → 服务层首个返回类型（去 []/* 与包限定）
func apisServiceElement(t *testing.T, body string, spec apisServerSpec, returns map[string]string) string {

	t.Helper()
	matched := apisServerInstanceCallPattern.FindStringSubmatch(body)
	if matched == nil {
		t.Fatalf("平台处理器 %s（%s）未找到服务层调用（处理器格式可能已变化）", spec.handler, spec.method)
	}
	list, exists := returns[matched[1]]
	if !exists {
		t.Fatalf("平台业务层未解析到方法 %s 的签名（%s，形态对账无法推导元素类型）", matched[1], spec.method)
	}
	return apisNormalizeElementType(strings.Split(list, ",")[0])
}

// apisStructBody - 截取 Go 源码中指定类型的结构体体（类型必须定义在给定源码中）
func apisStructBody(t *testing.T, source, name, file string) string {

	t.Helper()
	marker := "type " + name + " struct {"
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("%s 未找到类型 %s（结构体清单可能已变化）", file, name)
	}
	rest := source[start+len(marker):]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatalf("%s 的类型 %s 未正常结束（结构体格式可能已变化）", file, name)
	}
	return rest[:end]
}

// apisParseFields - 解析结构体字段（字段名 / 类型 / json tag），允许任意缩进；
// 仅用于已限定范围的内联结构体体（顶层结构体请用 apisStructFields）。
func apisParseFields(body string) []apisStructField {

	fields := []apisStructField{}
	for _, matched := range apisAnyIndentFieldPattern.FindAllStringSubmatch(body, -1) {
		fields = append(fields, apisStructField{name: matched[1], goType: matched[2], tag: matched[3]})
	}
	return fields
}

// apisStructFields - 指定类型的顶层字段列表（保持源码顺序）；
// 内联匿名结构体字段记为 goType = "struct"（其字段由调用方另行比对）。
func apisStructFields(t *testing.T, source, name, file string) []apisStructField {

	t.Helper()
	body := apisStructBody(t, source, name, file)
	fields := []apisStructField{}
	lines := strings.Split(body, "\n")
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSuffix(lines[index], "\r")
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		trimmed := strings.TrimPrefix(line, "\t")
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if matched := apisTopFieldPattern.FindStringSubmatch(trimmed); matched != nil {
			fields = append(fields, apisStructField{name: matched[1], goType: matched[2], tag: matched[3]})
			continue
		}
		if matched := apisInlineStructOpenPattern.FindStringSubmatch(trimmed); matched != nil {
			tag := ""
			for scan := index + 1; scan < len(lines); scan++ {
				if closed := apisInlineStructClosePattern.FindStringSubmatch(strings.TrimSuffix(lines[scan], "\r")); closed != nil {
					tag, index = closed[1], scan
					break
				}
			}
			if tag == "" {
				t.Fatalf("%s 的类型 %s 内联字段 %s 的 json tag 未找到（结构体格式可能已变化）", file, name, matched[1])
			}
			fields = append(fields, apisStructField{name: matched[1], goType: "struct", tag: tag})
		}
	}
	return fields
}

// apisFuncBodyIndex - 定位函数体起始花括号（兼容 LF 与 CRLF 文件：licen-hub 内两种行尾并存）
func apisFuncBodyIndex(text string) int {

	best := -1
	for _, marker := range []string{" {\n", " {\r\n"} {
		position := strings.Index(text, marker)
		if position >= 0 && (best < 0 || position < best) {
			best = position
		}
	}
	if best < 0 {
		return -1
	}
	return best + 1
}

// apisNormalizeElementType - 归一**元素类型**（去掉 * 与 [] 修饰后套用命名映射），
// 与 apisNormalizeType 的区别：后者保留修饰符用于字段级比对，前者用于「页元素/返回元素」推导。
func apisNormalizeElementType(value string) string {

	normalized := strings.TrimSpace(value)
	for {
		switch {
		case strings.HasPrefix(normalized, "*"):
			normalized = strings.TrimPrefix(normalized, "*")
			continue
		case strings.HasPrefix(normalized, "[]"):
			normalized = strings.TrimPrefix(normalized, "[]")
			continue
		}
		break
	}
	return apisNormalizeType(normalized)
}

// TestApisReconcileRouteElementTypes - 「路由 → 平台返回元素类型」对账：
// 从平台登记表的处理器体（`apisOK(...)` 载荷形态）+ 服务层方法签名机械推导每条路由的返回形态与元素类型，
// 与 SDK 源码声明的返回形态逐条比对。B1 这类「路由对、元素类型错（扁平 vs 嵌套）」缺陷由此当场见红。
func TestApisReconcileRouteElementTypes(t *testing.T) {

	apisRequireSiblingRepo(t)

	specs, registry := loadApisServerSpecs(t)
	serviceReturns := loadApisServiceReturns(t)
	sdkResults := loadApisSDKResults(t)

	if len(specs) != apisRouteCount {
		t.Fatalf("平台登记表条数 %d，期望 %d", len(specs), apisRouteCount)
	}
	if len(sdkResults) != apisRouteCount {
		t.Fatalf("SDK 返回形态解析条数 %d，期望 %d", len(sdkResults), apisRouteCount)
	}

	for _, spec := range specs {
		body := apisHandlerBody(t, registry, spec.handler)
		kind, element := apisPlatformPayload(t, body, spec, serviceReturns)

		wantElement := element
		if kind == "export" {
			wantElement = ""
		}
		got, exists := sdkResults[spec.method]
		if !exists {
			t.Errorf("平台登记 %s.%s/%s 在 SDK 资源层没有对应方法", spec.service, spec.method, spec.httpPath)
			continue
		}
		if got.kind != kind || got.element != wantElement {
			t.Errorf("路由 %s %s（%s）的 SDK 返回形态 = %s %s，平台为 %s %s【平台载荷元素 %s，处理器 %s】",
				spec.httpMethod, spec.httpPath, spec.method, got.kind, got.element, kind, wantElement, element, spec.handler)
		}
	}
}

// TestApisReconcileTypeShapes - SDK 商城 DTO ↔ 平台出处类型逐字段对账（字段名 / 顺序 / json tag / Go 类型）：
// 兄弟仓库未检出时跳过；新增 DTO 未登记进 apisShapeFiles 不会被自动覆盖（登记表即口径）。
func TestApisReconcileTypeShapes(t *testing.T) {

	apisRequireSiblingRepo(t)

	sdkSource := readApisSource(t, apisSDKTypesPath) + "\n" + readApisSource(t, apisSDKSharedTypesPath)
	serverCache := map[string]string{}

	for _, one := range apisShapeFiles {
		serverSource, cached := serverCache[one.serverFile]
		if !cached {
			serverSource = readApisSource(t, filepath.Join(apisSiblingRepoPath, one.serverFile))
			serverCache[one.serverFile] = serverSource
		}
		sdkFields := apisStructFields(t, sdkSource, one.sdkType, apisSDKTypesPath+"或"+apisSDKSharedTypesPath)
		serverFields := apisStructFields(t, serverSource, one.serverType, one.serverFile)

		if len(sdkFields) != len(serverFields) {
			t.Errorf("类型 %s（↔ %s）字段数 %d，平台 %d：SDK %v / 平台 %v",
				one.sdkType, one.serverType, len(sdkFields), len(serverFields), apisFieldNames(sdkFields), apisFieldNames(serverFields))
			continue
		}
		for index, serverField := range serverFields {
			sdkField := sdkFields[index]
			if sdkField.name != serverField.name {
				t.Errorf("类型 %s（↔ %s）第 %d 个字段名 = %s，平台 %s", one.sdkType, one.serverType, index+1, sdkField.name, serverField.name)
			}
			if sdkField.tag != serverField.tag {
				t.Errorf("类型 %s（↔ %s）字段 %s 的 json tag = %q，平台 %q", one.sdkType, one.serverType, sdkField.name, sdkField.tag, serverField.tag)
			}
			if serverField.goType == "struct" {
				// 平台内联匿名结构体字段（如 UsageDailySummary.Totals）：类型无法逐字比对，
				// 其内部字段由下方具名类型（ApisUsageDailySummaryTotals）单独比对
				continue
			}
			gotType, wantType := apisNormalizeType(sdkField.goType), apisNormalizeType(serverField.goType)
			if gotType != wantType {
				t.Errorf("类型 %s（↔ %s）字段 %s 的 Go 类型 = %s，平台 %s（归一后 %s / %s）",
					one.sdkType, one.serverType, sdkField.name, sdkField.goType, serverField.goType, gotType, wantType)
			}
		}
	}

	// 平台 UsageDailySummary.Totals 是内联匿名结构，SDK 侧提为具名类型：单独按同一口径比对
	summarySource := readApisSource(t, filepath.Join(apisSiblingRepoPath, "backend/app/service/apis/usage-summary.go"))
	summaryBody := apisStructBody(t, summarySource, "UsageDailySummary", "usage-summary.go")
	inner := regexp.MustCompile(`(?s)Totals struct \{(.*?)\n\t\}`).FindStringSubmatch(summaryBody)
	if inner == nil {
		t.Fatalf("平台 UsageDailySummary 的内联 Totals 结构未解析到（结构格式可能已变化）")
	}
	serverTotals := apisParseFields(inner[1])
	sdkTotals := apisStructFields(t, sdkSource, "ApisUsageDailySummaryTotals", apisSDKTypesPath)
	if len(sdkTotals) != len(serverTotals) {
		t.Fatalf("ApisUsageDailySummaryTotals 字段数 %d，平台内联 totals %d", len(sdkTotals), len(serverTotals))
	}
	for index, serverField := range serverTotals {
		sdkField := sdkTotals[index]
		if sdkField.name != serverField.name || sdkField.tag != serverField.tag {
			t.Errorf("看板合计第 %d 个字段 = %s(%s)，平台 %s(%s)", index+1, sdkField.name, sdkField.tag, serverField.name, serverField.tag)
		}
		if gotType, wantType := apisNormalizeType(sdkField.goType), apisNormalizeType(serverField.goType); gotType != wantType {
			t.Errorf("看板合计字段 %s 的 Go 类型 = %s，平台 %s", sdkField.name, gotType, wantType)
		}
	}
}

// apisFieldNames - 字段名序列（对账失败时的可读输出）
func apisFieldNames(fields []apisStructField) []string {

	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.name)
	}
	return names
}
