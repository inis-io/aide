package pay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
)

type testProvider struct {
	name   string
	caps   []Capability
	closed atomic.Int32
}

func (this *testProvider) Name() string               { return this.name }
func (this *testProvider) Capabilities() []Capability { return this.caps }
func (this *testProvider) Close() error               { this.closed.Add(1); return nil }

type creatorTestProvider struct{ *testProvider }

func (this *creatorTestProvider) CreateTrade(ctx context.Context, request TradeCreateRequest) (TradeResult, error) {
	return TradeResult{OutTradeNo: request.OutTradeNo, Status: TradeStatusPending, ChargedAmount: request.Amount}, ctx.Err()
}

type collectObserver struct{ records []Observation }

func (this *collectObserver) Observe(_ context.Context, record Observation) {
	this.records = append(this.records, record)
}

type collectLogger struct{ records []LogRecord }

func (this *collectLogger) Log(_ context.Context, record LogRecord) {
	this.records = append(this.records, record)
}

type failingCreatorProvider struct{ *testProvider }

func (this *failingCreatorProvider) CreateTrade(_ context.Context, _ TradeCreateRequest) (TradeResult, error) {
	return TradeResult{}, &GatewayError{Provider: "demo", Operation: "gateway", Code: "NO_AUTH", Message: "此商家的收款功能已被限制", Outcome: OutcomeKnownFailed, Retryable: false, Cause: ErrGatewayRejected}
}

// TestMoneyIntegerModel - 验证金额解析、格式化、精度约束与溢出保护
func TestMoneyIntegerModel(t *testing.T) {
	cases := []struct {
		value, currency, expected string
		minor                     int64
	}{
		{"10.01", "CNY", "10.01", 1001}, {"0.30", "USD", "0.30", 30}, {"100", "JPY", "100", 100}, {"-0.01", "CNY", "-0.01", -1},
	}
	for _, item := range cases {
		money, err := ParseMoney(item.value, item.currency)
		if err != nil || money.Minor != item.minor || money.MajorString() != item.expected {
			t.Fatalf("金额解析不符：%+v, %v", money, err)
		}
	}
	if _, err := ParseMoney("1.001", "CNY"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("应拒绝超精度金额：%v", err)
	}
	if NewMoneyMinor(1, "ZZZ").Validate() == nil {
		t.Fatal("未知币种不应静默采用两位精度")
	}
	forged := Money{Minor: 1, Currency: Currency{Code: "CNY", Exponent: 3}}
	if forged.Validate() == nil {
		t.Fatal("已知币种精度不可伪造")
	}
	if _, err := (Money{Minor: math.MaxInt64, Currency: Currency{Code: "CNY", Exponent: 2}}).Add(NewMoneyMinor(1, "CNY")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("金额溢出应返回请求错误")
	}
	if err := RegisterCurrency("TST", 4); err != nil {
		t.Fatalf("注册测试币种失败：%v", err)
	}
	if money, err := ParseMoney("1.2345", "TST"); err != nil || money.Minor != 12345 {
		t.Fatalf("自定义币种解析失败：%+v %v", money, err)
	}
}

