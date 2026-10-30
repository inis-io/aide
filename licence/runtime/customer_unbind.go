package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// CustomerUnbindInput - 解绑授权账号凭据，Method 缺省为 totp。
type CustomerUnbindInput struct {
	Method      string
	Code        string
	ChallengeNo string
	Password    string
}

// CustomerUnbindDestination - 授权账号的脱敏联系方式。
type CustomerUnbindDestination struct {
	Email string `json:"email"`
	Phone string `json:"phone"`
}

// CustomerUnbindOptions - 平台按优先级返回的可用解绑验证方式。
type CustomerUnbindOptions struct {
	Preferred   string
	Methods     []string
	Destination CustomerUnbindDestination
}

// CustomerUnbindChallenge - 一次性解绑验证码挑战。
type CustomerUnbindChallenge struct {
	ChallengeNo string
	ExpiresAt   int64
	Destination string
}

type customerUnbindBody struct {
	Method      string `json:"method"`
	Code        string `json:"code,omitempty"`
	ChallengeNo string `json:"challengeNo,omitempty"`
	Password    string `json:"password,omitempty"`
	TwoFACode   string `json:"twoFACode,omitempty"`
}

type customerUnbindChallengeBody struct {
	Channel string `json:"channel"`
}

type customerUnbindResponse struct {
	Status     string `json:"status"`
	ServerTime int64  `json:"serverTime"`
	Message    string `json:"message"`
}

type customerUnbindOptionsResponse struct {
	Status      string                    `json:"status"`
	Preferred   string                    `json:"preferred"`
	Methods     []string                  `json:"methods"`
	Destination CustomerUnbindDestination `json:"destination"`
	ServerTime  int64                     `json:"serverTime"`
	Message     string                    `json:"message"`
}

type customerUnbindChallengeResponse struct {
	Status      string `json:"status"`
	ChallengeNo string `json:"challengeNo"`
	ExpiresAt   int64  `json:"expiresAt"`
	Destination string `json:"destination"`
	ServerTime  int64  `json:"serverTime"`
	Message     string `json:"message"`
}

func (this *Client) customerUnbindToken(action string) (string, error) {
	this.mu.RLock()
	token := this.state.ActivationToken
	this.mu.RUnlock()
	if token == "" {
		return "", errors.New("客户端未激活，无法" + action)
	}
	return token, nil
}

func customerUnbindStatusError(status, message string) error {
	if status == "" {
		status = "ERROR"
	}
	return &ProvisionError{Status: status, Message: message}
}

// CustomerUnbindOptions - 读取 2FA、邮箱、短信、密码的实际可用方式与脱敏目的地。
func (this *Client) CustomerUnbindOptions(ctx context.Context) (CustomerUnbindOptions, error) {
	if _, err := this.customerUnbindToken("读取解绑验证方式"); err != nil {
		return CustomerUnbindOptions{}, err
	}
	statusCode, raw, err := this.doRequest(ctx, http.MethodGet, "/api/v1/licenses/customer-unbind/options", nil, true)
	if err != nil {
		return CustomerUnbindOptions{}, err
	}
	if statusCode != http.StatusOK {
		if statusCode == http.StatusNotFound {
			return CustomerUnbindOptions{}, errors.New("许可证或实例信息无效")
		}
		return CustomerUnbindOptions{}, fmt.Errorf("解绑验证选项请求失败（HTTP %d）", statusCode)
	}
	var response customerUnbindOptionsResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return CustomerUnbindOptions{}, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status != "OK" {
		return CustomerUnbindOptions{}, customerUnbindStatusError(response.Status, response.Message)
	}
	return CustomerUnbindOptions{Preferred: response.Preferred, Methods: response.Methods, Destination: response.Destination}, nil
}

// CustomerUnbindChallenge - 向授权账号登记的邮箱或手机号发送一次性验证码。
func (this *Client) CustomerUnbindChallenge(ctx context.Context, channel string) (CustomerUnbindChallenge, error) {
	if _, err := this.customerUnbindToken("发送解绑验证码"); err != nil {
		return CustomerUnbindChallenge{}, err
	}
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel != "email" && channel != "sms" {
		return CustomerUnbindChallenge{}, errors.New("解绑验证码仅支持邮箱或手机号")
	}
	body, err := json.Marshal(customerUnbindChallengeBody{Channel: channel})
	if err != nil {
		return CustomerUnbindChallenge{}, err
	}
	statusCode, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/licenses/customer-unbind/challenge", body, true)
	if err != nil {
		return CustomerUnbindChallenge{}, err
	}
	if statusCode != http.StatusOK {
		if statusCode == http.StatusNotFound {
			return CustomerUnbindChallenge{}, errors.New("许可证或实例信息无效")
		}
		return CustomerUnbindChallenge{}, fmt.Errorf("解绑验证码发送失败（HTTP %d）", statusCode)
	}
	var response customerUnbindChallengeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return CustomerUnbindChallenge{}, err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status != "OK" {
		return CustomerUnbindChallenge{}, customerUnbindStatusError(response.Status, response.Message)
	}
	return CustomerUnbindChallenge{ChallengeNo: response.ChallengeNo, ExpiresAt: response.ExpiresAt, Destination: response.Destination}, nil
}

// CustomerUnbind - 分级验证后释放席位并回退基础免费套餐，业务拒绝原样透传。
func (this *Client) CustomerUnbind(ctx context.Context, input CustomerUnbindInput) error {
	if _, err := this.customerUnbindToken("解绑授权账号"); err != nil {
		return err
	}
	input.Method = strings.ToLower(strings.TrimSpace(input.Method))
	if input.Method == "" {
		input.Method = "totp"
	}
	input.Code = strings.TrimSpace(input.Code)
	input.ChallengeNo = strings.TrimSpace(input.ChallengeNo)
	switch input.Method {
	case "totp":
		if input.Code == "" {
			return errors.New("两步验证码不能为空")
		}
	case "email", "sms":
		if input.ChallengeNo == "" || input.Code == "" {
			return errors.New("解绑挑战号与验证码不能为空")
		}
	case "password":
		if input.Password == "" {
			return errors.New("授权账号密码不能为空")
		}
	default:
		return errors.New("不支持的解绑验证方式")
	}
	legacyCode := ""
	if input.Method == "totp" {
		legacyCode = input.Code
	}
	body, err := json.Marshal(customerUnbindBody{
		Method: input.Method, Code: input.Code, ChallengeNo: input.ChallengeNo, Password: input.Password,
		TwoFACode: legacyCode,
	})
	if err != nil {
		return err
	}
	statusCode, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/licenses/customer-unbind", body, true)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK {
		if statusCode == http.StatusNotFound {
			return errors.New("许可证或实例信息无效")
		}
		return fmt.Errorf("客户解绑请求失败（HTTP %d）", statusCode)
	}
	var response customerUnbindResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)
	if response.Status != "OK" {
		return customerUnbindStatusError(response.Status, response.Message)
	}
	return nil
}
