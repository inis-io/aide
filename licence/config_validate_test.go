package licence

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// float64Ptr/intPtr - 测试辅助：取数值指针（RuleSet 的 Min/Max/MinLen/MaxLen 为指针类型）
func float64Ptr(value float64) *float64 { return &value }
func intPtr(value int) *int             { return &value }

// assertValidationWhere - 断言校验失败是 *ConfigValidationError 且 Where 命中预期字段
func assertValidationWhere(t *testing.T, err error, where string) {

	t.Helper()
	var validationErr *ConfigValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("应返回 *ConfigValidationError，实际 %T %v", err, err)
	}
	if validationErr.Where != where {
		t.Fatalf("Where 应为 %q，实际 %q（%v）", where, validationErr.Where, validationErr)
	}
}

// TestParseConfigRuleSet - RuleSet 解析：空/null/{} → nil；全字段；严格模式未知字段/类型错误/非对象拒绝
func TestParseConfigRuleSet(t *testing.T) {

	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage(""), json.RawMessage("null"), json.RawMessage("{}"), json.RawMessage("  {  } ")} {
		rules, err := ParseConfigRuleSet(raw)
		if err != nil || rules != nil {
			t.Fatalf("空 rules（%q）应解析为 nil: %v %v", string(raw), rules, err)
		}
	}

	rules, err := ParseConfigRuleSet(json.RawMessage(`{"required":true,"regex":"^a$","min":0,"max":100,"minLen":1,"maxLen":64,"enum":["a","b"]}`))
	if err != nil {
		t.Fatalf("全字段解析失败: %v", err)
	}
	if !rules.Required || rules.Regex != "^a$" || len(rules.Enum) != 2 {
		t.Fatalf("字段解析异常: %+v", rules)
	}
	// min:0 必须与「未设置」区分（指针语义）
	if rules.Min == nil || *rules.Min != 0 || rules.Max == nil || *rules.Max != 100 {
		t.Fatalf("min/max 指针语义异常: %+v", rules)
	}
	if rules.MinLen == nil || *rules.MinLen != 1 || rules.MaxLen == nil || *rules.MaxLen != 64 {
		t.Fatalf("minLen/maxLen 指针语义异常: %+v", rules)
	}

	// 严格模式：未知字段拒绝（防拼写错误静默失效，如 regex 误写为 regexp）
	if _, err = ParseConfigRuleSet(json.RawMessage(`{"regexp":"^a$"}`)); err == nil {
		t.Fatal("未知字段应拒绝")
	}
	if _, err = ParseConfigRuleSet(json.RawMessage(`{"min":"abc"}`)); err == nil {
		t.Fatal("字段类型错误应拒绝")
	}
	if _, err = ParseConfigRuleSet(json.RawMessage(`["required"]`)); err == nil {
		t.Fatal("非对象应拒绝")
	}
}

// TestValidateConfigDefinition - 定义结构校验：key/label/type/options/rules/defaultValue/groupPath
func TestValidateConfigDefinition(t *testing.T) {

	groupPaths := map[string]struct{}{"storage": {}, "storage/oss": {}}
	valid := ConfigDefinitionItem{
		Key: "storage.driver", Label: "存储驱动", Type: "select", GroupPath: "storage",
		Options:      json.RawMessage(`[{"value":"local","label":"本地存储"},{"value":"oss","label":"阿里云 OSS"}]`),
		Rules:        json.RawMessage(`{"required":true}`),
		DefaultValue: "local",
	}
	if err := ValidateConfigDefinition(valid, groupPaths, nil); err != nil {
		t.Fatalf("合法定义应通过: %v", err)
	}
	// allowedTypes 注入：白名单内放行、白名单外拒绝；nil 不限制
	if err := ValidateConfigDefinition(valid, groupPaths, map[string]struct{}{"select": {}}); err != nil {
		t.Fatalf("白名单内类型应通过: %v", err)
	}
	if err := ValidateConfigDefinition(valid, groupPaths, map[string]struct{}{"input": {}}); err == nil {
		t.Fatal("白名单外类型应拒绝")
	} else {
		assertValidationWhere(t, err, "type")
	}

	cases := []struct {
		name   string
		mutate func(item *ConfigDefinitionItem)
		where  string
	}{
		{"key 非法", func(item *ConfigDefinitionItem) { item.Key = "Bad.Key" }, "key"},
		{"label 空", func(item *ConfigDefinitionItem) { item.Label = "  " }, "label"},
		{"label 超长", func(item *ConfigDefinitionItem) { item.Label = strings.Repeat("签", 101) }, "label"},
		{"type 空", func(item *ConfigDefinitionItem) { item.Type = "" }, "type"},
		{"select 无 options", func(item *ConfigDefinitionItem) { item.Options = nil; item.DefaultValue = "" }, "options"},
		{"options 非数组", func(item *ConfigDefinitionItem) { item.Options = json.RawMessage(`{"a":1}`) }, "options"},
		{"options value 重复", func(item *ConfigDefinitionItem) {
			item.Options = json.RawMessage(`[{"value":"local","label":"A"},{"value":"local","label":"B"}]`)
		}, "options"},
		{"defaultValue 不在 options", func(item *ConfigDefinitionItem) { item.DefaultValue = "cos" }, "default_value"},
		{"rules 未知字段", func(item *ConfigDefinitionItem) { item.Rules = json.RawMessage(`{"require":true}`) }, "rules"},
		{"rules regex 不可编译", func(item *ConfigDefinitionItem) { item.Rules = json.RawMessage(`{"regex":"["}`) }, "rules.regex"},
		{"groupPath 悬空", func(item *ConfigDefinitionItem) { item.GroupPath = "storage/missing" }, "group_path"},
	}
	for _, item := range cases {
		cloned := valid
		item.mutate(&cloned)
		err := ValidateConfigDefinition(cloned, groupPaths, nil)
		if err == nil {
			t.Fatalf("%s 应拒绝", item.name)
		}
		assertValidationWhere(t, err, item.where)
	}

	// defaultValue 过自身 type + rules：int 默认值非整数 / 违反 min
	numeric := ConfigDefinitionItem{
		Key: "rate.limit", Label: "限流", Type: "int",
		Rules: json.RawMessage(`{"min":0,"max":100}`), DefaultValue: "abc",
	}
	assertValidationWhere(t, ValidateConfigDefinition(numeric, groupPaths, nil), "default_value")
	numeric.DefaultValue = "101"
	assertValidationWhere(t, ValidateConfigDefinition(numeric, groupPaths, nil), "default_value")
	numeric.DefaultValue = "50"
	if err := ValidateConfigDefinition(numeric, groupPaths, nil); err != nil {
		t.Fatalf("合法数值默认值应通过: %v", err)
	}
	// groupPath 空 = 未分组，恒合法
	valid.GroupPath = ""
	if err := ValidateConfigDefinition(valid, groupPaths, nil); err != nil {
		t.Fatalf("未分组定义应通过: %v", err)
	}
}

