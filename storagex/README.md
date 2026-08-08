# storagex - 存储

> 包路径：`github.com/inis-io/aide/storagex`
> 以接口模式封装本地磁盘 / 对象存储文件存储，注册表 + 链式调用，可自由扩展存储后端。

## 1. 特性

- **接口模式**：`Store` 接口是唯一扩展点（7 个方法，均带 `context.Context`），新后端实现即可接入
- **内置驱动**：`local`（本地磁盘）、`oss`（阿里云对象存储）、`cos`（腾讯云对象存储）
- **链式调用**：值语义，每次调用返回副本，并发安全，天然隔离上下文
- **路径安全**：公开路径换算与路径穿越防护统一收敛在 Driver 层，驱动只接收安全相对路径
- **文件管理**：上传之外支持列目录、建目录、删除（递归）、移动/重命名
- **全局门面**：控制器单例 + 全局实例，支持配置热重载

## 2. 安装

```bash
go get github.com/inis-io/aide/storagex
```

```go
import "github.com/inis-io/aide/storagex"
```

## 3. 快速开始

```go
package main

import (
	"fmt"
	"os"

	"github.com/inis-io/aide/storagex"
)

func main() {

	// 1) 初始化全局存储（应用启动时执行一次；不初始化则默认 local 引擎）
	storagex.Inst.Init(storagex.Config{
		Engine: "local",
		Local: storagex.LocalConfig{
			Domain: "http://localhost:2000", // 访问域名，用于拼接 Url
			Root:   "public/storage",        // 本地存储根目录
		},
	})

	// 2) 上传：缺省按 年-月/日 生成目录、毫秒时间戳-随机后缀 生成文件名
	file, _ := os.Open("./avatar.png")
	defer file.Close()

	resp := storagex.Storage.Ext("png").Put(file)
	if resp.Error != nil {
		panic(resp.Error)
	}
	fmt.Println(resp.Path) // /storage/2026-08/08/1786180307028-xK9f2a.png
	fmt.Println(resp.Url)  // http://localhost:2000/storage/2026-08/08/1786180307028-xK9f2a.png

	// 3) 指定目录与文件名（Name 自动去除目录成分，Dir 拒绝 .. 越界）
	resp = storagex.Storage.Dir("avatar").Name("user-1001").Ext("jpg").Put(file)

	// 4) 列目录（目录在前，文件在后）
	list := storagex.Storage.List(storagex.ListParams{Dir: "/storage/avatar", Limit: 50})
	for _, entry := range list.List {
		fmt.Println(entry.Name, entry.Path, entry.Url, entry.Size)
	}

	// 5) 文件管理
	storagex.Storage.MakeDir("/storage/media")
	storagex.Storage.Move("/storage/avatar/user-1001.jpg", "/storage/media/user-1001.jpg")
	storagex.Storage.Remove("/storage/media/user-1001.jpg") // 目录则递归删除
}
```

## 4. 链式方法

| 方法 | 说明 |
|---|---|
| `Ctx(ctx)` | 请求上下文（控制云存储操作的取消与超时，默认 `context.Background()`） |
| `Dir(dir)` | 上传目录（相对存储根；显式拒绝 `..` 段，非法时回退缺省日期目录） |
| `Name(name)` | 上传文件名（自动去除目录成分；为空按 `毫秒时间戳-随机后缀` 生成，防同毫秒撞名） |
| `Ext(ext)` | 上传文件后缀（自动补前导 `.`） |
| `Put(reader)` | 上传文件 |
| `List(params)` | 列目录（见下节） |
| `MakeDir(dir)` | 创建目录（公开路径） |
| `Remove(paths ...)` | 删除文件或目录（公开路径，目录递归删除） |
| `Move(src, dst)` | 移动或重命名（公开路径；禁止移动到自身内部） |
| `Root()` | 存储根公开路径（如 `/storage`、`/AIDE`） |
| `Store()` | 取出底层驱动（供类型断言访问驱动特有方法） |

## 5. 公开路径语义

存储根由驱动决定（`store.Root()`）：`local` 取根目录最后一段（`public/storage` → `/storage`），`oss` / `cos` 为配置的 `Path`（如 `/AIDE`）。

- `Put` 响应与 `List` 条目的 `Path` 为带前导 `/` 的公开路径，`Url = Domain + Path`（Domain 未配置时用存储桶默认域名）
- 所有 `MakeDir` / `Remove` / `Move` / `List.Dir` 的入参均为公开路径，Driver 层统一校验并换算为相对路径后交给驱动

`storagex.Resp`：`Error`（nil 即成功）、`Path`、`Url`、`Domain`、`Name`。

## 6. 列目录与分页

