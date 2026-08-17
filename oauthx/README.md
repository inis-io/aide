# oauthx - 聚合第三方登录 SDK

> 以接口模式封装 QQ、微信、GitHub、B 站等第三方 OAuth 登录，统一授权跳转与用户信息模型，注册表 + 链式调用，可扩展服务商。
>
> 本文档是**实现方案**（设计定稿后按此编码），与 `pushx` / `cachex` / `storagex` 等子包约定同构。

## 一、目标与范围

- **目标**：业务方一次接入，即可按名称切换/并存多个三方登录渠道；拿到统一的用户模型（OpenID/UnionID/昵称/头像）后自行完成本地账号绑定与签发会话（JWT 可用 `utils.Jwt`）。
- **范围内**：授权 URL 生成、state 防 CSRF 簿记、code 换 token、token 换用户信息、统一用户模型、多平台注册与热重载。
- **范围外**：本地账号体系、登录态签发、绑定/解绑逻辑（由业务方负责，SDK 只交付标准 `User`）。
- **不做的事**：不自动持久化 token 到数据库；不内置 HTTP 框架路由（只交付 URL 与解析函数，业务方挂到自己的 gin/echo/标准库路由上）。

## 二、模块定位

- 嵌套独立模块 `github.com/inis-io/aide/oauthx`，不参与父模块 `go build ./...`，经 `replace` 引用 `../utils` 与 `../dto`。
- 依赖尽量精简：网络请求用 `utils.Http`，JSON 用 `utils.Json`，不引入各平台官方 SDK（QQ/微信/GitHub 均为标准或近标准 OAuth2，HTTP 直连即可，避免重依赖）。
- **B 站说明（重要风险）**：B 站目前**没有面向普通开发者的公开三方登录 OAuth**（开放平台仅面向企业/服务商定向开放）。方案中 `bilibili` 作为**预留驱动位**：接口对齐、工厂登记，配置缺失或接口不可用时按 `driverError` 占位返回明确错误；若后续拿到开放权限再补实现。首批交付建议 **QQ + 微信 + GitHub**。

## 三、核心抽象

### 3.1 Provider 接口（唯一扩展点）

```go
// Provider - 三方登录驱动接口：平台只需实现两个方法即可接入
type Provider interface {
    // AuthURL - 生成授权跳转链接（state 已由 Driver 层生成并簿记，直接拼参即可）
    AuthURL(ctx context.Context, state string) (string, error)
    // User - 用回调 code 换取统一用户信息（内部完成 code→token→userinfo 全链路，
    // 授权凭证内嵌在返回的 User.Token 中，不单独暴露）
    User(ctx context.Context, code string) (*User, error)
}

// Factory - 驱动工厂：按配置构建驱动实例（传入的 Config 已归一化）
type Factory func(config Config) (Provider, error)
```

- 内置驱动在 `oauthx.go` 的 `registry` **变量初始化时登记**（不依赖文件 init 顺序）；外部驱动在自己包内 `init()` 中 `Register("名称", 工厂)` 注册，同名覆盖先注册者（可替换内置实现）。
- 为什么把 token 交换收敛进 `User`：QQ 需要先换 openid 再取资料、微信 token 响应里直接带 unionid，各平台链路差异大，暴露两步接口只会把差异泄漏给业务方。

### 3.2 统一用户模型与凭证

