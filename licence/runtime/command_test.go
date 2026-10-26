package runtime

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	licencev1 "github.com/inis-io/aide/licence/proto/licence/v1"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

// ============================= 实例指令通道（HTTP/gRPC 双协议同组用例） =============================

// pushCommand - 向假平台指令队列追加一条待执行指令，返回指令编号
func (this *fakePlatform) pushCommand(action string, paramsJSON string) string {

	this.mu.Lock()
	defer this.mu.Unlock()
	commandNo := "CMD-2026-" + action
	this.commands = append(this.commands, CommandTask{
		CommandNo: commandNo, Action: action, ParamsJSON: paramsJSON, IssuedAt: time.Now().UnixMilli(),
	})
	return commandNo
}

// commandReportsSnapshot - 回执记录副本（按 commandNo 索引）
func (this *fakePlatform) commandReportsSnapshot() map[string]map[string]any {

	this.mu.Lock()
	defer this.mu.Unlock()
	result := make(map[string]map[string]any, len(this.commandReports))
	for _, report := range this.commandReports {
		if commandNo, ok := report["commandNo"].(string); ok {
			result[commandNo] = report
		}
	}
	return result
}

// waitCommandCondition - 轮询等待条件达成（指令循环为异步，避免 sleep 猜时序）
func waitCommandCondition(t *testing.T, condition func() bool) {

	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待条件超时")
}

// TestPullCommandsEmptyBatchHTTP - HTTP 领取空批次：无指令返回空列表不报错
func TestPullCommandsEmptyBatchHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil {
		t.Fatalf("PullCommands 失败: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("空批次应无指令，实际 %+v", commands)
	}
}

// TestPullCommandsWithTasksHTTP - HTTP 领取带指令批次：字段逐一对齐且平台队列被弹出
func TestPullCommandsWithTasksHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	first := platform.pushCommand(CommandActionRuntimeInfo, "")
	second := platform.pushCommand("custom_action", `{"key":"value"}`)

	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil {
		t.Fatalf("PullCommands 失败: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("应领取 2 条指令，实际 %+v", commands)
	}
	if commands[0].CommandNo != first || commands[0].Action != CommandActionRuntimeInfo {
		t.Fatalf("首条指令映射不符: %+v", commands[0])
	}
	if commands[1].CommandNo != second || commands[1].ParamsJSON != `{"key":"value"}` || commands[1].IssuedAt <= 0 {
		t.Fatalf("次条指令映射不符: %+v", commands[1])
	}
	// 队列已弹出：再次领取为空
	commands, err = client.PullCommands(t.Context(), 100)
	if err != nil || len(commands) != 0 {
		t.Fatalf("领取后队列应为空: %v %+v", err, commands)
	}
}

// TestReportCommandHTTP - HTTP 回执成功：平台记录 result/outputJson/clientTime
func TestReportCommandHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	if err = client.ReportCommand(t.Context(), "CMD-2026-000001", CommandResultSuccess, `{"os":"windows"}`); err != nil {
		t.Fatalf("ReportCommand 失败: %v", err)
	}
	reports := platform.commandReportsSnapshot()
	report, exist := reports["CMD-2026-000001"]
	if !exist {
		t.Fatalf("平台未收到回执")
	}
	if report["result"] != CommandResultSuccess || report["outputJson"] != `{"os":"windows"}` {
		t.Fatalf("回执内容不符: %+v", report)
	}
	if clientTime, ok := report["clientTime"].(float64); !ok || clientTime <= 0 {
		t.Fatalf("回执缺少 clientTime: %+v", report)
	}
	if report["licenseNo"] != "LIC-2026-000123" {
		t.Fatalf("回执 licenseNo 不符: %+v", report)
	}
}

// TestCommandsBadSignRejectedHTTP - 私钥被篡改后指令通道请求验签失败（签名头校验）：
// pull 与 report 均得模糊 404 语义
func TestCommandsBadSignRejectedHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	seed, _, _ := LicenceProtocol.GenerateKeyPair()
	client.mu.Lock()
	client.state.ClientSeed = hex.EncodeToString(seed)
	client.mu.Unlock()

	if _, err = client.PullCommands(t.Context(), 100); err == nil || err.Error() != "许可证或实例信息无效" {
		t.Fatalf("验签失败 pull 应得模糊 NotFound，实际 %v", err)
	}
	if err = client.ReportCommand(t.Context(), "CMD-1", CommandResultSuccess, ""); err == nil || err.Error() != "许可证或实例信息无效" {
		t.Fatalf("验签失败 report 应得模糊 NotFound，实际 %v", err)
	}
}

