package utils

import (
	"fmt"
	"reflect"
	"strconv"
	"time"
	
	"github.com/spf13/cast"
)

// Struct - 操作结构体
var Struct *StructClass

type StructClass struct {}

// Set - 动态给结构体赋值
func (this *StructClass) Set(obj any, key string, val any) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 判断字段的tag是否等于传入的key
		if tag == key {
			// 类型断言
			switch field.Type.Kind() {
			case reflect.String:
				value.Field(i).SetString(cast.ToString(val))
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				value.Field(i).SetInt(cast.ToInt64(val))
			case reflect.Bool:
				value.Field(i).SetBool(cast.ToBool(val))
			case reflect.Float32, reflect.Float64:
				value.Field(i).SetFloat(cast.ToFloat64(val))
			default:
				if val == nil {
					val = reflect.Zero(field.Type).Interface()
				} else {
					value.Field(i).Set(reflect.ValueOf(val))
				}
			}
			return
		}
	}
}

// Get - 动态获取结构体的值
func (this *StructClass) Get(obj any, key string) (result any) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 判断字段的tag是否等于传入的key
		if tag == key {
			// 获取字段的值
			return value.Field(i).Interface()
		}
	}
	return nil
}

// Del - 删除结构体的字段
func (this *StructClass) Del(obj any, key string) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 判断字段的tag是否等于传入的key
		if tag == key {
			// 获取字段的值
			value.Field(i).Set(reflect.Zero(field.Type))
		}
	}
}

// Has - 判断结构体是否存在某个字段
func (this *StructClass) Has(obj any, key string) (ok bool) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 判断字段的tag是否等于传入的key
		if tag == key {
			return true
		}
	}
	return false
}

// Keys - 获取结构体的字段
func (this *StructClass) Keys(obj any) (slice []string) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 定义一个切片
	keys := make([]string, 0)
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 判断字段的tag是否等于传入的key
		keys = append(keys, tag)
	}
	return keys
}

// Values - 获取结构体的值
func (this *StructClass) Values(obj any) (slice []any) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 定义一个切片
	keys := make([]any, 0)
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取字段的值
		keys = append(keys, value.Field(i).Interface())
	}
	return keys
}

// Len - 获取结构体的长度
func (this *StructClass) Len(obj any) (length int) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	return value.NumField()
}

// Map - 将结构体转换为map
func (this *StructClass) Map(obj any) (result map[string]any) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 获取结构体的类型
	typ := value.Type()
	// 定义一个map
	result = make(map[string]any)
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取结构体的字段
		field := typ.Field(i)
		// 获取字段的tag
		tag := field.Tag.Get("json")
		// 获取字段的值
		result[tag] = value.Field(i).Interface()
	}
	return result
}

// Slice - 将结构体转换为切片
func (this *StructClass) Slice(obj any) (slice []any) {
	// 获取结构体的值
	value := reflect.ValueOf(obj).Elem()
	// 定义一个切片
	s := make([]any, 0)
	// 遍历结构体的字段
	for i := 0; i < value.NumField(); i++ {
		// 获取字段的值
		s = append(s, value.Field(i).Interface())
	}
	return s
}

// ToStringMap - 将结构体转换为map[string]any
func (this *StructClass) ToStringMap(obj any) (result map[string]any) {
	return cast.ToStringMap(Json.Encode(obj))
}

// ToAsciiString - 将结构体转换为ASCII字符串
func (this *StructClass) ToAsciiString(obj any) (result string) {
	return Ascii.ToString(this.ToStringMap(obj), true)
}

// Fields - 获取结构体的字段名
func (this *StructClass) Fields(dest any) []string {

	// 获取结构体的反射值
	item := reflect.ValueOf(dest)
	// 确保传递的是一个指针
	if item.Kind() == reflect.Ptr {
		item = item.Elem()
	}
	// 确保反射值的类型是结构体
	if item.Kind()!= reflect.Struct { return nil }

	_type := item.Type()
	field := make([]string, item.NumField())

	for i := 0; i < item.NumField(); i++ {
		field[i] = _type.Field(i).Name
	}

	return field
}

