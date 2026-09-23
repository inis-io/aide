package protocol

import (
	"testing"
	"time"
)

// TestLocalStatus - 离线本地时间维度判定（宽限/临期/永久）
func TestLocalStatus(t *testing.T) {

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	format := func(item time.Time) string { return item.UTC().Format(time.RFC3339) }
	base := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

	if got := LocalStatus(now, "", 7); got != StatusValid {
		t.Fatalf("永久授权应 VALID，实际 %s", got)
	}
	if got := LocalStatus(now, format(base.Add(400*24*time.Hour)), 7); got != StatusValid {
		t.Fatalf("远未到期应 VALID，实际 %s", got)
	}
	if got := LocalStatus(now, format(base.Add(10*24*time.Hour)), 7); got != StatusExpiring {
		t.Fatalf("30 天内到期应 EXPIRING，实际 %s", got)
	}
	if got := LocalStatus(now, format(base.Add(-time.Hour)), 7); got != StatusGrace {
		t.Fatalf("宽限内应 GRACE，实际 %s", got)
	}
	if got := LocalStatus(now, format(base.Add(-10*24*time.Hour)), 7); got != StatusExpired {
		t.Fatalf("宽限耗尽可能 EXPIRED，实际 %s", got)
	}
	if got := LocalStatus(now, "not-a-time", 7); got != StatusExpired {
		t.Fatalf("时间解析失败应安全兜底 EXPIRED，实际 %s", got)
	}
}
