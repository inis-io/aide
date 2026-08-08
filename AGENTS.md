# AGENTS.md

> 本文件面向 AI 编码代理，介绍本项目的结构、约定与常用命令。阅读前无需任何项目背景知识。

## 项目概览

- 项目名称：`aide`，模块路径 `github.com/inis-io/aide`（见 `go.mod`）。
- 定位：一个 **Go 工具包（library，不是可执行应用）**，收录常用函数与通用服务封装，用于简化业务开发中的重复性工作。仓库根目录的 `main.go` 是空的占位文件（`func main() {}`），项目本身不产出可运行程序。
- 安装方式：`go get github.com/inis-io/aide`。
- Go 版本要求：`go 1.26`（`go.mod` 中声明）。

## 技术栈与主要依赖

纯 Go 实现，无 cgo、无前端、无代码生成。主要第三方依赖（均为 `go.mod` 直接依赖）：

- 配置/解析：`spf13/viper`、`spf13/cast`、`spf13/afero`、`json-iterator/go`
- 缓存：`redis/go-redis/v9`
- 日志：`go.uber.org/zap`（间接）+ `natefinch/lumberjack.v2`（日志切割，间接）
- 存储：`aliyun/aliyun-oss-go-sdk`（阿里云 OSS）、`tencentyun/cos-go-sdk-v5`（腾讯云 COS）
- 短信：`alibabacloud-go/dysmsapi-20170525`（阿里云）、`tencentcloud-sdk-go/sms`（腾讯云）、`gomail.v2`（邮件）
- 其他：`golang-jwt/jwt/v5`、`bwmarrin/snowflake`、`google/uuid`、`mholt/archives`、`fsnotify`

## 目录结构与模块划分

```
├── main.go      # 空占位 main，无实际逻辑
├── dto/         # 数据传输对象：各服务的配置与响应结构体（StorageConfig、LogConfig、JwtBody 等），纯 struct，无行为
├── facade/      # 门面层：全局单例服务（存储 / 日志），面向调用方的统一入口
├── pushx/       # 消息推送：以接口模式封装短信 / 邮件验证码推送，注册表 + 链式调用，可扩展服务商
├── cachex/      # 缓存：以接口模式封装文件 / Redis 缓存，注册表 + 链式调用，可扩展后端
├── licence/     # 许可证签发与验签：Ed25519 签名信封，纯函数层，不依赖数据库与配置
└── utils/       # 工具函数集合（36 个文件）：校验、加解密、JWT、日期、HTTP、文件、数组、掩码等
```

### utils 包约定

- 大部分能力以「无状态工具类」形式暴露：文件中声明 `var Is *IsClass` 这类**全局 nil 指针变量**，方法全部挂在指针接收者上但不解引用 `this`，因此 nil 接收者也能正常调用。调用方式如 `utils.Is.Empty(x)`、`utils.Hash.Sum32(s)`、`utils.Json.Encode(v)`。
- 现有全局工具类（部分）：`Is`、`Has`、`In`、`Hash`、`AES`、`RSA`、`Json`、`Date`、`Rand`、`File`、`Net`、`Get`、`Gen`、`Mask`、`Lang`、`Format`、`Struct`、`Mime`、`Parse`、`Unity`、`Identify`、`Version`。
- 需要携带状态的功能改用**构造函数 + 链式调用**：`utils.Http(...)`、`utils.Viper(...)`、`utils.NewFile(...)`、`utils.Env()`、`utils.NewFileCache(...)`。
- 新增工具方法时优先挂到已有工具类上，保持与现有文件一致的命名（文件名即能力名，如 `mask.go` 对应 `Mask`）。

### facade 包约定

- 每个服务有一对入口：**配置控制器单例**（`StorageInst` / `LogInst`）+ **全局活动实例**（如 `facade.Storage`、`facade.Log`）。
- 统一生命周期：包 `init()` 时以默认配置初始化；调用方通过 `XxxInst.Init(dto.XxxConfig{...})` 注入配置；`ReloadIfChanged()` 依据配置 Hash 判断是否需要热重载。`normConfig()` 负责补齐默认值，保证不同接入方行为一致。
- 存储（`facade/storage.go`）支持 `local` / `oss` / `cos` 三种引擎，通过 `StorageAPI` 接口抽象；`Dir(...).Name(...).Ext(...).Upload(...)` 链式调用通过 clone 实现，链式参数与共享配置/客户端隔离。本地存储写入 `public/storage/` 目录。
- 日志（`facade/log.go`）基于 zap + lumberjack 滚动切割，默认值：`Size=2MB`、`Age=7天`、`Backups=20`。

### cachex 包约定（缓存）