```go
// User - 统一三方用户模型
type User struct {
    Provider string `json:"provider"` // 平台标识（qq / wechat / github / bilibili）
    OpenID   string `json:"openid"`   // 平台内用户唯一 ID（GitHub 为数字 ID 的字符串形式）
    UnionID  string `json:"unionid"`  // 跨应用统一 ID（微信绑定开放平台后有；QQ 需 /oauth2.0/me 带 unionid=1 且应用已绑定开放平台；其余平台为空）
    Nickname string `json:"nickname"`
    Avatar   string `json:"avatar"`
    Gender   string `json:"gender"`   // male / female / unknown（各平台取值统一收敛）
    Email    string `json:"email"`    // 多数平台不给；GitHub 需单独请求 /user/emails
    Raw      string `json:"raw"`      // 平台原始 userinfo JSON，业务方需要更多字段时自行解析
    Token    *Token `json:"token"`    // 授权凭证内嵌返回，Provider 接口签名因此保持简洁（差异不外泄）
}

// Token - 授权凭证（内嵌于 User 一并返回，业务方可选持久化用于调平台 OpenAPI）
type Token struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token"` // QQ/微信有，GitHub（OAuth Apps）无
    ExpiresAt    int64  `json:"expires_at"`    // access_token 过期秒级时间戳，0 表示未知/不过期
    Scope        string `json:"scope"`
}
```

### 3.3 state 防 CSRF

```go
// StateStore - state 簿记接口：生成即写入、校验即销毁（一次性）
// fingerprint - 会话指纹（如 session id / 登录 Cookie 的哈希），用于把 state 绑定到发起者本人
type StateStore interface {
    Put(state string, fingerprint string, expired time.Duration) error
    // Take - 命中且指纹一致则删除并返回 true；未命中/已过期/指纹不符返回 false。
    // 后端故障按未命中处理（fail-closed），与 cachex Incr 返回 error 的约定同属安全取向
    Take(state string, fingerprint string) bool
}
```

- **会话绑定是防登录 CSRF 的关键**：只做全局一次性校验无法防御「攻击者拿自己的 state 诱导受害者完成回调」的攻击。业务方通过链式 `Driver.Session(sid)` 注入会话标识，Driver 层对其取哈希作为 fingerprint 写入与校验；**未调用 `Session` 时 fingerprint 为空串，仅保证一次性 + 时效，文档与注释需明示该降级语义**。
- state 熵源要求：`crypto/rand`，长度 ≥ 32 字符（防暴力枚举）。
- 默认实现：`sync.Map` + 惰性过期的内存版（单机够用）。
- 多实例部署时业务方注入适配器（例如包一层 `cachex`：`SetNX + TTL`），**oauthx 不直接依赖 cachex**，避免子模块互相耦合，适配器 10 行代码可写在业务仓库。
- `Driver.AuthURL()` 自动生成随机 state 并写入 Store；`Driver.User(code, state)` 先 `Take` 校验，失败直接返回 `ErrStateInvalid`，不发起任何平台请求。

## 四、Driver 链式设计（值语义）

```go
driver, err := oauthx.New("github", oauthx.Config{...})

// 1) 生成授权链接（自动簿记 state，Session 注入会话指纹防登录 CSRF）
url, err := driver.Ctx(ctx).Session(sid).Scopes("read:user", "user:email").AuthURL()

// 2) 回调处理：校验 state + 换用户（授权凭证在 user.Token 中）
user, err := driver.Ctx(ctx).Session(sid).User(code, state)

// 业务方也可用全局门面
user, err := oauthx.Use("qq").Ctx(ctx).Session(sid).User(code, state)
```

- `Driver` 为 `Provider` 之上的链式包装（`Ctx` / `Session` / `Scopes` / `State(...自定义state)` / `AuthURL` / `User`），**值语义**：每次链式调用返回副本，天然隔离上下文。**ctx 统一走链式 `Ctx` 注入，`AuthURL()` / `User(code, state)` 不带 ctx 参数**（与 storagex 风格一致）；Provider 接口方法仍显式接收 ctx，由 Driver 透传。
- **state 生成与校验统一收敛在 Driver 层**（含 `Session` 指纹绑定），Provider 只负责拼 URL 和换用户，新驱动接入即免费获得 CSRF 防护。
- `Driver.User` 返回 `(*User, error)`，授权凭证内嵌在 `User.Token`——与 Provider 接口签名严格一致，不存在第二返回值。
- `oauthx.New("qq", config)` 创建独立实例；`Driver.Provider()` 可取底层驱动做类型断言。

## 五、配置设计

```go
type Config struct {
    Engine  string                       `json:"engine"`  // 全局门面默认驱动名（未注册时的回退见下）
    QQ      ProviderConfig              `json:"qq"`
    WeChat  WeChatConfig                 `json:"wechat"`
    GitHub  ProviderConfig               `json:"github"`
    Bilibili ProviderConfig              `json:"bilibili"`
    Options map[string]map[string]any    `json:"options"` // 扩展驱动自定义配置（key 为驱动名）
    Hash    string                       `json:"hash"`
}

