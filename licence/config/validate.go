package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// configKeyPattern - 配置键/分组名格式：小写字母数字开头，允许 . _ -，全长 ≤128。
// 与 licen-hub backend app/service/licence/pushback.go 的 pushbackKeyPattern 同款。
var configKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// ConfigRuleSet - 配置值校验规则集（存于定义 rules 字段，JSON 对象）
type ConfigRuleSet struct {
	// Required - 值必填（非空串，去除首尾空白后判定）
	Required bool `json:"required"`
	// Regex - 正则约束（RE2 语法），全部类型适用
	Regex string `json:"regex"`
	// Min - 数值下限（仅 int/float 等数值类型生效；指针以区分「未设置」与 0）
	Min *float64 `json:"min"`
	// Max - 数值上限（仅数值类型生效）
	Max *float64 `json:"max"`
	// MinLen - 最小字符数（仅 string 语义类型生效，按 rune 计）
	MinLen *int `json:"minLen"`
	// MaxLen - 最大字符数（仅 string 语义类型生效，按 rune 计）
	MaxLen *int `json:"maxLen"`
	// Enum - 允许值集合；select 类型以 options 为准，不必重复声明（若声明则双重生效）
	Enum []string `json:"enum"`
}

// ConfigValidationError - 单条配置校验失败明细（Where 定位 + Message 原因）。
// 与平台 400 响应 errors 数组同构（where/message），SDK 本地预校验与平台回包共用同一结构。
type ConfigValidationError struct {
	// Where - 失败定位（如 "configs[3].key"、"rate.limit"、"rules.regex"）
	Where string `json:"where"`
	// Message - 失败原因
	Message string `json:"message"`
}

// Error - "where：message" 单行书出（CLI 直接打印可读）
func (this ConfigValidationError) Error() string {

	if this.Where == "" {
		return this.Message
	}
	return this.Where + "：" + this.Message
}

// ParseConfigRuleSet - 解析 RuleSet JSON 原文；空/nil/null/{} 及全零值对象一律返回 nil（无规则）。
// 严格模式：未知字段、类型错误、非对象一律报错。
func ParseConfigRuleSet(raw json.RawMessage) (*ConfigRuleSet, error) {

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var rules ConfigRuleSet
	if err := decoder.Decode(&rules); err != nil {
		return nil, errors.New("rules 必须是合法 RuleSet 对象（未知字段一律拒绝）：" + err.Error())
	}
	// 全零值等价无规则（覆盖 "null"、"{}"、"  {  } "、"{\"required\":false}" 等形态）
	if !rules.Required && rules.Regex == "" && rules.Min == nil && rules.Max == nil &&
		rules.MinLen == nil && rules.MaxLen == nil && len(rules.Enum) == 0 {
		return nil, nil
	}
	return &rules, nil
}

// configValueKind - 值语义归类（type → 校验语义）
type configValueKind int

const (
	// configKindString - 字符串语义（string/input/textarea/radio/checkbox/tags/image/file/color/json/richtext/date 等；仅 minLen/maxLen 生效）
	configKindString configValueKind = iota
	// configKindInt - 整数语义（int/integer）
	configKindInt
	// configKindFloat - 数值语义（float/double/number）
	configKindFloat
	// configKindBool - 布尔语义（bool/boolean/switch）
	configKindBool
	// configKindSelect - 选项语义（select，以 options 为枚举）
	configKindSelect
)

// configKindOf - type → 值语义归类；未知类型按字符串语义兜底（放行，min/max 不生效）
func configKindOf(typ string) configValueKind {

	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "int", "integer":
		return configKindInt
	case "float", "double", "number":
		return configKindFloat
	case "bool", "boolean", "switch":
		return configKindBool
	case "select":
		return configKindSelect
	default:
		return configKindString
	}
}

// configOption - select 选项（value 为提交值，label 为显示名）
type configOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// parseConfigOptions - 解析选项数组原文；空/nil/null 返回 nil（无选项）
func parseConfigOptions(raw json.RawMessage) ([]configOption, error) {

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var options []configOption
	if err := json.Unmarshal(trimmed, &options); err != nil {
		return nil, err
	}
	return options, nil
}

