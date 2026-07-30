package facade

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
	"time"
	
	AliYunOpenApi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	AliYunSmsApi "github.com/alibabacloud-go/dysmsapi-20170525/v5/client"
	AliYunUtilV2 "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	AliYunCredential "github.com/aliyun/credentials-go/credentials"
	"github.com/inis-io/aide/dto"
	"github.com/inis-io/aide/utils"
	"github.com/spf13/cast"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	TencentCloud "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/sms/v20210111"
	"gopkg.in/gomail.v2"
)

var SmsInst = &SmsClass{}

type SmsClass struct {
	// 记录配置 Hash 值，用于检测配置文件是否有变化
	Hash 	  string        `json:"hash"`
	// 当前短信配置（由调用方注入）
	Config    dto.SmsConfig `json:"config"`
	// 是否已经注入过配置
	HasConfig bool          `json:"hasConfig"`
}

func init() { SmsInst.Init() }

// normConfig - 统一配置默认值，避免不同项目接入时行为不一致
func (this *SmsClass) normConfig(config dto.SmsConfig) dto.SmsConfig {
	
	config.Engine.Email = strings.ToLower(strings.TrimSpace(config.Engine.Email))
	if utils.Is.Empty(config.Engine.Email) || config.Engine.Email != "email" {
		config.Engine.Email = "email"
	}
	
	config.Engine.SMS = strings.ToLower(strings.TrimSpace(config.Engine.SMS))
	switch config.Engine.SMS {
	case "aliyun", "tencent", "smsbao":
	default:
		config.Engine.SMS = "aliyun"
	}
	
	if utils.Is.Empty(config.Email.Host) {
		config.Email.Host = "smtp.qq.com"
	}
	if config.Email.Port <= 0 {
		config.Email.Port = 465
	}
	if utils.Is.Empty(config.Email.Nickname) {
		config.Email.Nickname = "邮件昵称"
	}
	if utils.Is.Empty(config.Email.Subject) {
		config.Email.Subject = "邮件主题"
	}
	
	if utils.Is.Empty(config.AliYun.Endpoint) {
		config.AliYun.Endpoint = "dysmsapi.aliyuncs.com"
	}
	if utils.Is.Empty(config.Tencent.Endpoint) {
		config.Tencent.Endpoint = "sms.tencentcloudapi.com"
	}
	if utils.Is.Empty(config.Tencent.Region) {
		config.Tencent.Region = "ap-guangzhou"
	}
	if utils.Is.Empty(config.Smsbao.BaseUrl) {
		config.Smsbao.BaseUrl = "https://api.smsbao.com"
	}
	
	if utils.Is.Empty(config.Hash) {
		config.Hash = utils.Hash.Sum32(utils.Json.Encode(config))
	}
	
	return config
}

// normSmsMode - 统一驱动名称
func (this *SmsClass) normSmsMode(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "email", "aliyun", "tencent", "smsbao":
		return strings.ToLower(strings.TrimSpace(engine))
	default:
		return ""
	}
}

// defaultSmsConfig - 获取默认短信配置
func (this *SmsClass) defaultSmsConfig() dto.SmsConfig {
	return SmsInst.normConfig(dto.SmsConfig{})
}

// defaultSmsBody - 获取默认短信上下文
func (this *SmsClass) defaultSmsBody() dto.SmsBody {
	return dto.SmsBody{Length: 6, Expired: 5}
}

// mergeSmsBody - 合并短信上下文
func (this *SmsClass) mergeSmsBody(current dto.SmsBody, body dto.SmsBody) dto.SmsBody {
	current.Code = utils.Default(body.Code, current.Code)
	current.Length = utils.Default(body.Length, current.Length)
	current.Target = utils.Default(body.Target, current.Target)
	current.Template = utils.Default(body.Template, current.Template)
	current.Title = utils.Default(body.Title, current.Title)
	current.Subject = utils.Default(body.Subject, current.Subject)
	current.Nickname = utils.Default(body.Nickname, current.Nickname)
	current.Username = utils.Default(body.Username, current.Username)
	if body.Expired != 0 {
		current.Expired = body.Expired
	}
	current.Address = utils.Default(body.Address, current.Address)
	if current.Length <= 0 {
		current.Length = 6
	}
	if current.Expired <= 0 {
		current.Expired = 5
	}
	return current
}

