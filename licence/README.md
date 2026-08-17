# Licen Hub 授权平台 Go SDK

> 包路径：`github.com/inis-io/aide/licence`（Licen Hub 授权平台官方 Go SDK，仅支持 Go，要求 Go 1.26+）。
> 本文档分两部分：**第 1~10 节 快速上手教程**（材料清单、快速开始、FAQ），**第 11~22 节 接口级参考手册**（逐接口列出签名、参数、返回与语义）。
> 所有接口均可脱机编译测试；运行面/管理面接口依赖平台服务，对接前请先与平台方确认 `ServerURL` 与公钥材料。

## 目录

### 上手指南（第 1~10 节）

1. [开始前：你需要从平台方拿到什么](#1-开始前你需要从平台方拿到什么)
2. [安装](#2-安装)
3. [快速开始：许可证激活与授权校验（运行面）](#3-快速开始许可证激活与授权校验运行面)
   - [装机自动申领与激活码兑换（Provision / Redeem）](#31-装机自动申领与激活码兑换provision--redeem)
4. [授权状态与降级策略](#4-授权状态与降级策略)
   - [多机席位（一 Client 一机一席）](#41-多机席位一-client-一机一席)
5. [在线更新（更新代理/自更新程序用）](#5-在线更新更新代理自更新程序用)
6. [SaaS 多租户（服务商集成）](#6-saas-多租户服务商集成)
7. [管理面（商户 CI/运维自动化，勿随交付项目分发）](#7-管理面商户-ci运维自动化勿随交付项目分发)
8. [常见问题（FAQ）](#8-常见问题faq)
9. [排错速查](#9-排错速查)
10. [一图流（集成清单）](#10-一图流集成清单)

### 接口参考（第 11~22 节）

11. [包结构与分层](#11-包结构与分层)
12. [常量与状态码](#12-常量与状态码)
13. [纯函数层：签名与信封](#13-纯函数层签名与信封)
14. [版本范围判定](#14-版本范围判定)
15. [实例指纹](#15-实例指纹)
16. [安全存储接口](#16-安全存储接口)
17. [运行面客户端 Client](#17-运行面客户端-client)
   - [装机自动申领与激活码兑换（包级函数）](#174-装机自动申领与激活码兑换包级函数)
18. [在线更新模块](#18-在线更新模块)
19. [SaaS 多租户接口](#19-saas-多租户接口)
20. [管理面客户端 AdminClient](#20-管理面客户端-adminclient)
21. [接口契约与兼容性约定](#21-接口契约与兼容性约定)
22. [附：包内文件与能力映射](#22-附包内文件与能力映射)

---

# 第一部分 · 快速上手教程

## 1. 开始前：你需要从平台方拿到什么

| 材料 | 是什么 | 从哪里获得 |
|---|---|---|
| **平台地址 `ServerURL`** | 授权平台的访问地址 | 平台方提供，如 `https://licen-hub.inis.cn` |
| **许可证编号 `licenseNo`** | 你的部署实例对应的许可证，格式 `LIC-{年}-XXXXXX` | 平台管理端「我的许可证」页签发后可见（先完成注册 → 资格审核 → 建项目/实例 → 授权申请 → 平台审批签发） |
| **指纹盐 `Salt`** | 实例指纹的盐值 | 你在平台登记部署实例时自己定的盐；二者必须一致 |
| **验签公钥 `PublicKeys`** | 平台 license-key 的 Ed25519 公钥（hex），用于验证许可证信封是真的 | 管理端登录后**按项目**导出 `GET /api/licenses/public-key?projectId=X`，返回全版本 `keys[]`（含历史轮换密钥），逐字内置到你的程序里 |
| **release 公钥 `ReleasePublicKeys`** | release-key 公钥（hex），验证更新清单与发布物 | 同上，`GET /api/signing-keys/public?purpose=release`；**只用在线更新功能时才需要** |
| **管理面账号密码** | 平台后台账号 | **仅 CI/运维自动化才需要**（查项目、申请授权、导公钥等）；禁止随交付项目分发 |

安全约定（SDK 已强制，了解一下即可）：

- 你**永远拿不到**平台的签名私钥，也不需要——签发只在平台进行；
- 首次启动时 SDK 会在本机生成一对 Ed25519 密钥（请求签名用），私钥加密存本机、不出机器、不写日志；
- 激活令牌（`activationToken`）同样加密存储，丢失时 SDK 自动重新激活换取。

## 2. 安装

```bash
go get github.com/inis-io/aide/licence
```

```go
import "github.com/inis-io/aide/licence"
```

### HTTP / gRPC 传输选择

运行面 `Client` 与管理面 `AdminClient` 的公开业务方法同时支持 HTTP 和原生 gRPC。零值继续使用
HTTP，现有调用方无需改动；需要 gRPC 时只切换配置，不要改业务调用：

```go
client, err := licence.New(licence.Options{
	ServerURL: "https://licen-hub.inis.cn",
	Transport: licence.TransportGRPC,
	// LicenseNo、Salt、PublicKeys 等照常填写
})

admin, err := licence.NewAdmin(licence.AdminOptions{
	ServerURL: "https://licen-hub.inis.cn",
	Transport: licence.TransportGRPC,
	Account: "ops", Password: "secret",
})
defer admin.Close()
```

生产环境使用 `https://`（TLS + HTTP/2）。本机或受控内网的 h2c 必须同时使用 `http://` 和
`GRPCOptions{AllowInsecure: true}` 显式开启。SDK 不做 HTTP/gRPC 自动回退，以免写操作被执行两次；
两类客户端使用完毕后应调用幂等的 `Close()`。发布物上传和带文件验签在 gRPC 下使用
client-streaming，`io.Reader` 不会被 SDK 全量读入内存。

## 3. 快速开始：许可证激活与授权校验（运行面）

嵌入交付项目的就是这部分。**5 行接入**：

```go
package main

import (
	"context"
	"fmt"

	"github.com/inis-io/aide/licence"
)

func main() {

	client, err := licence.New(licence.Options{
		ServerURL: "https://licen-hub.inis.cn",     // 平台地址（必填）
		LicenseNo: "LIC-2026-000123",               // 许可证编号（必填）
		Salt:      "my-project-salt",               // 指纹盐，与实例登记一致（必填）
		PublicKeys: map[string]string{              // 验签公钥表（必填：项目全版本并存，从平台按项目导出）
			"license-key-INIS-202608-1a2b3c": "79b5562e8fe6…（从平台导出的公钥 hex）",
		},
		ReleasePublicKeys: map[string]string{       // release 公钥（用在线更新才填）
			"release-key-2026-01": "…",
		},
		StorageDir: "/var/lib/myapp/licence",       // 状态存储目录（默认 ./runtime/licence）
		Version:    "2.3.1",                        // 当前项目版本（随校验上送）
	})
	if err != nil {
		panic(err)
	}

	// Start：首次自动激活（生成密钥对 + 注册公钥 + 换取令牌），
	// 之后进入后台滑动刷新循环；有缓存时平台不可达也能降级启动
	if err = client.Start(context.Background()); err != nil {
		panic(err)
	}
	defer client.Close() // 停止刷新并释放 HTTP/gRPC 连接

	// 业务闸门：功能 / 额度 / 版本范围
	if client.HasFeature("report.advanced") {
		// 开放高级报表
	}
	maxUsers, exist := client.GetLimit("max_users")
	if !client.CheckVersion("2.3.1") {
		// 当前版本不在授权范围
	}

	// 状态与用量上报
	fmt.Println(client.Status()) // VALID / EXPIRING / GRACE / ...
	client.ReportUsage(map[string]int64{"max_users": 42})
	_ = maxUsers
	_ = exist
}
```

`Start` 之后你什么都不用管：

- 后台每 12~24 小时自动 `validate` 滑动续期（默认 12h，可配 `RefreshInterval`）；
- 每个请求自动携带客户端私钥签名（时间戳 + nonce 防重放），开发者零感知；
- 断网/平台故障自动按本地缓存信封降级运行（宽限内 `GRACE`，耗尽 `EXPIRED`）；
- 许可证被续期/调整权益后，新信封自动下发、验签、替换缓存。

### 3.1 装机自动申领与激活码兑换（Provision / Redeem）

面向**私有化/交付装机**场景：客户机器上只跑你的程序，程序启动时调用包级函数自动拿证，**零人工/零管理端操作**。两种获取方式共用同一套参数与返回：

| 函数 | 适用 | 平台侧 |
|---|---|---|
| `Provision` | 免费版自动申领（装机即激活） | 管理员预置 `inis` 项目 + 免费自动授权模板；客户机器免密申领 |
| `Redeem` | 商业版激活码兑换 | 管理员预置商用模板并向客户发放激活码；客户机器输入码即激活 |

```go
result, err := licence.Provision(ctx, licence.ProvisionOptions{
    ServerURL:      "https://licen-hub.inis.cn", // 平台地址（官方二进制内写死）
    TemplateCode:   "INIS-AUTO",                 // 免费模板编码（官方二进制内写死）
    ProvisionToken: "tmpl-token",                // 签发令牌（官方二进制内写死，服务端常数时间比对）
})
if err != nil {
    // 业务拒绝是 *licence.ProvisionError，errors.As 后按 Status 降级
    var denied *licence.ProvisionError
    if errors.As(err, &denied) {
        switch denied.Status {
        case "RATE_LIMITED":   /* 稍后重试 */
        case "QUOTA_EXCEEDED": /* 今日发放额用完 */
        }
    }
    return err
}

// 用申领结果直接构造运行面客户端，并自动激活
client, err := licence.New(licence.Options{
    ServerURL:  "https://licen-hub.inis.cn",
    LicenseNo:  result.LicenseNo,
    Salt:       result.Salt,
    PublicKeys: map[string]string{"license-key-INIS-202608-1a2b3c": "<平台按项目导出的公钥 hex>"},
})
if err = client.Start(ctx); err != nil {
    panic(err)
}
```

**安装唯一标识（install.sn）**：申领/兑换按机器持久化一个 UUID v4 到 `StorageDir/install.sn`（默认 `./runtime/licence/install.sn`）。同机重装/换证复用一个 SN，平台据此幂等复用或有界续签；SDK 首次使用自动生成，也可用 `ProvisionOptions.InstallSN` 显式覆盖。该文件独立于按 licenseNo 命名的状态文件，跨许可证生命周期稳定。

> **商业版与免费版同一套后端能力**：`RedeemOptions` 内嵌 `ProvisionOptions`，除激活码外语义完全一致；免费自动激活额度不足时引导客户买码，程序内零改动切换。

## 4. 授权状态与降级策略

`client.Status()` 的可能取值与建议动作：

| 状态 | 含义 | 建议动作 |
|---|---|---|
| `VALID` | 正常 | 正常运行 |
| `EXPIRING` | 30 天内到期 | 提醒客户续期 |
| `GRACE` | 宽限期 | 降级运行（如禁增改保查询），尽快恢复网络/续期 |
| `CLOCK_TAMPERED` | 疑似回拨系统时间 | 只告警，不停服务 |
| `EXPIRED` | 到期/凭证失效 | 停止授权功能；SDK 会自动尝试重新激活 |
| `REVOKED` / `SUSPENDED` | 已吊销/暂停 | 立即停止，提示联系平台方 |
| `SEAT_LIMIT_EXCEEDED` | 多机席位已满（仅激活时返回） | 停止授权功能；SDK **不自动重试**，释放席位或扩容后显式 `Reactivate`，见 §4.1 |
| `SEAT_RELEASED` | 管理员释放了本机席位 | 停止授权功能；SDK **不自动重激活**（保留本机凭证），确认容量后显式 `Reactivate`/`Reset`，见 §4.1 |
| `INSTANCE_MISMATCH` | 机器指纹不匹配 | 走换机流程，见 §8 常见问题 |
| `VERSION_NOT_ALLOWED` | 版本不在授权范围 | 提示升级/降级 |
| `FEATURE_NOT_ALLOWED` / `LIMIT_EXCEEDED` | 功能未授权/额度超限 | 关闭入口/提示扩容 |
| `NOT_FOUND` | 许可证或请求签名无效 | 核对 licenseNo 与公钥配置 |

状态变化回调（接监控告警）：

```go
licence.Options{
	// ...
	OnStatusChange: func(oldStatus, newStatus string) {
		// 写日志、推告警、切换降级策略
	},
}
```

### 4.1 多机席位（一 Client 一机一席）

开发/预发许可证可配置「多机席位」（载荷 `bindingPolicy: "seats"` + `seatLimit: N`；单机许可证为 `single` + 1，历史信封缺省也按此解释）：平台为每台机器登记一条席位记录，**一个 Client 实例独占一机一席**——同机反复 `Start` 复用原席位，不同机器各占一席，占满后新机激活被拒。

- **机器名称**：`Options.DeviceName` 仅供平台席位列表展示，缺省取 `os.Hostname()`（去空格、限长 128）；`client.DeviceName()` 返回实际生效值，`client.SeatNo()` 返回本机席位编号（排障用，激活后才有值）。
- **指纹即席位身份键**：同机指纹必须长期稳定、异机必须不同。克隆镜像/容器等因子雷同场景必须注入 `Options.Fingerprint` 或 `Options.Provider` 区分，否则会出现「同机抢多席」或「异机挤一席」。

席位状态码处置语义（**与 `EXPIRED` 的自动引导不同，SDK 不做任何自动恢复**）：

| 状态码 | 触发时机 | SDK 行为 | 你的动作 |
|---|---|---|---|
| `SEAT_LIMIT_EXCEEDED` | 激活时席位已满（仅 activate 返回） | 激活失败并置状态，后台循环停摆，**不自动重试抢席** | 平台管理端释放闲置席位或联系平台方扩容，然后显式 `Reactivate` |
| `SEAT_RELEASED` | 管理员在平台释放了本机席位（validate/current 返回） | 停止授权功能并触发 `OnStatusChange`；清除信封与派生缓存（平台配置快照、配置同步水位、租户信封缓存）；**保留** activation token、客户端私钥、席位号与状态文件，**不自动 `Reactivate`**，重启后仍稳定停在该状态 | 确认容量后显式 `Reactivate`（重新绑席）或 `Reset`（清本机态后重新激活） |

人工恢复路径的分工：

- `Reactivate(ctx)`：**通知平台**，生成新客户端密钥对重新激活绑席（换机/令牌丢失/EXPIRED 引导/席位恢复共用）；
- `Reset()`：**不通知平台**（平台侧席位不变），只清本机运行状态；下次 `Start` 以同一指纹重新激活，由服务端复用或重新占用原席位。

> 已知时滞语义：`SEAT_RELEASED` 经 updates/saas/events 通道（`CheckUpdate`/`TenantSync`/`PlatformConfigSync`/`EventSubscriber.Poll`）到达时，只 fail-closed 返回错误（`实例许可证非放行态：SEAT_RELEASED` / `许可证非放行态：SEAT_RELEASED`），**不回写 `client.Status()`**；授权状态收敛以 validate 循环为准（最长一个刷新周期 `RefreshInterval`）。

平台侧席位运维走 AdminClient（商户 CI/运维自动化，见 §7 与 §20.5）：

```go
page, _ := admin.Licenses.Seats(ctx, &licence.LicenseSeatFindParams{LicenseId: 9, Status: "occupied"})
seat, _ := admin.Licenses.SeatTake(ctx, 3)
_, _ = admin.Licenses.ReleaseSeat(ctx, licence.LicenseSeatReleaseInput{Id: 3, Reason: "临时腾出席位"})
```

## 5. 在线更新（更新代理/自更新程序用）

```go
// 1. 检查更新（服务端判定授权状态、升级权 upgradeUntil、灰度规则）
info, err := client.CheckUpdate(ctx, "linux/amd64")
if err != nil || !info.Available {
	return // 无更新或平台不可达
}
manifest := info.Manifest // 已完成 release-key 验签 + 发布物签名复核

// 2. 下载发布物（大小 + SHA-256 校验，全部通过才落盘）
err = client.DownloadArtifact(ctx, manifest, manifest.Payload.Artifacts[0], "/tmp/app-2.4.0.tar.gz")

// 3. 上报升级轨迹（创建 → 推进 → 日志）
recordNo, _ := client.ReportUpgrade(ctx, licence.UpgradeReport{
	FromVersion: "2.3.1", TargetVersion: "2.4.0",
	Status: licence.UpgradeDownloading, Message: "开始下载",
})
// ... 安装完成后：
client.ReportUpgrade(ctx, licence.UpgradeReport{
	RecordNo: recordNo, TargetVersion: "2.4.0",
	Status: licence.UpgradeSuccess, Message: "升级完成",
})
client.ReportUpgradeLog(ctx, recordNo, []string{"预检通过", "安装完成"})
```

离线更新包（隔离网络）：把平台生成的 `manifest.json` 读出来验签：

```go
raw, _ := os.ReadFile("manifest.json")
manifest, err := client.VerifyManifest(raw) // 验签通过才允许继续安装
```

升级执行编排（预检/备份/停服/迁移/回滚）由你的更新代理负责，SDK 只提供检查、下载、验签与上报。

## 6. SaaS 多租户（服务商集成）

实例许可证激活是总闸（非放行租户接口全部不可用）。在此之上：

```go
// 启动时 + 每小时同步一次（增量水位线）
syncTime, manifests, err := client.TenantSync(ctx, 0) // 之后传上次返回的 syncTime

// 租户用户登录/访问受控功能时实时校验
status, err := client.TenantValidate(ctx, "tenant-a", licence.TenantValidateOptions{
	Feature: "report.advanced",
	Usage:   map[string]int64{"max_users": 12},
})

// 平台不可达时的本地降级判定（缓存信封 + 本地时间判定）
status = client.TenantStatus("tenant-a")
ok := client.TenantFeature("tenant-a", "report.advanced")
```

fail-open / fail-closed 由你按业务决策（建议写进你自己的集成文档）。

### 6.1 回调通知与配置同步（事件订阅 · 事件推送）

先在平台的「项目管理 → 部署实例」为每个实例登记 `notify_url`（建议 `POST /licence/callback`），然后在项目中挂载同一个回调接收器：

```go
handler := licence.NewCallbackHandler(licence.CallbackOptions{
	PublicKeys: publicKeys, // 与运行面 Client 使用同一组 license-key 公钥
})
handler.OnEvent("saas.*", func(ctx context.Context, event *licence.CallbackEvent) (licence.Ack, error) {
	go func() { _, _, _ = client.TenantSync(context.Background(), 0) }()
	return licence.AckSuccess, nil
})
handler.OnEvent("platform.config.*", func(ctx context.Context, event *licence.CallbackEvent) (licence.Ack, error) {
	go func() { _, _ = client.PlatformConfigSync(context.Background()) }()
	return licence.AckSuccess, nil
})
http.Handle("/licence/callback", handler)
```

回调会先做原文验签、正负 5 分钟时间窗检查、nonce 防重放和 `deliveryNo` 幂等去重。业务回调应快速返回：`success`/`ok` 表示成功，`ignored` 表示无需处理，`retry` 要求平台重试，`rejected` 表示永久拒绝。未注册的事件自动返回 `ignored`。

**没有 `notify_url`（或想内网直连）也能收到事件**：用 `Client.Subscribe` 主动订阅项目事件（HTTP 长轮询 / gRPC 服务端流，双传输任选）。订阅信封由平台现场重签，复用与回调完全相同的验签与去重管线；收到 `saas.tenant.created` 等事件后同样调 `TenantSync` / `PlatformConfigSync` 拉全量：

```go
sub := client.Subscribe(licence.CallbackOptions{}).
    OnEvent("saas.*", func(ctx context.Context, event *licence.CallbackEvent) (licence.Ack, error) {
        var data struct {
            TenantNo string `json:"tenantNo"`
            PlanCode string `json:"planCode"`
        }
        _ = event.MustData(&data)
        _, _, _ = client.TenantSync(context.Background(), 0) // 推送即失效信号：收到后拉完整租户信封
        return licence.AckSuccess, nil
    }).
    OnEvent(licence.EventPlatformConfigUpdated, func(ctx context.Context, event *licence.CallbackEvent) (licence.Ack, error) {
        _, _ = client.PlatformConfigSync(context.Background())
        return licence.AckSuccess, nil
    })
sub.SetWatermark(lastEventId) // 可选：跳过已消费的历史事件
n, err := sub.Poll(ctx)       // 一轮长轮询拉取并分发，返回条数；或 _ = sub.Run(ctx) 后台循环
```

订阅器按 `eventId` 单调推进水位，`deliveryNo` 稳定为 `SUB-{eventNo}` 跨轮去重；非放行态统一返回 `许可证非放行态：{status}`。接口明细见 §19.6。

回调只是「变更信号」，全量事实仍通过拉取收敛。配置同步后可直接读本地快照：

```go
_, err := client.PlatformConfigSync(ctx)
item, ok := client.PlatformConfig("platform.some.key") // 返回条目副本（含最终值/选项/规则）
item = client.PlatformConfigMust("platform.some.key")   // 不存在时 panic
```

配置快照与许可证状态一起经 `Store` 加密持久化；建议除了回调驱动的即时同步，再做周期性 `PlatformConfigSync` 兜底。

## 7. 管理面（商户 CI/运维自动化，勿随交付项目分发）

```go
admin, err := licence.NewAdmin(licence.AdminOptions{
	ServerURL: "https://licen-hub.inis.cn",
	Account:   "your-account",
	Password:  "your-password", // 开启 2FA 时另传 TOTP
})
// 首次请求自动登录（POST /api/comm/sign-in），401 自动重登；token 只存内存
// CI 复用已有会话：admin.SetToken(licence.Token{Value: jwt, Expired: ms})

projects, _ := admin.Projects.Find(ctx, &licence.ProjectFindParams{Page: 1})
artifact, _ := admin.Artifacts.Upload(ctx, licence.ArtifactUploadInput{VersionId: 12}, "app.tar.gz", file)
pub, _ := admin.SigningKeys.Public(ctx, "license", "", projectId)   // 按项目导出验签公钥（license 用途必填 projectId）
verify, _ := admin.Artifacts.VerifyWithFile(ctx, 3, "app.tar.gz", file) // 服务端代验
```

资源清单：`Qualification`（资格申请/审批）、`Projects`、`Instances`、`Licenses`（申请/审批/续期/暂停/吊销/重签/载荷/公钥/激活记录）、`SigningKeys`、`Artifacts`、`Versions`（含发布/归档）、`Modules`（项目功能模块）、`SaasMenus`（菜单清单草稿/发布/归档）、`SaasFeatures`（功能字典登记/禁用/删除）、`SaasPlans`（套餐定义/状态流转）、`SaasTenants`（租户开通/变更/状态机/重签/批量续期/用量/留痕）、`SaasReview`（租户申请单审批）。

注意：管理面账密通过所选传输上送，生产环境无论 HTTP 还是 gRPC 都**必须使用 TLS**。

## 8. 常见问题（FAQ）

**Q：换服务器/换机怎么办？**
当前平台是「重新激活即换绑」：在新机器上直接 `Start`，无绑定则自动建立；若平台已绑定旧机器指纹会返回 `INSTANCE_MISMATCH`，联系平台方解绑后调 `client.Reactivate(ctx)`（会生成新密钥对重新注册）。

**Q：token 丢了怎么办？**
不用管。本地凭证失效后服务端回 `EXPIRED`，SDK 自动重新激活换新 token。

**Q：交付环境没外网？**
已激活的项目靠本地加密缓存的信封运行：宽限期（许可证 `graceDays`）内正常/降级，耗尽后 `EXPIRED`。完全隔离的环境用离线更新包（§5）。

**Q：客户机器时间不准？**
签名时间戳用服务端校时后的时间（响应 `serverTime` 自动校正），±5 分钟时间窗内都合法；回拨系统时间只会告警（`CLOCK_TAMPERED`），不停服务。

**Q：Docker/云主机指纹不稳定？**
注入你自己的稳定指纹源：

```go
licence.Options{
	Provider: func() (string, error) { return myStableMachineID(), nil },
	// 或直接给算好的哈希：Fingerprint: "64位hex",
}
```

**Q：状态存哪？安全吗？**
`StorageDir`（默认 `./runtime/licence`）下的 `licence-<licenseNo>.state`，AES-256-GCM 加密（密钥派生自盐+指纹），权限 0600。token、私钥、信封都在里面。SDK 只有在拿到并验签完整许可证信封后才会落盘；首次激活被拒绝不保存半成品状态，升级时遇到旧版无信封状态会自动清理并重新激活。想接系统密钥库（DPAPI/Keychain/keyring）就实现 `licence.Store` 接口注入。

**Q：平台轮换密钥了怎么办？**
`PublicKeys`/`ReleasePublicKeys` 是 map，把新旧公钥都放进去即可平滑过渡（SDK 按载荷 `keyVersion` 自动选钥）。轮换后重新从项目接入配置导出 `keys[]` 全量同步即可，**历史版本务必保留**——旧缓存信封离线降级恢复时仍按历史 keyVersion 验签。

## 9. 排错速查

| 现象 | 先查什么 |
|---|---|
| `Start` 报「许可证或实例信息无效」 | `licenseNo` 是否正确；许可证是否已签发且未吊销 |
| `Start` 报「未内置 keyVersion=… 的验签公钥」 | 公钥 map 的 key 必须等于平台的 `keyVersion`（形如 `license-key-INIS-202608-1a2b3c`，从项目导出 `keys[]` 逐字复制，勿拼接猜测） |
| `信封验签失败` | 公钥是否配对、是否最新；找平台方重新导出 |
| 一直 `GRACE` | 交付机网络到平台不通，或许可证已过 `validUntil` |
| `INSTANCE_MISMATCH` | 换了机器/指纹源变了；检查 `Salt` 是否与实例登记一致 |
| 管理面 401 | 账号密码/2FA；或 token 过期（SDK 会自动重登一次） |

## 10. 一图流（集成清单）

1. 平台方开通账号 → 资格审核通过
2. 建项目 → 登记实例（记下你的指纹盐）→ 申请授权 → 平台签发
3. 拿到 `licenseNo` + 按项目导出 `PublicKeys`（`GET /api/licenses/public-key?projectId=X` 全版本 `keys[]`；用更新功能再导出 `ReleasePublicKeys`）
4. `licence.New` + `Start` → 用 `HasFeature/GetLimit/CheckVersion` 做业务闸门
5.（可选）`CheckUpdate`/`DownloadArtifact`/`ReportUpgrade` 接在线更新
6.（可选 SaaS）`TenantSync`/`TenantValidate` 接多租户
7.（可选 CI）`NewAdmin` 做运维自动化

---

# 第二部分 · SDK 接口参考

## 11. 包结构与分层

| 分层 | 入口 | 使用方 |
|---|---|---|
| 纯函数层 | `licence.Licence` 链式工具、`Envelope`/`Payload` 结构、`VersionInRange`、`FingerprintHash`、`Store` 接口 | 需要自行签发/验签/解析信封的服务端或测试 |
| 运行面客户端 | `licence.New(Options)` → `*Client` | **交付项目嵌入**：激活、授权闸门、在线更新、SaaS 租户 |
| 管理面客户端 | `licence.NewAdmin(AdminOptions)` → `*AdminClient` | 商户运维系统 / CI 自动化（登录态接口，勿随交付项目分发） |

两类客户端的协议完全不同，互不通用：

- **运行面**：Ed25519 信封 + 逐请求客户端签名（`X-License-*` 头），无登录概念；
- **管理面**：账密登录换取 JWT（`Authorization: Bearer`），响应统一 `{code, msg, data}` 信封。

## 12. 常量与状态码

### 12.1 算法与信封版本（`envelope.go`）

| 常量 | 值 | 说明 |
|---|---|---|
| `Algorithm` | `"Ed25519"` | 签名算法标识，所有信封固定 |
| `EnvelopeVersion` | `1` | 信封结构版本，所有信封固定 |

### 12.2 运行面状态码（`status.go`）

放行状态（业务闸门放行）：`VALID` / `EXPIRING` / `GRACE` / `CLOCK_TAMPERED`。

| 常量 | 值 | 含义 |
|---|---|---|
| `StatusValid` | `VALID` | 授权正常 |
| `StatusExpiring` | `EXPIRING` | 即将到期（30 天内） |
| `StatusGrace` | `GRACE` | 已过期但仍处于宽限期 |
| `StatusExpired` | `EXPIRED` | 授权已到期（含宽限期耗尽）或凭证失效 |
| `StatusRevoked` | `REVOKED` | 许可证已吊销 |
| `StatusSuspended` | `SUSPENDED` | 因商务或管理原因暂停 |
| `StatusSeatLimitExceeded` | `SEAT_LIMIT_EXCEEDED` | 多机席位已满，激活失败且不自动重试 |
| `StatusSeatReleased` | `SEAT_RELEASED` | 所属席位已释放，保留本机凭证但不自动重激活 |
| `StatusInstanceMismatch` | `INSTANCE_MISMATCH` | 部署实例或设备绑定不匹配 |
| `StatusVersionNotAllowed` | `VERSION_NOT_ALLOWED` | 当前项目版本不在授权范围 |
| `StatusFeatureNotAllowed` | `FEATURE_NOT_ALLOWED` | 功能未授权 |
| `StatusLimitExceeded` | `LIMIT_EXCEEDED` | 人数、设备数或额度超限 |
| `StatusClockTampered` | `CLOCK_TAMPERED` | 疑似回拨系统时间（仅告警标记，不拒绝服务） |
| `StatusNotFound` | `NOT_FOUND` | 许可证/实例信息无效或请求签名不合法（传输层） |
| `StatusError` | `ERROR` | 服务端故障（按网络异常处理，沿用本地缓存） |

### 12.3 升级记录状态（`update.go`）

| 常量 | 值 | 说明 |
|---|---|---|
| `UpgradePending` | `pending` | 已创建任务 |
| `UpgradeDownloading` | `downloading` | 下载中 |
| `UpgradeInstalling` | `installing` | 安装中 |
| `UpgradeSuccess` | `success` | 升级成功 |
| `UpgradeFailed` | `failed` | 升级失败 |
| `UpgradeRolledBack` | `rolled_back` | 已回滚 |

## 13. 纯函数层：签名与信封

### 13.1 信封结构体

#### `Payload` - 许可证签发载荷（`envelope.go`）

**字段顺序即 JSON 序列化顺序，直接决定签名内容：新增字段只允许追加到结构体末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。**

| 字段 | 类型 | 说明 |
|---|---|---|
| `LicenseId` | `string` | 许可证业务编号（`LIC-...`） |
| `UserId` | `string` | 用户业务编号（`USR-...`） |
| `ProjectId` | `string` | 项目业务编号（`PRJ-...`） |
| `InstanceId` | `string` | 实例业务编号（`INS-...`） |
| `Environment` | `string` | 部署环境 |
| `ValidFrom` | `string` | 生效时间（RFC3339，空串 = 不限制） |
| `ValidUntil` | `string` | 到期时间（RFC3339，空串 = 永久授权） |
| `MaintenanceUntil` | `string` | 维保到期时间（RFC3339，空串 = 不限制） |
| `UpgradeUntil` | `string` | 升级权到期时间（RFC3339，空串 = 不限制） |
| `GraceDays` | `int` | 宽限期（天） |
| `VersionRange` | `string` | 允许的项目版本范围（空 = 不限制） |
| `Features` | `map[string]bool` | 功能权益表（`code → 是否授权`） |
| `Limits` | `map[string]int64` | 额度表（`key → 上限`） |
| `Binding` | `*Binding` | 绑定信息（类型/值，如设备绑定） |
| `IssuedAt` | `string` | 签发时间（RFC3339） |
| `KeyVersion` | `string` | 签名密钥版本（选公钥用） |
| `Nonce` | `string` | 防重放随机值 |

#### `Envelope` - 签名信封

| 字段 | 类型 | 说明 |
|---|---|---|
| `Version` | `int` | 信封版本（= `EnvelopeVersion`） |
| `Algorithm` | `string` | 签名算法（= `Algorithm`） |
| `Payload` | `Payload` | 签发载荷 |
| `Signature` | `string` | 对 `MarshalPayload(Payload)` 字节的 Ed25519 签名（hex） |

### 13.2 序列化与解析

| 函数 | 签名 | 说明 |
|---|---|---|
| `MarshalPayload` | `func MarshalPayload(payload Payload) ([]byte, error)` | 序列化载荷为待签名字节。结构体字段顺序稳定，map 字段（features/limits）由 `encoding/json` 按键名排序，同一载荷任意时刻序列化结果字节一致 |
| `ParseEnvelope` | `func ParseEnvelope(data []byte) (envelope Envelope, rawPayload []byte, err error)` | 解析信封 JSON，同时返回**载荷原始字节**。验签必须基于载荷原文（而非重序列化），平台只追加新字段时原文验签天然兼容 |

### 13.3 Licence 链式工具类（nil 安全，全局变量 `licence.Licence`）

值语义：每个方法返回副本，不修改原实例。`*LicenceClass` 方法：

| 方法 | 签名 | 说明 |
|---|---|---|
| `Payload` | `(this *LicenceClass) Payload(payload Payload) *LicenceClass` | 设置待签发载荷 |
| `Seed` | `(this *LicenceClass) Seed(seed []byte) *LicenceClass` | 设置私钥种子（32 字节） |
| `PublicKey` | `(this *LicenceClass) PublicKey(publicKey string) *LicenceClass` | 设置验签公钥（hex） |
| `GenerateKeyPair` | `(this *LicenceClass) GenerateKeyPair() *LicenceClass` | 生成新 Ed25519 密钥对并写入实例 |
| `KeyPair` | `(this *LicenceClass) KeyPair() (seed []byte, publicKey string)` | 取当前私钥种子与公钥（hex） |
| `Issue` | `(this *LicenceClass) Issue() (Envelope, error)` | 签发：序列化当前载荷并签名，组装完整信封 |
| `Sign` | `(this *LicenceClass) Sign(payloadBytes []byte) (string, error)` | 对任意字节做 Ed25519 签名，返回 hex 签名 |
| `Verify` | `(this *LicenceClass) Verify(envelope Envelope, publicKey ...string) bool` | 校验完整信封：重序列化载荷并验签（可传参覆盖当前公钥） |
| `VerifySign` | `(this *LicenceClass) VerifySign(payloadBytes []byte, signatureHex string, publicKey ...string) bool` | 用公钥校验载荷签名（可传参覆盖当前公钥） |
| `VerifyRaw` | `(this *LicenceClass) VerifyRaw(rawPayload []byte, signatureHex string, publicKey ...string) bool` | **验签首选**：对载荷原文验签，兼容平台只追加的新字段 |
| `Parse` | `(this *LicenceClass) Parse(data []byte) (Envelope, error)` | 解析信封 JSON |
| `Nonce` | `(this *LicenceClass) Nonce() string` | 生成随机挑战值（hex 编码 16 字节），作载荷防重放 nonce |

典型链式用法：

```go
envelope, err := licence.Licence.Payload(payload).GenerateKeyPair().Issue()
ok := licence.Licence.PublicKey(publicKey).Verify(envelope)
ok2 := licence.Licence.VerifyRaw(rawPayload, envelope.Signature, publicKey)
```

> 注意：`Verify` 基于重序列化（丢未知字段），生产环境验签请用 `VerifyRaw` + `ParseEnvelope` 的载荷原文。

## 14. 版本范围判定

### `VersionInRange`（`version-range.go`）

```go
func VersionInRange(version string, rangeExpr string) bool
```

判断版本号是否落在版本范围表达式内，与平台 `app/common/version-range.go` 语义完全一致。

- 表达式为空 = 不限制（返回 `true`）；
- 空格或逗号分隔多个比较子，如 `">=2.0.0 <3.0.0"`、`">=2.0.0, <3.0.0"`；
- 支持算子：`>=` `>` `<=` `<` `=` `==` `!=`（`==` 等价 `=`）；
- 版本号按 `a.b.c` 数值比较（缺段补 0，容忍前导 `v`/`V`）；
- 表达式非法或版本号无法解析 = 拒绝（返回 `false`，安全兜底）。

```go
licence.VersionInRange("2.3.1", ">=2.0.0 <3.0.0") // true
```

## 15. 实例指纹

### `FingerprintProvider`（`fingerprint.go`）

```go
type FingerprintProvider func() (string, error)
```

自定义实例指纹提供者。容器/云主机等硬件因子不稳定的场景由业务注入稳定指纹源；返回值为指纹原文，SDK 统一加盐哈希后使用。

### `FingerprintHash`

```go
func FingerprintHash(salt string, override string, provider FingerprintProvider) (string, error)
```

生成实例指纹哈希（64 位 hex）：

| 参数 | 说明 |
|---|---|
| `salt` | 项目盐（与实例登记时使用的盐一致） |
| `override` | 显式指纹哈希（非空直接返回，视为业务已完成采集与哈希） |
| `provider` | 自定义指纹提供者（非 nil 时优先于自动采集） |

默认采集机器 ID + 系统 UUID + 主板序列号多因子加盐 SHA-256（禁止单用 IP/MAC）；单因子不可得的平台按既定降级组合（有几个用几个），全不可得时返回错误——此时必须注入 `FingerprintProvider`。

## 16. 安全存储接口

### `Store`（`store.go`）

```go
type Store interface {
    Load() ([]byte, error)   // 读取密文状态并解密；不存在返回 nil, nil
    Save(data []byte) error  // 加密并原子写入状态
    Clear() error            // 清除状态
}
```

activationToken / 客户端私钥 / 信封缓存只允许经 `Store` 持久化。默认实现为 AES-256-GCM 加密文件（密钥派生自 项目盐 + 实例指纹，权限 0600，文件名 `licence-<licenseNo>.state`）；需要系统密钥库（DPAPI/Keychain/keyring）时实现该接口并在 `licence.Options.Store` 注入。

## 17. 运行面客户端 Client

### 17.1 配置 `Options`

```go
type Options struct {
    ServerURL          string                 // 平台地址（必填），如 "https://licen-hub.inis.cn"
    LicenseNo          string                 // 许可证编号（必填），格式 LIC-{年}-XXXXXX
    InstanceNo         string                 // 部署实例编号（可选，许可证绑定实例时上送）
    DeviceName         string                 // 机器名称（可选，仅供席位列表展示；缺省取 os.Hostname）
    Salt               string                 // 指纹盐（必填，与实例登记时一致）
    PublicKeys         map[string]string      // 验签公钥表（必填，keyVersion -> hex 公钥，可多版本并存轮换）
    ReleasePublicKeys  map[string]string      // release 验签公钥表（在线更新模块必填）
    StorageDir         string                 // 状态存储目录（默认 ./runtime/licence）
    Fingerprint        string                 // 显式指纹哈希（可选，覆盖自动采集）
    Provider           FingerprintProvider    // 自定义指纹提供者（可选）
    Store              Store                  // 自定义安全存储（可选）
    Version            string                 // 当前项目版本（随校验上送）
    RefreshInterval    time.Duration          // 校验刷新间隔（默认 12 小时，建议 12~24 小时）
    HTTPTimeout        time.Duration          // 单次请求超时（默认 15 秒）
    Transport          Transport              // 零值 HTTP；可选 TransportGRPC
    GRPC               GRPCOptions
    OnStatusChange     func(oldStatus string, newStatus string) // 状态变化回调（可选）
}
```

### 17.2 创建与生命周期

| 方法 | 签名 | 说明 |
|---|---|---|
| `New` | `func New(options Options) (*Client, error)` | 创建客户端：归一化配置 + 采集指纹 + 初始化存储，**不发起网络请求**。必填校验：`ServerURL` / `LicenseNo` / `Salt` / `PublicKeys` |
| `Start` | `func (this *Client) Start(ctx context.Context) error` | 启动：恢复本地状态（读存储 → 验签缓存信封）或执行首激活（生成客户端密钥对 + 注册公钥 + 换令牌），随后进入后台滑动刷新循环。有可验签的本地缓存信封时，平台不可达也能降级启动（按本地时间维度给出 `GRACE` / `EXPIRED` 状态）；无缓存且激活失败时返回错误 |
| `Stop` | `func (this *Client) Stop()` | 停止后台刷新循环 |
| `Close` | `func (this *Client) Close() error` | 释放 HTTP idle connection 或复用的 gRPC connection；可重复调用 |
| `Reset` | `func (this *Client) Reset() error` | 清除本机运行状态（token/客户端私钥/信封/派生缓存全清），**不通知平台释放席位**；下次 `Start` 以同一指纹重新激活，由服务端复用或重新占用原席位 |

后台循环行为（开发者零感知）：

- 每 `RefreshInterval`（±10% 抖动防惊群）自动 `validate` 滑动续期；网络故障退避 1 分钟重试；
- 每个请求自动携带客户端私钥签名（校时时间戳 + nonce 防重放）；
- 断网/平台故障自动按本地缓存信封降级运行（宽限内 `GRACE`，耗尽 `EXPIRED`）；
- 许可证被续期/调整权益后新信封自动下发、验签、替换缓存；
- `validate` 返回 `EXPIRED` 时自动清除本地凭证并尝试重新激活；
- `SEAT_LIMIT_EXCEEDED` / `SEAT_RELEASED` 为非自动恢复终态：后台循环立即停摆，不做任何自动重试/重激活（见 §4.1）。
- 首次激活被拒绝时不落盘无信封状态；启动时检测到旧版残留的无信封状态会按未激活自愈。

### 17.3 业务闸门方法

| 方法 | 签名 | 说明 |
|---|---|---|
| `Status` | `func (this *Client) Status() string` | 当前授权状态（状态码见 §12.2），业务据此放行/降级 |
| `SeatNo` | `func (this *Client) SeatNo() string` | 当前机器的席位编号（尚未激活或历史服务端未返回时为空；仅展示/排障） |
| `DeviceName` | `func (this *Client) DeviceName() string` | 实际生效的机器名称（`Options.DeviceName` 或其缺省主机名，与激活上送值一致） |
| `Envelope` | `func (this *Client) Envelope() (Envelope, bool)` | 当前缓存信封（第二返回值标识是否存在） |
| `HasFeature` | `func (this *Client) HasFeature(code string) bool` | 功能权益闸门：放行状态且载荷 `features[code]` 为 true |
| `GetLimit` | `func (this *Client) GetLimit(key string) (int64, bool)` | 额度查询：返回载荷 `limits[key]`（未配置返回 0, false） |
| `CheckVersion` | `func (this *Client) CheckVersion(version string) bool` | 版本范围本地判定（与平台语义一致，空范围 = 不限制） |
| `ReportUsage` | `func (this *Client) ReportUsage(usage map[string]int64)` | 用量上报：合并进待上报表，随下次 validate 携带，服务端确认后清空 |
| `Reactivate` | `func (this *Client) Reactivate(ctx context.Context) error` | 重新激活（换机/令牌丢失/EXPIRED 引导路径）：生成新客户端密钥对重新绑定，旧公钥随旧激活记录失效 |
| `Current` | `func (this *Client) Current(ctx context.Context) (Envelope, error)` | 按需拉取当前生效信封（不做滑动刷新；失败返回错误，不影响本地缓存） |

典型接入：

```go
client, err := licence.New(licence.Options{
	ServerURL: "https://licen-hub.inis.cn", LicenseNo: "LIC-2026-000123",
	Salt: "my-project-salt",
	PublicKeys: map[string]string{"license-key-INIS-202608-1a2b3c": "<平台按项目导出的公钥 hex>"},
	Version: "2.3.1",
})
if err = client.Start(context.Background()); err != nil {
	panic(err)
}
defer client.Stop()

if client.HasFeature("report.advanced") { /* 开放高级报表 */ }
if maxUsers, ok := client.GetLimit("max_users"); ok { /* 额度内 */ }
if !client.CheckVersion("2.3.1") { /* 当前版本不在授权范围 */ }
```

### 17.4 装机自动申领与激活码兑换（包级函数 Provision / Redeem）

> 包级函数，无需 `Client` 实例；在 `New`/`Start` 之前调用，拿证后再构造运行面客户端。传输层与运行面复用同一 HTTP/gRPC 双实现，申领/兑换为匿名凭证（`withSign=false`，不请求签名）；业务码统一放 200/nil-error 的 `status` 字段，两协议线格式一致。

```go
type ProvisionOptions struct {
    ServerURL      string        // 平台地址（必填）
    TemplateCode   string        // 模板编码（必填，官方二进制写死）
    ProvisionToken string        // 签发令牌（必填，匿名申领凭证，官方二进制写死）
    StorageDir     string        // install.sn 持久化目录（默认 ./runtime/licence）
    InstallSN      string        // 安装唯一标识（可选，为空懒生成 UUID v4 并持久化）
    DeviceName     string        // 机器名称（可选，缺省取 os.Hostname）
    Transport      Transport     // 传输协议（默认 HTTP；可选 TransportGRPC）
    GRPC           GRPCOptions   // gRPC 连接配置（仅 TransportGRPC 生效）
    HTTPTimeout    time.Duration // 单次请求超时（默认 15 秒）
}
type RedeemOptions struct {
    ProvisionOptions
    Code string // 激活码（必填）
}
type ProvisionResult struct {
    LicenseNo     string // 许可证编号（New 必填）
    Salt          string // 指纹盐（New 必填，与申领模板一致）
    BindingPolicy string // single / seats
    SeatLimit     int
    ExpiresAt     int64  // 有效期截止（毫秒）
    Reissued      bool   // 同 SN 有界续签
}
type ProvisionError struct { // 业务拒绝，errors.As 断言
    Status  string // PROVISION_DISABLED / TEMPLATE_INVALID / QUOTA_EXCEEDED / RATE_LIMITED / ...
    Message string // 平台提示
}
```

| 函数 | 签名 | 说明 |
|---|---|---|
| `Provision` | `func Provision(ctx context.Context, opts ProvisionOptions) (ProvisionResult, error)` | 免费版装机自动申领；业务拒绝返回 `*ProvisionError` |
| `Redeem` | `func Redeem(ctx context.Context, opts RedeemOptions) (ProvisionResult, error)` | 商业激活码兑换，语义同上 |

`install.sn` 生命周期：显式 `InstallSN` 优先；否则读 `StorageDir/install.sn` 幂等复用；不存在则懒生成 UUID v4 原子落盘（`MkdirAll 0700` + tmp/rename + `0600`）。卸载清理删除该文件属预期行为（重装生成新 SN，视为新装机）。

## 18. 在线更新模块

### 18.1 数据结构

```go
// 更新检查结果
type UpdateInfo struct {
	Status    string    // 授权状态（仅放行状态会附带可用清单）
	Available bool      // 是否存在可升级版本
	Manifest  *Manifest // 已验签的更新清单（Available 为 true 时非空）
}

// 升级结果上报（RecordNo 为空创建记录，非空推进已有记录状态）
type UpgradeReport struct {
	RecordNo      string // 记录编号（推进时填）
	FromVersion   string // 源版本
	TargetVersion string // 目标版本
	ArtifactNo    string // 发布物编号
	Status        string // 状态（Upgrade* 常量）
	Message       string // 说明
}
```

### 18.2 清单与发布物结构

| 类型 | 说明 |
|---|---|
| `ManifestArtifact` | 更新清单中的发布物项：`ArtifactNo` / `FileName` / `Url` / `Size` / `OsArch` / `Sha256` / `Signature` / `KeyVersion` |
| `ManifestPayload` | 更新清单签名载荷（与平台 `app/common/sign/manifest.go` 字节级镜像，字段顺序即签名内容，只许追加） |
| `Manifest` | 更新清单信封（release-key 签名，与许可证信封同构） |
| `ArtifactPayload` | 发布物签名载荷（`ArtifactNo` / `Version` / `Sha256`） |
| `MarshalManifestPayload` | `func MarshalManifestPayload(payload ManifestPayload) ([]byte, error)` 序列化清单载荷为待签名字节 |
| `ParseManifest` | `func ParseManifest(data []byte) (manifest Manifest, rawPayload []byte, err error)` 解析清单 JSON + 返回载荷原文 |

### 18.3 接口方法（挂在 `*Client` 上）

| 方法 | 签名 | 说明 |
|---|---|---|
| `CheckUpdate` | `func (this *Client) CheckUpdate(ctx context.Context, osArch string) (UpdateInfo, error)` | 在线更新检查：上报当前版本与架构（如 `"linux/amd64"`，空串 = 不区分），服务端判定授权状态、升级权（`upgradeUntil`）与灰度规则后返回 release-key 签名的清单；本方法完成清单验签与发布物签名复核；实例许可证非放行态（含 `SEAT_RELEASED`）时 fail-closed 返回错误，不回写本地授权状态 |
| `VerifyManifest` | `func (this *Client) VerifyManifest(raw json.RawMessage) (*Manifest, error)` | 验签更新清单原文（在线响应与离线更新包 `manifest.json` 共用） |
| `DownloadArtifact` | `func (this *Client) DownloadArtifact(ctx context.Context, manifest *Manifest, artifact ManifestArtifact, destPath string) error` | 下载发布物并校验（签名复核 + 大小 + SHA-256，全部通过才落盘；先写临时文件，校验通过后原子重命名，大文件下载 30 分钟长超时） |
| `ReportUpgrade` | `func (this *Client) ReportUpgrade(ctx context.Context, report UpgradeReport) (string, error)` | 上报升级结果（创建或推进升级记录），返回升级记录编号 |
| `ReportUpgradeLog` | `func (this *Client) ReportUpgradeLog(ctx context.Context, recordNo string, lines []string) error` | 追加升级过程日志（按行，单行最长 512，单次最多 100 行） |

```go
info, err := client.CheckUpdate(ctx, "linux/amd64")
if err == nil && info.Available {
	manifest := info.Manifest
	err = client.DownloadArtifact(ctx, manifest, manifest.Payload.Artifacts[0], "/tmp/app-2.4.0.tar.gz")
}

// 离线更新包验签
raw, _ := os.ReadFile("manifest.json")
manifest, err := client.VerifyManifest(raw)
```

> 升级执行编排（预检/备份/停服/迁移/回滚）由你的更新代理负责，SDK 只提供检查、下载、验签与上报。

## 19. SaaS 多租户接口

### 19.1 数据结构

```go
// 租户运行状态（sync 下发项；放行租户携带已验签信封）
type TenantInfo struct {
	TenantCode string          // 租户编码
	Status     string          // 租户状态（VALID/EXPIRING/GRACE/...，无实例专属状态）
	Envelope   *TenantEnvelope // 已验签的租户授权信封（非放行租户为 nil）
}

// 项目菜单清单（sync 随响应下发）
type TenantManifest struct {
	Version int             // 清单版本
	Menus   json.RawMessage // 菜单树原文（结构由 SaaS 项目自行解释）
}

// 单租户实时校验的可选判定输入
type TenantValidateOptions struct {
	Version string           // 当前版本（上送则按租户载荷 versionRange 判定）
	Feature string           // 待校验功能编码
	Usage   map[string]int64 // 用量上报（服务端按小时水位落库去重，重试安全）
}
```

`TenantPayload` / `TenantEnvelope`：租户授权载荷与信封（与平台 `app/common/sign/tenant.go` 字节级镜像，字段顺序即签名内容，只许追加）。租户不做指纹绑定，签名密钥复用 license-key，验签使用 `Options.PublicKeys`。

```go
func ParseTenantEnvelope(data []byte) (envelope TenantEnvelope, rawPayload []byte, err error)
```

### 19.2 接口方法（挂在 `*Client` 上）

| 方法 | 签名 | 说明 |
|---|---|---|
| `TenantSync` | `func (this *Client) TenantSync(ctx context.Context, sinceTime int64) (int64, *TenantManifests, error)` | 租户授权全量/增量同步（每小时 + 启动时调用）。`sinceTime` 为增量水位线（毫秒，0 = 全量），返回本次同步时间与 `platform`、`tenant` 双轨项目菜单清单（某一轨未发布时对应字段为 nil）。放行租户的信封验签后写入本地缓存；平台不可达时返回错误，本地缓存继续可用 |
| `TenantSearch` | `func (this *Client) TenantSearch(ctx context.Context, prefix string) ([]TenantSearchItem, error)` | 按至少 3 位租户编码前缀搜索当前许可证项目下可登录租户，最多返回 20 条；HTTP 与 gRPC 均使用实例激活签名，不提供匿名跨项目枚举 |
| `TenantValidate` | `func (this *Client) TenantValidate(ctx context.Context, tenantCode string, options TenantValidateOptions) (string, error)` | 单租户实时校验（租户用户登录/访问受控功能时调用）。放行返回状态码并把信封写入本地缓存；非放行只返回状态码 |
| `TenantCurrent` | `func (this *Client) TenantCurrent(ctx context.Context, tenantCode string) (*TenantEnvelope, error)` | 取租户当前生效信封（不更新缓存水位，仅按需拉取） |
| `TenantStatus` | `func (this *Client) TenantStatus(tenantCode string) string` | 租户本地状态：有缓存信封时按信封做时间维度本地判定，无信封时返回缓存的服务端判定（平台不可达时的降级判定依据；无缓存返回空串） |
| `TenantFeature` | `func (this *Client) TenantFeature(tenantCode string, code string) bool` | 租户功能权益本地判定：缓存信封放行且 `features[code]` 为 true |

```go
// 启动时 + 每小时同步一次（增量水位线）
syncTime, manifests, err := client.TenantSync(ctx, 0) // 之后传上次返回的 syncTime
tenants, _ := client.TenantSearch(ctx, "mes")          // 登录页远程下拉
envelope, _ := client.TenantCurrent(ctx, "tenant-a")
// manifests 整体或其中未发布的轨都可能为 nil（服务端省略 manifests 键 / 该轨未发布），取值前必须判空
var tenantMenus []licence.ManifestMenu
if manifests != nil && manifests.Tenant != nil {
	tenantMenus, _ = licence.FilterManifestMenus(manifests.Tenant, envelope.Payload.MenuCodes)
}

// 租户用户登录/访问受控功能时实时校验
status, err := client.TenantValidate(ctx, "tenant-a", licence.TenantValidateOptions{
	Feature: "report.advanced",
	Usage:   map[string]int64{"max_users": 12},
})

// 平台不可达时的本地降级判定
status = client.TenantStatus("tenant-a")
ok := client.TenantFeature("tenant-a", "report.advanced")
```

> 前置闸门：实例许可证非放行态时 `TenantSync` 直接返回错误（fail-closed 总闸）；`TenantValidate` 不设总闸，把校验状态码原样返回（放行态附带缓存信封）。fail-open / fail-closed 的租户级策略由服务商按 `TenantStatus` 自行决策。

### 19.3 回调通知（事件订阅 · 事件推送）

`NewCallbackHandler(CallbackOptions)` 返回标准 `http.Handler`，可挂载到标准库、Gin 或其他 HTTP 框架。`PublicKeys` 与运行面 `Options.PublicKeys` 使用同一组 license-key 信任链。

```go
type CallbackOptions struct {
	PublicKeys map[string]string
	TimeWindow time.Duration // 默认 ±5 分钟
	DedupTTL   time.Duration // 默认 10 分钟
	MaxBody    int64         // 默认 1MB
}

func NewCallbackHandler(options CallbackOptions) *CallbackHandler
func (this *CallbackHandler) OnEvent(event string, fn CallbackFunc) *CallbackHandler
func (this *CallbackHandler) OnAny(fn CallbackFunc) *CallbackHandler
func ParseCallbackEnvelope(data []byte) (CallbackEnvelope, []byte, error)
```

分发顺序为精确事件 → 逐级前缀通配（例如 `saas.plan.*` 、`saas.*`）→ `OnAny` → 自动 `ignored`。应答词是 `AckSuccess` / `AckOk` / `AckIgnored` / `AckRetry` / `AckRejected`；回调返回 error 等价于 `AckRetry`，panic 由 SDK 恢复为 HTTP 500。`deliveryNo` 是业务幂等键，相同 nonce 的原请求重放会被 HTTP 401 拒绝。

**类型参考**（`callback.go`，信封与平台签发端字节级镜像，字段顺序即签名内容，只许追加）：

| 类型 | 说明 |
|---|---|
| `CallbackPayload` | 回调事件载荷：`EventNo` / `DeliveryNo` / `Event` / `ProjectId` / `InstanceId` / `OccurredAt` / `Nonce` / `KeyVersion` / `Data`（`json.RawMessage`） |
| `CallbackEnvelope` | 回调签名信封（与许可证信封同构） |
| `Ack` | 应答词类型（`type Ack string`），合法值：`AckSuccess` / `AckOk` / `AckIgnored` / `AckRetry` / `AckRejected` |
| `CallbackFunc` | `func(ctx context.Context, event *CallbackEvent) (Ack, error)` 业务回调；返回 error 等价于 `AckRetry` |
| `CallbackEvent` | 分发给业务回调的事件对象：`Payload`（完整回调载荷）、`Data`（载荷 `Data` 的只读视图） |
| `CallbackEvent.MustData` | `func (this *CallbackEvent) MustData(v any)` 将事件 `Data` 解码到 `v`，失败时 panic |

事件名常量：SaaS 套餐 / 租户 / 菜单清单 / 平台配置全部 9 个事件均有导出常量（`EventSaasPlanCreated`、`EventSaasTenantCreated`、`EventSaasMenuPublished`、`EventPlatformConfigUpdated` 等，完整清单与 `data` 字段见 API.md §2.4），可直接传入 `OnEvent`；按事件族订阅用前缀通配（如 `saas.plan.*`、`platform.config.*`、`saas.*`）一次捕获该族全部动作。

### 19.4 平台配置同步

| 方法 | 签名 | 说明 |
|---|---|---|
| `PlatformConfigSync` | `func (this *Client) PlatformConfigSync(ctx context.Context) (*PlatformConfigEnvelope, error)` | 使用持久化水位增量拉取，验签后按服务端下发全量条目覆盖本地快照并持久化 |
| `PlatformConfig` | `func (this *Client) PlatformConfig(key string) (PlatformConfigItem, bool)` | 返回本地平台配置条目副本 |
| `PlatformConfigMust` | `func (this *Client) PlatformConfigMust(key string) PlatformConfigItem` | 配置不存在时 panic |

**数据结构**（与平台 `app/common/sign/platform-config.go` 字节级镜像，字段顺序即签名内容，只许追加）：

| 类型 | 说明 |
|---|---|
| `PlatformConfigItem` | 平台配置下发条目（服务端已完成维度匹配，`Value` 为最终值）：`Key` / `Label` / `Type` / `Value` / `DefaultValue` / `Options` / `Rules` / `Placeholder` / `Remark` / `Sensitive`（bool）/ `Version` |
| `PlatformConfigGroup` | 平台配置分组（树形，扁平输出 + `Children` 嵌套）：`Id` / `Pid` / `Name` / `Label` / `Icon` / `Sort` / `Children` |
| `PlatformConfigPayload` | 同步载荷：`ProjectId` / `SyncVersion` / `Groups` / `Configs` / `IssuedAt` / `KeyVersion` / `Nonce` |
| `PlatformConfigEnvelope` | 签名信封（与许可证信封同构） |
| `ParsePlatformConfigEnvelope` | `func ParsePlatformConfigEnvelope(data []byte) (PlatformConfigEnvelope, []byte, error)` 解析并返回 payload 原文 |

`PlatformConfigPayload` / `PlatformConfigEnvelope` 与平台字节级镜像，验签必须使用 `ParsePlatformConfigEnvelope` 返回的 payload 原文。回调事件 `platform.config.updated`（值变更）、`platform.config.definition.changed`（定义变更）只是失效信号，客户端收到后应调用 `PlatformConfigSync` 拉取全量收敛，并以周期性同步兜底。

### 19.6 运行面事件订阅（EventSubscriber）

`Client.Subscribe(CallbackOptions)` 创建**项目级事件订阅器**，拉取平台 `callback_events` 增量（HTTP 长轮询 + gRPC 服务端流双传输），把订阅信封喂给与 §19.3 `CallbackHandler` 完全相同的验签 / 防重放 / 分发管线。**无需登记 `notify_url`**；事件由平台现场重签（`occurredAt` 重新盖戳、`nonce` 新鲜、`deliveryNo` 稳定 `SUB-{eventNo}`），客户端以 `eventId` 单调推进水位。

| 方法 | 签名 | 说明 |
|---|---|---|
| `Client.Subscribe` | `func (this *Client) Subscribe(options CallbackOptions) *EventSubscriber` | 创建订阅器（默认复用 `client.PublicKeys`，options 可覆盖 `TimeWindow` / `DedupTTL` 等） |
| `OnEvent` | `func (this *EventSubscriber) OnEvent(event string, fn CallbackFunc) *EventSubscriber` | 注册精确事件或前缀通配处理器（如 `saas.*`，链式） |
| `OnAny` | `func (this *EventSubscriber) OnAny(fn CallbackFunc) *EventSubscriber` | 注册未匹配事件的兜底处理器 |
| `Poll` | `func (this *EventSubscriber) Poll(ctx context.Context) (int, error)` | 一轮长轮询/流式拉取并分发，返回已成功分发条数 |
| `Run` | `func (this *EventSubscriber) Run(ctx context.Context) error` | 循环 `Poll` 直到 `ctx` 取消 |
| `Watermark` | `func (this *EventSubscriber) Watermark() int64` | 当前已消费水位（`callback_events.id`） |
| `SetWatermark` | `func (this *EventSubscriber) SetWatermark(id int64)` | 设置初始水位（跳过头历史事件，需在首轮 `Poll` 前调用） |

**语义**：

- 事件按 `eventId` 升序消费：`success/ok/ignored` 推进水位；`retry/rejected` 或验签失败停在该事件不推进（下轮重拉，`deliveryNo` 去重 TTL 内不重复分发）。`Poll` 不持有 `client.opMu`，长轮询 hold 不阻塞 validate/activate 刷新循环。
- 单轮 hold 默认 15s；HTTP 传输用专用更长超时的 http.Client（`HTTPTimeout+30s`），gRPC 传输 `context.WithTimeout(hold+10s)`。
- 非放行态归一：HTTP 200 body 的 `status` 与 gRPC `FailedPrecondition` 均归一为 `许可证非放行态：{status}`；`ERROR` 返回 `服务端故障：{message}`（镜像 `TenantSync`）。
- 未注册事件自动 `AckIgnored` 计入水位，不进入重试风暴。事件常量与 `data` 字段见 API.md §2.4（含 `EventSaasTenantCreated`）。

```go
sub := client.Subscribe(licence.CallbackOptions{}).
    OnEvent(licence.EventSaasTenantCreated, func(ctx context.Context, event *licence.CallbackEvent) (licence.Ack, error) {
        var data struct {
            TenantNo string `json:"tenantNo"`
            PlanCode string `json:"planCode"`
        }
        _ = event.MustData(&data)
        _, _, _ = client.TenantSync(ctx, 0) // 推送即失效信号：收到后拉完整租户信封
        return licence.AckSuccess, nil
    })
sub.SetWatermark(lastEventId)
n, err := sub.Poll(ctx) // 或 _ = sub.Run(ctx) 后台循环
```

平台侧契约（`POST /api/v1/events/subscribe`、gRPC `EventRuntimeService/Subscribe`、请求签名、水位与状态码）见 `licen-hub/docs/md/许可证运行面接口契约.md` §10「运行面事件订阅」。

## 20. 管理面客户端 AdminClient

> 使用方是商户自有运维系统/CI。HTTP 与 gRPC 共用资源 API、JWT、IAM、数据范围和 `APIError` 语义。生产登录必须使用 TLS；HTTP 的 `safety.api.sign` 开关不套用到 gRPC。

### 20.1 配置与创建

```go
type AdminOptions struct {
	ServerURL   string        // 平台地址（必填）
	Account     string        // 登录账号（自动登录与 401 重登的凭据；不填则只能 SetToken 注入令牌）
	Password    string        // 登录密码（明文上送，依赖 HTTPS）
	TOTP        string        // 2FA 验证码（可选；账号开启双因素认证时必填）
	HTTPTimeout time.Duration // 单次请求超时（默认 15 秒）
	Transport   Transport     // 零值 HTTP；可选 TransportGRPC
	GRPC        GRPCOptions
}

func NewAdmin(options AdminOptions) (*AdminClient, error)
```

创建后挂载 13 个资源组字段：

```go
admin, _ := licence.NewAdmin(licence.AdminOptions{
	ServerURL: "https://licen-hub.inis.cn", Account: "ops", Password: "secret",
})
// admin.Qualification / admin.Projects / admin.Instances / admin.Licenses /
// admin.SigningKeys / admin.Artifacts / admin.Versions / admin.Modules /
// admin.SaasMenus / admin.SaasFeatures / admin.SaasPlans / admin.SaasTenants / admin.SaasReview
```

### 20.2 登录态

| 方法 | 签名 | 说明 |
|---|---|---|
| `Login` | `func (this *AdminClient) Login(ctx context.Context) (*SignInResult, error)` | 显式登录：`POST /api/comm/sign-in`，成功后将 token 内存保管。一般无需调用——首次请求自动登录；账号开启 2FA 且 TOTP 失效时返回 `*APIError`（`Require2FA=true`），更新 `AdminOptions.TOTP` 后重试 |
| `SignOut` | `func (this *AdminClient) SignOut(ctx context.Context) error` | 退出登录：`DELETE /api/comm/sign-out`（无论平台结果如何都清除本地令牌） |
| `CheckToken` | `func (this *AdminClient) CheckToken(ctx context.Context, refresh bool) (*SignInResult, error)` | 校验/刷新令牌：`POST /api/comm/check-token`；`refresh=true` 时平台续期并返回新令牌，同步更新本地令牌 |
| `Token` | `func (this *AdminClient) Token() Token` | 当前登录令牌（内存副本；`Expired` 为**毫秒**时间戳） |
| `Close` | `func (this *AdminClient) Close() error` | 释放 HTTP idle connection 或复用的 gRPC connection；可重复调用 |
| `SetToken` | `func (this *AdminClient) SetToken(token Token)` | 注入外部保管的令牌（如 CI 复用已有会话；注入后跳过自动登录） |

```go
type Token struct {
	No      string // 会话编号
	Value   string // JWT 令牌值（请求时放入 Authorization: Bearer <value>）
	Expired int64  // 过期时间（毫秒时间戳）
}
type SignInResult struct {
	User  User            // 登录用户（字段见 admin-types.go）
	Token Token           // 登录令牌
	Auth  json.RawMessage // 权限快照（结构随版本演进，保留原文）
}
```

### 20.3 错误分层

| 类型 | 说明 |
|---|---|
| `Response` | 统一响应信封：`Code`（200=成功；400 参数错误；401 未登录/登录失效；403 无权限；404 不存在；409 状态冲突）、`Msg`（平台 i18n 文案）、`Data`（业务数据，无数据时为 null） |
| `APIError` | 业务错误（信封 `code != 200`）：`Code` / `Msg` / `Data` / `Require2FA`（登录被拒且平台要求补交 TOTP 时为 true）。请求已到达平台并被业务逻辑拒绝 |
| `HTTPError` | HTTP 传输层错误（响应状态码非 200，通常是网关/中间件/服务异常）：`StatusCode` / `Body`（截断原文） |
| `Page[T]` | 分页结果：`Data []T` / `Count int64` / `Page int`（find 类接口的 `data` 内统一为 `{data,count,page}`） |

```go
// 错误判断示例
var apiErr *licence.APIError
if errors.As(err, &apiErr) && apiErr.Code == http.StatusUnauthorized { /* 登录失效 */ }
```

### 20.4 通用结果结构

| 类型 | 字段 | 用途 |
|---|---|---|
| `IdResult` | `Id int` | create/update/release 等 |
| `IdsResult` | `Ids []int` | remove/delete/restore/clear 等批量操作 |
| `ApplyResult` | `Id int`, `ApplyNo string` | 资格/授权申请提交 |
| `ReviewResult` | `Id int`, `Action string`, `LicenseNo string` | 资格/授权审批（approve 时 `Id` 为签发的许可证 ID，`LicenseNo` 仅 approve 返回） |
| `LicenseNoResult` | `Id int`, `LicenseNo string` | renew/reissue |
| `StatusResult` | `Id int`, `Status string` | suspend/revoke |
| `ReleaseResult` | `Id int`, `Version string` | 版本发布 |

### 20.5 资源组接口明细

所有方法第一个参数均为 `ctx context.Context`。`input` / `params` 参数类型见 §20.6（DTO 均在 `admin-types.go` 定义，json tag 与平台逐一对齐，camelCase）。

#### Qualification - 资格审核（`/api/qualification/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Apply` | 提交资格申请（member 自助） | `POST /api/qualification/apply` | `QualificationApplyInput` → `*ApplyResult` |
| `Current` | 我的资格状态 + 有效配额 + 最近一条申请 | `GET /api/qualification/current` | 无 → `*QualificationCurrent` |
| `Mine` | 我的申请历史（倒序分页） | `GET /api/qualification/mine` | `*QualificationFindParams` → `*Page[QualificationApplication]` |
| `Rows` | 申请队列全部（管理员，不分页，需 `qualification.review` 权限） | `GET /api/qualification/rows` | `*QualificationFindParams` → `[]QualificationApplication` |
| `Find` | 申请队列分页（管理员） | `GET /api/qualification/find` | `*QualificationFindParams` → `*Page[QualificationApplication]` |
| `Take` | 申请详情（管理员） | `GET /api/qualification/take?id=N` | `id int` → `*QualificationApplication` |
| `Review` | 审批资格申请（approve/reject） | `POST /api/qualification/review` | `QualificationReviewInput` → `*ReviewResult` |
| `Revoke` | 撤销已批准的用户资格（管理员） | `POST /api/qualification/revoke` | `userId int, reviewNote string` → 无 |

#### Projects - 项目（`/api/projects/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Rows` | 列表（不分页） | `GET /api/projects/rows` | `*ProjectFindParams` → `[]Project` |
| `Find` | 分页 | `GET /api/projects/find` | `*ProjectFindParams` → `*Page[Project]` |
| `Take` | 详情 | `GET /api/projects/take?id=N` | `id int` → `*Project` |
| `Create` | 新增（member 受资格与配额闸门约束，归属用户取登录态） | `POST /api/projects/create` | `ProjectInput` → `*IdResult` |
| `Update` | 编辑（项目归属不可通过编辑变更） | `PUT /api/projects/update` | `ProjectInput` → `*IdResult` |
| `Remove` | 逻辑删除（回收站） | `DELETE /api/projects/remove` | `ids []int` → `*IdsResult` |
| `Delete` | 物理删除 | `DELETE /api/projects/delete` | `ids []int` → `*IdsResult` |
| `Restore` | 恢复回收站数据 | `PUT /api/projects/restore` | `ids []int` → `*IdsResult` |

#### Instances - 部署实例（`/api/instances/*`）

`Rows` / `Find` / `Take` / `Create` / `Update` / `Remove` / `Delete` / `Restore` —— 与 Projects 完全同构（参数 `InstanceFindParams` / `InstanceInput`，返回 `DeploymentInstance`）。注意：`ServerFingerprint` 提交原文，平台加盐哈希后存储（不回存原文）。

#### Licenses - 许可证与授权申请（`/api/licenses/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Rows` | 许可证列表（不分页；非审批视角限本人） | `GET /api/licenses/rows` | `*LicenseFindParams` → `[]License` |
| `Find` | 许可证分页 | `GET /api/licenses/find` | `*LicenseFindParams` → `*Page[License]` |
| `Take` | 许可证详情 | `GET /api/licenses/take?id=N` | `id int` → `*License` |
| `TakePayload` | 查看签发载荷（载荷/签名原文，可用本包 `Parse` + 公钥验签） | `GET /api/licenses/take-payload?id=N` | `id int` → `*LicensePayloadView` |
| `PublicKey` | 项目验签公钥表（任意登录用户可读；projectId 必填，返回全版本 `keys[]` 含历史轮换密钥） | `GET /api/licenses/public-key?projectId=` | `projectId int` → `*LicensePublicKey` |
| `Apply` | 提交授权申请（member 自助） | `POST /api/licenses/apply` | `LicenseApplyInput` → `*ApplyResult` |
| `Cancel` | 撤回授权申请（仅本人 pending 可撤回） | `POST /api/licenses/cancel` | `id int` → 无 |
| `Applications` | 授权申请列表（不分页） | `GET /api/licenses/applications/rows` | `*LicenseApplicationFindParams` → `[]LicenseApplication` |
| `ApplicationTake` | 授权申请详情 | `GET /api/licenses/applications/take?id=N` | `id int` → `*LicenseApplication` |
| `Review` | 审批授权申请（approve 自动签发，需 `license.review` 权限） | `POST /api/licenses/review` | `LicenseReviewInput` → `*ReviewResult` |
| `Renew` | 续期（重签新期限；revoked 不可续，需 `license.renew` 权限） | `POST /api/licenses/renew` | `LicenseActionInput` → `*LicenseNoResult` |
| `Suspend` | 暂停（active → suspended，不重签，Reason 必填） | `POST /api/licenses/suspend` | `LicenseActionInput` → `*StatusResult` |
| `Revoke` | 吊销（不可逆，Reason 必填） | `POST /api/licenses/revoke` | `LicenseActionInput` → `*StatusResult` |
| `Reissue` | 重新签发（现载荷 + `IssuePayload` 覆盖，重签并全量替换子表） | `POST /api/licenses/reissue` | `LicenseActionInput` → `*LicenseNoResult` |
| `History` | 变更历史分页 | `GET /api/licenses/history/rows` | `*LicenseHistoryFindParams` → `*Page[LicenseHistory]` |
| `HistoryTake` | 变更历史详情 | `GET /api/licenses/history/take?id=N` | `id int` → `*LicenseHistory` |
| `Activations` | 激活记录列表（不分页） | `GET /api/licenses/activations/rows` | `*ActivationFindParams` → `[]Activation` |
| `ActivationTake` | 激活记录详情 | `GET /api/licenses/activations/take?id=N` | `id int` → `*Activation` |
| `Seats` | 许可证机器席位分页（多机席位运维，见 §4.1） | `GET /api/licenses/seats/find` | `*LicenseSeatFindParams` → `*Page[LicenseSeat]` |
| `SeatTake` | 机器席位详情 | `GET /api/licenses/seats/take?id=N` | `id int` → `*LicenseSeat` |
| `ReleaseSeat` | 释放机器席位（仅平台用户，需 `license.seat.release` 权限） | `POST /api/licenses/seats/release` | `LicenseSeatReleaseInput` → `*StatusResult` |

#### SigningKeys - 签名密钥（`/api/signing-keys/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Public` | 导出公钥（任意登录用户可读）。`purpose` 仅支持 `license` / `release`；`projectId` 仅 license 用途必填（密钥按项目隔离）；`keyVersion` 留空取当前版本，release 支持按版本导出历史公钥，license 仅保留当前版本 | `GET /api/signing-keys/public?purpose=&keyVersion=&projectId=` | `purpose string, keyVersion string, projectId int` → `*SigningKeyPublic` |
| `Rotate` | 轮换签名密钥（高风险，需 `system.signing-key.rotate` 权限）。`projectId` 仅 license 用途必填（按项目轮换）；`reason` 为轮换原因（审计留痕）。release 保留历史版本供旧发布物验签；license 仅切换当前版本 | `POST /api/signing-keys/rotate` | `purpose string, projectId int, reason string` → `*SigningKeyPublic` |

#### Artifacts - 项目发布物（`/api/project-artifacts/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Rows` | 列表（不分页） | `GET /api/project-artifacts/rows` | `*ArtifactFindParams` → `[]ProjectArtifact` |
| `Find` | 分页 | `GET /api/project-artifacts/find` | `*ArtifactFindParams` → `*Page[ProjectArtifact]` |
| `Take` | 详情 | `GET /api/project-artifacts/take?id=N` | `id int` → `*ProjectArtifact` |
| `Upload` | 上传发布物（multipart，文件字段 `file`；平台按上传字节算 SHA-256、release-key 签名并锁定，需 `project.artifact.upload` 权限；版本须未发布/未归档） | `POST /api/project-artifacts/upload` | `input ArtifactUploadInput, fileName string, content io.Reader` → `*ProjectArtifact` |
| `Update` | 更新元数据（已锁定记录仅放行扫描状态） | `PUT /api/project-artifacts/update` | `ArtifactUpdateInput` → `*IdResult` |
| `Verify` | 服务端代验（按库内 sha256+signature 验签；登录即可用，数据范围按项目域约束） | `POST /api/project-artifacts/verify` | `id int` → `*ArtifactVerifyResult` |
| `VerifyWithFile` | 服务端代验 + 上传本地文件重算 SHA-256 双重校验（multipart，文件字段 `file`） | `POST /api/project-artifacts/verify` | `id int, fileName string, content io.Reader` → `*ArtifactVerifyResult` |
| `Remove` | 逻辑删除（已签名锁定禁止删除） | `DELETE /api/project-artifacts/remove` | `ids []int` → `*IdsResult` |
| `Delete` | 物理删除（已签名锁定禁止删除） | `DELETE /api/project-artifacts/delete` | `ids []int` → `*IdsResult` |

#### Versions - 项目版本（`/api/project-versions/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Rows` / `Find` / `Take` | 列表/分页/详情（同 Projects 结构） | `GET /api/project-versions/{rows,find,take}` | `*VersionFindParams` / `id` → `ProjectVersion` |
| `Create` | 新建版本草稿（仅允许 draft/testing） | `POST /api/project-versions/create` | `VersionInput` → `*IdResult` |
| `Update` | 更新（released/archived 版本仅 remark/supportUntil 允许修改） | `PUT /api/project-versions/update` | `VersionInput` → `*IdResult` |
| `Release` | 发布版本（需至少 1 个已锁定发布物） | `POST /api/project-versions/release` | `id int` → `*ReleaseResult` |
| `Archive` | 归档（仅已发布版本可归档，构建/发布物/更新日志全部保留） | `PUT /api/project-versions/archive` | `id int` → `*IdResult` |
| `Remove` / `Delete` | 逻辑/物理删除（已发布/已归档版本禁止删除） | `DELETE /api/project-versions/{remove,delete}` | `ids []int` → `*IdsResult` |
| `Restore` | 恢复回收站数据 | `PUT /api/project-versions/restore` | `ids []int` → `*IdsResult` |

#### Modules - 项目功能模块（`/api/project-modules/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Rows` / `Find` / `Take` | 列表/分页/详情 | `GET /api/project-modules/{rows,find,take}` | `*ProjectModuleFindParams` / `id` → `ProjectModule` |
| `Create` | 新增（moduleCode 项目内唯一） | `POST /api/project-modules/create` | `ProjectModuleInput` → `*IdResult` |
| `Update` | 编辑 | `PUT /api/project-modules/update` | `ProjectModuleInput` → `*IdResult` |
| `Sort` | 修改排序（mode：`up`-上移 / `down`-下移） | `PUT /api/project-modules/sort` | `id int, mode string` → 无 |
| `Remove` / `Delete` | 逻辑/物理删除 | `DELETE /api/project-modules/{remove,delete}` | `ids []int` → `*IdsResult` |
| `Restore` | 恢复回收站数据 | `PUT /api/project-modules/restore` | `ids []int` → `*IdsResult` |

#### SaasMenus - SaaS 菜单清单（`/api/saas-menus/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Find` | 分页（按 project_id asc, version desc 排序） | `GET /api/saas-menus/find` | `*SaasMenuFindParams` → `*Page[SaasMenuManifest]` |
| `Take` | 按菜单轨取详情 | `GET /api/saas-menus/take?id=N&menuKind=tenant` | `id int, menuKind string` → `*SaasMenuManifest` |
| `Save` | 保存草稿（Id=0 新建递增版本草稿，否则更新既有 draft 行） | `POST /api/saas-menus/save` | `SaasMenuSaveInput` → `*SaasMenuSaveResult` |
| `Publish` | 分轨发布并返回删除编码、受影响套餐与租户 | `POST /api/saas-menus/publish` | `id int, menuKind string` → `*SaasMenuSaveResult` |
| `Archive` | 分轨强制归档（reason 必填；平台写审计留痕） | `POST /api/saas-menus/archive` | `id int, menuKind string, reason string` → 无 |

#### SaasFeatures - SaaS 功能字典（`/api/saas-features/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Find` / `Take` | 分页/详情 | `GET /api/saas-features/{find,take}` | `*SaasFeatureFindParams` / `id` → `SaasFeatureDict` |
| `Save` | 登记/修改（Id=0 登记；登记后 code 不可改；命中已软删的同 code 记录时恢复复用） | `POST /api/saas-features/save` | `SaasFeatureSaveInput` → `*IdResult` |
| `Disable` | 禁用（即时生效：套餐不可再引用，存量已签发信封不受影响） | `POST /api/saas-features/disable` | `id int` → 无 |
| `Delete` | 物理删除（被任一套餐引用时禁止删除只能禁用） | `DELETE /api/saas-features/delete` | `id int` → 无 |

#### SaasPlans - SaaS 套餐模板（`/api/saas-plans/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Find` / `Take` | 分页（按 sort asc, id desc）/详情 | `GET /api/saas-plans/{find,take}` | `*SaasPlanFindParams` / `id` → `SaasPlan` |
| `Create` | 新建（初始 draft；保存即做字典引用与菜单子集校验） | `POST /api/saas-plans/create` | `SaasPlanSaveInput` → `*PlanNoResult` |
| `Update` | 修改（planCode 不可改；不波及存量租户与在途申请） | `POST /api/saas-plans/update` | `SaasPlanSaveInput` → `*IdResult` |
| `Status` | 状态流转（draft→enabled 发布；enabled/disabled 互转；status 仅支持 enabled/disabled） | `POST /api/saas-plans/status` | `id int, status string, reason string` → `*StatusResult` |
| `Delete` | 逻辑删除（被租户引用禁止删除） | `DELETE /api/saas-plans/delete` | `id int` → 无 |

#### SaasTenants - SaaS 租户（`/api/saas-tenants/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Find` | 分页（member 限本人；platform 按范围策略） | `GET /api/saas-tenants/find` | `*SaasTenantFindParams` → `*Page[SaasTenant]` |
| `Take` | 详情 | `GET /api/saas-tenants/take?id=N` | `id int` → `*SaasTenant` |
| `TakePayload` | 查看租户授权原文（payload/signature，仅归属人/平台可见，可用本包 `TenantPayload` 解析验签） | `GET /api/saas-tenants/take-payload?id=N` | `id int` → `*SaasTenantPayloadView` |
| `Subscribe` | 开通申请（member 创建 pending 行 + 申请单，命中自动过单同事务生效；platform 直通生效） | `POST /api/saas-tenants/subscribe` | `SaasTenantSubscribeInput` → `*SaasTenantSubscribeResult` |
| `Change` | 权益变更申请（仅 active 可发起且一律人工审批；pending 租户为驳回后重新提审；platform 直通生效） | `POST /api/saas-tenants/change` | `SaasTenantChangeInput` → `*SaasTenantChangeResult` |
| `UpdateInfo` | 非权益字段直改（tenantName/contact 即时生效 + 审计） | `POST /api/saas-tenants/update-info` | `SaasTenantInfoUpdateInput` → `*IdResult` |
| `Cancel` | 撤回租户申请（仅本人 pending 可撤回） | `POST /api/saas-tenants/cancel` | `id int` → 无 |
| `Delete` | 删除（仅 pending 可删，软删；名下 pending 申请单一并流转 cancelled） | `DELETE /api/saas-tenants/delete` | `id int` → 无 |
| `Suspend` | 暂停（active → suspended，即时生效不设审批，reason 必填） | `POST /api/saas-tenants/suspend` | `id int, reason string` → `*StatusResult` |
| `Resume` | 恢复（suspended → active，即时生效，reason 必填） | `POST /api/saas-tenants/resume` | `id int, reason string` → `*StatusResult` |
| `Revoke` | 吊销（active/suspended → revoked，不可逆，reason 必填） | `POST /api/saas-tenants/revoke` | `id int, reason string` → `*StatusResult` |
| `Reissue` | 重签（以现载荷为基础按入参覆盖，空值沿用现载荷；直通不产生申请单） | `POST /api/saas-tenants/reissue` | `SaasTenantReissueInput` → `*SaasTenantNoResult` |
| `SyncMenus` | 按当前 published 租户清单裁剪悬空编码并原子重签；tenantIds 为空处理全项目 | `POST /api/saas-tenants/sync-menus` | `projectId int, tenantIds []int` → 无 |
| `BatchRenew` | 批量续期（仅 active/suspended 可续；member 逐租户生成 change 申请单走审批；platform 直通重签；ids 须全部处于写数据范围内否则整体拒绝） | `POST /api/saas-tenants/batch-renew` | `SaasTenantBatchRenewInput` → `*SaasTenantBatchRenewResult` |
| `Applications` | 我的申请分页 | `GET /api/saas-tenants/applications/find` | `*SaasTenantApplicationFindParams` → `*Page[SaasTenantApplication]` |
| `ApplicationTake` | 我的申请详情 | `GET /api/saas-tenants/applications/take?id=N` | `id int` → `*SaasTenantApplication` |
| `UsageFind` | 用量历史分页（按租户/额度项/时间段过滤） | `GET /api/saas-tenants/usage/find` | `*SaasTenantUsageFindParams` → `*Page[SaasTenantUsageRow]` |
| `UsageSummary` | 用量水位（每额度项最新上报值 vs 载荷 limits 上限） | `GET /api/saas-tenants/usage/summary?id=N` | `id int` → `*SaasTenantUsageSummary` |
| `HistoryExport` | 留痕导出（CSV 文本包裹在 JSON 内；tenantId/projectId 可选过滤，0=不过滤） | `GET /api/saas-tenants/history/export` | `tenantId int, projectId int` → `*SaasTenantHistoryExport` |

#### SaasReview - SaaS 租户申请审批（`/api/saas-review/*`）

| 方法 | 说明 | 路由 | 参数 → 返回 |
|---|---|---|---|
| `Find` | 审批队列分页（支持 status/bizType/userId/projectId 筛选；审批人 = 超管或持 `saas.review` 权限码用户） | `GET /api/saas-review/find` | `*SaasTenantApplicationFindParams` → `*Page[SaasTenantApplication]` |
| `Take` | 审批申请详情 | `GET /api/saas-review/take?id=N` | `id int` → `*SaasTenantApplication` |
| `Review` | 审批（approve 单事务生效并签发；reject 需填审批意见） | `POST /api/saas-review/review` | `SaasReviewInput` → `*SaasReviewResult` |

### 20.6 主要 DTO 说明（`admin-types.go`）

**输入结构**（json tag 与平台请求结构体逐一对齐）：

| 结构 | 关键字段与约束 |
|---|---|
| `QualificationApplyInput` | `Reason`（必填，最长 512）、`Contact`（必填，最长 128） |
| `QualificationReviewInput` | `Id`（必填）、`Action`（approve/reject）、`ReviewNote`（reject 必填）、`ProjectQuota`（个人配额，>=1 生效） |
| `ProjectInput` | `Id`（Update 必填）、`ProjectName`（Create 必填，最长 128）；归属用户由平台按登录态强制写入 |
| `InstanceInput` | `ProjectId`（Create 必填）、`ServerFingerprint`（原文提交，平台加盐哈希存储）、`NotifyUrl`（平台回调入口）、`IsBillableSet`（显式设置计费标记） |
| `LicenseApplyInput` | `ProjectId` / `LicenseType` / `Environment` / `Reason` 必填、`RequestPayload`（期望权益自由 JSON） |
| `LicenseIssuePayload` | 签发参数：四个期限为毫秒时间戳，0=不限制（`ValidUntil` 0=永久）、`Features` / `Limits` / `Binding` |
| `LicenseReviewInput` | `Id` / `Action` 必填、`IssuePayload`（approve 时可选，为空从申请的 requestPayload 回退解析） |
| `LicenseActionInput` | `Id` 必填、`Reason`（suspend/revoke 必填）、renew 传新期限、`IssuePayload`（reissue 生效） |
| `VersionInput` | 新建仅允许 draft/testing 状态；发布与归档走专用接口 |
| `ArtifactUploadInput` | `VersionId`（必填，版本须未发布/未归档）、`ArtifactType`（默认 full）、`SourceVersion` / `TargetVersion`（增量包）、`OsArch` |
| `SaasMenuSaveInput` | `Id`=0 新建分轨递增版本草稿、`ProjectId` / `MenuKind` / `Manifest` 必填 |
| `SaasFeatureSaveInput` | `Id`=0 登记、`FeatureCode`（小写字母/数字开头，段间可含 `. _ -`，登记后不可改） |
| `SaasPlanSaveInput` | `PlanCode`（创建后不可改）、`Features`（key 须命中功能字典 enabled）、`MenuCodes`（当前 published 租户清单编码，服务端物化祖先闭包） |
| `SaasTenantSubscribeInput` | `ProjectId` / `PlanId`（须 enabled）/ `TenantCode`（项目内唯一）/ `TenantName` / `SubscriptionType`（trial/official）/ `Environment` / `Reason` 必填；`Overrides.Menus` 只允许 `Remove` 裁剪 |
| `SaasTenantChangeInput` | `TenantId` / `PlanId` / `SubscriptionType` / `Environment` / `Reason` 必填 |
| `SaasTenantReissueInput` | `Id` 必填；其余字段非空覆盖（期限 0=保留原值） |
| `SaasTenantBatchRenewInput` | `Ids` 必填、`ValidUntil`（必填且须晚于当前时间）、`Reason` |
| `SaasReviewInput` | `Id` / `Action`（approve/reject）必填、`ReviewNote`（reject 必填） |
| `ProjectModuleInput` | `ProjectId` / `ModuleCode` / `ModuleName` 必填、`ParentCode`（父模块编码） |

**查询参数**（find/rows 类共用；数组字段（如 `Status []string`、`ProjectId []int`）序列化为 `key[]=v` 重复键；`Page` 默认 1，`Limit` 默认 10）：

| 结构 | 常用筛选字段 |
|---|---|
| `ProjectFindParams` | `ProjectName`（模糊）、`ProjectType` / `DeliveryMode` / `Status` / `LicenseMode`（IN）、`CreateTime` / `UpdateTime`（毫秒区间 Between）、`OnlyTrashed` / `WithTrashed`（仅管理员） |
| `InstanceFindParams` | `InstanceNo` / `Domain`（模糊）、`ProjectId` / `Environment` / `DeploymentType` / `NetworkMode` / `IsBillable`（IN）、`LastSeenAt`（区间） |
| `LicenseFindParams` | `Status`、`UserId`（审批视角）、`ProjectId` |
| `LicenseHistoryFindParams` | `LicenseId`（必填）、`Page`、`Limit` |
| `ActivationFindParams` | `LicenseId`、`Status`（审批视角生效） |
| `VersionFindParams` | `Version` / `BuildNumber` / `PipelineNo`（模糊）、`ProjectId` / `Status` / `OsArch`（IN）、`ReleasedAt` / `SupportUntil`（区间） |
| `ArtifactFindParams` | `ArtifactNo` / `FileName` / `Sha256`（模糊）、`VersionId` / `ArtifactType` / `ScanStatus` / `KeyVersion` / `OsArch` / `IsLocked`（IN） |
| `SaasTenantFindParams` | `ProjectId`、`UserId`（仅平台视角）、`Status`（pending/active/suspended/revoked）、`Environment` |
| `SaasTenantApplicationFindParams` | `ProjectId`、`UserId`（审批视角）、`BizType`（subscribe/change）、`Status` |
| `SaasTenantUsageFindParams` | `TenantId` / `ProjectId` / `LimitKey`、`StartTime` / `EndTime`（毫秒） |
| `QualificationFindParams` / `SaasMenuFindParams` / `SaasFeatureFindParams` / `SaasPlanFindParams` | `Status` 等常规筛选 |

**输出结构**（对齐平台 models/basic 各模型 json tag；时间戳除注明外均为毫秒）：

| 结构 | 说明 |
|---|---|
| `QualificationApplication` / `QualificationCurrent` | 资格申请 / 我的资格状态（状态：none/pending/approved/rejected/revoked） |
| `Project` / `DeploymentInstance` | 项目 / 部署实例（含 `LicenseStatus` 最近授权状态、`LastSeenAt` 心跳） |
| `License` | 许可证（`Payload` / `Signature` / `KeyVersion` 为签发快照，可用运行面包解析验签） |
| `LicenseApplication` / `LicenseHistory` / `Activation` | 授权申请（pending→issued/rejected/cancelled）/ 变更历史（issue/renew/suspend/revoke/reissue）/ 激活记录 |
| `LicensePayloadView` | 签发载荷视图（`LicenseNo` / `Payload` / `Signature` / `KeyVersion`） |
| `SigningKeyPublic` | 公钥导出（`Purpose` / `KeyVersion` / `Algorithm` / `PublicKey`） |
| `ProjectVersion` | 版本（状态：draft/testing/released/archived；灰度：`GrayMode` 空=全量/whitelist/percent + `GrayInstances` / `GrayPercent`） |
| `ProjectArtifact` | 发布物（`Url` / `Sha256` / `Signature` / `IsLocked`，签名即锁定） |
| `ArtifactVerifyResult` | 发布物验签结果（`HashMatch` / `SignatureValid` / `Valid`） |
| `SaasMenuManifest` / `SaasFeatureDict` / `SaasPlan` | 菜单清单（projectId+version 联合唯一，同一时刻仅一条 published）/ 功能字典 / 套餐（`Features` / `Limits` / `MenuCodes` 为 JSON 原文） |
| `SaasTenant` | 租户（状态机：pending → active ↔ suspended → revoked 不可逆；`Payload` / `Signature` 为签发快照） |
| `SaasTenantApplication` | 申请单（`BizType` subscribe/change、`RequestPayload` / `MergedPreview` 为 JSON 原文） |
| `SaasTenantSubscribeResult` / `SaasTenantChangeResult` | 开通/变更结果，三种形态：member 待审（`Id`=申请单 ID）/ 自动过单（`AutoApproved=true`）/ platform 直通 |
| `SaasTenantUsageRow` / `SaasTenantUsageSummary` / `SaasTenantHistoryExport` | 用量历史行（`HourBucket` 整点水位）/ 用量水位（`Limit`/`Value` 为指针，nil 表示未定义/未上报）/ 留痕 CSV 导出 |
| `ProjectModule` | 项目功能模块（`ModuleCode` 项目内唯一、`ParentCode` 父模块编码） |

## 21. 接口契约与兼容性约定

1. **信封版本与字段顺序**：所有签名载荷（`Payload` / `ManifestPayload` / `TenantPayload`）的字段顺序即签名内容——新增字段只允许追加到结构体末尾，禁止插入或调整既有字段顺序，否则历史签名全部失效。
2. **验签首选原文模式**：`ParseEnvelope` / `ParseManifest` / `ParseTenantEnvelope` 均返回载荷原始字节，`VerifyRaw` 基于原文验签。平台只追加新字段时重序列化会丢字段导致验签失败，原文验签天然兼容。
3. **签名算法与密钥**：固定 Ed25519，签名 hex 编码；验签公钥按载荷 `KeyVersion` 从 `PublicKeys` / `ReleasePublicKeys` 选取（支持新旧公钥并存轮换过渡）。你永远拿不到平台的签名私钥，签发只在平台进行。
4. **时间戳约定**：运行面信封与租户载荷的期限字段为 RFC3339 字符串（空串 = 不限制）；管理面 DTO 与请求参数的时间戳均为**毫秒**；`Token.Expired` 为毫秒时间戳（平台注释误标为秒，以毫秒为准）。
5. **运行面请求签名**：每个请求自动携带 `X-License-Token` / `X-License-Timestamp` / `X-License-Nonce` / `X-License-Sign` 四头，签名内容 = `method\nuri\ntimestamp\nnonce\nsha256hex(body)`；gRPC 传输额外携带第 5 个头 `X-License-Sign-Version`（签名版本），签名内容 = `GRPC\nfullMethod\ntimestamp\nnonce\nsha256hex(确定性 protobuf 字节)`。时间戳用服务端校时（±5 分钟时间窗），nonce 防重放，开发者零感知。
6. **管理面协议**：HTTP 状态码恒为 200，业务结果看 `code`（200=成功；400 参数错误；401 未登录/登录失效；403 无权限；404 不存在；409 状态冲突）；GET 走 query，POST/PUT/DELETE 走 JSON body；数组查询参数序列化为 `key[]=v` 重复键；自动登录、令牌过期预判重登、业务 401 自动重登并重试一次。账密明文上送，必须 HTTPS；平台开启「API 签名验证」时管理面客户端不支持。
7. **降级策略**：运行面断网/服务端故障时按本地缓存信封 + 本地时间判定（`GRACE` → `EXPIRED`）；`CLOCK_TAMPERED` 只告警不停服务。SaaS 租户的 fail-open/fail-closed 由服务商依据 `TenantStatus` 自行决策。
8. **安全存储**：token、客户端私钥、信封缓存只允许经 `Store` 持久化（默认 AES-256-GCM 加密文件，密钥派生自 项目盐 + 实例指纹，权限 0600）；token 仅 activate 返回一次，丢失只能重新激活。

## 22. 附：包内文件与能力映射

| 文件 | 能力 |
|---|---|
| `envelope.go` / `sign.go` / `licence.go` | 纯函数层：信封结构、签发、验签、密钥对、nonce |
| `status.go` | 运行面状态码与本地时间判定 |
| `version-range.go` | 版本范围表达式判定 |
| `fingerprint*.go` | 实例指纹采集与哈希（按平台 build tags 分文件） |
| `store.go` | 安全存储接口与默认加密文件实现 |
| `client.go` / `transport.go` | 运行面客户端：生命周期、后台刷新、请求签名、信封缓存 |
| `manifest.go` / `update.go` | 在线更新：清单结构、检查、下载、升级上报 |
| `tenant.go` / `saas.go` | SaaS 租户：信封结构、同步、校验、本地判定 |
| `callback.go` | 回调接收：验签、防重放、幂等分发 |
| `events.go` | 事件订阅：`EventSubscriber` 增量拉取、水位推进、HTTP/gRPC 双传输 |
| `platform-config.go` | 平台配置签名同步与本地快照（`PlatformConfigSync`/`PlatformConfig`/`PlatformConfigMust`） |
| `admin.go` / `admin-response.go` / `admin-types.go` | 管理面：登录态、请求出口、错误分层、DTO |
| `qualification.go` / `projects.go` / `instances.go` / `licenses.go` / `signingkeys.go` / `artifacts.go` / `versions.go` / `projectmodules.go` / `saasmenus.go` / `saasfeatures.go` / `saasplans.go` / `saastenants.go` / `saasreview.go` | 管理面 13 个资源组 |
