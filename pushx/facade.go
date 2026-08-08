package pushx

import (
	"errors"
	"sync"

	"github.com/inis-io/aide/utils"
)

// Inst - 推送服务控制器单例
var Inst = &Controller{}

// Controller - 推送服务控制器：管理配置注入与全局活动实例的热重载
type Controller struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash string `json:"hash"`
	// 当前推送配置（由调用方注入）
	Config Config `json:"config"`
	// 是否已经注入过配置
	HasConfig bool `json:"hasConfig"`
	// 读写锁，保护配置和Hash的并发访问
	Mutex sync.RWMutex
}

func init() { Inst.Init() }

// Push  - 全局链式推送实例（按目标类型自动路由邮件/短信驱动）
/**
 * @example：
 * 	resp, err := pushx.Push.Target("13800000000").Send()
 */
var Push Driver

// Email - 当前邮件驱动
var Email Sender

// SMS   - 当前短信驱动
var SMS Sender

// useDefault - 使用默认配置激活推送服务
func (this *Controller) useDefault() {
	conf := normConfig(Config{})

	this.Mutex.Lock()
	this.Hash = conf.Hash
	this.HasConfig = false
	this.Mutex.Unlock()

	Inst.setActive(conf)
}

// setActive - 按配置切换当前活动推送实现
func (this *Controller) setActive(config Config) {

	conf := normConfig(config)

	// 驱动构建可能耗时（云端凭证初始化），在临界区外完成
	email, err := open(conf.Engine.Email, conf)
	if err != nil {
		email = senderError{name: conf.Engine.Email, err: err}
	}

	sms, err := open(conf.Engine.SMS, conf)
	if err != nil {
		sms = senderError{name: conf.Engine.SMS, err: err}
	}

	// 单次临界区原子提交，避免并发重载时配置与全局实例交错
	this.Mutex.Lock()
	this.Config = conf
	Email = email
	SMS = sms
	Push = NewDriver(Router{Email: email, SMS: sms})
	this.Mutex.Unlock()
}

// setConfig - 注入推送配置
func (this *Controller) setConfig(config Config) *Controller {
	this.Mutex.Lock()
	this.Config = normConfig(config)
	this.HasConfig = true
	this.Mutex.Unlock()
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载推送服务
func (this *Controller) ReloadIfChanged(config ...Config) {

	if len(config) > 0 {
		this.setConfig(config[0])
	}

	this.Mutex.RLock()
	hasConfig := this.HasConfig
	changed := this.Hash != this.Config.Hash
	this.Mutex.RUnlock()

	if !hasConfig {
		return
	}

	// hash 变化，说明配置有更新
	if changed {
		this.Init()
	}
}

// Init 初始化推送服务
func (this *Controller) Init(config ...Config) {

	if len(config) > 0 {
		this.setConfig(config[0])
	}

	this.Mutex.RLock()
	hasConfig := this.HasConfig
	this.Mutex.RUnlock()

	if !hasConfig {
		Inst.useDefault()
		return
	}

	this.Mutex.Lock()
	this.Config = normConfig(this.Config)
	this.Hash = this.Config.Hash
	this.Mutex.Unlock()

	this.Mutex.RLock()
	conf := this.Config
	this.Mutex.RUnlock()

	Inst.setActive(conf)
}

// ================================== 智能路由 - 开始 ==================================

// Router - 智能路由驱动：按目标类型（邮箱/手机号）自动分发到邮件或短信驱动
type Router struct {
	// 邮件驱动
	Email Sender
	// 短信驱动
	SMS Sender
}

// Send - 按目标类型路由发送
func (this Router) Send(message Message) (*Response, error) {

	mode, err := utils.Identify.EmailOrPhone(message.Target)
	// 如果不是邮箱或手机号
	if err != nil {
		return nil, err
	}

	if mode == "email" {
		if this.Email == nil {
			return nil, errors.New("pushx: 邮件驱动未初始化")
		}
		return this.Email.Send(message)
	}

	if this.SMS == nil {
		return nil, errors.New("pushx: 短信驱动未初始化")
	}
	return this.SMS.Send(message)
}

// ================================== 智能路由 - 结束 ==================================
