package runtime

import "testing"

// TestLookupPublicKey - 公钥查表：精确命中优先，未命中回退小写键，彻底缺失返回 false
func TestLookupPublicKey(t *testing.T) {

	keys := map[string]string{
		"license-key-PRJ-1-ABC": "pub-exact",
		"license-key-prj-2-def": "pub-lower",
	}

	// 精确命中（大小写混合键原样命中）
	if key, ok := LookupPublicKey(keys, "license-key-PRJ-1-ABC"); !ok || key != "pub-exact" {
		t.Fatalf("精确命中失败：ok=%v key=%q", ok, key)
	}
	// 回退小写：keyVersion 大小写混合，map 键被配置库转成小写
	if key, ok := LookupPublicKey(keys, "license-key-PRJ-2-DEF"); !ok || key != "pub-lower" {
		t.Fatalf("小写回退失败：ok=%v key=%q", ok, key)
	}
	// 彻底缺失
	if _, ok := LookupPublicKey(keys, "license-key-PRJ-9-XXX"); ok {
		t.Fatal("不存在的 keyVersion 不应命中")
	}
	// 空表不 panic
	if _, ok := LookupPublicKey(nil, "license-key-PRJ-1-ABC"); ok {
		t.Fatal("空公钥表不应命中")
	}
}
