package licence

import (
	"strings"
	"testing"
)

// TestParseSaasMenuWrite - 许可证菜单写响应解析：成功/带影响报告/服务端故障/非放行态。
func TestParseSaasMenuWrite(t *testing.T) {
	client := &Client{}

	t.Run("success with impact report", func(t *testing.T) {
		raw := []byte(`{"status":"VALID","serverTime":1000,"id":5,"version":3,"impactReport":{"removedCodes":["a"],"affectedPlans":[],"affectedTenants":[]}}`)
		result, err := client.parseSaasMenuWrite(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Id != 5 || result.Version != 3 {
			t.Fatalf("id/version mismatch: %+v", result)
		}
		if result.ImpactReport == nil || len(result.ImpactReport.RemovedCodes) != 1 || result.ImpactReport.RemovedCodes[0] != "a" {
			t.Fatalf("impactReport mismatch: %+v", result.ImpactReport)
		}
	})

	t.Run("success without impact report", func(t *testing.T) {
		raw := []byte(`{"status":"VALID","serverTime":1000,"id":1,"version":2}`)
		result, err := client.parseSaasMenuWrite(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if result.Id != 1 || result.Version != 2 {
			t.Fatalf("id/version mismatch: %+v", result)
		}
		if result.ImpactReport != nil {
			t.Fatalf("impactReport should be nil, got %+v", result.ImpactReport)
		}
	})

	t.Run("server error", func(t *testing.T) {
		raw := []byte(`{"status":"ERROR","serverTime":1000,"message":"清单已发布"}`)
		_, err := client.parseSaasMenuWrite(raw)
		if err == nil || !strings.Contains(err.Error(), "清单已发布") {
			t.Fatalf("expected server error with message, got %v", err)
		}
	})

	t.Run("non pass-through license", func(t *testing.T) {
		raw := []byte(`{"status":"EXPIRED","serverTime":1000}`)
		_, err := client.parseSaasMenuWrite(raw)
		if err == nil || !strings.Contains(err.Error(), "EXPIRED") {
			t.Fatalf("expected non-pass-through error, got %v", err)
		}
	})
}
