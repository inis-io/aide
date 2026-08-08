package pushx

import (
	"time"

	"github.com/inis-io/aide/utils"
)

// Message - 推送消息体
type Message struct {
	// 目标手机号或邮箱
	Target string
	// 自定义验证码
	Code string
	// 验证码长度
	Length int
	// 发送模板（仅本地渲染的驱动生效：email / smsbao；阿里云、腾讯云为云端模板，此字段无效）
	Template string
	// 自定义模板变量参数：
	// 本地渲染驱动（email / smsbao）在 Render 中以 ${键名} 使用；
	// 阿里云按键名合并进模板变量 JSON（默认含 code / time）；
	// 腾讯云为位置参数，按数字键名（"1"、"2"...）升序组装为参数数组，提供后完全接管（不再自动附带验证码）
	Params map[string]any
	// 主题（标题）
	Subject string
	// 昵称（发件人昵称）
	Nickname string
	// 用户名（收件人昵称）
	Username string
	// 过期时间（分钟）
	Expired int64
	// 通信地址
	Address string
	// 标题
	Title string
}

// Response - 推送响应
type Response struct {
	// 结果
	Result any
	// 文本
	Text string
	// 验证码
	VerifyCode string
}

// mergeMessage - 合并消息体（以 current 为基础，用 override 的非零值覆盖；Params 按键名逐条合并）
func mergeMessage(current Message, override Message) Message {
	current.Code = utils.Default(override.Code, current.Code)
	current.Length = utils.Default(override.Length, current.Length)
	current.Target = utils.Default(override.Target, current.Target)
	current.Template = utils.Default(override.Template, current.Template)
	current.Title = utils.Default(override.Title, current.Title)
	current.Subject = utils.Default(override.Subject, current.Subject)
	current.Nickname = utils.Default(override.Nickname, current.Nickname)
	current.Username = utils.Default(override.Username, current.Username)
	if override.Expired != 0 {
		current.Expired = override.Expired
	}
	current.Address = utils.Default(override.Address, current.Address)
	if len(override.Params) > 0 {
		if current.Params == nil {
			current.Params = make(map[string]any, len(override.Params))
		}
		for key, val := range override.Params {
			current.Params[key] = val
		}
	}
	return current
}

// normMessage - 统一消息体默认值，验证码为空时自动生成（链式入口由 Driver.Send 统一调用；内置驱动各自兜底，保证直接使用 Sender 的场景同样归一）
func normMessage(message Message) Message {
	if message.Length <= 0 {
		message.Length = 6
	}
	if message.Expired <= 0 {
		message.Expired = 5
	}
	// 如果自定义验证码为空，则生成一个验证码
	if utils.Is.Empty(message.Code) {
		message.Code = utils.Rand.Code(message.Length)
	}
	return message
}

// Render - 渲染模板：将模板中的 ${变量} 占位符替换为消息体字段值
/**
 * 内置变量：${target} ${code} ${length} ${expired} ${subject} ${nickname} ${username} ${title} ${address} ${year}
 * 自定义变量：Params 中的每个键名可作为 ${键名} 使用；extra 为驱动级附加变量
 * 覆盖顺序（后者优先）：内置变量 < Params < extra
 * @param template string - 模板内容（占位符格式 ${变量名}，未识别的占位符保留原样）
 * @param extra ...map[string]any - 驱动级附加变量
 * @return string - 渲染后的内容
 * @example：
 * 	pushx.Message{Code: "1234", Expired: 5}.Render("验证码 ${code}，${expired} 分钟内有效")
 */
func (this Message) Render(template string, extra ...map[string]any) string {

	vars := map[string]any{
		"${target}":   this.Target,
		"${code}":     this.Code,
		"${length}":   this.Length,
		"${expired}":  this.Expired,
		"${subject}":  this.Subject,
		"${nickname}": this.Nickname,
		"${username}": this.Username,
		"${title}":    this.Title,
		"${address}":  this.Address,
		"${year}":     time.Now().Format("2006"),
	}

	// 自定义模板变量（覆盖内置变量）
	for key, val := range this.Params {
		vars["${"+key+"}"] = val
	}

	// 驱动级附加变量（优先级最高）
	for _, item := range extra {
		for key, val := range item {
			vars[key] = val
		}
	}

	return utils.Replace(template, vars)
}
