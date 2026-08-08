# Licen Hub 授权平台 Go SDK 使用教程

> 包路径：`github.com/inis-io/aide/licence`（Licen Hub 授权平台官方 Go SDK，仅支持 Go）。
> 本教程面向**接入授权平台的商户/项目开发者**，读完即可完成集成。
> 接口契约细节见平台文档《许可证运行面接口契约》（licen-hub 仓库 `docs/md/`），本文只讲怎么用。

## 1. 开始前：你需要从平台方拿到什么

| 材料 | 是什么 | 从哪里获得 |
|---|---|---|
| **平台地址 `ServerURL`** | 授权平台的访问地址 | 平台方提供，如 `https://licen-hub.inis.cn` |
| **许可证编号 `licenseNo`** | 你的部署实例对应的许可证，格式 `LIC-{年}-XXXXXX` | 平台管理端「我的许可证」页签发后可见（先完成注册 → 资格审核 → 建项目/实例 → 授权申请 → 平台审批签发） |
| **指纹盐 `Salt`** | 实例指纹的盐值 | 你在平台登记部署实例时自己定的盐；二者必须一致 |
| **验签公钥 `PublicKeys`** | 平台 license-key 的 Ed25519 公钥（hex），用于验证许可证信封是真的 | 管理端登录后 `GET /api/licenses/public-key` 导出（或找平台方索取），内置到你的程序里 |
| **release 公钥 `ReleasePublicKeys`** | release-key 公钥（hex），验证更新清单与发布物 | 同上，`GET /api/signing-keys/public?purpose=release`；**只用在线更新功能时才需要** |
| **管理面账号密码** | 平台后台账号 | **仅 CI/运维自动化才需要**（查项目、申请授权、导公钥等）；禁止随交付项目分发 |

安全约定（SDK 已强制，了解一下即可）：

- 你**永远拿不到**平台的签名私钥，也不需要——签发只在平台进行；
- 首次启动时 SDK 会在本机生成一对 Ed25519 密钥（请求签名用），私钥加密存本机、不出机器、不写日志；
- 激活令牌（`activationToken`）同样加密存储，丢失时 SDK 自动重新激活换取。

## 2. 安装

```bash
go get github.com/inis-io/aide
```

```go
import "github.com/inis-io/aide/licence"
```

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
		PublicKeys: map[string]string{              // 验签公钥（必填，内置）
			"license-key-2026-01": "79b5562e8fe6…（从平台导出的公钥 hex）",
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
	defer client.Stop()

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
syncTime, manifest, err := client.TenantSync(ctx, 0) // 之后传上次返回的 syncTime

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
pub, _ := admin.SigningKeys.Public(ctx, "license", "")   // 导出验签公钥
verify, _ := admin.Artifacts.VerifyWithFile(ctx, 3, "app.tar.gz", file) // 服务端代验
```

资源清单：`Qualification`（资格申请/审批）、`Projects`、`Instances`、`Licenses`（申请/审批/续期/暂停/吊销/重签/载荷/公钥/激活记录）、`SigningKeys`、`Artifacts`、`Versions`（含发布/归档）。

注意：管理面账密按明文 JSON 上送，**必须走 HTTPS**。

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
`StorageDir`（默认 `./runtime/licence`）下的 `licence-<licenseNo>.state`，AES-256-GCM 加密（密钥派生自盐+指纹），权限 0600。token、私钥、信封都在里面。想接系统密钥库（DPAPI/Keychain/keyring）就实现 `licence.Store` 接口注入。

**Q：平台轮换密钥了怎么办？**
`PublicKeys`/`ReleasePublicKeys` 是 map，把新旧公钥都放进去即可平滑过渡（SDK 按载荷 `keyVersion` 自动选钥）。

## 9. 排错速查

| 现象 | 先查什么 |
|---|---|
| `Start` 报「许可证或实例信息无效」 | `licenseNo` 是否正确；许可证是否已签发且未吊销 |
| `Start` 报「未内置 keyVersion=… 的验签公钥」 | 公钥 map 的 key 必须等于平台的 `keyVersion`（如 `license-key-2026-01`） |
| `信封验签失败` | 公钥是否配对、是否最新；找平台方重新导出 |
| 一直 `GRACE` | 交付机网络到平台不通，或许可证已过 `validUntil` |
| `INSTANCE_MISMATCH` | 换了机器/指纹源变了；检查 `Salt` 是否与实例登记一致 |
| 管理面 401 | 账号密码/2FA；或 token 过期（SDK 会自动重登一次） |

## 10. 一图流（集成清单）

1. 平台方开通账号 → 资格审核通过
2. 建项目 → 登记实例（记下你的指纹盐）→ 申请授权 → 平台签发
3. 拿到 `licenseNo` + 导出 `PublicKeys`（用更新功能再导出 `ReleasePublicKeys`）
4. `licence.New` + `Start` → 用 `HasFeature/GetLimit/CheckVersion` 做业务闸门
5.（可选）`CheckUpdate`/`DownloadArtifact`/`ReportUpgrade` 接在线更新
6.（可选 SaaS）`TenantSync`/`TenantValidate` 接多租户
7.（可选 CI）`NewAdmin` 做运维自动化
