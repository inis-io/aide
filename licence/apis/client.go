// Package apis - API 商城 typed 方法包：runtime 包 Client 的唯一业务能力挂载点。
//
// 本包不 import runtime：typed 方法面向 core.Doer 窄接口编程，凭证、签名头族、
// HTTP/gRPC 协议选择、连接管理、校时与失败退避全部由宿主（runtime 包 Client）
// 经适配器注入（依赖方向 runtime → apis，编译期保证无环）。
//
// 能力子包分类约定：API 商城未来会有很多类型的 API，每个能力按文件夹分类为一个
// 能力子包（当前已落地 iplocate / mailsend），包内暴露 Resource + 能力专属类型；
// 根 Client 把各能力 Resource 挂载为同名字段（与 admin 资源组同风格）：
//
//	lic.Apis.IPLocate.Query(ctx, "203.0.113.10")   // iplocate.Resource
//	lic.Apis.MailSend.Send(ctx, mailsend.Input{…}) // mailsend.Resource
//
// 跨能力语义留在根包：Invoke（通用兜底）/ Usage（只读对账）；共享核心件
// （Doer/Error/Receipt/业务码/ErrNotActivated/信封解析/幂等键）下沉在 apis/core，
// 由本包以 type alias 原样 re-export（runtime 等既有引用零改动）。新增能力的落点
// 与子包施工步骤见 licen-hub docs/plan/apis/08 能力接入指南。
//
// 已落地能力的 HTTP 路径、gRPC full method 与业务码集合以权威契约为准——
// proto 见 `licence/proto/apis/v1/runtime.proto`，映射清单见 protocol-matrix.yaml，
// 服务端映射表见 licen-hub docs/plan/apis/04 §2.4；本包只处理 JSON，不 import proto 与传输实现。
//
// 错误归一（唯一解析点，见 apis/core 的 ParseEnvelope）：双协议下业务失败都被宿主还原为
// `{code, msg, detail}` 同一形态的信封，统一解析为 *apis.Error。典型用法：
//
//	result, receipt, err := lic.Apis.IPLocate.Query(ctx, "203.0.113.10")
//	var apiErr *apis.Error
//	if errors.As(err, &apiErr) && apiErr.Code == apis.ErrorCodeQuotaExceeded {
//		// 额度耗尽：切本地缓存数据源，或按 apiErr.Detail["resetAt"] 安排的恢复时间
//	}
package apis

import (
	"context"
	"encoding/json"

	"github.com/inis-io/aide/licence/apis/core"
	"github.com/inis-io/aide/licence/apis/iplocate"
	"github.com/inis-io/aide/licence/apis/mailsend"
)

// ============================= 共享核心件 re-export（既有引用零改动） =============================
//
// 能力子包不 import 本包，共享件统一下沉 apis/core 打破依赖环；
// 这里以类型别名 / 常量镜像原样 re-export（运行时与下游的 apis.Error、
// apis.Receipt、apis.Doer、业务码常量、apis.ErrNotActivated、apis.HTTPStatusByCode
// 引用零改动）。能力专属类型（iplocate.Result / mailsend.Input / mailsend.Result）
// 不 re-export，引用方必须 import 对应能力子包（分类的意义）。

// Error - 运行面业务错误（= core.Error，双协议同一形态）
type Error = core.Error

// Receipt - 计量回执（= core.Receipt，跨 HTTP/gRPC 字段一致）
type Receipt = core.Receipt

// Doer - 宿主注入的已认证调用能力（= core.Doer）
type Doer = core.Doer

// ErrNotActivated - 未激活闸门（= core.ErrNotActivated）
var ErrNotActivated = core.ErrNotActivated

// HTTPStatusByCode - 业务码 → HTTP 等价状态码（= core.HTTPStatusByCode；Go 无函数别名，薄壳转发）
func HTTPStatusByCode(code string) int { return core.HTTPStatusByCode(code) }