// ValidateConfigDefinition - 校验单条配置项定义（定义反推入口与 SDK 预校验共用）。
// 返回 *ConfigValidationError（Where 为字段名，如 key/label/type/options/rules/default_value/group_path）。
/**
 * @param item ConfigDefinitionItem - 待校验的配置项定义
 * @param groupPaths map[string]struct{} - 可见分组 name 路径集合（groupPath 非空时须命中；空路径 = 未分组，恒合法）
 * @param allowedTypes map[string]struct{} - type 白名单；nil/空 = 不限制（SDK 默认）。Hub 侧由 backend 注入控件白名单
 */
func ValidateConfigDefinition(item ConfigDefinitionItem, groupPaths map[string]struct{}, allowedTypes map[string]struct{}) error {

	if !configKeyPattern.MatchString(item.Key) {
		return &ConfigValidationError{Where: "key", Message: "配置键格式非法（需匹配 ^[a-z0-9][a-z0-9._-]{0,127}$）：" + item.Key}
	}
	if strings.TrimSpace(item.Label) == "" {
		return &ConfigValidationError{Where: "label", Message: "显示名不能为空"}
	}
	if utf8.RuneCountInString(item.Label) > 100 {
		return &ConfigValidationError{Where: "label", Message: "显示名超长（≤100 字符）"}
	}
	if strings.TrimSpace(item.Type) == "" {
		return &ConfigValidationError{Where: "type", Message: "类型不能为空"}
	}
	if len(allowedTypes) > 0 {
		if _, ok := allowedTypes[item.Type]; !ok {
			return &ConfigValidationError{Where: "type", Message: "类型不在允许白名单：" + item.Type}
		}
	}

	// options：非空时必须为合法数组且 value 唯一；select 额外要求非空
	options, err := parseConfigOptions(item.Options)
	if err != nil {
		return &ConfigValidationError{Where: "options", Message: "options 必须是选项数组（[{\"value\":...,\"label\":...}]）：" + err.Error()}
	}
	seenValues := make(map[string]struct{}, len(options))
	for _, option := range options {
		if _, dup := seenValues[option.Value]; dup {
			return &ConfigValidationError{Where: "options", Message: "options 存在重复 value：" + option.Value}
		}
		seenValues[option.Value] = struct{}{}
	}
	if item.Type == "select" && len(options) == 0 {
		return &ConfigValidationError{Where: "options", Message: "select 类型必须提供非空 options"}
	}

	// rules：必须可解析（严格模式）且 regex 可编译
	rules, err := ParseConfigRuleSet(item.Rules)
	if err != nil {
		return &ConfigValidationError{Where: "rules", Message: err.Error()}
	}
	if rules != nil && rules.Regex != "" {
		if _, err = regexp.Compile(rules.Regex); err != nil {
			return &ConfigValidationError{Where: "rules.regex", Message: "regex 无法编译：" + err.Error()}
		}
	}

	// defaultValue 非空时须通过自身 type 校验 + RuleSet 校验（select 的 ∈ options 由值校验覆盖）
	if item.DefaultValue != "" {
		if err = ValidateConfigValue(item.Type, item.Options, rules, item.DefaultValue); err != nil {
			var validationErr *ConfigValidationError
			if errors.As(err, &validationErr) {
				return &ConfigValidationError{Where: "default_value", Message: validationErr.Message}
			}
			return &ConfigValidationError{Where: "default_value", Message: err.Error()}
		}
	}

	// groupPath：空 = 未分组（合法）；非空须存在于可见分组路径集合
	if item.GroupPath != "" {
		if _, ok := groupPaths[item.GroupPath]; !ok {
			return &ConfigValidationError{Where: "group_path", Message: "分组路径不存在：" + item.GroupPath}
		}
	}
	return nil
}

