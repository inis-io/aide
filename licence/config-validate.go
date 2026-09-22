package licence

import (
	"errors"
	"maps"
	"slices"

	"github.com/inis-io/aide/licence/configdef"
)

// 配置校验引擎已整体迁往叶子子包 configdef（全系统唯一实现，SDK 与 licen-hub backend
// 共同 import，禁止在平台侧复制第二份规则引擎）。本文件只保留 Client 上的两个薄壳方法：
//   - ValidateConfig：基于本地平台配置快照做值预校验（快照来自 PlatformConfigSync 缓存）；
//   - ValidateConfigDefinitions：定义快照本地预校验，一行委托 configdef.ValidateDefinitions。

// ValidateConfig - 基于本地平台配置快照（PlatformConfigSync 缓存）对一组键值做本地预校验。
// 供消费端 CLI 在值回推前 fail-fast；逐 key 校验语义：
//   - 有定义且非敏感 → type 可解析 + RuleSet 校验（select 以 options 为枚举）；
//   - sensitive 定义跳过（回推的是脱敏值而非真值）；
//   - 无定义放行（前向兼容）。
//
// 快照为空（从未 PlatformConfigSync，或项目本就无配置项）时返回 nil：本地无字典可校验，
// 以平台侧校验为准。返回 nil 表示全部通过。
func (this *Client) ValidateConfig(items map[string]string) []configdef.ConfigValidationError {

	this.mu.RLock()
	snapshot := make(map[string]PlatformConfigItem, len(this.state.PlatformConfigs))
	maps.Copy(snapshot, this.state.PlatformConfigs)
	this.mu.RUnlock()
	if len(snapshot) == 0 {
		return nil
	}

	var failures []configdef.ConfigValidationError
	for _, key := range slices.Sorted(maps.Keys(items)) {
		definition, exists := snapshot[key]
		if !exists || definition.Sensitive {
			continue
		}
		rules, err := configdef.ParseConfigRuleSet(definition.Rules)
		if err != nil {
			failures = append(failures, configdef.ConfigValidationError{Where: key, Message: "定义 rules 解析失败：" + err.Error()})
			continue
		}
		if err = configdef.ValidateConfigValue(definition.Type, definition.Options, rules, items[key]); err != nil {
			message := err.Error()
			var validationErr *configdef.ConfigValidationError
			if errors.As(err, &validationErr) {
				message = validationErr.Message
			}
			failures = append(failures, configdef.ConfigValidationError{Where: key, Message: message})
		}
	}
	return failures
}

// ValidateConfigDefinitions - 定义快照本地预校验（不上平台即可在 CI 拦错）。
// 校验范围：分组 name 正则、name 路径唯一（含大小写冲突）、parent 闭包与无环、
// 配置项 key 正则与快照内唯一、逐条定义结构校验（引擎实现见 configdef.ValidateDefinitions）。
// type 白名单不限制——平台侧由 backend 注入 Hub 控件白名单兜底。
// 返回 nil 表示全部通过。
func (this *Client) ValidateConfigDefinitions(defs configdef.ConfigDefinitions) []configdef.ConfigValidationError {

	return configdef.ValidateDefinitions(defs)
}