// TestCommandUnknownActionFailedHTTP - 未知动作指令：执行回执 failed 且输出含「未知指令」
func TestCommandUnknownActionFailedHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	client, err := New(testOptions(platform, t.TempDir()))
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	commandNo := platform.pushCommand("unknown_action", "")
	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil || len(commands) != 1 {
		t.Fatalf("领取指令失败: %v %+v", err, commands)
	}
	client.executeCommand(t.Context(), commands[0])

	reports := platform.commandReportsSnapshot()
	report, exist := reports[commandNo]
	if !exist {
		t.Fatalf("平台未收到未知动作回执")
	}
	if report["result"] != CommandResultFailed {
		t.Fatalf("未知动作应回 failed，实际 %+v", report)
	}
	output, _ := report["outputJson"].(string)
	if !strings.Contains(output, "未知指令") {
		t.Fatalf("failed 输出应含'未知指令'，实际 %s", output)
	}
}

// TestCommandLoopEndToEndHTTP - 指令循环端到端：Start 后自动领取 → 串行执行 → 逐条回执。
// 覆盖内置 runtime_info / license_refresh（与后台 tick 共用 opMu 入口，实证不死锁）/
// update_check 与用户自定义 handler；license_refresh 触发的 validate 走真实假平台。
func TestCommandLoopEndToEndHTTP(t *testing.T) {

	platform := newFakePlatform(t)
	options := testOptions(platform, t.TempDir())
	options.CommandPullTimeout = 50 * time.Millisecond
	options.CommandHandlers = map[string]CommandHandler{
		"custom_echo": func(ctx context.Context, task CommandTask) (any, error) {
			return map[string]any{"echo": task.ParamsJSON}, nil
		},
	}
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	infoNo := platform.pushCommand(CommandActionRuntimeInfo, "")
	refreshNo := platform.pushCommand(CommandActionLicenseRefresh, "")
	checkNo := platform.pushCommand(CommandActionUpdateCheck, "")
	echoNo := platform.pushCommand("custom_echo", `{"k":1}`)

	waitCommandCondition(t, func() bool {
		reports := platform.commandReportsSnapshot()
		return len(reports) >= 4
	})
	reports := platform.commandReportsSnapshot()
	for commandNo, expect := range map[string]string{
		infoNo: CommandResultSuccess, refreshNo: CommandResultSuccess,
		checkNo: CommandResultSuccess, echoNo: CommandResultSuccess,
	} {
		report, exist := reports[commandNo]
		if !exist {
			t.Fatalf("缺少指令 %s 的回执", commandNo)
		}
		if report["result"] != expect {
			t.Fatalf("指令 %s 回执应为 %s，实际 %+v", commandNo, expect, report)
		}
	}
	// runtime_info 输出含运行信息字段
	var info map[string]any
	raw, _ := reports[infoNo]["outputJson"].(string)
	if err = json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("runtime_info 输出解析失败: %v raw=%s", err, raw)
	}
	if info["os"] == "" || info["arch"] == "" || info["status"] != LicenceProtocol.StatusValid {
		t.Fatalf("runtime_info 输出缺字段: %+v", info)
	}
	// license_refresh 输出最新状态
	var refresh map[string]any
	raw, _ = reports[refreshNo]["outputJson"].(string)
	if err = json.Unmarshal([]byte(raw), &refresh); err != nil || refresh["status"] != LicenceProtocol.StatusValid {
		t.Fatalf("license_refresh 输出异常: %v %+v", err, refresh)
	}
	// update_check 只检查不升级：无可用版本时 update=false
	var check map[string]any
	raw, _ = reports[checkNo]["outputJson"].(string)
	if err = json.Unmarshal([]byte(raw), &check); err != nil || check["update"] != false {
		t.Fatalf("update_check 输出异常: %v %+v", err, check)
	}
	// 自定义 handler 输出回显参数
	var echo map[string]any
	raw, _ = reports[echoNo]["outputJson"].(string)
	if err = json.Unmarshal([]byte(raw), &echo); err != nil || echo["echo"] != `{"k":1}` {
		t.Fatalf("自定义 handler 输出异常: %v %+v", err, echo)
	}
}

