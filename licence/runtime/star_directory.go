package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/inis-io/aide/licence/apis/core"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// 本文件承载星链中枢（Star Hub）运行面的 SDK 公开方法（目录组）与共享骨架。
//
// 契约权威源：licen-hub docs/md/star/星链中枢设计与落地方案.md §4/§5；
// proto 见 proto/star/v1/star.proto（package licenhub.star.v1），映射清单见
// proto/star/v1/protocol-matrix.yaml。业务语义只写一份：公开方法 → call → 根 Client
// doRequest（withSign=true，activation-sign-v1 凭证族）→ 传输层 switch 双侧 case
// （HTTP 直发 / gRPC 映射为同形信封），与 saas.go TenantSync 同一范式。
//
// 错误归一：星链运行面与 API 商城共用同一信封形态（{code:0,msg,data} /
// {code:"业务码",msg,detail}）与同一业务码集合，统一经 apis/core.ParseEnvelope 解析——
// 业务拒绝返回 *apis.Error（errors.As 断言），传输/网络错误原样返回，不伪装业务码。
//
// 幂等键：写操作幂等键 = 业务单号（purchase_no/grant_no/promote_no/report_no），
// 经 apis/core.ResolveRequestID 归一（显式传入优先，缺省自动生成 req_ 前缀键并在结果中回显），
// 由 call 注入 context、传输层透传 X-Request-Id / metadata x-request-id（不进签名 canonical）。
// 服务端防呆：非空 X-Request-Id 必须等于业务单号，不一致即 INVALID_ARGUMENT。

// 星链运行面 HTTP 路径照 saas.go 惯例在调用点以字面量书写（/api/v1/star/...，
// 与 protocol-matrix.yaml 第 2 列逐字一致；gRPC 侧传输 switch 以同一路径字面量为路由键，
// 双侧一致性由 proto/star/v1 的矩阵门禁测试守护）。

// 星链枚举取值（与 licen-hub 服务端逐字一致）
const (
	// StarStatusActive - 实例资格态：正常
	StarStatusActive = "active"
	// StarStatusSuspended - 实例资格态：暂停（trust < 0.3 自动触发，索引剔除 + 目录标记）
	StarStatusSuspended = "suspended"
	// StarStatusLeft - 实例资格态：已离网
	StarStatusLeft = "left"
	// StarStatsAccepted - 投流阅读上报口径：有效阅读（参与分账）
	StarStatsAccepted = "accepted"
	// StarStatsQuarantined - 投流阅读上报口径：隔离（不参与分账）
	StarStatsQuarantined = "quarantined"
	// StarOutcomePending - 仲裁处置结果：待处置
	StarOutcomePending = "pending"
	// StarOutcomeUpheld - 仲裁处置结果：成立（trust −0.1）
	StarOutcomeUpheld = "upheld"
	// StarOutcomeRejected - 仲裁处置结果：驳回
	StarOutcomeRejected = "rejected"
)

// ErrStarNotActivated - 星链未激活闸门：根 Client 未完成激活（无 activation token）
// 或授权状态非放行态时，client.Star.* 直接返回本错误（不发请求）；
// 服务端的凭证校验仍是最终边界。
var ErrStarNotActivated = errors.New("star: 许可证未激活或授权状态不可用")

// StarService - 星链中枢运行面挂载点（lic.Star.Join(...) 形态，与 lic.Apis 同风格）。
// 由 New 构造，生命周期跟随根 Client；星链实例身份锚定激活体系
// （instance_id 缺省取 Options.InstanceNo，服务端强制其等于凭证锚定的部署实例编号）。
type StarService struct {
	// client - 宿主根 Client
	client *Client
}

// call - 星链运行面统一调用出口：未激活闸门 → doRequest（withSign=true + 幂等键注入 context）
// → 信封解析（apis/core.ParseEnvelope，成功返回 data 原文，业务失败返回 *apis.Error）。
// requestId 为空串表示本次调用不带幂等键（只读/快照幂等调用）。
func (this *StarService) call(ctx context.Context, path string, body []byte, requestId string) (json.RawMessage, error) {

	client := this.client
	client.mu.RLock()
	token := client.state.ActivationToken
	status := client.state.Status
	client.mu.RUnlock()
	if token == "" || !LicenceProtocol.PassThrough(status) {
		return nil, ErrStarNotActivated
	}
	_, raw, err := client.doRequest(withApisRequestID(ctx, requestId), http.MethodPost, path, body, true)
	if err != nil {
		return nil, err
	}
	return core.ParseEnvelope(raw)
}

// instanceId - 实例身份归一：显式传入优先，缺省取 Options.InstanceNo（激活锚定的部署实例编号）
func (this *StarService) instanceId(value string) (string, error) {

	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(this.client.options.InstanceNo)
	}
	if value == "" {
		return "", errors.New("star: instanceId 不能为空（缺省取 Options.InstanceNo）")
	}
	return value, nil
}

// ============================= 星链目录 =============================

