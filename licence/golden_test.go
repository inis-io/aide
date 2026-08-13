package licence

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Golden 向量由 licen-hub 平台签发端（backend/app/common/sign）原样生成，
// 任何序列化/签名语义的漂移（如误换 JSON 库导致 <>& 转义差异）都会在此暴露。

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

// goldenVectors - 平台签发端生成的两组向量（全字段含 <>& 特殊字符 / 最小字段永久授权）
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
			Features: map[string]bool{"report.advanced": true, "export<pro>&": true, "ai.chat": false},
			Limits:   map[string]int64{"max_users": 100, "api<call>&daily": 1000000},
			Binding:  &Binding{Type: "fingerprint", Value: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
			IssuedAt: "2026-08-08T12:00:00Z", KeyVersion: "license-key-2026-01", Nonce: "a1b2c3d4e5f60718293a4b5c6d7e8f90",
			BindingPolicy: "single", SeatLimit: 1,
		},
		canonical: `{"licenseId":"LIC-2026-000123","userId":"USR-2026-000001","projectId":"PRJ-2026-000001","instanceId":"INS-2026-000001","environment":"production","validFrom":"2026-01-01T00:00:00Z","validUntil":"2027-01-01T00:00:00Z","maintenanceUntil":"2026-12-31T23:59:59Z","upgradeUntil":"2026-12-31T23:59:59Z","graceDays":15,"versionRange":"\u003e=2.0.0 \u003c3.0.0","features":{"ai.chat":false,"export\u003cpro\u003e\u0026":true,"report.advanced":true},"limits":{"api\u003ccall\u003e\u0026daily":1000000,"max_users":100},"binding":{"type":"fingerprint","value":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},"issuedAt":"2026-08-08T12:00:00Z","keyVersion":"license-key-2026-01","nonce":"a1b2c3d4e5f60718293a4b5c6d7e8f90","bindingPolicy":"single","seatLimit":1}`,
		signature: "d69d53f0e6cdca38ff8d7e9823100e95592417ea0c0fcbe88f626409a55a1b552b0b82f6026985eb1b13ced0578284423dd8bb61cb03375cb14068e423300408",
	},
	{
		name: "最小字段永久授权",
		payload: Payload{
			LicenseId: "LIC-2026-000456", UserId: "USR-2026-000002", ProjectId: "PRJ-2026-000002", InstanceId: "",
			Environment: "trial",
			ValidFrom:   "2026-01-01T00:00:00Z", ValidUntil: "", MaintenanceUntil: "", UpgradeUntil: "",
			GraceDays: 0, VersionRange: "",
			Features: map[string]bool{}, Limits: map[string]int64{}, Binding: nil,
			IssuedAt: "2026-08-08T12:00:00Z", KeyVersion: "license-key-2026-01", Nonce: "00000000000000000000000000000000",
			BindingPolicy: "seats", SeatLimit: 5,
		},
		canonical: `{"licenseId":"LIC-2026-000456","userId":"USR-2026-000002","projectId":"PRJ-2026-000002","instanceId":"","environment":"trial","validFrom":"2026-01-01T00:00:00Z","validUntil":"","maintenanceUntil":"","upgradeUntil":"","graceDays":0,"versionRange":"","features":{},"limits":{},"binding":null,"issuedAt":"2026-08-08T12:00:00Z","keyVersion":"license-key-2026-01","nonce":"00000000000000000000000000000000","bindingPolicy":"seats","seatLimit":5}`,
		signature: "4bb18ffcad47f90fcd4e2264adaa633bf014bc57cafc810140acbb27cc69b2eb2b57cde14ca9019dbab8da9849b8daa7ea9a87e81f6a18572f7941eab944b10d",
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
			signature, err := signPayload([]byte(vector.canonical), seed)
			if err != nil {
				t.Fatalf("signPayload 失败: %v", err)
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
