package licence

// 运行面统一状态码（与 licen-hub 平台 app/common/license-status.go 保持一致，见
// docs/md/许可证运行面接口契约.md 第 4 节；NOT_FOUND/ERROR 为传输层状态，见第 5 节）
const (
	StatusValid             = "VALID"               // 授权正常
	StatusExpiring          = "EXPIRING"            // 即将到期（30 天内）
	StatusGrace             = "GRACE"               // 已过期但仍处于宽限期
	StatusExpired           = "EXPIRED"             // 授权已到期（含宽限期耗尽）或凭证失效
	StatusRevoked           = "REVOKED"             // 许可证已吊销
	StatusSuspended         = "SUSPENDED"           // 因商务或管理原因暂停
	StatusInstanceMismatch  = "INSTANCE_MISMATCH"   // 部署实例或设备绑定不匹配
	StatusVersionNotAllowed = "VERSION_NOT_ALLOWED" // 当前项目版本不在授权范围
	StatusFeatureNotAllowed = "FEATURE_NOT_ALLOWED" // 功能未授权
	StatusLimitExceeded     = "LIMIT_EXCEEDED"      // 人数、设备数或额度超限
	StatusClockTampered     = "CLOCK_TAMPERED"      // 疑似回拨系统时间（仅告警标记，不拒绝服务）
	StatusNotFound          = "NOT_FOUND"           // 许可证/实例信息无效或请求签名不合法（传输层）
	StatusError             = "ERROR"               // 服务端故障（按网络异常处理，沿用本地缓存）
)

// expiringMillis - 「即将到期」窗口（30 天，与平台 LicenseExpiringDays 一致）
const expiringMillis = int64(30) * 24 * 3600 * 1000

// dayMillis - 一天的毫秒数
const dayMillis = int64(24 * 3600 * 1000)

// passThrough - 放行状态判定：VALID/EXPIRING/GRACE/CLOCK_TAMPERED 视为放行（契约 §2.2）
func passThrough(status string) bool {
	return status == StatusValid || status == StatusExpiring ||
		status == StatusGrace || status == StatusClockTampered
}

// localStatus - 离线本地判定（服务端 JudgeLicenseStatus 时间维度的镜像）：
// 依据缓存信封的 validUntil 与 graceDays 判定，用于网络异常/服务端 ERROR 时的降级运行。
// now 为校时后的当前毫秒；validUntil 为 RFC3339 字符串（空串 = 永久授权）。
// 绑定/版本/功能/额度维度不在本地判定范围内（以最近一次服务端判定为准）。
func localStatus(now int64, validUntil string, graceDays int) string {

	if validUntil == "" {
		return StatusValid
	}
	until, err := parseRFC3339Milli(validUntil)
	if err != nil {
		// 时间解析失败按到期处理（安全兜底）
		return StatusExpired
	}
	if now > until+int64(graceDays)*dayMillis {
		return StatusExpired
	}
	if now > until {
		return StatusGrace
	}
	if until-now <= expiringMillis {
		return StatusExpiring
	}
	return StatusValid
}
