# cachex - 缓存

> 包路径：`github.com/inis-io/aide/cachex`
> 以接口模式封装文件 / 内存 / Redis 缓存，注册表 + 链式调用，可自由扩展缓存后端。

## 1. 特性

- **接口模式**：`Store` 接口是唯一扩展点（5 个读写方法 + 3 个原子方法），新后端实现即可接入
- **原子原语**：`Incr`（固定窗口自增）/ `SetNX`（占位不续期）/ `TTL`（存活查询），返回 `error` 可判别后端故障，支撑安全限流等 fail-closed 场景
- **内置驱动**：`file`（本地文件，零依赖开箱即用）、`memory`（ristretto v2，进程内高速缓存）、`layered`（内存 + 文件分层，读快且重启不丢）、`redis`（go-redis）
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
| `Incr(key)` | 原子自增 1（固定窗口计数：仅首次自增写入链式 `Expired` 指定的过期时间；返回 `(count, err)`，不参与标签簿记） |
| `SetNX(key, value)` | 仅当键不存在时设置（已存在不覆盖、不续期；返回 `(ok, err)`，不参与标签簿记） |
| `TTL(key)` | 剩余存活秒数（返回 `(seconds, err)`：`>0` 有效、`0` 不存在或已过期、`-1` 存在但永不过期） |
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

### 6.1 内存驱动（memory）

基于 ristretto v2（TinyLFU 准入 + SampledLFU 淘汰），进程内高速缓存，零依赖开箱即用：

```go
cachex.Inst.Init(cachex.Config{
	Engine: "memory",
	Memory: cachex.MemoryConfig{MaxEntries: 100000}, // 默认 10000
})
cachex.Cache.Expired(5 * time.Minute).Set("code", "123456")
```

与 file / redis 的差异（接入前须知）：

- **无持久化**：重启即全部丢失，不能承受丢失的数据请用 `file`（跨重启保留）或 `redis`（跨进程共享）
- **值直存不序列化**：类型保留（`int64` 不会变 `float64`），但存入的是原对象引用，调用方事后修改会反映到缓存
- **容量淘汰**：超过 `MaxEntries` 后按 TinyLFU/SampledLFU 策略淘汰（含准入拒绝），极端压力下 `Set` 成功也可能不保留
- **写后立即可读**：驱动内部已用 `Wait()` 对齐 file/redis 的同步语义，无需业务感知
- 独立实例（`cachex.New("memory", ...)`）用完后应 `driver.Store().(interface{ Close() error }).Close()` 释放后台 goroutine；全局门面热重载会自动关闭旧实例

### 6.2 分层驱动（layered）

L1 内存 + L2 文件的分层缓存（cache-aside 模式）：**读走内存（快）、写落文件（重启不丢）**，配置复用 `Memory` / `File` 两段：

```go
cachex.Inst.Init(cachex.Config{
	Engine: "layered",
	Memory: cachex.MemoryConfig{MaxEntries: 100000},
	File:   cachex.FileConfig{Root: "./runtime/cache"},
})
cachex.Cache.Expired(5 * time.Minute).Set("ticket", "T-1") // 落盘，首读回源后进内存
```

语义与边界：

- **写路径 ≈ 磁盘速度**：每次写都落盘（权威层），随后失效内存副本；首次读回源 L2 并回灌 L1
- **重启恢复**：进程重启后数据仍在文件层，懒加载回源（ristretto 无法枚举键，不做启动预热）；`Incr` 计数重启后连续
- **一致性**：L1 是 L2 的保守子集，回灌 TTL 向下取整，L1 只会比 L2 更早过期；文件层写失败即整体失败
- **适用**：读多写少 + 单进程 + 重启不想丢；高频写/计数请用 `redis`，只要不丢不在乎读速直接用 `file`

## 7. 配置项

`cachex.Config`：

| 字段 | 说明 |
|---|---|
| `Engine` | 引擎：`file` / `memory` / `layered` / `redis` / 自定义注册名（未注册名回退 `file`） |
| `Redis` | `Host`（默认 `127.0.0.1`）、`Port`（默认 6379）、`Password`、`Database`、`Prefix`（默认 `AIDE`）、`Expired`（秒，默认 7200） |
| `File` | `Root`（默认 `./runtime/cache`）、`Suffix`（默认 `json`）、`Prefix`（默认 `AIDE`）、`Expired`（秒，默认 7200） |
| `Memory` | `MaxEntries`（默认 10000，最大条目数）、`Metrics`（默认关，开启命中率统计）、`Prefix`（默认 `AIDE`）、`Expired`（秒，默认 7200） |
| `Options` | 扩展驱动的自定义配置（`map[驱动名]map[string]any`） |
| `Hash` | 配置变更指纹（不传自动计算） |

凭据全部运行时注入，请勿硬编码进仓库。file 驱动落盘 `Root/键名.后缀`（JSON 格式，临时文件 + Rename 原子写入）。

## 8. 扩展新后端

实现 `Store` 接口并在自己包内注册：

```go
package custom

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
func (this store) Incr(key string, expired time.Duration) (int64, error) {
	count, _ := this.items[key].(int64)
	count++
	this.items[key] = count // 示例从简：生产实现需仅在首次自增（count == 1）时写入过期时间
	return count, nil
}
func (this store) SetNX(key string, value any, expired time.Duration) (bool, error) {
	if _, ok := this.items[key]; ok { return false, nil }
	this.items[key] = value
	return true, nil
}
func (this store) TTL(key string) (int64, error) {
	if _, ok := this.items[key]; !ok { return 0, nil }
	return -1, nil // 示例从简：一律按永不过期处理
}

func newStore(config cachex.Config) (cachex.Store, error) {
	// 自定义配置从 config.Options["custom"] 读取
	return store{items: map[string]any{}}, nil
}

func init() {
	cachex.Register("custom", newStore) // 同名注册会覆盖先注册者（内置 memory 等亦可覆盖替换）
}
```

`Store` 契约：

- 键由 Driver 层命名（前缀 + 哈希），驱动按原名持久化，不要自行再加工
- `Set` 的 value 须可 JSON 序列化，`expired <= 0` 表示永不过期
- `Get` 未命中或已过期返回 `nil`；返回值 `bool` 仅表示操作是否成功
- 原子方法 `Incr` / `SetNX` / `TTL` 返回 `error` 暴露后端故障（fail-closed 调用方依赖该错误判别）；`Incr` 仅在自增结果为 1 时写入过期时间（固定窗口语义）；`TTL` 约定 `>0` 有效、`0` 不存在或已过期、`-1` 永不过期

注册后：`cachex.New("custom", config)` 可用；`Config.Engine` 填 `"custom"` 即可接入全局门面。

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
- `memory` 引擎无持久化（重启即失）、容量淘汰按 TinyLFU/SampledLFU 策略、存入值引用共享——详见 6.1 节差异说明
