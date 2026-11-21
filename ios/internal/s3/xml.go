package s3

import (
	"encoding/xml"
	"strings"
	"time"
)

// ============================= XML 模型 - 开始 =============================

// xmlTime - ISO8601 毫秒时间（解析失败容忍零值）
type xmlTime struct {
	time.Time
}

// UnmarshalXML - 解析 ISO8601（兼容带毫秒与不带毫秒两种形态）
func (this *xmlTime) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {

	var raw string
	if err := decoder.DecodeElement(&raw, &start); err != nil {
		return err
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000Z07:00", time.RFC3339} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			this.Time = parsed
			return nil
		}
	}
	return nil
}

// xmlListAllMyBucketsResult - ListBuckets 响应
type xmlListAllMyBucketsResult struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Buckets []struct {
		Name         string  `xml:"Name"`
		CreationDate xmlTime `xml:"CreationDate"`
	} `xml:"Buckets>Bucket"`
}

// xmlListBucketResult - ListObjectsV2 响应
type xmlListBucketResult struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Name                  string   `xml:"Name"`
	Prefix                string   `xml:"Prefix"`
	MaxKeys               int      `xml:"MaxKeys"`
	IsTruncated           bool     `xml:"IsTruncated"`
	NextContinuationToken string   `xml:"NextContinuationToken"`
	Contents              []struct {
		Key          string  `xml:"Key"`
		LastModified xmlTime `xml:"LastModified"`
		ETag         string  `xml:"ETag"`
		Size         int64   `xml:"Size"`
		StorageClass string  `xml:"StorageClass"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

// xmlInitiateMultipartUploadResult - CreateMultipartUpload 响应
type xmlInitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

// xmlCompletePart - CompleteMultipartUpload 请求分片项
type xmlCompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// xmlCompleteMultipartUpload - CompleteMultipartUpload 请求体
type xmlCompleteMultipartUpload struct {
	XMLName xml.Name          `xml:"CompleteMultipartUpload"`
	Parts   []xmlCompletePart `xml:"Part"`
}

// xmlCompleteMultipartUploadResult - CompleteMultipartUpload 响应
type xmlCompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// xmlInstantResult - 秒传探测响应（POST /{bucket}/{key}?instant；Hit=true 表示命中并已建关系链）
type xmlInstantResult struct {
	XMLName xml.Name `xml:"InstantResult"`
	ETag    string   `xml:"ETag"`
	Hit     bool     `xml:"Hit"`
}

// xmlListPartsResult - ListParts 响应
type xmlListPartsResult struct {
	XMLName              xml.Name `xml:"ListPartsResult"`
	Bucket               string   `xml:"Bucket"`
	Key                  string   `xml:"Key"`
	UploadId             string   `xml:"UploadId"`
	IsTruncated          bool     `xml:"IsTruncated"`
	NextPartNumberMarker int      `xml:"NextPartNumberMarker"`
	Parts                []struct {
		PartNumber   int     `xml:"PartNumber"`
		LastModified xmlTime `xml:"LastModified"`
		ETag         string  `xml:"ETag"`
		Size         int64   `xml:"Size"`
	} `xml:"Part"`
}

// xmlListMultipartUploadsResult - ListMultipartUploads 响应
type xmlListMultipartUploadsResult struct {
	XMLName            xml.Name `xml:"ListMultipartUploadsResult"`
	Bucket             string   `xml:"Bucket"`
	KeyMarker          string   `xml:"KeyMarker"`
	UploadIdMarker     string   `xml:"UploadIdMarker"`
	NextKeyMarker      string   `xml:"NextKeyMarker"`
	NextUploadIdMarker string   `xml:"NextUploadIdMarker"`
	IsTruncated        bool     `xml:"IsTruncated"`
	Uploads            []struct {
		Key       string  `xml:"Key"`
		UploadId  string  `xml:"UploadId"`
		Initiated xmlTime `xml:"Initiated"`
	} `xml:"Upload"`
}

// xmlDeleteObjectEntry - DeleteObjects 请求对象条目
type xmlDeleteObjectEntry struct {
	Key string `xml:"Key"`
}

// xmlDeleteRequest - DeleteObjects 请求体
type xmlDeleteRequest struct {
	XMLName xml.Name              `xml:"Delete"`
	Quiet   bool                  `xml:"Quiet"`
	Objects []xmlDeleteObjectEntry `xml:"Object"`
}

// xmlDeleteResult - DeleteObjects 响应
type xmlDeleteResult struct {
	XMLName xml.Name `xml:"DeleteResult"`
	Deleted []struct {
		Key string `xml:"Key"`
	} `xml:"Deleted"`
	Errors []struct {
		Key     string `xml:"Key"`
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	} `xml:"Error"`
}

// xmlCopyObjectResult - CopyObject 响应
type xmlCopyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	ETag         string   `xml:"ETag"`
	LastModified xmlTime `xml:"LastModified"`
}

// xmlCORSRule - CORS 规则条目（与网关 corsRuleXML 逐字段对应）
type xmlCORSRule struct {
	AllowedOrigin []string `xml:"AllowedOrigin"`
	AllowedMethod []string `xml:"AllowedMethod"`
	AllowedHeader []string `xml:"AllowedHeader,omitempty"`
	ExposeHeader  []string `xml:"ExposeHeader,omitempty"`
	MaxAgeSeconds int64    `xml:"MaxAgeSeconds,omitempty"`
}

// xmlCORSConfiguration - PutBucketCors 请求体 / GetBucketCors 响应
type xmlCORSConfiguration struct {
	XMLName xml.Name     `xml:"CORSConfiguration"`
	Rules   []xmlCORSRule `xml:"CORSRule"`
}

// xmlVersioningConfiguration - PutBucketVersioning 请求体 / GetBucketVersioning 响应
type xmlVersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

// xmlLifecycleFilter - 生命周期规则过滤器（仅前缀语义）
type xmlLifecycleFilter struct {
	Prefix string `xml:"Prefix,omitempty"`
}

// xmlLifecycleExpiration - 当前版本过期动作
type xmlLifecycleExpiration struct {
	Days int64 `xml:"Days"`
}

// xmlLifecycleNoncurrent - 历史版本过期动作
type xmlLifecycleNoncurrent struct {
	NoncurrentDays int64 `xml:"NoncurrentDays"`
}

// xmlLifecycleAbortMultipart - 未完成分片上传中止动作
type xmlLifecycleAbortMultipart struct {
	DaysAfterInitiation int64 `xml:"DaysAfterInitiation"`
}

// xmlLifecycleRule - 生命周期规则条目（Filter 为新版形态；裸 Prefix 为旧式兼容，解析时归一）
type xmlLifecycleRule struct {
	ID                             string                       `xml:"ID"`
	Status                         string                       `xml:"Status"`
	Filter                         xmlLifecycleFilter           `xml:"Filter"`
	Prefix                         string                       `xml:"Prefix,omitempty"`
	Expiration                     *xmlLifecycleExpiration      `xml:"Expiration,omitempty"`
	NoncurrentVersionExpiration    *xmlLifecycleNoncurrent      `xml:"NoncurrentVersionExpiration,omitempty"`
	AbortIncompleteMultipartUpload *xmlLifecycleAbortMultipart  `xml:"AbortIncompleteMultipartUpload,omitempty"`
}

// xmlLifecycleConfiguration - PutBucketLifecycle 请求体 / GetBucketLifecycle 响应
type xmlLifecycleConfiguration struct {
	XMLName xml.Name           `xml:"LifecycleConfiguration"`
	Rules   []xmlLifecycleRule `xml:"Rule"`
}

// xmlGetSessionTokenResponse - STS GetSessionToken 响应（AWS STS 兼容形态）
type xmlGetSessionTokenResponse struct {
	XMLName xml.Name `xml:"GetSessionTokenResponse"`
	Result  struct {
		Credentials struct {
			AccessKeyId     string `xml:"AccessKeyId"`
			SecretAccessKey string `xml:"SecretAccessKey"`
			SessionToken    string `xml:"SessionToken"`
			Expiration      string `xml:"Expiration"`
		} `xml:"Credentials"`
	} `xml:"GetSessionTokenResult"`
}

// xmlNotificationTopic - 事件通知条目（Secret 留空由服务端生成，GET 回读可见）
type xmlNotificationTopic struct {
	Id     string   `xml:"Id,omitempty"`
	Topic  string   `xml:"Topic"`
	Events []string `xml:"Event"`
	Secret string   `xml:"Secret,omitempty"`
}

// xmlNotificationConfiguration - PutBucketNotification 请求体 / GetBucketNotification 响应
type xmlNotificationConfiguration struct {
	XMLName xml.Name                `xml:"NotificationConfiguration"`
	Topics  []xmlNotificationTopic `xml:"TopicConfiguration"`
}

// trimETag - 去除 ETag 值的双引号（S3 返回的 ETag 带引号）
func trimETag(etag string) string {

	return strings.Trim(etag, `"`)
}

// quoteETag - Complete 清单中的 ETag 需要带引号回传
func quoteETag(etag string) string {

	trimmed := trimETag(etag)
	if trimmed == "" {
		return ""
	}
	return `"` + trimmed + `"`
}

// ============================= XML 模型 - 结束 =============================
