package openpay

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ============================= 黄金向量 - 开始 =============================
//
// 下列签名期望值由平台服务端实现 utils.Md5.GenSign（aide/utils/cipher.go）对
// 「固定参数集 + 固定密钥」实测生成后硬编码，是 SDK 与服务端签名兼容性的验收线。
// 改动 Signer 实现、signValue 格式化或默认值注入后必须重新生成向量
// （生成方法见 openpay/AGENTS.md「签名黄金向量」小节）。
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
	// goldenProbeProtoJSON - 探测：protojson 形态（int64 为字符串 "900"）与 HTTP 形态签名一致
	goldenProbeProtoJSON = "A06AB2EF25AA7CE8BF69DE4EEFD24CB8"
	// goldenNotifySign - Webhook 报文签名（金额 12.3 + 毫秒时间戳 1755244800000）
	goldenNotifySign = "6AF54267F9FE7682EE98ACACCB42E517"
)

// goldenNotifyBody - 与 goldenNotifySign 对应的 Webhook 原始报文
const goldenNotifyBody = `{"tradeNo":"202608151200000001","outTradeNo":"OUT20260815001","gatewayTradeNo":"ALIPAY2026081500001","mchId":"M1000001","amount":12.3,"currency":"CNY","scene":"alipay","way":"url","status":"paid","createAt":1755244800000,"updateAt":1755244860000,"sign":"6AF54267F9FE7682EE98ACACCB42E517"}`

// ============================= 黄金向量 - 结束 =============================

// referenceSign - 服务端签名算法的独立复算实现（测试基准，与被测实现互为校验）
func referenceSign(params map[string]any, secret string) string {

	keys := make([]string, 0, len(params))
	for key := range params {
		if key == signField {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := referenceValue(params[key])
		if value == "" {
			continue
		}
		parts = append(parts, key+"="+value)
	}

	sum := md5.Sum([]byte(strings.Join(parts, "&") + "&" + secretField + "=" + secret))

	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// referenceValue - 服务端 cast.ToString 的极简镜像（测试基准）
func referenceValue(value any) string {

	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// TestSignGoldenVectors - 签名黄金向量：SDK 签名必须与服务端实测值逐字节相同
func TestSignGoldenVectors(t *testing.T) {

	signer := &md5Signer{}

	// 下单请求：只填业务字段，默认值由 normalize 注入
	createTrade := &TradeCreateRequest{
		Amount:      12.30,
		ChannelCode: "ALIPAY_PC",
		Describe:    "测试订单",
		OutTradeNo:  "OUT20260815001",
		NotifyUrl:   "https://example.com/notify",
		ReturnUrl:   "https://example.com/return",
	}
	createTrade.normalize()

	amount12 := &TradeCreateRequest{
		Amount:      12.00,
		ChannelCode: "WECHAT_NATIVE",
		OutTradeNo:  "OUT20260815002",
	}
	amount12.normalize()

	transfer := &TransferRequest{
		ChannelCode:  "ALIPAY_TRANS",
		Scene:        "alipay",
		Amount:       88.80,
		PayeeAccount: "payee@example.com",
		PayeeName:    "张三",
		Describe:     "服务报酬",
		OutTradeNo:   "TOUT20260815001",
	}
	transfer.normalize()

	tests := []struct {
		// name - 用例名
		name string
		// params - 参数集
		params map[string]any
		// want - 服务端实测签名
		want string
	}{
		{
			name:   "下单-默认值注入与金额12.30",
			params: withMchId(t, createTrade),
			want:   goldenCreateTrade,
		},
		{
			name:   "下单-金额12.00按float语义拼串",
			params: withMchId(t, amount12),
			want:   goldenCreateTradeAmount12,
		},
		{
			name:   "查单-按平台订单号",
			params: withMchId(t, &TradeQueryRequest{TradeNo: "202608151200000001"}),
			want:   goldenTradeQueryNo,
		},
		{
			name:   "查单-按商户订单号",
			params: withMchId(t, &TradeQueryRequest{OutTradeNo: "OUT20260815001"}),
			want:   goldenTradeQueryOut,
		},
		{
			name:   "通道列表-仅商户号",
			params: map[string]any{mchIdField: "M1000001"},
			want:   goldenChannelList,
		},
		{
			name:   "转账-默认值payeeType",
			params: withMchId(t, transfer),
			want:   goldenTransferSend,
		},
		{
			name:   "转账查询-空值剔除",
			params: map[string]any{mchIdField: "M1000001", "tradeNo": "", "outTradeNo": "TOUT20260815001", signField: ""},
			want:   goldenTransferQuery,
		},
		{
			name:   "收银-bool值参与签名",
			params: map[string]any{mchIdField: "M1000001", "tradeNo": "202608151200000001", "scene": "alipay", "way": "auto", "genUrl": true},
			want:   goldenCheckout,
		},
		{
			name:   "探测-0与false参与签名而空串与nil剔除",
			params: map[string]any{mchIdField: "M1000001", "zero": 0, "flag": false, "empty": "", "none": nil},
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
			params: map[string]any{mchIdField: "M1000001", "amount": 12.3, "expired": "900", "channelCode": "ALIPAY_PC", "currency": "CNY", "genUrl": true},
			want:   goldenProbeProtoJSON,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			got := signer.Sign(test.params, goldenSecret)
			if got != test.want {
				t.Fatalf("签名不一致：got=%s want=%s", got, test.want)
			}

			// 独立复算实现必须得出同一结果
			if reference := referenceSign(test.params, goldenSecret); reference != test.want {
				t.Fatalf("复算实现不一致：got=%s want=%s", reference, test.want)
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

	signer := &md5Signer{}
	params := map[string]any{mchIdField: "M1000001", "tradeNo": "202608151200000001"}
	withSign := map[string]any{mchIdField: "M1000001", "tradeNo": "202608151200000001", signField: "WHATEVER"}

	first := signer.Sign(params, goldenSecret)
	if second := signer.Sign(withSign, goldenSecret); second != first {
		t.Fatalf("sign 键未被剔除：%s != %s", first, second)
	}
	if first != goldenTradeQueryNo {
		t.Fatalf("签名不一致：got=%s want=%s", first, goldenTradeQueryNo)
	}
}

// TestSignValueFormatting - 值格式化语义（与服务端 cast.ToString 逐项对齐）
func TestSignValueFormatting(t *testing.T) {

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
		{name: "uint", value: uint(42), want: "42"},
		{name: "毫秒时间戳不用科学计数法", value: float64(1755244800000), want: "1755244800000"},
		{name: "jsonNumber", value: json.Number("900"), want: "900"},
	}

	for _, test := range tests {
		if got := signValue(test.value); got != test.want {
			t.Fatalf("%s：got=%q want=%q", test.name, got, test.want)
		}
	}
}

// withMchId - 结构体展开为参数集并注入商户号（模拟 HTTP 链路 payload）
func withMchId(t *testing.T, value any) map[string]any {

	t.Helper()

	params, err := structToMap(value)
	if err != nil {
		t.Fatalf("展开参数失败：%v", err)
	}
	params[mchIdField] = "M1000001"

	return params
}
