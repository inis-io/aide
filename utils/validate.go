package utils

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/spf13/cast"
)

type validator struct {
	model   any
	message map[string]string
	rule    func(name string, value any, rule string, message map[string]string) error
}

// Validate - 验证器入口
var Validate = func(model any) *validator {
	return &validator{
		model: model,
		rule:  ValidateRules,
	}
}

// Validate - 验证模型
func (this *validator) Validate(model any) *validator {
	this.model = model
	return this
}

// Message - 自定义错误信息
func (this *validator) Message(message map[string]string) *validator {
	this.message = message
	return this
}

// Rule - 自定义校验规则
func (this *validator) Rule(callback func(name string, value any, rule string, message map[string]string) error) *validator {
	this.rule = callback
	return this
}

// Check - 验证
func (this *validator) Check(params any) (err error) {

	if this.model == nil {
		return errors.New("model is nil")
	}

	// params 转 string
	data, _ := json.Marshal(params)

	// string 转 map
	var tempMap map[string]any
	err = json.Unmarshal(data, &tempMap)

	if err != nil {
		return err
	}

	// 解指针并校验 model 必须为结构体，避免 NumField 崩溃
	model := reflect.TypeOf(this.model)
	if model.Kind() == reflect.Ptr {
		model = model.Elem()
	}
	if model.Kind() != reflect.Struct {
		return errors.New("model must be a struct or pointer to struct")
	}

	// 获取结构体的字段和标签
	for i := 0; i < model.NumField(); i++ {

		field := model.Field(i)
		tag := field.Tag
		rule := tag.Get("rule")

		// 字段名（剥离 json tag 中的 ,omitempty 等选项）
		var name string
		// 字段值
		var value any
		if jsonTag := strings.Split(tag.Get("json"), ",")[0]; !Is.Empty(jsonTag) {
			name = jsonTag
		} else {
			name = field.Name
		}
		value = tempMap[name]

		if Is.Empty(rule) {
			// 结束本次循环，进入下一次循环
			continue
		}

		err := this.rule(name, value, rule, this.message)
		if err != nil {
			return err
		}
	}

	return nil
}

// ValidateRules - 验证规则
/**
 * @rule - 内置规则 - 如下：
 * required：必填
 * min：最小值
 * max：最大值
 * email：是否为邮箱
 * number：是否为数字
 * float：是否为浮点数
 * bool：是否为布尔值
 * slice：是否为切片 - （数组）
 * jsonStr：是否为 JSON 字符串
 * accepted：验证某个字段是否为为 yes, on, 或是 1
 * date：是否为日期
 * alpha：只能包含字母
 * alphaNum：只能包含字母和数字
 * alphaDash：只能包含字母、数字和下划线_及破折号-
 * chs：只能包含汉字
 * chsAlpha：只能包含汉字、字母
 * chsAlphaNum：只能包含汉字、字母和数字
 * chsDash：只能是汉字、字母、数字和下划线_及破折号-
 * cntrl：是否为控制字符 - （换行、缩进、空格）
 * graph：是否为可见字符 - （除空格外的所有可打印字符）
 * lower：是否为小写字母
 * upper：是否为大写字母
 * space：是否为空白字符 - （空格、制表符、换页符等）
 * xdigit：是否为十六进制字符 - （0-9、a-f、A-F）
 * activeUrl：是否为有效的域名或者IP
 * url：是否为有效的URL地址
 * ip：是否为IP地址
 * mobile：是否为手机号
 * idCard：是否为身份证号
 * MacAddr：是否为MAC地址
 * zip：是否为邮政编码
 **/
func ValidateRules(name string, value any, rule string, message map[string]string) (err error) {

	// 获取 rule 中的规则 - 字符串切片，逗号分隔
	rules := strings.Split(rule, ",")

	// 判断 rules 是否包含 required
	if !InArray("required", cast.ToStringSlice(rules)) {
		// 判断 value 是否为空
		if Is.Empty(value) {
			// 结束本次循环，进入下一次循环
			return nil
		}
	}

	for _, val := range rules {
		if strings.Contains(val, "=") {
			// 获取规则和参数
			ruleAndParam := strings.Split(val, "=")
			first := ruleAndParam[0]
			second := ruleAndParam[1]

			switch first {
			case "min":
				// 判断长度是否小于最小值
				if second != "" && len(cast.ToString(value)) < cast.ToInt(second) {
					msg := message[name+"."+first]
					if msg == "" {
						msg = name + " length cannot be less than " + second + "！"
					}
					return errors.New(msg)
				}
			case "max":
				// 判断长度是否大于最大值
				if second != "" && len(cast.ToString(value)) > cast.ToInt(second) {
					msg := message[name+"."+first]
					if msg == "" {
						msg = name + " length cannot be greater than " + second + "！"
					}
					return errors.New(msg)
				}
			}

		} else {

			switch val {
			case "required":
				if Is.Empty(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " is not empty!"
					}
					return errors.New(msg)
				}
			case "email":
				if !Is.Email(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <email> format!"
					}
					return errors.New(msg)
				}
			case "number":
				if !Is.Number(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <number> format!"
					}
					return errors.New(msg)
				}
			case "float":
				if !Is.Float(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <float> format!"
					}
					return errors.New(msg)
				}
			case "bool":
				if !Is.Bool(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <bool> format!"
					}
					return errors.New(msg)
				}
			case "slice":
				if !Is.Slice(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <array> format!"
					}
					return errors.New(msg)
				}
			case "jsonStr":
				if !Is.JsonString(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <json string> format!"
					}
					return errors.New(msg)
				}
			case "accepted":
				if !Is.Accepted(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "date":
				if !Is.Date(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect! Required in <date> format!"
					}
					return errors.New(msg)
				}
			case "alpha":
				if !Is.Alpha(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "alphaNum":
				if !Is.AlphaNum(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "alphaDash":
				if !Is.AlphaDash(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "chs":
				if !Is.Chs(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "chsAlpha":
				if !Is.ChsAlpha(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "chsAlphaNum":
				if !Is.ChsAlphaNum(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "chsDash":
				if !Is.ChsDash(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "cntrl":
				if !Is.Cntrl(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "graph":
				if !Is.Graph(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "lower":
				if !Is.Lower(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "upper":
				if !Is.Upper(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "space":
				if !Is.Space(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "xdigit":
				if !Is.Xdigit(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "activeUrl":
				if !Is.ActiveUrl(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "url":
				if !Is.Url(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "ip":
				if !Is.Ip(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "mobile":
				if !Is.Mobile(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "idCard":
				if !Is.IdCard(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "MacAddr":
				if !Is.MacAddr(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			case "zip":
				if !Is.Zip(value) {
					msg := message[name+"."+val]
					if msg == "" {
						msg = name + " format is incorrect!"
					}
					return errors.New(msg)
				}
			}
		}
	}

	return nil
}
