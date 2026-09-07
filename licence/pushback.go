package licence

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
		return randomNonce() // 兜底退化为 16 字节随机 hex，仍满足去重用途
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
