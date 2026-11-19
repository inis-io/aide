package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
)

// ============================= 桶配置族 - 开始 =============================

const (
	// maxCorsRules - CORS 规则数上限（网关约束 1-100）
	maxCorsRules = 100
	// maxLifecycleRules - 生命周期规则数上限（网关约束 1-1000）
	maxLifecycleRules = 1000
)

const (
	// VersioningEnabled - 版本控制开启
	VersioningEnabled = "Enabled"
	// VersioningSuspended - 版本控制暂停（开启后不可回 off，只可暂停）
	VersioningSuspended = "Suspended"
)

// CORSRule - 跨域规则（与网关 corsRuleXML 逐字段对应）
type CORSRule struct {
	// AllowedOrigins - 允许来源（支持通配 *）
	AllowedOrigins []string
	// AllowedMethods - 允许方法（GET/PUT/POST/DELETE/HEAD）
	AllowedMethods []string
	// AllowedHeaders - 允许请求头（空 = 不声明）
	AllowedHeaders []string
	// ExposeHeaders - 暴露响应头（空 = 不声明）
	ExposeHeaders []string
	// MaxAgeSeconds - 预检缓存秒数（0 = 不声明）
	MaxAgeSeconds int64
}

// LifecycleRule - 生命周期规则（平台支持子集：前缀过滤 + 三个动作，至少配置一个动作）
type LifecycleRule struct {
	// ID - 规则标识（同一配置内唯一）
	ID string
	// Status - 规则状态（Enabled / Disabled）
	Status string
	// Prefix - 前缀过滤（空 = 全桶）
	Prefix string
	// ExpirationDays - 当前版本过期天数（0 = 不配置）
	ExpirationDays int64
	// NoncurrentDays - 历史版本过期天数（0 = 不配置；需桶已开启版本控制）
	NoncurrentDays int64
	// AbortIncompleteDays - 未完成分片上传中止天数（0 = 不配置）
	AbortIncompleteDays int64
}

// GetBucketCors - 取桶 CORS 配置（GET /{bucket}?cors；未配置返回 *S3Error{Code: NoSuchCORSConfiguration}）
func (this *Client) GetBucketCors(ctx context.Context, bucket string) ([]CORSRule, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, query: map[string]string{"cors": ""}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlCORSConfiguration
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：GetBucketCors 响应解析失败（%v）", ErrTransport, err)
	}

	rules := make([]CORSRule, 0, len(raw.Rules))
	for _, item := range raw.Rules {
		rules = append(rules, CORSRule{
			AllowedOrigins: item.AllowedOrigin,
			AllowedMethods: item.AllowedMethod,
			AllowedHeaders: item.AllowedHeader,
			ExposeHeaders:  item.ExposeHeader,
			MaxAgeSeconds:  item.MaxAgeSeconds,
		})
	}
	return rules, nil
}

// PutBucketCors - 写桶 CORS 配置（PUT /{bucket}?cors，整体覆盖语义）
func (this *Client) PutBucketCors(ctx context.Context, bucket string, rules []CORSRule) error {

	if err := checkCorsRules(rules); err != nil {
		return err
	}

	raw := xmlCORSConfiguration{Rules: make([]xmlCORSRule, 0, len(rules))}
	for _, rule := range rules {
		raw.Rules = append(raw.Rules, xmlCORSRule{
			AllowedOrigin:  rule.AllowedOrigins,
			AllowedMethod:  rule.AllowedMethods,
			AllowedHeader:  rule.AllowedHeaders,
			ExposeHeader:   rule.ExposeHeaders,
			MaxAgeSeconds:  rule.MaxAgeSeconds,
		})
	}

	return this.putBucketConfig(ctx, bucket, "cors", raw)
}

// DeleteBucketCors - 删桶 CORS 配置（DELETE /{bucket}?cors）
func (this *Client) DeleteBucketCors(ctx context.Context, bucket string) error {

	return this.deleteBucketConfig(ctx, bucket, "cors")
}

// GetBucketVersioning - 取桶版本控制状态（GET /{bucket}?versioning；"" = 未开启，S3 语义不返回 404）
func (this *Client) GetBucketVersioning(ctx context.Context, bucket string) (string, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, query: map[string]string{"versioning": ""}})
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlVersioningConfiguration
	if err = xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&raw); err != nil {
		return "", fmt.Errorf("%w：GetBucketVersioning 响应解析失败（%v）", ErrTransport, err)
	}
	return raw.Status, nil
}

// PutBucketVersioning - 写桶版本控制状态（PUT /{bucket}?versioning；仅收 Enabled / Suspended）
func (this *Client) PutBucketVersioning(ctx context.Context, bucket, status string) error {

	if status != VersioningEnabled && status != VersioningSuspended {
		return fmt.Errorf("%w：版本控制状态仅支持 %s / %s", ErrInvalidRequest, VersioningEnabled, VersioningSuspended)
	}

	return this.putBucketConfig(ctx, bucket, "versioning", xmlVersioningConfiguration{Status: status})
}