func (this *SmsClass) NewGoMail(config dto.SmsConfig) *GoMailClass {
	item := &GoMailClass{Config: SmsInst.normConfig(config)}
	item.Init()
	return item
}

func (this *SmsClass) NewSmsAliYun(config dto.SmsConfig) *SmsAliYunClass {
	item := &SmsAliYunClass{Config: SmsInst.normConfig(config)}
	item.Init()
	return item
}

func (this *SmsClass) NewSmsTencent(config dto.SmsConfig) *SmsTencentClass {
	item := &SmsTencentClass{Config: SmsInst.normConfig(config)}
	item.Init()
	return item
}

func (this *SmsClass) NewSmsBao(config dto.SmsConfig) *SmsBaoClass {
	item := &SmsBaoClass{Config: SmsInst.normConfig(config)}
	item.Init()
	return item
}

// useDefaultSms - 使用默认配置激活短信服务
func (this *SmsClass) useDefaultSms() {
	conf := SmsInst.defaultSmsConfig()
	SmsInst.Config = conf
	SmsInst.Hash = conf.Hash
	SmsInst.setActiveSms(conf)
}

// setActiveSms - 按配置切换当前活动短信实现
func (this *SmsClass) setActiveSms(config dto.SmsConfig) {
	conf := SmsInst.normConfig(config)
	SmsInst.Config = conf
	
	GoMail = SmsInst.NewGoMail(conf)
	
	SmsAliYun = nil
	SmsTencent = nil
	SmsBao = nil
	SMS = GoMail
	
	switch SmsInst.normSmsMode(conf.Engine.SMS) {
	case "tencent":
		SmsTencent = SmsInst.NewSmsTencent(conf)
	case "smsbao":
		SmsBao = SmsInst.NewSmsBao(conf)
	default:
		SmsAliYun = SmsInst.NewSmsAliYun(conf)
	}
}

// newWithConfig - 使用传入配置重建短信服务并返回指定驱动
func (this *SmsClass) newWithConfig(config dto.SmsConfig, engine string) SmsAPI {
	conf := SmsInst.normConfig(config)
	switch SmsInst.normSmsMode(engine) {
	case "aliyun":
		return SmsInst.NewSmsAliYun(conf)
	case "tencent":
		return SmsInst.NewSmsTencent(conf)
	case "smsbao":
		return SmsInst.NewSmsBao(conf)
	default:
		return SmsInst.NewGoMail(conf)
	}
}

// setConfig - 注入短信配置
func (this *SmsClass) setConfig(config dto.SmsConfig) *SmsClass {
	this.Config = SmsInst.normConfig(config)
	this.HasConfig = true
	return this
}

// ReloadIfChanged - 当配置发生变化时重新加载短信服务
func (this *SmsClass) ReloadIfChanged(config ...dto.SmsConfig) {
	
	if len(config) > 0 {
		this.setConfig(config[0])
	}
	
	if !this.HasConfig {
		return
	}
	
	// hash 变化，说明配置有更新
	if this.Hash != this.Config.Hash {
		this.Init()
	}
}

// Init 初始化 SMS
func (this *SmsClass) Init(config ...dto.SmsConfig) {
	
	if len(config) > 0 {
		this.setConfig(config[0])
	}
	
	if !this.HasConfig {
		SmsInst.useDefaultSms()
		return
	}
	
	this.Config = SmsInst.normConfig(this.Config)
	this.Hash = this.Config.Hash
	SmsInst.setActiveSms(this.Config)
}

// SMS - SMS实例
/**
 * @return SmsAPI
 * @example：
 * sms := facade.SMS.VerifyCode("手机号", "验证码")
 */
var SMS SmsAPI

// GoMail 	   - GoMail邮件服务
var GoMail = &GoMailClass{}

