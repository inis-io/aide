// Package pushx - 消息推送包：以接口模式封装短信、邮件等验证码推送能力
//
// 设计要点：
//   - Sender 是唯一扩展点：服务商只需实现 Send(Message) 即可接入
//   - 内置驱动在注册表变量初始化时登记（不依赖文件 init 顺序）；外部驱动在自己包内
//     通过 init() + Register 注册，同名注册会覆盖先注册者（可借此替换内置实现）
//   - 扩展驱动的自定义配置通过 Config.Options 传入（key 为驱动名）
//   - Driver 在 Sender 之上提供链式调用（值语义，天然隔离上下文，无需 clone）
//   - Router 按目标类型（邮箱/手机号）自动路由到对应驱动，各驱动只发自己通道
//   - Inst + Push 提供与 facade 层一致的全局单例入口
package pushx

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 接口与注册表 - 开始 ==================================

// Sender - 推送驱动接口：短信、邮件及未来接入的服务商统一实现该接口
type Sender interface {
	// Send - 按消息体发送（实现方需校验目标合法性，并调用 normMessage 补齐默认值与验证码）
	Send(message Message) (*Response, error)
}

// Factory - 驱动工厂：按配置构建驱动实例（传入的 Config 已归一化）
type Factory func(config Config) (Sender, error)

// registry - 驱动注册表（读写锁保护并发注册与查找）
// 内置驱动在变量初始化时登记，保证包初始化期间即可用；外部驱动通过 Register 注册
var registry = struct {
	sync.RWMutex
	items map[string]Factory
}{items: map[string]Factory{
	"email":   newEmailSender,
	"aliyun":  newAliYunSender,
	"tencent": newTencentSender,
	"smsbao":  newSmsbaoSender,
}}

// Register - 注册推送驱动
/**
 * @param name    string  - 驱动名称（不区分大小写，同名后注册覆盖先注册）
 * @param factory Factory - 驱动工厂
 * @example：
 * 	func init() { pushx.Register("aliyun", newAliYunSender) }
 */
func Register(name string, factory Factory) {
	name = strings.ToLower(strings.TrimSpace(name))
	if utils.Is.Empty(name) {
		panic("pushx: 驱动名称不能为空")
	}
	if factory == nil {
		panic("pushx: 驱动[" + name + "]工厂不能为空")
	}
	registry.Lock()
	registry.items[name] = factory
	registry.Unlock()
}

// registered - 驱动是否已注册
func registered(name string) bool {
	registry.RLock()
	defer registry.RUnlock()
	_, ok := registry.items[name]
	return ok
}

// Names - 已注册的驱动名称列表（有序）
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()
	names := make([]string, 0, len(registry.items))
	for name := range registry.items {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// open - 按名称构建驱动实例（内部使用，不补齐默认值）
func open(name string, config Config) (Sender, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	registry.RLock()
	factory, ok := registry.items[name]
	registry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("pushx: 未注册的驱动[%s]（可用: %s）", name, strings.Join(Names(), ", "))
	}
	return factory(config)
}

// New - 按驱动名称与配置创建链式推送实例
/**
 * @param name   string - 驱动名称（email / aliyun / tencent / smsbao / 自定义注册名）
 * @param config Config - 推送配置
 * @example：
 * 	driver, err := pushx.New("aliyun", pushx.Config{...})
 * 	resp, err := driver.Target("13800000000").Send()
 */
func New(name string, config Config) (Driver, error) {
	sender, err := open(name, normConfig(config))
	if err != nil {
		return Driver{}, err
	}
	return NewDriver(sender), nil
}

// ================================== 接口与注册表 - 结束 ==================================

// ================================== 链式推送实例 - 开始 ==================================

// Driver - 链式推送实例：在 Sender 之上提供链式上下文（值语义，每次调用返回副本）
type Driver struct {
	// 底层推送驱动
	sender Sender
	// 链式上下文消息体
	message Message
}

// NewDriver - 用 Sender 包装出链式推送实例
func NewDriver(sender Sender, message ...Message) Driver {
	driver := Driver{sender: sender}
	if len(message) > 0 {
		driver.message = message[0]
	}
	return driver
}

// Sender - 取出底层推送驱动
func (this Driver) Sender() Sender {
	return this.sender
}

// Target - 目标手机号或邮箱
func (this Driver) Target(target string) Driver {
	this.message.Target = target
	return this
}

// Code - 自定义验证码
func (this Driver) Code(code string) Driver {
	this.message.Code = code
	return this
}

// Len - 验证码长度
func (this Driver) Len(length int) Driver {
	this.message.Length = length
	return this
}

// Expired - 验证码有效期（分钟）
func (this Driver) Expired(minutes int64) Driver {
	this.message.Expired = minutes
	return this
}

// Subject - 主题（标题）
func (this Driver) Subject(subject string) Driver {
	this.message.Subject = subject
	return this
}

// Template - 自定义发送模板（仅本地渲染的驱动生效：email / smsbao；阿里云、腾讯云模板在云端控制台维护，此方法对其无效）
func (this Driver) Template(template string) Driver {
	this.message.Template = template
	return this
}

// Param - 设置自定义模板变量参数（多次调用同名键后者覆盖前者）
/**
 * 本地渲染驱动（email / smsbao）在模板中以 ${键名} 使用；
 * 阿里云按键名合并进模板变量 JSON；腾讯云按数字键名（"1"、"2"...）升序组装为参数数组
 * @example：
 * 	pushx.Push.Target("13800000000").Param("1", "123456").Param("2", "5").Send()
 */
func (this Driver) Param(key string, value any) Driver {
	if this.message.Params == nil {
		this.message.Params = make(map[string]any)
	}
	this.message.Params[key] = value
	return this
}

// SetMessage - 设置消息体（非零字段覆盖当前值）
func (this Driver) SetMessage(message Message) Driver {
	this.message = mergeMessage(this.message, message)
	return this
}

// Send - 发送验证码
/**
 * @param target ...any - 目标手机号或邮箱（可选，优先级最高）
 * @example：
 * 	resp, err := pushx.Push.Send("13800000000")
 */
func (this Driver) Send(target ...any) (*Response, error) {
	if this.sender == nil {
		return nil, errors.New("pushx: 推送驱动未初始化")
	}
	// 这里的 target 优先级最高
	if len(target) > 0 {
		this.message.Target = cast.ToString(target[0])
	}
	return this.sender.Send(this.message)
}

// ================================== 链式推送实例 - 结束 ==================================

// senderError - 初始化失败的驱动占位：在 Send 时返回初始化错误
type senderError struct {
	// 驱动名称
	name string
	// 初始化错误
	err error
}

// Send - 返回驱动初始化错误
func (this senderError) Send(Message) (*Response, error) {
	return nil, fmt.Errorf("pushx: 驱动[%s]初始化失败 - %w", this.name, this.err)
}
