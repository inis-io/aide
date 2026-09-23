package protocol

import (
	"testing"
)

// TestVersionInRange - 版本范围表达式（与平台语义一致的判定表）
func TestVersionInRange(t *testing.T) {

	cases := []struct {
		version string
		expr    string
		expect  bool
	}{
		{"2.3.1", "", true},
		{"2.3.1", ">=2.0.0 <3.0.0", true},
		{"3.0.0", ">=2.0.0 <3.0.0", false},
		{"1.9.9", ">=2.0.0 <3.0.0", false},
		{"2.0.0", ">=2.0.0", true},
		{"2.0.0", ">2.0.0", false},
		{"2.0.0", "<=2.0.0", true},
		{"2.0.0", "==2.0.0", true},
		{"2.0.0", "!=2.0.0", false},
		{"2.0.0", ">=2.0.0, <3.0.0", true},
		{"v2.3.1", ">=2.0.0", true},
		{"2.3", ">=2.0.0 <3.0.0", true},
		{"abc", ">=2.0.0", false},
		{"2.0.0", "=>2.0.0", false},
	}
	for _, item := range cases {
		if got := VersionInRange(item.version, item.expr); got != item.expect {
			t.Fatalf("VersionInRange(%q, %q) = %v，期望 %v", item.version, item.expr, got, item.expect)
		}
	}
}
