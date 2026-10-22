package sign

import (
	"encoding/json"
	"strings"
	"testing"
)

// ============================= 黄金向量 - 开始 =============================
//
// 下列签名期望值由平台服务端实现 utils.Md5.GenSign（aide/utils/cipher.go）对
// 「固定参数集 + 固定密钥」实测生成后硬编码，是签名算法与服务端兼容性的验收线。
// 改动 Sign / Value 实现后必须重新生成向量（生成方法见 openpay/AGENTS.md「签名黄金向量」）。
const (
	// goldenSecret - 向量专用密钥（禁止使用真实密钥）
	goldenSecret = "test-secret-0123456789abcdef"

	// goldenCreateTrade - 下单：金额 12.30（拼串 amount=12.3）+ 默认值 expired=900
	goldenCreateTrade = "13A5669628000EDA65E66ECA899DC1F1"
	// goldenCreateTradeAmount12 - 下单：金额 12.00（float 语义拼串 amount=12）
	goldenCreateTradeAmount12 = "DB6A0BCC774985065CFD973EE521C688"
	// goldenTradeQueryNo - 查单：按平台订单号
	goldenTradeQueryNo = "E3E0E5A762A63AA54BCE01109E90E940"
	// goldenTradeQueryOut - 查单：按商户订单号
	goldenTradeQueryOut = "2FC83FC4DCA4B101A68A426BE878DB2A"
	// goldenChannelList - 通道列表：仅 mchId（sign 被剔除）
	goldenChannelList = "6A35AD16700E6666DEA83ADBDB0D3F1B"
	// goldenTransferSend - 转账：金额 88.80 + 默认值 payeeType
	goldenTransferSend = "1CBA1F1C646D0AA087EAC78E5CD71CAB"
	// goldenTransferQuery - 转账查询：空 tradeNo 被剔除
	goldenTransferQuery = "3BD6D02F6EF0DD3B5FFBDFC241252268"
	// goldenCheckout - 收银：bool 值 genUrl=true 参与签名
	goldenCheckout = "780B05781A73AB877F9B8A836921DD10"
	// goldenProbeEmptyValue - 探测：0 与 false 参与签名，空串与 nil 被剔除
	goldenProbeEmptyValue = "72B11D13752BD46CFA478C73E57B8868"
	// goldenProbeOrder - 探测：多键字典序（A/a/b/c）
	goldenProbeOrder = "603D26D0331A5633A398F9A85BBFAFED"
	// goldenProbeEmpty - 探测：空参数集 → MD5("&secret=<secret>")
	goldenProbeEmpty = "A91933CFE1C5BD1709222D0382E73C14"
	// goldenProbeProtoJSON - 探测：protojson 形态（int64 为字符串 "900"）与 HTTP 形态同签名
	goldenProbeProtoJSON = "A06AB2EF25AA7CE8BF69DE4EEFD24CB8"
)

// ============================= 黄金向量 - 结束 =============================

