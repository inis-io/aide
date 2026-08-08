package pushx

import (
	"errors"
	"sort"

	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	TencentCloud "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
)

// ================================== 腾讯云短信 - 开始 ==================================

// TencentSender - 腾讯云短信驱动
type TencentSender struct {
	// 腾讯云短信客户端
	Client *TencentCloud.Client
	// 配置
	Config Config
}

// newTencentSender - 腾讯云短信驱动工厂
func newTencentSender(config Config) (Sender, error) {

	credential := common.NewCredential(config.Tencent.SecretId, config.Tencent.SecretKey)
	clientProfile := profile.NewClientProfile()
	// sms.tencentcloudapi.com
	clientProfile.HttpProfile.Endpoint = config.Tencent.Endpoint
	// ap-guangzhou
	client, err := TencentCloud.NewClient(credential, config.Tencent.Region, clientProfile)
	if err != nil {
		return nil, err
	}

	return &TencentSender{Client: client, Config: config}, nil
}

// tencentTemplateParams - 组装腾讯云模板参数数组：
// Params 为空时默认 [验证码]；否则按数字键名（"1"、"2"...，对应云端模板的 {1}、{2}）升序取值，完全接管参数列表
func tencentTemplateParams(message Message) []string {

	if len(message.Params) == 0 {
		return []string{message.Code}
	}

	keys := make([]string, 0, len(message.Params))
	for key := range message.Params {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return cast.ToInt(keys[i]) < cast.ToInt(keys[j])
	})

	params := make([]string, 0, len(keys))
	for _, key := range keys {
		params = append(params, cast.ToString(message.Params[key]))
	}
	return params
}

// Send - 发送验证码
func (this *TencentSender) Send(message Message) (*Response, error) {

	// 短信驱动只发手机号目标，路由分发由 Router 负责
	if !utils.Is.Phone(message.Target) {
		return nil, errors.New("pushx: 目标手机号格式无效")
	}

	if this == nil || this.Client == nil {
		return nil, errors.New("pushx: 腾讯云短信客户端未初始化")
	}

	message = normMessage(message)

	// 实例化一个请求对象,每个接口都会对应一个request对象
	request := TencentCloud.NewSendSmsRequest()

	request.PhoneNumberSet = common.StringPtrs([]string{message.Target})
	request.SmsSdkAppId = common.StringPtr(this.Config.Tencent.SmsSdkAppId)
	request.SignName = common.StringPtr(this.Config.Tencent.SignName)
	request.TemplateId = common.StringPtr(this.Config.Tencent.VerifyCode)
	request.TemplateParamSet = common.StringPtrs(tencentTemplateParams(message))

	item, err := this.Client.SendSms(request)
	if err != nil {
		return nil, err
	}

	if item.Response == nil {
		return nil, errors.New("pushx: 腾讯云短信响应为空")
	}

	if len(item.Response.SendStatusSet) == 0 {
		return nil, errors.New("pushx: 腾讯云短信响应状态为空")
	}

	if item.Response.SendStatusSet[0].Code == nil {
		return nil, errors.New("pushx: 腾讯云短信响应状态码为空")
	}

	if *item.Response.SendStatusSet[0].Code != "Ok" {
		return nil, errors.New(cast.ToString(item.Response.SendStatusSet[0].Message))
	}

	return &Response{
		Result:     utils.Json.Decode(item.ToJsonString()),
		Text:       item.ToJsonString(),
		VerifyCode: message.Code,
	}, nil
}

// ================================== 腾讯云短信 - 结束 ==================================
