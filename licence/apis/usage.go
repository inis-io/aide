package apis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// usagePath - 调用流水自助查询路径（protocol-matrix.yaml 第 3 行；只读，HTTP ↔ gRPC Usage 一一对应）
const usagePath = "/api/v1/apis/usage"

// UsageQuery - 调用流水查询入参：proto UsageRequest 的客户端可用子集
// （不含 userId/subId/activationNo 等管理面语义字段——运行面身份由许可证凭证推导，只能查自己）。
// 全零值 = 服务端默认（page 1 / limit 10 / 不限条件）。
type UsageQuery struct {
	// Page - 页码（从 1 起；0 = 服务端默认 1）
	Page int
	// Limit - 每页条数（0 = 服务端默认 10；上限 1000）
	Limit int
	// Order - 排序（空 = 服务端默认 id desc；白名单裁决）
	Order string
	// Capability - 能力编码筛选（空 = 不限）
	Capability string
	// ProductId - 产品筛选（0 = 不限）
	ProductId int64
	// ChargeMode - 计费模式筛选：free_quota / trial / subscription_quota / metered_balance
	ChargeMode string
	// Result - 调用结果筛选：success / provider_error / rejected / settle_error
	Result string
	// CacheHit - 缓存命中三态筛选："" 不限 / "true" 仅命中 / "false" 仅未命中
	CacheHit string
	// RequestId - 流水筛选：完整行 request_id 精确匹配（拆分计量与拒绝/失败行带 :a/:b/:rej/:err 派生后缀）。
	// 注意：这是查询条件，与本次调用的幂等键无关（Usage 只读，不下发调用级幂等键）
	RequestId string
	// CreateAt - 调用时间区间 [起, 止]（毫秒戳；0 或省略 = 不限，最多 2 个元素）
	CreateAt []int64
}

// UsagePage - 调用流水分页结果（信封 data.{records,count,page}）
type UsagePage struct {
	// Records - 流水行（无命中时为空）
	Records []UsageRecord `json:"records"`
	// Count - 满足条件的总条数
	Count int64 `json:"count"`
	// Page - 服务端归一后的实际页码
	Page int `json:"page"`
}

// UsageRecord - 调用流水行：字段与 proto apis.v1.UsageRecord 及平台 ApisUsageRecord 的 JSON 一一对应。
type UsageRecord struct {
	// Id - 流水主键
	Id int64 `json:"id"`
	// RequestId - 调用级幂等键（拆分计量派生 :a/:b 后缀）
	RequestId string `json:"requestId"`
	// UserId - 归属用户（运行面恒为凭证推导的本人）
	UserId int64 `json:"userId"`
	// Capability - 能力编码
	Capability string `json:"capability"`
	// ProductId - 产品 ID
	ProductId int64 `json:"productId"`
	// ActivationNo - 激活记录编号（排障定位与分实例统计；空 = 未关联激活）
	ActivationNo string `json:"activationNo"`
	// SubId - 命中的订阅 ID（纯按量调用为 0）
	SubId int64 `json:"subId"`
	// Quantity - 本次计量数
	Quantity int64 `json:"quantity"`
	// ChargeMode - 计费模式
	ChargeMode string `json:"chargeMode"`
	// CacheHit - 是否命中平台缓存
	CacheHit bool `json:"cacheHit"`
	// Amount - 本次扣费（分；免费、体验与订阅额度内为 0）
	Amount int64 `json:"amount"`
	// Result - 调用结果：success / provider_error / rejected / settle_error
	Result string `json:"result"`
	// UpstreamMs - 上游耗时（毫秒，监控用）
	UpstreamMs int64 `json:"upstreamMs"`
	// CreateAt - 创建时间（毫秒戳）
	CreateAt int64 `json:"createAt"`
}

// Usage - 调用流水自助查询（只读：不占闸门、不计量、不收费，故无计量回执，
// 返回形态为 (UsagePage, error)；身份由许可证凭证推导，只能查自己）。
/**
 * @param ctx context.Context - 调用上下文
 * @param query UsageQuery - 分页与筛选条件
 * @return UsagePage - 流水行 + 总条数 + 服务端归一的页码
 * @return error - 业务拒绝为 *apis.Error，传输/网络错误原样返回
 * @example：
 * 	page, err := lic.Apis.Usage(ctx, apis.UsageQuery{
 * 		Capability: "ip-locate", Limit: 20,
 * 		CreateAt: []int64{1700000000000, 1800000000000},
 * 	})
 */
func (this *Client) Usage(ctx context.Context, query UsageQuery) (UsagePage, error) {

	path := usagePath
	if values := usageQuery(query); len(values) > 0 {
		path += "?" + values.Encode()
	}
	// 只读调用：不下发调用级幂等键（requestId 传空；query.RequestId 是流水筛选条件）
	data, err := this.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return UsagePage{}, err
	}
	var page UsagePage
	if err = json.Unmarshal(data, &page); err != nil {
		return UsagePage{}, fmt.Errorf("apis: 流水响应解析失败：%w", err)
	}
	return page, nil
}

// usageQuery - 查询参数序列化：标量非零/非空才下发（零值即服务端缺省语义，省略可保持 URI 稳定）；
// 数组按平台约定序列化为 createAt[]=v 重复键（与 admin 子包 toQuery 同口径，服务端 facade.Params.Smart 绑定）。
func usageQuery(query UsageQuery) url.Values {

	values := url.Values{}
	if query.Page > 0 {
		values.Set("page", strconv.Itoa(query.Page))
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Order != "" {
		values.Set("order", query.Order)
	}
	if query.Capability != "" {
		values.Set("capability", query.Capability)
	}
	if query.ProductId > 0 {
		values.Set("productId", strconv.FormatInt(query.ProductId, 10))
	}
	if query.ChargeMode != "" {
		values.Set("chargeMode", query.ChargeMode)
	}
	if query.Result != "" {
		values.Set("result", query.Result)
	}
	if query.CacheHit != "" {
		values.Set("cacheHit", query.CacheHit)
	}
	if query.RequestId != "" {
		values.Set("requestId", query.RequestId)
	}
	for _, at := range query.CreateAt {
		values.Add("createAt[]", strconv.FormatInt(at, 10))
	}
	return values
}