// StarJoinInput - 注册/心跳入参
type StarJoinInput struct {
	// InstanceId - 实例标识（缺省取 Options.InstanceNo；必须等于凭证锚定的部署实例编号）
	InstanceId string `json:"instanceId"`
	// Name - 舰名
	Name string `json:"name"`
	// Endpoint - 舰际 gRPC 地址
	Endpoint string `json:"endpoint"`
	// Pubkey - 实例 Ed25519 公钥 hex（实例级密钥由舰端生成，私钥不出舰；轮换 = 上送即覆盖）
	Pubkey string `json:"pubkey"`
	// AppVersion - im 版本（心跳顺带；缺省取 Options.Version）
	AppVersion string `json:"appVersion"`
}

// StarJoinResult - 注册/心跳回执
type StarJoinResult struct {
	// StarStatus - 仲裁资格态（active/suspended/left）
	StarStatus string
	// TrustScore - 信用分（万分位 0~10000，冷启动 6000 = 0.60）
	TrustScore int
	// Since - 目录水位（毫秒戳，供 ListPeers 增量拉取）
	Since int64
}

// starJoinPayload - join 响应 data（双协议同形：HTTP 为服务端原文，gRPC 为传输层合成）
type starJoinPayload struct {
	StarStatus string `json:"starStatus"`
	TrustScore int    `json:"trustScore"`
	Since      int64  `json:"since"`
}

// Join - 星链目录注册/心跳（upsert 天然幂等；心跳周期建议 10 分钟，随 hotindex 周期）。
// 幂等键取实例标识（同一实例的心跳重放安全）；返回资格态/信用分/目录水位。
/**
 * @param ctx context.Context - 调用上下文
 * @param input StarJoinInput - 实例身份与公钥
 * @return StarJoinResult - 资格态 / 信用分（万分位）/ 目录水位
 * @example：
 * 	result, err := client.Star.Join(ctx, licence.StarJoinInput{
 * 		Name: "兔子舰", Endpoint: "grpc://ship.example.com:9000", Pubkey: pubkeyHex,
 * 	})
 */
func (this *StarService) Join(ctx context.Context, input StarJoinInput) (StarJoinResult, error) {

	instanceId, err := this.instanceId(input.InstanceId)
	if err != nil {
		return StarJoinResult{}, err
	}
	input.InstanceId = instanceId
	if input.AppVersion = strings.TrimSpace(input.AppVersion); input.AppVersion == "" {
		input.AppVersion = this.client.options.Version
	}
	body, err := json.Marshal(input)
	if err != nil {
		return StarJoinResult{}, err
	}
	data, err := this.call(ctx, "/api/v1/star/directory/join", body, instanceId)
	if err != nil {
		return StarJoinResult{}, err
	}
	var payload starJoinPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return StarJoinResult{}, fmt.Errorf("star: join 响应解析失败：%w", err)
	}
	return StarJoinResult{StarStatus: payload.StarStatus, TrustScore: payload.TrustScore, Since: payload.Since}, nil
}

// StarPeer - 目录同伴条目
type StarPeer struct {
	// InstanceId - 实例标识
	InstanceId string `json:"instanceId"`
	// Name - 舰名
	Name string `json:"name"`
	// Endpoint - 舰际 gRPC 地址
	Endpoint string `json:"endpoint"`
	// Pubkey - 实例 Ed25519 公钥 hex
	Pubkey string `json:"pubkey"`
	// StarStatus - 仲裁资格态（active/suspended/left）
	StarStatus string `json:"starStatus"`
	// TrustScore - 信用分（万分位 0~10000）
	TrustScore int `json:"trustScore"`
	// AppVersion - im 版本
	AppVersion string `json:"appVersion"`
	// LastBeatAt - 最近心跳（毫秒戳）
	LastBeatAt int64 `json:"lastBeatAt"`
}

// StarPeersResult - 目录增量拉取回执
type StarPeersResult struct {
	// Peers - 自 since 以来变更的同伴
	Peers []StarPeer
	// Since - 新目录水位（毫秒戳；下次增量拉取的上送值）
	Since int64
}

// starPeersPayload - peers 响应 data
type starPeersPayload struct {
	Peers []StarPeer `json:"peers"`
	Since int64      `json:"since"`
}

// ListPeers - 目录增量拉取（只读，不带幂等键）：since 为水位线（上次返回的 Since；0 = 全量）。
/**
 * @param ctx context.Context - 调用上下文
 * @param since int64 - 目录水位（毫秒戳；0 = 全量）
 * @return StarPeersResult - 变更同伴 + 新水位
 * @example：
 * 	result, err := client.Star.ListPeers(ctx, 0)
 */
func (this *StarService) ListPeers(ctx context.Context, since int64) (StarPeersResult, error) {

	body, err := json.Marshal(map[string]any{"since": since})
	if err != nil {
		return StarPeersResult{}, err
	}
	data, err := this.call(ctx, "/api/v1/star/directory/peers", body, "")
	if err != nil {
		return StarPeersResult{}, err
	}
	var payload starPeersPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return StarPeersResult{}, fmt.Errorf("star: peers 响应解析失败：%w", err)
	}
	return StarPeersResult{Peers: payload.Peers, Since: payload.Since}, nil
}
