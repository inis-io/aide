package licence

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	LicenceProtocol "github.com/inis-io/aide/licence/protocol"
)

// activateBody - 激活请求体（契约 §2.1；activate 本身不签名，上送客户端公钥注册）
type activateBody struct {
	LicenseNo       string `json:"licenseNo"`
	InstanceNo      string `json:"instanceNo,omitempty"`
	FingerprintHash string `json:"fingerprintHash"`
	ClientPublicKey string `json:"clientPublicKey"`
	ClientTime      int64  `json:"clientTime,omitempty"`
}

// validateBody - 校验请求体（契约 §2.2）
type validateBody struct {
	LicenseNo       string           `json:"licenseNo"`
	FingerprintHash string           `json:"fingerprintHash"`
	Version         string           `json:"version,omitempty"`
	Feature         string           `json:"feature,omitempty"`
	Usage           map[string]int64 `json:"usage,omitempty"`
	ClientTime      int64            `json:"clientTime,omitempty"`
}

// runtimeResponse - 运行面统一响应（契约 §2；envelope 为原文，验签后替换缓存）
type runtimeResponse struct {
	Status          string          `json:"status"`
	ServerTime      int64           `json:"serverTime"`
	Envelope        json.RawMessage `json:"envelope"`
	ActivationNo    string          `json:"activationNo"`
	ActivationToken string          `json:"activationToken"`
	ExpiresAt       int64           `json:"expiresAt"`
	Message         string          `json:"message"`
}

// activateLocked - 执行激活（首激活/重新激活）：注册客户端公钥 + 建立指纹绑定 +
// 换取激活令牌与短期签名信封。调用前必须持有 opMu。
func (this *Client) activateLocked(ctx context.Context) error {

	// 确保客户端密钥对存在（没有则现场生成，私钥不出本机）
	this.mu.Lock()
	if this.state.ClientSeed == "" {
		seed, _, err := generateKeyPair()
		if err != nil {
			this.mu.Unlock()
			return err
		}
		this.state.ClientSeed = hex.EncodeToString(seed)
	}
	seedHex := this.state.ClientSeed
	this.mu.Unlock()

	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		return err
	}
	publicKey := hex.EncodeToString(ed25519PublicKey(seed))

	body, err := json.Marshal(activateBody{
		LicenseNo: this.options.LicenseNo, InstanceNo: this.options.InstanceNo,
		FingerprintHash: this.fingerprint, ClientPublicKey: publicKey,
		ClientTime: time.Now().UnixMilli(),
	})
	if err != nil {
		return err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/licenses/activate", body, false)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound {
		this.setStatus(StatusNotFound)
		return errors.New("许可证或实例信息无效")
	}

	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)

	if response.Status != StatusValid && response.Status != StatusExpiring && response.Status != StatusGrace {
		this.setStatus(response.Status)
		this.persist()
		return errors.New("激活被拒绝：" + response.Status)
	}

	// 验签信封后持久化（token 仅此一次返回，丢失只能重新激活）
	if err = this.applyEnvelope(response.Envelope); err != nil {
		return err
	}
	this.mu.Lock()
	this.state.ActivationToken = response.ActivationToken
	this.state.ActivationNo = response.ActivationNo
	this.state.ExpiresAt = response.ExpiresAt
	this.mu.Unlock()
	this.setStatus(response.Status)
	this.persist()
	return nil
}

// validateLocked - 执行运行面校验 + 滑动刷新（请求签名）。调用前必须持有 opMu。
func (this *Client) validateLocked(ctx context.Context) error {

	this.mu.RLock()
	usage := make(map[string]int64, len(this.pendingUsage))
	for key, value := range this.pendingUsage {
		usage[key] = value
	}
	this.mu.RUnlock()

	body, err := json.Marshal(validateBody{
		LicenseNo: this.options.LicenseNo, FingerprintHash: this.fingerprint,
		Version: this.options.Version, Usage: usage,
		ClientTime: time.Now().UnixMilli(),
	})
	if err != nil {
		return err
	}

	code, raw, err := this.doRequest(ctx, http.MethodPost, "/api/v1/licenses/validate", body, true)
	if err != nil {
		// 网络异常：沿用本地缓存降级运行（契约 §5）
		this.offline()
		return err
	}
	if code == http.StatusNotFound {
		this.setStatus(StatusNotFound)
		return errors.New("许可证或实例信息无效")
	}

	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return err
	}
	this.updateClockOffset(response.ServerTime)

	// 服务端已接收本次请求（含用量上报），清空待上报表
	this.mu.Lock()
	clear(this.pendingUsage)
	this.mu.Unlock()

	// 服务端故障按网络异常处理（契约 §5）
	if response.Status == StatusError {
		this.offline()
		return errors.New("服务端故障：" + response.Message)
	}

	// 凭证失效：清除本地凭证并立即尝试重新激活（引导路径）
	if response.Status == StatusExpired {
		this.mu.Lock()
		this.state.ActivationToken = ""
		this.mu.Unlock()
		this.setStatus(StatusExpired)
		this.persist()
		return this.activateLocked(ctx)
	}

	// 放行状态：滑动刷新 + 载荷变化时替换信封
	if passThrough(response.Status) {
		this.mu.Lock()
		this.state.ExpiresAt = response.ExpiresAt
		this.mu.Unlock()
		if len(response.Envelope) > 0 {
			if err = this.applyEnvelope(response.Envelope); err != nil {
				return err
			}
		}
	}

	this.setStatus(response.Status)
	this.persist()
	return nil
}

