package protocol

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

const (
	keysetGoldenRootPublicKey = "79b5562e8fe654f94078b112e8a98ba7901f853ae695bed7e0e3910bad049664"
	keysetGoldenRootSeed      = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	keysetGoldenPayload       = `{"projectNo":"PRJ-A","revision":7,"issuedAt":"2026-10-27T08:00:00Z","licenseKeys":[{"keyVersion":"license-key-a-1","publicKey":"43cdc023d22d5f9e107d1a0693457d35d1d10eb7d21c721192f56f5de40665d3"}],"releaseKeys":[{"keyVersion":"release-key-a-1","publicKey":"481ede772da15785c9e59a086201b8c3f9c307129e3fc6d7f34aa4dfed5814e5"}],"keyVersion":"bootstrap-key-test","nonce":"nonce-golden"}`
	keysetGoldenEnvelope      = `{"version":1,"algorithm":"Ed25519","payload":` + keysetGoldenPayload + `,"signature":"25ec4935c8e6cb38a22830677200295bd226f7b26974b21fb4b375dbb1a85d0939d5255801bb748d9914558a4b14f12dc5b7380a1f5ce67dbf655368a1a8160b"}`
)

// TestKeysetPayloadGoldenWire - 锚定平台共享的 payload 字段顺序与公钥项线格式。
func TestKeysetPayloadGoldenWire(t *testing.T) {
	payload := KeysetPayload{
		ProjectNo: "PRJ-A", Revision: 7, IssuedAt: "2026-10-27T08:00:00Z",
		LicenseKeys: []KeysetPublicKey{{KeyVersion: "license-key-a-1", PublicKey: "43cdc023d22d5f9e107d1a0693457d35d1d10eb7d21c721192f56f5de40665d3"}},
		ReleaseKeys: []KeysetPublicKey{{KeyVersion: "release-key-a-1", PublicKey: "481ede772da15785c9e59a086201b8c3f9c307129e3fc6d7f34aa4dfed5814e5"}},
		KeyVersion:  "bootstrap-key-test", Nonce: "nonce-golden",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != keysetGoldenPayload {
		t.Fatalf("keyset payload 线格式漂移\n期望: %s\n实际: %s", keysetGoldenPayload, raw)
	}
}

// TestVerifyKeysetGolden - 固定签名向量可验根，同 revision 重复验证保持幂等。
func TestVerifyKeysetGolden(t *testing.T) {
	roots := map[string]string{"bootstrap-key-test": keysetGoldenRootPublicKey}
	first, err := VerifyKeyset([]byte(keysetGoldenEnvelope), "PRJ-A", roots, 7)
	if err != nil {
		t.Fatalf("固定 keyset 向量验签失败: %v", err)
	}
	if first.Payload.ProjectNo != "PRJ-A" || first.Payload.Revision != 7 {
		t.Fatalf("keyset 项目或 revision 解析错误: %+v", first.Payload)
	}
	if len(first.Payload.LicenseKeys) != 1 || first.Payload.LicenseKeys[0].PublicKey != "43cdc023d22d5f9e107d1a0693457d35d1d10eb7d21c721192f56f5de40665d3" {
		t.Fatalf("license 工作公钥解析错误: %+v", first.Payload.LicenseKeys)
	}
	second, err := VerifyKeyset([]byte(keysetGoldenEnvelope), "PRJ-A", roots, 7)
	if err != nil || second.Payload.Nonce != first.Payload.Nonce {
		t.Fatalf("同一可信 keyset 应允许幂等恢复: %+v %v", second, err)
	}
}

// TestVerifyKeysetRejectsUntrustedInput - 错根、跨项目、篡改与低 revision 必须 fail-closed。
func TestVerifyKeysetRejectsUntrustedInput(t *testing.T) {
	roots := map[string]string{"bootstrap-key-test": keysetGoldenRootPublicKey}
	tests := []struct {
		name            string
		raw             string
		projectNo       string
		roots           map[string]string
		minimumRevision int64
	}{
		{name: "错根", raw: keysetGoldenEnvelope, projectNo: "PRJ-A", roots: map[string]string{"bootstrap-key-test": "481ede772da15785c9e59a086201b8c3f9c307129e3fc6d7f34aa4dfed5814e5"}},
		{name: "缺少根版本", raw: keysetGoldenEnvelope, projectNo: "PRJ-A", roots: map[string]string{"bootstrap-key-other": keysetGoldenRootPublicKey}},
		{name: "跨项目", raw: keysetGoldenEnvelope, projectNo: "PRJ-B", roots: roots},
		{name: "篡改", raw: strings.Replace(keysetGoldenEnvelope, `"revision":7`, `"revision":8`, 1), projectNo: "PRJ-A", roots: roots},
		{name: "低revision", raw: keysetGoldenEnvelope, projectNo: "PRJ-A", roots: roots, minimumRevision: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if keyset, err := VerifyKeyset([]byte(test.raw), test.projectNo, test.roots, test.minimumRevision); err == nil {
				t.Fatalf("不可信 keyset 必须拒绝: %+v", keyset)
			}
		})
	}
}

// TestVerifyKeysetUsesRawPayload - 平台追加未知字段后仍按 payload 原文验签，禁止重序列化丢字段。
func TestVerifyKeysetUsesRawPayload(t *testing.T) {
	payload := []byte(strings.TrimSuffix(keysetGoldenPayload, "}") + `,"futureField":{"opaque":[1,2]}}`)
	seed, err := hex.DecodeString(keysetGoldenRootSeed)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := SignPayload(payload, seed)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(struct {
		Version   int             `json:"version"`
		Algorithm string          `json:"algorithm"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}{Version: EnvelopeVersion, Algorithm: Algorithm, Payload: payload, Signature: signature})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyKeyset(raw, "PRJ-A", map[string]string{"bootstrap-key-test": keysetGoldenRootPublicKey}, 0); err != nil {
		t.Fatalf("含未知字段的已签名 payload 原文应通过: %v", err)
	}
}
