package pay

import (
	"fmt"
	"net/url"
	"strings"
)

// ActionKind - 支付动作类型
type ActionKind string

const (
	ActionQRCode   ActionKind = "qr-code"
	ActionRedirect ActionKind = "redirect"
	ActionForm     ActionKind = "form"
	ActionSDK      ActionKind = "sdk"
)

// PaymentAction - 结构化支付动作
type PaymentAction struct {
	// Kind - 动作类型
	Kind ActionKind `json:"kind"`
	// QRCode - 二维码动作
	QRCode *QRCodeAction `json:"qrCode,omitempty"`
	// Redirect - 浏览器跳转动作
	Redirect *RedirectAction `json:"redirect,omitempty"`
	// Form - 受约束网页表单动作
	Form *FormAction `json:"form,omitempty"`
	// SDK - 客户端 SDK 参数动作
	SDK *SDKAction `json:"sdk,omitempty"`
}

// QRCodeAction - 二维码内容动作
type QRCodeAction struct {
	// Content - 用于生成二维码的文本内容
	Content string `json:"content"`
}

// RedirectAction - 浏览器跳转动作
type RedirectAction struct {
	// URL - HTTP(S) 跳转地址
	URL string `json:"url"`
}

// FormAction - 受约束的网页表单动作
type FormAction struct {
	// Method - GET 或 POST
	Method string `json:"method"`
	// URL - HTTP(S) 表单提交地址
	URL string `json:"url"`
	// Fields - 表单字段
	Fields map[string]string `json:"fields"`
}

// SDKAction - 客户端 SDK 参数动作
type SDKAction struct {
	// Parameters - Provider SDK 所需参数
	Parameters map[string]string `json:"parameters"`
}

// Validate - 校验动作唯一性与 URL、方法安全约束
func (this PaymentAction) Validate() error {
	count := 0
	if this.QRCode != nil {
		count++
	}
	if this.Redirect != nil {
		count++
	}
	if this.Form != nil {
		count++
	}
	if this.SDK != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("%w：支付动作必须且只能包含一种载荷", ErrInvalidRequest)
	}
	switch this.Kind {
	case ActionQRCode:
		if this.QRCode == nil || strings.TrimSpace(this.QRCode.Content) == "" {
			return fmt.Errorf("%w：二维码内容为空", ErrInvalidRequest)
		}
	case ActionRedirect:
		if this.Redirect == nil || !safeHTTPURL(this.Redirect.URL) {
			return fmt.Errorf("%w：跳转地址非法", ErrInvalidRequest)
		}
	case ActionForm:
		if this.Form == nil || !safeHTTPURL(this.Form.URL) {
			return fmt.Errorf("%w：表单地址非法", ErrInvalidRequest)
		}
		method := strings.ToUpper(strings.TrimSpace(this.Form.Method))
		if method != "GET" && method != "POST" {
			return fmt.Errorf("%w：表单方法只允许 GET 或 POST", ErrInvalidRequest)
		}
		for key, value := range this.Form.Fields {
			joined := strings.ToLower(key + "\x00" + value)
			if strings.Contains(joined, "<script") || strings.Contains(joined, "javascript:") {
				return fmt.Errorf("%w：表单字段包含可执行内容", ErrInvalidRequest)
			}
		}
	case ActionSDK:
		if this.SDK == nil || len(this.SDK.Parameters) == 0 {
			return fmt.Errorf("%w：SDK 参数为空", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w：支付动作类型未知", ErrInvalidRequest)
	}
	return nil
}

func safeHTTPURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.User == nil
}
