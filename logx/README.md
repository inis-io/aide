# logx - 日志

> 包路径：`github.com/inis-io/aide/logx`
> 基于 zap + lumberjack 的结构化日志，按级别分文件精确收录，滚动切割。

## 1. 特性

- **级别分文件**：`debug` / `info` / `warn` / `error` 四个文件**精确匹配**收录（error 档含 panic/fatal），日志不重复写盘
- **滚动切割**：lumberjack 按大小切割、按天数与份数清理，落盘结构 `根目录/日期/级别.log`
- **结构化字段**：`map[string]any` 传字段，JSON 落盘一行一条，输出按键排序稳定
- **零开销关闭**：`Disable=true` 构建 Nop Logger，写操作无开销
- **开发友好**：`Console=true` 彩色文本同步输出到 stdout；error 自动附带堆栈；caller 定位到业务调用方
- **全局门面**：控制器单例 + 全局实例，支持配置热重载（自动释放旧实例句柄）

## 2. 安装

```bash
go get github.com/inis-io/aide/logx
```

```go
import "github.com/inis-io/aide/logx"
```

## 3. 快速开始

```go
package main

import (
	"github.com/inis-io/aide/logx"
)

func main() {

	// 1) 初始化全局日志（应用启动时执行一次；不初始化则默认配置，写 runtime/logs）
	logx.Inst.Init(logx.Config{
		Level:   "info", // 最低写入级别（低于阈值的级别文件不创建）
		Console: true,   // 开发环境同时输出到控制台
	})

	// 2) 按级别写入（字段为可选的 map[string]any，msg 为空回退级别名）
	logx.Log.Info("create user", map[string]any{"module": "user", "id": 1001})
	logx.Log.Warn("slow query", map[string]any{"cost": "2.3s", "sql": "SELECT ..."})
	logx.Log.Error("pay failed", map[string]any{"orderId": "O-1"}) // 自动附带堆栈
	logx.Log.Debug("trace point")

	// 3) 派生带固定字段的子实例（原实例不受影响，适合请求级日志）
	reqLog := logx.Log.With(map[string]any{"traceId": "T-10086"})
	reqLog.Info("request in", map[string]any{"path": "/api/pay"})
	reqLog.Error("request failed")

	// 4) 退出前刷缓冲
	defer logx.Log.Sync()
}
```

落盘结果（JSON 一行一条，含时间与调用位置）：

```
runtime/logs/2026-08-08/info.log     ← 只有 info
runtime/logs/2026-08-08/warn.log     ← 只有 warn
runtime/logs/2026-08-08/error.log    ← error 及以上（含堆栈）
runtime/logs/2026-08-08/debug.log    ← 只有 debug
```

```json
{"level":"info","time":"2026-08-08 15:04:05","caller":"main.go:15","msg":"create user","id":1001,"module":"user"}
```

## 4. 方法

| 方法 | 说明 |
|---|---|
| `Info(msg, data ...map[string]any)` | 信息日志 |
| `Warn(msg, data ...map[string]any)` | 警告日志 |
| `Error(msg, data ...map[string]any)` | 错误日志（自动附带堆栈） |
| `Debug(msg, data ...map[string]any)` | 调试日志 |
| `With(data map[string]any)` | 派生带固定字段的子实例（原实例不受影响；派生实例不持有文件句柄） |
| `Zap()` | 取出底层 `*zap.Logger`（对接 zap 生态的高级用法） |
| `Sync()` | 刷写缓冲区（建议程序退出前调用一次） |
| `Close()` | 刷缓冲并释放 lumberjack 文件句柄（独立实例用 `defer logger.Close()`） |
| `Config()` | 当前配置 |

字段表可传多个，按序合并（后者覆盖前者）；`msg` 为空或全空白时回退为级别名。

## 5. 独立实例

不经过全局门面，按配置创建（适合临时调试、多租户分文件）：

```go
logger := logx.New(logx.Config{
	Root:  "runtime/logs/tenant-9",
	Level: "debug",
	Size:  5, // 单文件 5MB
})
defer logger.Close() // 释放文件句柄

logger.Debug("debug once", map[string]any{"traceId": "T-10086"})
```

## 6. 配置项

`logx.Config`（零值即可用：默认启用、写 `runtime/logs`）：

| 字段 | 说明 | 默认值 |
|---|---|---|
| `Disable` | 关闭日志（零值 `false` 即启用，`true` 时构建 Nop Logger 零开销） | `false` |
| `Root` | 日志根目录（落盘 `根目录/日期/级别.log`） | `runtime/logs` |
| `Level` | 最低写入级别：`debug` / `info` / `warn` / `error`（非法值按 debug） | `debug` |
| `Size` | 单个日志文件大小（MB），超过则切割 | `10` |
| `Age` | 日志文件保存天数 | `7` |
| `Backups` | 日志文件最大保存数量 | `20` |
| `Console` | 同时输出到控制台（彩色文本格式，便于开发阅读） | `false` |
| `Hash` | 配置变更指纹（不传自动计算） | - |

## 7. 全局门面与热重载

| 入口 | 说明 |
|---|---|
| `logx.Inst` | 控制器单例：`Init(config)` 注入配置、`ReloadIfChanged()` 按 Hash 热重载 |
| `logx.Log` | 全局日志实例（`*logx.Logger`） |

热重载在单次临界区内原子替换全局实例，随后自动 `Close()` 旧实例释放文件句柄，不会丢日志也不会泄漏句柄。

## 8. 注意事项

- 级别文件为**精确匹配**收录（不是阈值语义）：`info.log` 只有 info，排障时按级别直接翻对应文件即可；`Level` 阈值控制的是"哪些级别文件在写"
- caller 定位已跳过包装层，指向业务调用方（`zap.AddCaller` + `AddCallerSkip(2)`）
- `With` 派生实例与原实例共享底层文件句柄，只有原实例（或全局门面）负责 `Close()`；派生实例无需也不应 `Close()`
- 首次写入才创建目录与文件（lumberjack 懒加载），import 本包无副作用
- `runtime/logs` 为运行时产物目录，请勿提交进仓库
- 输出含个人信息的内容到日志前，请先用 `utils.Mask` 脱敏（手机号 / 邮箱 / 身份证等）
