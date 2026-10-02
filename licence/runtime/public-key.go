package runtime

import "strings"

// LookupPublicKey - 按 keyVersion 查验签公钥：精确命中优先，未命中时回退小写键再查。
//
// 平台签发的 keyVersion 允许大小写混合（如 license-key-PRJ-...），而部分配置库
// （viper 读取 TOML/INI 的 map 键等）会把键统一转小写，导致直接查表永远未命中。
// PublicKeys 与 ReleasePublicKeys 的所有查表统一走本函数，容忍此类接入形态。
func LookupPublicKey(keys map[string]string, keyVersion string) (string, bool) {

	if key, ok := keys[keyVersion]; ok {
		return key, true
	}
	key, ok := keys[strings.ToLower(keyVersion)]
	return key, ok
}