// GetBucketLifecycle - 取桶生命周期配置（GET /{bucket}?lifecycle；未配置返回 *S3Error{Code: NoSuchLifecycleConfiguration}）
func (this *Client) GetBucketLifecycle(ctx context.Context, bucket string) ([]LifecycleRule, error) {

	response, err := this.do(ctx, requestInput{method: http.MethodGet, bucket: bucket, query: map[string]string{"lifecycle": ""}})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()

	var raw xmlLifecycleConfiguration
	if err = xml.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w：GetBucketLifecycle 响应解析失败（%v）", ErrTransport, err)
	}

	rules := make([]LifecycleRule, 0, len(raw.Rules))
	for _, item := range raw.Rules {
		rule := LifecycleRule{
			ID:     item.ID,
			Status: item.Status,
			Prefix: item.Filter.Prefix,
		}
		// 旧式裸 Prefix 归一（Filter 优先）
		if rule.Prefix == "" {
			rule.Prefix = item.Prefix
		}
		if item.Expiration != nil {
			rule.ExpirationDays = item.Expiration.Days
		}
		if item.NoncurrentVersionExpiration != nil {
			rule.NoncurrentDays = item.NoncurrentVersionExpiration.NoncurrentDays
		}
		if item.AbortIncompleteMultipartUpload != nil {
			rule.AbortIncompleteDays = item.AbortIncompleteMultipartUpload.DaysAfterInitiation
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// PutBucketLifecycle - 写桶生命周期配置（PUT /{bucket}?lifecycle，整体覆盖语义）
func (this *Client) PutBucketLifecycle(ctx context.Context, bucket string, rules []LifecycleRule) error {

	if err := checkLifecycleRules(rules); err != nil {
		return err
	}

	raw := xmlLifecycleConfiguration{Rules: make([]xmlLifecycleRule, 0, len(rules))}
	for _, rule := range rules {
		item := xmlLifecycleRule{
			ID:     rule.ID,
			Status: rule.Status,
			Filter: xmlLifecycleFilter{Prefix: rule.Prefix},
		}
		if rule.ExpirationDays > 0 {
			item.Expiration = &xmlLifecycleExpiration{Days: rule.ExpirationDays}
		}
		if rule.NoncurrentDays > 0 {
			item.NoncurrentVersionExpiration = &xmlLifecycleNoncurrent{NoncurrentDays: rule.NoncurrentDays}
		}
		if rule.AbortIncompleteDays > 0 {
			item.AbortIncompleteMultipartUpload = &xmlLifecycleAbortMultipart{DaysAfterInitiation: rule.AbortIncompleteDays}
		}
		raw.Rules = append(raw.Rules, item)
	}

	return this.putBucketConfig(ctx, bucket, "lifecycle", raw)
}

// DeleteBucketLifecycle - 删桶生命周期配置（DELETE /{bucket}?lifecycle）
func (this *Client) DeleteBucketLifecycle(ctx context.Context, bucket string) error {

	return this.deleteBucketConfig(ctx, bucket, "lifecycle")
}

// putBucketConfig - 桶配置写入公共件（PUT /{bucket}?<sub>，XML 请求体，成功 200 空体）
func (this *Client) putBucketConfig(ctx context.Context, bucket, sub string, payload any) error {

	if bucket == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	body, err := xml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w：桶配置序列化失败（%v）", ErrInvalidRequest, err)
	}

	response, err := this.do(ctx, requestInput{
		method:  http.MethodPut,
		bucket:  bucket,
		query:   map[string]string{sub: ""},
		headers: map[string]string{"Content-Type": "application/xml"},
		body:    bytes.NewReader(body),
	})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// deleteBucketConfig - 桶配置删除公共件（DELETE /{bucket}?<sub>，成功 204）
func (this *Client) deleteBucketConfig(ctx context.Context, bucket, sub string) error {

	if bucket == "" {
		return fmt.Errorf("%w：bucket 不能为空", ErrInvalidRequest)
	}

	response, err := this.do(ctx, requestInput{method: http.MethodDelete, bucket: bucket, query: map[string]string{sub: ""}})
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// checkCorsRules - CORS 规则集本地校验（条数界内、每条至少一个来源与一个方法）
func checkCorsRules(rules []CORSRule) error {

	if len(rules) == 0 || len(rules) > maxCorsRules {
		return fmt.Errorf("%w：CORS 规则数须在 1-%d 之间", ErrInvalidRequest, maxCorsRules)
	}
	for i, rule := range rules {
		if len(rule.AllowedOrigins) == 0 {
			return fmt.Errorf("%w：第 %d 条 CORS 规则缺少 AllowedOrigins", ErrInvalidRequest, i+1)
		}
		if len(rule.AllowedMethods) == 0 {
			return fmt.Errorf("%w：第 %d 条 CORS 规则缺少 AllowedMethods", ErrInvalidRequest, i+1)
		}
	}
	return nil
}

// checkLifecycleRules - 生命周期规则集本地校验（条数界内、状态合法、至少一个动作）
func checkLifecycleRules(rules []LifecycleRule) error {

	if len(rules) == 0 || len(rules) > maxLifecycleRules {
		return fmt.Errorf("%w：生命周期规则数须在 1-%d 之间", ErrInvalidRequest, maxLifecycleRules)
	}
	for i, rule := range rules {
		if rule.Status != "Enabled" && rule.Status != "Disabled" {
			return fmt.Errorf("%w：第 %d 条生命周期规则 Status 仅支持 Enabled / Disabled", ErrInvalidRequest, i+1)
		}
		if rule.ExpirationDays <= 0 && rule.NoncurrentDays <= 0 && rule.AbortIncompleteDays <= 0 {
			return fmt.Errorf("%w：第 %d 条生命周期规则至少配置一个动作", ErrInvalidRequest, i+1)
		}
	}
	return nil
}

// ============================= 桶配置族 - 结束 =============================