// Defaults 可以处理任意结构体指针，为所有带有 default 标签的字段设置默认值
func (this *StructClass) Defaults(dest any) error {
	
	value := reflect.ValueOf(dest)
	
	// 检查是否为 nil
	if value.IsNil() { return fmt.Errorf("object is nil") }
	
	// 检查是否是指针
	if value.Kind() != reflect.Ptr { return fmt.Errorf("object must be a pointer") }
	
	// 获取指针指向的值
	value = value.Elem()
	
	// 检查是否是结构体
	if value.Kind() != reflect.Struct { return fmt.Errorf("object must be a pointer to struct") }
	
	// 获取结构体类型信息
	attr := value.Type()
	
	// 遍历所有字段
	for i := 0; i < value.NumField(); i++ {
		
		field     := value.Field(i)
		fieldType := attr.Field(i)
		
		// 如果字段是结构体，递归处理
		if field.Kind() == reflect.Struct && field.CanSet() {
			if field.CanAddr() {
				if err := this.Defaults(field.Addr().Interface()); err != nil {
					return fmt.Errorf("处理嵌套结构体 %s 时出错: %w", fieldType.Name, err)
				}
			}
			continue
		}
		
		// 如果字段是指向结构体的指针
		if field.Kind() == reflect.Ptr && !field.IsNil() && field.Elem().Kind() == reflect.Struct {
			if err := this.Defaults(field.Interface()); err != nil {
				return fmt.Errorf("处理指针字段 %s 时出错: %w", fieldType.Name, err)
			}
			continue
		}
		
		// 检查字段是否可设置
		if !field.CanSet() { continue }
		
		// 如果字段已经有值（非零值），则跳过
		if !field.IsZero() { continue }
		
		// 获取 default 标签值
		defaultValue := fieldType.Tag.Get("default")
		if defaultValue == "" { continue }
		
		// 根据字段类型设置默认值
		if err := this.setFieldWithDefault(field, defaultValue); err != nil {
			return fmt.Errorf("设置字段 %s 默认值 '%s' 时出错: %w", fieldType.Name, defaultValue, err)
		}
	}
	
	return nil
}

// setFieldWithDefault 根据字段类型设置默认值
func (this *StructClass) setFieldWithDefault(field reflect.Value, value string) error {
	
	// 处理指针类型
	if field.Kind() == reflect.Ptr {
		
		// 创建新的指针并赋值
		elemType := field.Type().Elem()
		newValue := reflect.New(elemType).Elem()
		
		if err := this.setFieldWithDefault(newValue, value); err != nil {
			return err
		}
		
		field.Set(reflect.New(elemType))
		field.Elem().Set(newValue)
		
		return nil
	}
	
	// 根据具体类型设置值
	switch field.Kind() {
	case reflect.String: field.SetString(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// 检查是否是time.Duration类型
		if field.Type().String() == "time.Duration" {
			duration, err := time.ParseDuration(value)
			if err != nil {
				return fmt.Errorf("解析时间间隔失败: %w", err)
			}
			field.SetInt(int64(duration))
		} else {
			intValue, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return fmt.Errorf("解析整数失败: %w", err)
			}
			field.SetInt(intValue)
		}
	
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintValue, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return fmt.Errorf("解析无符号整数失败: %w", err)
		}
		field.SetUint(uintValue)
	
	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("解析浮点数失败: %w", err)
		}
		field.SetFloat(floatValue)
	
	case reflect.Bool:
		boolValue, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("解析布尔值失败: %w", err)
		}
		field.SetBool(boolValue)
	
	case reflect.Slice:
		// 对于切片，支持用逗号分隔的字符串
		if field.Type().Elem().Kind() == reflect.String {
			// 简单处理，按逗号分割
			var items []string
			if value != "" {
				// 这里可以根据需要实现更复杂的分割逻辑
				items = []string{value}
			}
			sliceValue := reflect.MakeSlice(field.Type(), len(items), len(items))
			for i, item := range items {
				sliceValue.Index(i).SetString(item)
			}
			field.Set(sliceValue)
		} else {
			return fmt.Errorf("不支持设置 %s 类型的切片默认值", field.Type().Elem().Kind())
		}
	
	case reflect.Struct:
		// 对于结构体，尝试根据字段名设置（简单实现）
		return fmt.Errorf("结构体类型需要递归处理")
	
	default:
		return fmt.Errorf("不支持的字段类型: %s", field.Kind())
	}
	
	return nil
}