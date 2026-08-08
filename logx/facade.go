package logx

import (
	"sync"
)

// Inst - 日志服务控制器单例
var Inst = &Controller{}

// Controller - 日志服务控制器：管理配置注入与全局活动实例的热重载
type Controller struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash string `json:"hash"`
	// 当前日志配置（由调用方注入）
	Config Config `json:"config"`
	// 是否已经注入过配置
	HasConfig bool `json:"hasConfig"`
	// 读写锁，保护配置和Hash的并发访问
	Mutex sync.RWMutex
}

func init() { Inst.Init() }

// Log - 全局日志实例
/**
 * @example：
 * 	logx.Log.Info("用户登录", map[string]any{"uid": 1001})
 * 	logx.Log.With(map[string]any{"traceId": "T-10086"}).Error("支付失败")
 */
var Log *Logger

// useDefault - 使用默认配置激活日志服务
func (this *Controller) useDefault() {
	conf := normConfig(Config{})

	this.Mutex.Lock()
	this.Hash = conf.Hash
	this.HasConfig = false
	this.Mutex.Unlock()

	Inst.setActive(conf)
}

// setActive - 按配置切换当前活动日志实例
func (this *Controller) setActive(config Config) {

	conf := normConfig(config)
	next := New(conf)

	// 单次临界区原子提交，避免并发重载时配置与全局实例交错
	this.Mutex.Lock()
	prev := Log
	this.Config = conf
	Log = next
	this.Mutex.Unlock()

	// 替换后刷写并释放旧实例，避免切换瞬间丢日志与句柄泄漏
	if prev != nil {
		prev.Close()
	}
}

// setConfig - 注入日志配置
func (this *Controller) setConfig(config Config) *Controller {
	this.Mutex.Lock()
	this.Config = normConfig(config)
	this.HasConfig = true
	this.Mutex.Unlock()
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载日志服务
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

// Init 初始化日志服务
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
