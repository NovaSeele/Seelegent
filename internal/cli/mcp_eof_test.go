package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMCPExitsOnStdinEOF runs the real `seelegent mcp` process: when the host goes away (its end of stdin closes)
// the server exits on its own with status 0, after a session and a tool call as well as before any message, so no
// MCP process outlives its host (bugs.md C-B22).
func TestMCPExitsOnStdinEOF(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(t.Context(), limit)
		cmd := exec.CommandContext(ctx, exe, "mcp")
		cmd.Env = append(os.Environ(), envExecute+"=1", "SEELEGENT_HOME="+t.TempDir(),
			"SEELEGENT_PORT="+strconv.Itoa(freeTestPort(t)), envNoAutoStart+"=1", "SEELEGENT_WORKER=")
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := bufio.NewScanner(stdout)
		lines.Buffer(make([]byte, 0, 1<<20), 1<<24)
		send := func(msg map[string]any) {
			t.Helper()
			data, _ := json.Marshal(msg)
			if _, err := stdin.Write(append(data, '\n')); err != nil {
				t.Fatal(err)
			}
		}
		answer := func(id int) string {
			t.Helper()
			for lines.Scan() {
				var m struct {
					ID *int `json:"id"`
				}
				if json.Unmarshal(lines.Bytes(), &m) == nil && m.ID != nil && *m.ID == id {
					return lines.Text()
				}
			}
			t.Fatalf("no answer to request %d: %v", id, lines.Err())
			return ""
		}
		if session {
			send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
				"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
				"clientInfo": map[string]any{"name": "test", "version": "0"}}})
			if a := answer(1); !strings.Contains(a, `"serverInfo"`) {
				t.Fatalf("initialize: %s", a)
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
			send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call",
				"params": map[string]any{"name": "status", "arguments": map[string]any{}}})
			if a := answer(2); !strings.Contains(a, envNoAutoStart) {
				t.Fatalf("status: %s", a)
			}
		}
		start := time.Now()
		if err := stdin.Close(); err != nil {
			t.Fatal(err)
		}
		go func() {
			for lines.Scan() { // drain whatever is left, so the process never blocks on stdout
			}
		}()
		err = cmd.Wait()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Fatalf("session %v: the mcp process was still running %v after stdin closed", session, limit)
		}
		if err != nil {
			t.Fatalf("session %v: the mcp process exited with %v", session, err)
		}
		t.Logf("session %v: exited %v after stdin closed", session, time.Since(start))
	}
}