// SmsAliYun   - 阿里云短信
var SmsAliYun  *SmsAliYunClass
// SmsTencent  - 腾讯云短信
var SmsTencent *SmsTencentClass
// SmsBao 	   - 短信宝
var SmsBao     *SmsBaoClass

// SmsAPI - 短信接口
type SmsAPI interface {
	// Target - 目标手机号
	Target(target string) SmsAPI
	// Code - 自定义验证码
	Code(code string) SmsAPI
	// Len - 验证码长度
	Len(length int) SmsAPI
	// Send - 发送验证码
	Send(target ...any) (*dto.SmsResp, error)
	// Subject - 主题（标题）
	Subject(subject string) SmsAPI
	// SetBody - 设置参数体
	SetBody(body dto.SmsBody) SmsAPI
	// NewSms - 使用配置创建新的短信实例
	NewSms(config dto.SmsConfig) SmsAPI
}

// ================================== GoMail邮件服务 - 开始 ==================================

// GoMailClass - GoMail邮件服务
type GoMailClass struct {
	// 邮件客户端
	Client *gomail.Dialer
	// 配置
	Config dto.SmsConfig
	// 参数
	Body   dto.SmsBody
}

// clone - 克隆邮件实例（共享客户端，隔离上下文）
func (this *GoMailClass) clone() *GoMailClass {
	if this == nil { return nil }
	clone := *this
	return &clone
}

// Init 初始化 邮件服务
func (this *GoMailClass) Init() {
	this.Config = SmsInst.normConfig(this.Config)
	this.Client = gomail.NewDialer(this.Config.Email.Host, this.Config.Email.Port, this.Config.Email.Account, this.Config.Email.Password)
	// 确保在STARTTLS/implicit-TLS流程中，TLS握手使用正确的服务器名称。
	this.Client.TLSConfig = &tls.Config{ServerName: this.Config.Email.Host}
	this.Body   = SmsInst.mergeSmsBody(SmsInst.defaultSmsBody(), this.Body)
	this.Body.Template = utils.Default(this.Body.Template, dto.TempEmailCode)
}

// Code - 自定义验证码
func (this *GoMailClass) Code(code string) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body.Code = code
	return mail
}

// Len - 验证码长度
func (this *GoMailClass) Len(length int) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body.Length = length
	return mail
}

// Target - 目标邮箱
func (this *GoMailClass) Target(target string) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body.Target = target
	return mail
}

// Subject - 主题（标题）
func (this *GoMailClass) Subject(subject string) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body.Subject = subject
	return mail
}

// Nickname - 昵称（发件人）
func (this *GoMailClass) Nickname(nickname string) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body.Nickname = nickname
	return mail
}

// Send - 发送验证码
func (this *GoMailClass) Send(target ...any) (*dto.SmsResp, error) {
	
	mail := this.clone()
	if mail == nil { return nil, errors.New("email client is not initialized") }
	
	if mail.Client == nil { return nil, errors.New("email client is not initialized") }
	
	// 这里的 target 是邮箱地址 - 优先级最高
	if len(target) > 0 {
		mail.Body.Target = cast.ToString(target[0])
	}
	
	socialType, err := utils.Identify.EmailOrPhone(mail.Body.Target)
	// 如果不是邮箱或手机号
	if err != nil { return nil, err }
	
	if socialType == "phone" {
		
		sender := SmsInst.newWithConfig(mail.Config, mail.Config.Engine.SMS)
		if sender == nil { return nil, errors.New("sms sender is not initialized") }
		
		return sender.SetBody(mail.Body).Send(mail.Body.Target)
	}
	
	// 如果自定义验证码为空，则生成一个验证码
	if utils.Is.Empty(mail.Body.Code) {
		mail.Body.Code = utils.Rand.Code(mail.Body.Length)
	}
	
	subject  := utils.Default(mail.Body.Subject, mail.Config.Email.Subject)
	nickname := utils.Default(mail.Body.Nickname, mail.Config.Email.Nickname)
	
	item := gomail.NewMessage()
	// 设置邮件内容类型
	item.SetHeader("Content-Type", "text/html; charset=UTF-8")
	// 设置发件人
	item.SetAddressHeader("From", mail.Config.Email.Account, utils.Default(mail.Body.Nickname, mail.Config.Email.Nickname))
	// 发送给多个用户
	item.SetHeader("To", mail.Body.Target)
	// 设置邮件主题
	item.SetHeader("Subject", subject)
	// 替换验证码
	temp := utils.Replace(mail.Body.Template, map[string]any{
		"${title}":    mail.Body.Title,
		"${code}":     mail.Body.Code,
		"${subject}":  subject,
		"${nickname}": nickname,
		"${username}": mail.Body.Username,
		"${expired}":  mail.Body.Expired,
		"${email}":    mail.Config.Email.Account,
		"${address}":  mail.Body.Address,
		"${year}":     time.Now().Format("2006"),
	})
	// 设置邮件正文
	item.SetBody("text/html", temp)
	
	// 发送邮件
	// 使用明确的 “拨号 + 发送” 操作，以确保在执行AUTH和MAIL命令之前完成SMTP握手（EHLO/STARTTLS）。
	sender, err := mail.Client.Dial()
	if err != nil { return nil, err }
	
	defer func() { _ = sender.Close() }()
	
	if err := gomail.Send(sender, item); err != nil {
		return nil, err
	}
	
	return &dto.SmsResp{
		VerifyCode: mail.Body.Code,
	}, nil
}

