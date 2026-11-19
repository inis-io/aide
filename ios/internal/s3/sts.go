package s3

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ============================= STS 临时凭证 - 开始 =============================

const (
	// defaultSessionSeconds - 会话令牌默认有效期（秒，与网关一致）
	defaultSessionSeconds = 3600
	// minSessionSeconds - 会话令牌最小有效期（秒，网关钳制下限）
	minSessionSeconds = 900
	// maxSessionSeconds - 会话令牌最大有效期（秒，网关钳制上限）
	maxSessionSeconds = 43200
)

// SessionCredentials - STS 临时凭证（仅数据面可用；控制面签名只收永久凭证）
type SessionCredentials struct {
	// AccessKey - 临时 AccessKey
	AccessKey string
	// Secret - 临时 SecretKey
	Secret string
	// Token - 会话令牌（x-amz-security-token）
	Token string
	// ExpireAt - 过期时间
	ExpireAt time.Time
}

// GetSessionToken - 以永久凭证签发临时凭证（POST /，Action=GetSessionToken）
// duration ≤ 0 取默认 3600 秒，本地钳制 [900, 43200] 与网关一致；
// 临时凭证不可再签发（防令牌自我续期，网关层同样拒绝）。
func (this *Client) GetSessionToken(ctx context.Context, duration time.Duration) (*SessionCredentials, error) {

	if this.creds.sessionToken != "" {
		return nil, fmt.Errorf("%w：临时凭证不能再签发会话令牌（请用永久凭证的客户端调用）", ErrInvalidRequest)
	}

	seconds := int64(duration / time.Second)
	if seconds <= 0 {
		seconds = defaultSessionSeconds
	}
	if seconds < minSessionSeconds {
		seconds = minSessionSeconds
	}
	if seconds > maxSessionSeconds {
		seconds = maxSessionSeconds
	}

	form := url.Values{}
	form.Set("Action", "GetSessionToken")
	form.Set("DurationSeconds", strconv.FormatInt(seconds, 10))

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPost,
		headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		body:    strings.NewReader(form.Encode()),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlGetSessionTokenResponse
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：GetSessionToken 响应解析失败（%v）", ErrTransport, err)
	}

	credentials := &raw.Result.Credentials
	if credentials.AccessKeyId == "" || credentials.SecretAccessKey == "" || credentials.SessionToken == "" {
		return nil, fmt.Errorf("%w：GetSessionToken 响应缺少凭据字段", ErrTransport)
	}

	expireAt, err := time.Parse(time.RFC3339, credentials.Expiration)
	if err != nil {
		return nil, fmt.Errorf("%w：GetSessionToken 过期时间解析失败（%q）", ErrTransport, credentials.Expiration)
	}

	return &SessionCredentials{
		AccessKey: credentials.AccessKeyId,
		Secret:    credentials.SecretAccessKey,
		Token:     credentials.SessionToken,
		ExpireAt:  expireAt,
	}, nil
}

// ============================= STS 临时凭证 - 结束 =============================
