# AGENTS.md - oauthx

> 面向 AI 编码代理的本模块约定。实现前先读 `README.md`（实现方案），与根目录 `AGENTS.md` 同构。

## 模块定位

- 嵌套独立模块 `github.com/inis-io/aide/oauthx`，不参与父模块 `go build ./...`。
- 依赖经 `replace` 引用本地路径：`../utils`（间接传递 `../dto`）。修改依赖时同步维护本目录 `go.mod` 的 `require` / `replace`。
- 不引入各平台官方 SDK，HTTP 直连（`utils.Http` + `utils.Json`），保持依赖精简。

## 核心约定

- **`Provider` 接口是唯一扩展点**：`AuthURL(ctx, state) (string, error)` + `User(ctx, code) (*User, error)` 两个方法。code→token→userinfo 全链路收敛在 `User` 内，差异不外泄；授权凭证内嵌在返回的 `User.Token` 字段，接口不单独返回 Token。
- 内置驱动在 `oauthx.go` 的 `registry` **变量初始化时登记**（不依赖文件 init 顺序）；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)`，同名覆盖先注册者。仓库内新增内置驱动：新建文件实现 `Provider`，并在 `registry` 登记一行。
- `Driver` 是 `Provider` 之上的链式包装（`Ctx` / `Session` / `Scopes` / `State` / `AuthURL` / `User(code, state)`），**值语义**，每次调用返回副本；**ctx 统一走链式 `Ctx` 注入，终结方法不带 ctx 参数**（与 storagex 风格一致）。**state 生成与 CSRF 校验统一收敛在 Driver 层**（`StateStore` 接口：`Put(state, fingerprint, expired)` / `Take(state, fingerprint)`，`Session(sid)` 注入会话指纹防登录 CSRF，未设置时降级为一次性+时效簿记；默认内存实现，可注入 cachex 适配器；oauthx 不直接依赖 cachex；state 熵源要求 crypto/rand ≥ 32 字符；Take 后端故障按未命中 fail-closed）。
- 配置自包含在包内：`oauthx.Config`（含 `qq` / `wechat` / `github` / `bilibili` 内置驱动配置，扩展驱动配置放 `Config.Options`）；`normConfig()` 补齐默认值（引擎未注册/为空时回退**第一个配置完整的已注册驱动**，全缺则 `driverError` 占位报错——不回退固定平台，OAuth 驱动缺凭据必然失败，固定回退只会延迟暴露配置错误）；AppID/AppSecret/RedirectURL 缺失在工厂阶段直接报错，不做静默降级；配置 Hash 用 `utils.Hash.Token(..., 16)`（MD5 前 16 位），不用 Sum32（32 位碰撞会导致热重载静默失效）。
- 全局门面与其他子包同构：控制器单例 `oauthx.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）+ 全局实例 `oauthx.OAuth` + `oauthx.Use(name)`。**门面按配置完整的平台全部初始化**（登录场景多平台并存，刻意偏离 pushx 的单引擎语义）；失败驱动用 `driverError` 占位。
- 平台坑位速查：QQ 的 token/openid 响应是表单或 JSONP（需剥离 `callback(...)`）且需先 `/oauth2.0/me` 换 openid；微信 token 响应直接带 openid/unionid，区分 open（扫码）/ mp（公众号）模式；GitHub 换 token 必须 `Accept: application/json`，请求 API 必须带 `User-Agent`，邮箱常需追加 `/user/emails`。
- 性别在各驱动内统一收敛为 `male` / `female` / `unknown`；原始 userinfo JSON 放 `User.Raw`。

## 测试约定

- 各平台端点用 `httptest.NewServer` mock，端点 BaseURL 留注入点。
- 必测：QQ 的 JSONP 剥离、微信 errcode、GitHub Accept 协商、state 一次性语义与过期、**state 指纹绑定（指纹不符必须失败）**、`normConfig` 默认值与**默认驱动回退策略**、门面热重载原子替换。
