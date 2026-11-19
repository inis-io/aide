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
// 改动 Sign / Value 实现后必须重新生成向量（生成方法见 ios/AGENTS.md「签名黄金向量」）。
const (
	// goldenSecret - 向量专用密钥（禁止使用真实密钥）
	goldenSecret = "INIS-TEST-SECRET-0123456789abcdef"

	// goldenVerifyAccessKey - 凭据验证：仅 accessKey + timestamp
	goldenVerifyAccessKey = "5FDFB63422549D89EDBED2E73FDC77F6"
	// goldenListBucketInfos - 管理视角桶列表（与凭据验证同参数集）
	goldenListBucketInfos = "5FDFB63422549D89EDBED2E73FDC77F6"
	// goldenEmptyBucket - 清空桶
	goldenEmptyBucket = "9B3034217A86D8B156309458C8544B2B"
	// goldenCreateShare - 签发分享：默认值 expires=0 参与签名
	goldenCreateShare = "2525E294E7BA40163FA370D18C9DFF5F"
	// goldenCreateShareExpires - 签发分享：expires=3600
	goldenCreateShareExpires = "175CC55AF3C05581247929F6FDC47BC1"
	// goldenListShares - 对象分享列表
	goldenListShares = "61C88411F6363F7E0ABA2B49684746B9"
	// goldenRevokeShare - 吊销分享
	goldenRevokeShare = "FFACAE9E7EA83F81B3132D7C19D16F29"
	// goldenListRecycle - 回收站列表：默认值 page=1/limit=20
	goldenListRecycle = "AEE6258AD56E2D9D91511984034DB4D7"
	// goldenListRecycleFull - 回收站列表：全参数
	goldenListRecycleFull = "3BE7D1969114F0633167108A2259BE3C"
	// goldenRestoreObjects - 回收站还原：ids 逗号分隔串
	goldenRestoreObjects = "3D3AB2872B827B81BA1B09ED1CEE514C"
	// goldenPurgeObjects - 回收站指定释放
	goldenPurgeObjects = "1E134425EB1224E7A7A7633A751409C0"
	// goldenClearRecycle - 回收站全部释放（与清空桶同参数集）
	goldenClearRecycle = "9B3034217A86D8B156309458C8544B2B"
	// goldenUsageOverview - 用量总览（与凭据验证同参数集）
	goldenUsageOverview = "5FDFB63422549D89EDBED2E73FDC77F6"
	// goldenProbeEmptyValue - 探测：0 与 false 参与签名，空串与 nil 被剔除
	goldenProbeEmptyValue = "131BBCB26F7B5AED6794CB2D76C300F0"
	// goldenProbeOrder - 探测：多键字典序（A/a/b/c）
	goldenProbeOrder = "E6E1A32D3D1CDD6683A277F1E65B6188"
	// goldenProbeEmpty - 探测：空参数集 → MD5("&secret=<secret>")
	goldenProbeEmpty = "D0B3CFC9511621BDD5EEE0C62F850B99"
	// goldenProbeProtoJSON - 探测：protojson 形态（int64 为字符串）与 HTTP 形态同签名
	goldenProbeProtoJSON = "2525E294E7BA40163FA370D18C9DFF5F"
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
			name:   "凭据验证-仅密钥与时间戳",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000)},
			want:   goldenVerifyAccessKey,
		},
		{
			name:   "清空桶",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "bucket": "photos"},
			want:   goldenEmptyBucket,
		},
		{
			name: "签发分享-默认值expires0",
			params: map[string]any{
				"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000),
				"bucket": "photos", "key": "a/b.jpg", "expires": int64(0),
			},
			want: goldenCreateShare,
		},
		{
			name: "签发分享-有效期3600",
			params: map[string]any{
				"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000),
				"bucket": "photos", "key": "a/b.jpg", "expires": int64(3600),
			},
			want: goldenCreateShareExpires,
		},
		{
			name:   "对象分享列表",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "bucket": "photos", "key": "a/b.jpg"},
			want:   goldenListShares,
		},
		{
			name:   "吊销分享",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "id": int64(42)},
			want:   goldenRevokeShare,
		},
		{
			name:   "回收站列表-默认值注入",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "page": 1, "limit": 20},
			want:   goldenListRecycle,
		},
		{
			name: "回收站列表-全参数",
			params: map[string]any{
				"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000),
				"bucket": "photos", "keyword": "report", "page": 2, "limit": 50,
			},
			want: goldenListRecycleFull,
		},
		{
			name:   "回收站还原-ids逗号串",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "ids": "1,2,3"},
			want:   goldenRestoreObjects,
		},
		{
			name:   "回收站指定释放",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "ids": "4,5"},
			want:   goldenPurgeObjects,
		},
		{
			name:   "探测-0与false参与签名而空串与nil剔除",
			params: map[string]any{"accessKey": "INIS0123456789ABCDEF", "zero": 0, "flag": false, "empty": "", "none": nil},
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
			name: "探测-protojson形态与HTTP形态同签名",
			params: map[string]any{
				"accessKey": "INIS0123456789ABCDEF", "timestamp": "1763568000000",
				"bucket": "photos", "key": "a/b.jpg", "expires": "0",
			},
			want: goldenProbeProtoJSON,
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
	params := map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000)}
	withSign := map[string]any{"accessKey": "INIS0123456789ABCDEF", "timestamp": int64(1763568000000), "sign": "WHATEVER"}

	first := signer.Sign(params, goldenSecret)
	if second := signer.Sign(withSign, goldenSecret); second != first {
		t.Fatalf("sign 键未被剔除：%s != %s", first, second)
	}
	if first != goldenVerifyAccessKey {
		t.Fatalf("签名不一致：got=%s want=%s", first, goldenVerifyAccessKey)
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
		{name: "毫秒时间戳不用科学计数法", value: float64(1763568000000), want: "1763568000000"},
		{name: "jsonNumber", value: json.Number("900"), want: "900"},
	}

	for _, test := range tests {
		if got := Value(test.value); got != test.want {
			t.Fatalf("%s：got=%q want=%q", test.name, got, test.want)
		}
	}
}
