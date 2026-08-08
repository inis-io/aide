# cachex - 缓存

> 包路径：`github.com/inis-io/aide/cachex`
> 以接口模式封装文件 / Redis 缓存，注册表 + 链式调用，可自由扩展缓存后端。

## 1. 特性

- **接口模式**：`Store` 接口是唯一扩展点（仅 5 个方法），新后端实现即可接入
- **内置驱动**：`file`（本地文件，零依赖开箱即用）、`redis`（go-redis）
- **链式调用**：值语义，每次调用返回副本，并发安全，天然隔离上下文
- **标签分组**：`Tag` 簿记成员、`Delete` 按标签整组清除，簿记收敛在 Driver 层，新后端免费获得
- **全局门面**：控制器单例 + 全局实例，支持配置热重载

## 2. 安装

```bash
go get github.com/inis-io/aide/cachex
```

```go
import "github.com/inis-io/aide/cachex"
```

## 3. 快速开始

```go
package main

import (
	"fmt"
	"time"

	"github.com/inis-io/aide/cachex"
)

func main() {

	// 1) 初始化全局缓存（应用启动时执行一次；不初始化则默认 file 引擎）
	cachex.Inst.Init(cachex.Config{
		Engine: "redis",
		Redis: cachex.RedisConfig{
			Host:     "127.0.0.1",
			Port:     6379,
			Password: "",
			Database: 0,
		},
	})

	// 2) 基本读写（值须可 JSON 序列化）
	cachex.Cache.Set("username", "张三")
	value := cachex.Cache.Get("username") // any 类型，自行断言或 cast 转换
	fmt.Println(value)

	// 3) 指定过期时间（支持 time.Duration、duration 字符串、数值秒）
	cachex.Cache.Expired(5 * time.Minute).Set("code", "123456")
	cachex.Cache.Expired("10m").Set("ticket", "T-1")
	cachex.Cache.Expired(300).Set("token", "abc") // 300 秒
	cachex.Cache.Expired(0).Set("config", "...")  // <= 0 表示永不过期

	// 4) 判断与删除
	if cachex.Cache.Has("username") {
		cachex.Cache.Delete("username")
	}
}
```

## 4. 标签分组

`Tag` 在 `Set` 时把键簿记到标签下，`Delete` 时按标签整组清除（成员键与标签列表一并删除）：

```go
cachex.Cache.Tag("user").Set("user:1001:name", "张三")
cachex.Cache.Tag("user").Set("user:1001:role", "admin")
cachex.Cache.Tag("goods").Set("goods:9:price", 99)

// 清除 user 标签下全部缓存
cachex.Cache.Tag("user").Delete()

// 也可混合：删除指定键 + 指定标签
cachex.Cache.Key("goods:9:price").Tag("user").Delete()
```

标签簿记带键控锁，进程内并发写入安全；标签列表本身永不过期。

## 5. 链式方法

| 方法 | 说明 |
|---|---|
| `Tag(tag ...string)` | 标签（`Set` 时簿记成员，`Delete` 时按标签整组删除） |
| `Key(key ...string)` | 累积键名（`Delete` 时一并删除） |
| `Expired(second any)` | 过期时间：`time.Duration` / `"5s"`、`"10m"` 字符串 / 数值（按秒）；`<= 0` 永不过期 |
| `Has(key)` | 判断缓存是否存在（过期视为不存在） |
| `Get(key)` | 获取缓存（未命中或已过期返回 `nil`） |
| `Set(key, value)` | 设置缓存（value 须可 JSON 序列化） |
| `Delete(key ...string)` | 删除缓存（实参键 + 链式累积键 + 各标签成员） |
| `Clear()` | 清空缓存（redis 按前缀扫描删除，前缀为空回退 `FlushDB`） |
| `Store()` | 取出底层驱动（供类型断言访问驱动特有方法） |

键命名规则：`前缀-MD5前16位(key)`（64 位哈希，碰撞概率极低），驱动按原名持久化。

## 6. 独立实例

```go
driver, err := cachex.New("redis", cachex.Config{
	Redis: cachex.RedisConfig{Host: "127.0.0.1", Port: 6379, Prefix: "SHOP"},
})
if err != nil {
	panic(err)
}

driver.Expired(time.Hour).Set("cart:1001", items)
```

## 7. 配置项

`cachex.Config`：

| 字段 | 说明 |
|---|---|
| `Engine` | 引擎：`file` / `redis` / 自定义注册名（未注册名回退 `file`） |
| `Redis` | `Host`（默认 `127.0.0.1`）、`Port`（默认 6379）、`Password`、`Database`、`Prefix`（默认 `AIDE`）、`Expired`（秒，默认 7200） |
| `File` | `Root`（默认 `./runtime/cache`）、`Suffix`（默认 `json`）、`Prefix`（默认 `AIDE`）、`Expired`（秒，默认 7200） |
| `Options` | 扩展驱动的自定义配置（`map[驱动名]map[string]any`） |
| `Hash` | 配置变更指纹（不传自动计算） |

凭据全部运行时注入，请勿硬编码进仓库。file 驱动落盘 `Root/键名.后缀`（JSON 格式，临时文件 + Rename 原子写入）。

## 8. 扩展新后端

实现 `Store` 接口并在自己包内注册：

```go
package memory

import (
	"time"

	"github.com/inis-io/aide/cachex"
)

type store struct{ items map[string]any }

func (this store) Has(key string) bool           { _, ok := this.items[key]; return ok }
func (this store) Get(key string) any            { return this.items[key] }
func (this store) Set(key string, value any, expired time.Duration) bool {
	this.items[key] = value // expired <= 0 表示永不过期
	return true
}
func (this store) Delete(key ...string) bool {
	for _, item := range key { delete(this.items, item) }
	return true
}
func (this store) Clear() bool { clear(this.items); return true }

func newStore(config cachex.Config) (cachex.Store, error) {
	// 自定义配置从 config.Options["memory"] 读取
	return store{items: map[string]any{}}, nil
}

func init() {
	cachex.Register("memory", newStore) // 同名注册会覆盖先注册者
}
```

`Store` 契约：

- 键由 Driver 层命名（前缀 + 哈希），驱动按原名持久化，不要自行再加工
- `Set` 的 value 须可 JSON 序列化，`expired <= 0` 表示永不过期
- `Get` 未命中或已过期返回 `nil`；返回值 `bool` 仅表示操作是否成功

注册后：`cachex.New("memory", config)` 可用；`Config.Engine` 填 `"memory"` 即可接入全局门面。

## 9. 全局门面与热重载

| 入口 | 说明 |
|---|---|
| `cachex.Inst` | 控制器单例：`Init(config)` 注入配置、`ReloadIfChanged()` 按 Hash 热重载 |
| `cachex.Cache` | 全局链式缓存实例 |

驱动初始化失败时全局位用错误占位实现，所有操作返回失败，不会静默吞错。

## 10. 注意事项

- 默认配置（不调用 `Init`）为 `file` 引擎，落盘 `./runtime/cache/`，该目录为运行时产物，请勿提交进仓库
- 键哈希曾从 32 位升级为 MD5 前 16 位，旧键不兼容，随默认过期时间自然淘汰，无需人工清理
- `Delete` 不传任何参数且没有链式 `Key`/`Tag` 时不做任何操作（返回成功）
