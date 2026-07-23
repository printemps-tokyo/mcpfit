package mcp

import (
	"testing"
	"time"

	"github.com/printemps-tokyo/mcpfit/internal/audit"
)

// TestStdioProbe_AgainstMockServer is the os/exec-based smoke test: it spawns
// the in-repo mock MCP server via `go run` and drives the real stdio transport
// through the full probe + audit pipeline, asserting the modern persona grades
// READY and the legacy persona grades NOT READY with the right findings.
func TestStdioProbe_AgainstMockServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio smoke test in -short mode (compiles a subprocess)")
	}

	t.Run("modern", func(t *testing.T) {
		t.Setenv("MOCK_MODE", "modern")
		obs := probeStdio(t)
		if obs.ProtocolVersion != "2026-07-28" {
			t.Errorf("protocolVersion = %q, want 2026-07-28", obs.ProtocolVersion)
		}
		if !obs.CacheHints {
			t.Errorf("expected cache hints from modern server")
		}
		if obs.TasksCapability {
			t.Errorf("modern server should not declare tasks capability")
		}
		r := audit.Run(obs)
		if r.Counts.Fail != 0 {
			t.Fatalf("modern server has FAILs: %+v", r.Findings)
		}
		if r.Grade != audit.GradeReady {
			t.Errorf("modern grade = %q, want %q", r.Grade, audit.GradeReady)
		}
	})

	t.Run("legacy", func(t *testing.T) {
		t.Setenv("MOCK_MODE", "legacy")
		obs := probeStdio(t)
		if len(obs.TaskMethodsBlocking) == 0 {
			t.Errorf("legacy server: expected blocking task methods, got none")
		}
		r := audit.Run(obs)
		if r.Counts.Fail == 0 {
			t.Fatalf("legacy server produced no FAILs: %+v", r.Findings)
		}
		if r.Grade != audit.GradeNotReady {
			t.Errorf("legacy grade = %q, want %q", r.Grade, audit.GradeNotReady)
		}
	})
}

func probeStdio(t *testing.T) audit.Observation {
	t.Helper()
	c, err := New(Options{
		Command: "go",
		Args:    []string{"run", "./mockserver"},
		Timeout: 60 * time.Second, // generous: first run compiles the mock
	})
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
