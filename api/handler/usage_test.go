package handler

import "testing"

// TestParsePositiveInt 验证 days 参数解析：纯数字 → 数值；非法 → 0。
func TestParsePositiveInt(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"7", 7},
		{"30", 30},
		{"1", 1},
		{"", 0},
		{"abc", 0},
		{"7d", 0},
		{"-3", 0},
		{"012", 12},
	}
	for _, c := range cases {
		if got := parsePositiveInt(c.in); got != c.want {
			t.Errorf("parsePositiveInt(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
