package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
	"time"

	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// ============================= 实例指令通道 =============================
//
// 平台远程操作部署实例：实例经 SDK 长轮询主动领取指令、执行后回执（穿透 NAT/防火墙，
// 平台不反向直连实例）。通道模型与事件订阅一致；鉴权与 validate 完全同套
// （activation token + Ed25519 签名四头/metadata + nonce + 时间窗）。
// Pull 兼心跳（平台回写实例 last_seen_at）；回执幂等 = command_no 终态先入先赢，
// 回执失败时平台会重新下发，故指令 handler 必须具备幂等性（内置四个均为只读/幂等操作）。

const (
	// CommandActionRuntimeInfo - 实时运行信息（只读快照）
	CommandActionRuntimeInfo = "runtime_info"
	// CommandActionLicenseRefresh - 立即执行一轮许可证校验（与后台 tick 同入口）
	CommandActionLicenseRefresh = "license_refresh"
	// CommandActionConfigRefresh - 立即增量同步平台配置
	CommandActionConfigRefresh = "config_refresh"
	// CommandActionUpdateCheck - 立即检查更新（只检查，不执行升级）
	CommandActionUpdateCheck = "update_check"
)

const (
	// CommandResultSuccess - 指令执行成功
	CommandResultSuccess = "success"
	// CommandResultFailed - 指令执行失败
	CommandResultFailed = "failed"
)

const (
	// commandDefaultHold - 长轮询默认 hold（服务端默认 10s、硬上限 30s，客户端默认 25s 对齐设计口径）
	commandDefaultHold = 25 * time.Second
	// commandMaxHold - 长轮询 hold 硬上限（与服务端 timeout_ms 上限 30000 一致）
	commandMaxHold = 30 * time.Second
	// commandRetryDelay - 网络故障/服务拒绝退避（与 doRequest 的 retryDelay 同口径 1 分钟）
	commandRetryDelay = time.Minute
	// commandIdleDelay - 无凭证或 SEAT 终态停摆时的状态重查间隔（激活恢复后 10s 内指令通道随之恢复）
	commandIdleDelay = 10 * time.Second
)

// CommandTask - 平台下发的一条远程指令（json tag camelCase 与服务端逐字对齐）
type CommandTask struct {
	// CommandNo - 指令编号（幂等键，同时是回执锚点）
	CommandNo string `json:"commandNo"`
	// Action - 指令动作（内置白名单见 CommandAction* 常量，可经 Options.CommandHandlers 扩展/覆盖）
	Action string `json:"action"`
	// ParamsJSON - 动作参数 JSON 原文（无参数为空串）
	ParamsJSON string `json:"paramsJson"`
	// IssuedAt - 平台下发时间（毫秒）
	IssuedAt int64 `json:"issuedAt"`
}

// CommandHandler - 指令处理器：output 会被 JSON 序列化为 output_json 随回执上送（nil 则无输出）；
// 返回 error 即回执 failed（输出 {"message":"..."}）。
// 注意：回执失败平台会重新下发同一指令，handler 必须具备幂等性。
type CommandHandler func(ctx context.Context, task CommandTask) (output any, err error)

// commandPullResult - 一轮指令领取结果（HTTP 响应体 / gRPC 响应的协议无关形态）
type commandPullResult struct {
	Status     string
	ServerTime int64
	Commands   []CommandTask
	Message    string
}

// commandReportResult - 指令回执响应（HTTP 响应体 / gRPC 响应的协议无关形态）
type commandReportResult struct {
	Status     string
	ServerTime int64
	Message    string
}