```go
params := storagex.ListParams{
	Dir:    "/storage/media", // 公开路径，空串表示存储根
	Marker: "",               // 分页标记（上一页响应的 NextMarker）
	Limit:  100,              // 每页数量（默认 100，上限 1000）
	Prefix: "user-",          // 名称前缀过滤（页内过滤）
}
resp := storagex.Storage.List(params)
// resp.List：条目（目录在前，文件在后）
// resp.NextMarker：非空则有下一页，回传给 Marker 继续翻页
```

`storagex.Entry`：`Name`、`Path`、`Url`（仅文件）、`Size`、`IsDir`、`ModTime`（毫秒）。

注意：`Prefix` 为 Driver 层页内过滤。云端分页场景下命中数量可能少于 `Limit`，是否还有下一页以 `NextMarker` 为准。`local` 为内存分页（Marker 即偏移量），`oss` / `cos` 为云端 Marker 分页。

## 7. 独立实例

```go
driver, err := storagex.New("oss", storagex.Config{
	OSS: storagex.OSSConfig{
		AccessKeyId:     "阿里云 AccessKey ID",
		AccessKeySecret: "阿里云 AccessKey Secret",
		Endpoint:        "oss-cn-guangzhou.aliyuncs.com",
		Bucket:          "my-bucket", // 需预先创建
		Path:            "AIDE",
	},
})
if err != nil {
	panic(err) // 配置不完整（缺 AK/SK/Bucket 等）在此直接报错
}

resp := driver.Dir("docs").Ext("pdf").Put(file)
```

## 8. 配置项

`storagex.Config`：

| 字段 | 说明 |
|---|---|
| `Engine` | 引擎：`local` / `oss` / `cos` / 自定义注册名（未注册名回退 `local`） |
| `Local` | `Domain`（默认 `http://localhost:2000`）、`Root`（默认 `public/storage`） |
| `OSS` | `AccessKeyId`、`AccessKeySecret`、`Endpoint`（默认 `oss-cn-guangzhou.aliyuncs.com`）、`Bucket`、`Domain`（可选，默认用桶外网域名）、`Path`（默认 `AIDE`） |
| `COS` | `AppId`、`SecretId`、`SecretKey`、`Bucket`、`Region`（默认 `ap-guangzhou`）、`Domain`（可选）、`Path`（默认 `AIDE`） |
| `Options` | 扩展驱动的自定义配置（`map[驱动名]map[string]any`） |
| `Hash` | 配置变更指纹（不传自动计算） |

凭据全部运行时注入，请勿硬编码进仓库。**云驱动不自动建桶**：Bucket 需在控制台预先创建，配置不完整在工厂阶段直接报错，不做静默降级。

## 9. 扩展新后端

实现 `Store` 接口并在自己包内注册：

```go
package qiniu

import (
	"context"
	"io"

	"github.com/inis-io/aide/storagex"
)

type store struct{ config storagex.Config }

func (this store) Root() string   { return "QINIU" }
func (this store) Domain() string { return "https://cdn.example.com" }
func (this store) Put(ctx context.Context, key string, reader io.Reader) error {
	// key 为相对存储根的安全路径（已经 Driver 层穿越校验），按原名上传
	return nil
}
func (this store) List(ctx context.Context, dir string, marker string, limit int) ([]storagex.Entry, string, error) {
	// 返回条目只需填 Name / IsDir / Size / ModTime，Path / Url 由 Driver 层组装
	return nil, "", nil
}
func (this store) MakeDir(ctx context.Context, dir string) error      { return nil }
func (this store) Remove(ctx context.Context, paths ...string) error  { return nil }
func (this store) Move(ctx context.Context, src, dst string) error    { return nil }

func newStore(config storagex.Config) (storagex.Store, error) {
	// 自定义配置从 config.Options["qiniu"] 读取
	return store{config: config}, nil
}

func init() {
	storagex.Register("qiniu", newStore) // 同名注册会覆盖先注册者
}
```

注册后：`storagex.New("qiniu", config)` 可用；`Config.Engine` 填 `"qiniu"` 即可接入全局门面。

## 10. 全局门面与热重载

| 入口 | 说明 |
|---|---|
| `storagex.Inst` | 控制器单例：`Init(config)` 注入配置、`ReloadIfChanged()` 按 Hash 热重载 |
| `storagex.Storage` | 全局链式存储实例 |

驱动初始化失败时全局位用错误占位实现，所有操作返回原始初始化错误，不会静默吞错。

## 11. 注意事项

- 对象存储的"目录"是以 `/` 结尾的空占位对象；`Move` 为逐对象复制后删除源对象，大目录移动非原子操作
- 默认配置（不调用 `Init`）为 `local` 引擎，落盘 `public/storage/`，该目录为运行时产物，请勿提交进仓库
- 路径安全三类校验（`Dir` 的 `..` 拒绝、公开路径换算、`Name` 去目录成分）统一收敛在 Driver 层，自定义驱动无需重复实现，也不应绕过
