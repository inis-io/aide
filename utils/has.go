package utils

// HasDuplicate 判断切片中是否存在重复元素
// 支持：string、int、int64、float64、uint 等所有可比较类型
func HasDuplicate[T comparable](slice []T) bool {
	
	seen := make(map[T]struct{}, len(slice))
	
	for _, v := range slice {
		// 发现重复
		if _, exists := seen[v]; exists { return true }
		// 记录已见过的元素
		seen[v] = struct{}{}
	}
	
	// 无重复
	return false
}