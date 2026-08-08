package pushx

import (
	"errors"
	"fmt"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 短信宝 - 开始 ==================================

// SmsbaoSender - 短信宝短信驱动
type SmsbaoSender struct {
	// 短信宝账号
	Account string
	// 短信宝API密钥
	ApiKey string
	// 短信签名
	SignName string
	// 接口地址
	BaseUrl string
	// 配置
	Config Config
}

// newSmsbaoSender - 短信宝短信驱动工厂
func newSmsbaoSender(config Config) (Sender, error) {
	return &SmsbaoSender{
		Account:  config.Smsbao.Account,
		ApiKey:   config.Smsbao.ApiKey,
		SignName: config.Smsbao.SignName,
		BaseUrl:  config.Smsbao.BaseUrl,
		Config:   config,
	}, nil
}

// Send - 发送验证码
func (this *SmsbaoSender) Send(message Message) (*Response, error) {

	// 短信驱动只发手机号目标，路由分发由 Router 负责
	if !utils.Is.Phone(message.Target) {
		return nil, errors.New("pushx: 目标手机号格式无效")
	}

	if this == nil {
		return nil, errors.New("pushx: 短信宝驱动未初始化")
	}

	message = normMessage(message)

	if utils.Is.Empty(this.ApiKey) {
		return nil, errors.New("pushx: 短信宝API密钥不能为空")
	}

	if utils.Is.Empty(this.Account) {
		return nil, errors.New("pushx: 短信宝账号不能为空")
	}

	// 默认模板未自定义时兜底，变量由 Render 统一替换
	template := utils.Default(message.Template, fmt.Sprintf("【%s】您的验证码是：${code}，有效期${expired}分钟。（打死也不要把验证码告诉别人）", this.SignName))

	item := utils.Http(utils.HttpRequest{
		Method: "GET",
		Url:    fmt.Sprintf("%s/sms", this.BaseUrl),
		Query: map[string]any{
			"u": this.Account,
			"p": this.ApiKey,
			"m": message.Target,
			"c": message.Render(template),
		},
	}).Send()

	if item.Error != nil {
		return nil, item.Error
	}

	if cast.ToInt(item.Text) != 0 {
		return nil, errors.New("pushx: 短信宝发送失败")
	}

	return &Response{
		Text:       item.Text,
		VerifyCode: message.Code,
	}, nil
}

// ================================== 短信宝 - 结束 ==================================