- 接口模式封装文件 / Redis 缓存。**`Store` 接口是唯一扩展点**：只含 `Has` / `Get` / `Set(key, value, expired)` / `Delete` / `Clear` 五个方法。内置驱动在 `cachex.go` 的 `registry` **变量初始化时登记**（不依赖文件 init 顺序）；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名注册会覆盖先注册者。仓库内新增内置驱动：新建一个文件实现 `Store`，并在 `registry` 中登记一行。
- `Store` 契约：键由 `Driver` 层命名（`前缀-MD5前16位(key)`，64 位哈希降低碰撞；与 Sum32 时代的旧键不兼容，旧键随默认过期自然淘汰），驱动按原名持久化；`Set` 的 value 须可 JSON 序列化，**`expired <= 0` 表示永不过期**；`Get` 未命中或已过期返回 `nil`。
- `Driver` 是 `Store` 之上的链式包装（`Tag` / `Key` / `Expired` / `Has` / `Get` / `Set` / `Delete` / `Clear`），**值语义**：每次链式调用返回副本，天然隔离上下文。**标签簿记统一收敛在 Driver 层**（标签列表键 `前缀-TAG-大写标签名`，成员列表永不过期；簿记读-改-写带**键控锁**，进程内并发安全），后端不感知标签，新后端接入即免费获得标签能力。`cachex.New("redis", config)` 创建独立实例，`Driver.Store()` 可取底层驱动做类型断言。
- 配置自包含在包内：`cachex.Config`（含 `file` / `redis` 两组内置驱动配置，外部扩展驱动的自定义配置放 `Config.Options`）；`normConfig()` 补齐默认值（引擎名未注册时回退 `file`，默认前缀 `AIDE`、默认过期 7200 秒），`defaultContext()` 按引擎取对应分段的前缀与过期时间。
- 全局门面与 facade 层同构：控制器单例 `cachex.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `cachex.Cache`。驱动初始化失败时全局位用 `storeError` 占位，所有操作返回失败。
- 内置驱动文件：`file.go`（afero 文件缓存，落盘 `存储目录/键名.后缀` JSON，过期为秒级时间戳，**临时文件 + Rename 原子写入**，Windows 下 Rename 不允许覆盖会删目标重试，可注入 `afero.NewMemMapFs` 测试）、`redis.go`（go-redis，`Clear` 按前缀扫描删除，前缀为空时回退 `FlushDB`）。

### pushx 包约定（消息推送）

- 接口模式封装短信 / 邮件验证码推送。**`Sender` 接口是唯一扩展点**：只含 `Send(Message) (*Response, error)` 一个方法。内置驱动在 `pushx.go` 的 `registry` **变量初始化时登记**（不依赖文件 init 顺序）；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名注册会覆盖先注册者（可借此替换内置实现）。仓库内新增内置驱动：新建一个文件实现 `Sender`，并在 `registry` 中登记一行。
- 各驱动只发自己通道，发送前校验目标类型（邮件驱动校验邮箱、短信驱动校验手机号），不合法立即报错；跨通道路由由 `Router` 统一负责（按 `utils.Identify.EmailOrPhone` 分发），驱动不做跨通道路由。
- `Driver` 是 `Sender` 之上的链式包装（`Target` / `Code` / `Len` / `Expired` / `Subject` / `Template` / `Param` / `SetMessage` / `Send`），**值语义**：每次链式调用返回副本，天然隔离上下文，无需 clone。`pushx.New("aliyun", config)` 创建独立实例。
- 模板与变量替换：调用方通过 `Message.Template` 或链式 `Template(...)` 自定义模板，占位符格式 `${变量名}`，统一由 **`Message.Render`** 渲染（内置变量：`${target}` `${code}` `${length}` `${expired}` `${subject}` `${nickname}` `${username}` `${title}` `${address}` `${year}`；未识别占位符保留原样）。**只有本地渲染的驱动（email / smsbao）支持自定义模板；阿里云、腾讯云为云端控制台模板，本地仅传模板 ID 与参数，`Template` 对其无效。** 新增本地渲染驱动时必须走 `Render`，不得自写替换 map。
- 自定义模板变量 `Message.Params`（链式 `Param(key, value)`）：本地渲染驱动在 `Render` 中以 `${键名}` 使用（覆盖顺序：内置变量 < Params < 驱动级 extra）；**阿里云**按键名合并进模板变量 JSON（默认含 `code` / `time`，同名覆盖）；**腾讯云**为位置参数，按数字键名（`"1"`、`"2"`...，对应云端模板 `{1}`、`{2}`）升序组装为参数数组，提供后完全接管（不再自动附带验证码）。参数组装逻辑收敛在 `aliYunTemplateParams` / `tencentTemplateParams` 纯函数中，便于测试。
- 配置与消息体自包含在包内：`pushx.Config`（含 `email` / `aliyun` / `tencent` / `smsbao` 四组内置驱动配置，外部扩展驱动的自定义配置放 `Config.Options`，key 为驱动名）、`pushx.Message` / `pushx.Response`；`normConfig()` 补齐默认值（引擎名未注册时回退 `email` / `aliyun`），`normMessage()` 统一验证码长度（6）、有效期（5 分钟）并在验证码为空时自动生成，**驱动发送前必须调用**。
- 全局门面与 facade 层同构：控制器单例 `pushx.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `pushx.Push`（链式智能路由）、`pushx.Email` / `pushx.SMS`（当前驱动）。驱动初始化失败时全局位用 `senderError` 占位，`Send` 时返回原始初始化错误。
- 内置驱动文件：`email.go`（gomail 邮件）、`aliyun.go`、`tencent.go`、`smsbao.go`；邮件 HTML 模板常量在 `template.go`（`TempEmailCode`）。

