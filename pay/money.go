package pay

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

var (
	currencyMu sync.RWMutex
	currencies = map[string]uint8{
		"CNY": 2, "USD": 2, "EUR": 2, "GBP": 2, "HKD": 2,
		"JPY": 0, "KRW": 0, "VND": 0,
		"BHD": 3, "JOD": 3, "KWD": 3,
	}
)

// Currency - 币种及其最小单位精度
type Currency struct {
	// Code - ISO 4217 三位币种代码
	Code string `json:"code"`
	// Exponent - 最小货币单位的小数位数
	Exponent uint8 `json:"exponent"`
}

// Money - 以最小货币单位整数表示的金额
type Money struct {
	// Minor - 最小货币单位整数
	Minor int64 `json:"minor"`
	// Currency - 币种及精度
	Currency Currency `json:"currency"`
}

// RegisterCurrency - 显式注册币种精度
func RegisterCurrency(code string, exponent uint8) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 3 || exponent > 9 {
		return fmt.Errorf("%w：币种或精度非法", ErrInvalidRequest)
	}
	currencyMu.Lock()
	defer currencyMu.Unlock()
	if old, ok := currencies[code]; ok && old != exponent {
		return fmt.Errorf("%w：币种 %s 已注册为 %d 位精度", ErrInvalidRequest, code, old)
	}
	currencies[code] = exponent
	return nil
}

// NewMoneyMinor - 从最小货币单位创建金额；未知币种会生成不可通过 Validate 的值
func NewMoneyMinor(minor int64, currency string) Money {
	code := strings.ToUpper(strings.TrimSpace(currency))
	exponent, ok := currencyExponent(code)
	if !ok {
		exponent = math.MaxUint8
	}
	return Money{Minor: minor, Currency: Currency{Code: code, Exponent: exponent}}
}

// ParseMoney - 从十进制主单位字符串解析金额
func ParseMoney(value string, currency string) (Money, error) {
	code := strings.ToUpper(strings.TrimSpace(currency))
	exponent, ok := currencyExponent(code)
	if !ok {
		return Money{}, fmt.Errorf("%w：未知币种 %s", ErrInvalidRequest, code)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return Money{}, fmt.Errorf("%w：金额为空", ErrInvalidRequest)
	}
	negative := strings.HasPrefix(value, "-")
	if negative || strings.HasPrefix(value, "+") {
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return Money{}, fmt.Errorf("%w：金额格式非法", ErrInvalidRequest)
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > int(exponent) {
		return Money{}, fmt.Errorf("%w：金额精度超过币种精度", ErrInvalidRequest)
	}
	if !digits(parts[0]) || (frac != "" && !digits(frac)) {
		return Money{}, fmt.Errorf("%w：金额格式非法", ErrInvalidRequest)
	}
	frac += strings.Repeat("0", int(exponent)-len(frac))
	digitsValue := strings.TrimLeft(parts[0]+frac, "0")
	if digitsValue == "" {
		digitsValue = "0"
	}
	if negative {
		digitsValue = "-" + digitsValue
	}
	minor, err := strconv.ParseInt(digitsValue, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w：金额超出范围", ErrInvalidRequest)
	}
	return Money{Minor: minor, Currency: Currency{Code: code, Exponent: exponent}}, nil
}

// MajorString - 返回不经过浮点数的主单位金额字符串
func (this Money) MajorString() string {
	exponent := int(this.Currency.Exponent)
	negative := this.Minor < 0
	var magnitude uint64
	if negative {
		magnitude = uint64(-(this.Minor + 1)) + 1
	} else {
		magnitude = uint64(this.Minor)
	}
	digitsValue := strconv.FormatUint(magnitude, 10)
	if exponent > 0 {
		if len(digitsValue) <= exponent {
			digitsValue = strings.Repeat("0", exponent-len(digitsValue)+1) + digitsValue
		}
		index := len(digitsValue) - exponent
		digitsValue = digitsValue[:index] + "." + digitsValue[index:]
	}
	if negative {
		return "-" + digitsValue
	}
	return digitsValue
}

// Validate - 校验币种、精度和金额表示
func (this Money) Validate() error {
	exponent, ok := currencyExponent(strings.ToUpper(this.Currency.Code))
	if !ok || this.Currency.Code != strings.ToUpper(this.Currency.Code) || exponent != this.Currency.Exponent {
		return fmt.Errorf("%w：币种或精度无效", ErrInvalidRequest)
	}
	return nil
}

// IsPositive - 判断金额是否为正数且币种有效
func (this Money) IsPositive() bool { return this.Minor > 0 && this.Validate() == nil }

// SameCurrency - 判断两个金额是否属于相同币种和精度
func (this Money) SameCurrency(other Money) bool { return this.Currency == other.Currency }

// Add - 安全相加两个同币种金额
func (this Money) Add(other Money) (Money, error) {
	if this.Validate() != nil || other.Validate() != nil || !this.SameCurrency(other) {
		return Money{}, fmt.Errorf("%w：币种不一致", ErrInvalidRequest)
	}
	if (other.Minor > 0 && this.Minor > math.MaxInt64-other.Minor) || (other.Minor < 0 && this.Minor < math.MinInt64-other.Minor) {
		return Money{}, fmt.Errorf("%w：金额溢出", ErrInvalidRequest)
	}
	this.Minor += other.Minor
	return this, nil
}

// Sub - 安全相减两个同币种金额
func (this Money) Sub(other Money) (Money, error) {
	if other.Minor == math.MinInt64 {
		return Money{}, fmt.Errorf("%w：金额溢出", ErrInvalidRequest)
	}
	other.Minor = -other.Minor
	return this.Add(other)
}

func currencyExponent(code string) (uint8, bool) {
	currencyMu.RLock()
	defer currencyMu.RUnlock()
	exponent, ok := currencies[code]
	return exponent, ok
}

func digits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
