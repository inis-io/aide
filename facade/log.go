package facade

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/inis-io/aide/dto"
	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// LogInst - 日志配置控制器实例
var LogInst = &LogClass{}

// LogClass - 日志配置控制器
type LogClass struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash      string        `json:"hash"`
	// 当前日志配置（由调用方注入）
	Config    dto.LogConfig `json:"config"`
	// 是否已经注入过配置
	HasConfig bool          `json:"hasConfig"`
	// 日志级别
	Level     string        `json:"level"`
	// 日志内容
	Msg       string        `json:"msg"`
}

func init() { LogInst.Init() }

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func (this *LogClass) normConfig(config dto.LogConfig) dto.LogConfig {

	if config.Size    <= 0 { config.Size = 2 }
	if config.Age     <= 0 { config.Age  = 7 }
	if config.Backups <= 0 { config.Backups = 20 }

	// 仅当是空配置时启用默认值，避免覆盖调用方显式关闭日志。
	if !config.Enable && config.Hash == "" && config.Size == 2 && config.Age == 7 && config.Backups == 20 {
		config.Enable = true
	}

	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}

	return config
}

// defaultConfig - 获取默认日志配置
func (this *LogClass) defaultConfig() dto.LogConfig {
	return LogInst.normConfig(dto.LogConfig{Enable: true})
}

// useDefaultLog - 使用默认配置激活日志
func (this *LogClass) useDefaultLog() {
	conf := LogInst.defaultConfig()
	this.Config = conf
	this.Hash = conf.Hash
	this.HasConfig = false
	LogInst.setActiveLog(conf)
}

