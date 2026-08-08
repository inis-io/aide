# pushx - 消息推送

> 包路径：`github.com/inis-io/aide/pushx`
> 以接口模式封装短信 / 邮件验证码推送，注册表 + 链式调用，可自由扩展服务商。

## 1. 特性

- **接口模式**：`Sender` 接口是唯一扩展点，新服务商实现 `Send(Message)` 即可接入
- **内置驱动**：`email`（SMTP 邮件）、`aliyun`（阿里云短信）、`tencent`（腾讯云短信）、`smsbao`（短信宝）
- **智能路由**：按目标类型（邮箱 / 手机号）自动分发到邮件或短信驱动
- **链式调用**：值语义，每次调用返回副本，并发安全，无需 clone
- **模板渲染**：本地渲染驱动支持 `${变量}` 自定义模板；云端驱动提交模板 ID 与变量参数
- **全局门面**：控制器单例 + 全局实例，支持配置热重载

## 2. 安装

```bash
go get github.com/inis-io/aide/pushx
```

```go
import "github.com/inis-io/aide/pushx"
```

## 3. 快速开始

```go
package main

import (
	"fmt"

	"github.com/inis-io/aide/pushx"
)

func main() {

	// 1) 初始化全局推送服务（应用启动时执行一次）
	pushx.Inst.Init(pushx.Config{
		Engine: pushx.EngineConfig{
			Email: "email",   // 邮件通道驱动（默认 email）
			SMS:   "aliyun",  // 短信通道驱动（默认 aliyun）
		},
		Email: pushx.EmailConfig{
			Host:     "smtp.qq.com",
			Port:     465,
			Account:  "your@qq.com",
			Password: "SMTP 授权码（非邮箱密码）",
			Nickname: "系统通知",
			Subject:  "验证码",
		},
		AliYun: pushx.AliYunConfig{
			AccessKeyId:     "阿里云 AccessKey ID",
			AccessKeySecret: "阿里云 AccessKey Secret",
			SignName:        "短信签名",
			VerifyCode:      "SMS_123456789", // 云端验证码模板 ID
		},
	})

	// 2) 智能路由发送：目标是手机号走短信驱动，是邮箱走邮件驱动
	resp, err := pushx.Push.Target("13800000000").Send()
	if err != nil {
		panic(err)
	}
	fmt.Println("验证码：", resp.VerifyCode)

	// 3) 链式定制：自定义验证码长度、有效期、模板变量
	resp, err = pushx.Push.
		Target("user@example.com").
		Len(4).             // 验证码长度（默认 6）
		Expired(10).        // 有效期分钟（默认 5）
		Param("app", "商城").
		Send()

	// 4) 也可以直发：Send 的实参优先级最高，等价于 Target(...).Send()
	resp, err = pushx.Push.Send("13800000000")
	_, _ = resp, err
}
```

## 4. 独立实例

不经过全局门面，按驱动名与配置直接创建（适合多租户、多账号场景）：

```go
driver, err := pushx.New("tencent", pushx.Config{
	Tencent: pushx.TencentConfig{
		SecretId:    "腾讯云 SecretId",
		SecretKey:   "腾讯云 SecretKey",
		SmsSdkAppId: "短信应用 ID",
		SignName:    "短信签名",
		VerifyCode:  "1234567", // 云端验证码模板 ID
	},
})
if err != nil {
	panic(err)
}

resp, err := driver.Target("13800000000").Send()
```

## 5. 链式方法

| 方法 | 说明 |
|---|---|
| `Target(target)` | 目标手机号或邮箱 |
| `Code(code)` | 自定义验证码（为空时按 Length 自动生成） |
| `Len(length)` | 验证码长度（默认 6） |
| `Expired(minutes)` | 验证码有效期，分钟（默认 5） |
| `Subject(subject)` | 主题（邮件标题） |
| `Template(template)` | 自定义模板（**仅本地渲染驱动 email / smsbao 生效**；阿里云、腾讯云为云端模板，此方法无效） |
| `Param(key, value)` | 自定义模板变量（各驱动语义不同，见下节） |
| `SetMessage(message)` | 批量设置消息体（非零字段覆盖） |
| `Send(target ...any)` | 发送；实参为目标，优先级最高 |
| `Sender()` | 取出底层驱动（供类型断言访问驱动特有方法） |

链式调用为**值语义**：每次调用返回副本，互不影响，可安全复用中间状态。

```go
base := pushx.Push.Len(4)
base.Target("13800000000").Send()   // 4 位验证码
base.Target("user@a.com").Send()    // 互不影响
```

## 6. 模板与变量替换

### 6.1 本地渲染驱动（email / smsbao）

模板中用 `${变量名}` 占位，由 `Message.Render` 统一渲染，未识别的占位符保留原样：

```go
pushx.Push.
	Target("user@example.com").
	Template("【${app}】您的验证码是 ${code}，${expired} 分钟内有效。").
	Param("app", "商城").
	Send()
```

内置变量（无需 Param 即可使用）：

