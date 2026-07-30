package utils

import (
	"errors"
	"os"

	"github.com/spf13/cast"
	"github.com/spf13/viper"
)

type ViperClass struct {
	// 配置文件路径
	Path string
	// 配置文件类型
	Mode string
	// 文件名
	Name string
	// 文件内容
	Content string
}

type ViperResponse struct {
	// 配置文件内容
	Result map[string]any
	// 错误信息
	Error error
	// viper实例
	Viper *viper.Viper
}

func Viper(model ...ViperClass) *ViperClass {

	// 无参时返回空实例，避免后续链式调用空指针崩溃
	item := &ViperClass{}

	if len(model) > 0 {
		item = &model[0]
	}

	return item
}

func (this *ViperClass) SetPath(path string) *ViperClass {
	this.Path = path
	return this
}

func (this *ViperClass) SetMode(mode string) *ViperClass {
	this.Mode = mode
	return this
}

func (this *ViperClass) SetName(name string) *ViperClass {
	this.Name = name
	return this
}

func (this *ViperClass) Read() (result ViperResponse) {

	item := viper.New()

	if !Is.Empty(this.Path) {
		item.AddConfigPath(this.Path)
	}

	if !Is.Empty(this.Mode) {
		item.SetConfigType(this.Mode)
	}

	if !Is.Empty(this.Name) {
		item.SetConfigName(this.Name)
	}
	
	// 如果 this.Path 不存在，则创建目录
	if _, err := os.Stat(this.Path); os.IsNotExist(err) {
		_ = os.MkdirAll(this.Path, 0755)
	}

	result.Viper = item
	result.Error = item.ReadInConfig()
	result.Result = cast.ToStringMap(item.AllSettings())

	if result.Error != nil {
		// 仅当配置文件不存在时，才创建并写入默认配置；解析失败等其他错误原样返回，绝不触碰文件
		var notFound viper.ConfigFileNotFoundError
		if (errors.As(result.Error, &notFound) || os.IsNotExist(result.Error)) && !Is.Empty(this.Content) {

			path := this.Path + "/" + this.Name + "." + this.Mode

			// 写入默认配置文件
			if err := os.WriteFile(path, []byte(this.Content), 0755); err != nil {
				result.Error = err
			} else {
				result.Error = nil
			}
		}
	}

	return
}

func (this *ViperResponse) Get(key string, def ...any) (result any) {

	var item any

	if len(def) > 0 {
		item = def[0]
	}

	if this.Error != nil || this.Result == nil {
		return item
	}

	result = this.Viper.Get(key)
	result = Ternary(!Is.Empty(result), result, item)

	return
}

func (this *ViperResponse) Set(key string, value any) (result ViperResponse) {

	if this.Error != nil {
		return
	}

	if this.Result == nil {
		return
	}

	file, err := os.OpenFile(this.Viper.ConfigFileUsed(), os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		result.Error = err
		return
	}

	this.Result[key] = value
	this.Viper.Set(key, value)
	result = *this
	result.Error = this.Viper.WriteConfigAs(file.Name())

	// 释放资源（Close 错误不覆盖此前的真实错误）
	if err := file.Close(); err != nil && result.Error == nil {
		result.Error = err
	}

	return
}