type ProviderConfig struct {
    AppID       string   `json:"app_id"`     // QQ 叫 AppID，GitHub 叫 Client ID，统一为 AppID
    AppSecret   string   `json:"app_secret"` // 同理统一
    RedirectURL string   `json:"redirect_url"`
    Scopes      []string `json:"scopes"`     // 缺省用平台默认
}

type WeChatConfig struct {
    ProviderConfig
    // Mode - open（开放平台网站扫码，有 unionid）/ mp（公众号网页授权）
    Mode string `json:"mode" default:"open"`
}
```

- `normConfig()` 补齐默认值：各平台 scope 缺省（QQ `get_user_info`、微信 open `snsapi_login` / mp `snsapi_userinfo`、GitHub `read:user`）；AppID/AppSecret/RedirectURL 缺失时工厂阶段直接报错，**不做静默降级**（与 storagex 云驱动约定一致）。
- **默认驱动回退策略**：`Engine` 未注册或为空时，回退到**第一个配置完整的已注册驱动**（按内置驱动登记顺序）；一个配置完整的驱动都没有时，全局默认位用 `driverError` 占位报错。不回退到某个固定平台——pushx 回退 email、cachex 回退 file 都是"零配置本地可用"的兜底，而任何 OAuth 平台缺凭据都必然失败，固定回退只会把配置错误延迟到调用时才暴露。
- `Hash` 缺省用 `utils.Hash.Token(utils.Json.Encode(config), 16)`（MD5 前 16 位，64 位）计算，支撑热重载；不用 `Sum32`——cachex 键哈希已因 32 位碰撞风险升级，配置 Hash 碰撞会导致热重载静默失效，更难排查。

## 六、全局门面

与其他子包同构：

- 控制器单例 `oauthx.Inst`（`Init` / `ReloadIfChanged`，`sync.RWMutex` 保护）。
- 全局活动实例：`oauthx.OAuth`（`Engine` 指定的默认 Driver，未注册时按上节回退策略选定）+ `oauthx.Use("qq")` 按名取任意已初始化驱动（门面按 Config 中出现且配置完整的平台**全部初始化**，不是单选——登录场景天然多平台并存，这点与 pushx 的单引擎语义不同，是刻意偏离）。
- 驱动初始化失败时对应位置用 `driverError` 占位，`AuthURL` / `User` 返回原始初始化错误。
- 包 `init()` 时以默认配置初始化。

## 七、各平台接入要点

### QQ 互联（`qq.go`）
1. 授权：`GET https://graph.qq.com/oauth2.0/authorize?response_type=code&client_id=&redirect_uri=&state=`
2. 换 token：`GET /oauth2.0/access_token`（响应是 `application/x-www-form-urlencoded`，**不是 JSON**，用 `utils.Parse` 按表单解析；错误时返回 JSONP 包裹，需剥离 `callback(...)`）
3. **先换 openid**：`GET /oauth2.0/me?access_token=`（JSONP 包裹，剥离后取 `openid`）；应用已绑定开放平台时带 `unionid=1` 参数可直接拿到 `unionid`，驱动应支持（与微信 UnionID 字段对齐，避免模型不对称）
4. 取资料：`GET /user/get_user_info?access_token=&oauth_consumer_key={appid}&openid=`（`ret != 0` 视为失败）
5. 头像取 `figureurl_qq_2`（100px），性别 `男/女` 收敛为 `male/female`。

