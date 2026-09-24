package apis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// invokePath - 通用能力调用路径（protocol-matrix.yaml 第 1 行；HTTP ↔ gRPC Invoke 一一对应）
const invokePath = "/api/v1/apis/invoke"

// InvokeInput - 通用调用入参（所有能力的完备兜底：新能力上线即可调，不必等 SDK 发版）
type InvokeInput struct {
	// Capability - 能力编码（如 ip-locate；须已注册且有在售产品）
	Capability string
	// Action - 动作（单动作能力可为空）
	Action string
	// Params - 业务参数（能力自定义，与 HTTP 请求体同构）
	Params map[string]any
	// ProductId - 显式产品选择（同一能力多产品时使用；0 = 服务端按订阅/排序解析）
	ProductId int64
	// Quantity - 调用方声明的最大计量数（默认 1，受能力上限收紧）
	Quantity int64
	// RequestId - 调用级幂等键（可选；为空自动生成 req_ 前缀键，重试必须复用同一值）
	RequestId string
}

// invokeBody - Invoke 请求体：ProductId/Quantity 零值省略（服务端零值即缺省语义），
// requestId 不进消息体（由宿主经 X-Request-Id / metadata x-request-id 上送）
type invokeBody struct {
	Capability string         `json:"capability"`
	Action     string         `json:"action"`
	Params     map[string]any `json:"params,omitempty"`
	ProductId  int64          `json:"productId,omitempty"`
	Quantity   int64          `json:"quantity,omitempty"`
}

// Invoke - 通用能力调用：result 为能力出参 JSON（HTTP 侧为出参原文直传，gRPC 侧为经
// google.protobuf.Struct 重新序列化的等价 JSON——字段名与取值一致，键序不保证一致；
// 结构由能力定义，客户按 JSON 解析、自行解码）。
//
// 已知边界（属 proto 契约层，非本包可修）：gRPC 侧 result 经 google.protobuf.Struct，
// 数字一律按 IEEE-754 double 往返，绝对值 >2^53 的整数会丢精度（HTTP 侧精确）。当前内置能力
// （如 ip-locate）出参全为字符串/布尔，不受影响；未来能力若需要精确大整数（雪花 ID、纳秒
// 时间戳等）应升级为 typed RPC，或以字符串承载这些字段。
/**
 * @param ctx context.Context - 调用上下文
 * @param input InvokeInput - 能力编码/动作/参数/产品/计量数/幂等键
 * @return json.RawMessage - 能力出参 JSON（信封 data.result；HTTP 原文 / gRPC 等价 JSON）
 * @return Receipt - 计量回执（信封 data.receipt）
 * @return error - 业务拒绝为 *apis.Error，传输/网络错误原样返回
 * @example：
 * 	out, receipt, err := lic.Apis.Invoke(ctx, apis.InvokeInput{
 * 		Capability: "mail-send", Action: "send", Params: map[string]any{"to": "user@example.com"},
 * 	})
 */
func (this *Client) Invoke(ctx context.Context, input InvokeInput) (json.RawMessage, Receipt, error) {

	body, err := json.Marshal(invokeBody{
		Capability: input.Capability, Action: input.Action, Params: input.Params,
		ProductId: input.ProductId, Quantity: input.Quantity,
	})
	if err != nil {
		return nil, Receipt{}, err
	}
	data, err := this.do(ctx, http.MethodPost, invokePath, body, resolveRequestID(input.RequestId))
	if err != nil {
		return nil, Receipt{}, err
	}
	var payload struct {
		Result  json.RawMessage `json:"result"`
		Receipt Receipt         `json:"receipt"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		return nil, Receipt{}, fmt.Errorf("apis: 调用响应解析失败：%w", err)
	}
	return payload.Result, payload.Receipt, nil
}
