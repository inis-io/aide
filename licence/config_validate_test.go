package licence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/inis-io/aide/licence/configdef"
)

// 引擎级测试（ParseConfigRuleSet / ValidateConfigDefinition / ValidateConfigValue）
// 已随校验引擎迁往 configdef 子包（configdef/validate_test.go）；
// 本文件只保留 Client 薄壳方法（ValidateConfig / ValidateConfigDefinitions）的装配级测试。

// TestClientValidateConfig - 本地快照值预校验：逐 key 校验、敏感跳过、无定义放行、快照空放行
func TestClientValidateConfig(t *testing.T) {

	client := &Client{}
	client.state.PlatformConfigs = map[string]PlatformConfigItem{
		"rate.limit": {
			Key: "rate.limit", Type: "number",
			Rules: json.RawMessage(`{"min":0,"max":100}`),
		},
		"app.title": {
			Key: "app.title", Type: "input",
			Rules: json.RawMessage(`{"required":true,"minLen":2}`),
		},
		"storage.driver": {
			Key: "storage.driver", Type: "select",
			Options: json.RawMessage(`[{"value":"local","label":"本地"}]`),
		},
		"security.api_token": {
			Key: "security.api_token", Type: "number", Sensitive: true,
		},
		"feature.mode": {
			Key: "feature.mode", Type: "input",
			Rules: json.RawMessage(`{"enum":["a","b"]}`),
		},
	}

	// 全部合法 → nil
	failures := client.ValidateConfig(map[string]string{
		"rate.limit": "50", "app.title": "系统", "storage.driver": "local", "feature.mode": "a",
	})
	if len(failures) != 0 {
		t.Fatalf("合法值应全部通过: %+v", failures)
	}

	// 逐条失败定位：where = key
	failures = client.ValidateConfig(map[string]string{
		"rate.limit": "101", "app.title": "", "storage.driver": "cos", "feature.mode": "c",
	})
	if len(failures) != 4 {
		t.Fatalf("应产生 4 条失败，实际 %+v", failures)
	}
	for _, failure := range failures {
		if failure.Where == "" || failure.Message == "" {
			t.Fatalf("失败明细缺 where/message: %+v", failure)
		}
	}
	if failures[0].Where != "app.title" { // 输出按 key 排序，首条应为 app.title（required 违规）
		t.Fatalf("失败明细应按键排序，实际 %+v", failures)
	}

	// 敏感定义跳过（脱敏值不做真值校验）；无定义放行（前向兼容）
	failures = client.ValidateConfig(map[string]string{
		"security.api_token": "not-a-number", "unknown.key": "anything",
	})
	if len(failures) != 0 {
		t.Fatalf("敏感键与未定义键应放行: %+v", failures)
	}

	// 快照为空（未同步/项目无配置项）→ nil，以平台侧校验为准
	emptyClient := &Client{}
	if failures = emptyClient.ValidateConfig(map[string]string{"rate.limit": "abc"}); failures != nil {
		t.Fatalf("空快照应放行: %+v", failures)
	}
}

// TestClientValidateConfigDefinitions - 定义快照预校验：分组闭包/深层链、key 唯一、逐条结构校验
func TestClientValidateConfigDefinitions(t *testing.T) {

	client := &Client{}

	// 合法快照：深层分组链 + 引用分组路径
	valid := configdef.ConfigDefinitions{
		Groups: []configdef.ConfigDefinitionGroup{
			{Name: "storage", Label: "存储"},
			{Name: "oss", Label: "对象存储", Parent: "storage"},
			{Name: "advanced", Label: "高级", Parent: "storage/oss"},
		},
		Configs: []configdef.ConfigDefinitionItem{
			{Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage",
				Options: json.RawMessage(`[{"value":"local","label":"本地"}]`)},
			{Key: "storage.oss.ak", Label: "AK", Type: "input", GroupPath: "storage/oss/advanced"},
			{Key: "app.title", Label: "标题", Type: "input"}, // 未分组
		},
	}
	if failures := client.ValidateConfigDefinitions(valid); len(failures) != 0 {
		t.Fatalf("合法快照应通过: %+v", failures)
	}

	// key 快照内重复
	dupKey := valid
	dupKey.Configs = append(append([]configdef.ConfigDefinitionItem{}, valid.Configs...), valid.Configs[0])
	failures := client.ValidateConfigDefinitions(dupKey)
	assertHasFailure(t, failures, "configs[3].key", "重复")

	// 分组 name 非法 + 路径重复
	badGroups := configdef.ConfigDefinitions{Groups: []configdef.ConfigDefinitionGroup{
		{Name: "Bad Name", Label: "非法"},
		{Name: "a", Label: "A"},
		{Name: "a", Label: "A 重复"},
	}}
	failures = client.ValidateConfigDefinitions(badGroups)
	assertHasFailure(t, failures, "groups[0].name", "格式非法")
	assertHasFailure(t, failures, "groups[2].name", "重复")

	// parent 闭包：父路径不在快照内
	dangling := configdef.ConfigDefinitions{Groups: []configdef.ConfigDefinitionGroup{
		{Name: "child", Label: "子", Parent: "missing"},
	}}
	failures = client.ValidateConfigDefinitions(dangling)
	assertHasFailure(t, failures, "groups[0].parent", "不在快照内")

	// groupPath 悬空（引用的分组不在快照）
	danglingRef := valid
	danglingRef.Configs = append(append([]configdef.ConfigDefinitionItem{}, valid.Configs...),
		configdef.ConfigDefinitionItem{Key: "x.y", Label: "悬空", Type: "input", GroupPath: "storage/missing"})
	failures = client.ValidateConfigDefinitions(danglingRef)
	assertHasFailure(t, failures, "configs[3].group_path", "不存在")

	// 逐条结构校验聚合进统一定位（configs[i].field 形态，与平台 errors 数组同构）
	badItem := configdef.ConfigDefinitions{Configs: []configdef.ConfigDefinitionItem{
		{Key: "a.b", Label: "缺选项", Type: "select"},
	}}
	failures = client.ValidateConfigDefinitions(badItem)
	assertHasFailure(t, failures, "configs[0].options", "非空 options")
}

// assertHasFailure - 断言失败明细中存在指定 where 且消息含给定片段
func assertHasFailure(t *testing.T, failures []configdef.ConfigValidationError, where string, messagePart string) {

	t.Helper()
	for _, failure := range failures {
		if failure.Where == where && strings.Contains(failure.Message, messagePart) {
			return
		}
	}
	t.Fatalf("应存在 where=%q 且含 %q 的失败，实际 %+v", where, messagePart, failures)
}
