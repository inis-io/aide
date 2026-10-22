// Package sign - inis-pay 开放平台的请求签名算法。
//
// 算法与平台服务端 utils.Md5.GenSign 逐字节等价：参数集（HTTP 为 JSON 展开的 map，
// gRPC 为 protojson 展开的 map）→ 剔除 sign 键 → 剔除空值 → 键字典序拼
// k1=v1&k2=v2 → 追加 &secret=<商户密钥> → MD5 十六进制转大写。
//
// 为什么自带实现而不是复用 aide/utils：utils.Md5 是携带可变字段（SetFields/SetOmitEmpty）
// 的包级全局单例，并发不安全；本包为零状态纯函数，可并发、可独立测试。
//
// 演进：平台开放 API v1（HMAC-SHA256 + timestamp/nonce）落地后新增 Signer 实现即可，
// 商户代码只需换注入的签名器（openpay.WithSigner）。
package sign

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ============================= 签名器 - 开始 =============================

// 签名固定约定（与平台服务端 utils.Md5 的默认配置一致：SignName=sign、SecretName=secret、
// 过滤空值、结果大写）。此处以常量固化，不做运行期可改配置，避免全局可变状态。
const (
	// signField - 签名键名（签名输入中该键始终被剔除）
	signField = "sign"
	// secretField - 密钥键名（拼在签名串末尾，形如 &secret=<商户密钥>）
	secretField = "secret"
)

// Signer - 请求签名器（两条链路共用）
type Signer interface {
	// Sign - 对参数集生成签名（参数集键名一律 camelCase，可含 sign 键与空值，内部自行剔除）
	Sign(params map[string]any, secret string) string
	// Verify - 校验签名（忽略首尾空白与大小写）
	Verify(params map[string]any, secret, digest string) bool
}

// MD5 - MD5 签名器（无状态，可并发使用）
type MD5 struct{}

// New - 创建默认签名器（MD5）
func New() Signer { return &MD5{} }

// Sign - 生成 32 位大写十六进制签名
/**
 * 算法（与平台服务端 utils.Md5.GenSign 逐字节等价）：
 *  1. 剔除 sign 键
 *  2. 剔除「字符串形式为空」的值（空串 / nil；数值 0 与 false 会格式化为 "0"/"false" 参与签名）
 *  3. 剩余键按字典序排序，拼成 k1=v1&k2=v2
 *  4. 末尾追加 &secret=<secret>
 *  5. MD5 → 十六进制 → 转大写
 *
 * @param params map[string]any - 参数集
 * @param secret string - 商户密钥
 * @return string - 空参数集时返回 MD5("&secret=<secret>") 的大写值
 */
func (this *MD5) Sign(params map[string]any, secret string) string {

	sum := md5.Sum([]byte(this.payload(params) + "&" + secretField + "=" + secret))

	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// Verify - 校验签名（常量时间比较，避免计时侧信道）
/**
 * @param params map[string]any - 参数集（含 sign 键亦可，内部剔除）
 * @param secret string - 商户密钥
 * @param digest string - 待校验签名（大小写不敏感）
 * @return bool - 是否通过
 */
func (this *MD5) Verify(params map[string]any, secret, digest string) bool {

	expect := strings.ToUpper(strings.TrimSpace(digest))
	if expect == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(this.Sign(params, secret)), []byte(expect)) == 1
}

// payload - 拼装待签名串（剔除 sign 与空值，键字典序升序）
func (this *MD5) payload(params map[string]any) string {

	keys := make([]string, 0, len(params))
	for key := range params {
		if key == "" || key == signField {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		value := Value(params[key])
		// 与服务端 utils.Ascii.ToString 一致：只剔除「字符串形式为空」的值
		if value == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(value)
	}

	return builder.String()
}

// Value - 将参数值格式化为签名串中的字面量
// 与平台服务端 cast.ToString 的取值逐项对齐，数字格式化一律 FormatFloat(v,'f',-1,64)：
// 12.30 → "12.3"、12.00 → "12"；bool 输出 "true"/"false"；nil 输出空串（被剔除）。
// HTTP 链路组装 query 时复用同一格式化，保证 query 值与签名输入完全一致。
/**
 * @param value any - 参数值
 * @return string - 字面量
 */
func Value(value any) string {

	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case json.Number:
		return typed.String()
	case []byte:
		return string(typed)
	case error:
		return typed.Error()
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprintf("%v", value)
	}
}

// ============================= 签名器 - 结束 =============================