// SetBody - 设置参数体
func (this *GoMailClass) SetBody(body dto.SmsBody) SmsAPI {
	mail := this.clone()
	if mail == nil {
		return this
	}
	mail.Body = SmsInst.mergeSmsBody(mail.Body, body)
	return mail
}

// NewSms - 使用传入配置创建短信实例
func (this *GoMailClass) NewSms(config dto.SmsConfig) SmsAPI {
	return SmsInst.newWithConfig(config, "email")
}

// ================================== 阿里云短信 - 开始 ==================================

// SmsAliYunClass - 阿里云短信
type SmsAliYunClass struct {
	Client *AliYunSmsApi.Client
	Config dto.SmsConfig
	// 短信请求
	Body dto.SmsBody
}

// clone - 克隆阿里云短信实例（共享客户端，隔离上下文）
func (this *SmsAliYunClass) clone() *SmsAliYunClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Init 初始化 阿里云短信
func (this *SmsAliYunClass) Init() {
	this.Config = SmsInst.normConfig(this.Config)
	this.Body = SmsInst.mergeSmsBody(SmsInst.defaultSmsBody(), this.Body)
	
	// 创建访问凭证
	credential, err := AliYunCredential.NewCredential(nil)
	// 凭证创建失败
	if err != nil {
		return
	}
	
	// 创建客户端
	client, err := AliYunSmsApi.NewClient(&AliYunOpenApi.Config{
		Credential: credential,
		// 访问的域名 dysmsapi.aliyuncs.com
		Endpoint: tea.String(utils.Default(this.Config.AliYun.Endpoint, "dysmsapi.aliyuncs.com")),
		// 必填，您的 AccessKey ID
		AccessKeyId: tea.String(this.Config.AliYun.AccessKeyId),
		// 必填，您的 AccessKey Secret
		AccessKeySecret: tea.String(this.Config.AliYun.AccessKeySecret),
	})
	// 客户端创建失败
	if err != nil {
		return
	}
	
	this.Client = client
}

// ApiInfo - 接口信息
func (this *SmsAliYunClass) ApiInfo() (result *AliYunOpenApi.Params) {
	return &AliYunOpenApi.Params{
		// 接口名称
		Action: tea.String("SendSms"),
		// 接口版本
		Version: tea.String("2017-05-25"),
		// 接口协议
		Protocol: tea.String("HTTPS"),
		// 接口 HTTP 方法
		Method:   tea.String("POST"),
		AuthType: tea.String("AK"),
		Style:    tea.String("RPC"),
		// 接口 PATH
		Pathname: tea.String("/"),
		// 接口请求体内容格式
		ReqBodyType: tea.String("json"),
		// 接口响应体内容格式
		BodyType: tea.String("json"),
	}
}

