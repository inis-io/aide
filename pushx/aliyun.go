package pushx

import (
	"errors"
	"strings"

	AliYunOpenApi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	AliYunSmsApi "github.com/alibabacloud-go/dysmsapi-20170525/v5/client"
	AliYunUtilV2 "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	AliYunCredential "github.com/aliyun/credentials-go/credentials"
	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
)

// ================================== 阿里云短信 - 开始 ==================================

// AliYunSender - 阿里云短信驱动
type AliYunSender struct {
	// 阿里云短信客户端
	Client *AliYunSmsApi.Client
	// 配置
	Config Config
}

// newAliYunSender - 阿里云短信驱动工厂
func newAliYunSender(config Config) (Sender, error) {

	// 创建访问凭证
	credential, err := AliYunCredential.NewCredential(nil)
	// 凭证创建失败
	if err != nil {
		return nil, err
	}

	// 创建客户端
	client, err := AliYunSmsApi.NewClient(&AliYunOpenApi.Config{
		Credential: credential,
		// 访问的域名 dysmsapi.aliyuncs.com
		Endpoint: tea.String(config.AliYun.Endpoint),
		// 必填，您的 AccessKey ID
		AccessKeyId: tea.String(config.AliYun.AccessKeyId),
		// 必填，您的 AccessKey Secret
		AccessKeySecret: tea.String(config.AliYun.AccessKeySecret),
	})
	// 客户端创建失败
	if err != nil {
		return nil, err
	}

	return &AliYunSender{Client: client, Config: config}, nil
}

// aliYunTemplateParams - 组装阿里云模板变量：默认含 code / time，调用方 Params 按键名合并（同名覆盖）
func aliYunTemplateParams(message Message) map[string]any {
	vars := map[string]any{
		"code": message.Code,
		"time": message.Expired,
	}
	for key, val := range message.Params {
		vars[key] = val
	}
	return vars
}

// Send - 发送验证码
func (this *AliYunSender) Send(message Message) (*Response, error) {

	// 短信驱动只发手机号目标，路由分发由 Router 负责
	if !utils.Is.Phone(message.Target) {
		return nil, errors.New("pushx: 目标手机号格式无效")
	}

	if this == nil || this.Client == nil {
		return nil, errors.New("pushx: 阿里云短信客户端未初始化")
	}

	message = normMessage(message)

	params := &AliYunSmsApi.SendSmsRequest{
		PhoneNumbers:  tea.String(message.Target),
		SignName:      tea.String(this.Config.AliYun.SignName),
		TemplateCode:  tea.String(this.Config.AliYun.VerifyCode),
		TemplateParam: tea.String(utils.Json.Encode(aliYunTemplateParams(message))),
	}

	resp, err := this.Client.SendSmsWithOptions(params, &AliYunUtilV2.RuntimeOptions{})
	if err != nil {
		return nil, err
	}

	if resp == nil || resp.Body == nil || resp.Body.Code == nil {
		return nil, errors.New("pushx: 阿里云短信响应为空")
	}

	if strings.ToLower(*resp.Body.Code) != "ok" {
		return nil, errors.New(cast.ToString(tea.StringValue(resp.Body.Message)))
	}

	return &Response{
		Result:     cast.ToStringMap(*resp.Body),
		Text:       utils.Json.Encode(*resp.Body),
		VerifyCode: message.Code,
	}, nil
}

// ================================== 阿里云短信 - 结束 ==================================
