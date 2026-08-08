package pay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RequestFingerprint - 计算不含 Metadata、Raw 和敏感明文的确定性请求指纹
func RequestFingerprint(request any) (string, error) {
	var canonical any
	switch value := request.(type) {
	case TradeCreateRequest:
		canonical = struct {
			OutTradeNo                                                   string
			Mode                                                         TradeMode
			Subject                                                      string
			Amount                                                       Money
			NotifyURL, ReturnURL, CancelURL, ClientIP, AuthCode, BuyerID string
			Extensions                                                   Extensions
		}{value.OutTradeNo, value.Mode, value.Subject, value.Amount, value.NotifyURL, value.ReturnURL, value.CancelURL, value.ClientIP, value.AuthCode, value.BuyerID, value.Extensions}
	case RefundRequest:
		canonical = struct {
			OutTradeNo, GatewayTradeNo, OutRefundNo string
			TotalAmount, RefundAmount               Money
			Reason, NotifyURL                       string
			Extensions                              Extensions
		}{value.OutTradeNo, value.GatewayTradeNo, value.OutRefundNo, value.TotalAmount, value.RefundAmount, value.Reason, value.NotifyURL, value.Extensions}
	case TransferRequest:
		payeeDigest := sha256.Sum256([]byte(string(value.Payee.Type) + "\x00" + value.Payee.Account + "\x00" + value.Payee.Name))
		canonical = struct {
			OutTransferNo             string
			Amount                    Money
			PayeeHash                 string
			Subject, NotifyURL, Scene string
			SceneReport               map[string]string
			Extensions                Extensions
		}{value.OutTransferNo, value.Amount, hex.EncodeToString(payeeDigest[:]), value.Subject, value.NotifyURL, value.Scene, value.SceneReport, value.Extensions}
	case TradeCaptureRequest:
		canonical = struct {
			OutTradeNo, GatewayTradeNo string
			Amount                     Money
			Extensions                 Extensions
		}{value.OutTradeNo, value.GatewayTradeNo, value.Amount, value.Extensions}
	default:
		canonical = request
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func outNoHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:8])
}