// TestValidateConfigValue - 值校验：type 解析 + RuleSet（required/regex/min/max/minLen/maxLen/enum/select）
func TestValidateConfigValue(t *testing.T) {

	// required：空值仅受 required 约束；非必填空值跳过 type 解析
	if err := ValidateConfigValue("int", nil, &ConfigRuleSet{Required: true}, "  "); err == nil {
		t.Fatal("required 空值应拒绝")
	}
	if err := ValidateConfigValue("int", nil, nil, ""); err != nil {
		t.Fatalf("非必填空值应跳过 type 校验: %v", err)
	}

	// type 解析：int 拒绝小数，float 接受，bool 接受 true/false/1/0
	if err := ValidateConfigValue("int", nil, nil, "42"); err != nil {
		t.Fatalf("int 整数值应通过: %v", err)
	}
	if err := ValidateConfigValue("int", nil, nil, "1.5"); err == nil {
		t.Fatal("int 接受小数应拒绝")
	}
	if err := ValidateConfigValue("float", nil, nil, "1.5"); err != nil {
		t.Fatalf("float 应通过: %v", err)
	}
	if err := ValidateConfigValue("number", nil, nil, "abc"); err == nil {
		t.Fatal("number 非数值应拒绝")
	}
	for _, ok := range []string{"true", "false", "1", "0"} {
		if err := ValidateConfigValue("switch", nil, nil, ok); err != nil {
			t.Fatalf("switch 值 %q 应通过: %v", ok, err)
		}
	}
	if err := ValidateConfigValue("bool", nil, nil, "yes"); err == nil {
		t.Fatal("bool 非布尔值应拒绝")
	}

	// min/max：仅数值类型生效（含 min=0 的指针语义）
	rules := &ConfigRuleSet{Min: float64Ptr(0), Max: float64Ptr(100)}
	if err := ValidateConfigValue("int", nil, rules, "0"); err != nil {
		t.Fatalf("min=0 边界值 0 应通过: %v", err)
	}
	if err := ValidateConfigValue("int", nil, rules, "-1"); err == nil {
		t.Fatal("低于 min 应拒绝")
	}
	if err := ValidateConfigValue("int", nil, rules, "101"); err == nil {
		t.Fatal("高于 max 应拒绝")
	}
	// 非数值类型跳过 min/max
	if err := ValidateConfigValue("string", nil, rules, "9999"); err != nil {
		t.Fatalf("string 类型应跳过 min/max: %v", err)
	}

	// minLen/maxLen：仅字符串语义生效（按 rune 计）
	lengthRules := &ConfigRuleSet{MinLen: intPtr(2), MaxLen: intPtr(4)}
	if err := ValidateConfigValue("input", nil, lengthRules, "配置"); err != nil {
		t.Fatalf("minLen 边界应通过: %v", err)
	}
	if err := ValidateConfigValue("input", nil, lengthRules, "一"); err == nil {
		t.Fatal("低于 minLen 应拒绝")
	}
	if err := ValidateConfigValue("input", nil, lengthRules, "五个字符啊"); err == nil {
		t.Fatal("超过 maxLen 应拒绝")
	}
	// 非字符串类型跳过 minLen/maxLen
	if err := ValidateConfigValue("int", nil, lengthRules, "1"); err != nil {
		t.Fatalf("int 类型应跳过 minLen/maxLen: %v", err)
	}

	// regex（RE2）
	regexRules := &ConfigRuleSet{Regex: `^[a-z0-9-]+$`}
	if err := ValidateConfigValue("input", nil, regexRules, "abc-09"); err != nil {
		t.Fatalf("regex 命中应通过: %v", err)
	}
	if err := ValidateConfigValue("input", nil, regexRules, "ABC"); err == nil {
		t.Fatal("regex 未命中应拒绝")
	}
	// regex 不可编译（值校验路径防御）
	if err := ValidateConfigValue("input", nil, &ConfigRuleSet{Regex: "["}, "abc"); err == nil {
		t.Fatal("regex 不可编译应拒绝")
	}

	// enum：全部类型适用
	enumRules := &ConfigRuleSet{Enum: []string{"a", "b"}}
	if err := ValidateConfigValue("input", nil, enumRules, "a"); err != nil {
		t.Fatalf("enum 命中应通过: %v", err)
	}
	if err := ValidateConfigValue("input", nil, enumRules, "c"); err == nil {
		t.Fatal("enum 未命中应拒绝")
	}

	// select：以 options 为枚举；options 缺失/为空时跳过枚举校验（定义校验已拦截该非法定义）
	options := json.RawMessage(`[{"value":"local","label":"本地"},{"value":"oss","label":"云存储"}]`)
	if err := ValidateConfigValue("select", options, nil, "oss"); err != nil {
		t.Fatalf("select 命中 options 应通过: %v", err)
	}
	if err := ValidateConfigValue("select", options, nil, "cos"); err == nil {
		t.Fatal("select 未命中 options 应拒绝")
	}
	if err := ValidateConfigValue("select", nil, nil, "anything"); err != nil {
		t.Fatalf("select 无 options 应跳过枚举校验: %v", err)
	}
	// 未知类型按字符串语义兜底（minLen 生效、min/max 不生效）
	if err := ValidateConfigValue("richtext", nil, lengthRules, "ok"); err != nil {
		t.Fatalf("未知类型应按字符串语义: %v", err)
	}
}

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
	valid := ConfigDefinitions{
		Groups: []ConfigDefinitionGroup{
			{Name: "storage", Label: "存储"},
			{Name: "oss", Label: "对象存储", Parent: "storage"},
			{Name: "advanced", Label: "高级", Parent: "storage/oss"},
		},
		Configs: []ConfigDefinitionItem{
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
	dupKey.Configs = append(append([]ConfigDefinitionItem{}, valid.Configs...), valid.Configs[0])
	failures := client.ValidateConfigDefinitions(dupKey)
	assertHasFailure(t, failures, "configs[3].key", "重复")

	// 分组 name 非法 + 路径重复
	badGroups := ConfigDefinitions{Groups: []ConfigDefinitionGroup{
		{Name: "Bad Name", Label: "非法"},
		{Name: "a", Label: "A"},
		{Name: "a", Label: "A 重复"},
	}}
	failures = client.ValidateConfigDefinitions(badGroups)
	assertHasFailure(t, failures, "groups[0].name", "格式非法")
	assertHasFailure(t, failures, "groups[2].name", "重复")

	// parent 闭包：父路径不在快照内
	dangling := ConfigDefinitions{Groups: []ConfigDefinitionGroup{
		{Name: "child", Label: "子", Parent: "missing"},
	}}
	failures = client.ValidateConfigDefinitions(dangling)
	assertHasFailure(t, failures, "groups[0].parent", "不在快照内")

	// groupPath 悬空（引用的分组不在快照）
	danglingRef := valid
	danglingRef.Configs = append(append([]ConfigDefinitionItem{}, valid.Configs...),
		ConfigDefinitionItem{Key: "x.y", Label: "悬空", Type: "input", GroupPath: "storage/missing"})
	failures = client.ValidateConfigDefinitions(danglingRef)
	assertHasFailure(t, failures, "configs[3].group_path", "不存在")

	// 逐条结构校验聚合进统一定位（configs[i].field 形态，与平台 errors 数组同构）
	badItem := ConfigDefinitions{Configs: []ConfigDefinitionItem{
		{Key: "a.b", Label: "缺选项", Type: "select"},
	}}
	failures = client.ValidateConfigDefinitions(badItem)
	assertHasFailure(t, failures, "configs[0].options", "非空 options")
}

// assertHasFailure - 断言失败明细中存在指定 where 且消息含给定片段
func assertHasFailure(t *testing.T, failures []ConfigValidationError, where string, messagePart string) {

	t.Helper()
	for _, failure := range failures {
		if failure.Where == where && strings.Contains(failure.Message, messagePart) {
			return
		}
	}
	t.Fatalf("应存在 where=%q 且含 %q 的失败，实际 %+v", where, messagePart, failures)
}
