package pushx

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cast"
)

// fakeSender - 测试用驱动：记录收到的消息体，不触发任何网络请求
type fakeSender struct {
	// 最近一次收到的消息体
	got Message
	// 预设的发送错误
	err error
}

// Send - 记录消息体并返回预设响应
func (this *fakeSender) Send(message Message) (*Response, error) {
	this.got = message
	if this.err != nil {
		return nil, this.err
	}
	return &Response{VerifyCode: message.Code}, nil
}

// registerFake - 注册一个测试驱动并返回其实例（同名覆盖，互不影响）
func registerFake(name string) *fakeSender {
	fake := &fakeSender{}
	Register(name, func(config Config) (Sender, error) {
		return fake, nil
	})
	return fake
}

// TestRegisterAndNew - 验证注册表：内置驱动登记在册、列表有序、注册后能按名称创建、未注册名称报错且提示可用列表
func TestRegisterAndNew(t *testing.T) {

	registerFake("mock")

	names := Names()
	if !sort.StringsAreSorted(names) {
		t.Fatalf("Names() 应返回有序列表，实际: %v", names)
	}

	// 内置驱动应在变量初始化时登记，无需任何 init 调用
	for _, want := range []string{"email", "aliyun", "tencent", "smsbao", "mock"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("驱动[%s]应出现在 Names() 中，实际: %v", want, names)
		}
	}

	if _, err := New("mock", Config{}); err != nil {
		t.Fatalf("按已注册名称创建驱动不应报错: %v", err)
	}

	// 未注册名称应报错，且错误中提示可用驱动列表
	if _, err := New("not-exists", Config{}); err == nil || !strings.Contains(err.Error(), "可用") {
		t.Fatalf("未注册的驱动名称应返回带可用列表的错误，实际: %v", err)
	}
}

// TestDriverChain - 验证链式实例：值语义上下文隔离、链式参数透传、Send 参数优先级
func TestDriverChain(t *testing.T) {

	fake := registerFake("mock")

	base := NewDriver(fake)
	child := base.Target("13800000000").Code("123456").Len(4).Expired(10).Subject("主题").Template("验证码：${code}").Param("name", "张三").Param("name", "李四")

	// 值语义：链式调用不影响原实例
	if base.message.Target != "" {
		t.Fatal("链式调用不应修改原实例的消息体")
	}

	if _, err := child.Send(); err != nil {
		t.Fatalf("链式发送不应报错: %v", err)
	}
	if fake.got.Target != "13800000000" || fake.got.Code != "123456" || fake.got.Length != 4 || fake.got.Expired != 10 || fake.got.Subject != "主题" || fake.got.Template != "验证码：${code}" {
		t.Fatalf("链式参数应完整透传给驱动，实际: %+v", fake.got)
	}
	if len(fake.got.Params) != 1 || fake.got.Params["name"] != "李四" {
		t.Fatalf("Param 同名键应后者覆盖前者，实际: %+v", fake.got.Params)
	}

	// Send 传入的 target 优先级最高
	if _, err := child.Send("13900000000"); err != nil {
		t.Fatalf("发送不应报错: %v", err)
	}
	if fake.got.Target != "13900000000" {
		t.Fatalf("Send 参数应覆盖链式 Target，实际: %s", fake.got.Target)
	}

	// 底层驱动未初始化时应报错
	if _, err := NewDriver(nil).Send("13800000000"); err == nil {
		t.Fatal("底层驱动为空时应返回错误")
	}
}

