package utils

import (
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	
	"github.com/spf13/cast"
)

// ForMap - 遍历数组，返回新数组
func ForMap[T any](slice []T, fun func(item T) (result T)) (newSlice []T) {
	for key, val := range slice {
		slice[key] = fun(val)
	}
	return slice
}

// Ternary - 三元运算符
func Ternary[T any](IF bool, TRUE T, FALSE T) T {
	if IF {
		return TRUE
	}
	return FALSE
}

// Default - 设置默认值
func Default[T any](param T, value ...T) (result T) {
	if Is.Empty(param) {
		if len(value) > 0 {
			return value[0]
		}
		return result
	}
	return param
}

// Replace - 字符串替换
func Replace(value any, params map[string]any) (result string) {
	result = cast.ToString(value)
	for key, val := range params {
		result = strings.Replace(result, key, cast.ToString(val), -1)
	}
	return result
}

func GetCaller() (funcName string, fileName string, line int) {
	pc, fileName, line, ok := runtime.Caller(2)
	if !ok {
		return "", "", 0
	}
	funcName = runtime.FuncForPC(pc).Name()
	return funcName, fileName, line
}

type caller struct {
	// 文件名
	FileName string
	// 函数名
	FuncName string
	// 行号
	Line int
}

// Caller 获取代码调用者
func Caller() *caller {
	funcName, fileName, line := GetCaller()
	return &caller{
		FileName: fileName,
		FuncName: funcName,
		Line:     line,
	}
}

// Calc - 计算器
func Calc(input any) (output float64) {

	var stack, postfix []string
	// 是否为操作符
	operator := []string{"+", "-", "*", "/"}

	// 操作符优先级
	priority := func(operator string) int {
		switch operator {
		case "+", "-":
			return 1
		case "*", "/":
			return 2
		}
		return 0
	}

	reg := regexp.MustCompile(`\d+(\.\d*)?|[+\-*/()]`)

	for _, token := range reg.FindAllString(cast.ToString(input), -1) {

		if InArray(token, operator) {

			for len(stack) > 0 && priority(stack[len(stack)-1]) >= priority(token) {
				postfix = append(postfix, stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, token)

		} else if token == "(" {

			stack = append(stack, token)

		} else if token == ")" {

			for len(stack) > 0 && stack[len(stack)-1] != "(" {
				postfix = append(postfix, stack[len(stack)-1])
				stack = stack[:len(stack)-1]
			}
			stack = stack[:len(stack)-1]

		} else {

			postfix = append(postfix, token)
		}
	}

	for len(stack) > 0 {
		postfix = append(postfix, stack[len(stack)-1])
		stack = stack[:len(stack)-1]
	}

	var result []float64

	for _, token := range postfix {

		if InArray(token, operator) {

			right := result[len(result)-1]
			result = result[:len(result)-1]
			left := result[len(result)-1]
			result = result[:len(result)-1]

			switch token {
			case "+":
				result = append(result, left+right)
			case "-":
				result = append(result, left-right)
			case "*":
				result = append(result, left*right)
			case "/":
				result = append(result, left/right)
			}

		} else {

			num, _ := strconv.ParseFloat(token, 64)
			result = append(result, num)
		}
	}

	// 表达式没有匹配到任何有效内容时，避免索引越界
	if len(result) == 0 {
		return 0
	}

	return result[0]
}

var Ascii = &AsciiClass{}
type AsciiClass struct {}

// ToString - 根据ASCII码排序
func (this *AsciiClass) ToString(params map[string]any, omitempty bool) (result string) {

	// 字典排序
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var item strings.Builder
	for _, key := range keys {
		val := params[key]
		if len(key) > 0 && (len(cast.ToString(val)) > 0 || !omitempty) {
			item.WriteString(key + "=" + cast.ToString(val) + "&")
		}
	}

	// 去除最后一个 &
	text := item.String()
	if len(text) > 0 {
		text = text[:len(text)-1]
	}

	// 返回排序后的字符串
	return text
}