// TestCommandLoopCustomHandlerOverridesBuiltin - 自定义 handler 与内置同名时覆盖内置
func TestCommandLoopCustomHandlerOverridesBuiltin(t *testing.T) {

	platform := newFakePlatform(t)
	options := testOptions(platform, t.TempDir())
	options.CommandPullTimeout = 50 * time.Millisecond
	options.CommandHandlers = map[string]CommandHandler{
		CommandActionRuntimeInfo: func(ctx context.Context, task CommandTask) (any, error) {
			return map[string]any{"custom": true}, nil
		},
	}
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	commandNo := platform.pushCommand(CommandActionRuntimeInfo, "")
	waitCommandCondition(t, func() bool {
		_, exist := platform.commandReportsSnapshot()[commandNo]
		return exist
	})
	report := platform.commandReportsSnapshot()[commandNo]
	if report["result"] != CommandResultSuccess {
		t.Fatalf("覆盖内置的 handler 应执行成功: %+v", report)
	}
	var output map[string]any
	raw, _ := report["outputJson"].(string)
	if err = json.Unmarshal([]byte(raw), &output); err != nil || output["custom"] != true {
		t.Fatalf("应走自定义 handler 而非内置 runtime_info: %v %s", err, raw)
	}
}

// TestCommandLoopHandlerPanicRecovered - 自定义 handler panic 被 recover 为 failed 回执，不拖垮循环
func TestCommandLoopHandlerPanicRecovered(t *testing.T) {

	platform := newFakePlatform(t)
	options := testOptions(platform, t.TempDir())
	options.CommandPullTimeout = 50 * time.Millisecond
	options.CommandHandlers = map[string]CommandHandler{
		"panicking": func(ctx context.Context, task CommandTask) (any, error) {
			panic("handler 崩溃")
		},
	}
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	panickNo := platform.pushCommand("panicking", "")
	afterNo := platform.pushCommand("custom_echo_missing", "") // 未注册动作 → failed「未知指令」

	waitCommandCondition(t, func() bool {
		reports := platform.commandReportsSnapshot()
		_, first := reports[panickNo]
		_, second := reports[afterNo]
		return first && second
	})
	reports := platform.commandReportsSnapshot()
	if reports[panickNo]["result"] != CommandResultFailed {
		t.Fatalf("panic handler 应回 failed: %+v", reports[panickNo])
	}
	output, _ := reports[panickNo]["outputJson"].(string)
	if !strings.Contains(output, "panic") {
		t.Fatalf("panic 回执输出应含 panic 信息: %s", output)
	}
	// 循环未被拖垮：后续指令照常执行
	if reports[afterNo]["result"] != CommandResultFailed {
		t.Fatalf("后续指令应照常回执: %+v", reports[afterNo])
	}
}

// TestCommandLoopDisabled - DisableCommands=true 不启动指令循环
func TestCommandLoopDisabled(t *testing.T) {

	platform := newFakePlatform(t)
	options := testOptions(platform, t.TempDir())
	options.CommandPullTimeout = 50 * time.Millisecond
	options.DisableCommands = true
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	platform.pushCommand(CommandActionRuntimeInfo, "")
	time.Sleep(300 * time.Millisecond)
	platform.mu.Lock()
	pulls := platform.commandPullCalls
	reports := len(platform.commandReports)
	platform.mu.Unlock()
	if pulls != 0 || reports != 0 {
		t.Fatalf("DisableCommands 不应拉取/回执: pulls=%d reports=%d", pulls, reports)
	}
}

// TestCommandLoopStopsOnSeatTerminal - SEAT 终态停摆（与 tick 同口径）：状态置为
// SEAT_RELEASED 后指令循环不再拉取
func TestCommandLoopStopsOnSeatTerminal(t *testing.T) {

	platform := newFakePlatform(t)
	options := testOptions(platform, t.TempDir())
	options.CommandPullTimeout = 50 * time.Millisecond
	client, err := New(options)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	if err = client.Start(t.Context()); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	defer func() { _ = client.Close() }()

	// 等循环至少跑过一轮，排除在途长轮询干扰
	waitCommandCondition(t, func() bool {
		platform.mu.Lock()
		defer platform.mu.Unlock()
		return platform.commandPullCalls > 0
	})
	client.mu.Lock()
	client.state.Status = LicenceProtocol.StatusSeatReleased
	client.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	platform.mu.Lock()
	before := platform.commandPullCalls
	platform.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	platform.mu.Lock()
	after := platform.commandPullCalls
	platform.mu.Unlock()
	if after != before {
		t.Fatalf("SEAT 终态后不应继续拉取: before=%d after=%d", before, after)
	}
}