// PullCommands - 一轮指令领取（长轮询单次调用，返回待执行指令批次）。
// timeoutMs 为长轮询 hold 毫秒数：<= 0 取 Options.CommandPullTimeout（默认 25s），
// 超过 30000 收敛到服务端硬上限。时钟校正（updateClockOffset）与状态闸门
// （StatusError/非放行态 → error）在本方法内完成；未 Start（传输层未初始化）返回错误。
func (this *Client) PullCommands(ctx context.Context, timeoutMs int32) ([]CommandTask, error) {

	hold := time.Duration(timeoutMs) * time.Millisecond
	if hold <= 0 {
		hold = this.options.CommandPullTimeout
	}
	if hold <= 0 {
		hold = commandDefaultHold
	}
	if hold > commandMaxHold {
		hold = commandMaxHold
	}
	if this.transport == nil {
		return nil, errors.New("运行面传输层未初始化")
	}
	result, err := this.transport.PullCommands(ctx, this.options.LicenseNo, hold)
	if err != nil {
		return nil, err
	}
	this.updateClockOffset(result.ServerTime)
	if result.Status == LicenceProtocol.StatusError {
		return nil, errors.New("服务端故障：" + result.Message)
	}
	if !LicenceProtocol.PassThrough(result.Status) {
		return nil, errors.New("许可证非放行态：" + result.Status)
	}
	return result.Commands, nil
}

// ReportCommand - 指令执行回执（幂等：command_no 终态先入先赢，重复/迟到回执平台直接返回成功）。
// result 取 CommandResultSuccess / CommandResultFailed；outputJSON 为执行输出 JSON 原文
// （无输出传空串）。回执为 best-effort：仅凭证失效（404）与服务端故障返回错误，
// 失败不重试（平台侧指令未达终态会重新下发，由下一轮领取补偿）。
func (this *Client) ReportCommand(ctx context.Context, commandNo string, result string, outputJSON string) error {

	if this.transport == nil {
		return errors.New("运行面传输层未初始化")
	}
	response, err := this.transport.ReportCommand(ctx, this.options.LicenseNo, commandNo, result, outputJSON)
	if err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status == LicenceProtocol.StatusNotFound {
		return errors.New("许可证或实例信息无效")
	}
	if response.Status == LicenceProtocol.StatusError {
		return errors.New("服务端故障：" + response.Message)
	}
	return nil
}

// commandLoop - 后台指令循环（Start 启动，与授权刷新循环并列；DisableCommands 时不启动）。
// 每轮：停摆判定（无 token / SEAT 终态不拉取，与 tick 同口径）→ 长轮询领取 → 逐条串行
// 执行并回执。网络故障按 commandRetryDelay 退避；Stop 时随 ctx 退出（长轮询请求被中断）。
func (this *Client) commandLoop(ctx context.Context) {

	for {
		this.mu.RLock()
		hasToken := this.state.ActivationToken != ""
		status := this.state.Status
		this.mu.RUnlock()

		// 停摆判定（与 tick 同口径）：无凭证等待激活恢复；SEAT 终态非自动恢复，不拉取
		if !hasToken || status == LicenceProtocol.StatusSeatLimitExceeded || status == LicenceProtocol.StatusSeatReleased {
			if !commandSleep(ctx, commandIdleDelay) {
				return
			}
			continue
		}

		commands, err := this.PullCommands(ctx, int32(this.options.CommandPullTimeout.Milliseconds()))
		if err != nil {
			if ctx.Err() != nil {
				return // Stop 中断长轮询，直接退出
			}
			// 网络故障/服务拒绝：退避后重试（授权状态收敛以 validate 循环为准，本地状态下轮开头重查）
			if !commandSleep(ctx, commandRetryDelay) {
				return
			}
			continue
		}
		for _, task := range commands {
			this.executeCommand(ctx, task)
		}
	}
}

// commandSleep - 可中断睡眠：false 表示 ctx 已取消
func commandSleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// executeCommand - 执行单条指令并回执（串行调用入口；handler panic 被 recover 为 failed，不拖垮循环）
func (this *Client) executeCommand(ctx context.Context, task CommandTask) {

	output, err := this.dispatchCommand(ctx, task)
	result := CommandResultSuccess
	outputJSON := ""
	if err != nil {
		result = CommandResultFailed
		raw, _ := json.Marshal(map[string]any{"message": err.Error()})
		outputJSON = string(raw)
	} else if output != nil {
		raw, marshalErr := json.Marshal(output)
		if marshalErr != nil {
			result = CommandResultFailed
			raw, _ = json.Marshal(map[string]any{"message": "执行输出序列化失败：" + marshalErr.Error()})
		}
		outputJSON = string(raw)
	}
	// 回执失败静默：平台侧指令保持非终态会重新下发，下一轮领取补偿（handler 须幂等）
	_ = this.ReportCommand(ctx, task.CommandNo, result, outputJSON)
}

