// Package logx - 日志包：基于 zap + lumberjack 的结构化日志，按级别分文件滚动切割
//
// 设计要点：
//   - 单 Logger + Tee Core：四个级别文件各自精确收录对应级别（error 含 panic/fatal），
//     日志不重复写盘；Level 配置控制最低写入级别
//   - 落盘懒加载：lumberjack 首次写入才创建目录与文件，import 无副作用
//   - Disable 时构建 Nop Logger，写操作零开销，无需调用方判断
//   - Inst + Log 提供与 storagex 等子包一致的全局单例入口；New 创建独立实例
package logx

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/inis-io/aide/utils"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// ================================== 配置 - 开始 ==================================

// Config - 日志配置（由调用方传入，零值即可用：默认启用、写 runtime/logs）
type Config struct {
	// Hash - 计算配置是否发生变更（可选，不传会自动计算）
	Hash string `json:"hash"`
	// Disable - 关闭日志（零值 false 表示启用，显式 true 关闭）
	Disable bool `json:"disable"`
	// Root - 日志根目录（落盘结构：根目录/日期/级别.log）
	Root string `json:"root" comment:"日志根目录" default:"runtime/logs"`
	// Level - 最低写入级别（debug / info / warn / error，非法值按 debug）
	Level string `json:"level" comment:"最低写入级别" default:"debug"`
	// Size - 单个日志文件大小（MB）
	Size int `json:"size" comment:"单个日志文件大小（MB）" default:"10"`
	// Age - 日志文件保存天数
	Age int `json:"age" comment:"日志文件保存天数" default:"7"`
	// Backups - 日志文件最大保存数量
	Backups int `json:"backups" comment:"日志文件最大保存数量" default:"20"`
	// Console - 同时输出到控制台（彩色文本格式，便于开发调试）
	Console bool `json:"console" comment:"同时输出到控制台"`
}

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func normConfig(config Config) Config {

	if utils.Is.Empty(config.Root) {
		config.Root = "runtime/logs"
	}
	if utils.Is.Empty(config.Level) {
		config.Level = "debug"
	}
	if config.Size <= 0 {
		config.Size = 10
	}
	if config.Age <= 0 {
		config.Age = 7
	}
	if config.Backups <= 0 {
		config.Backups = 20
	}

	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}

	return config
}

// ================================== 配置 - 结束 ==================================

// ================================== 日志实例 - 开始 ==================================

// Logger - 日志实例：四个级别方法 + With 派生固定字段，底层为 zap Tee Core
type Logger struct {
	// 当前配置
	config Config
	// 底层 zap 实例（Tee Core：级别文件 + 可选控制台）
	zap *zap.Logger
	// 底层文件句柄（lumberjack），Close 时统一释放；With 派生实例不携带
	closers []io.Closer
}

// New - 按配置创建独立日志实例（配置自动归一化）
/**
 * @example：
 * 	logger := logx.New(logx.Config{Root: "runtime/logs", Level: "info"})
 * 	defer logger.Close()
 * 	logger.Info("用户登录", map[string]any{"uid": 1001})
 */
func New(config Config) *Logger {
	conf := normConfig(config)
	agent, closers := buildZap(conf)
	return &Logger{config: conf, zap: agent, closers: closers}
}

// Config - 当前配置
func (this *Logger) Config() Config {
	return this.config
}

// Zap - 取出底层 zap 实例（供对接 zap 生态的高级用法）
func (this *Logger) Zap() *zap.Logger {
	return this.zap
}

// Sync - 刷写缓冲区（建议程序退出前调用一次）
func (this *Logger) Sync() {
	_ = this.zap.Sync()
}

// Close - 刷写缓冲并释放底层文件句柄（热重载或退出前调用；关闭后实例不应再使用）
func (this *Logger) Close() {
	this.Sync()
	for _, closer := range this.closers {
		_ = closer.Close()
	}
}

// With - 派生带固定字段的子实例（原实例不受影响）
/**
 * @example：
 * 	reqLog := logx.Log.With(map[string]any{"traceId": "T-10086"})
 * 	reqLog.Error("支付失败", map[string]any{"orderId": "O-1"})
 */
func (this *Logger) With(data map[string]any) *Logger {
	return &Logger{config: this.config, zap: this.zap.With(mapFields(data)...)}
}

// Info - 信息日志
func (this *Logger) Info(msg string, data ...map[string]any) {
	this.write(zapcore.InfoLevel, msg, data)
}

// Warn - 警告日志
func (this *Logger) Warn(msg string, data ...map[string]any) {
	this.write(zapcore.WarnLevel, msg, data)
}

// Error - 错误日志（自动附带堆栈）
func (this *Logger) Error(msg string, data ...map[string]any) {
	this.write(zapcore.ErrorLevel, msg, data)
}

// Debug - 调试日志
func (this *Logger) Debug(msg string, data ...map[string]any) {
	this.write(zapcore.DebugLevel, msg, data)
}

