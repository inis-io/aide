# pushx/AGENTS.md

> 本文件面向 AI 编码代理，介绍 `pushx` 模块的结构、约定与常用命令。根目录通用规范（注释风格、`this` 接收者、`cast` 转换等）见 [`../AGENTS.md`](../AGENTS.md)，本文件只记录 pushx 专属约定；面向接入方的使用教程见 [`README.md`](README.md)。

## 模块定位

- 模块路径 `github.com/inis-io/aide/pushx`（见 `go.mod`），**嵌套独立模块**：不参与父模块 `go build ./...`，通过 `replace` 引用父模块（utils）与 `../dto`。
- 定位：**消息推送**——以接口模式封装短信 / 邮件验证码推送，注册表 + 链式调用，可扩展服务商。

## 目录结构

```
├── pushx.go       # Sender 接口、registry 注册表（变量初始化登记）、Driver 链式包装、Router 智能路由
├── config.go      # Config 配置结构、normConfig 默认值补齐、normMessage 消息体归一
├── facade.go      # 全局门面：Inst 控制器单例 + Push（链式智能路由）/ Email / SMS 全局实例
├── email.go       # gomail 邮件驱动（本地渲染）
├── aliyun.go      # 阿里云短信驱动（云端模板，Template 无效）
├── tencent.go     # 腾讯云短信驱动（云端模板，位置参数）
├── smsbao.go      # smsbao 短信驱动（本地渲染）
├── template.go    # 邮件 HTML 模板常量（TempEmailCode）
└── pushx_test.go  # 注册表、链式实例、配置/消息体归一化、模板渲染、云端参数组装、路由、热重载测试
```

## 核心约定

- **`Sender` 接口是唯一扩展点**：只含 `Send(Message) (*Response, error)` 一个方法。内置驱动在 `pushx.go` 的 `registry` 变量初始化时登记；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名覆盖。新增内置驱动：新建文件实现 `Sender`，并在 `registry` 登记一行。
- **驱动只发自己通道**：发送前校验目标类型（邮件驱动校验邮箱、短信驱动校验手机号），不合法立即报错；跨通道路由由 `Router` 统一负责（按 `utils.Identify.EmailOrPhone` 分发），驱动不做跨通道路由。
- **`Driver` 链式包装**（`Target` / `Code` / `Len` / `Expired` / `Subject` / `Template` / `Param` / `SetMessage` / `Send`），**值语义**：每次链式调用返回副本。`pushx.New("aliyun", config)` 创建独立实例。
- **模板与变量替换**：占位符格式 `${变量名}`，统一由 **`Message.Render`** 渲染（内置变量见注释清单）；未识别占位符保留原样。**只有本地渲染的驱动（email / smsbao）支持自定义模板；阿里云、腾讯云为云端控制台模板，`Template` 对其无效。** 新增本地渲染驱动时必须走 `Render`，不得自写替换 map。
- **自定义模板变量** `Message.Params`（链式 `Param(key, value)`）：本地渲染驱动在 `Render` 中以 `${键名}` 使用（覆盖顺序：内置变量 < Params < 驱动级 extra）；**阿里云**按键名合并进模板变量 JSON；**腾讯云**为位置参数，按数字键名（`"1"`、`"2"`...）升序组装为参数数组，提供后完全接管（不再自动附带验证码）。参数组装逻辑收敛在 `aliYunTemplateParams` / `tencentTemplateParams` 纯函数中。
- **配置与消息体自包含**：`pushx.Config`（含 `email` / `aliyun` / `tencent` / `smsbao` 四组内置驱动配置，外部扩展配置放 `Config.Options`，key 为驱动名）、`pushx.Message` / `pushx.Response`；`normConfig()` 补齐默认值（引擎未注册回退 `email` / `aliyun`），`normMessage()` 统一验证码长度（6）、有效期（5 分钟）并在验证码为空时自动生成，**链式入口由 `Driver.Send` 统一调用**。
- **全局门面**：控制器单例 `pushx.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `pushx.Push`（链式智能路由）、`pushx.Email` / `pushx.SMS`（当前驱动）。驱动初始化失败时全局位用 `senderError` 占位，`Send` 时返回原始初始化错误。

## 安全约定

- 短信/邮件推送凭据通过 `pushx.Config` 在运行时注入，仓库中不得硬编码任何真实凭据。

## 测试约定

- 测试与源码同包同目录，标准库 `testing`，用假驱动避免联网。
- 现有覆盖：注册表、链式实例、配置/消息体归一化、模板渲染、云端参数组装、智能路由、控制器热重载。

## 构建与测试命令

```bash
cd pushx && go build ./... && go vet ./... && go test ./...   # 本模块独立执行
```

## 版本发布

- 版本号递增默认 `+0.0.1`（patch 位）：除非用户主动要求跨版本（如 `+0.1.0`、`+1.0.0`），否则每次发 tag 一律在当前版本末位 `+0.0.1`，不自行跨版本。

## 文档

- 接入教程见 [`README.md`](README.md)；父模块总览见 [`../README.md`](../README.md)。
