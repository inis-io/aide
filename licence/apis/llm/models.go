package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ============================= 模型目录 =============================

// Model - 模型对象（OpenAI 兼容；仅含已上架且本 key 白名单允许的模型）
type Model struct {
	// Id - 模型 id（Chat/ChatStream 的 Model 字段取值）
	Id string `json:"id"`
	// Object - 对象类型（model）
	Object string `json:"object"`
	// Created - 创建时间（秒级时间戳）
	Created int64 `json:"created"`
	// OwnedBy - 归属方（平台固定标识）
	OwnedBy string `json:"owned_by"`
}

// modelList - GET /v1/models 响应信封（OpenAI list 对象）
type modelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Models - 模型目录（GET /models，启动期校验模型可用性用）。
/**
 * @param ctx context.Context - 调用上下文
 * @param requestId ...string - 可选调用级幂等键
 * @return []Model - 在售且本 key 可用的模型清单
 * @return error - 非 2xx 为 *Error
 * @example：
 * 	models, err := client.Models(ctx)
 * 	for _, item := range models { fmt.Println(item.Id) }
 */
func (this *Client) Models(ctx context.Context, requestId ...string) ([]Model, error) {

	resp, err := this.do(ctx, http.MethodGet, "/models", nil, variadicRequestId(requestId))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp)
	}
	defer resp.Body.Close()
	var list modelList
	if err = json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("llm: 模型目录解析失败：%w", err)
	}
	return list.Data, nil
}

// Model - 单个模型详情（GET /models/{id}；不存在/已下架/不在白名单返回
// *Error（ErrorCodeModelNotFound））。
/**
 * @param ctx context.Context - 调用上下文
 * @param id string - 模型 id
 * @param requestId ...string - 可选调用级幂等键
 * @return *Model - 模型详情
 * @return error - 非 2xx 为 *Error
 */
func (this *Client) Model(ctx context.Context, id string, requestId ...string) (*Model, error) {

	resp, err := this.do(ctx, http.MethodGet, "/models/"+url.PathEscape(id), nil, variadicRequestId(requestId))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(resp)
	}
	defer resp.Body.Close()
	var result Model
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("llm: 模型详情解析失败：%w", err)
	}
	return &result, nil
}
