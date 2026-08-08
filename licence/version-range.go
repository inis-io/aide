package licence

import (
	"strconv"
	"strings"
	"time"
)

// parseRFC3339Milli - RFC3339 时间字符串转毫秒时间戳（空串由调用方先行处理）
func parseRFC3339Milli(text string) (int64, error) {
	item, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return 0, err
	}
	return item.UnixMilli(), nil
}

// VersionInRange - 判断版本号是否落在版本范围表达式内
// 与 licen-hub 平台 app/common/version-range.go 语义完全一致：
// 表达式为空 = 不限制（true）；空格或逗号分隔的比较子（如 ">=2.0.0 <3.0.0"），
// 支持 >= > <= < = != == 算子；版本号按 a.b.c 数值比较（缺段补 0，前导 v/V 容忍）；
// 表达式非法或版本号无法解析 = 拒绝（false，安全兜底）。
/**
 * @param version string - 待判定版本号，如 "2.3.1"
 * @param rangeExpr string - 版本范围表达式，如 ">=2.0.0 <3.0.0"
 * @return bool - 是否在范围内
 * @example：
 * 	licence.VersionInRange("2.3.1", ">=2.0.0 <3.0.0") // true
 */
func VersionInRange(version string, rangeExpr string) bool {

	rangeExpr = strings.TrimSpace(rangeExpr)
	if rangeExpr == "" {
		return true
	}
	target, ok := parseSemver(version)
	if !ok {
		return false
	}

	// 空格与逗号都算分隔符（">=2.0.0, <3.0.0" 与 ">=2.0.0 <3.0.0" 等价）
	fields := strings.FieldsFunc(rangeExpr, func(r rune) bool { return r == ' ' || r == ',' })
	for _, field := range fields {
		op, text := splitComparator(field)
		bound, ok := parseSemver(text)
		if op == "" || !ok {
			return false
		}
		if !matchComparator(compareSemver(target, bound), op) {
			return false
		}
	}
	return true
}

// splitComparator - 拆分比较子为算子与版本号（缺省算子按 = 处理）
func splitComparator(expr string) (op string, version string) {

	for _, candidate := range []string{">=", "<=", "!=", "==", ">", "<", "="} {
		if strings.HasPrefix(expr, candidate) {
			op = candidate
			if op == "==" {
				op = "="
			}
			return op, strings.TrimSpace(expr[len(candidate):])
		}
	}
	return "=", strings.TrimSpace(expr)
}

// matchComparator - 比较结果（-1/0/1）是否满足算子
func matchComparator(cmp int, op string) bool {
	switch op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<=":
		return cmp <= 0
	case "<":
		return cmp < 0
	case "=":
		return cmp == 0
	case "!=":
		return cmp != 0
	}
	return false
}

// parseSemver - 解析 a[.b[.c]] 版本号为三段数值（缺段补 0，前导 v/V 容忍）
func parseSemver(version string) ([3]int, bool) {

	var result [3]int
	version = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(version, "v"), "V"))
	if version == "" {
		return result, false
	}
	parts := strings.Split(version, ".")
	if len(parts) > 3 {
		return result, false
	}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return result, false
		}
		result[index] = value
	}
	return result, true
}

// compareSemver - 数值比较两个三段版本号：a<b 返回 -1，a==b 返回 0，a>b 返回 1
func compareSemver(a, b [3]int) int {
	for index := 0; index < 3; index++ {
		if a[index] != b[index] {
			if a[index] < b[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}
