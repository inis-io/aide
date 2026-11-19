package types

import (
	"fmt"
	"strconv"
	"strings"
)

// ============================= 回收站 - 开始 =============================

// RecycleEntry - 回收站条目
type RecycleEntry struct {
	// Id - 对象行 ID（还原/释放凭据）
	Id int64 `json:"id"`
	// Bucket - 桶名
	Bucket string `json:"bucket"`
	// Key - 对象键
	Key string `json:"key"`
	// Etag - ETag
	Etag string `json:"etag"`
	// Size - 字节数
	Size int64 `json:"size"`
	// Mime - Content-Type
	Mime string `json:"mime"`
	// StorageClass - 存储级别
	StorageClass string `json:"storageClass"`
	// DeletedAt - 删除时间（毫秒）
	DeletedAt int64 `json:"deletedAt"`
	// PurgeAt - 到期物理释放时间（毫秒）
	PurgeAt int64 `json:"purgeAt"`
}

// RecycleListRequest - 回收站列表请求
type RecycleListRequest struct {
	// Bucket - 桶名（空 = 全部桶）
	Bucket string `json:"bucket"`
	// Keyword - key 模糊关键词
	Keyword string `json:"keyword"`
	// Page - 页码（默认 1）
	Page int `json:"page"`
	// Limit - 每页条数（默认 20，服务端封顶 200）
	Limit int `json:"limit"`
}

// Normalize - 注入服务端默认值（签名前调用，两条链路共用）
func (this *RecycleListRequest) Normalize() {

	if this.Page <= 0 {
		this.Page = 1
	}
	if this.Limit <= 0 {
		this.Limit = 20
	}
}

// RecycleListResult - 回收站列表结果
type RecycleListResult struct {
	// Entries - 回收站条目
	Entries []RecycleEntry `json:"entries"`
	// Total - 总条数
	Total int64 `json:"total"`
}

// RecycleRestoreRequest - 回收站还原请求
type RecycleRestoreRequest struct {
	// Ids - 对象行 ID 列表（必填；签名参数集限定标量，契约中以逗号分隔字符串承载）
	Ids []int64 `json:"-"`
}

// JoinIds - 将 ID 列表编码为契约字符串（"1,2,3"）
func JoinIds(ids []int64) string {

	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, strconv.FormatInt(id, 10))
	}
	return strings.Join(items, ",")
}

// ParseIds - 解析契约字符串为 ID 列表（空段忽略，非法段返回错误）
func ParseIds(raw string) ([]int64, error) {

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}

	parts := strings.Split(trimmed, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ids 含非法数字段 %q", part)
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// RecycleRestoreResult - 回收站还原结果
type RecycleRestoreResult struct {
	// Restored - 已还原数量
	Restored int64 `json:"restored"`
	// Conflicts - 冲突跳过的 key 列表（回收期间被重新上传占用）
	Conflicts []string `json:"conflicts"`
}

// RecyclePurgeRequest - 回收站指定释放请求（硬删元数据 + 物理清理，不可恢复）
type RecyclePurgeRequest struct {
	// Ids - 对象行 ID 列表（必填）
	Ids []int64 `json:"-"`
}

// RecyclePurgeResult - 回收站释放结果
type RecyclePurgeResult struct {
	// Purged - 已释放数量
	Purged int64 `json:"purged"`
}

// RecycleClearRequest - 回收站全部释放请求（可选指定桶；不可恢复）
type RecycleClearRequest struct {
	// Bucket - 桶名（空 = 全部桶）
	Bucket string `json:"bucket"`
}

// ============================= 回收站 - 结束 =============================