| 变量 | 含义 | 变量 | 含义 |
|---|---|---|---|
| `${target}` | 目标 | `${subject}` | 主题 |
| `${code}` | 验证码 | `${nickname}` | 发件人昵称 |
| `${length}` | 验证码长度 | `${username}` | 收件人昵称 |
| `${expired}` | 有效期（分钟） | `${title}` | 标题 |
| `${address}` | 通信地址 | `${year}` | 当前年份 |

覆盖顺序（后者优先）：**内置变量 < `Param` 自定义变量 < 驱动级附加变量**。

### 6.2 阿里云短信（云端模板）

模板在阿里云控制台维护，本地只提交**模板 ID** 与变量参数。`Param` 按键名合并进模板变量 JSON（默认已含 `code`、`time`，同名覆盖）：

```go
// 云端模板：您的验证码为：${code}，您正在登录【${app}】，${time}分钟内有效。
pushx.Push.
	Target("13800000000").
	Param("app", "商城").   // 合并进模板变量 JSON：{"code":"888123","time":5,"app":"商城"}
	Send()
```

### 6.3 腾讯云短信（云端模板）

腾讯云为**位置参数**，`Param` 按数字键名（`"1"`、`"2"`...，对应云端模板 `{1}`、`{2}`）升序组装为参数数组；一旦提供即完全接管，不再自动附带验证码：

```go
// 云端模板：您的验证码为：{1}，请于 {2} 分钟内填写。
pushx.Push.
	Target("13800000000").
	Param("1", "888123").
	Param("2", "5").
	Send()
```

## 7. 消息体与响应

`pushx.Message` 字段：`Target`、`Code`、`Length`、`Template`、`Params`、`Subject`、`Nickname`、`Username`、`Expired`、`Address`、`Title`。驱动发送前统一由 `normMessage` 补齐默认值（长度 6、有效期 5 分钟、空验证码自动生成）。

`pushx.Response` 字段：

| 字段 | 说明 |
|---|---|
| `VerifyCode` | 实际发送的验证码（自定义或自动生成） |
| `Result` | 驱动原始响应（各服务商结构不同） |
| `Text` | 文本回执（邮件渲染内容 / 短信宝返回文本等） |

## 8. 配置项

`pushx.Config`：

| 字段 | 说明 |
|---|---|
| `Engine` | 双通道引擎选择：`Engine.Email`（默认 `email`）、`Engine.SMS`（默认 `aliyun`），填未注册名回退默认 |
| `Email` | 邮件：`Host`（默认 `smtp.qq.com`）、`Port`（默认 465）、`Account`、`Password`（SMTP 授权码）、`Nickname`、`Subject` |
| `AliYun` | 阿里云：`AccessKeyId`、`AccessKeySecret`、`Endpoint`（默认 `dysmsapi.aliyuncs.com`）、`SignName`、`VerifyCode`（模板 ID） |
| `Tencent` | 腾讯云：`SecretId`、`SecretKey`、`Endpoint`（默认 `sms.tencentcloudapi.com`）、`SmsSdkAppId`、`SignName`、`VerifyCode`（模板 ID）、`Region`（默认 `ap-guangzhou`） |
| `Smsbao` | 短信宝：`Account`、`ApiKey`、`SignName`、`BaseUrl`（默认 `https://api.smsbao.com`） |
| `Options` | 扩展驱动的自定义配置（`map[驱动名]map[string]any`） |
| `Hash` | 配置变更指纹（不传自动计算） |

凭据全部运行时注入，请勿硬编码进仓库。

## 9. 扩展新服务商

实现 `Sender` 接口并在自己包内注册：

```go
package qcloud

import "github.com/inis-io/aide/pushx"

type sender struct{ config pushx.Config }

// Send - 实现推送逻辑（经 Driver 链式入口的消息体已归一化：长度、有效期、空验证码已就绪）
func (this sender) Send(message pushx.Message) (*pushx.Response, error) {
	// ... 调用服务商 API
	return &pushx.Response{VerifyCode: message.Code}, nil
}

func newSender(config pushx.Config) (pushx.Sender, error) {
	// 自定义配置从 config.Options["qcloud"] 读取
	return sender{config: config}, nil
}

func init() {
	pushx.Register("qcloud", newSender) // 同名注册会覆盖先注册者
}
```

约定：

- 各驱动**只发自己通道**，发送前校验目标类型（邮件驱动校验邮箱、短信驱动校验手机号）；跨通道路由由 `Router` 统一负责，驱动不要做跨通道路由
- 本地渲染模板必须走 `Message.Render`，不要自写替换 map
- 注册后：`pushx.New("qcloud", config)` 可用；`Config.Engine.Email / Engine.SMS` 填 `"qcloud"` 即可接入全局门面

## 10. 全局门面与热重载

| 入口 | 说明 |
|---|---|
| `pushx.Inst` | 控制器单例：`Init(config)` 注入配置、`ReloadIfChanged()` 按 Hash 热重载 |
| `pushx.Push` | 全局链式实例（智能路由） |
| `pushx.Email` / `pushx.SMS` | 当前邮件 / 短信驱动（`Sender` 接口） |

驱动初始化失败时全局位用错误占位实现，`Send` 时返回原始初始化错误，不会静默吞错。
