package types

// ============================= 用量 - 开始 =============================

// QuotaView - 有效配额（0 = 不限）
type QuotaView struct {
	// MaxBuckets - 桶数上限
	MaxBuckets int64 `json:"maxBuckets"`
	// MaxObjects - 对象数上限
	MaxObjects int64 `json:"maxObjects"`
	// MaxStorageBytes - 总容量上限（字节）
	MaxStorageBytes int64 `json:"maxStorageBytes"`
	// MaxObjectSize - 单对象大小上限（字节）
	MaxObjectSize int64 `json:"maxObjectSize"`
	// MonthlyBandwidthBytes - 月流量上限（字节）
	MonthlyBandwidthBytes int64 `json:"monthlyBandwidthBytes"`
}

// UsageOverview - 用量总览
type UsageOverview struct {
	// StorageBytes - 存储总量（字节）
	StorageBytes int64 `json:"storageBytes"`
	// ObjectCount - 对象总数
	ObjectCount int64 `json:"objectCount"`
	// BucketCount - 桶数
	BucketCount int64 `json:"bucketCount"`
	// EgressBytes - 当月出口流量（字节）
	EgressBytes int64 `json:"egressBytes"`
	// Quota - 有效配额
	Quota QuotaView `json:"quota"`
}

// BucketUsage - 单桶用量
type BucketUsage struct {
	// Bucket - 桶名（已软删的桶仍展示计量）
	Bucket string `json:"bucket"`
	// StorageBytes - 存储量（字节）
	StorageBytes int64 `json:"storageBytes"`
	// ObjectCount - 对象数
	ObjectCount int64 `json:"objectCount"`
	// EgressBytes - 当月出口流量（字节）
	EgressBytes int64 `json:"egressBytes"`
}

// EgressPoint - 流量趋势点
type EgressPoint struct {
	// Date - 日期（YYYY-MM-DD）
	Date string `json:"date"`
	// Bytes - 出口流量（字节）
	Bytes int64 `json:"bytes"`
}

// ============================= 用量 - 结束 =============================