// dispatchCommand - 指令分发：用户 CommandHandlers 优先（与内置同名时覆盖内置）→ 内置 handler →
// 未知动作回 failed「未知指令」。panic recover 兜底在本函数完成。
func (this *Client) dispatchCommand(ctx context.Context, task CommandTask) (output any, err error) {

	defer func() {
		if recovered := recover(); recovered != nil {
			output = nil
			err = fmt.Errorf("指令执行 panic：%v", recovered)
		}
	}()

	if handler, ok := this.options.CommandHandlers[task.Action]; ok {
		return handler(ctx, task)
	}
	switch task.Action {
	case CommandActionRuntimeInfo:
		return this.commandRuntimeInfo()
	case CommandActionLicenseRefresh:
		return this.commandLicenseRefresh(ctx)
	case CommandActionConfigRefresh:
		return this.commandConfigRefresh(ctx)
	case CommandActionUpdateCheck:
		return this.commandUpdateCheck(ctx)
	}
	return nil, errors.New("未知指令：" + task.Action)
}

// commandRuntimeInfo - 内置指令 runtime_info：返回实时运行信息快照（仅采集客户端确实持有的字段，
// 取不到的不编造不输出）。
func (this *Client) commandRuntimeInfo() (any, error) {

	this.mu.RLock()
	status := this.state.Status
	seatNo := this.state.SeatNo
	expiresAt := this.state.ExpiresAt
	validUntil := this.envelope.Payload.ValidUntil
	configSyncVersion := this.state.PlatformConfigSyncVersion
	configCount := len(this.state.PlatformConfigs)
	startedAt := this.startedAt
	this.mu.RUnlock()

	info := map[string]any{
		"os": goruntime.GOOS, "arch": goruntime.GOARCH,
		"goVersion": goruntime.Version(),
		"status":    status, "seatNo": seatNo, "expiresAt": expiresAt,
		"validUntil": validUntil, "version": this.options.Version,
		"deviceName": this.options.DeviceName, "licenseNo": this.options.LicenseNo,
		"platformConfigSyncVersion": configSyncVersion, "platformConfigCount": configCount,
	}
	if hostname, err := os.Hostname(); err == nil {
		info["hostname"] = hostname
	}
	if !startedAt.IsZero() {
		info["uptimeSeconds"] = int64(time.Since(startedAt).Seconds())
	}
	return info, nil
}

// commandLicenseRefresh - 内置指令 license_refresh：立即执行一轮校验。
// 直接调用与后台 tick 共用的安全入口（内部持有 opMu，含停摆判定与激活分支），
// 不与后台刷新循环并发，也不会死锁。
func (this *Client) commandLicenseRefresh(ctx context.Context) (any, error) {

	this.tick(ctx)
	return map[string]any{"status": this.Status()}, nil
}

// commandConfigRefresh - 内置指令 config_refresh：立即增量同步平台配置（PlatformConfigSync 自带 opMu）。
func (this *Client) commandConfigRefresh(ctx context.Context) (any, error) {

	envelope, err := this.PlatformConfigSync(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"synced": true, "version": envelope.Payload.SyncVersion}, nil
}

// commandUpdateCheck - 内置指令 update_check：立即检查更新（CheckUpdate 自带 opMu）。
// 只检查并返回清单摘要，不触发 updater 执行升级。
func (this *Client) commandUpdateCheck(ctx context.Context) (any, error) {

	info, err := this.CheckUpdate(ctx, goruntime.GOOS+"/"+goruntime.GOARCH)
	if err != nil {
		return nil, err
	}
	output := map[string]any{"update": info.Available, "status": info.Status}
	if info.Available && info.Manifest != nil {
		output["version"] = info.Manifest.Payload.Version
		output["buildNumber"] = info.Manifest.Payload.BuildNumber
		output["artifacts"] = len(info.Manifest.Payload.Artifacts)
	}
	return output, nil
}
