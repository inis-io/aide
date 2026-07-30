package utils

import (
	"reflect"
	"regexp"
	"strings"
)

func typeof(args ...any) (typeof string, empty bool) {
	typeof, empty = "string", false
	for _, item := range args {

		// 判断是否为空
		if item == nil {
			empty = true
			continue
		}

		// 先利用反射获取数据类型，再进入不同类型的判空逻辑
		kind := reflect.TypeOf(item).Kind()
		typeof = kind.String()
		switch kind {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// int 系，0 为空
			if reflect.ValueOf(item).Int() == 0 {
				empty = true
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			// uint 系（byte 与 uint8 同 kind，统一标记为 uint8），0 为空
			if reflect.ValueOf(item).Uint() == 0 {
				empty = true
			}
		case reflect.Float32, reflect.Float64:
			// 浮点系，0 为空
			if reflect.ValueOf(item).Float() == 0 {
				empty = true
			}
		case reflect.Bool:
			// false 为空
			if !reflect.ValueOf(item).Bool() {
				empty = true
			}
		case reflect.String:
			if item == "" {
				empty = true
			}
		case reflect.Ptr, reflect.Interface:
			// 接口状态下，它不认为自己是nil，所以要用反射判空
			if reflect.ValueOf(item).IsNil() {
				empty = true
			}
		case reflect.Slice, reflect.Array:
			s := reflect.ValueOf(item)
			// 遍历所有元素，元素包含 map 时标记为二维
			for i := 0; i < s.Len(); i++ {
				elem := reflect.ValueOf(s.Index(i).Interface())
				if elem.Kind() == reflect.Map {
					if kind == reflect.Slice {
						typeof = "2d slice"
					} else {
						typeof = "2d array"
					}
					break
				}
			}
			if s.Len() == 0 {
				empty = true
			}
		case reflect.Map, reflect.Chan:
			if reflect.ValueOf(item).Len() == 0 {
				empty = true
			}
		case reflect.Func:
			if reflect.ValueOf(item).IsNil() {
				empty = true
			}
		case reflect.Struct:
			// 结构体不判空
		default:
			// 其他类型不判空
		}

	}
	return
}

func CustomProcessApi(url string, api string) (result string) {
	if empty := Is.Empty(api); empty {
		api = "api"
	}
	result = url
	if empty := Is.Empty(url); !empty {
		prefix := "//"
		if check := strings.HasPrefix(url, "https://"); check {
			prefix = "https://"
		} else if check := strings.HasPrefix(url, "http://"); check {
			prefix = "http://"
		}
		// 正则匹配 http(s):// - 并去除
		url = regexp.MustCompile("^((https|http)?://)").ReplaceAllString(url, "")
		array := ArrayFilter(strings.Split(url, `/`))
		if len(array) == 1 {
			result = prefix + array[0] + "/" + api + "/"
		} else if len(array) == 2 {
			result = prefix + array[0] + "/" + array[1] + "/"
		}
	}
	return
}

// InMapKey
// 在 map key 中
func InMapKey(key string, array map[string]any) bool {
	for k := range array {
		if key == k {
			return true
		}
	}
	return false
}

// MapMerge
// map合并
func MapMerge(map1 map[any]any, map2 map[any]any) map[any]any {

	result := make(map[any]any)
	for i, v := range map1 {
		for j, w := range map2 {
			if i == j {
				result[i] = w
			} else {
				if _, ok := result[i]; !ok {
					result[i] = v
				}
				if _, ok := result[j]; !ok {
					result[j] = w
				}
			}
		}
	}

	return result
}

// MapMergeString
// map合并
func MapMergeString(map1 map[string]string, map2 map[string]string) map[string]string {

	result := make(map[string]string)
	for i, v := range map1 {
		for j, w := range map2 {
			if i == j {
				result[i] = w
			} else {
				if _, ok := result[i]; !ok {
					result[i] = v
				}
				if _, ok := result[j]; !ok {
					result[j] = w
				}
			}
		}
	}

	return result
}

// InMapValue
// 在 map value 中
func InMapValue(value any, array map[string]string) bool {
	for _, val := range array {
		if value == val {
			return true
		}
	}
	return false
}