// write - 统一写入：空消息回退级别名，多个字段表按序合并（后者覆盖前者）
func (this *Logger) write(level zapcore.Level, msg string, data []map[string]any) {

	if utils.Is.Empty(strings.TrimSpace(msg)) {
		msg = level.String()
	}

	fields := mapFields(mergeMaps(data))
	switch level {
	case zapcore.WarnLevel:
		this.zap.Warn(msg, fields...)
	case zapcore.ErrorLevel:
		this.zap.Error(msg, fields...)
	case zapcore.DebugLevel:
		this.zap.Debug(msg, fields...)
	default:
		this.zap.Info(msg, fields...)
	}
}

// mergeMaps - 合并字段表（后者覆盖前者）
func mergeMaps(data []map[string]any) map[string]any {
	if len(data) == 0 {
		return nil
	}
	merged := make(map[string]any, len(data[0]))
	for _, item := range data {
		for key, value := range item {
			merged[key] = value
		}
	}
	return merged
}

// mapFields - 字段表转 zap 字段（按键排序，保证输出顺序稳定）
func mapFields(data map[string]any) []zap.Field {
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

// ================================== 日志实例 - 结束 ==================================

// ================================== zap 构建 - 开始 ==================================

// levelFile - 级别文件定义
type levelFile struct {
	// 文件名（不含 .log 后缀）
	name string
	// 精确收录的级别（error 档收录 error 及以上，含 panic/fatal）
	level zapcore.Level
}

// levelFiles - 四个级别文件（与落盘文件名一致）
var levelFiles = []levelFile{
	{name: "debug", level: zapcore.DebugLevel},
	{name: "info", level: zapcore.InfoLevel},
	{name: "warn", level: zapcore.WarnLevel},
	{name: "error", level: zapcore.ErrorLevel},
}

// buildZap - 按配置构建 zap 实例（Disable 时返回 Nop，写操作零开销）
// 同时返回 lumberjack 文件句柄列表，供 Close 统一释放
func buildZap(conf Config) (*zap.Logger, []io.Closer) {

	if conf.Disable {
		return zap.NewNop(), nil
	}

	minLevel := parseLevel(conf.Level)
	date := time.Now().Format("2006-01-02")

	cores := make([]zapcore.Core, 0, len(levelFiles)+1)
	closers := make([]io.Closer, 0, len(levelFiles))

	// 级别文件 - 精确匹配收录，日志不重复写盘
	encoder := fileEncoder()
	for _, item := range levelFiles {
		core, closer := levelFileCore(encoder, conf, date, item, minLevel)
		cores = append(cores, core)
		closers = append(closers, closer)
	}

	// 控制台 - 彩色文本格式，收录最低级别以上全部
	if conf.Console {
		cores = append(cores, zapcore.NewCore(consoleEncoder(), zapcore.AddSync(os.Stdout), minLevel))
	}

	return zap.New(
		zapcore.NewTee(cores...),
		// 记录调用位置（跳过 write 分发与级别方法两层包装，指向业务调用方）
		zap.AddCaller(), zap.AddCallerSkip(2),
		// error 及以上自动附带堆栈
		zap.AddStacktrace(zapcore.ErrorLevel),
	), closers
}

// levelFileCore - 构建单个级别文件的 Core（lumberjack 滚动切割），返回句柄供 Close 释放
func levelFileCore(encoder zapcore.Encoder, conf Config, date string, item levelFile, minLevel zapcore.Level) (zapcore.Core, io.Closer) {

	roller := &lumberjack.Logger{
		Filename:   filepath.Join(conf.Root, date, item.name+".log"),
		MaxSize:    conf.Size,
		MaxAge:     conf.Age,
		MaxBackups: conf.Backups,
	}

	enabler := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		if level < minLevel {
			return false
		}
		// error 档收录 error 及以上（含 panic/fatal），其余档精确匹配
		if item.level == zapcore.ErrorLevel {
			return level >= zapcore.ErrorLevel
		}
		return level == item.level
	})

	return zapcore.NewCore(encoder, zapcore.AddSync(roller), enabler), roller
}

// fileEncoder - 落盘编码器（JSON，一行一条）
func fileEncoder() zapcore.Encoder {
	conf := zap.NewProductionEncoderConfig()
	conf.TimeKey = "time"
	conf.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	conf.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewJSONEncoder(conf)
}

// consoleEncoder - 控制台编码器（彩色级别 + 文本格式，便于开发阅读）
func consoleEncoder() zapcore.Encoder {
	conf := zap.NewProductionEncoderConfig()
	conf.TimeKey = "time"
	conf.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05")
	conf.EncodeLevel = zapcore.CapitalColorLevelEncoder
	conf.EncodeCaller = zapcore.ShortCallerEncoder
	return zapcore.NewConsoleEncoder(conf)
}

// parseLevel - 解析最低写入级别（非法值按 debug 处理）
func parseLevel(level string) zapcore.Level {
	parsed := new(zapcore.Level)
	if err := parsed.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err != nil {
		return zapcore.DebugLevel
	}
	return *parsed
}

// ================================== zap 构建 - 结束 ==================================
