package pay

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry - 实例级 Provider 工厂注册表
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

var defaultRegistry = NewRegistry()

// NewRegistry - 创建相互隔离的注册表
func NewRegistry() *Registry { return &Registry{factories: make(map[string]Factory)} }

// DefaultRegistry - 返回进程级便捷注册表；官方 Provider 不会自动写入
func DefaultRegistry() *Registry { return defaultRegistry }

// Register - 注册工厂并拒绝同名覆盖
func (this *Registry) Register(name string, factory Factory) error {
	name = normalizeProviderName(name)
	if name == "" || factory == nil {
		return fmt.Errorf("%w：名称为空或 Factory 为 nil", ErrInvalidProvider)
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if _, ok := this.factories[name]; ok {
		return fmt.Errorf("%w：%s", ErrDuplicateProvider, name)
	}
	this.factories[name] = factory
	return nil
}

// MustRegister - 注册工厂，失败时 panic
func (this *Registry) MustRegister(name string, factory Factory) {
	if err := this.Register(name, factory); err != nil {
		panic(err)
	}
}

// Replace - 显式替换已注册工厂
func (this *Registry) Replace(name string, factory Factory) error {
	name = normalizeProviderName(name)
	if name == "" || factory == nil {
		return fmt.Errorf("%w：名称为空或 Factory 为 nil", ErrInvalidProvider)
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if _, ok := this.factories[name]; !ok {
		return fmt.Errorf("%w：%s", ErrProviderNotFound, name)
	}
	this.factories[name] = factory
	return nil
}

// Names - 返回排序后的 Provider 名称快照
func (this *Registry) Names() []string {
	this.mu.RLock()
	defer this.mu.RUnlock()
	names := make([]string, 0, len(this.factories))
	for name := range this.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// New - 用强类型配置构造独立 Driver
func (this *Registry) New(ctx context.Context, name string, config any, options ...Option) (*Driver, error) {
	if config == nil {
		return nil, fmt.Errorf("%w：配置不能为空", ErrInvalidConfig)
	}
	return this.open(ctx, name, ConfigInput{Value: config}, options...)
}

// OpenRaw - 用动态 JSON 配置构造独立 Driver
func (this *Registry) OpenRaw(ctx context.Context, name string, raw json.RawMessage, options ...Option) (*Driver, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("%w：动态配置不是有效 JSON", ErrInvalidConfig)
	}
	return this.open(ctx, name, ConfigInput{Raw: append(json.RawMessage(nil), raw...)}, options...)
}

func (this *Registry) open(ctx context.Context, name string, input ConfigInput, options ...Option) (*Driver, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	name = normalizeProviderName(name)
	this.mu.RLock()
	factory := this.factories[name]
	this.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("%w：%s", ErrProviderNotFound, name)
	}
	settings := normalizeOpenOptions(options)
	provider, err := factory(ctx, input, settings)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, fmt.Errorf("%w：Factory 返回 nil", ErrInvalidProvider)
	}
	if normalizeProviderName(provider.Name()) != name {
		_ = provider.Close()
		return nil, fmt.Errorf("%w：注册名 %s 与 Provider 名 %s 不一致", ErrInvalidProvider, name, provider.Name())
	}
	capabilities, err := validateProvider(provider)
	if err != nil {
		_ = provider.Close()
		return nil, err
	}
	return &Driver{provider: provider, capabilities: capabilities, options: settings}, nil
}

func normalizeProviderName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func validateProvider(provider Provider) (map[Capability]struct{}, error) {
	declared := make(map[Capability]struct{})
	for _, capability := range provider.Capabilities() {
		if _, exists := declared[capability]; exists {
			return nil, fmt.Errorf("%w：能力 %s 重复", ErrInvalidProvider, capability)
		}
		declared[capability] = struct{}{}
	}
	checks := []struct {
		capability  Capability
		implemented bool
	}{
		{CapTradeCreate, implements[TradeCreator](provider)}, {CapTradeQuery, implements[TradeQuerier](provider)},
		{CapTradeCapture, implements[TradeCapturer](provider)}, {CapTradeClose, implements[TradeCloser](provider)},
		{CapRefund, implements[Refunder](provider)}, {CapRefundQuery, implements[RefundQuerier](provider)},
		{CapTransfer, implements[Transferer](provider)}, {CapTransferQuery, implements[TransferQuerier](provider)},
		{CapBill, implements[Biller](provider)},
	}
	for _, check := range checks {
		_, declaredCapability := declared[check.capability]
		if declaredCapability != check.implemented {
			return nil, fmt.Errorf("%w：能力 %s 的声明与接口实现不一致", ErrInvalidProvider, check.capability)
		}
	}
	_, parser := provider.(NotifyParser)
	notifyCount := 0
	for _, capability := range []Capability{CapNotifyTrade, CapNotifyRefund, CapNotifyTransfer, CapWebhook} {
		if _, ok := declared[capability]; ok {
			notifyCount++
		}
	}
	if parser != (notifyCount > 0) {
		return nil, fmt.Errorf("%w：通知能力声明与接口实现不一致", ErrInvalidProvider)
	}
	for capability := range declared {
		if !knownCapability(capability) {
			return nil, fmt.Errorf("%w：未知能力 %s", ErrInvalidProvider, capability)
		}
	}
	return declared, nil
}

func implements[T any](provider Provider) bool { _, ok := any(provider).(T); return ok }

func knownCapability(capability Capability) bool {
	switch capability {
	case CapTradeCreate, CapTradeQuery, CapTradeCapture, CapTradeClose, CapRefund, CapRefundQuery,
		CapTransfer, CapTransferQuery, CapNotifyTrade, CapNotifyRefund, CapNotifyTransfer, CapWebhook, CapBill:
		return true
	default:
		return false
	}
}
