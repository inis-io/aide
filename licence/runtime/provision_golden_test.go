package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Provision golden 向量：锚定申领的 wire 线格式（JSON 字段名/键序/HTML 转义）。
// 请求方向锚定 SDK 实际发出的字节（json.Marshal provisionBody），
// 响应方向锚定契约 §6 响应 JSON 原文 → provisionResponse → ProvisionResult 的解析映射。
// 任何序列化语义漂移（如误换 JSON 库导致 <>& 转义差异、字段重命名、键序变化）都会在此暴露。

const (
	// provisionGoldenRequest - 请求体期望 canonical 字节（tenantId 缺省 0 被 omitempty 省略）：
	// 键序=声明序；<>& 被 Go encoding/json 转义为
	// \u003c / \u003e / \u0026 字面序列（与信封 golden 同一语义，防误换不转义的 JSON 库）。
	provisionGoldenRequest = `{"projectNo":"INIS-AUTO","installSN":"7d3b9f2e-4c1a-4b6d-9e8f-0a1b2c3d4e5f","deviceName":"dev\u003cpro\u003e\u0026","clientTime":1789459200000}`
	// provisionGoldenTenantRequest - SaaS 子租户发证：tenantId > 0 时按声明序紧随 projectNo 上送。
	provisionGoldenTenantRequest = `{"projectNo":"INIS-SAAS","tenantId":42,"installSN":"7d3b9f2e-4c1a-4b6d-9e8f-0a1b2c3d4e5f","clientTime":1789459200000}`
)

// TestProvisionBodyGoldenWire - 申领请求体 JSON 必须与契约 §6 逐字节一致（含 <>& 转义）。
func TestProvisionBodyGoldenWire(t *testing.T) {

	body, err := json.Marshal(provisionBody{
		ProjectNo:  "INIS-AUTO",
		InstallSN:  "7d3b9f2e-4c1a-4b6d-9e8f-0a1b2c3d4e5f",
		DeviceName: "dev<pro>&", ClientTime: 1789459200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != provisionGoldenRequest {
		t.Fatalf("请求线格式漂移\n期望: %s\n实际: %s", provisionGoldenRequest, string(body))
	}
	if !strings.Contains(string(body), `\u003c`) {
		t.Fatalf("请求线格式缺少 < 转义序列（防误换不转义的 JSON 库）: %s", string(body))
	}

	// 子租户方向：tenantId 参与序列化且位置固定在 projectNo 之后
	tenantBody, err := json.Marshal(provisionBody{
		ProjectNo: "INIS-SAAS", TenantId: 42,
		InstallSN: "7d3b9f2e-4c1a-4b6d-9e8f-0a1b2c3d4e5f", ClientTime: 1789459200000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(tenantBody) != provisionGoldenTenantRequest {
		t.Fatalf("子租户请求线格式漂移\n期望: %s\n实际: %s", provisionGoldenTenantRequest, string(tenantBody))
	}

}

// TestProvisionResponseGoldenWire - 契约 §6 响应 JSON 原文 → 统一响应 → 结果映射。
func TestProvisionResponseGoldenWire(t *testing.T) {

	raw := `{"status":"OK","serverTime":1789459200000,"licenseNo":"LIC-2026-000888","salt":"salt-http","bindingPolicy":"single","seatLimit":1,"expiresAt":1790000000000,"reissued":true,"message":""}`
	var response provisionResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		t.Fatal(err)
	}
	result, err := finishProvision(response)
	if err != nil {
		t.Fatal(err)
	}
	if result.LicenseNo != "LIC-2026-000888" || result.Salt != "salt-http" {
		t.Fatalf("许可证/盐解析漂移: %+v", result)
	}
	if result.BindingPolicy != "single" || result.SeatLimit != 1 {
		t.Fatalf("绑定策略/席位解析漂移: %+v", result)
	}
	if result.ExpiresAt != 1790000000000 || !result.Reissued {
		t.Fatalf("有效期/续签标识解析漂移: %+v", result)
	}
	if result.Customer != nil {
		t.Fatalf("未绑定客户时 customer 应为 nil: %+v", result.Customer)
	}

	// 已绑定客户方向：customer 视图解析到 ProvisionResult.Customer（reissued=false 原样保持）
	bound := `{"status":"OK","serverTime":1789459200000,"licenseNo":"LIC-2026-000888","salt":"salt-http","bindingPolicy":"single","seatLimit":1,"expiresAt":1790000000000,"reissued":false,"customer":{"maskAccount":"r***@x.com","boundAt":123}}`
	var boundResponse provisionResponse
	if err = json.Unmarshal([]byte(bound), &boundResponse); err != nil {
		t.Fatal(err)
	}
	boundResult, err := finishProvision(boundResponse)
	if err != nil {
		t.Fatal(err)
	}
	if boundResult.Customer == nil || boundResult.Customer.MaskAccount != "r***@x.com" || boundResult.Customer.BoundAt != 123 {
		t.Fatalf("客户账号视图解析漂移: %+v", boundResult.Customer)
	}
	if boundResult.Reissued {
		t.Fatalf("reissued=false 解析漂移: %+v", boundResult)
	}

	// 业务拒绝方向：status 字段驱动 *ProvisionError（旧模板申领拒绝码 TEMPLATE_DEPRECATED 如实透传）
	denied := `{"status":"TEMPLATE_DEPRECATED","serverTime":1789459200000,"message":"授权模板体系已下线，请升级客户端"}`
	var refused provisionResponse
	if err = json.Unmarshal([]byte(denied), &refused); err != nil {
		t.Fatal(err)
	}
	_, err = finishProvision(refused)
	if err == nil {
		t.Fatal("业务拒绝应返回错误")
	}
	var provisionErr *ProvisionError
	if !errors.As(err, &provisionErr) || provisionErr.Status != "TEMPLATE_DEPRECATED" || provisionErr.Message != "授权模板体系已下线，请升级客户端" {
		t.Fatalf("业务拒绝状态/提示解析漂移: %v", err)
	}
}
