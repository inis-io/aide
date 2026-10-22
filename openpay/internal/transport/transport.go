// Package transport - openpay SDK 的内部传输链路实现（HTTP / gRPC）。
//
// 本包是内部实现细节：商户不直接接触，统一经根包 openpay.Client 使用。
// 两条链路共用 sign.Signer 与 types 的默认值注入（types.*.Normalize），
// 因此对同一逻辑请求产出同一签名；Operation 是链路无关的稳定操作名。
package transport

import (
	"context"
	"net/http"
	"time"

	"google.golang.org/grpc"

	"github.com/inis-io/aide/openpay/sign"
)

// ============================= 传输层抽象 - 开始 =============================

// Config - 传输链路装配参数（由根包 Client 填充）
type Config struct {
	// Endpoint - 平台地址（如 https://pay.example.com，末尾斜杠已裁剪）
	Endpoint string
	// MchId - 商户号（HTTP 链路进 body/query，gRPC 链路进消息体与 metadata）
	MchId string
	// Secret - 商户密钥（仅本地签名使用）
	Secret string
	// Signer - 签名器（nil 时回退 MD5）
	Signer sign.Signer
	// Timeout - 单次调用超时（HTTP 链路用作内置客户端超时）
	Timeout time.Duration
	// UserAgent - 请求 User-Agent
	UserAgent string
	// HTTPClient - HTTP 链路自定义客户端（nil 时按 Timeout 内置）
	HTTPClient *http.Client
	// DialOpts - gRPC 拨号选项（为空时使用 plaintext）
	DialOpts []grpc.DialOption
}

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

// mchIdField - 商户号键名（HTTP 链路须进 body/query，gRPC 链路在消息体内）
const mchIdField = "mchId"

// ============================= 传输层抽象 - 结束 =============================
