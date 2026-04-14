package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// Mask - 脱敏
var Mask *MaskClass

type MaskClass struct {}


// Phone - 手机号脱敏
func (this *MaskClass) Phone(phone string) string {
	
	if Is.Empty(phone) { return "" }
	
	// 保留前3位和后4位，长度不一定为11
	if len(phone) <= 7 {
		
		// 长度小于等于7时，无法同时保留3前4后，按能保留的优先处理
		if len(phone) <= 3 { return phone }
		
		// 保留前3位和最后1位，长度不一定固定
		if len(phone) <= 4 {
			// 长度小于等于3时直接返回，长度为4时保留前三和最后1位（中间无星号）
			if len(phone) <= 3 { return phone }
			return fmt.Sprintf("%s%s", phone[:3], phone[3:])
		}
		
		// 中间替换为对应数量的星号，保留前3位和最后1位
		midLen := len(phone) - 4
		return fmt.Sprintf("%s%s%s", phone[:3], strings.Repeat("*", midLen), phone[len(phone)-1:])
	}
	
	// 中间替换为对应数量的星号
	midLen := len(phone) - 7
	return fmt.Sprintf("%s%s%s", phone[:3], strings.Repeat("*", midLen), phone[len(phone)-4:])
}

// Email - 邮箱脱敏
func (this *MaskClass) Email(email string) string {
	
	if Is.Empty(email) { return "" }
	
	// 简单的邮箱格式正则校验，实际中可以使用更严格的校验方式
	match, _ := regexp.MatchString(`^[\w-]+(\.[\w-]+)*@[\w-]+(\.[\w-]+)+$`, email)
	
	if !match { return email }
	
	emailArr  := strings.Split(email, "@")
	emailName := emailArr[0]
	
	if len(emailName) > 3 {
		prefix := emailName[:3]
		last   := emailName[len(emailName)-1:]
		emailName = prefix + "****" + last
	}
	
	emailDomain := emailArr[1]
	
	return fmt.Sprintf("%s@%s", emailName, emailDomain)
}

// IDCard - 身份证号脱敏
func (this *MaskClass) IDCard(idCard string) string {

	if Is.Empty(idCard) { return "" }

	// 简单的身份证号格式正则校验，实际中可以使用更严格的校验方式
	match, _ := regexp.MatchString(`^\d{17}[\dXx]$`, idCard)

	if !match { return idCard }

	return fmt.Sprintf("%s****%s", idCard[:4], idCard[14:])
}

// BankCard - 银行卡号脱敏
func (this *MaskClass) BankCard(bankCard string) string {

	if Is.Empty(bankCard) { return "" }

	return fmt.Sprintf("%s****%s", bankCard[:4], bankCard[len(bankCard)-4:])
}

// Password - 密码脱敏
func (this *MaskClass) Password(password string) string {

	if Is.Empty(password) { return "" }

	return "******"
}

// Custom - 自定义脱敏（支持中文字符）
func (this *MaskClass) Custom(str string, start, end int) string {
	
	if Is.Empty(str) { return "" }
	
	// 转换为 rune 切片，按字符处理
	runes  := []rune(str)
	strLen := len(runes)
	
	// 边界检查（使用字符长度）
	if start < 0 || end < 0 || start > end || end > strLen {
		return str
	}
	
	// 处理 start 和 end 相同的情况（脱敏空字符串，直接返回原串）
	if start == end {
		return str
	}
	
	// 拼接：前 start 个字符 + "****" + 从 end 开始的字符
	return fmt.Sprintf("%s****%s", string(runes[:start]), string(runes[end:]))
}

// Name - 中文姓名脱敏
func (this *MaskClass) Name(name string) string {
	
	if Is.Empty(name) { return "" }
	
	// 转换为 rune 切片以正确处理中文字符
	nameRunes := []rune(name)
	nameLen   := len(nameRunes)
	
	// 根据长度进行脱敏处理
	switch nameLen {
	case 1:
		// 单字姓名：直接返回原字（无法脱敏）
		return name
	
	case 2:
		// 两字姓名：张三 -> 张*
		return string(nameRunes[:1]) + "*"
	
	case 3:
		// 三字姓名：张三丰 -> 张*丰
		return string(nameRunes[:1]) + "*" + string(nameRunes[2:])
	
	case 4:
		// 四字姓名：欧阳慕容 -> 欧**蓉
		// 或：张小三丰 -> 张***丰
		prefix := string(nameRunes[:1])
		suffix := string(nameRunes[nameLen-1:])
		return prefix + "**" + suffix
	
	default:
		// 五字及以上：保留首尾各一个字符，中间全部用*代替
		// 例如：欧阳慕容复 -> 欧***复
		prefix  := string(nameRunes[:1])
		suffix  := string(nameRunes[nameLen-1:])
		maskLen := nameLen - 2
		mask  := ""
		for i := 0; i < maskLen; i++ {
			mask += "*"
		}
		return prefix + mask + suffix
	}
}