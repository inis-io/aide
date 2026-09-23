package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/inis-io/aide/licence/config"
	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// 配置回推（Config Pushback）：客户端 → 平台方向，与平台配置下发（PlatformConfigSync）互不混用。
// 调用方把某一作用域（项目级 / 租户级）的完整配置快照推送回平台，平台将该快照与库存做 diff
// （新增/更新/删除/不变）落库并写只追加审计；快照语义天然幂等，重复推送同一快照全部 unchanged。
// 不做 SDK 侧自动定时推送（YAGNI），推送时机由调用方决定；写失败不自动跨协议回退/重试。

// PushbackResult - 配置回推批次结果
type PushbackResult struct {
	// BatchID - 平台批次号（pushback_batches.id，即 push_version）
	BatchID uint64
	// Created - 快照中存在、库中不存在而新增的键数
	Created int
	// Updated - 两侧都存在、值不同而更新的键数
	Updated int
	// Deleted - 库中存在、快照中缺失而删除的键数
	Deleted int
	// Unchanged - 值相同未变的键数（不产生审计）
	Unchanged int
	// PushedAt - 平台批次落库时间
	PushedAt time.Time
	// ClientPushID - 本批次幂等号（未显式传入时由 SDK 自动生成的 UUIDv4）
	ClientPushID string
}

// pushbackBody - 配置回推请求体（与平台契约一致：snake_case 字段，整体走 activation-sign-v1 签名）
type pushbackBody struct {
	TenantID     uint64            `json:"tenant_id"`
	Items        map[string]string `json:"items"`
	ClientPushID string            `json:"client_push_id"`
}

// pushbackResponse - 配置回推响应（message 仅在 400 校验失败等异常时携带）
type pushbackResponse struct {
	BatchID   uint64 `json:"batch_id"`
	Created   int    `json:"created"`
	Updated   int    `json:"updated"`
	Deleted   int    `json:"deleted"`
	Unchanged int    `json:"unchanged"`
	PushedAt  string `json:"pushed_at"`
	Message   string `json:"message"`
}

// PushConfig - 回推项目级配置全量快照（客户端权威，平台 diff 落库 + 只追加审计）
/**
 * @param items map[string]string - 项目级完整配置快照（键格式与数量上限由平台校验）
 * @param clientPushID ...string - 可选批次幂等号（缺省 SDK 自动生成 UUIDv4，经 PushbackResult.ClientPushID 返回）
 * @example：
 * 	result, err := client.PushConfig(ctx, map[string]string{"feature.x": "on", "rate.limit": "200"})
 */
func (this *Client) PushConfig(ctx context.Context, items map[string]string, clientPushID ...string) (*PushbackResult, error) {

	return this.pushConfigs(ctx, 0, items, clientPushID...)
}

// PushTenantConfig - 回推指定租户的配置全量快照（平台校验租户确属此项目，不匹配按模糊 NotFound 处理）
/**
 * @param tenantID uint64 - SaaS 租户 ID（必须大于 0）
 * @param items map[string]string - 该租户的完整配置快照
 * @param clientPushID ...string - 可选批次幂等号（缺省 SDK 自动生成 UUIDv4）
 * @example：
 * 	result, err := client.PushTenantConfig(ctx, 88, map[string]string{"theme": "dark"})
 */
func (this *Client) PushTenantConfig(ctx context.Context, tenantID uint64, items map[string]string, clientPushID ...string) (*PushbackResult, error) {

	if tenantID == 0 {
		return nil, errors.New("tenantID 必须大于 0（项目级回推请用 PushConfig）")
	}
	return this.pushConfigs(ctx, tenantID, items, clientPushID...)
}