// ValidateConfigValue - 按定义校验单个配置值（值反推入口、管理端值编辑与 SDK 预校验共用）。
// 语义：先 type 可解析性（int/float/bool 必须可解析；select 以 options 为枚举），再 RuleSet
// （required/regex/min/max/minLen/maxLen/enum）。空值（去除首尾空白）只受 required 约束，
// 其余校验跳过（与 Hub 侧空值跳过语义一致）；非数值类型跳过 min/max，非字符串类型跳过 minLen/maxLen。
// 返回 *ConfigValidationError；rules 传 nil = 无规则。
/**
 * @param typ string - 定义类型（int/integer、float/double/number、bool/boolean/switch、select，其余按字符串语义）
 * @param options json.RawMessage - select 选项数组原文（非 select 可传 nil）
 * @param rules *ConfigRuleSet - 已解析的规则集（ParseConfigRuleSet 产物），nil = 无规则
 * @param value string - 待校验的配置值
 */
func ValidateConfigValue(typ string, options json.RawMessage, rules *ConfigRuleSet, value string) error {

	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if rules != nil && rules.Required {
			return &ConfigValidationError{Where: "value", Message: "值为必填项，不能为空"}
		}
		return nil
	}

	kind := configKindOf(typ)
	number := 0.0
	switch kind {
	case configKindInt:
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return &ConfigValidationError{Where: "value", Message: "值必须是整数：" + trimmed}
		}
		number = float64(parsed)
	case configKindFloat:
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return &ConfigValidationError{Where: "value", Message: "值必须是数值：" + trimmed}
		}
		number = parsed
	case configKindBool:
		if _, err := strconv.ParseBool(trimmed); err != nil {
			return &ConfigValidationError{Where: "value", Message: "值必须是布尔值（true/false/1/0）：" + trimmed}
		}
	case configKindSelect:
		// select 以 options 为枚举；options 缺失/为空时不做枚举校验（定义校验已拦截该非法定义）
		choices, err := parseConfigOptions(options)
		if err != nil {
			return &ConfigValidationError{Where: "options", Message: "定义 options 无法解析：" + err.Error()}
		}
		if len(choices) > 0 {
			allowed := make([]string, 0, len(choices))
			for _, choice := range choices {
				allowed = append(allowed, choice.Value)
			}
			if !slices.Contains(allowed, trimmed) {
				return &ConfigValidationError{Where: "value", Message: "值不在 options 可选范围：" + trimmed}
			}
		}
	}

	if rules == nil {
		return nil
	}
	if rules.Regex != "" {
		pattern, err := regexp.Compile(rules.Regex)
		if err != nil {
			return &ConfigValidationError{Where: "rules.regex", Message: "regex 无法编译：" + err.Error()}
		}
		if !pattern.MatchString(trimmed) {
			return &ConfigValidationError{Where: "value", Message: "值不匹配正则 " + rules.Regex + "：" + trimmed}
		}
	}
	if kind == configKindInt || kind == configKindFloat {
		if rules.Min != nil && number < *rules.Min {
			return &ConfigValidationError{Where: "value", Message: "值小于下限 " + strconv.FormatFloat(*rules.Min, 'f', -1, 64) + "：" + trimmed}
		}
		if rules.Max != nil && number > *rules.Max {
			return &ConfigValidationError{Where: "value", Message: "值大于上限 " + strconv.FormatFloat(*rules.Max, 'f', -1, 64) + "：" + trimmed}
		}
	}
	if kind == configKindString {
		length := utf8.RuneCountInString(trimmed)
		if rules.MinLen != nil && length < *rules.MinLen {
			return &ConfigValidationError{Where: "value", Message: "值长度小于 " + strconv.Itoa(*rules.MinLen) + " 字符"}
		}
		if rules.MaxLen != nil && length > *rules.MaxLen {
			return &ConfigValidationError{Where: "value", Message: "值长度超过 " + strconv.Itoa(*rules.MaxLen) + " 字符"}
		}
	}
	if len(rules.Enum) > 0 && !slices.Contains(rules.Enum, trimmed) {
		return &ConfigValidationError{Where: "value", Message: "值不在 enum 允许集合：" + trimmed}
	}
	return nil
}

