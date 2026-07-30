package utils

import (
	"reflect"
)

// InArray - 判断某个值是否在数组中
func InArray[T any](value T, array []T) (ok bool) {
	for _, val := range array {
		if reflect.DeepEqual(value, val) {
			return true
		}
	}
	return false
}