package utils

import (
	"regexp"
	"strings"
	
	"github.com/spf13/cast"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type CasesParams struct {
	// 是否大写
	IsUpper bool
}

type CasesClass struct {
	// 参数
	Params CasesParams
}

var Cases = &CasesClass{
	Params: CasesParams{
		IsUpper: false,
	},
}

// IsUpper 是否大写（返回副本，避免修改全局单例导致状态串扰）
func (this *CasesClass) IsUpper(yes bool) *CasesClass {
	clone := *this
	clone.Params.IsUpper = yes
	return &clone
}

// Snake 蛇式命名法
func (this *CasesClass) Snake(name any) string {

	// 处理驼峰命名法（包括大驼峰和小驼峰）
	item := regexp.MustCompile("([a-z])([A-Z])")
	text := item.ReplaceAllString(cast.ToString(name), "${1}_${2}")

	switch this.Params.IsUpper {
	case true:
		text = cases.Upper(language.English).String(text)
	default:
		text = cases.Lower(language.English).String(text)
	}

	return strings.ReplaceAll(text, " ", "_")
}

// Kebab 烤肉串式命名法
func (this *CasesClass) Kebab(name any) string {

	// 处理驼峰命名法（包括大驼峰和小驼峰）
	item := regexp.MustCompile("([a-z])([A-Z])")
	text := item.ReplaceAllString(cast.ToString(name), "${1}-${2}")

	switch this.Params.IsUpper {
	case true:
		text = cases.Upper(language.English).String(text)
	default:
		text = cases.Lower(language.English).String(text)
	}

	return strings.ReplaceAll(text, " ", "-")
}

// Space 空格命名法
func (this *CasesClass) Space(name any) string {

	// 处理驼峰命名法（包括大驼峰和小驼峰）
	item := regexp.MustCompile("([a-z])([A-Z])")
	text := item.ReplaceAllString(cast.ToString(name), "${1} ${2}")

	switch this.Params.IsUpper {
	case true:
		text = cases.Upper(language.English).String(text)
	default:
		text = cases.Lower(language.English).String(text)
	}

	return strings.ReplaceAll(text, " ", " ")
}

// Camel 骆驼命名法
func (this *CasesClass) Camel(name any) string {

	// 先将字符串按分隔符（这里假设是空格或下划线）分割成单词切片
	words := regexp.MustCompile(`[\s_]+`).Split(cast.ToString(name), -1)

	// 转换每个单词为首字母大写
	var text string

	switch this.Params.IsUpper {
	case true:
		for _, word := range words {
			text += cases.Title(language.English).String(word)
		}
	default:
		for i, word := range words {
			if i == 0 {
				text += word
			} else {
				text += cases.Title(language.English).String(word)
			}
		}

		text = cases.Lower(language.English).String(text[:1]) + text[1:]
	}

	return text
}