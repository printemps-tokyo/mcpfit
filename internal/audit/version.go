package audit

import (
	"strconv"
	"strings"
)

// mcpVersion is a parsed MCP protocol revision. MCP versions are dates of the
// form YYYY-MM-DD (e.g. 2025-06-18, 2026-07-28), so lexical date order is the
// meaningful order.
type mcpVersion struct {
	year, month, day int
	ok               bool
}

// parseVersion parses a YYYY-MM-DD MCP revision. ok is false for anything that
// does not fit that shape (the caller then treats the version as unknown).
func parseVersion(s string) mcpVersion {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 3 {
		return mcpVersion{}
	}
	y, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	d, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return mcpVersion{}
	}
	if len(parts[0]) != 4 || m < 1 || m > 12 || d < 1 || d > 31 {
		return mcpVersion{}
	}
	return mcpVersion{year: y, month: m, day: d, ok: true}
}

// compareVersions returns -1 if a is older than b, 0 if equal, +1 if newer.
// Unparseable versions never compare equal to a parseable one; two unparseable
// versions compare equal only if their strings match.
func compareVersions(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	if !pa.ok || !pb.ok {
		if a == b {
			return 0
		}
		// Keep the result deterministic even for junk input.
		if a < b {
			return -1
		}
		return 1
	}
	switch {
	case pa.year != pb.year:
		return sign(pa.year - pb.year)
	case pa.month != pb.month:
		return sign(pa.month - pb.month)
	case pa.day != pb.day:
		return sign(pa.day - pb.day)
	default:
		return 0
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
