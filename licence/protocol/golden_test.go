package protocol

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Golden 向量由 licen-hub 平台签发端（backend/app/common/sign）原样生成，
// 任何序列化/签名语义的漂移（如误换 JSON 库导致 <>& 转义差异）都会在此暴露。
// 权益表统一为 entitlements 后，两组向量已按新载荷（含 bool / 整数 / 小数三种取值）重新生成，
// 平台侧 Payload 结构必须与本文件逐字段镜像。

const (
	// goldenSeed - 固定私钥种子（01 02 ... 20）的 hex
	goldenSeed = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	// goldenPublicKey - 固定种子推导的 Ed25519 公钥
	goldenPublicKey = "79b5562e8fe654f94078b112e8a98ba7901f853ae695bed7e0e3910bad049664"
)

// goldenVector - 测试向量：载荷 + 期望 canonical 字节 + 期望签名
type goldenVector struct {
	name      string
	payload   Payload
	canonical string
	signature string
}

// goldenVectors - 两组向量（全字段含 <>& 特殊字符且权益含 bool/整数/小数 / 最小字段永久授权）
// canonical 中的 <>& 等六个字面字符序列即 Go encoding/json 的 HTML 转义原文
var goldenVectors = []goldenVector{
	{
		name: "全字段含特殊字符",
		payload: Payload{
			LicenseId: "LIC-2026-000123", UserId: "USR-2026-000001", ProjectId: "PRJ-2026-000001", InstanceId: "INS-2026-000001",
			Environment: "production",
			ValidFrom:   "2026-01-01T00:00:00Z", ValidUntil: "2027-01-01T00:00:00Z",
			MaintenanceUntil: "2026-12-31T23:59:59Z", UpgradeUntil: "2026-12-31T23:59:59Z",
			GraceDays: 15, VersionRange: ">=2.0.0 <3.0.0",
			Entitlements: map[string]any{
				"report.advanced": true, "export<pro>&": true, "ai.chat": false,
				"max_users": 100, "api<call>&daily": 1000000, "max_storage_gb": 2.5,
			},
			Binding:  &Binding{Type: "fingerprint", Value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			IssuedAt: "2026-08-08T12:00:00Z", KeyVersion: "license-key-2026-01", Nonce: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
			BindingPolicy: "single", SeatLimit: 1,
		},
		canonical: `{"licenseId":"LIC-2026-000123","userId":"USR-2026-000001","projectId":"PRJ-2026-000001","instanceId":"INS-2026-000001","environment":"production","validFrom":"2026-01-01T00:00:00Z","validUntil":"2027-01-01T00:00:00Z","maintenanceUntil":"2026-12-31T23:59:59Z","upgradeUntil":"2026-12-31T23:59:59Z","graceDays":15,"versionRange":"\u003e=2.0.0 \u003c3.0.0","entitlements":{"ai.chat":false,"api\u003ccall\u003e\u0026daily":1000000,"export\u003cpro\u003e\u0026":true,"max_storage_gb":2.5,"max_users":100,"report.advanced":true},"binding":{"type":"fingerprint","value":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},"issuedAt":"2026-08-08T12:00:00Z","keyVersion":"license-key-2026-01","nonce":"a1b2c3d4e5f60718293a4b5c6d7e8f90","bindingPolicy":"single","seatLimit":1}`,
		signature: "9df24668574f6a561ad8a58668c132bcb15b39c7f77914f591c55f01afcaef75ef7dcc8422c04089e401b5a4af3a71e7a0779e1666af35d5d77c1d60f6e5020d",
	},
	{
		name: "最小字段永久授权",
		payload: Payload{
			LicenseId: "LIC-2026-000456", UserId: "USR-2026-000002", ProjectId: "PRJ-2026-000002", InstanceId: "",
			Environment: "trial",
			ValidFrom:   "2026-01-01T00:00:00Z", ValidUntil: "", MaintenanceUntil: "", UpgradeUntil: "",
			GraceDays: 0, VersionRange: "",
			Entitlements: map[string]any{}, Binding: nil,
			IssuedAt: "2026-08-08T12:00:00Z", KeyVersion: "license-key-2026-01", Nonce: "00000000000000000000000000000000",
			BindingPolicy: "seats", SeatLimit: 5,
		},
		canonical: `{"licenseId":"LIC-2026-000456","userId":"USR-2026-000002","projectId":"PRJ-2026-000002","instanceId":"","environment":"trial","validFrom":"2026-01-01T00:00:00Z","validUntil":"","maintenanceUntil":"","upgradeUntil":"","graceDays":0,"versionRange":"","entitlements":{},"binding":null,"issuedAt":"2026-08-08T12:00:00Z","keyVersion":"license-key-2026-01","nonce":"00000000000000000000000000000000","bindingPolicy":"seats","seatLimit":5}`,
		signature: "f3f2abc5f1dce3c7d87dde85543eeb7199542cbf8e7095c73b5b0538ea3ab0fea4e5839ca9a51f58c3ac12f26ba41a99d2b284a723a0acbfb061142a44d50706",
	},
}

// TestGoldenCanonical - canonical JSON 字节必须与平台签发端逐字节一致（含 <>& 转义与 map 键排序）
func TestGoldenCanonical(t *testing.T) {

	for _, vector := range goldenVectors {
		t.Run(vector.name, func(t *testing.T) {
			canonical, err := MarshalPayload(vector.payload)
			if err != nil {
				t.Fatalf("MarshalPayload 失败: %v", err)
			}
			if string(canonical) != vector.canonical {
				t.Fatalf("canonical 字节不一致\n期望: %s\n实际: %s", vector.canonical, string(canonical))
			}
			// 特殊字符向量必须出现 HTML 转义序列（防误换不转义的 JSON 库）
			if vector.name == "全字段含特殊字符" && !strings.Contains(string(canonical), `\u003c`) {
				t.Fatalf("canonical 缺少 < 转义序列: %s", string(canonical))
			}
		})
	}
}

// TestGoldenSignature - 用固定种子对 canonical 字节签名，结果必须与平台签发端一致
func TestGoldenSignature(t *testing.T) {

	for _, vector := range goldenVectors {
		t.Run(vector.name, func(t *testing.T) {
			seed, err := hex.DecodeString(goldenSeed)
			if err != nil {
				t.Fatalf("种子 hex 解码失败: %v", err)
			}
			signature, err := SignPayload([]byte(vector.canonical), seed)
			if err != nil {
				t.Fatalf("SignPayload 失败: %v", err)
			}
			if signature != vector.signature {
				t.Fatalf("签名不一致\n期望: %s\n实际: %s", vector.signature, signature)
			}
			if !Licence.VerifySign([]byte(vector.canonical), signature, goldenPublicKey) {
				t.Fatalf("验签失败: %s", vector.name)
			}
		})
	}
}

// TestGoldenTamper - 篡改 canonical 任意字节或使用错误公钥，验签必须失败
func TestGoldenTamper(t *testing.T) {

	vector := goldenVectors[0]
	tampered := []byte(vector.canonical)
	tampered[len(tampered)/2] ^= 0x01
	if Licence.VerifySign(tampered, vector.signature, goldenPublicKey) {
		t.Fatalf("篡改后验签竟然通过")
	}
	if Licence.VerifySign([]byte(vector.canonical), vector.signature, strings.Repeat("00", 32)) {
		t.Fatalf("错误公钥验签竟然通过")
	}
}