// currentLocked - 拉取当前生效信封（契约 §2.3，不做滑动刷新）。调用前必须持有 opMu。
func (this *Client) currentLocked(ctx context.Context) (Envelope, error) {

	uri := "/api/v1/licenses/current?licenseNo=" + this.options.LicenseNo
	code, raw, err := this.doRequest(ctx, http.MethodGet, uri, nil, true)
	if err != nil {
		this.offline()
		return Envelope{}, err
	}
	if code == http.StatusNotFound {
		this.setStatus(StatusNotFound)
		return Envelope{}, errors.New("许可证或实例信息无效")
	}

	var response runtimeResponse
	if err = json.Unmarshal(raw, &response); err != nil {
		return Envelope{}, err
	}
	this.updateClockOffset(response.ServerTime)

	if response.Status != StatusValid && response.Status != StatusExpiring && response.Status != StatusGrace {
		this.setStatus(response.Status)
		this.persist()
		return Envelope{}, errors.New("当前授权不可用：" + response.Status)
	}
	if err = this.applyEnvelope(response.Envelope); err != nil {
		return Envelope{}, err
	}
	this.setStatus(response.Status)
	this.persist()

	this.mu.RLock()
	defer this.mu.RUnlock()
	return this.envelope, nil
}

// doRequest - 统一请求出口：拼接 ServerURL + requestURI，按需附带请求签名三要素
// （契约 §2.4：X-License-Timestamp / X-License-Nonce / X-License-Sign）
func (this *Client) doRequest(ctx context.Context, method string, requestURI string, body []byte, withSign bool) (int, []byte, error) {
	if this.transport == nil {
		return 0, nil, errors.New("运行面传输层未初始化")
	}
	code, raw, err := this.transport.RoundTrip(ctx, method, requestURI, body, withSign)
	this.mu.Lock()
	if err != nil {
		this.retryDelay = time.Minute
	} else {
		this.retryDelay = 0
	}
	this.mu.Unlock()
	return code, raw, err
}

// signHeaders - 生成请求签名头部（契约 §2.4）
// 签名内容 = method\nrequestURI\ntimestamp\nnonce\nsha256hex(body)；
// 时间戳使用校时后的服务端时间（±5 分钟时间窗），nonce 随机生成。
func (this *Client) signHeaders(method string, requestURI string, body []byte) (map[string]string, error) {

	this.mu.RLock()
	token := this.state.ActivationToken
	seedHex := this.state.ClientSeed
	this.mu.RUnlock()

	if token == "" || seedHex == "" {
		return nil, errors.New("凭证缺失，请先激活")
	}
	seed, err := hex.DecodeString(seedHex)
	if err != nil {
		return nil, err
	}

	timestamp := strconv.FormatInt(this.now(), 10)
	nonce := Licence.Nonce()
	content := LicenceProtocol.HTTPContent(method, requestURI, timestamp, nonce, body)
	signature, err := signPayload(content, seed)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"X-License-Token":     token,
		"X-License-Timestamp": timestamp,
		"X-License-Nonce":     nonce,
		"X-License-Sign":      signature,
	}, nil
}

// applyEnvelope - 验签并替换当前缓存信封（公钥按载荷 keyVersion 选取）
func (this *Client) applyEnvelope(raw json.RawMessage) error {

	if len(raw) == 0 {
		return errors.New("响应缺少信封")
	}
	envelope, rawPayload, err := ParseEnvelope(raw)
	if err != nil {
		return err
	}
	publicKey, exist := this.options.PublicKeys[envelope.Payload.KeyVersion]
	if !exist {
		return errors.New("未内置 keyVersion=" + envelope.Payload.KeyVersion + " 的验签公钥")
	}
	if envelope.Version != EnvelopeVersion || envelope.Algorithm != Algorithm ||
		!Licence.VerifyRaw(rawPayload, envelope.Signature, publicKey) {
		return errors.New("信封验签失败")
	}

	this.mu.Lock()
	this.envelope = envelope
	this.state.Envelope = raw
	this.mu.Unlock()
	return nil
}

// updateClockOffset - 用响应 serverTime 校正服务端时间偏移（供签名时间戳与本地判定）
func (this *Client) updateClockOffset(serverTime int64) {

	if serverTime <= 0 {
		return
	}
	this.mu.Lock()
	this.clockOffset = serverTime - time.Now().UnixMilli()
	this.mu.Unlock()
}