// TestCommandsTransportNotReady - 未 Start（传输层未初始化）直接返回错误
func TestCommandsTransportNotReady(t *testing.T) {

	client := &Client{}
	if _, err := client.PullCommands(context.Background(), 0); err == nil || err.Error() != "运行面传输层未初始化" {
		t.Fatalf("期望'运行面传输层未初始化'，实际 %v", err)
	}
	if err := client.ReportCommand(context.Background(), "CMD-1", CommandResultSuccess, ""); err == nil || err.Error() != "运行面传输层未初始化" {
		t.Fatalf("期望'运行面传输层未初始化'，实际 %v", err)
	}
}

// ============================= gRPC 侧（bufconn 同组用例） =============================

// runtimeCommandServer - 指令通道 gRPC 假服务端：校验签名 metadata 五件套；
// commands 为待领取队列（Pull 弹出全部），reports 记录回执原文
type runtimeCommandServer struct {
	licencev1.UnimplementedCommandRuntimeServiceServer
	t        *testing.T
	mu       sync.Mutex
	commands []*licencev1.CommandTask
	reports  []*licencev1.CommandReportRequest
}

func (s *runtimeCommandServer) check(ctx context.Context) {
	s.t.Helper()
	md, _ := metadata.FromIncomingContext(ctx)
	for _, key := range []string{LicenceProtocol.MetadataToken, LicenceProtocol.MetadataTimestamp, LicenceProtocol.MetadataNonce, LicenceProtocol.MetadataSignature, LicenceProtocol.MetadataSignVersion} {
		if len(md.Get(key)) == 0 {
			s.t.Fatalf("缺少签名 metadata %s", key)
		}
	}
}

func (s *runtimeCommandServer) Pull(ctx context.Context, _ *licencev1.CommandPullRequest) (*licencev1.CommandPullResponse, error) {
	s.check(ctx)
	s.mu.Lock()
	commands := s.commands
	s.commands = nil
	s.mu.Unlock()
	return &licencev1.CommandPullResponse{
		Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli(), Commands: commands,
	}, nil
}

func (s *runtimeCommandServer) Report(ctx context.Context, request *licencev1.CommandReportRequest) (*licencev1.CommandReportResponse, error) {
	s.check(ctx)
	s.mu.Lock()
	s.reports = append(s.reports, request)
	s.mu.Unlock()
	return &licencev1.CommandReportResponse{Status: LicenceProtocol.StatusValid, ServerTime: time.Now().UnixMilli()}, nil
}

func (s *runtimeCommandServer) pushCommand(commandNo string, action string, paramsJSON string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, &licencev1.CommandTask{
		CommandNo: commandNo, Action: action, ParamsJson: paramsJSON, IssuedAt: time.Now().UnixMilli(),
	})
}

