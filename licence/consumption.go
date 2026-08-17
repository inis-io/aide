package licence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// 平台配置读取埋点与消费上报。
// PlatformConfig/PlatformConfigMust 读命中后 trackConfigConsumption 累计计数（仅互斥 map 自增，
// 不阻塞读取）；后台 consumptionLoop 按间隔（默认 30s，累计达 50 个 key 提前触发）批量上报
// /api/v1/platform/configs/consume，经 transport.RoundTrip 统一分发 HTTP/gRPC 双协议，gate 验签防重放。
// best-effort：失败静默并保留计数下轮重试；不上报成功/失败到调用方，不污染授权刷新循环的退避状态。

// consumptionKeyThreshold - 待上报 key 数达到该值提前触发一次上报（防高并发读积压）
const consumptionKeyThreshold = 50

// platformConfigConsumeBody - 配置消费上报请求体（与平台 types.PlatformConfigConsume 契约一致）
type platformConfigConsumeBody struct {
	LicenseNo string                      `json:"licenseNo"`
	Items     []platformConfigConsumeItem `json:"items"`
}

// platformConfigConsumeItem - 单配置键消费计数
type platformConfigConsumeItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// platformConfigConsumeResponse - 配置消费上报响应
type platformConfigConsumeResponse struct {
	Status     string `json:"status"`
	ServerTime int64  `json:"serverTime"`
	Message    string `json:"message"`
}

// trackConfigConsumption - 记录一次配置 key 读取（仅在开启埋点且读取命中时调用）。
func (this *Client) trackConfigConsumption(key string) {
	if this.options.DisableConsumptionReport {
		return
	}
	this.consumptionMu.Lock()
	this.pendingConsumption[key]++
	trigger := len(this.pendingConsumption) >= consumptionKeyThreshold
	this.consumptionMu.Unlock()
	// 达到阈值时异步提前冲刷（flush 内部 CAS 防并发），避免等待下个 ticker 的空窗期
	if trigger {
		go this.flushConsumption(context.Background())
	}
}

// consumptionLoop - 后台消费上报循环（Start 启动，与授权刷新循环独立；停用开关时不启动）。
func (this *Client) consumptionLoop(ctx context.Context) {
	ticker := time.NewTicker(this.options.ConsumptionReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// 退出前尽力冲刷一次，避免已累计计数滞留
			this.flushConsumption(context.Background())
			return
		case <-ticker.C:
			this.flushConsumption(context.Background())
		}
	}
}

// flushConsumption - 冲刷一次待上报消费：CAS 防并发，snapshot 后上报，成功才扣减已上报部分。
// 失败静默（计数保留待下轮重试）；期间新增的读取计数不受影响。
func (this *Client) flushConsumption(ctx context.Context) {
	if this.options.DisableConsumptionReport {
		return
	}
	if !this.flushing.CompareAndSwap(false, true) {
		return // 已有上报在跑，下轮再冲刷
	}
	defer this.flushing.Store(false)

	this.consumptionMu.Lock()
	if len(this.pendingConsumption) == 0 {
		this.consumptionMu.Unlock()
		return
	}
	items := make([]platformConfigConsumeItem, 0, len(this.pendingConsumption))
	for key, count := range this.pendingConsumption {
		items = append(items, platformConfigConsumeItem{Key: key, Count: count})
	}
	this.consumptionMu.Unlock()

	if err := this.reportConsumption(ctx, items); err != nil {
		return // 失败静默：计数仍在 pending，下轮重试
	}
	this.consumptionMu.Lock()
	for _, item := range items {
		if remain := this.pendingConsumption[item.Key] - item.Count; remain > 0 {
			this.pendingConsumption[item.Key] = remain
		} else {
			delete(this.pendingConsumption, item.Key)
		}
	}
	this.consumptionMu.Unlock()
}

// reportConsumption - 上报消费明细到平台。直连 transport.RoundTrip（不经 doRequest），
// 避免失败/成功改写授权刷新循环的 retryDelay 退避状态；clockOffset 校时仍复用。
func (this *Client) reportConsumption(ctx context.Context, items []platformConfigConsumeItem) error {
	if this.transport == nil {
		return errors.New("运行面传输层未初始化")
	}
	if len(items) == 0 {
		return nil
	}
	body, err := json.Marshal(platformConfigConsumeBody{LicenseNo: this.options.LicenseNo, Items: items})
	if err != nil {
		return err
	}
	code, raw, err := this.transport.RoundTrip(ctx, http.MethodPost, "/api/v1/platform/configs/consume", body, true)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		return errors.New("许可证或实例信息无效")
	}
	var response platformConfigConsumeResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)
	if !passThrough(response.Status) {
		return errors.New("消费上报被拒绝：" + response.Status)
	}
	return nil
}
