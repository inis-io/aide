package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/inis-io/aide/openpay/sign"
	"github.com/inis-io/aide/openpay/types"

	merchantv1 "github.com/inis-io/aide/openpay/proto/gen/openpay/merchant/v1"
)

// ============================= gRPC 链路 - 开始 =============================

const (
	// metadataSign - 请求签名 metadata 键（值 = 请求消息 protojson → map → MD5 大写签名）
	metadataSign = "x-openpay-sign"
	// metadataMchId - 商户号 metadata 键（商户号同时位于消息体内，此处供服务端拦截器取证与日志）
	metadataMchId = "x-openpay-mch-id"
	// defaultGRPCPort - 拨号目标缺省端口（未显式指定端口时按 443 补全）
	defaultGRPCPort = "443"
)

// grpcOp - gRPC 链路操作描述符
type grpcOp struct {
	// method - 完整 RPC 方法名（/openpay.merchant.v1.MerchantService/Xxx）
	method string
	// request - 由 SDK 请求构造 proto 请求消息（含服务端默认值注入）
	request func(mchId string, req any) (proto.Message, error)
	// response - 构造空的 proto 响应消息
	response func() proto.Message
	// fill - 将 proto 响应消息转换为 SDK 响应模型（信封校验已在外层完成）
	fill func(message proto.Message, resp any) error
}

// grpcTransport - gRPC 链路实现
// 与 HTTP 链路共用同一个 sign.Signer 与同一批 Normalize() 默认值逻辑：
// 请求消息经 protojson 展开为 map 后签名，写入 outgoing metadata x-openpay-sign。
type grpcTransport struct {
	// conn - gRPC 连接（惰性建链）
	conn *grpc.ClientConn
	// mchId - 商户号
	mchId string
	// secret - 商户密钥
	secret string
	// signer - 签名器
	signer sign.Signer
	// operations - 操作描述符表
	operations map[Operation]grpcOp
	// once - 保证连接只关闭一次（重复 Close 幂等）
	once sync.Once
	// closeErr - 首次关闭的结果
	closeErr error
}

// NewGRPC - 装配 gRPC 链路（不主动建链，首个请求才真正连接）
func NewGRPC(config Config) (Transport, error) {

	signer := config.Signer
	if signer == nil {
		signer = sign.New()
	}

	dialOpts := []grpc.DialOption{grpc.WithUserAgent(config.UserAgent)}
	if len(config.DialOpts) == 0 {
		// 缺省 plaintext（h2c），对齐平台 HTTP/gRPC 同端口分流现状
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, config.DialOpts...)
	}

	conn, err := grpc.NewClient(GRPCTarget(config.Endpoint), dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("%w：gRPC 连接创建失败（%v）", types.ErrTransport, err)
	}

	return &grpcTransport{
		conn:       conn,
		mchId:      config.MchId,
		secret:     config.Secret,
		signer:     signer,
		operations: grpcOperations(),
	}, nil
}

// Invoke - 发起 gRPC 调用：构造消息 → protojson 签名 → 写 metadata → 调用 → 解信封 → 转模型
func (this *grpcTransport) Invoke(ctx context.Context, op Operation, req, resp any) error {

	descriptor, ok := this.operations[op]
	if !ok {
		return fmt.Errorf("%w：未知操作 %s", types.ErrInvalidRequest, op)
	}

	message, err := descriptor.request(this.mchId, req)
	if err != nil {
		return err
	}

	signature, err := this.signature(message)
	if err != nil {
		return err
	}

	ctx = metadata.AppendToOutgoingContext(ctx, metadataMchId, this.mchId, metadataSign, signature)

	reply := descriptor.response()
	if err = this.conn.Invoke(ctx, descriptor.method, message, reply); err != nil {
		return grpcError(err)
	}

	if err = checkEnvelope(reply); err != nil {
		return err
	}

	return descriptor.fill(reply, resp)
}

// Close - 关闭 gRPC 连接（幂等，重复调用返回首次结果）
func (this *grpcTransport) Close() error {

	if this == nil || this.conn == nil {
		return nil
	}

	this.once.Do(func() { this.closeErr = this.conn.Close() })

	return this.closeErr
}