// Code - 自定义验证码
func (this *SmsAliYunClass) Code(code string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Code = code
	return sms
}

// Len - 验证码长度
func (this *SmsAliYunClass) Len(length int) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Length = length
	return sms
}

// Target - 目标手机号
func (this *SmsAliYunClass) Target(target string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Target = target
	return sms
}

// Subject - 主题（标题）
func (this *SmsAliYunClass) Subject(subject string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Subject = subject
	return sms
}

// Nickname - 昵称（发件人）
func (this *SmsAliYunClass) Nickname(nickname string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Nickname = nickname
	return sms
}

// Send - 发送验证码
func (this *SmsAliYunClass) Send(target ...any) (*dto.SmsResp, error) {
	
	sms := this.clone()
	if sms == nil { return nil, errors.New("aliyun sms client is not initialized") }
	
	if sms.Client == nil { return nil, errors.New("aliyun sms client is not initialized") }
	
	// 这里的 target 是手机号 - 优先级最高
	if len(target) > 0 {
		sms.Body.Target = cast.ToString(target[0])
	}
	
	// 如果不是邮箱或手机号
	if attr, err := utils.Identify.EmailOrPhone(sms.Body.Target); err != nil {
		return nil, err
	} else if attr == "email" {
		sender := SmsInst.newWithConfig(sms.Config, sms.Config.Engine.Email)
		if sender == nil {
			return nil, errors.New("email sender is not initialized")
		}
		return sender.SetBody(sms.Body).Send(sms.Body.Target)
	}
	
	// 如果自定义验证码为空，则生成一个验证码
	if utils.Is.Empty(sms.Body.Code) {
		sms.Body.Code = utils.Rand.Code(sms.Body.Length)
	}
	
	params := &AliYunSmsApi.SendSmsRequest{
		PhoneNumbers: tea.String(sms.Body.Target),
		SignName:     tea.String(sms.Config.AliYun.SignName),
		TemplateCode: tea.String(sms.Config.AliYun.VerifyCode),
		TemplateParam: tea.String(utils.Json.Encode(map[string]any{
			"code": sms.Body.Code,
			"time": sms.Body.Expired,
		})),
	}
	
	resp, err := sms.Client.SendSmsWithOptions(params, &AliYunUtilV2.RuntimeOptions{})
	if err != nil { return nil, err }
	
	if resp == nil || resp.Body == nil || resp.Body.Code == nil {
		return nil, errors.New("aliyun sms response is nil")
	}
	
	if strings.ToLower(*resp.Body.Code) != "ok" {
		return nil, errors.New(cast.ToString(tea.StringValue(resp.Body.Message)))
	}
	
	return &dto.SmsResp{
		Result:     cast.ToStringMap(*resp.Body),
		Text:       utils.Json.Encode(*resp.Body),
		VerifyCode: sms.Body.Code,
	}, nil
}

// SetBody - 设置参数体
func (this *SmsAliYunClass) SetBody(body dto.SmsBody) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body = SmsInst.mergeSmsBody(sms.Body, body)
	return sms
}

// NewSms - 使用传入配置创建短信实例
func (this *SmsAliYunClass) NewSms(config dto.SmsConfig) SmsAPI {
	return SmsInst.newWithConfig(config, "aliyun")
}

// ================================== 腾讯云短信 - 开始 ==================================

// SmsTencentClass - 腾讯云短信
type SmsTencentClass struct {
	Client *TencentCloud.Client
	Config dto.SmsConfig
	// 短信请求
	Body dto.SmsBody
}

// clone - 克隆腾讯云短信实例（共享客户端，隔离上下文）
func (this *SmsTencentClass) clone() *SmsTencentClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Init 初始化 腾讯云短信
func (this *SmsTencentClass) Init() {
	this.Config = SmsInst.normConfig(this.Config)
	this.Body = SmsInst.mergeSmsBody(SmsInst.defaultSmsBody(), this.Body)
	
	credential := common.NewCredential(this.Config.Tencent.SecretId, this.Config.Tencent.SecretKey)
	clientProfile := profile.NewClientProfile()
	// sms.tencentcloudapi.com
	clientProfile.HttpProfile.Endpoint = this.Config.Tencent.Endpoint
	// ap-guangzhou
	client, err := TencentCloud.NewClient(credential, this.Config.Tencent.Region, clientProfile)
	
	if err != nil {
		return
	}
	
	this.Client = client
}

