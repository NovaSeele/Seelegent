package cli

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPCommand runs the mcp command over an in-memory transport with a temp data directory, a port nothing
// listens on and autostart off: every tool is listed and a call says that the daemon is not running.
func TestMCPCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	t.Setenv("SEELEGENT_HOME", t.TempDir())
	t.Setenv("SEELEGENT_PORT", strconv.Itoa(port))
	t.Setenv(envNoAutoStart, "1")

	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, testInfo, st, &stderr) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v := cs.InitializeResult().ServerInfo.Version; v != testInfo.Version {
		t.Fatalf("server version = %q", v)
	}
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != 17 {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	msg := res.Content[0].(*mcp.TextContent).Text
	if !res.IsError || !strings.Contains(msg, envNoAutoStart) {
		t.Fatalf("status = %s", msg)
	}
	_ = cs.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the mcp command did not stop")
	}
}