// TestRegistryIsolationAndValidation - 验证注册表隔离、重复拒绝、显式替换及能力双向校验
func TestRegistryIsolationAndValidation(t *testing.T) {
	registry := NewRegistry()
	factory := func(context.Context, ConfigInput, OpenOptions) (Provider, error) {
		return &creatorTestProvider{&testProvider{name: "demo", caps: []Capability{CapTradeCreate}}}, nil
	}
	if err := registry.Register(" Demo ", factory); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("demo", factory); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("重复注册应失败：%v", err)
	}
	if names := registry.Names(); len(names) != 1 || names[0] != "demo" {
		t.Fatalf("名称归一化或排序错误：%v", names)
	}
	if len(NewRegistry().Names()) != 0 {
		t.Fatal("实例 Registry 之间不应共享注册")
	}
	if err := registry.Replace("demo", factory); err != nil {
		t.Fatal(err)
	}
	driver, err := registry.New(context.Background(), "DEMO", struct{}{})
	if err != nil || !driver.Supports(CapTradeCreate) {
		t.Fatalf("构造 Driver 失败：%v", err)
	}
	if _, err = driver.Refund(context.Background(), RefundRequest{}); !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("不支持能力分类错误：%v", err)
	}
	bad := NewRegistry()
	_ = bad.Register("bad", func(context.Context, ConfigInput, OpenOptions) (Provider, error) {
		return &testProvider{name: "bad", caps: []Capability{CapTradeCreate}}, nil
	})
	if _, err = bad.New(context.Background(), "bad", struct{}{}); !errors.Is(err, ErrInvalidProvider) {
		t.Fatalf("能力与接口不一致应拒绝构造：%v", err)
	}
}

// TestActionExtensionAndFingerprint - 验证结构化动作、扩展严格解码及请求指纹稳定性
func TestActionExtensionAndFingerprint(t *testing.T) {
	action := PaymentAction{Kind: ActionForm, Form: &FormAction{Method: "POST", URL: "https://example.com/pay", Fields: map[string]string{"token": "ok"}}}
	if err := action.Validate(); err != nil {
		t.Fatal(err)
	}
	action.Redirect = &RedirectAction{URL: "https://example.com"}
	if err := action.Validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("多个动作载荷必须拒绝")
	}
	if err := (PaymentAction{Kind: ActionForm, Form: &FormAction{Method: "POST", URL: "javascript:alert(1)"}}).Validate(); err == nil {
		t.Fatal("非 HTTP(S) 表单地址必须拒绝")
	}
	type extension struct {
		Timeout string `json:"timeout"`
	}
	exts, err := SetExtension(nil, "demo", extension{Timeout: "15m"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded extension
	if err = DecodeExtension(exts, "demo", &decoded); err != nil || decoded.Timeout != "15m" {
		t.Fatalf("扩展解码失败：%+v %v", decoded, err)
	}
	exts["other"] = json.RawMessage(`{}`)
	if err = DecodeExtension(exts, "demo", &decoded); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("跨 Provider 命名空间必须拒绝")
	}
	request := NewTradeCreateRequest("T-1", TradeModeQR, "商品", NewMoneyMinor(100, "CNY"))
	request.Metadata = map[string]string{"trace": "a"}
	first, _ := RequestFingerprint(request)
	request.Metadata["trace"] = "b"
	second, _ := RequestFingerprint(request)
	if first != second {
		t.Fatal("Metadata 不应影响请求指纹")
	}
	request.Amount.Minor++
	third, _ := RequestFingerprint(request)
	if first == third {
		t.Fatal("金额变化必须改变请求指纹")
	}
}

// TestSensitiveRawAndGatewayError - 验证敏感值、Raw 与错误文本不会泄露明文
func TestSensitiveRawAndGatewayError(t *testing.T) {
	secret := NewSensitiveString("canary-secret")
	body, _ := json.Marshal(struct {
		Secret SensitiveString `json:"secret"`
	}{secret})
	for _, text := range []string{fmt.Sprint(secret), fmt.Sprintf("%#v", secret), string(body)} {
		if strings.Contains(text, "canary-secret") {
			t.Fatalf("敏感值泄露：%s", text)
		}
	}
	raw := CaptureRaw(RawCapturePolicy{Mode: RawCaptureRedacted, MaxBytes: 128}, "application/json", []byte(`{"authorization":"canary-secret","ok":1}`))
	if raw == nil || strings.Contains(string(raw.Body), "canary-secret") {
		t.Fatalf("Raw 未脱敏：%s", raw.Body)
	}
	errorValue := &GatewayError{Provider: "demo", Operation: "pay", Code: "E1", Message: "拒绝", Cause: ErrGatewayRejected}
	if !errors.Is(errorValue, ErrGatewayRejected) || strings.Contains(errorValue.Error(), "canary-secret") {
		t.Fatalf("错误分类或脱敏失败：%v", errorValue)
	}
}