// signature - 对请求消息生成签名（protojson 展开为 map 后走同一签名器）
func (this *grpcTransport) signature(message proto.Message) (string, error) {

	text, err := protojson.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("%w：gRPC 请求序列化失败（%v）", types.ErrInvalidRequest, err)
	}

	params := make(map[string]any)
	if err = json.Unmarshal(text, &params); err != nil {
		return "", fmt.Errorf("%w：gRPC 请求参数解析失败（%v）", types.ErrInvalidRequest, err)
	}

	return this.signer.Sign(params, this.secret), nil
}

// GRPCTarget - 平台地址归一为拨号目标（剥离 scheme 与路径，缺省端口按 443 补全）
func GRPCTarget(endpoint string) string {

	target := strings.TrimSpace(endpoint)
	if index := strings.Index(target, "://"); index >= 0 {
		target = target[index+3:]
	}
	if index := strings.IndexAny(target, "/?#"); index >= 0 {
		target = target[:index]
	}

	// host 无端口时补缺省端口；IPv6 字面量（形如 [::1]:2000）已含冒号，不参与改写
	if !strings.Contains(target, ":") {
		target += ":" + defaultGRPCPort
	}

	return target
}

// grpcError - RPC 状态错误归一（Unauthenticated → ErrInvalidSign，其余 → ErrTransport）
func grpcError(err error) error {

	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("%w：gRPC 调用失败（%v）", types.ErrTransport, err)
	}

	if st.Code() == codes.Unauthenticated {
		return fmt.Errorf("%w：平台拒绝该请求签名（%s）：%s", types.ErrInvalidSign, st.Code(), st.Message())
	}

	return fmt.Errorf("%w：gRPC 调用失败（%s）：%s", types.ErrTransport, st.Code(), st.Message())
}

// checkEnvelope - 校验响应信封（code!=200 → *types.APIError）
func checkEnvelope(message proto.Message) error {

	enveloped, ok := message.(interface {
		GetEnvelope() *merchantv1.Envelope
	})
	if !ok {
		return fmt.Errorf("%w：gRPC 响应消息缺少信封字段", types.ErrTransport)
	}

	envelope := enveloped.GetEnvelope()
	if envelope == nil {
		return fmt.Errorf("%w：gRPC 响应消息缺少信封字段", types.ErrTransport)
	}

	if envelope.GetCode() != 200 {
		return &types.APIError{Code: int(envelope.GetCode()), Msg: envelope.GetMsg()}
	}

	return nil
}

