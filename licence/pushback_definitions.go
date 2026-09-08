package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 配置定义反推（Config Definition Pushback）：把「分组树 + 配置项定义」的客户端权威全量快照
// 推送回平台，平台按项目开关闸门（未开启 → 403）与定义结构校验（失败 → 400 + errors 明细）
// 预检后，对分组与配置项分别 diff 落库（created/updated/deleted/unchanged）并写只追加审计。
// 与值回推（pushback.go）正交：定义反推不触碰任何配置值，值回推不修改定义。
// 仅项目级（租户层只有值覆盖，无定义）；幂等语义与值回推一致：快照幂等 + client_push_id 批次去重。

// ConfigDefinitionGroup - 配置分组定义（客户端权威；Parent 为父分组 name 路径，空 = 顶级）
type ConfigDefinitionGroup struct {
	// Name - 分组名（单段，正则同配置键：^[a-z0-9][a-z0-9._-]{0,127}$）
	Name string `json:"name"`
	// Label - 分组显示名（中文）
	Label string `json:"label"`
	// LabelEn - 分组显示名（英文，可选）
	LabelEn string `json:"label_en"`
	// Icon - 分组图标（可选）
	Icon string `json:"icon"`
	// Sort - 同级排序（小值在前）
	Sort int `json:"sort"`
	// Parent - 父分组 name 路径（斜杠分隔，如 "email"），空 = 顶级分组
	Parent string `json:"parent"`
}

// ConfigDefinitionItem - 配置项定义（客户端权威）
type ConfigDefinitionItem struct {
	// Key - 配置键（正则 ^[a-z0-9][a-z0-9._-]{0,127}$，项目内唯一）
	Key string `json:"key"`
	// Label - 显示名（必填，≤100 字符）
	Label string `json:"label"`
	// Type - 控件/值类型（SDK 预校验不限制白名单，平台侧以 Hub 控件白名单兜底）
	Type string `json:"type"`
	// GroupPath - 所属分组 name 路径（斜杠分隔，如 "email/smtp"），空 = 未分组
	GroupPath string `json:"group_path"`
	// Options - 选项数组 JSON 原文（[{"value":"local","label":"本地存储"}]），空 = null；select 必填
	Options json.RawMessage `json:"options"`
	// Rules - 校验规则集 JSON 原文（RuleSet 对象，见 config-validate.go），空 = null
	Rules json.RawMessage `json:"rules"`
	// Placeholder - 输入占位文本（可选）
	Placeholder string `json:"placeholder"`
	// Remark - 备注说明（可选）
	Remark string `json:"remark"`
	// DefaultValue - 默认值（可选；非空时须通过自身 type + rules 校验，select 须 ∈ options）
	DefaultValue string `json:"default_value"`
	// Sensitive - 敏感键（值回推只回推脱敏值，平台与 SDK 校验均跳过真值校验）
	Sensitive bool `json:"sensitive"`
	// Sort - 组内排序（小值在前）
	Sort int `json:"sort"`
}

// ConfigDefinitions - 项目级配置定义全量快照（分组树 + 配置项定义）
type ConfigDefinitions struct {
	// Groups - 分组定义列表（parent 链必须在快照内闭合）
	Groups []ConfigDefinitionGroup `json:"groups"`
	// Configs - 配置项定义列表（key 快照内唯一）
	Configs []ConfigDefinitionItem `json:"configs"`
}

// PushbackDiffStats - 分组/配置项各自的 diff 计数
type PushbackDiffStats struct {
	// Created - 快照中存在、库中不存在而新增的条数
	Created int `json:"created"`
	// Updated - 两侧都存在、载荷字段不同而更新的条数
	Updated int `json:"updated"`
	// Deleted - 库中存在、快照中缺失而删除的条数（platform_owned 行不参与）
	Deleted int `json:"deleted"`
	// Unchanged - 完全一致未变的条数（不产生审计）
	Unchanged int `json:"unchanged"`
}

// PushbackDefinitionsResult - 配置定义反推批次结果
type PushbackDefinitionsResult struct {
	// BatchID - 平台批次号（pushback_batches.id，kind=definitions）
	BatchID uint64
	// Groups - 分组 diff 计数
	Groups PushbackDiffStats
	// Configs - 配置项 diff 计数
	Configs PushbackDiffStats
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
	Errors []ConfigValidationError
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
	TenantID     uint64                  `json:"tenant_id"`
	Groups       []ConfigDefinitionGroup `json:"groups"`
	Configs      []ConfigDefinitionItem  `json:"configs"`
	ClientPushID string                  `json:"client_push_id"`
}

// pushbackDefinitionsResponse - 定义反推响应（message/errors 仅在 400/403 等异常时携带）
type pushbackDefinitionsResponse struct {
	BatchID  uint64                  `json:"batch_id"`
	Groups   PushbackDiffStats       `json:"groups"`
	Configs  PushbackDiffStats       `json:"configs"`
	PushedAt string                  `json:"pushed_at"`
	Message  string                  `json:"message"`
	Errors   []ConfigValidationError `json:"errors"`
}

// PushConfigDefinitions - 回推项目级配置定义全量快照（分组树 + 配置项定义；仅项目级，租户层无定义）。
// 推送前可先用 Client.ValidateConfigDefinitions 做本地预校验（CI 拦错）。
// 平台错误语义：404 模糊 NotFound；401 签名闸门；403 项目未开启定义反推模式；
// 400 定义校验失败（返回 *PushbackDefinitionsError，含逐条明细）。
/**
 * @param defs ConfigDefinitions - 项目级完整定义快照；空快照 = 清空该项目非 platform_owned 的定义
 * @param clientPushID ...string - 可选批次幂等号（缺省 SDK 自动生成 UUIDv4，经 PushbackDefinitionsResult.ClientPushID 返回）
 * @example：
 * 	result, err := client.PushConfigDefinitions(ctx, licence.ConfigDefinitions{
 * 		Groups:  []licence.ConfigDefinitionGroup{{Name: "storage", Label: "存储", Sort: 1}},
 * 		Configs: []licence.ConfigDefinitionItem{{Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage", Options: json.RawMessage(`[{"value":"local","label":"本地存储"}]`)}},
 * 	})
 */
func (this *Client) PushConfigDefinitions(ctx context.Context, defs ConfigDefinitions, clientPushID ...string) (*PushbackDefinitionsResult, error) {

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
		defs.Groups = []ConfigDefinitionGroup{}
	}
	if defs.Configs == nil {
		defs.Configs = []ConfigDefinitionItem{}
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
