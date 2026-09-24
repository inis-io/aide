package apis

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ipLocatePath - IP 归属地查询路径（protocol-matrix.yaml 第 2 行；HTTP ↔ gRPC IPLocate 一一对应）。
// typed 方法内部不做额外逻辑：能力/动作/计量数由服务端固定，客户端只上送 ip。
const ipLocatePath = "/api/v1/apis/ip-locate/query"

// IPLocateResult - IP 归属地归一化结果：字段与 proto apis.v1.IPLocateResult 及
// licen-hub iplocate Provider 出参逐字对齐（HTTP data.result / gRPC IPLocateResponse.result 同形）。
type IPLocateResult struct {
	// Ip - 查询的 IP
	Ip string `json:"ip"`
	// Nation - 国家（高德源固定「中国」，离线源取如「美国」）
	Nation string `json:"nation"`
	// Province - 省份/州
	Province string `json:"province"`
	// City - 城市
	City string `json:"city"`
	// Adcode - 城市行政编码（仅高德源有，可空）
	Adcode string `json:"adcode"`
	// Rectangle - 城市矩形范围（仅高德源有，可空）
	Rectangle string `json:"rectangle"`
	// Isp - 运营商（仅离线源有，可空）
	Isp string `json:"isp"`
	// Source - 数据来源：amap / ip2region
	Source string `json:"source"`
	// CacheHit - 是否命中 12h 平台缓存（计费折扣依据，与 Receipt.CacheHit 一致）
	CacheHit bool `json:"cacheHit"`
	// Stale - 是否为回源失败降级返回的过期库数据（正常缺省 false）
	Stale bool `json:"stale"`
}

// ipLocateBody - IPLocate 请求体（{"ip":"…"}；与平台 HTTP 侧 typed 入口同源）
type ipLocateBody struct {
	Ip string `json:"ip"`
}

// IPLocate - IP 归属地查询（typed 便捷方法，高频核心能力）。
/**
 * @param ctx context.Context - 调用上下文
 * @param ip string - 待查询 IP（IPv4 公网地址）
 * @param requestId ...string - 可选调用级幂等键（缺省自动生成 req_ 前缀键）
 * @return IPLocateResult - 归属地结果（字段见类型注释）
 * @return Receipt - 计量回执
 * @return error - 业务拒绝为 *apis.Error（如 ErrorCodeIPNotFound / ErrorCodeQuotaExceeded）
 * @example：
 * 	loc, receipt, err := lic.Apis.IPLocate(ctx, "203.0.113.10")
 * 	if err == nil {
 * 		fmt.Println(loc.Province, loc.City, loc.Source, receipt.QuotaRemaining)
 * 	}
 */
func (this *Client) IPLocate(ctx context.Context, ip string, requestId ...string) (IPLocateResult, Receipt, error) {

	body, err := json.Marshal(ipLocateBody{Ip: ip})
	if err != nil {
		return IPLocateResult{}, Receipt{}, err
	}
	data, err := this.do(ctx, http.MethodPost, ipLocatePath, body, resolveRequestID(requestId...))
	if err != nil {
		return IPLocateResult{}, Receipt{}, err
	}
	var payload struct {
		Result  IPLocateResult `json:"result"`
		Receipt Receipt        `json:"receipt"`
	}
	if err = json.Unmarshal(data, &payload); err != nil {
		return IPLocateResult{}, Receipt{}, fmt.Errorf("apis: IP 定位响应解析失败：%w", err)
	}
	return payload.Result, payload.Receipt, nil
}
