// Package config - 配置定义与 RuleSet 校验引擎（全系统唯一实现）。
//
// 本包是叶子包：不 import 根包 licence，SDK（本地预校验）与 licen-hub backend
// （平台侧落库校验）共同 import 本包，禁止在平台侧复制第二份规则引擎。
// 覆盖三个校验点：
//   - 定义结构校验（ValidateConfigDefinition / ValidateDefinitions）
//   - 值校验（ValidateConfigValue）
//
// 规则 DSL（RuleSet）严格模式：未知字段一律拒绝，防止拼写错误静默失效。
package config

import "encoding/json"

// ConfigDefinitionGroup - 配置分组定义（客户端权威；Parent 为父分组 name 路径，空 = 顶级）
type ConfigDefinitionGroup struct {
	// Name - 分组名（单段，正则同配置键：^[a-z0-9][a-z0-9._-]{0,127}$）
	Name string `json:"name"`
	// Label - 分组显示名（中文）
	Label string `json:"label"`
	// LabelEn - 分组显示名（英文，可选）
	LabelEn string `json:"label_en"`
	// Icon - 分组图标（可选）
	Icon string `json:"icon"`
	// Sort - 同级排序（小值在前）
	Sort int `json:"sort"`
	// Parent - 父分组 name 路径（斜杠分隔，如 "email"），空 = 顶级分组
	Parent string `json:"parent"`
}

// ConfigDefinitionItem - 配置项定义（客户端权威）
type ConfigDefinitionItem struct {
	// Key - 配置键（正则 ^[a-z0-9][a-z0-9._-]{0,127}$，项目内唯一）
	Key string `json:"key"`
	// Label - 显示名（必填，≤100 字符）
	Label string `json:"label"`
	// Type - 控件/值类型（SDK 预校验不限制白名单，平台侧以 Hub 控件白名单兜底）
	Type string `json:"type"`
	// GroupPath - 所属分组 name 路径（斜杠分隔，如 "email/smtp"），空 = 未分组
	GroupPath string `json:"group_path"`
	// Options - 选项数组 JSON 原文（[{"value":"local","label":"本地存储"}]），空 = null；select 必填
	Options json.RawMessage `json:"options"`
	// Rules - 校验规则集 JSON 原文（RuleSet 对象，见 validate.go），空 = null
	Rules json.RawMessage `json:"rules"`
	// Placeholder - 输入占位文本（可选）
	Placeholder string `json:"placeholder"`
	// Remark - 备注说明（可选）
	Remark string `json:"remark"`
	// DefaultValue - 默认值（可选；非空时须通过自身 type + rules 校验，select 须 ∈ options）
	DefaultValue string `json:"default_value"`
	// Sensitive - 敏感键（值回推只回推脱敏值，平台与 SDK 校验均跳过真值校验）
	Sensitive bool `json:"sensitive"`
	// Sort - 组内排序（小值在前）
	Sort int `json:"sort"`
}

// ConfigDefinitions - 项目级配置定义全量快照（分组树 + 配置项定义）
type ConfigDefinitions struct {
	// Groups - 分组定义列表（parent 链必须在快照内闭合）
	Groups []ConfigDefinitionGroup `json:"groups"`
	// Configs - 配置项定义列表（key 快照内唯一）
	Configs []ConfigDefinitionItem `json:"configs"`
}

// PushbackDiffStats - 分组/配置项各自的 diff 计数
type PushbackDiffStats struct {
	// Created - 快照中存在、库中不存在而新增的条数
	Created int `json:"created"`
	// Updated - 两侧都存在、载荷字段不同而更新的条数
	Updated int `json:"updated"`
	// Deleted - 库中存在、快照中缺失而删除的条数（platform_owned 行不参与）
	Deleted int `json:"deleted"`
	// Unchanged - 完全一致未变的条数（不产生审计）
	Unchanged int `json:"unchanged"`
}
