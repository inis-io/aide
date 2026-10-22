package transport

import (
	"context"
	"testing"

	"github.com/inis-io/aide/openpay/types"
)

// ============================= 双链路一致性测试 - 开始 =============================

// TestSignatureConsistencyAcrossTransports - 同一逻辑请求在 HTTP 与 gRPC 链路产出同一签名
// 依据：HTTP 走「结构体 → JSON → map」，gRPC 走「proto 消息 → protojson → map」，
// 两条路径的键名（camelCase）与值格式化必须完全一致，否则服务端无法用同一验签函数处理。
func TestSignatureConsistencyAcrossTransports(t *testing.T) {

	ctx := context.Background()

	// HTTP 链路：从假平台捕获的 body 中取签名
	server, capture := startHTTPPlatform(t, func(*httpCapture) (int, string) {
		return 200, jsonReply(t, 200, "OK", map[string]any{"row": map[string]any{"tradeNo": "202608151200000001"}})
	})
	httpLink := startHTTPTransport(t, server.URL)
	if err := httpLink.Invoke(ctx, OpTradeCreate, newTradeCreateRequest(), &types.TradeCreateResult{}); err != nil {
		t.Fatalf("HTTP 下单失败：%v", err)
	}
	httpSign, _ := capture.body[signField].(string)

	// gRPC 链路：从假服务的 incoming metadata 中取签名
	fake := &fakeMerchantService{secret: testSecret}
	grpcLink := startFakeGRPC(t, fake)
	if err := grpcLink.Invoke(ctx, OpTradeCreate, newTradeCreateRequest(), &types.TradeCreateResult{}); err != nil {
		t.Fatalf("gRPC 下单失败：%v", err)
	}
	grpcSign := fake.snapshot().sign

	if httpSign != grpcSign {
		t.Fatalf("两条链路签名不一致：http=%s grpc=%s", httpSign, grpcSign)
	}
	if httpSign != goldenCreateTrade {
		t.Fatalf("签名与服务端黄金向量不一致：got=%s want=%s", httpSign, goldenCreateTrade)
	}
}

// TestSignatureConsistencyCheckout - 收银链路差异说明：HTTP 不签名、gRPC 签名
// 两条链路的签名输入同为「收银请求字段 + mchId」，此处校验 gRPC 侧确实等于 HTTP 形态参数集的签名。
func TestSignatureConsistencyCheckout(t *testing.T) {

	ctx := context.Background()

	fake := &fakeMerchantService{secret: testSecret}
	grpcLink := startFakeGRPC(t, fake)
	if err := grpcLink.Invoke(ctx, OpCheckout, &types.CheckoutRequest{TradeNo: "202608151200000001"}, &types.CheckoutResult{}); err != nil {
		t.Fatalf("gRPC 收银失败：%v", err)
	}

	// 手工构造与 HTTP 形态等价的参数集（同一请求逻辑）
	params := map[string]any{
		mchIdField: testMchId,
		"tradeNo":  "202608151200000001",
		"scene":    "alipay",
		"way":      "auto",
		"genUrl":   true,
	}
	if want := referenceSign(params, testSecret); fake.snapshot().sign != want {
		t.Fatalf("收银签名与 HTTP 形态不一致：got=%s want=%s", fake.snapshot().sign, want)
	}
}

// ============================= 双链路一致性测试 - 结束 =============================
