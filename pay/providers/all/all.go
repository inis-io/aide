// Package all 提供全部官方支付 Provider 的显式注册入口。
package all

import (
	"fmt"

	"github.com/inis-io/aide/pay"
	"github.com/inis-io/aide/pay/providers/alipay"
	"github.com/inis-io/aide/pay/providers/paypal"
	"github.com/inis-io/aide/pay/providers/wechat"
)

// Register - 按固定顺序注册支付宝、微信支付与 PayPal
func Register(registry *pay.Registry) error {
	if registry == nil {
		return fmt.Errorf("%w：Registry 为 nil", pay.ErrInvalidConfig)
	}
	for _, register := range []func(*pay.Registry) error{alipay.Register, wechat.Register, paypal.Register} {
		if err := register(registry); err != nil {
			return err
		}
	}
	return nil
}
