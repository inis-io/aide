package all

import (
	"reflect"
	"testing"

	"github.com/inis-io/aide/pay"
)

// TestRegister - 验证全部官方 Provider 使用显式注册且名称稳定
func TestRegister(t *testing.T) {
	registry := pay.NewRegistry()
	if err := Register(registry); err != nil {
		t.Fatal(err)
	}
	expected := []string{"alipay", "paypal", "wechat"}
	if !reflect.DeepEqual(registry.Names(), expected) {
		t.Fatalf("官方 Provider 名称不符：%v", registry.Names())
	}
}