### 微信开放平台（`wechat.go`）
- `Mode=open`（网站扫码）：授权走 `open.weixin.qq.com/connect/qrconnect`，scope `snsapi_login`；`Mode=mp`（公众号）：授权走 `open.weixin.qq.com/connect/oauth2/authorize`，scope `snsapi_userinfo` 且需 `#wechat_redirect` 后缀。
- 换 token：`GET api.weixin.qq.com/sns/oauth2/access_token`，**响应直接带 `openid` 与 `unionid`**，无需二次请求。注意 mp 模式的 `unionid` 仅在公众号已绑定微信开放平台后返回，否则为空（文档注明，避免业务方误判为 Bug）。
- 取资料：`GET /sns/userinfo`；错误响应含 `errcode != 0`。
- 性别 `1/2/0` 收敛为 `male/female/unknown`。

### GitHub（`github.go`）
1. 授权：`GET https://github.com/login/oauth/authorize?client_id=&redirect_uri=&state=&scope=`
2. 换 token：`POST https://github.com/login/oauth/access_token`，**必须带 `Accept: application/json`**，否则返回表单格式。
3. 取资料：`GET https://api.github.com/user`（`Authorization: Bearer <token>` + 固定 `User-Agent` 头，GitHub 强制要求）。
4. 邮箱：`/user` 的 `email` 常为 null（用户设私有），scope 含 `user:email` 时再请求 `GET /user/emails` 取 `primary && verified` 的那条。
5. 无 refresh_token（OAuth Apps），`OpenID` 用数字 `id` 的字符串形式。

### B 站（`bilibili.go`，预留）
- 保留驱动文件与注册行，工厂校验配置后直接返回「暂未开放」错误占位；文档注明获取开放权限后的接入入口（开放平台申请 → 按同一 `Provider` 接口实现）。

## 八、文件结构

```
oauthx/
├── go.mod          # 独立模块（已建）
├── oauthx.go       # Provider 接口 + registry + Register + Names + New + Driver 链式 + driverError 占位
├── config.go       # Config / ProviderConfig / WeChatConfig + normConfig
├── user.go         # User / Token + 性别收敛等辅助函数
├── state.go        # StateStore 接口 + 内存默认实现
├── facade.go       # Inst 控制器 + OAuth 全局实例 + Use(name)
├── qq.go           # QQ 互联驱动
├── wechat.go       # 微信开放平台/公众号驱动
├── github.go       # GitHub 驱动
├── bilibili.go     # B 站预留驱动（错误占位）
├── oauthx_test.go  # httptest mock 各平台端点 + 链式/state/归一化测试
├── AGENTS.md       # 子模块约定（与根 AGENTS.md 同构）
└── README.md       # 本文档
```

## 九、错误处理与测试

- **错误**：平台侧错误（QQ `ret`、微信 `errcode`、GitHub `error` 字段）统一包装为 `oauthx: 平台[qq] 换取用户失败 - <原始信息>`；网络层错误直接透传；state 校验失败返回独立的 `ErrStateInvalid`，方便业务方区分「用户取消了授权」与「CSRF 攻击」。
- **测试**：全部走 `httptest.NewServer` mock 各平台端点（QQ 的 JSONP、微信的 errcode、GitHub 的 Accept 协商是重点用例）；state 簿记测一次性语义、过期淘汰与**指纹绑定（指纹不符必须 Take 失败）**；`normConfig` 测默认值补齐与**默认驱动回退策略**（回退到第一个配置完整驱动 / 全缺时占位报错）；门面测热重载原子替换。配置里的端点 BaseURL 留可注入字段（`Options` 或包内私有变量），测试时指向 mock server。

## 十、里程碑

| 阶段 | 内容 | 产出 |
|---|---|---|
| M1 | 骨架：接口/注册表/Driver/state/配置/门面 + GitHub 驱动 | 可用最小闭环（GitHub 登录跑通） |
| M2 | QQ + 微信（open/mp 双模式） | 国内双端可用 |
| M3 | B 站预留位 + 测试补齐 + AGENTS.md/README 定稿 | 完整交付 |

> 命名说明：沿用 `pushx`/`cachex`/`storagex`/`taskx` 的 `x` 后缀惯例取名 `oauthx`；若你更想要 `authx` 或 `loginx`，改名只影响目录与 module 行，方案其余部分不变。