// TestDriverContextObserverAndClose - 验证 context 透传、观测白名单和幂等关闭
func TestDriverContextObserverAndClose(t *testing.T) {
	observer := &collectObserver{}
	provider := &creatorTestProvider{&testProvider{name: "demo", caps: []Capability{CapTradeCreate}}}
	registry := NewRegistry()
	_ = registry.Register("demo", func(context.Context, ConfigInput, OpenOptions) (Provider, error) { return provider, nil })
	driver, err := registry.New(context.Background(), "demo", struct{}{}, WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := NewTradeCreateRequest("sensitive-order-number", TradeModeQR, "商品", NewMoneyMinor(100, "CNY"))
	if _, err = driver.CreateTrade(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("context 未透传：%v", err)
	}
	if len(observer.records) != 2 || observer.records[1].OutNoHash == "sensitive-order-number" || observer.records[1].OutNoHash == "" {
		t.Fatalf("Observer 字段未按白名单摘要：%+v", observer.records)
	}
	if err = driver.Close(); err != nil {
		t.Fatal(err)
	}
	_ = driver.Close()
	if provider.closed.Load() != 1 {
		t.Fatal("Driver Close 必须幂等")
	}
}

// TestObserveForwardsGatewayMessage - 验证 observe() 将 GatewayError.Message 透传到 LogRecord/Observation，
// 且 Error() 文本保持不含 Message 的现有设计
func TestObserveForwardsGatewayMessage(t *testing.T) {
	logger := &collectLogger{}
	observer := &collectObserver{}
	provider := &failingCreatorProvider{&testProvider{name: "demo", caps: []Capability{CapTradeCreate}}}
	registry := NewRegistry()
	_ = registry.Register("demo", func(context.Context, ConfigInput, OpenOptions) (Provider, error) { return provider, nil })
	driver, err := registry.New(context.Background(), "demo", struct{}{}, WithObserver(observer), WithLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	defer driver.Close()

	request := NewTradeCreateRequest("T-1", TradeModeQR, "商品", NewMoneyMinor(100, "CNY"))
	if _, err = driver.CreateTrade(context.Background(), request); !errors.Is(err, ErrGatewayRejected) {
		t.Fatalf("应返回网关拒绝错误：%v", err)
	}
	if len(logger.records) != 1 {
		t.Fatalf("应产生一条 LogRecord：%+v", logger.records)
	}
	record := logger.records[0]
	if record.Code != "NO_AUTH" || record.Message != "此商家的收款功能已被限制" || record.Outcome != OutcomeKnownFailed || record.Retryable {
		t.Fatalf("LogRecord 未完整透传网关错误字段：%+v", record)
	}
	if len(observer.records) != 2 {
		t.Fatalf("应产生两条 Observation：%+v", observer.records)
	}
	// start 阶段：仅 Provider/Operation/摘要，无错误字段
	start := observer.records[0]
	if start.Phase != "start" || start.Code != "" || start.Message != "" || start.Outcome != "" || start.Retryable {
		t.Fatalf("start Observation 不应携带网关错误字段：%+v", start)
	}
	// end 阶段：完整透传网关错误字段
	end := observer.records[1]
	if end.Phase != "end" || end.Code != "NO_AUTH" || end.Message != "此商家的收款功能已被限制" || end.Outcome != OutcomeKnownFailed || end.Retryable {
		t.Fatalf("end Observation 未完整透传网关错误字段：%+v", end)
	}
	if strings.Contains(err.Error(), "此商家的收款功能已被限制") {
		t.Fatalf("Error 文本不应包含 Message：%s", err.Error())
	}
}
