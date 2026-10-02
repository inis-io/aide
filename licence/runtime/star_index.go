package runtime

import (
	"context"
	"encoding/json"
	"fmt"
)

// 本文件承载星链中枢热度索引（StarIndex）的 SDK 公开方法：
// PushHotIndex（站内热榜上报，上报即该舰全量快照、整批覆盖，快照幂等）与
// PullGlobalHot（全网榜拉取，score_net 归一降序）。
// 中枢只存索引不存正文；meta/embedding 为原始字节透传（meta 为 JSON 原文，
// embedding 为 float16 压缩向量），客户端与服务端都不做重序列化解释。

// StarHotIndexItem - 站内热榜条目
type StarHotIndexItem struct {
	// RemoteNoteId - 源站笔记标识
	RemoteNoteId string `json:"remoteNoteId"`
	// Percentile - 站内百分位（万分位 0~10000）
	Percentile int `json:"percentile"`
	// Category - 分类过滤标签（可空）
	Category string `json:"category,omitempty"`
	// Language - 语言过滤标签（可空）
	Language string `json:"language,omitempty"`
	// Meta - 摘要元数据 JSON 原文（标题/摘要/作者快照/统计快照/origin_url），原样透传
	Meta json.RawMessage `json:"meta,omitempty"`
	// Embedding - float16 压缩笔记向量（联邦召回用，可空）
	Embedding []byte `json:"embedding,omitempty"`
	// PublishedAt - 笔记发布时间（毫秒戳，freshness 半衰输入）
	PublishedAt int64 `json:"publishedAt"`
}

// StarPushHotIndexInput - 热榜上报入参
type StarPushHotIndexInput struct {
	// InstanceId - 来源舰实例标识（缺省取 Options.InstanceNo）
	InstanceId string
	// Items - 热榜条目批（整批覆盖该舰索引；空批 = 清空该舰索引）
	Items []StarHotIndexItem
}

// starPushHotIndexBody - 上报请求体（InstanceId 归一后落位）
type starPushHotIndexBody struct {
	InstanceId string             `json:"instanceId"`
	Items      []StarHotIndexItem `json:"items"`
}

// starPushPayload - 上报响应 data
type starPushPayload struct {
	Accepted int `json:"accepted"`
}

// PushHotIndex - 站内热榜上报（快照幂等，不带幂等键；返回受理条目数）。
/**
 * @param ctx context.Context - 调用上下文
 * @param input StarPushHotIndexInput - 来源舰与条目批
 * @return int - 受理条目数
 * @example：
 * 	accepted, err := client.Star.PushHotIndex(ctx, licence.StarPushHotIndexInput{Items: items})
 */
func (this *StarService) PushHotIndex(ctx context.Context, input StarPushHotIndexInput) (int, error) {

	instanceId, err := this.instanceId(input.InstanceId)
	if err != nil {
		return 0, err
	}
	body, err := json.Marshal(starPushHotIndexBody{InstanceId: instanceId, Items: input.Items})
	if err != nil {
		return 0, err
	}
	data, err := this.call(ctx, "/api/v1/star/index/push", body, "")
	if err != nil {
		return 0, err
	}
	var payload starPushPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return 0, fmt.Errorf("star: push 响应解析失败：%w", err)
	}
	return payload.Accepted, nil
}

// StarPullGlobalHotInput - 全网榜拉取入参
type StarPullGlobalHotInput struct {
	// TopK - 拉取条数上限（服务端默认 20、上限 100）
	TopK int `json:"topK"`
	// Category - 分类过滤（空 = 不过滤）
	Category string `json:"category,omitempty"`
	// Language - 语言过滤（空 = 不过滤）
	Language string `json:"language,omitempty"`
}

// StarGlobalHotItem - 全网榜条目（score_net 归一降序）
type StarGlobalHotItem struct {
	// InstanceId - 来源舰实例标识
	InstanceId string `json:"instanceId"`
	// RemoteNoteId - 源站笔记标识
	RemoteNoteId string `json:"remoteNoteId"`
	// ScoreNet - 归一热度分（万分位；percentile × 半衰 × trust，pull 时计算）
	ScoreNet int64 `json:"scoreNet"`
	// Meta - 摘要元数据 JSON 原文
	Meta json.RawMessage `json:"meta,omitempty"`
	// Embedding - float16 压缩笔记向量（可空）
	Embedding []byte `json:"embedding,omitempty"`
	// OriginUrl - 源站笔记链接
	OriginUrl string `json:"originUrl"`
	// PublishedAt - 笔记发布时间（毫秒戳）
	PublishedAt int64 `json:"publishedAt"`
}

// starPullPayload - 全网榜响应 data
type starPullPayload struct {
	Items []StarGlobalHotItem `json:"items"`
}

// PullGlobalHot - 全网榜拉取（只读，不带幂等键；score_net 降序）。
/**
 * @param ctx context.Context - 调用上下文
 * @param input StarPullGlobalHotInput - topK 与分类/语言过滤
 * @return []StarGlobalHotItem - 全网榜条目
 * @example：
 * 	items, err := client.Star.PullGlobalHot(ctx, licence.StarPullGlobalHotInput{TopK: 20})
 */
func (this *StarService) PullGlobalHot(ctx context.Context, input StarPullGlobalHotInput) ([]StarGlobalHotItem, error) {

	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	data, err := this.call(ctx, "/api/v1/star/index/pull", body, "")
	if err != nil {
		return nil, err
	}
	var payload starPullPayload
	if err = json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("star: pull 响应解析失败：%w", err)
	}
	return payload.Items, nil
}
