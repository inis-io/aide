package s3

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ============================= 桶通知配置 - 开始 =============================

const (
	// maxNotificationRules - 事件通知规则数上限（网关约束 0-100；0 = 清除配置）
	maxNotificationRules = 100
)

// 事件名常量（平台当前支持的两类通配）
const (
	// EventObjectCreated - 对象创建事件（上传 / 拷贝 / 分片完成触发）
	EventObjectCreated = "s3:ObjectCreated:*"
	// EventObjectRemoved - 对象删除事件（软删进回收站触发）
	EventObjectRemoved = "s3:ObjectRemoved:*"
)

// NotificationRule - 事件通知规则（Topic = Webhook 目标 URL）
type NotificationRule struct {
	// Id - 规则标识（空 = 服务端生成 UUID）
	Id string
	// Topic - Webhook 目标地址（http/https）
	Topic string
	// Events - 订阅事件名列表（取 EventObjectCreated / EventObjectRemoved）
	Events []string
	// Secret - 投递签名密钥（空 = 服务端生成；GET 回读可见，供接收方验签配置）
	Secret string
}

// GetBucketNotification - 取桶事件通知配置（GET /{bucket}?notification；未配置返回空列表，AWS 语义不 404）
// 返回规则含服务端生成的 Id 与 Secret（限属主签名端点），Secret 即接收方验签密钥。
func (this *Client) GetBucketNotification(ctx context.Context, bucket string) ([]NotificationRule, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, query: map[string]string{"notification": ""}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlNotificationConfiguration
	if err = xml.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：GetBucketNotification 响应解析失败（%v）", ErrTransport, err)
	}

	rules := make([]NotificationRule, 0, len(raw.Topics))
	for _, item := range raw.Topics {
		rules = append(rules, NotificationRule{
			Id:     item.Id,
			Topic:  item.Topic,
			Events: item.Events,
			Secret: item.Secret,
		})
	}
	return rules, nil
}

// PutBucketNotification - 写桶事件通知配置（PUT /{bucket}?notification，整组替换语义；空列表 = 清除）
func (this *Client) PutBucketNotification(ctx context.Context, bucket string, rules []NotificationRule) error {

	if err := checkNotificationRules(rules); err != nil {
		return err
	}

	raw := xmlNotificationConfiguration{Topics: make([]xmlNotificationTopic, 0, len(rules))}
	for _, rule := range rules {
		raw.Topics = append(raw.Topics, xmlNotificationTopic{
			Id:     rule.Id,
			Topic:  rule.Topic,
			Events: rule.Events,
			Secret: rule.Secret,
		})
	}

	return this.putBucketConfig(ctx, bucket, "notification", raw)
}

// DeleteBucketNotification - 清除桶事件通知配置（PUT 空配置语义，AWS 无独立 Delete 操作）
func (this *Client) DeleteBucketNotification(ctx context.Context, bucket string) error {

	if bucket == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}
	return this.putBucketConfig(ctx, bucket, "notification", xmlNotificationConfiguration{})
}

// checkNotificationRules - 事件通知规则集本地校验（条数界内、目标 http(s)、事件名合法非空）
func checkNotificationRules(rules []NotificationRule) error {

	if len(rules) > maxNotificationRules {
		return fmt.Errorf("%w：事件通知规则数不得超过 %d 条", ErrInvalidRequest, maxNotificationRules)
	}
	for i, rule := range rules {
		if !strings.HasPrefix(rule.Topic, "https://") && !strings.HasPrefix(rule.Topic, "http://") {
			return fmt.Errorf("%w：第 %d 条通知规则 Topic 须为 http(s) 地址", ErrInvalidRequest, i+1)
		}
		if len(rule.Events) == 0 {
			return fmt.Errorf("%w：第 %d 条通知规则 Events 不能为空", ErrInvalidRequest, i+1)
		}
		for _, name := range rule.Events {
			if name != EventObjectCreated && name != EventObjectRemoved {
				return fmt.Errorf("%w：第 %d 条通知规则含不支持的事件名 %q", ErrInvalidRequest, i+1, name)
			}
		}
	}
	return nil
}

// ============================= 桶通知配置 - 结束 =============================
