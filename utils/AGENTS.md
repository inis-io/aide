# utils/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `utils` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 utils 专属约定。

## 模块定位

- 模块路径 `github.com/inis-io/aide/utils`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用 `../dto`（jwt.go / cipher.go 使用 `dto.JwtBody`）。
- 定位：**无状态工具函数集合**（36 个文件）：校验、加解密、JWT、日期、HTTP、文件、数组、掩码等，供各业务子包（pushx / cachex / storagex / logx / taskx）与外部项目复用。
- 子模块经 `replace github.com/inis-io/aide/utils => ../utils` 引用本模块；修改依赖时同步维护 `go.mod`。

## 目录结构（文件即能力名，共 36 个 .go 文件）

```
├── is.go / has.go / in.go     # 判空、包含、存在性校验（Is / Has / In）
├── basics.go / types.go       # 基础类型与常用工具（Basics）
├── array.go / map.go          # 数组与映射工具
├── validate.go / identify.go  # 校验（Validate）、标识识别（Identify：邮箱/手机号）
├── hash.go                    # 哈希（Hash，Sum32 等）
├── aes.go / rsa.go / cipher.go # 加解密（AES / RSA / Cipher，后两者依赖 dto.JwtBody）
├── jwt.go / password.go       # JWT（依赖 dto.JwtBody）、密码（Password，bcrypt）
├── json.go                    # JSON（json-iterator）
├── date.go / rand.go          # 日期（Date）、随机（Rand）
├── file.go / net.go / http.go / url.go # 文件（File/NewFile）、网络（Net）、HTTP（Http）、URL
├── get.go / gen.go            # 取值（Get）、生成（Gen，snowflake/uuid）
├── mask.go                    # 脱敏（Mask：手机号/邮箱/身份证）
├── lang.go / cases.go / format.go # 语言（Lang）、大小写（Cases）、格式化（Format）
├── struct.go / mime.go / app.go  # 结构体（Struct）、MIME（Mime）、应用（App）
├── parse.go / unity.go / other.go # 解析（Parse）、统一（Unity）、其他（Other）
├── version.go                 # 版本比对（Version）
├── async.go / cache.go        # 异步（Async）、缓存（Cache/NewFileCache）
└── viper.go / env.go          # 配置（Viper / Env）
```

## 核心约定

- **无状态工具类**：文件中声明 `var Is *IsClass` 这类**全局 nil 指针变量**，方法全部挂在指针接收者上但不解引用 `this`，因此 nil 接收者也能正常调用。调用方式如 `utils.Is.Empty(x)`、`utils.Hash.Sum32(s)`、`utils.Json.Encode(v)`。
- **有状态能力**改用**构造函数 + 链式调用**：`utils.Http(...)`、`utils.Viper(...)`、`utils.NewFile(...)`、`utils.Env()`、`utils.NewFileCache(...)`。
- **新增方法**：优先挂到已有工具类上，保持文件名即能力名的约定（如 `mask.go` 对应 `Mask`）；新能力先确认无既有工具类可挂载，再决定是否新建文件。
- **类型转换**统一用 `github.com/spf13/cast`；**判空**统一用 `utils.Is.Empty(...)`，不手写零值判断。
- **错误处理风格**：工具方法多返回带 `Error error` 字段的响应结构体（如 `StorageResp`、`ViperResponse`），而非多返回值抛错；调用方检查 `.Error`。

## 安全约定

- 不硬编码任何真实密钥；加解密/签名相关的密钥均由调用方传入。
- `utils.Mask` 提供手机号/邮箱/身份证等脱敏能力，输出含个人信息的内容到日志前应使用它。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，不依赖外部服务，禁止联网。
- 当前 utils 无测试文件；新增可脱离外部服务运行的逻辑时应补充单元测试。

## 构建与测试命令

```bash
cd utils && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 文档

- 父模块总览见 [`../README.md`](../README.md)；各子模块 AGENTS.md 见 `../dto`、`../pushx` 等目录。
