package taskx

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/inis-io/aide/utils"
)

// Config - 异步队列配置
type Config struct {
	// Hash - 配置指纹（可选）
	Hash string `json:"hash"`
	// Engine - 队列驱动名
	Engine string `json:"engine"`
	// Concurrency - worker 并发数
	Concurrency int `json:"concurrency"`
	// Queues - 队列与权重
	Queues map[string]int `json:"queues"`
	// PollInterval - 搬运与空转轮询周期
	PollInterval time.Duration `json:"pollInterval"`
	// LeaseTTL - 任务租约时长
	LeaseTTL time.Duration `json:"leaseTtl"`
	// ShutdownTimeout - 优雅退出等待上限
	ShutdownTimeout time.Duration `json:"shutdownTimeout"`
	// JanitorInterval - 清理周期
	JanitorInterval time.Duration `json:"janitorInterval"`
	// RetryDelay - 重试退避函数
	RetryDelay func(attempts int, err error) time.Duration `json:"-"`
	// ErrorHandler - 任务失败钩子
	ErrorHandler func(ctx context.Context, msg *Message, err error) `json:"-"`
	// Logger - 日志接口
	Logger Logger `json:"-"`
	// File - file 驱动配置
	File FileConfig `json:"file"`
	// Redis - redis 驱动配置
	Redis RedisConfig `json:"redis"`
	// Options - 外部驱动自定义配置
	Options map[string]any `json:"options"`
}

// FileConfig - file 驱动配置
type FileConfig struct {
	// Root - 队列根目录
	Root string `json:"root"`
	// SyncWrites - 是否同步刷盘
	SyncWrites bool `json:"syncWrites"`
}

// RedisConfig - redis 驱动配置
type RedisConfig struct {
	// Addr - Redis 地址
	Addr string `json:"addr"`
	// Username - Redis 用户名
	Username string `json:"username"`
	// Password - Redis 密码
	Password string `json:"password"`
	// DB - Redis 逻辑库
	DB int `json:"db"`
	// Prefix - Redis key 前缀
	Prefix string `json:"prefix"`
	// PoolSize - 连接池大小
	PoolSize int `json:"poolSize"`
}

// Logger - taskx 窄日志接口
type Logger interface {
	// Debug - 输出调试日志
	Debug(msg string, fields ...map[string]any)
	// Info - 输出信息日志
	Info(msg string, fields ...map[string]any)
	// Warn - 输出警告日志
	Warn(msg string, fields ...map[string]any)
	// Error - 输出错误日志
	Error(msg string, fields ...map[string]any)
}

func normConfig(config Config) Config {
	config.Engine = strings.ToLower(strings.TrimSpace(config.Engine))
	if !registered(config.Engine) {
		config.Engine = "file"
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 10
	}
	if len(config.Queues) == 0 {
		config.Queues = map[string]int{"default": 1}
	} else {
		queues := make(map[string]int, len(config.Queues))
		for name, weight := range config.Queues {
			name = strings.TrimSpace(name)
			if name == "" || weight <= 0 {
				continue
			}
			queues[name] = weight
		}
		if len(queues) == 0 {
			queues["default"] = 1
		}
		config.Queues = queues
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.LeaseTTL <= 0 {
		config.LeaseTTL = 30 * time.Second
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 30 * time.Second
	}
	if config.JanitorInterval <= 0 {
		config.JanitorInterval = 5 * time.Minute
	}
	if config.RetryDelay == nil {
		config.RetryDelay = defaultRetryDelay
	}
	if strings.TrimSpace(config.File.Root) == "" {
		config.File.Root = "./runtime/queue"
	}
	if strings.TrimSpace(config.Redis.Addr) == "" {
		config.Redis.Addr = "localhost:6379"
	}
	if strings.TrimSpace(config.Redis.Prefix) == "" {
		config.Redis.Prefix = "AIDE:TASKX:"
	}
	if config.Redis.PoolSize <= 0 {
		config.Redis.PoolSize = 10
	}
	if strings.TrimSpace(config.Hash) == "" {
		config.Hash = configHash(config)
	}
	return config
}

func configHash(config Config) string {
	names := make([]string, 0, len(config.Queues))
	for name := range config.Queues {
		names = append(names, name)
	}
	sort.Strings(names)
	queues := make([]string, 0, len(names))
	for _, name := range names {
		queues = append(queues, fmt.Sprintf("%s=%d", name, config.Queues[name]))
	}
	value := fmt.Sprintf("%s|%d|%s|%d|%d|%d|%d|%+v|%+v|%v|%x|%x|%s",
		config.Engine, config.Concurrency, strings.Join(queues, ","), config.PollInterval,
		config.LeaseTTL, config.ShutdownTimeout, config.JanitorInterval, config.File, config.Redis,
		config.Options, functionPointer(config.RetryDelay), functionPointer(config.ErrorHandler), loggerIdentity(config.Logger))
	return utils.Hash.Sum32(value)
}

func functionPointer(function any) uintptr {
	if function == nil {
		return 0
	}
	return reflect.ValueOf(function).Pointer()
}

func loggerIdentity(logger Logger) string {
	if logger == nil {
		return ""
	}
	value := reflect.ValueOf(logger)
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice {
		return fmt.Sprintf("%T:%x", logger, value.Pointer())
	}
	return fmt.Sprintf("%T:%v", logger, logger)
}
