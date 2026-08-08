package pushx

import (
	"crypto/tls"
	"errors"

	"github.com/inis-io/aide/utils"
	"gopkg.in/gomail.v2"
)

// ================================== GoMail邮件服务 - 开始 ==================================

// EmailSender - 邮件驱动（基于 gomail）
type EmailSender struct {
	// 邮件客户端
	Client *gomail.Dialer
	// 配置
	Config Config
}

// newEmailSender - 邮件驱动工厂
func newEmailSender(config Config) (Sender, error) {
	item := &EmailSender{Config: config}
	item.Client = gomail.NewDialer(config.Email.Host, config.Email.Port, config.Email.Account, config.Email.Password)
	// 确保在STARTTLS/implicit-TLS流程中，TLS握手使用正确的服务器名称。
	item.Client.TLSConfig = &tls.Config{ServerName: config.Email.Host}
	return item, nil
}

// Send - 发送验证码
func (this *EmailSender) Send(message Message) (*Response, error) {

	// 邮件驱动只发邮箱目标，路由分发由 Router 负责
	if !utils.Is.Email(message.Target) {
		return nil, errors.New("pushx: 目标邮箱格式无效")
	}

	if this == nil || this.Client == nil {
		return nil, errors.New("pushx: 邮件客户端未初始化")
	}

	message = normMessage(message)

	// 频道默认值回填，保证渲染结果与最终取值一致
	message.Subject = utils.Default(message.Subject, this.Config.Email.Subject)
	message.Nickname = utils.Default(message.Nickname, this.Config.Email.Nickname)

	item := gomail.NewMessage()
	// 设置邮件内容类型
	item.SetHeader("Content-Type", "text/html; charset=UTF-8")
	// 设置发件人
	item.SetAddressHeader("From", this.Config.Email.Account, message.Nickname)
	// 设置收件人
	item.SetHeader("To", message.Target)
	// 设置邮件主题
	item.SetHeader("Subject", message.Subject)
	// 渲染邮件正文（${email} 为发件人账号，属驱动级变量）
	item.SetBody("text/html", message.Render(utils.Default(message.Template, TempEmailCode), map[string]any{
		"${email}": this.Config.Email.Account,
	}))

	// 发送邮件
	// 使用明确的 “拨号 + 发送” 操作，以确保在执行AUTH和MAIL命令之前完成SMTP握手（EHLO/STARTTLS）。
	sender, err := this.Client.Dial()
	if err != nil {
		return nil, err
	}

	defer func() { _ = sender.Close() }()

	if err := gomail.Send(sender, item); err != nil {
		return nil, err
	}

	return &Response{
		VerifyCode: message.Code,
	}, nil
}

// ================================== GoMail邮件服务 - 结束 ==================================
