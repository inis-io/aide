package licence

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// runtimeTransport - 运行面底层传输抽象。
type runtimeTransport interface {
	RoundTrip(ctx context.Context, method, requestURI string, body []byte, withSign bool) (int, []byte, error)
	// SubscribeEvents - 一轮事件拉取（HTTP 长轮询单次 POST 解码批次；gRPC 服务端流 Recv 到 EOF 收集成 slice）。
	SubscribeEvents(ctx context.Context, licenseNo string, sinceEventId int64, hold time.Duration) (subscribeResult, error)
	Close() error
}

// SubscribedEvent - 单条订阅事件。
type SubscribedEvent struct {
	// EventId - 平台事件水位（callback_events.id），成功分发后推进
	EventId int64 `json:"eventId"`
	// Envelope - 平台现场重签的 CallbackEnvelope JSON 原文，客户端基于其中 payload 原文验签
	Envelope json.RawMessage `json:"envelope"`
}

// subscribeResult - 一轮事件订阅结果（HTTP 侧 status 来自响应体；gRPC 侧流正常结束即放行态）。
type subscribeResult struct {
	Status     string
	ServerTime int64
	Events     []SubscribedEvent
	Message    string
}

func newRuntimeTransport(client *Client) (runtimeTransport, error) {
	switch client.options.Transport {
	case "", TransportHTTP:
		client.options.Transport = TransportHTTP
		return newHTTPRuntimeTransport(client), nil
	case TransportGRPC:
		return newGRPCRuntimeTransport(client)
	default:
		return nil, errors.New("不支持的运行面传输协议：" + string(client.options.Transport))
	}
}