// Code - 自定义验证码
func (this *SmsTencentClass) Code(code string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Code = code
	return sms
}

// Len - 验证码长度
func (this *SmsTencentClass) Len(length int) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Length = length
	return sms
}

// Target - 目标手机号
func (this *SmsTencentClass) Target(target string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Target = target
	return sms
}

// Subject - 主题（标题）
func (this *SmsTencentClass) Subject(subject string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Subject = subject
	return sms
}

// Nickname - 昵称（发件人）
func (this *SmsTencentClass) Nickname(nickname string) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body.Nickname = nickname
	return sms
}

// Send - 发送验证码
func (this *SmsTencentClass) Send(target ...any) (*dto.SmsResp, error) {
	
	sms := this.clone()
	if sms == nil { return nil, errors.New("tencent sms client is not initialized") }
	
	if sms.Client == nil { return nil, errors.New("tencent sms client is not initialized") }
	
	// 这里的 target 是手机号 - 优先级最高
	if len(target) > 0 {
		sms.Body.Target = cast.ToString(target[0])
	}
	
	socialType, err := utils.Identify.EmailOrPhone(sms.Body.Target)
	// 如果不是邮箱或手机号
	if err != nil { return nil, err }
	
	if socialType == "email" {
		
		sender := SmsInst.newWithConfig(sms.Config, sms.Config.Engine.Email)
		if sender == nil { return nil, errors.New("email sender is not initialized") }
		
		return sender.SetBody(sms.Body).Send(sms.Body.Target)
	}
	
	// 如果自定义验证码为空，则生成一个验证码
	if utils.Is.Empty(sms.Body.Code) {
		sms.Body.Code = utils.Rand.Code(sms.Body.Length)
	}
	
	// 实例化一个请求对象,每个接口都会对应一个request对象
	request := TencentCloud.NewSendSmsRequest()
	
	request.PhoneNumberSet = common.StringPtrs([]string{sms.Body.Target})
	request.SmsSdkAppId = common.StringPtr(sms.Config.Tencent.SmsSdkAppId)
	request.SignName = common.StringPtr(sms.Config.Tencent.SignName)
	request.TemplateId = common.StringPtr(sms.Config.Tencent.VerifyCode)
	request.TemplateParamSet = common.StringPtrs([]string{sms.Body.Code})
	
	item, err := sms.Client.SendSms(request)
	
	if err != nil { return nil, err }
	
	if item.Response == nil { return nil, errors.New("response is nil") }
	
	if len(item.Response.SendStatusSet) == 0 { return nil, errors.New("response send status set is nil") }
	
	if item.Response.SendStatusSet[0].Code == nil { return nil, errors.New("response send status code is nil") }
	
	if *item.Response.SendStatusSet[0].Code != "Ok" { return nil, errors.New(cast.ToString(item.Response.SendStatusSet[0].Message)) }
	
	return &dto.SmsResp{
		Result:     utils.Json.Decode(item.ToJsonString()),
		Text:       item.ToJsonString(),
		VerifyCode: sms.Body.Code,
	}, nil
}

// SetBody - 设置参数体
func (this *SmsTencentClass) SetBody(body dto.SmsBody) SmsAPI {
	sms := this.clone()
	if sms == nil {
		return this
	}
	sms.Body = SmsInst.mergeSmsBody(sms.Body, body)
	return sms
}

// NewSms - 使用传入配置创建短信实例
func (this *SmsTencentClass) NewSms(config dto.SmsConfig) SmsAPI {
	return SmsInst.newWithConfig(config, "tencent")
}

// ================================== 短信宝 - 开始 ==================================

