package utils

import (
	"reflect"
	"sync"
	
	"github.com/spf13/cast"
)

type AsyncClass[T any] struct {
	// 读写锁
	Mutex sync.RWMutex
	// 等待组
	Wait sync.WaitGroup
	// 数据
	Data T
}

// Async - 异步数据
func Async[T any]() *AsyncClass[T] {

	var data T
	typeof := reflect.TypeOf(data)
	if typeof == nil {
		// T 为接口类型（如 any）时，直接返回零值实例
		return &AsyncClass[T]{
			Mutex: sync.RWMutex{},
			Wait:  sync.WaitGroup{},
			Data:  data,
		}
	}

	switch typeof.Kind() {
	case reflect.Slice:
		data = reflect.MakeSlice(reflect.SliceOf(typeof.Elem()), 0, 0).Interface().(T)
	case reflect.Map:
		data = reflect.MakeMap(reflect.MapOf(typeof.Key(), typeof.Elem())).Interface().(T)
	default:
		data = reflect.Zero(typeof).Interface().(T)
	}

	return &AsyncClass[T]{
		Mutex: sync.RWMutex{},
		Wait:  sync.WaitGroup{},
		Data:  data,
	}
}

// Get - 获取数据
func (this *AsyncClass[T]) Get(key string) any {

	defer this.Mutex.RUnlock()
	this.Mutex.RLock()

	if Is.Empty(this.Data) {
		return nil
	}

	typeof := reflect.TypeOf(this.Data)
	if typeof.Kind() == reflect.Map && typeof.Key().Kind() == reflect.String {
		item := cast.ToStringMap(this.Data)
		return item[key]
	} else if typeof.Kind() == reflect.Slice {
		item := cast.ToSlice(this.Data)
		return item[cast.ToInt(key)]
	}

	return this.Data
}

// Set - 设置数据
func (this *AsyncClass[T]) Set(key string, val any) {

	defer this.Mutex.Unlock()
	this.Mutex.Lock()

	typeof := reflect.TypeOf(this.Data)
	if typeof.Kind() == reflect.Map && typeof.Key().Kind() == reflect.String {
		item := cast.ToStringMap(this.Data)
		item[key] = val
	} else if typeof.Kind() == reflect.Slice {
		index := cast.ToInt(key)
		// 通过反射直接写入原切片，避免 cast.ToSlice 拿到副本导致修改丢失
		item := reflect.ValueOf(this.Data)
		if index >= 0 && index < item.Len() {
			itemVal := reflect.ValueOf(val)
			// 类型不可赋值时静默跳过
			if itemVal.IsValid() && itemVal.Type().AssignableTo(item.Type().Elem()) {
				item.Index(index).Set(itemVal)
			}
		}
	} else {
		this.Data = val.(T)
	}
}

// Has - 判断是否存在
func (this *AsyncClass[T]) Has(key string) (ok bool) {

	defer this.Mutex.RUnlock()
	this.Mutex.RLock()

	if Is.Empty(this.Data) {
		return false
	}

	typeof := reflect.TypeOf(this.Data)
	if typeof.Kind() == reflect.Map && typeof.Key().Kind() == reflect.String {
		item := cast.ToStringMap(this.Data)
		_, ok = item[key]
	} else if typeof.Kind() == reflect.Slice {
		// 判断索引是否在切片长度范围内
		index := cast.ToInt(key)
		ok = index >= 0 && index < reflect.ValueOf(this.Data).Len()
	} else {
		ok = true
	}

	return ok
}

// Result - 获取所有数据
func (this *AsyncClass[T]) Result() T {
	defer this.Mutex.RUnlock()
	this.Mutex.RLock()
	return this.Data
}
