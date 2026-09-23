package callback

import (
	"context"
	"errors"
	"sync"
	"time"

	LicenceRuntime "github.com/inis-io/aide/licence/runtime"
)

// EventPuller - 事件拉取方窄接口（消费方定义，避免 callback 反向依赖具体客户端实现）。
// *LicenceRuntime.Client 的 PullEvents 方法天然满足本接口；测试可注入假实现。
type EventPuller interface {
	// PullEvents - 一轮事件拉取（长轮询/流式），返回按 eventId 升序的已接收事件批次；
	// 非放行态/服务端故障/传输错误均以 error 返回
	PullEvents(ctx context.Context, sinceEventId int64, hold time.Duration) ([]LicenceRuntime.SubscribedEvent, error)
}

// publicKeysProvider - 拉取方可选实现的公钥提供者（*LicenceRuntime.Client.PublicKeys 满足）；
// 命中时订阅器强制复用其公钥（镜像旧 Client.Subscribe 语义：订阅验签公钥与客户端同源）
type publicKeysProvider interface {
	PublicKeys() map[string]string
}

// EventSubscriber - 项目事件订阅器：拉取项目 callback_events 并复用 CallbackHandler 分发管线。
// 订阅信封由平台现场重签（occurredAt 重新盖戳、nonce 每次新鲜、deliveryNo 稳定为 SUB-{eventNo}），
// 客户端按 eventId 单调推进水位；收到事件后通常调 TenantSync / PlatformConfigSync 拉取完整信封（推送即失效信号）。
type EventSubscriber struct {
	puller    EventPuller
	handler   *CallbackHandler
	mu        sync.Mutex
	watermark int64         // 已成功分发事件的 max eventId（callback_events.id）
	hold      time.Duration // 单轮长轮询 hold（0 用默认 15s，传输层再收敛到各自超时）
}

// NewEventSubscriber - 创建事件订阅器（puller 通常为 *LicenceRuntime.Client；
// puller 实现 PublicKeys() 时验签公钥强制取自 puller，否则用 options.PublicKeys；
// options 可覆盖 TimeWindow/DedupTTL 等）。
/**
 * @example：
 * 	sub := callback.NewEventSubscriber(client, callback.CallbackOptions{}).
 * 		OnEvent(callback.EventPlatformConfigUpdated, func(ctx context.Context, event *callback.CallbackEvent) (callback.Ack, error) {
 * 			return callback.AckSuccess, client.PlatformConfigSync(ctx)
 * 		})
 * 	go sub.Run(ctx)
 */
func NewEventSubscriber(puller EventPuller, options CallbackOptions) *EventSubscriber {
	if provider, ok := puller.(publicKeysProvider); ok {
		options.PublicKeys = provider.PublicKeys()
	}
	return &EventSubscriber{
		puller:  puller,
		handler: NewCallbackHandler(options),
		hold:    15 * time.Second,
	}
}

// OnEvent - 注册精确事件或前缀通配处理器（与 CallbackHandler.OnEvent 同语义）。
func (this *EventSubscriber) OnEvent(event string, fn CallbackFunc) *EventSubscriber {
	this.handler.OnEvent(event, fn)
	return this
}

// OnAny - 注册未匹配事件的兜底处理器。
func (this *EventSubscriber) OnAny(fn CallbackFunc) *EventSubscriber {
	this.handler.OnAny(fn)
	return this
}

// Watermark - 当前已消费水位（callback_events.id）。
func (this *EventSubscriber) Watermark() int64 {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.watermark
}

// SetWatermark - 设置初始水位（新订阅器跳过头历史事件时调用，需在 Poll 前设置）。
func (this *EventSubscriber) SetWatermark(id int64) {
	this.mu.Lock()
	defer this.mu.Unlock()
	this.watermark = id
}

// Poll - 一轮长轮询/流式拉取并分发，返回已成功分发条数。
// 事件按 eventId 升序消费：success/ok/ignored 视为已消费并推进水位；
// retry/rejected 或验签失败则停在该事件不推进（下轮重拉，deliveryNo 去重 TTL 内不重复分发）。
// Poll 不持有客户端操作锁，长轮询 hold 不会阻塞 validate/activate 刷新循环。
func (this *EventSubscriber) Poll(ctx context.Context) (int, error) {
	this.mu.Lock()
	watermark := this.watermark
	hold := this.hold
	this.mu.Unlock()
	if hold <= 0 {
		hold = 15 * time.Second
	}

	events, err := this.puller.PullEvents(ctx, watermark, hold)
	if err != nil {
		return 0, err
	}

	delivered := 0
	advance := watermark
	for _, item := range events {
		if item.EventId <= advance {
			continue
		}
		envelope, rawPayload, parseErr := ParseCallbackEnvelope(item.Envelope)
		if parseErr != nil {
			return delivered, parseErr
		}
		if parseErr = validateCallbackEnvelope(envelope); parseErr != nil {
			return delivered, parseErr
		}
		ack, dispatchErr := this.dispatch(ctx, envelope, rawPayload)
		if dispatchErr != nil {
			return delivered, dispatchErr
		}
		switch ack {
		case AckSuccess, AckOk, AckIgnored:
			advance = item.EventId
			delivered++
		default: // AckRetry / AckRejected：不推进水位，下轮重拉该事件及之后
			return delivered, nil
		}
	}
	this.mu.Lock()
	if advance > this.watermark {
		this.watermark = advance
	}
	this.mu.Unlock()
	return delivered, nil
}

// dispatch - 复用 CallbackHandler 分发内核，把业务回调 panic 转换为 error（不推进水位）。
func (this *EventSubscriber) dispatch(ctx context.Context, envelope CallbackEnvelope, rawPayload []byte) (ack Ack, err error) {
	defer func() {
		if recover() != nil {
			ack = ""
			err = errors.New("订阅事件分发 panic")
		}
	}()
	return this.handler.dispatchEnvelope(ctx, envelope, rawPayload)
}

// Run - 循环 Poll 直到 ctx 取消（便捷方法）；Poll 返回错误时直接透出，由调用方决定是否重启。
func (this *EventSubscriber) Run(ctx context.Context) error {
	for {
		if _, err := this.Poll(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}
