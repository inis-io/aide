package utils

import (
	cryptorand "crypto/rand"
	"fmt"
	"math/rand"
	"time"

	"github.com/spf13/cast"
)

// Rand - 随机数
var Rand *RandClass

type RandClass struct {}

// Number - 生成指定长度的随机数（数字验证码，使用加密安全随机源）
func (this *RandClass) Number(length any) (result string) {

	// 逐位读取随机字节映射到数字（>=250 的字节重取，消除取模偏差）
	for i := 0; i < cast.ToInt(length); i++ {
		b := make([]byte, 1)
		for {
			if _, err := cryptorand.Read(b); err != nil {
				// 随机源不可用时退化为伪随机，保证输出形态不变
				b[0] = byte(rand.Intn(10))
				break
			}
			if b[0] < 250 { break }
		}
		result += fmt.Sprintf("%d", b[0]%10)
	}

	return result
}

// String - 生成随机字符串
func (this *RandClass) String(length any, chars ...string) (result string) {

	var charset string

	if Is.Empty(chars) {
		charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	} else {
		charset = chars[0]
	}
	var seededRand = rand.New(rand.NewSource(time.Now().UnixNano()))

	item := make([]byte, cast.ToInt(length))
	for i := range item {
		item[i] = charset[seededRand.Intn(len(charset))]
	}

	return string(item)
}

// Code - 生成随机验证码
// number:数字, letter:字母, mix:混合
func (this *RandClass) Code(length any, mode ...string) (result string) {

	var charset string

	if Is.Empty(mode) {
		charset = "number"
	} else {
		charset = mode[0]
	}

	switch charset {
	case "number":
		return this.Number(length)
	case "letter":
		return this.String(length, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	case "mix":
		return this.String(length, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	default:
		return this.Number(length)
	}
}

// Int - 生成随机整数
func (this *RandClass) Int(max any, min ...any) (result int) {
	if Is.Empty(min) {
		min = []any{0}
	}
	if cast.ToInt(max) <= cast.ToInt(min[0]) {
		// 交换两个数
		max, min[0] = min[0], cast.ToInt(max)
	}
	if max == min[0] {
		return cast.ToInt(max)
	}
	return rand.Intn(cast.ToInt(max)-cast.ToInt(min[0])) + cast.ToInt(min[0])
}

// Slice - 返回随机的指定长度的切片
func (this *RandClass) Slice(slice []any, limit any) (result []any) {

	// 如果切片为空，直接返回
	if len(slice) == 0 {
		return slice
	}

	// 限制最大长度，超过切片长度时返回全部
	n := cast.ToInt(limit)
	if n < 0 { n = 0 }
	if n > len(slice) { n = len(slice) }

	// 拷贝副本后打乱顺序取前 N 个，避免修改调用方的原切片
	item := append([]any{}, slice...)
	rand.Shuffle(len(item), func(i, j int) {
		item[i], item[j] = item[j], item[i]
	})

	return item[:n]
}

// MapSlice - 打乱切片顺序
func (this *RandClass) MapSlice(slice []map[string]any) (result []map[string]any) {

	// 如果切片为空，直接返回
	if len(slice) == 0 {
		return slice
	}

	// 打乱切片顺序
	rand.Shuffle(len(slice), func(i, j int) {
		slice[i], slice[j] = slice[j], slice[i]
	})

	return slice
}
