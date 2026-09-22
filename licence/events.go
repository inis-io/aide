package licence

import (
	"context"
	"errors"
	"time"
)

// 事件订阅的客户端侧只剩两个原语：PullEvents（一轮拉取，含时钟校正与放行态闸门）与
// PublicKeys（验签公钥副本）。订阅器（水位推进、去重、前缀通配分发）位于 callback 子包
// （callback.EventSubscriber，经 callback.NewEventSubscriber(client, options) 创建）——
// 因订阅器复用 callback.CallbackHandler 分发内核而 callback 又须 import 根包验签，
// 为保持依赖方向（callback → licence），订阅器整体迁出，本文件不再提供 Client.Subscribe。

// PullEvents - 一轮事件拉取（HTTP 长轮询单次 POST / gRPC 服务端流收齐），返回按 eventId 升序的事件批次。
// 信封为平台现场重签的 CallbackEnvelope JSON 原文（occurredAt 重新盖戳、nonce 每次新鲜、
// deliveryNo 稳定为 SUB-{eventNo}），消费方基于 payload 原文验签。
// 时钟校正（updateClockOffset）与状态闸门（StatusError/非放行态 → error）在本方法内完成；
// hold <= 0 时默认 15s（传输层再收敛到各自超时）。未 Start（传输层未初始化）返回错误。
// SDK 使用方通常经 callback.NewEventSubscriber 消费本方法，而非直接调用。
func (this *Client) PullEvents(ctx context.Context, sinceEventId int64, hold time.Duration) ([]SubscribedEvent, error) {

	if hold <= 0 {
		hold = 15 * time.Second
	}
	if this.transport == nil {
		return nil, errors.New("运行面传输层未初始化")
	}
	result, err := this.transport.SubscribeEvents(ctx, this.options.LicenseNo, sinceEventId, hold)
	if err != nil {
		return nil, err
	}
	this.updateClockOffset(result.ServerTime)
	if result.Status == StatusError {
		return nil, errors.New("服务端故障：" + result.Message)
	}
	if !passThrough(result.Status) {
		return nil, errors.New("许可证非放行态：" + result.Status)
	}
	return result.Events, nil
}

// PublicKeys - 返回平台公钥副本（keyVersion → hex 公钥），供回调/订阅验签复用。
// 返回副本而非内部 map 引用，调用方修改不影响客户端。
func (this *Client) PublicKeys() map[string]string {

	publicKeys := make(map[string]string, len(this.options.PublicKeys))
	for version, key := range this.options.PublicKeys {
		publicKeys[version] = key
	}
	return publicKeys
}
