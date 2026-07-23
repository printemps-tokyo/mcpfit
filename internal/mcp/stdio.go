package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// stdioTransport launches the target server as a subprocess and exchanges
// newline-delimited JSON-RPC messages over its stdin/stdout. MCP messages must
// not contain embedded newlines, so line splitting is safe.
type stdioTransport struct {
	command string
	args    []string
	timeout time.Duration

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader

	mu         sync.Mutex
	stderrTail []string
}

func newStdioTransport(command string, args []string, timeout time.Duration) *stdioTransport {
	return &stdioTransport{command: command, args: args, timeout: timeout}
}

func (t *stdioTransport) start() error {
	t.cmd = exec.Command(t.command, t.args...)
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio: cannot open stdin: %w", err)
	}
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdio: cannot open stdout: %w", err)
	}
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stdio: cannot open stderr: %w", err)
	}
	if err := t.cmd.Start(); err != nil {
		return fmt.Errorf("stdio: failed to launch %q: %w", t.command, err)
	}
	t.stdin = stdin
	t.reader = bufio.NewReaderSize(stdout, 1<<20)
	go t.drainStderr(stderr)
	return nil
}

func (t *stdioTransport) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		t.mu.Lock()
		t.stderrTail = append(t.stderrTail, line)
		if len(t.stderrTail) > 20 {
			t.stderrTail = t.stderrTail[len(t.stderrTail)-20:]
		}
		t.mu.Unlock()
	}
}

func (t *stdioTransport) stderrHint() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.stderrTail) == 0 {
		return ""
	}
	tail := t.stderrTail
	if len(tail) > 3 {
		tail = tail[len(tail)-3:]
	}
	return "\n  stderr: " + strings.Join(tail, " | ")
}

func (t *stdioTransport) sendRequest(req Request) (*Response, error) {
	if err := t.write(req); err != nil {
		return nil, err
	}
	// Read lines until we see the response with the matching id, bounded by the
	// timeout. Server-initiated requests (e.g. ping) are answered politely.
	deadline := time.Now().Add(t.timeout)
	for {
		line, err := t.readLine(deadline)
		if err != nil {
			return nil, err
		}
		if line == "" {
			continue
		}
		// Peek at id/method to route.
		var probe struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(line), &probe) != nil {
			continue // ignore non-JSON noise
		}
		switch {
		case probe.Method != "" && probe.ID != nil:
			// Server-initiated request: answer ping, decline others.
			t.answerServerRequest(*probe.ID, probe.Method)
		case probe.Method != "" && probe.ID == nil:
			// Notification from server: ignore.
		default:
			var resp Response
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				continue
			}
			if resp.ID != nil && *resp.ID == req.ID {
				return &resp, nil
			}
		}
	}
}

func (t *stdioTransport) answerServerRequest(id int, method string) {
	if method == "ping" {
		_ = t.write(Response{JSONRPC: "2.0", ID: &id, Result: json.RawMessage("{}")})
		return
	}
	_ = t.write(Response{JSONRPC: "2.0", ID: &id, Error: &RPCError{Code: -32601, Message: "method not found"}})
}

func (t *stdioTransport) sendNotification(n Notification) error {
	return t.write(n)
}

func (t *stdioTransport) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := t.stdin.Write(b); err != nil {
		return fmt.Errorf("stdio: write failed: %w%s", err, t.stderrHint())
	}
	return nil
}

// readLine reads one line, enforcing an overall deadline by reading on a
// goroutine and racing it against a timer. This guarantees the client never
// blocks forever on a silent server.
func (t *stdioTransport) readLine(deadline time.Time) (string, error) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return "", fmt.Errorf("stdio: timed out waiting for a response%s", t.stderrHint())
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := t.reader.ReadString('\n')
		ch <- result{line: line, err: err}
	}()
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			if errors.Is(r.err, io.EOF) && strings.TrimSpace(r.line) == "" {
				return "", fmt.Errorf("stdio: server closed the connection before responding%s", t.stderrHint())
			}
			if r.line == "" {
				return "", fmt.Errorf("stdio: read failed: %w%s", r.err, t.stderrHint())
			}
		}
		return strings.TrimSpace(r.line), nil
	case <-timer.C:
		return "", fmt.Errorf("stdio: timed out after %s waiting for a response%s", t.timeout, t.stderrHint())
	}
}

func (t *stdioTransport) close() error {
	if t.cmd == nil {
		return nil
	}
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	// Give the server a moment to exit, then terminate.
	done := make(chan struct{})
	go func() {
		_ = t.cmd.Wait()
		close(done)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		<-done
	}
	return nil
}

func (t *stdioTransport) signals() TransportSignals {
	// stdio carries no session/auth headers.
	return TransportSignals{}
}