// TestGoldenVectors - 黄金向量：签名必须与服务端实测值逐字节相同
func TestGoldenVectors(t *testing.T) {

	signer := New()

	tests := []struct {
		// name - 用例名
		name string
		// params - 参数集（与服务端收到的一致：camelCase 键 + 已注入默认值）
		params map[string]any
		// want - 服务端实测签名
		want string
	}{
		{
			name: "下单-默认值注入与金额12.30",
			params: map[string]any{
				"mchId":       "M1000001",
				"amount":      12.30,
				"currency":    "CNY",
				"channelCode": "ALIPAY_PC",
				"describe":    "测试订单",
				"expired":     int64(900),
				"outTradeNo":  "OUT20260815001",
				"notifyUrl":   "https://example.com/notify",
				"returnUrl":   "https://example.com/return",
				"remark":      "",
			},
			want: goldenCreateTrade,
		},
		{
			name: "下单-金额12.00按float语义拼串",
			params: map[string]any{
				"mchId":       "M1000001",
				"amount":      12.00,
				"currency":    "CNY",
				"channelCode": "WECHAT_NATIVE",
				"describe":    "",
				"expired":     int64(900),
				"outTradeNo":  "OUT20260815002",
				"notifyUrl":   "",
				"returnUrl":   "",
				"remark":      "",
			},
			want: goldenCreateTradeAmount12,
		},
		{
			name:   "查单-按平台订单号",
			params: map[string]any{"mchId": "M1000001", "tradeNo": "202608151200000001"},
			want:   goldenTradeQueryNo,
		},
		{
			name:   "查单-按商户订单号",
			params: map[string]any{"mchId": "M1000001", "outTradeNo": "OUT20260815001"},
			want:   goldenTradeQueryOut,
		},
		{
			name:   "通道列表-仅商户号",
			params: map[string]any{"mchId": "M1000001"},
			want:   goldenChannelList,
		},
		{
			name: "转账-默认值payeeType",
			params: map[string]any{
				"mchId":        "M1000001",
				"channelCode":  "ALIPAY_TRANS",
				"scene":        "alipay",
				"amount":       88.80,
				"currency":     "CNY",
				"payeeAccount": "payee@example.com",
				"payeeName":    "张三",
				"payeeType":    "ALIPAY_LOGON_ID",
				"describe":     "服务报酬",
				"outTradeNo":   "TOUT20260815001",
			},
			want: goldenTransferSend,
		},
		{
			name:   "转账查询-空值剔除",
			params: map[string]any{"mchId": "M1000001", "tradeNo": "", "outTradeNo": "TOUT20260815001", "sign": ""},
			want:   goldenTransferQuery,
		},
		{
			name:   "收银-bool值参与签名",
			params: map[string]any{"mchId": "M1000001", "tradeNo": "202608151200000001", "scene": "alipay", "way": "auto", "genUrl": true},
			want:   goldenCheckout,
		},
		{
			name:   "探测-0与false参与签名而空串与nil剔除",
			params: map[string]any{"mchId": "M1000001", "zero": 0, "flag": false, "empty": "", "none": nil},
			want:   goldenProbeEmptyValue,
		},
		{
			name:   "探测-多键字典序",
			params: map[string]any{"b": "2", "a": "1", "c": "3", "A": "0"},
			want:   goldenProbeOrder,
		},
		{
			name:   "探测-空参数集",
			params: map[string]any{},
			want:   goldenProbeEmpty,
		},
		{
			name:   "探测-protojson形态与HTTP形态同签名",
			params: map[string]any{"mchId": "M1000001", "amount": 12.3, "expired": "900", "channelCode": "ALIPAY_PC", "currency": "CNY", "genUrl": true},
			want:   goldenProbeProtoJSON,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			if got := signer.Sign(test.params, goldenSecret); got != test.want {
				t.Fatalf("签名不一致：got=%s want=%s", got, test.want)
			}

			// Verify 大小写不敏感
			if !signer.Verify(test.params, goldenSecret, strings.ToLower(test.want)) {
				t.Fatalf("小写签名应通过校验")
			}
			if signer.Verify(test.params, goldenSecret+"x", test.want) {
				t.Fatalf("错误密钥不应通过校验")
			}
			if signer.Verify(test.params, goldenSecret, "") {
				t.Fatalf("空签名不应通过校验")
			}
		})
	}
}

// TestSignExcludesSignField - 参数集中的 sign 键必须被剔除（否则重复签名会漂移）
func TestSignExcludesSignField(t *testing.T) {

	signer := New()
	params := map[string]any{"mchId": "M1000001", "tradeNo": "202608151200000001"}
	withSign := map[string]any{"mchId": "M1000001", "tradeNo": "202608151200000001", "sign": "WHATEVER"}

	first := signer.Sign(params, goldenSecret)
	if second := signer.Sign(withSign, goldenSecret); second != first {
		t.Fatalf("sign 键未被剔除：%s != %s", first, second)
	}
	if first != goldenTradeQueryNo {
		t.Fatalf("签名不一致：got=%s want=%s", first, goldenTradeQueryNo)
	}
}

// TestValueFormatting - 值格式化语义（与服务端 cast.ToString 逐项对齐）
func TestValueFormatting(t *testing.T) {

	tests := []struct {
		// name - 用例名
		name string
		// value - 参数值
		value any
		// want - 期望字面量
		want string
	}{
		{name: "nil转空串", value: nil, want: ""},
		{name: "空串", value: "", want: ""},
		{name: "float去掉尾零", value: 12.30, want: "12.3"},
		{name: "float整数形态", value: 12.00, want: "12"},
		{name: "float0保留", value: 0.0, want: "0"},
		{name: "booltrue", value: true, want: "true"},
		{name: "boolfalse保留", value: false, want: "false"},
		{name: "int64", value: int64(900), want: "900"},
		{name: "int", value: 42, want: "42"},
		{name: "int8", value: int8(-8), want: "-8"},
		{name: "int16", value: int16(-16), want: "-16"},
		{name: "int32", value: int32(-32), want: "-32"},
		{name: "uint", value: uint(42), want: "42"},
		{name: "uint8", value: uint8(8), want: "8"},
		{name: "uint16", value: uint16(16), want: "16"},
		{name: "uint32", value: uint32(32), want: "32"},
		{name: "uint64", value: uint64(64), want: "64"},
		{name: "float32", value: float32(1.5), want: "1.5"},
		{name: "bytes", value: []byte("RAW"), want: "RAW"},
		{name: "毫秒时间戳不用科学计数法", value: float64(1755244800000), want: "1755244800000"},
		{name: "jsonNumber", value: json.Number("900"), want: "900"},
	}

	for _, test := range tests {
		if got := Value(test.value); got != test.want {
			t.Fatalf("%s：got=%q want=%q", test.name, got, test.want)
		}
	}
}