// SmsBaoClass - 短信宝
type SmsBaoClass struct {
	// 短信宝账号
	Account string
	// 短信宝API密钥
	ApiKey string
	// 短信签名
	SignName string
	// 接口地址
	BaseUrl string
	// 配置
	Config dto.SmsConfig
	// 短信请求
	Body dto.SmsBody
}

// clone - 克隆短信宝实例（隔离上下文）
func (this *SmsBaoClass) clone() *SmsBaoClass {
	if this == nil {
		return nil
	}
	clone := *this
	return &clone
}

// Init 初始化 短信宝
func (this *SmsBaoClass) Init() {
	this.Config = SmsInst.normConfig(this.Config)
	this.Account = this.Config.Smsbao.Account
	this.ApiKey = this.Config.Smsbao.ApiKey
	this.SignName = this.Config.Smsbao.SignName
	this.BaseUrl = this.Config.Smsbao.BaseUrl
	this.Body = SmsInst.mergeSmsBody(SmsInst.defaultSmsBody(), this.Body)
	this.Body.Template = fmt.Sprintf("【%s】您的验证码是：${code}，有效期5分钟。（打死也不要把验证码告诉别人）", this.SignName)
}

// Code - 自定义验证码
func (this *SmsBaoClass) Code(code string) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body.Code = code
	return sms
}

// Len - 验证码长度
func (this *SmsBaoClass) Len(length int) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body.Length = length
	return sms
}

// Target - 目标手机号
func (this *SmsBaoClass) Target(target string) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body.Target = target
	return sms
}

// Subject - 主题（标题）
func (this *SmsBaoClass) Subject(subject string) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body.Subject = subject
	return sms
}

// Nickname - 昵称（发件人）
func (this *SmsBaoClass) Nickname(nickname string) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body.Nickname = nickname
	return sms
}

// Send - 发送验证码
func (this *SmsBaoClass) Send(target ...any) (*dto.SmsResp, error) {
	
	sms := this.clone()
	if sms == nil { return nil, errors.New("smsbao sender is not initialized") }
	
	// 这里的 target 是手机号 - 优先级最高
	if len(target) > 0 {
		sms.Body.Target = cast.ToString(target[0])
	}
	
	socialType, err := utils.Identify.EmailOrPhone(sms.Body.Target)
	// 如果不是邮箱或手机号
	if err != nil { return nil, err }
	
	if socialType == "email" {
		sender := SmsInst.newWithConfig(sms.Config, sms.Config.Engine.Email)
		if sender == nil { return nil, errors.New("email sender is not initialized") }
		return sender.SetBody(sms.Body).Send(sms.Body.Target)
	}
	
	// 如果自定义验证码为空，则生成一个验证码
	if utils.Is.Empty(sms.Body.Code) {
		sms.Body.Code = utils.Rand.Code(sms.Body.Length)
	}
	
	if utils.Is.Empty(sms.ApiKey) { return nil, errors.New("API密钥不能为空") }
	
	if utils.Is.Empty(sms.Account) { return nil, errors.New("账号不能为空") }
	
	item := utils.Http(utils.HttpRequest{
		Method: "GET",
		Url:    fmt.Sprintf("%s/sms", sms.BaseUrl),
		Query: map[string]any{
			"u": sms.Account,
			"p": sms.ApiKey,
			"m": sms.Body.Target,
			"c": utils.Replace(sms.Body.Template, map[string]any{
				"${code}": sms.Body.Code,
			}),
		},
	}).Send()
	
	if item.Error != nil { return nil, item.Error }
	
	if cast.ToInt(item.Text) != 0 { return nil, errors.New("发送失败") }
	
	return &dto.SmsResp{
		Text:       item.Text,
		VerifyCode: sms.Body.Code,
	}, nil
}

// SetBody - 设置参数体
func (this *SmsBaoClass) SetBody(body dto.SmsBody) SmsAPI {
	sms := this.clone()
	if sms == nil { return this }
	sms.Body = SmsInst.mergeSmsBody(sms.Body, body)
	return sms
}

// NewSms - 使用传入配置创建短信实例
func (this *SmsBaoClass) NewSms(config dto.SmsConfig) SmsAPI {
	return SmsInst.newWithConfig(config, "smsbao")
}