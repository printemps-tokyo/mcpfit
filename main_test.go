package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseArgs_StdioCommandAndArgs(t *testing.T) {
	cfg, err := parseArgs([]string{"npx", "-y", "some-server"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.command != "npx" {
		t.Errorf("command = %q", cfg.command)
	}
	if strings.Join(cfg.args, " ") != "-y some-server" {
		t.Errorf("args = %v", cfg.args)
	}
}

func TestParseArgs_DoubleDashArgs(t *testing.T) {
	cfg, err := parseArgs([]string{"--timeout", "5s", "mycmd", "--", "-y", "--flag"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.command != "mycmd" {
		t.Errorf("command = %q", cfg.command)
	}
	if strings.Join(cfg.args, " ") != "-y --flag" {
		t.Errorf("args = %v", cfg.args)
	}
	if cfg.timeout != 5*time.Second {
		t.Errorf("timeout = %v", cfg.timeout)
	}
}

func TestParseArgs_URLAndHeaders(t *testing.T) {
	cfg, err := parseArgs([]string{"--url", "https://x/mcp", "--header", "Authorization: Bearer t", "--strict", "--json", "--list"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.url != "https://x/mcp" {
		t.Errorf("url = %q", cfg.url)
	}
	if len(cfg.headers) != 1 || cfg.headers[0] != "Authorization: Bearer t" {
		t.Errorf("headers = %v", cfg.headers)
	}
	if !cfg.strict || !cfg.jsonOut || !cfg.list {
		t.Errorf("flags not set: %+v", cfg)
	}
}

func TestParseArgs_UnknownFlagBeforeCommand(t *testing.T) {
	if _, err := parseArgs([]string{"--nope"}); err == nil {
		t.Errorf("expected error for unknown flag")
	}
}

func TestParseArgs_MissingFlagValue(t *testing.T) {
	if _, err := parseArgs([]string{"--timeout"}); err == nil {
		t.Errorf("expected error for missing flag value")
	}
}

func TestParseArgs_BadTimeout(t *testing.T) {
	for _, v := range []string{"nope", "0s", "-3s"} {
		if _, err := parseArgs([]string{"--timeout", v, "cmd"}); err == nil {
			t.Errorf("expected error for --timeout %q", v)
		}
	}
}

func TestParseHeaders(t *testing.T) {
	m, err := parseHeaders([]string{"Authorization: Bearer abc", "X-Trace:  123 "})
	if err != nil {
		t.Fatal(err)
	}
	if m["Authorization"] != "Bearer abc" {
		t.Errorf("Authorization = %q", m["Authorization"])
	}
	if m["X-Trace"] != "123" {
		t.Errorf("X-Trace = %q (want trimmed)", m["X-Trace"])
	}
	for _, bad := range []string{"nocolon", ": noval", ""} {
		if _, err := parseHeaders([]string{bad}); err == nil {
			t.Errorf("expected error for header %q", bad)
		}
	}
}

func TestRun_HelpAndVersion(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Errorf("--help exit = %d", code)
	}
	if !strings.Contains(out.String(), "mcpfit") {
		t.Errorf("--help output missing banner")
	}
	out.Reset()
	if code := run([]string{"--version"}, &out, &errBuf); code != 0 {
		t.Errorf("--version exit = %d", code)
	}
}

func TestRun_NoTargetExits2(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run(nil, &out, &errBuf); code != 2 {
		t.Errorf("no-target exit = %d, want 2", code)
	}
}

func TestRun_BothTargetsExits2(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := run([]string{"--url", "https://x", "cmd"}, &out, &errBuf); code != 2 {
		t.Errorf("both-targets exit = %d, want 2", code)
	}
}
