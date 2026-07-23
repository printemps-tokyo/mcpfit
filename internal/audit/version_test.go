package audit

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2025-06-18", "2026-07-28", -1},
		{"2026-07-28", "2026-07-28", 0},
		{"2026-08-01", "2026-07-28", 1},
		{"2026-07-28", "2026-06-18", 1},
		{"2026-07-28", "2026-07-27", 1},
		{"garbage", "2026-07-28", 1},  // deterministic string fallback ("g" > "2")
		{"2026-07-28", "garbage", -1}, // symmetric fallback
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v := parseVersion("2026-07-28"); !v.ok || v.year != 2026 || v.month != 7 || v.day != 28 {
		t.Errorf("parseVersion good = %+v", v)
	}
	for _, bad := range []string{"", "2026-13-01", "2026-07", "26-07-28", "x-y-z", "2026-07-40"} {
		if v := parseVersion(bad); v.ok {
			t.Errorf("parseVersion(%q) unexpectedly ok", bad)
		}
	}
}