// 业务码常量（= core.ErrorCode*，与平台 ServiceError.Code 逐字一致）
const (
	// ErrorCodeInvalidArgument - 请求参数或业务前置校验失败（HTTP 400 / InvalidArgument）
	ErrorCodeInvalidArgument = core.ErrorCodeInvalidArgument
	// ErrorCodeUnauthorized - 调用者身份无效（HTTP 401 / Unauthenticated）
	ErrorCodeUnauthorized = core.ErrorCodeUnauthorized
	// ErrorCodeForbidden - 数据范围/角色边界拒绝（HTTP 403 / PermissionDenied）
	ErrorCodeForbidden = core.ErrorCodeForbidden
	// ErrorCodeNoEntitlement - 无订阅、无免费额度且无按量定价（HTTP 403 / PermissionDenied）
	ErrorCodeNoEntitlement = core.ErrorCodeNoEntitlement
	// ErrorCodeQuotaExceeded - 订阅额度耗尽（HTTP 403 / ResourceExhausted；detail 带 scope/resetAt）
	ErrorCodeQuotaExceeded = core.ErrorCodeQuotaExceeded
	// ErrorCodeInsufficientBalance - 余额不足（HTTP 402 / ResourceExhausted，充值后恢复）
	ErrorCodeInsufficientBalance = core.ErrorCodeInsufficientBalance
	// ErrorCodeSpendLimitExceeded - 月度消费限额（HTTP 429 / ResourceExhausted；detail 带 limit/monthSpent/resetAt）
	ErrorCodeSpendLimitExceeded = core.ErrorCodeSpendLimitExceeded
	// ErrorCodeAccountFrozen - 账户状态被冻结（HTTP 403 / FailedPrecondition，解冻后恢复）
	ErrorCodeAccountFrozen = core.ErrorCodeAccountFrozen
	// ErrorCodeConcurrencyLimited - 并发闸门（HTTP 429 / ResourceExhausted；detail 带 retryAfterMs）
	ErrorCodeConcurrencyLimited = core.ErrorCodeConcurrencyLimited
	// ErrorCodeRateLimited - 速率闸门（HTTP 429 / ResourceExhausted；detail 带 retryAfterMs）
	ErrorCodeRateLimited = core.ErrorCodeRateLimited
	// ErrorCodeUpstreamError - 上游能力失败（HTTP 502 / Internal，对外不泄露细节）
	ErrorCodeUpstreamError = core.ErrorCodeUpstreamError
	// ErrorCodeIPNotFound - IP 归属地空结果（HTTP 404 / NotFound）
	ErrorCodeIPNotFound = core.ErrorCodeIPNotFound
	// ErrorCodeCapabilityNotFound - 能力未注册或无在售产品（HTTP 404 / NotFound）
	ErrorCodeCapabilityNotFound = core.ErrorCodeCapabilityNotFound
	// ErrorCodeNotFound - 目标资源不存在；也是运行面认证失败的唯一外显码（模糊 404，防爆破）
	ErrorCodeNotFound = core.ErrorCodeNotFound
	// ErrorCodeConflict - 乐观锁/唯一键冲突（HTTP 409 / FailedPrecondition，刷新后重试）
	ErrorCodeConflict = core.ErrorCodeConflict
	// ErrorCodeInvalidState - 状态机不允许的流转（HTTP 409 / FailedPrecondition）
	ErrorCodeInvalidState = core.ErrorCodeInvalidState
	// ErrorCodeInternal - 服务端故障；未知/未登记业务码的回落（HTTP 500 / Internal）
	ErrorCodeInternal = core.ErrorCodeInternal
)

// ============================= 宿主调用能力与能力子资源挂载 =============================

// Client - API 商城 typed 方法挂载点（由 licence.New 构造，生命周期跟随根 Client）。
// 跨能力方法（Invoke/Usage）挂在 Client 自身，单能力方法挂在同名能力子资源上
// （lic.Apis.IPLocate.Query / lic.Apis.MailSend.Send）。
type Client struct {
	// doer - 宿主注入的已认证调用能力（跨能力方法 Invoke/Usage 直接使用）
	doer core.Doer
	// IPLocate - IP 定位能力（iplocate 子包；lic.Apis.IPLocate.Query(ctx, ip)）
	IPLocate *iplocate.Resource
	// MailSend - 邮件代发能力（mailsend 子包；lic.Apis.MailSend.Send(ctx, input)）
	MailSend *mailsend.Resource
}

// New - 创建商城客户端（仅装配宿主调用能力与各能力子资源，不发起网络请求）
func New(doer Doer) *Client {
	return &Client{
		doer:     doer,
		IPLocate: iplocate.New(doer),
		MailSend: mailsend.New(doer),
	}
}

// ============================= 请求出口 =============================

// do - 统一调用出口（跨能力方法 Invoke/Usage 用；能力子包内方法走各自 Resource）：
// 宿主调一次 → 按唯一解析点解信封（成功返回 data 原文，业务失败返回 *apis.Error）。
// 传输/网络错误原样返回，不伪装业务码。
func (this *Client) do(ctx context.Context, method string, path string, body []byte, requestId string) (json.RawMessage, error) {

	raw, err := this.doer.Do(ctx, method, path, body, requestId)
	if err != nil {
		return nil, err
	}
	return core.ParseEnvelope(raw)
}
