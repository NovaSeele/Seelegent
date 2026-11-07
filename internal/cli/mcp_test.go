package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startMCP runs the mcp command over an in-memory transport until the test ends and returns the client session.
func startMCP(t *testing.T) *mcp.ClientSession {
	t.Helper()
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, testInfo, defaultDeps(), st, &stderr) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the mcp command did not stop")
		}
	})
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// TestMCPCommand runs the mcp command with a temp data directory, a port nothing listens on and autostart off:
// every tool is listed and a call says that the daemon is not running.
func TestMCPCommand(t *testing.T) {
	t.Setenv("SEELEGENT_HOME", t.TempDir())
	t.Setenv("SEELEGENT_PORT", strconv.Itoa(freeTestPort(t)))
	t.Setenv("SEELEGENT_WORKER", "")
	t.Setenv(envNoAutoStart, "1")
	cs := startMCP(t)
	if v := cs.InitializeResult().ServerInfo.Version; v != testInfo.Version {
		t.Fatalf("server version = %q", v)
	}
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 17 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if msg, isErr := callText(t, cs, "status", map[string]any{}); !isErr || !strings.Contains(msg, envNoAutoStart) {
		t.Fatalf("status = %s", msg)
	}
}

// TestMCPInWorkerStartsNothing: inside a worker (SEELEGENT_WORKER=1) with no daemon answering, the server only looks
// for the daemon: nothing is started and the data directory is not even created.
func TestMCPInWorkerStartsNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("SEELEGENT_HOME", home)
	t.Setenv("SEELEGENT_PORT", strconv.Itoa(freeTestPort(t)))
	t.Setenv("SEELEGENT_WORKER", "1")
	t.Setenv(envNoAutoStart, "")
	cs := startMCP(t)
	msg, isErr := callText(t, cs, "status", map[string]any{})
	if !isErr || !strings.Contains(msg, "the Seelegent daemon running this worker cannot be reached") {
		t.Fatalf("status = %s", msg)
	}
	if msg, _ := callText(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": home}); msg !=
		"Seelegent is not available inside a Seelegent worker." {
		t.Fatalf("delegate = %s", msg)
	}
	if _, err := os.Stat(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the data directory was created (%v): something tried to start a daemon", err)
	}
}