// newTestCommandTransport - 装配指令通道 bufconn gRPC 测试传输层与宿主 Client
func newTestCommandTransport(t *testing.T, commandServer licencev1.CommandRuntimeServiceServer) (*grpcRuntimeTransport, *Client) {

	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	licencev1.RegisterCommandRuntimeServiceServer(server, commandServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	seed, _, err := LicenceProtocol.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{
		options: Options{LicenseNo: "LIC-2026-000123", HTTPTimeout: time.Second, CommandPullTimeout: 50 * time.Millisecond},
		state:   runtimeState{ActivationToken: "token", ClientSeed: hex.EncodeToString(seed)},
	}
	transport := &grpcRuntimeTransport{client: client, conn: conn, command: licencev1.NewCommandRuntimeServiceClient(conn)}
	client.transport = transport
	return transport, client
}

// TestPullCommandsEmptyBatchGRPC - gRPC 领取空批次
func TestPullCommandsEmptyBatchGRPC(t *testing.T) {

	_, client := newTestCommandTransport(t, &runtimeCommandServer{t: t})
	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil {
		t.Fatalf("PullCommands 失败: %v", err)
	}
	if len(commands) != 0 {
		t.Fatalf("空批次应无指令，实际 %+v", commands)
	}
}

// TestPullCommandsWithTasksGRPC - gRPC 领取带指令批次：proto → CommandTask 字段逐一对齐
func TestPullCommandsWithTasksGRPC(t *testing.T) {

	server := &runtimeCommandServer{t: t}
	server.pushCommand("CMD-2026-000001", CommandActionRuntimeInfo, "")
	server.pushCommand("CMD-2026-000002", "custom_action", `{"key":"value"}`)
	_, client := newTestCommandTransport(t, server)

	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil {
		t.Fatalf("PullCommands 失败: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("应领取 2 条指令，实际 %+v", commands)
	}
	if commands[0].CommandNo != "CMD-2026-000001" || commands[0].Action != CommandActionRuntimeInfo {
		t.Fatalf("首条指令映射不符: %+v", commands[0])
	}
	if commands[1].ParamsJSON != `{"key":"value"}` || commands[1].IssuedAt <= 0 {
		t.Fatalf("次条指令映射不符: %+v", commands[1])
	}
	// 队列已弹出
	commands, err = client.PullCommands(t.Context(), 100)
	if err != nil || len(commands) != 0 {
		t.Fatalf("领取后队列应为空: %v %+v", err, commands)
	}
}

// TestReportCommandGRPC - gRPC 回执成功：服务端记录 result/outputJson/clientTime
func TestReportCommandGRPC(t *testing.T) {

	server := &runtimeCommandServer{t: t}
	_, client := newTestCommandTransport(t, server)

	if err := client.ReportCommand(t.Context(), "CMD-2026-000001", CommandResultSuccess, `{"os":"linux"}`); err != nil {
		t.Fatalf("ReportCommand 失败: %v", err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.reports) != 1 {
		t.Fatalf("应收到 1 条回执，实际 %d", len(server.reports))
	}
	report := server.reports[0]
	if report.GetCommandNo() != "CMD-2026-000001" || report.GetResult() != CommandResultSuccess ||
		report.GetOutputJson() != `{"os":"linux"}` || report.GetClientTime() <= 0 ||
		report.GetLicenseNo() != "LIC-2026-000123" {
		t.Fatalf("回执内容不符: %+v", report)
	}
}

// TestCommandUnknownActionFailedGRPC - gRPC 未知动作指令：执行回执 failed 且输出含「未知指令」
func TestCommandUnknownActionFailedGRPC(t *testing.T) {

	server := &runtimeCommandServer{t: t}
	server.pushCommand("CMD-2026-unknown", "unknown_action", "")
	_, client := newTestCommandTransport(t, server)

	commands, err := client.PullCommands(t.Context(), 100)
	if err != nil || len(commands) != 1 {
		t.Fatalf("领取指令失败: %v %+v", err, commands)
	}
	client.executeCommand(t.Context(), commands[0])

	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.reports) != 1 {
		t.Fatalf("应收到 1 条回执，实际 %d", len(server.reports))
	}
	report := server.reports[0]
	if report.GetResult() != CommandResultFailed || !strings.Contains(report.GetOutputJson(), "未知指令") {
		t.Fatalf("未知动作应回 failed 含'未知指令': %+v", report)
	}
}

// TestCommandLoopGRPC - gRPC 指令循环端到端：手工装配客户端跑 commandLoop，
// 未知动作与自定义 handler 均被领取执行并回执（循环逻辑协议无关，此处实证 gRPC 传输贯通）
func TestCommandLoopGRPC(t *testing.T) {

	server := &runtimeCommandServer{t: t}
	_, client := newTestCommandTransport(t, server)
	client.options.CommandHandlers = map[string]CommandHandler{
		"custom_ping": func(ctx context.Context, task CommandTask) (any, error) {
			return map[string]any{"pong": true}, nil
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go client.commandLoop(ctx)

	server.pushCommand("CMD-2026-ping", "custom_ping", "")
	server.pushCommand("CMD-2026-unknown", "unknown_action", "")

	waitCommandCondition(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.reports) >= 2
	})
	server.mu.Lock()
	defer server.mu.Unlock()
	results := make(map[string]*licencev1.CommandReportRequest, len(server.reports))
	for _, report := range server.reports {
		results[report.GetCommandNo()] = report
	}
	if results["CMD-2026-ping"].GetResult() != CommandResultSuccess ||
		!strings.Contains(results["CMD-2026-ping"].GetOutputJson(), "pong") {
		t.Fatalf("自定义 handler 回执异常: %+v", results["CMD-2026-ping"])
	}
	if results["CMD-2026-unknown"].GetResult() != CommandResultFailed {
		t.Fatalf("未知动作回执异常: %+v", results["CMD-2026-unknown"])
	}
}
