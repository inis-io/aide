package openpay

import (
	"context"
	"encoding/json"
	"fmt"
)

// ============================= 传输层抽象 - 开始 =============================

// mchIdField - 商户号键名（HTTP 链路须进 body/query，gRPC 链路在消息体内）
const mchIdField = "mchId"

// Operation - 稳定操作名（链路无关；HTTP 映射到 method+path，gRPC 映射到 RPC 方法）
type Operation string

const (
	// OpTradeCreate - 统一下单（POST /api/trade/unity；MerchantService/CreateTrade）
	OpTradeCreate Operation = "trade.create"
	// OpTradeQuery - 商户查单（GET /api/trade/query；MerchantService/QueryTrade）
	OpTradeQuery Operation = "trade.query"
	// OpCheckout - 收银/取支付链接（GET /api/trade/unity；MerchantService/GetCheckout）
	OpCheckout Operation = "trade.checkout"
	// OpTransferSend - 转账发起（POST /api/trans/send；MerchantService/SendTransfer）
	OpTransferSend Operation = "trans.send"
	// OpTransferQuery - 转账查询（GET /api/trans/query；MerchantService/QueryTransfer）
	OpTransferQuery Operation = "trans.query"
	// OpChannelList - 商户可用通道列表（GET /api/merchant/channels；MerchantService/ListChannels）
	OpChannelList Operation = "channel.list"
)

// Transport - 传输链路抽象（HTTP / gRPC）
// 业务方法只负责组装强类型请求并调用 Invoke，链路差异全部收敛在实现内。
type Transport interface {
	// Invoke - 调用指定能力；req/resp 为各操作的强类型请求/响应
	Invoke(ctx context.Context, op Operation, req, resp any) error
	// Close - 释放底层连接（HTTP 为 no-op，gRPC 关闭 ClientConn）
	Close() error
}

// dataUnmarshaler - 链路适配解码器
// HTTP 信封 data 结构与 SDK 响应模型的字段名不完全一致时（如下单返回 row 而非 trade），
// 由响应类型自行实现该接口完成映射；未实现时按标准 json 解码。
type dataUnmarshaler interface {
	// UnmarshalData - 将信封 data 原始 JSON 解码进响应模型
	UnmarshalData(raw []byte) error
}

// structToMap - 按 JSON tag 将结构体展开为签名参数集
// 与服务端「结构体 → JSON → map」路径一致，保证签名键名（camelCase）与值形态完全相同。
/**
 * @param value any - 待展开的结构体（指针亦可）
 * @return map[string]any - 签名参数集
 * @return error - 序列化失败（包装 ErrInvalidRequest）
 */
func structToMap(value any) (map[string]any, error) {

	text, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w：请求参数序列化失败（%v）", ErrInvalidRequest, err)
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return nil, fmt.Errorf("%w：请求参数解析失败（%v）", ErrInvalidRequest, err)
	}

	return params, nil
}

// ============================= 传输层抽象 - 结束 =============================