// pushConfigs - 配置回推统一出口：序列化快照，经 doRequest 走 activation-sign-v1 签名链路。
// 空快照（items 为 nil/空）语义合法：平台会把该作用域库存键全部删除。
func (this *Client) pushConfigs(ctx context.Context, tenantID uint64, items map[string]string, clientPushID ...string) (*PushbackResult, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	pushID := ""
	for _, candidate := range clientPushID {
		if candidate != "" {
			pushID = candidate
			break
		}
	}
	if pushID == "" {
		pushID = newClientPushID()
	}
	if items == nil {
		items = map[string]string{}
	}
	body, err := json.Marshal(pushbackBody{TenantID: tenantID, Items: items, ClientPushID: pushID})
	if err != nil {
		return nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/pushback/configs", body, true)
	if err != nil {
		return nil, err
	}
	// 模糊 NotFound：许可证/实例/项目/租户不存在均不泄露存在性
	if code == http.StatusNotFound {
		return nil, errors.New("许可证、实例或租户信息无效")
	}
	if code == http.StatusUnauthorized {
		return nil, errors.New("配置回推未通过签名闸门")
	}
	var response pushbackResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if code == http.StatusBadRequest {
		return nil, errors.New("配置回推校验失败：" + response.Message)
	}
	if code != http.StatusOK {
		return nil, errors.New("配置回推失败：HTTP " + strconv.Itoa(code))
	}
	pushedAt, err := parsePushbackTime(response.PushedAt)
	if err != nil {
		return nil, err
	}
	return &PushbackResult{
		BatchID: response.BatchID, Created: response.Created, Updated: response.Updated,
		Deleted: response.Deleted, Unchanged: response.Unchanged,
		PushedAt: pushedAt, ClientPushID: pushID,
	}, nil
}

// newClientPushID - 生成默认批次幂等号（RFC 4122 UUIDv4；crypto/rand 实现，不引入外部依赖）
func newClientPushID() string {

	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return LicenceProtocol.RandomNonce() // 兜底退化为 16 字节随机 hex，仍满足去重用途
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40 // version 4
	buffer[8] = (buffer[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

// parsePushbackTime - 解析平台批次时间：优先带时区的 RFC3339；
// 兼容平台本地日期时间格式（"2006-01-02 15:04:05"，按项目时区 UTC+8 解释）。
func parsePushbackTime(raw string) (time.Time, error) {

	if raw == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.FixedZone("UTC+8", 8*3600)); err == nil {
		return parsed, nil
	}
	return time.Time{}, errors.New("无法识别的 pushed_at 时间格式：" + raw)
}

// ============================= 配置定义反推校验 =============================

// 配置校验引擎已整体迁往叶子子包 config（全系统唯一实现，SDK 与 licen-hub backend
// 共同 import，禁止在平台侧复制第二份规则引擎）。本文件只保留 Client 上的两个薄壳方法：
//   - ValidateConfig：基于本地平台配置快照做值预校验（快照来自 PlatformConfigSync 缓存）；
//   - ValidateConfigDefinitions：定义快照本地预校验，一行委托 config.ValidateDefinitions。

// ValidateConfig - 基于本地平台配置快照（PlatformConfigSync 缓存）对一组键值做本地预校验。
// 供消费端 CLI 在值回推前 fail-fast；逐 key 校验语义：
//   - 有定义且非敏感 → type 可解析 + RuleSet 校验（select 以 options 为枚举）；
//   - sensitive 定义跳过（回推的是脱敏值而非真值）；
//   - 无定义放行（前向兼容）。
//
// 快照为空（从未 PlatformConfigSync，或项目本就无配置项）时返回 nil：本地无字典可校验，
// 以平台侧校验为准。返回 nil 表示全部通过。
func (this *Client) ValidateConfig(items map[string]string) []config.ConfigValidationError {

	this.mu.RLock()
	snapshot := make(map[string]PlatformConfigItem, len(this.state.PlatformConfigs))
	maps.Copy(snapshot, this.state.PlatformConfigs)
	this.mu.RUnlock()
	if len(snapshot) == 0 {
		return nil
	}

	var failures []config.ConfigValidationError
	for _, key := range slices.Sorted(maps.Keys(items)) {
		definition, exists := snapshot[key]
		if !exists || definition.Sensitive {
			continue
		}
		rules, err := config.ParseConfigRuleSet(definition.Rules)
		if err != nil {
			failures = append(failures, config.ConfigValidationError{Where: key, Message: "定义 rules 解析失败：" + err.Error()})
			continue
		}
		if err = config.ValidateConfigValue(definition.Type, definition.Options, rules, items[key]); err != nil {
			message := err.Error()
			var validationErr *config.ConfigValidationError
			if errors.As(err, &validationErr) {
				message = validationErr.Message
			}
			failures = append(failures, config.ConfigValidationError{Where: key, Message: message})
		}
	}
	return failures
}

// ValidateConfigDefinitions - 定义快照本地预校验（不上平台即可在 CI 拦错）。
// 校验范围：分组 name 正则、name 路径唯一（含大小写冲突）、parent 闭包与无环、
// 配置项 key 正则与快照内唯一、逐条定义结构校验（引擎实现见 config.ValidateDefinitions）。
// type 白名单不限制——平台侧由 backend 注入 Hub 控件白名单兜底。
// 返回 nil 表示全部通过。
func (this *Client) ValidateConfigDefinitions(defs config.ConfigDefinitions) []config.ConfigValidationError {

	return config.ValidateDefinitions(defs)
}

// ============================= 配置定义回推 =============================

// 配置定义反推（Config Definition Pushback）：把「分组树 + 配置项定义」的客户端权威全量快照
// 推送回平台，平台按项目开关闸门（未开启 → 403）与定义结构校验（失败 → 400 + errors 明细）
// 预检后，对分组与配置项分别 diff 落库（created/updated/deleted/unchanged）并写只追加审计。
// 与值回推（pushback.go）正交：定义反推不触碰任何配置值，值回推不修改定义。
// 仅项目级（租户层只有值覆盖，无定义）；幂等语义与值回推一致：快照幂等 + client_push_id 批次去重。
//
// 定义载荷类型（ConfigDefinitions / ConfigDefinitionGroup / ConfigDefinitionItem /
// PushbackDiffStats）与校验引擎同迁叶子子包 config，本文件只保留反推调用与结果/错误组装。

// PushbackDefinitionsResult - 配置定义反推批次结果
type PushbackDefinitionsResult struct {
	// BatchID - 平台批次号（pushback_batches.id，kind=definitions）
	BatchID uint64
	// Groups - 分组 diff 计数
	Groups config.PushbackDiffStats
	// Configs - 配置项 diff 计数
	Configs config.PushbackDiffStats
	// PushedAt - 平台批次落库时间
	PushedAt time.Time
	// ClientPushID - 本批次幂等号（未显式传入时由 SDK 自动生成的 UUIDv4）
	ClientPushID string
}

// PushbackDefinitionsError - 定义反推被平台 400 拒绝：携带逐条校验明细（Where 定位 + Message 原因），
// 供 CLI 打印逐条错误；errors.As 可断言取结构化明细。
type PushbackDefinitionsError struct {
	// Message - 平台汇总消息
	Message string
	// Errors - 逐条校验明细（平台上限 20 条）
	Errors []config.ConfigValidationError
}

// Error - 汇总消息 + 逐条明细拼接（CLI 直接打印 err 即可读全）
func (this *PushbackDefinitionsError) Error() string {

	if len(this.Errors) == 0 {
		return "配置定义反推校验失败：" + this.Message
	}
	parts := make([]string, 0, len(this.Errors))
	for _, item := range this.Errors {
		parts = append(parts, item.Error())
	}
	summary := this.Message
	if summary == "" {
		summary = "定义校验未通过"
	}
	return "配置定义反推校验失败：" + summary + "（" + strings.Join(parts, "；") + "）"
}

// pushbackDefinitionsBody - 定义反推请求体（与平台契约一致：snake_case，options/rules 原生 JSON，
// 整体走 activation-sign-v1 签名；tenant_id 固定 0，定义仅项目级）
type pushbackDefinitionsBody struct {
	TenantID     uint64                         `json:"tenant_id"`
	Groups       []config.ConfigDefinitionGroup `json:"groups"`
	Configs      []config.ConfigDefinitionItem  `json:"configs"`
	ClientPushID string                         `json:"client_push_id"`
}

// pushbackDefinitionsResponse - 定义反推响应（message/errors 仅在 400/403 等异常时携带）
type pushbackDefinitionsResponse struct {
	BatchID  uint64                         `json:"batch_id"`
	Groups   config.PushbackDiffStats       `json:"groups"`
	Configs  config.PushbackDiffStats       `json:"configs"`
	PushedAt string                         `json:"pushed_at"`
	Message  string                         `json:"message"`
	Errors   []config.ConfigValidationError `json:"errors"`
}

// PushConfigDefinitions - 回推项目级配置定义全量快照（分组树 + 配置项定义；仅项目级，租户层无定义）。
// 推送前可先用 Client.ValidateConfigDefinitions 做本地预校验（CI 拦错）。
// 平台错误语义：404 模糊 NotFound；401 签名闸门；403 项目未开启定义反推模式；
// 400 定义校验失败（返回 *PushbackDefinitionsError，含逐条明细）。
/**
 * @param defs config.ConfigDefinitions - 项目级完整定义快照；空快照 = 清空该项目非 platform_owned 的定义
 * @param clientPushID ...string - 可选批次幂等号（缺省 SDK 自动生成 UUIDv4，经 PushbackDefinitionsResult.ClientPushID 返回）
 * @example：
 * 	result, err := client.PushConfigDefinitions(ctx, config.ConfigDefinitions{
 * 		Groups:  []config.ConfigDefinitionGroup{{Name: "storage", Label: "存储", Sort: 1}},
 * 		Configs: []config.ConfigDefinitionItem{{Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage", Options: json.RawMessage(`[{"value":"local","label":"本地存储"}]`)}},
 * 	})
 */
func (this *Client) PushConfigDefinitions(ctx context.Context, defs config.ConfigDefinitions, clientPushID ...string) (*PushbackDefinitionsResult, error) {

	this.opMu.Lock()
	defer this.opMu.Unlock()

	pushID := ""
	for _, candidate := range clientPushID {
		if candidate != "" {
			pushID = candidate
			break
		}
	}
	if pushID == "" {
		pushID = newClientPushID()
	}
	if defs.Groups == nil {
		defs.Groups = []config.ConfigDefinitionGroup{}
	}
	if defs.Configs == nil {
		defs.Configs = []config.ConfigDefinitionItem{}
	}
	body, err := json.Marshal(pushbackDefinitionsBody{TenantID: 0, Groups: defs.Groups, Configs: defs.Configs, ClientPushID: pushID})
	if err != nil {
		return nil, err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/pushback/config-definitions", body, true)
	if err != nil {
		return nil, err
	}
	// 模糊 NotFound：许可证/实例/项目不存在均不泄露存在性
	if code == http.StatusNotFound {
		return nil, errors.New("许可证、实例或项目信息无效")
	}
	if code == http.StatusUnauthorized {
		return nil, errors.New("配置定义反推未通过签名闸门")
	}
	var response pushbackDefinitionsResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	// 项目开关闸门：平台文案原样透传（设计上固定为「项目未开启定义反推模式」）
	if code == http.StatusForbidden {
		if response.Message != "" {
			return nil, errors.New(response.Message)
		}
		return nil, errors.New("项目未开启定义反推模式")
	}
	if code == http.StatusBadRequest {
		return nil, &PushbackDefinitionsError{Message: response.Message, Errors: response.Errors}
	}
	if code != http.StatusOK {
		return nil, errors.New("配置定义反推失败：HTTP " + strconv.Itoa(code))
	}
	pushedAt, err := parsePushbackTime(response.PushedAt)
	if err != nil {
		return nil, err
	}
	return &PushbackDefinitionsResult{
		BatchID: response.BatchID, Groups: response.Groups, Configs: response.Configs,
		PushedAt: pushedAt, ClientPushID: pushID,
	}, nil
}

// rawJSONText - json.RawMessage → gRPC 字符串字段：空/nil/null 归一为 ""（proto 契约：空串 = null）
func rawJSONText(raw json.RawMessage) string {

	text := string(raw)
	if text == "" || text == "null" {
		return ""
	}
	return text
}
