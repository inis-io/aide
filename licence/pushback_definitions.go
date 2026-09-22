package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/inis-io/aide/licence/configdef"
)

// 配置定义反推（Config Definition Pushback）：把「分组树 + 配置项定义」的客户端权威全量快照
// 推送回平台，平台按项目开关闸门（未开启 → 403）与定义结构校验（失败 → 400 + errors 明细）
// 预检后，对分组与配置项分别 diff 落库（created/updated/deleted/unchanged）并写只追加审计。
// 与值回推（pushback.go）正交：定义反推不触碰任何配置值，值回推不修改定义。
// 仅项目级（租户层只有值覆盖，无定义）；幂等语义与值回推一致：快照幂等 + client_push_id 批次去重。
//
// 定义载荷类型（ConfigDefinitions / ConfigDefinitionGroup / ConfigDefinitionItem /
// PushbackDiffStats）与校验引擎同迁叶子子包 configdef，本文件只保留反推调用与结果/错误组装。

// PushbackDefinitionsResult - 配置定义反推批次结果
type PushbackDefinitionsResult struct {
	// BatchID - 平台批次号（pushback_batches.id，kind=definitions）
	BatchID uint64
	// Groups - 分组 diff 计数
	Groups configdef.PushbackDiffStats
	// Configs - 配置项 diff 计数
	Configs configdef.PushbackDiffStats
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
	Errors []configdef.ConfigValidationError
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
	TenantID     uint64                             `json:"tenant_id"`
	Groups       []configdef.ConfigDefinitionGroup  `json:"groups"`
	Configs      []configdef.ConfigDefinitionItem   `json:"configs"`
	ClientPushID string                             `json:"client_push_id"`
}

// pushbackDefinitionsResponse - 定义反推响应（message/errors 仅在 400/403 等异常时携带）
type pushbackDefinitionsResponse struct {
	BatchID  uint64                            `json:"batch_id"`
	Groups   configdef.PushbackDiffStats       `json:"groups"`
	Configs  configdef.PushbackDiffStats       `json:"configs"`
	PushedAt string                            `json:"pushed_at"`
	Message  string                            `json:"message"`
	Errors   []configdef.ConfigValidationError `json:"errors"`
}

// PushConfigDefinitions - 回推项目级配置定义全量快照（分组树 + 配置项定义；仅项目级，租户层无定义）。
// 推送前可先用 Client.ValidateConfigDefinitions 做本地预校验（CI 拦错）。
// 平台错误语义：404 模糊 NotFound；401 签名闸门；403 项目未开启定义反推模式；
// 400 定义校验失败（返回 *PushbackDefinitionsError，含逐条明细）。
/**
 * @param defs configdef.ConfigDefinitions - 项目级完整定义快照；空快照 = 清空该项目非 platform_owned 的定义
 * @param clientPushID ...string - 可选批次幂等号（缺省 SDK 自动生成 UUIDv4，经 PushbackDefinitionsResult.ClientPushID 返回）
 * @example：
 * 	result, err := client.PushConfigDefinitions(ctx, configdef.ConfigDefinitions{
 * 		Groups:  []configdef.ConfigDefinitionGroup{{Name: "storage", Label: "存储", Sort: 1}},
 * 		Configs: []configdef.ConfigDefinitionItem{{Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage", Options: json.RawMessage(`[{"value":"local","label":"本地存储"}]`)}},
 * 	})
 */
func (this *Client) PushConfigDefinitions(ctx context.Context, defs configdef.ConfigDefinitions, clientPushID ...string) (*PushbackDefinitionsResult, error) {

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
		defs.Groups = []configdef.ConfigDefinitionGroup{}
	}
	if defs.Configs == nil {
		defs.Configs = []configdef.ConfigDefinitionItem{}
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
