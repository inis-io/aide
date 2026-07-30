package utils

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
	
	"github.com/spf13/cast"
)

type FileCacheClientItem struct {
	// 过期时间
	expire int64
	// 文件名
	name string
	// 开始时间
	start time.Time
	// 过期时间
	end time.Time
}

type FileCacheClient struct {
	dir    string     // 缓存目录
	mutex  sync.Mutex // 互斥锁，用于保证并发安全
	prefix string     // 缓存文件名前缀
	expire int64      // 默认缓存过期时间
	items  map[string]*FileCacheClientItem
}

// NewFileCache - 新建文件缓存
/**
 * @param dir 缓存目录
 * @param prefix 缓存名前缀
 * @return *FileCacheClient, error
 * @example：
 * 1. cache, err := facade.NewFileCacheClient("runtime/cache")
 * 2. cache, err := facade.NewFileCacheClient("runtime/cache", "cache_")
 */
func NewFileCache(dir any, expire any, prefix ...any) (*FileCacheClient, error) {

	err := os.MkdirAll(cast.ToString(dir), 0755)
	if err != nil {
		return nil, fmt.Errorf("create cache dir error: %v", err)
	}

	if len(prefix) == 0 {
		prefix = append(prefix, "cache_")
	}

	client := &FileCacheClient{
		dir:    cast.ToString(dir),
		prefix: cast.ToString(prefix[0]),
		items:  make(map[string]*FileCacheClientItem),
		expire: cast.ToInt64(expire),
	}

	// 定时器 - 每隔一段时间清理过期的缓存文件
	go client.timer()

	return client, nil
}

// Get 从缓存中获取key对应的数据
func (this *FileCacheClient) Get(key any) (result []byte) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	item, ok := this.items[cast.ToString(key)]
	if !ok {
		return nil
	}

	if time.Now().After(item.end) {
		// 文件已过期，删除文件并返回false
		err := os.Remove(item.name)
		if err != nil {
			return nil
		}
		delete(this.items, cast.ToString(key))
		return nil
	}

	data, err := os.ReadFile(item.name)
	if err != nil {
		return nil
	}

	return data
}

// Has 检查缓存中是否存在key对应的数据
func (this *FileCacheClient) Has(key any) (exist bool) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	item, ok := this.items[cast.ToString(key)]
	if !ok {
		return false
	}

	if time.Now().After(item.end) {
		// 文件已过期，删除文件并返回false
		err := os.Remove(item.name)
		if err != nil {
			return false
		}
		delete(this.items, cast.ToString(key))
		return false
	}

	return true
}

// Set 将key-value数据加入到缓存中
func (this *FileCacheClient) Set(key any, value []byte, expire ...any) (ok bool) {

	exp := this.expire

	if len(expire) > 0 {
		if !Is.Empty(expire[0]) {
			// 判断 expire[0] 是否为Duration类型
			if reflect.TypeOf(expire[0]).String() == "time.Duration" {
				// 转换为int64
				exp = cast.ToInt64(cast.ToDuration(expire[0]).Seconds())
			} else {
				exp = cast.ToInt64(expire[0])
			}
		}
	}

	err := this.SetE(key, value, exp)

	return Ternary(err != nil, false, true)
}

// Del 从缓存中删除key对应的数据
func (this *FileCacheClient) Del(key any) (ok bool) {
	err := this.DelE(key)
	return Ternary(err != nil, false, true)
}

// DelPrefix 从缓存中删除指定前缀的数据
func (this *FileCacheClient) DelPrefix(prefix ...any) (ok bool) {
	err := this.DelPrefixE(prefix...)
	return Ternary(err != nil, false, true)
}

// DelTags 从缓存中删除指定标签的数据
func (this *FileCacheClient) DelTags(tags ...any) (ok bool) {
	err := this.DelTagsE(tags...)
	return Ternary(err != nil, false, true)
}

// Clear 清空缓存
func (this *FileCacheClient) Clear() (ok bool) {
	err := this.ClearE()
	return Ternary(err != nil, false, true)
}

// SetE 将key-value数据加入到缓存中
func (this *FileCacheClient) SetE(key any, value []byte, expire int64) (err error) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	// 检查缓存目录是否存在
	if exist := NewFile().Exist(this.dir); !exist {
		err = os.MkdirAll(this.dir, 0755)
		if err != nil {
			return fmt.Errorf("create cache dir error: %v", err)
		}
	}

	name := this.name(cast.ToString(key))
	file, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("create cache file %s error: %v", name, err)
	}

	_, err = file.Write(value)
	if err != nil {
		return fmt.Errorf("write to cache file %s error: %v", name, err)
	}

	err = file.Close()
	if err != nil {
		return err
	}

	// end 过期时间，expire = 0 表示永不过期
	var end time.Time
	if expire == 0 {
		end = time.Now().AddDate(100, 0, 0)
	} else {
		end = time.Now().Add(time.Duration(expire) * time.Second)
	}

	this.items[cast.ToString(key)] = &FileCacheClientItem{
		expire: expire,
		name:   name,
		start:  time.Now(),
		end:    end,
	}

	return nil
}

