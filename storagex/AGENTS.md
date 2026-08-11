# storagex/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `storagex` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 storagex 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/storagex`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）与 `../dto`。
- 定位：**文件存储**——以接口模式封装本地 / OSS / COS 文件存储，注册表 + 链式调用，可扩展后端。

## 目录结构

```
├── storagex.go    # Store 接口、registry 注册表（变量初始化登记）、Driver 链式包装
├── config.go      # Config 配置结构、normConfig 默认值补齐
├── facade.go      # 全局门面：Inst 控制器单例 + Storage 全局实例（storeError 占位）
├── local.go       # 本地磁盘驱动（os 落盘，List 内存分页 Marker 为偏移量）
├── oss.go         # 阿里云 OSS 驱动（云端 Marker 分页，目录为 / 结尾占位对象，Move 复制后删源）
├── cos.go         # 腾讯云 COS 驱动（同 OSS 语义）
└── storagex_test.go # 注册表、链式值语义、配置归一化、命名与响应组装、公开路径防护、本地驱动实测、热重载测试
```

## 核心约定

- **`Store` 接口是唯一扩展点**：只含 `Root` / `Domain` / `Put` / `List` / `MakeDir` / `Remove` / `Move` 七个方法，均带 `context.Context`。内置驱动在 `storagex.go` 的 `registry` 变量初始化时登记；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名覆盖。新增内置驱动：新建文件实现 `Store`，并在 `registry` 登记一行。
- **`Store` 契约**：所有方法的 key / dir / src / dst 均为**相对存储根**的路径（不含根目录名），由 Driver 层完成公开路径换算与路径穿越校验后传入；`List` 返回条目只需填 `Name` / `IsDir` / `Size` / `ModTime`，`Path` / `Url` 由 Driver 层组装。
- **`Driver` 链式包装**（`Ctx` / `Dir` / `Name` / `Ext` / `Put` / `List` / `MakeDir` / `Remove` / `Move` / `Root`），**值语义**：每次链式调用返回副本。**对象命名统一收敛在 Driver 层**（缺省目录 `年-月/日/`、缺省文件名 `毫秒时间戳-随机后缀` 防同毫秒撞名）；`Name` 统一 `path.Base` 去目录成分。`storagex.New("oss", config)` 创建独立实例。
- **公开路径语义**：根为 `store.Root()`（local 取根目录最后一段，OSS/COS 为配置的 `Path`），`Put` 响应与 `List` 条目的 `Path` 带前导 `/`，`Url = Domain + Path`；`List` 的 `Prefix` 为 Driver 层页内过滤（云端分页场景命中数量可能少于 Limit）。
- **云驱动不自动建桶**：OSS/COS 的 Bucket 需预先创建，配置不完整（缺 AK/SK/Bucket 等）在工厂阶段直接报错，不做静默降级；初始化失败时全局位用 `storeError` 占位，所有操作返回原始初始化错误。
- **配置自包含**：`storagex.Config`（含 `local` / `oss` / `cos` 三组内置驱动配置，外部扩展配置放 `Config.Options`）；`normConfig()` 补齐默认值（引擎未注册回退 `local`，本地根目录默认 `public/storage`、域名默认 `http://localhost:2000`）。
- **全局门面**：控制器单例 `storagex.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `storagex.Storage`。

## 安全约定

- OSS/COS 的 AccessKey、Secret 通过 `storagex.Config` 运行时注入，仓库中不得硬编码任何真实凭据。
- **路径穿越防护**：公开路径必须经过包内 `cleanDir` / `splitPublicPath` 清理（`cleanDir` 显式拒绝 `..` 段，`splitPublicPath` 拒绝越出存储根），文件名用 `path.Base` 去除目录成分；三类校验统一收敛在 Driver 层，改动存储路径逻辑时不得绕过。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，用假驱动避免联网；本地驱动用临时目录实测。
- 现有覆盖：注册表、链式值语义、配置归一化、上传命名与响应组装、公开路径换算与穿越防护、列目录过滤、本地驱动全流程实测、控制器热重载。

## 构建与测试命令

```bash
cd storagex && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
