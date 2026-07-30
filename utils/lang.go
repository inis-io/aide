package utils

import (
	"fmt"
	"strconv"
	"strings"
	
	"github.com/spf13/cast"
)

var Lang *LangClass

func init() {
	// 初始化语言包实例（New 内部不依赖 Lang 自身）
	Lang = (&LangClass{}).New()
}

type LangClass struct {
	Directory string // 语言包目录
	Lang      string // 当前语言
	Mode      string // 文件类型
}

func (this *LangClass) Value(key any, args ...any) (result any) {

	// 读取语言包
	bytes := File().Byte(this.Directory + this.Lang + "." + this.Mode)

	if bytes.Error != nil { return }

	text := cast.ToString(key)

	// 解析语言包
	lang := cast.ToStringMap(Json.Decode(bytes.Text))

	// 获取语言
	result = lang[text]

	// 如果没有找到语言，通过javascript风格获取
	if Is.Empty(result) {
		item, err := Json.Get(bytes.Text, text)
		if err == nil { result = item }
	}

	// 如果没有找到语言，则返回原文
	if Is.Empty(result) {
		return fmt.Sprintf(text, args...)
	}

	// 如果有参数，则格式化
	if len(args) > 0 {
		return fmt.Sprintf(cast.ToString(result), args...)
	}

	return
}

// New 实例化
func (this *LangClass) New(model ...LangClass) *LangClass {

	item := new(LangClass)

	// 合并参数
	if len(model) > 0 {
		item = &model[0]
	}

	// 设置默认值
	item.Lang = Ternary[string](!Is.Empty(item.Lang), item.Lang, "zh-cn")
	item.Mode = Ternary[string](!Is.Empty(item.Mode), item.Mode, "json")
	item.Directory = Ternary[string](!Is.Empty(item.Directory), item.Directory, "config/i18n/")

	return item
}

type Language struct {
	Language string
	Quality  float64
}

// AcceptLanguage - 解析请求头的 Accept-Language
func (this *LangClass) AcceptLanguage(value any) (string, []Language, error) {

	var hot Language
	var items []Language

	for _, val := range strings.Split(cast.ToString(value), ",") {

		params  := strings.Split(val, ";")
		lang    := params[0]
		quality := 1.0

		if len(params) > 1 {
			q, err := strconv.ParseFloat(strings.TrimPrefix(params[1], "q="), 64)
			if err != nil {
				return "", nil, fmt.Errorf("invalid quality value: %v", err)
			}
			quality = q
		}

		item := Language{
			Language: lang,
			Quality : quality,
		}
		items = append(items, item)

		if len(hot.Language) == 0 || item.Quality > hot.Quality {
			hot = item
		}
	}

	return hot.Language, items, nil
}