// setConfig - 注入日志配置
func (this *LogClass) setConfig(config dto.LogConfig) *LogClass {
	this.Config = LogInst.normConfig(config)
	this.HasConfig = true
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载日志
func (this *LogClass) ReloadIfChanged(config ...dto.LogConfig) {
	
	if len(config) > 0 { this.setConfig(config[0]) }
	if !this.HasConfig { return }

	// hash 变化，说明配置有更新
	if this.Hash != this.Config.Hash {
		this.Init()
	}
}

// Init - 初始化日志
func (this *LogClass) Init(config ...dto.LogConfig) {

	if len(config) > 0 {
		this.setConfig(config[0])
	}

	if !this.HasConfig {
		LogInst.useDefaultLog()
		return
	}

	this.Config = LogInst.normConfig(this.Config)
	this.Hash = this.Config.Hash
	LogInst.setActiveLog(this.Config)
}

// setActiveLog - 按配置切换当前活动日志实现
func (this *LogClass) setActiveLog(config dto.LogConfig) {
	conf := LogInst.normConfig(config)
	this.Config = conf

	LogInfo  = this.NewLevel("info", conf)
	LogWarn  = this.NewLevel("warn", conf)
	LogError = this.NewLevel("error", conf)
	LogDebug = this.NewLevel("debug", conf)

	Log = &log{
		Config:      conf,
		InfoLogger:  LogInfo,
		WarnLogger:  LogWarn,
		ErrorLogger: LogError,
		DebugLogger: LogDebug,
	}
}

// newWithConfig - 使用传入配置创建新的日志实例
func (this *LogClass) newWithConfig(config dto.LogConfig) LogAPI {
	conf := LogInst.normConfig(config)
	return &log{
		Config:      conf,
		InfoLogger:  this.NewLevel("info", conf),
		WarnLogger:  this.NewLevel("warn", conf),
		ErrorLogger: this.NewLevel("error", conf),
		DebugLogger: this.NewLevel("debug", conf),
	}
}

// ensureLog - 保证默认日志实例可用
func (this *LogClass) ensureLog() {
	if Log == nil {
		LogInst.useDefaultLog()
	}
}

// Write - 写入日志
func (this *LogClass) Write(data map[string]any, msg ...any) {
	this.ensureLog()

	level := strings.ToLower(strings.TrimSpace(this.Level))
	if len(msg) == 0 {
		msg = append(msg, level)
	}
	if level == "" {
		level = "info"
	}

	// 使用局部变量分发消息，避免并发写入共享字段 Msg 互相覆盖
	message := cast.ToString(msg[0])

	switch level {
	case "warn":
		Log.Warn(data, message)
	case "error":
		Log.Error(data, message)
	case "debug":
		Log.Debug(data, message)
	default:
		Log.Info(data, message)
	}
}

// NewLevel - 创建日志通道
func (this *LogClass) NewLevel(levelName string, config ...dto.LogConfig) *zap.Logger {

	conf := this.Config
	if len(config) > 0 {
		conf = LogInst.normConfig(config[0])
	} else {
		conf = LogInst.normConfig(conf)
	}

	path := fmt.Sprintf("runtime/logs/%s/%s.log", time.Now().Format("2006-01-02"), levelName)

	write := zapcore.AddSync(&lumberjack.Logger{
		Filename:   path,
		MaxAge:     conf.Age,
		MaxSize:    conf.Size,
		MaxBackups: conf.Backups,
	})

	encoder := func() zapcore.Encoder {

		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.TimeKey = "time"
		encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
		encoderConfig.EncodeCaller = zapcore.ShortCallerEncoder

		return zapcore.NewJSONEncoder(encoderConfig)
	}

	level  := new(zapcore.Level)
	if err := level.UnmarshalText([]byte(levelName)); err != nil {
		_ = level.UnmarshalText([]byte("info"))
	}
	core := zapcore.NewCore(encoder(), write, level)

	return zap.New(core)
}

// LogInfo - info日志通道
var LogInfo *zap.Logger

// LogWarn - warn日志通道
var LogWarn *zap.Logger

// LogError - error日志通道
var LogError *zap.Logger

// LogDebug - debug日志通道
var LogDebug *zap.Logger

// LogAPI - 统一日志能力接口
type LogAPI interface {
	Info(data map[string]any, msg ...any)
	Warn(data map[string]any, msg ...any)
	Error(data map[string]any, msg ...any)
	Debug(data map[string]any, msg ...any)
	NewLog(config dto.LogConfig) LogAPI
}

// log - 日志结构体
type log struct {
	Config      dto.LogConfig
	InfoLogger  *zap.Logger
	WarnLogger  *zap.Logger
	ErrorLogger *zap.Logger
	DebugLogger *zap.Logger
}

// Log - 日志
var Log LogAPI

// NewLog - 使用自定义配置创建新的日志实例
func (this *log) NewLog(config dto.LogConfig) LogAPI {
	return LogInst.newWithConfig(config)
}

func (this *log) Info(data map[string]any, msg ...any) {
	this.write(this.InfoLogger, "info", data, msg...)
}

func (this *log) Warn(data map[string]any, msg ...any) {
	this.write(this.WarnLogger, "warn", data, msg...)
}

func (this *log) Error(data map[string]any, msg ...any) {
	this.write(this.ErrorLogger, "error", data, msg...)
}

func (this *log) Debug(data map[string]any, msg ...any) {
	this.write(this.DebugLogger, "debug", data, msg...)
}

// write - 统一日志写入实现
func (this *log) write(logger *zap.Logger, level string, data map[string]any, msg ...any) {
	if !this.Config.Enable {
		return
	}

	if len(msg) == 0 {
		msg = append(msg, level)
	}

	content := cast.ToString(msg[0])
	if strings.TrimSpace(content) == "" {
		content = level
	}

	fields := this.mapFields(data)
	logger = this.ensureLogger(logger)

	switch level {
	case "warn":
		logger.Warn(content, fields...)
	case "error":
		logger.Error(content, fields...)
	case "debug":
		logger.Debug(content, fields...)
	default:
		logger.Info(content, fields...)
	}
}

func (this *log) mapFields(data map[string]any) []zap.Field {
	if len(data) == 0 {
		return nil
	}

	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	fields := make([]zap.Field, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, zap.Any(key, data[key]))
	}
	return fields
}

func (this *log) ensureLogger(logger *zap.Logger) *zap.Logger {
	if logger == nil {
		return zap.NewNop()
	}
	return logger
}