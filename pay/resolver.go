package pay

import (
	"context"
	"encoding/json"
)

// Definition - Resolver 返回的 Provider 实例定义
type Definition struct {
	// Name - Provider 注册名
	Name string `json:"name"`
	// Config - Provider 动态 JSON 配置
	Config json.RawMessage `json:"config"`
	// Sandbox - 是否使用沙箱环境
	Sandbox bool `json:"sandbox"`
	// Version - 网关实例配置修订版本
	Version string `json:"version"`
	// SchemaVersion - Provider 配置结构版本
	SchemaVersion uint16 `json:"schemaVersion"`
}

// Resolver - 按业务网关标识解析 Provider 定义
type Resolver interface {
	Resolve(ctx context.Context, key string) (Definition, error)
}

// ResolverFunc - 函数式 Resolver 适配器
type ResolverFunc func(context.Context, string) (Definition, error)

// Resolve - 调用函数解析 Provider 定义
func (this ResolverFunc) Resolve(ctx context.Context, key string) (Definition, error) {
	return this(ctx, key)
}