// TestSetMessage - 验证消息体合并：非零字段覆盖、零字段保留、Params 按键名逐条合并
func TestSetMessage(t *testing.T) {

	fake := registerFake("mock")

	driver := NewDriver(fake, Message{Target: "13800000000", Code: "1111", Expired: 10, Params: map[string]any{"name": "张三", "order": "A1"}})
	driver = driver.SetMessage(Message{Code: "2222", Params: map[string]any{"order": "B2"}})

	if _, err := driver.Send(); err != nil {
		t.Fatalf("发送不应报错: %v", err)
	}

	if fake.got.Code != "2222" {
		t.Fatalf("非零字段应覆盖，实际 Code: %s", fake.got.Code)
	}
	if fake.got.Target != "13800000000" || fake.got.Expired != 10 {
		t.Fatalf("零字段应保留原值，实际: %+v", fake.got)
	}
	if fake.got.Params["name"] != "张三" || fake.got.Params["order"] != "B2" {
		t.Fatalf("Params 应按键名合并且同名覆盖，实际: %+v", fake.got.Params)
	}
}

// TestNormConfig - 验证配置归一化：默认值补齐、未知引擎回退、Hash 生成
func TestNormConfig(t *testing.T) {

	conf := normConfig(Config{})

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"邮件引擎默认值", conf.Engine.Email, "email"},
		{"短信引擎默认值", conf.Engine.SMS, "aliyun"},
		{"邮件服务器默认值", conf.Email.Host, "smtp.qq.com"},
		{"邮件端口默认值", conf.Email.Port, 465},
		{"阿里云Endpoint默认值", conf.AliYun.Endpoint, "dysmsapi.aliyuncs.com"},
		{"腾讯云Endpoint默认值", conf.Tencent.Endpoint, "sms.tencentcloudapi.com"},
		{"腾讯云Region默认值", conf.Tencent.Region, "ap-guangzhou"},
		{"短信宝接口地址默认值", conf.Smsbao.BaseUrl, "https://api.smsbao.com"},
	}
	for _, item := range cases {
		if item.got != item.want {
			t.Errorf("%s: 期望 %v，实际 %v", item.name, item.want, item.got)
		}
	}

	if conf.Hash == "" {
		t.Error("Hash 应自动生成")
	}

	// 未注册的引擎名称应回退到默认值
	conf = normConfig(Config{Engine: EngineConfig{Email: "unknown", SMS: "unknown"}})
	if conf.Engine.Email != "email" || conf.Engine.SMS != "aliyun" {
		t.Errorf("未知引擎应回退默认值，实际: %+v", conf.Engine)
	}
}

// TestNormMessage - 验证消息体归一化：长度与有效期下限、验证码自动生成
func TestNormMessage(t *testing.T) {

	message := normMessage(Message{})
	if message.Length != 6 || message.Expired != 5 {
		t.Errorf("默认值应为 6 位长度、5 分钟有效期，实际: %+v", message)
	}
	if len(message.Code) != 6 {
		t.Errorf("验证码应自动生成且长度匹配，实际: %s", message.Code)
	}

	message = normMessage(Message{Code: "9999", Length: 4, Expired: 1})
	if message.Code != "9999" {
		t.Errorf("自定义验证码不应被覆盖，实际: %s", message.Code)
	}
}

// TestRender - 验证模板渲染：内置变量替换、未识别占位符保留、附加变量同名覆盖
func TestRender(t *testing.T) {

	message := Message{
		Target:   "13800000000",
		Code:     "123456",
		Length:   6,
		Expired:  10,
		Subject:  "登录验证",
		Nickname: "系统通知",
		Username: "张三",
		Title:    "我的应用",
		Address:  "北京市",
	}

	got := message.Render("【${title}】${username} 您好，验证码 ${code}，${expired} 分钟内有效。发件人：${nickname}，地址：${address}，目标：${target}，主题：${subject}，长度：${length}")
	want := "【我的应用】张三 您好，验证码 123456，10 分钟内有效。发件人：系统通知，地址：北京市，目标：13800000000，主题：登录验证，长度：6"
	if got != want {
		t.Errorf("内置变量应全部替换\n期望: %s\n实际: %s", want, got)
	}

	// 内置 ${year} 变量应为当前年份
	if got := message.Render("© ${year}"); got != "© "+cast.ToString(time.Now().Year()) {
		t.Errorf("${year} 应替换为当前年份，实际: %s", got)
	}

	// 未识别的占位符保留原样
	if got := message.Render("${unknown}"); got != "${unknown}" {
		t.Errorf("未识别的占位符应保留原样，实际: %s", got)
	}

	// 附加变量：新增驱动级变量，同名覆盖内置变量
	got = message.Render("${code} / ${email}", map[string]any{"${email}": "noreply@example.com", "${code}": "654321"})
	if got != "654321 / noreply@example.com" {
		t.Errorf("附加变量应生效且同名覆盖内置变量，实际: %s", got)
	}

	// Params 自定义变量：以 ${键名} 使用，覆盖内置变量但低于 extra
	message.Params = map[string]any{"name": "张三", "code": "0000"}
	got = message.Render("${name} ${code}", map[string]any{"${code}": "9999"})
	if got != "张三 9999" {
		t.Errorf("Params 应以 ${键名} 生效，extra 同名时优先，实际: %s", got)
	}
}

