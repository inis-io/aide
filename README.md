### 简介
> 这是一个 [GoLang](https://golang.org/) 的工具包，包含了许多常用的函数，用于简化开发过程中的重复性工作。

### 安装
```bash
go get github.com/inis-io/aide
```

### 使用
> 详细的使用方法请参考 [文档](./document/README.md)

### Task 快速使用

```go
package main

import (
	"context"
	"time"

	"github.com/inis-io/aide/taskx"
)

func main() {
	taskx.Inst.Init(taskx.Config{Engine: "file"})
	taskx.Queue.Handle("mail:send", taskx.HandlerFunc(func(ctx context.Context, msg *taskx.Message) error {
		return nil
	}))

	_, _ = taskx.Queue.New("mail:send", map[string]any{"userId": 1001}).
		MaxRetry(3).
		Timeout(30 * time.Second).
		TaskID("mail:1001").
		Enqueue(context.Background())

	_ = taskx.Queue.Run(context.Background())
}
```

> `taskx` 提供 file / redis 双后端，完整配置、可靠性语义、Scheduler 与 Inspect/Manage 用法见 [taskx 文档](./taskx/README.md)。

### Storage 快速使用

```go
package main

import (
	"os"

	"github.com/inis-io/aide/storagex"
)

func main() {
	// 1) 初始化全局存储（推荐在应用启动时执行一次）
	storagex.Inst.Init(storagex.Config{
		Engine: "local",
		Local:  storagex.LocalConfig{Domain: "http://localhost:2000"},
	})

	// 2) 使用全局实例
	file, _ := os.Open("./avatar.png")
	defer file.Close()
	resp := storagex.Storage.Dir("avatar").Ext("png").Put(file)
	_ = resp // resp.Url 为完整访问地址

	// 3) 按配置创建独立实例（适合多租户或临时切换引擎）
	custom, _ := storagex.New("local", storagex.Config{})
	_ = custom
}
```

### Log 快速使用

```go
package main

import (
	"github.com/inis-io/aide/logx"
)

func main() {
	// 1) 初始化全局日志（推荐在应用启动时执行一次）
	logx.Inst.Init(logx.Config{
		Level:   "info", // 最低写入级别
		Size:    10,     // 单个日志文件大小（MB）
		Age:     15,     // 日志保留天数
		Backups: 30,     // 最大备份数量
		Console: true,   // 同时输出到控制台（开发调试）
	})

	// 2) 使用全局实例（按级别分文件精确收录：根目录/日期/级别.log）
	logx.Log.Info("create user", map[string]any{"module": "user", "id": 1001})
	logx.Log.Warn("slow query", map[string]any{"module": "user", "id": 1001})

	// 3) 派生带固定字段的子实例（原实例不受影响）
	reqLog := logx.Log.With(map[string]any{"traceId": "T-10086"})
	reqLog.Error("pay failed", map[string]any{"orderId": "O-1"})

	// 4) 按配置创建独立实例（适合临时调试、多租户）
	custom := logx.New(logx.Config{Root: "runtime/logs", Size: 5, Age: 3, Backups: 5})
	defer custom.Close()
	custom.Debug("debug once", map[string]any{"traceId": "T-10086"})
}
```

> `logx.Config` 默认值：启用（`Disable=false`）、`Root=runtime/logs`、`Level=debug`、`Size=10`、`Age=7`、`Backups=20`、`Console=false`。

