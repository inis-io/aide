package pay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Extensions - 按 Provider 命名空间隔离的扩展参数
type Extensions map[string]json.RawMessage

// SetExtension - 编码并写入 Provider 专属扩展
func SetExtension[T any](extensions Extensions, provider string, value T) (Extensions, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return nil, fmt.Errorf("%w：扩展命名空间为空", ErrInvalidRequest)
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w：扩展编码失败", ErrInvalidRequest)
	}
	result := make(Extensions, len(extensions)+1)
	for key, raw := range extensions {
		result[key] = append(json.RawMessage(nil), raw...)
	}
	result[provider] = body
	return result, nil
}

// DecodeExtension - 严格解码指定 Provider 的扩展参数
func DecodeExtension(extensions Extensions, provider string, target any) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for key := range extensions {
		if key != provider {
			return fmt.Errorf("%w：不允许的扩展命名空间 %s", ErrInvalidRequest, key)
		}
	}
	raw, ok := extensions[provider]
	if !ok {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w：扩展参数非法", ErrInvalidRequest)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%w：扩展包含多个 JSON 值", ErrInvalidRequest)
	}
	return nil
}
