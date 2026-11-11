package mcp

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/printemps-tokyo/mcpfit/internal/audit"
)

// TestStdioProbe_AgainstMockServer is the os/exec-based smoke test: it builds
// the in-repo mock MCP server and drives the real stdio transport through the
// full probe + audit pipeline, asserting the modern persona (server/discover,
// no initialize) grades READY and the legacy personas fall back to initialize
// and grade NOT READY with the right findings.
func TestStdioProbe_AgainstMockServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio smoke test in -short mode (compiles a subprocess)")
	}
	bin := filepath.Join(t.TempDir(), "mockserver")
	if out, err := exec.Command("go", "build", "-o", bin, "./mockserver").CombinedOutput(); err != nil {
		t.Fatalf("building the mock server: %v\n%s", err, out)
	}

	t.Run("modern", func(t *testing.T) {
		t.Setenv("MOCK_MODE", "modern")
		obs := probeStdio(t, bin, 10*time.Second)
		if obs.Era != audit.EraModern {
			t.Errorf("era = %q, want %q", obs.Era, audit.EraModern)
		}
		if obs.ProtocolVersion != "2026-07-28" {
			t.Errorf("protocolVersion = %q, want 2026-07-28", obs.ProtocolVersion)
		}
		if obs.ServerName != "modern-mock" {
			t.Errorf("serverName = %q, want modern-mock (from _meta serverInfo)", obs.ServerName)
		}
		if obs.CacheableResults != 4 || len(obs.CacheHintsMissing) != 0 {
			t.Errorf("cache hints: inspected=%d missing=%v, want 4 and none", obs.CacheableResults, obs.CacheHintsMissing)
		}
		if len(obs.Tools) != 1 {
			t.Errorf("tools = %+v, want 1 (requests must carry _meta)", obs.Tools)
		}
		if obs.TasksCapability {
			t.Errorf("modern server should not declare tasks capability")
		}
		r := audit.Run(obs)
		if r.Counts.Fail != 0 || r.Counts.Warn != 0 {
			t.Fatalf("modern server has FAIL/WARN findings: %+v", r.Findings)
		}
		if r.Grade != audit.GradeReady {
			t.Errorf("modern grade = %q, want %q", r.Grade, audit.GradeReady)
		}
	})

	t.Run("legacy", func(t *testing.T) {
		t.Setenv("MOCK_MODE", "legacy")
		assertLegacy(t, probeStdio(t, bin, 10*time.Second))
	})

	// A legacy server that stays silent on server/discover: the probe times
	// out and the client must still complete the initialize fallback.
	t.Run("legacy-silent", func(t *testing.T) {
		t.Setenv("MOCK_MODE", "legacy-silent")
		assertLegacy(t, probeStdio(t, bin, 2*time.Second))
	})
}

func assertLegacy(t *testing.T, obs audit.Observation) {
	t.Helper()
	if obs.Era != audit.EraLegacy {
		t.Errorf("era = %q, want %q", obs.Era, audit.EraLegacy)
	}
	if obs.ProtocolVersion != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want 2025-06-18", obs.ProtocolVersion)
	}
	if len(obs.TaskMethodsRemoved) == 0 {
		t.Errorf("legacy server: expected the removed tasks/list to respond")
	}
	if len(obs.CacheHintsMissing) == 0 {
		t.Errorf("legacy server: expected missing cache hints")
	}
	r := audit.Run(obs)
	if r.Counts.Fail == 0 {
		t.Fatalf("legacy server produced no FAILs: %+v", r.Findings)
	}
	if r.Grade != audit.GradeNotReady {
		t.Errorf("legacy grade = %q, want %q", r.Grade, audit.GradeNotReady)
	}
}

func probeStdio(t *testing.T, bin string, timeout time.Duration) audit.Observation {
	t.Helper()
	c, err := New(Options{Command: bin, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	_, obs, err := c.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	return obs
}