### licence 包约定

- 纯函数层：`Issue`（签发）、`VerifyEnvelope`（验签）、`Parse`（解析）、`GenerateKeyPair`、`Nonce`。
- 信封格式（`Envelope`/`Payload`，`envelope.go`）与平台侧契约保持一致。**`Payload` 字段顺序即签名内容：新增字段只允许追加到结构体末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。**

## 构建与测试命令

无 Makefile、无 CI 配置（仓库中没有 `.github/`），直接使用 Go 工具链：

```bash
go build ./...    # 编译全部包
go vet ./...      # 静态检查
go test ./...     # 运行全部测试
go test ./licence/ -v   # 单独跑 licence 包测试
```

当前有测试的包：`licence`（签发/验签/防篡改，表驱动风格）、`pushx`（注册表、链式实例、配置/消息体归一化、模板渲染、云端参数组装、智能路由、控制器热重载，用假驱动避免联网）与 `cachex`（注册表、链式实例、配置归一化、过期解析、标签簿记、标签并发簿记回归、文件驱动内存文件系统实测、控制器热重载）。测试函数以 `Test` 开头、注释说明意图，使用标准库 `testing`。上述命令在 Go 1.26（windows/amd64）下均已验证通过。

## 代码风格指南

- 注释与文档一律使用**中文**；导出的类型、函数、方法必须有注释。
- 注释格式：行首 `// 名称 - 简述`；复杂方法使用块注释标注参数与示例：

  ```go
  // Compare - 版本号比对
  /**
   * @param v1 string - 小版本号
   * @return int - 0: 相等，1: v1 < v2，-1: v1 > v2
   * @example：
   * 	utils.Version.Compare("1.2.0", "1.0.0") // 1
   */
  ```
- 结构体字段逐行写中文注释；facade 层用 `====...==== 模块名 - 开始/结束 ====...====` 分隔带划分文件内的功能区块。
- 方法接收者统一命名为 `this`（不用 `s`、`c` 等缩写）。
- 类型转换统一用 `github.com/spf13/cast`（`cast.ToString` 等），判空统一用 `utils.Is.Empty(...)`，不要手写零值判断。
- 错误处理风格：工具方法多返回带 `Error error` 字段的响应结构体（如 `StorageResp`、`ViperResponse`），而不是多返回值抛错；调用方检查 `.Error`。
- 并发：facade 控制器用 `sync.RWMutex` 保护配置读写；链式操作通过 clone 隔离参数，避免修改共享实例。

## 测试说明

- 测试与源码同包、同目录，文件名 `*_test.go`，使用标准库 `testing`，无第三方断言框架。
- 目前测试覆盖 `licence`、`pushx` 与 `cachex` 包。在 `facade`、`utils`、`pushx`、`cachex` 中新增逻辑时，如逻辑可脱离外部服务（云存储、Redis、短信网关）运行，应补充同类单元测试；依赖外部凭据的能力不要做联网测试。

## 安全注意事项

- **凭据不入库**：OSS/COS 的 AccessKey、Secret 通过 `dto.*Config` 注入，Redis 凭据通过 `cachex.Config` 注入，短信/邮件推送凭据通过 `pushx.Config` 注入，全部在运行时由调用方传入，仓库中不得硬编码任何真实密钥。
- **路径穿越防护**：存储层的公开路径必须经过 `cleanDir` / `splitPublicPath` 清理（拒绝 `..` 越出存储根），文件名用 `path.Base` 去除目录成分。改动存储路径逻辑时不得绕过这两处校验。
- **许可证签名兼容**：见上文 licence 包约定——`Payload` 字段只许追加，签名算法固定 Ed25519，信封版本号 `EnvelopeVersion = 1`。
- **日志脱敏**：`utils.Mask` 提供手机号/邮箱/身份证等脱敏能力，输出含个人信息的内容到日志前应使用它。
- 本地存储默认落在 `public/storage/`、文件缓存落在 `./runtime/cache/`，这些是运行时产物目录，不要把用户文件提交进仓库。

## 其他说明

- `README.md` 引用的 `./document/README.md` 文档目录当前**不存在**于仓库中，属已知的文档缺失。
- `.gitignore` 已忽略 IDE 目录（`.idea`、`.vscode`）与编译产物；项目无 vendor 目录，依赖由 Go modules 管理。