// DelE 从缓存中删除key对应的数据
func (this *FileCacheClient) DelE(key any) (err error) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	cacheItem, ok := this.items[cast.ToString(key)]
	if !ok {
		return nil
	}

	err = os.Remove(cacheItem.name)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete cache file %s error: %v", cacheItem.name, err)
	}

	delete(this.items, cast.ToString(key))

	return nil
}

// GetKeys 获取所有缓存的key
func (this *FileCacheClient) GetKeys() (slice []string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()

	keys := make([]string, 0, len(this.items))
	for key := range this.items {
		keys = append(keys, key)
	}

	return keys
}

// ClearE 清空缓存
func (this *FileCacheClient) ClearE() (err error) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	for _, cacheItem := range this.items {
		err := os.Remove(cacheItem.name)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete cache file %s error: %v", cacheItem.name, err)
		}
	}

	this.items = make(map[string]*FileCacheClientItem)

	// 清空缓存目录
	err = os.RemoveAll(this.dir)
	if err != nil {
		return fmt.Errorf("remove cache dir %s error: %v", this.dir, err)
	}

	return nil
}

// DelPrefixE 删除指定前缀的缓存
func (this *FileCacheClient) DelPrefixE(prefix ...any) (err error) {

	var keys []string
	var prefixes []string

	if len(prefix) == 0 {
		return nil
	}

	for _, value := range prefix {
		// 判断是否为切片
		if reflect.ValueOf(value).Kind() == reflect.Slice {
			for _, val := range cast.ToSlice(value) {
				prefixes = append(prefixes, cast.ToString(val))
			}
		} else {
			prefixes = append(prefixes, cast.ToString(value))
		}
	}

	this.mutex.Lock()
	defer this.mutex.Unlock()

	for key, cacheItem := range this.items {
		for _, value := range prefixes {
			if strings.HasPrefix(key, value) {
				err := os.Remove(cacheItem.name)
				if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("delete cache file %s error: %v", cacheItem.name, err)
				}
				keys = append(keys, key)
			}
		}
	}

	for _, key := range keys {
		delete(this.items, key)
	}

	return nil
}

// DelTagsE 删除指定标签的缓存
func (this *FileCacheClient) DelTagsE(tag ...any) (err error) {

	var keys []string
	var tags []string

	if len(tag) == 0 {
		return nil
	}

	for _, value := range tag {

		var item string

		// 判断是否为切片
		if reflect.ValueOf(value).Kind() == reflect.Slice {
			var tmp []string
			for _, val := range cast.ToSlice(value) {
				tmp = append(tmp, cast.ToString(val))
			}
			item = strings.Join(tmp, "*")
		} else {
			item = cast.ToString(value)
		}

		tags = append(tags, fmt.Sprintf("*%s*", item))
	}

	// 获取所有缓存名称
	for key := range this.items {
		keys = append(keys, key)
	}

	// 模糊匹配
	keys = this.fuzzyMatch(keys, tags)

	this.mutex.Lock()
	defer this.mutex.Unlock()

	for _, key := range keys {
		item, ok := this.items[key]
		if !ok {
			continue
		}
		err := os.Remove(item.name)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("delete cache file %s error: %v", item.name, err)
		}
		delete(this.items, key)
	}

	return nil
}

// GetInfo 获取缓存信息
func (this *FileCacheClient) GetInfo(key any) (info map[string]any) {

	this.mutex.Lock()
	defer this.mutex.Unlock()

	item, ok := this.items[cast.ToString(key)]
	if !ok {
		return nil
	}

	// 剩余剩余多少秒过期
	var expire int64
	if item.end.IsZero() {
		expire = 0
	} else {
		expire = int64(item.end.Sub(time.Now()).Seconds())
	}

	var value []byte

	value, _ = os.ReadFile(item.name)

	return map[string]any{
		"name":   item.name,
		"expire": expire,
		"value":  string(value),
	}
}

// 模糊匹配
// keys := []string{"cache_test1","cache_test2","cache_inis_test1","cache_inis_test2","cache_admin_name","cache_unti_name"}
// tags := []string{"*inis*test*", "*unti*", "*admin*"}
// return []string{"cache_inis_test1","cache_inis_test2","cache_admin_name","cache_unti_name"}
func (this *FileCacheClient) fuzzyMatch(keys []string, tags []string) (result []string) {
	for _, item := range keys {
		for _, tag := range tags {
			if matched, _ := filepath.Match(tag, item); matched {
				result = append(result, item)
				break
			}
		}
	}
	return result
}

// name 返回缓存文件名
func (this *FileCacheClient) name(key any) (result string) {

	// 过滤掉 windows、linux和mac下的非法字符
	key = regexp.MustCompile(`[\\/:*?"<>|]`).ReplaceAllString(cast.ToString(fmt.Sprintf("%s%s", this.prefix, key)), "")

	return path.Join(this.dir, cast.ToString(key))
}

// timer 定时器 - 每隔一段时间清理过期的缓存文件
func (this *FileCacheClient) timer() {
	for {

		time.Sleep(1 * time.Second)

		this.mutex.Lock()
		for key, item := range this.items {
			if time.Now().After(item.end) {
				// 文件已过期，删除文件并从缓存中删除
				err := os.Remove(item.name)
				if err != nil {
					continue
				}
				delete(this.items, key)
			}
		}
		this.mutex.Unlock()
	}
}
