package openpay

import (
	"context"
)

// ============================= 通道 - 开始 =============================

// Channel - 商户可用的支付/转账通道
type Channel struct {
	// Id - 通道ID
	Id int32 `json:"id"`
	// Code - 通道编码（下单/转账时的 channelCode）
	Code string `json:"code"`
	// Name - 通道名称
	Name string `json:"name"`
	// Currency - 通道支持币种
	Currency string `json:"currency"`
	// FeeRate - 通道费率（如 0.0020 表示 0.20%）
	FeeRate float64 `json:"feeRate"`
	// Status - 通道状态（normal 可用）
	Status string `json:"status"`
	// Remark - 备注
	Remark string `json:"remark"`
	// Gateways - 通道支持的网关
	Gateways []Gateway `json:"gateways"`
}

// ListChannels - 查询商户可用通道列表
// 通道编码（Channel.Code）即下单/转账请求的 channelCode。
/**
 * @param ctx context.Context - 调用上下文
 * @return []Channel - 通道列表（无可用通道时为空切片）
 * @return error - ErrTransport / ErrInvalidSign / *APIError
 * @example：
 * 	channels, err := client.ListChannels(ctx)
 * 	for _, channel := range channels { fmt.Println(channel.Code) }
 */
func (this *Client) ListChannels(ctx context.Context) ([]Channel, error) {

	channels := &[]Channel{}
	if err := this.invoke(ctx, OpChannelList, nil, channels); err != nil {
		return nil, err
	}

	return *channels, nil
}

// ============================= 通道 - 结束 =============================