// TestAliYunTemplateParams - 验证阿里云模板变量组装：默认 code/time、Params 按键名合并、同名覆盖
func TestAliYunTemplateParams(t *testing.T) {

	vars := aliYunTemplateParams(Message{Code: "123456", Expired: 5})
	if vars["code"] != "123456" || vars["time"] != int64(5) {
		t.Fatalf("默认应含 code/time，实际: %+v", vars)
	}

	vars = aliYunTemplateParams(Message{
		Code:   "123456",
		Params: map[string]any{"name": "张三", "code": "654321"},
	})
	if vars["name"] != "张三" {
		t.Errorf("Params 自定义键应合并进模板变量，实际: %+v", vars)
	}
	if vars["code"] != "654321" {
		t.Errorf("Params 同名键应覆盖默认值，实际: %+v", vars)
	}
}

// TestTencentTemplateParams - 验证腾讯云模板参数组装：空 Params 默认 [验证码]，提供后按数字键名升序完全接管
func TestTencentTemplateParams(t *testing.T) {

	params := tencentTemplateParams(Message{Code: "123456"})
	if len(params) != 1 || params[0] != "123456" {
		t.Fatalf("Params 为空时应默认 [验证码]，实际: %v", params)
	}

	// 数字键名应按数值升序（"10" 排在 "2" 之后），值统一转为字符串
	params = tencentTemplateParams(Message{
		Code:   "123456",
		Params: map[string]any{"10": "末位", "2": int64(5), "1": "654321"},
	})
	want := []string{"654321", "5", "末位"}
	if len(params) != len(want) {
		t.Fatalf("参数长度不符，期望: %v，实际: %v", want, params)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Fatalf("参数应按数字键名升序组装，期望: %v，实际: %v", want, params)
		}
	}
}

// TestDriverTargetValidation - 验证驱动的目标校验：各驱动只发自己通道，目标不合法时快速失败（不触发网络）
func TestDriverTargetValidation(t *testing.T) {

	cases := []struct {
		name    string
		sender  Sender
		message Message
		wantErr string
	}{
		{"邮件驱动收到手机号", &EmailSender{}, Message{Target: "13800000000"}, "目标邮箱格式无效"},
		{"阿里云驱动收到邮箱", &AliYunSender{}, Message{Target: "test@example.com"}, "目标手机号格式无效"},
		{"腾讯云驱动收到非法目标", &TencentSender{}, Message{Target: "abc"}, "目标手机号格式无效"},
		{"短信宝驱动收到非法目标", &SmsbaoSender{}, Message{Target: "abc"}, "目标手机号格式无效"},
		// 目标合法但凭据缺失时，应进入驱动自身的检查（验证校验顺序，全程不联网）
		{"短信宝目标合法但密钥为空", &SmsbaoSender{}, Message{Target: "13800000000"}, "API密钥不能为空"},
		{"邮件目标合法但客户端为空", &EmailSender{}, Message{Target: "test@example.com"}, "邮件客户端未初始化"},
	}

	for _, item := range cases {
		_, err := item.sender.Send(item.message)
		if err == nil || !strings.Contains(err.Error(), item.wantErr) {
			t.Errorf("%s: 期望错误含[%s]，实际: %v", item.name, item.wantErr, err)
		}
	}
}