// ValidateDefinitions - 定义快照校验（SDK 本地预校验与平台侧落库前校验共用，CI 拦错）。
// 校验范围：分组 name 正则、name 路径唯一（含大小写冲突）、parent 闭包与无环、
// 配置项 key 正则与快照内唯一、逐条 ValidateConfigDefinition。
// type 白名单不限制（nil）——平台侧由 backend 注入 Hub 控件白名单兜底。
// 返回 nil 表示全部通过。
func ValidateDefinitions(defs ConfigDefinitions) []ConfigValidationError {

	var failures []ConfigValidationError

	// 分组：name 正则 + 路径唯一（含大小写冲突检测）
	paths := make(map[string]int, len(defs.Groups))         // 完整路径 → groups 下标
	lowerPaths := make(map[string]string, len(defs.Groups)) // 小写路径 → 首个原始路径
	for index, group := range defs.Groups {
		where := "groups[" + strconv.Itoa(index) + "].name"
		if !configKeyPattern.MatchString(group.Name) {
			failures = append(failures, ConfigValidationError{Where: where, Message: "分组名格式非法（需匹配 ^[a-z0-9][a-z0-9._-]{0,127}$）：" + group.Name})
			continue
		}
		path := groupPathOf(group)
		if _, dup := paths[path]; dup {
			failures = append(failures, ConfigValidationError{Where: where, Message: "分组路径重复：" + path})
			continue
		}
		if original, dup := lowerPaths[strings.ToLower(path)]; dup {
			failures = append(failures, ConfigValidationError{Where: where, Message: "分组路径仅大小写差异，与既有分组冲突：" + path + " vs " + original})
			continue
		}
		paths[path] = index
		lowerPaths[strings.ToLower(path)] = path
	}

	// 分组：parent 闭包 + 无环（parent 为完整 name 路径，必然短于子路径，环在结构上不可能；
	// visiting 集合为防御性兜底，防止未来语义漂移）
	for index, group := range defs.Groups {
		if group.Parent == "" {
			continue
		}
		where := "groups[" + strconv.Itoa(index) + "].parent"
		if _, ok := paths[group.Parent]; !ok {
			failures = append(failures, ConfigValidationError{Where: where, Message: "父分组路径不在快照内：" + group.Parent})
			continue
		}
		visiting := map[string]struct{}{groupPathOf(group): {}}
		for cursor := group.Parent; cursor != ""; {
			if _, loop := visiting[cursor]; loop {
				failures = append(failures, ConfigValidationError{Where: where, Message: "分组 parent 链存在环：" + cursor})
				break
			}
			visiting[cursor] = struct{}{}
			parentIndex, ok := paths[cursor]
			if !ok {
				break // 闭包错误已记录
			}
			cursor = defs.Groups[parentIndex].Parent
		}
	}

	// 配置项：key 快照内唯一 + 逐条结构校验（groupPaths 取推送分组的全量路径集合）
	groupPaths := make(map[string]struct{}, len(paths))
	for path := range paths {
		groupPaths[path] = struct{}{}
	}
	seenKeys := make(map[string]int, len(defs.Configs))
	for index, item := range defs.Configs {
		where := "configs[" + strconv.Itoa(index) + "]"
		if previous, dup := seenKeys[item.Key]; dup {
			failures = append(failures, ConfigValidationError{
				Where:   where + ".key",
				Message: "配置键在快照内重复（首次出现于 configs[" + strconv.Itoa(previous) + "]）：" + item.Key,
			})
		} else {
			seenKeys[item.Key] = index
		}
		if err := ValidateConfigDefinition(item, groupPaths, nil); err != nil {
			var validationErr *ConfigValidationError
			if errors.As(err, &validationErr) {
				failures = append(failures, ConfigValidationError{Where: where + "." + validationErr.Where, Message: validationErr.Message})
			} else {
				failures = append(failures, ConfigValidationError{Where: where, Message: err.Error()})
			}
		}
	}
	return failures
}

// groupPathOf - 分组完整 name 路径：parent 为空 = name，否则 parent + "/" + name
func groupPathOf(group ConfigDefinitionGroup) string {

	if group.Parent == "" {
		return group.Name
	}
	return group.Parent + "/" + group.Name
}