// grpcOperations - 构建 操作 → RPC 描述符 表
func grpcOperations() map[Operation]grpcOp {

	return map[Operation]grpcOp{

		OpTradeCreate: {
			method: merchantv1.MerchantService_CreateTrade_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				in, ok := req.(*types.TradeCreateRequest)
				if !ok || in == nil {
					return nil, fmt.Errorf("%w：下单请求类型不匹配", types.ErrInvalidRequest)
				}
				value := *in
				value.Normalize()
				return &merchantv1.CreateTradeRequest{
					MchId:       mchId,
					Amount:      value.Amount,
					ChannelCode: value.ChannelCode,
					Currency:    value.Currency,
					Describe:    value.Describe,
					Expired:     value.Expired,
					OutTradeNo:  value.OutTradeNo,
					NotifyUrl:   value.NotifyUrl,
					ReturnUrl:   value.ReturnUrl,
					Remark:      value.Remark,
				}, nil
			},
			response: func() proto.Message { return &merchantv1.CreateTradeResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.TradeCreateResult)
				if !ok || out == nil {
					return fmt.Errorf("%w：下单响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.CreateTradeResponse)
				if !ok {
					return fmt.Errorf("%w：下单响应类型不匹配", types.ErrTransport)
				}
				out.Trade = fromProtoTrade(reply.GetTrade())
				out.Gateways = fromProtoGateways(reply.GetGateways())
				out.Merchant = fromProtoMerchant(reply.GetMerchant())
				return nil
			},
		},

		OpTradeQuery: {
			method: merchantv1.MerchantService_QueryTrade_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				in, ok := req.(*types.TradeQueryRequest)
				if !ok || in == nil {
					return nil, fmt.Errorf("%w：查单请求类型不匹配", types.ErrInvalidRequest)
				}
				return &merchantv1.TradeQueryRequest{
					MchId:      mchId,
					TradeNo:    in.TradeNo,
					OutTradeNo: in.OutTradeNo,
				}, nil
			},
			response: func() proto.Message { return &merchantv1.TradeQueryResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.Trade)
				if !ok || out == nil {
					return fmt.Errorf("%w：查单响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.TradeQueryResponse)
				if !ok {
					return fmt.Errorf("%w：查单响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoTrade(reply.GetTrade())
				return nil
			},
		},

		OpCheckout: {
			method: merchantv1.MerchantService_GetCheckout_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				in, ok := req.(*types.CheckoutRequest)
				if !ok || in == nil {
					return nil, fmt.Errorf("%w：收银请求类型不匹配", types.ErrInvalidRequest)
				}
				value := *in
				value.Normalize()
				return &merchantv1.CheckoutRequest{
					MchId:   mchId,
					TradeNo: value.TradeNo,
					Scene:   value.Scene,
					Way:     value.Way,
					GenUrl:  value.GenUrl,
				}, nil
			},
			response: func() proto.Message { return &merchantv1.CheckoutResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.CheckoutResult)
				if !ok || out == nil {
					return fmt.Errorf("%w：收银响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.CheckoutResponse)
				if !ok {
					return fmt.Errorf("%w：收银响应类型不匹配", types.ErrTransport)
				}
				out.Trade = fromProtoTrade(reply.GetTrade())
				out.Gateways = fromProtoGateways(reply.GetGateways())
				out.Merchant = fromProtoMerchant(reply.GetMerchant())
				return nil
			},
		},

		OpTransferSend: {
			method: merchantv1.MerchantService_SendTransfer_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				in, ok := req.(*types.TransferRequest)
				if !ok || in == nil {
					return nil, fmt.Errorf("%w：转账请求类型不匹配", types.ErrInvalidRequest)
				}
				value := *in
				value.Normalize()
				return &merchantv1.TransferRequest{
					MchId:                   mchId,
					ChannelCode:             value.ChannelCode,
					Scene:                   value.Scene,
					Amount:                  value.Amount,
					Currency:                value.Currency,
					PayeeAccount:            value.PayeeAccount,
					PayeeName:               value.PayeeName,
					PayeeType:               value.PayeeType,
					Describe:                value.Describe,
					OutTradeNo:              value.OutTradeNo,
					NotifyUrl:               value.NotifyUrl,
					Remark:                  value.Remark,
					TransferSceneName:       value.TransferSceneName,
					TransferSceneReportInfo: value.TransferSceneReportInfo,
				}, nil
			},
			response: func() proto.Message { return &merchantv1.TransferResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.TransferResult)
				if !ok || out == nil {
					return fmt.Errorf("%w：转账响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.TransferResponse)
				if !ok {
					return fmt.Errorf("%w：转账响应类型不匹配", types.ErrTransport)
				}
				out.Trans = fromProtoTrans(reply.GetTrans())
				out.Merchant = fromProtoMerchant(reply.GetMerchant())
				return nil
			},
		},

		OpTransferQuery: {
			method: merchantv1.MerchantService_QueryTransfer_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				in, ok := req.(*types.TransferQueryRequest)
				if !ok || in == nil {
					return nil, fmt.Errorf("%w：转账查询请求类型不匹配", types.ErrInvalidRequest)
				}
				return &merchantv1.TransferQueryRequest{
					MchId:      mchId,
					TradeNo:    in.TradeNo,
					OutTradeNo: in.OutTradeNo,
				}, nil
			},
			response: func() proto.Message { return &merchantv1.TransferQueryResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*types.Trans)
				if !ok || out == nil {
					return fmt.Errorf("%w：转账查询响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.TransferQueryResponse)
				if !ok {
					return fmt.Errorf("%w：转账查询响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoTrans(reply.GetTrans())
				return nil
			},
		},

		OpChannelList: {
			method: merchantv1.MerchantService_ListChannels_FullMethodName,
			request: func(mchId string, req any) (proto.Message, error) {
				return &merchantv1.ListChannelsRequest{MchId: mchId}, nil
			},
			response: func() proto.Message { return &merchantv1.ListChannelsResponse{} },
			fill: func(message proto.Message, resp any) error {
				out, ok := resp.(*[]types.Channel)
				if !ok || out == nil {
					return fmt.Errorf("%w：通道列表响应类型不匹配", types.ErrInvalidRequest)
				}
				reply, ok := message.(*merchantv1.ListChannelsResponse)
				if !ok {
					return fmt.Errorf("%w：通道列表响应类型不匹配", types.ErrTransport)
				}
				*out = fromProtoChannels(reply.GetChannels())
				return nil
			},
		},
	}
}

// ============================= proto → SDK 模型 - 开始 =============================

// fromProtoTrade - proto 订单 → SDK 订单
func fromProtoTrade(source *merchantv1.Trade) types.Trade {

	if source == nil {
		return types.Trade{}
	}

	return types.Trade{
		TradeNo:        source.GetTradeNo(),
		OutTradeNo:     source.GetOutTradeNo(),
		GatewayTradeNo: source.GetGatewayTradeNo(),
		MchId:          source.GetMchId(),
		Scene:          source.GetScene(),
		Way:            source.GetWay(),
		Amount:         source.GetAmount(),
		Currency:       source.GetCurrency(),
		Status:         source.GetStatus(),
		Url:            source.GetUrl(),
		Describe:       source.GetDescribe(),
		Remark:         source.GetRemark(),
		NotifyUrl:      source.GetNotifyUrl(),
		ReturnUrl:      source.GetReturnUrl(),
		ExpireAt:       source.GetExpireAt(),
		CreateAt:       source.GetCreateAt(),
		UpdateAt:       source.GetUpdateAt(),
	}
}

// fromProtoTrans - proto 转账单 → SDK 转账单
func fromProtoTrans(source *merchantv1.Trans) types.Trans {

	if source == nil {
		return types.Trans{}
	}

	return types.Trans{
		TradeNo:        source.GetTradeNo(),
		OutTradeNo:     source.GetOutTradeNo(),
		GatewayTradeNo: source.GetGatewayTradeNo(),
		MchId:          source.GetMchId(),
		Scene:          source.GetScene(),
		Way:            source.GetWay(),
		Amount:         source.GetAmount(),
		Currency:       source.GetCurrency(),
		Status:         source.GetStatus(),
		PayeeAccount:   source.GetPayeeAccount(),
		PayeeName:      source.GetPayeeName(),
		PayeeType:      source.GetPayeeType(),
		Describe:       source.GetDescribe(),
		Remark:         source.GetRemark(),
		ExpireAt:       source.GetExpireAt(),
		CreateAt:       source.GetCreateAt(),
		UpdateAt:       source.GetUpdateAt(),
	}
}

// fromProtoGateway - proto 网关 → SDK 网关
func fromProtoGateway(source *merchantv1.Gateway) types.Gateway {

	if source == nil {
		return types.Gateway{}
	}

	return types.Gateway{
		Id:        source.GetId(),
		Name:      source.GetName(),
		Scene:     source.GetScene(),
		Status:    source.GetStatus(),
		IsSandbox: source.GetIsSandbox(),
		CostRate:  source.GetCostRate(),
	}
}

// fromProtoGateways - proto 网关列表 → SDK 网关列表
func fromProtoGateways(source []*merchantv1.Gateway) []types.Gateway {

	if len(source) == 0 {
		return nil
	}

	gateways := make([]types.Gateway, 0, len(source))
	for _, item := range source {
		gateways = append(gateways, fromProtoGateway(item))
	}

	return gateways
}

// fromProtoMerchant - proto 商户展示信息 → SDK 商户展示信息
func fromProtoMerchant(source *merchantv1.MerchantBrief) types.MerchantBrief {

	if source == nil {
		return types.MerchantBrief{}
	}

	return types.MerchantBrief{
		Name:     source.GetName(),
		MchId:    source.GetMchId(),
		Avatar:   source.GetAvatar(),
		Describe: source.GetDescribe(),
		Status:   source.GetStatus(),
	}
}

// fromProtoChannel - proto 通道 → SDK 通道
func fromProtoChannel(source *merchantv1.Channel) types.Channel {

	if source == nil {
		return types.Channel{}
	}

	return types.Channel{
		Id:       source.GetId(),
		Code:     source.GetCode(),
		Name:     source.GetName(),
		Currency: source.GetCurrency(),
		FeeRate:  source.GetFeeRate(),
		Status:   source.GetStatus(),
		Remark:   source.GetRemark(),
		Gateways: fromProtoGateways(source.GetGateways()),
	}
}

// fromProtoChannels - proto 通道列表 → SDK 通道列表
func fromProtoChannels(source []*merchantv1.Channel) []types.Channel {

	channels := make([]types.Channel, 0, len(source))
	for _, item := range source {
		channels = append(channels, fromProtoChannel(item))
	}

	return channels
}

// ============================= proto → SDK 模型 - 结束 =============================