// TestRouterSend - 验证智能路由：按目标类型分发、非法目标与驱动缺失时报错
func TestRouterSend(t *testing.T) {

	email := &fakeSender{}
	sms := &fakeSender{}
	router := Router{Email: email, SMS: sms}

	if _, err := router.Send(Message{Target: "test@example.com"}); err != nil {
		t.Fatalf("邮箱路由不应报错: %v", err)
	}
	if email.got.Target != "test@example.com" || sms.got.Target != "" {
		t.Fatal("邮箱目标应分发给邮件驱动")
	}

	if _, err := router.Send(Message{Target: "13800000000"}); err != nil {
		t.Fatalf("手机号路由不应报错: %v", err)
	}
	if sms.got.Target != "13800000000" {
		t.Fatal("手机号目标应分发给短信驱动")
	}

	if _, err := router.Send(Message{Target: "abc"}); err == nil {
		t.Fatal("非法目标应返回错误")
	}

	if _, err := (Router{}).Send(Message{Target: "13800000000"}); err == nil {
		t.Fatal("短信驱动缺失时应返回错误")
	}
}

// TestControllerInitAndReload - 验证控制器：配置注入后全局实例生效，Hash 变化时热重载
func TestControllerInitAndReload(t *testing.T) {

	fake := registerFake("mock")
	config := Config{Engine: EngineConfig{Email: "mock", SMS: "mock"}}

	inst := &Controller{}
	inst.Init(config)

	if !inst.HasConfig {
		t.Fatal("注入配置后 HasConfig 应为 true")
	}
	if _, ok := SMS.(*fakeSender); !ok {
		t.Fatal("全局短信驱动应切换为已注册的实现")
	}

	// 全局链式实例应能按手机号路由到短信驱动
	if _, err := Push.Send("13800000000"); err != nil {
		t.Fatalf("全局实例发送不应报错: %v", err)
	}
	if fake.got.Target != "13800000000" {
		t.Fatalf("全局实例应路由到已注册驱动，实际: %+v", fake.got)
	}

	// Hash 未变化时不重载，变化后才重载
	hashBefore := inst.Hash
	inst.ReloadIfChanged()
	if inst.Hash != hashBefore {
		t.Fatal("配置未变化时不应重载")
	}

	other := registerFake("mock2")
	inst.ReloadIfChanged(Config{Engine: EngineConfig{Email: "mock2", SMS: "mock2"}})
	if other.got.Target != "" {
		t.Fatal("重载不应触发发送")
	}
	if _, ok := SMS.(*fakeSender); !ok {
		t.Fatal("配置变化后应重载为新驱动")
	}
	if _, err := Push.Send("13900000000"); err != nil {
		t.Fatalf("重载后发送不应报错: %v", err)
	}
	if other.got.Target != "13900000000" {
		t.Fatal("配置变化后应路由到新注册的驱动")
	}

	// 测试结束后恢复默认门面，避免影响其他用例
	inst.HasConfig = false
	inst.useDefault()
}

// TestSenderError - 验证驱动初始化失败的占位实现：Send 时返回初始化错误
func TestSenderError(t *testing.T) {

	Register("broken", func(config Config) (Sender, error) {
		return nil, errors.New("凭据无效")
	})

	inst := &Controller{}
	inst.Init(Config{Engine: EngineConfig{Email: "mock", SMS: "broken"}})

	_, err := Push.Send("13800000000")
	if err == nil || !strings.Contains(err.Error(), "凭据无效") {
		t.Fatalf("初始化失败的驱动应在 Send 时返回原始错误，实际: %v", err)
	}

	inst.HasConfig = false
	inst.useDefault()
}
