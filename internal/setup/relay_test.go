package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsOursRelay(t *testing.T) {
	for _, tt := range []struct {
		e    Entry
		want bool
	}{
		{Entry{Command: `C:\Programs\seelegent\seelegent-launch.exe`, Args: []string{"mcp"}}, true},
		{Entry{Command: "/usr/local/bin/seelegent-launch", Args: []string{"mcp"}}, true},
		{Entry{Command: "/usr/local/bin/seelegent-launch", Args: []string{"__launch", "x"}}, false},
	} {
		if got := isOurs(tt.e); got != tt.want {
			t.Errorf("isOurs(%+v) = %v", tt.e, got)
		}
	}
}

// TestRelayCommand: the hosts get the MCP relay when the companion next to the executable is of its build, and
// `<exe> mcp` otherwise; an entry of one kind is updated to the other, and both are Seelegent's on remove.
func TestRelayCommand(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	companion := filepath.Join(filepath.Dir(exe), "seelegent-launch.exe")
	cfg := m.write(".cursor/mcp.json", cursorConfig)
	entry := func() Entry {
		t.Helper()
		var doc struct {
			MCPServers map[string]Entry `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(read(t, cfg)), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.MCPServers["seelegent"]
	}
	usable := true
	env := m.env()
	env.Relay = func(got string) (string, error) {
		if got != exe {
			t.Errorf("relay asked for %s", got)
		}
		if !usable {
			return "", errors.New("it is not of this build")
		}
		return companion, nil
	}
	runEnv := func(req Request) Report {
		t.Helper()
		req.Port, req.Version = 47821, "v9.9.9"
		rep, err := Run(context.Background(), env, req)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep := runEnv(Request{Exe: exe, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionAdded)
	if e := entry(); e.Command != companion || len(e.Args) != 1 || e.Args[0] != "mcp" || !rep.Relay ||
		rep.Command != companion {
		t.Errorf("entry %+v, report %s relay %v", e, rep.Command, rep.Relay)
	}
	var text bytes.Buffer
	_ = rep.WriteText(&text)
	if !strings.Contains(text.String(), "MCP relay") {
		t.Errorf("text:\n%s", text.String())
	}
	usable = false
	rep = runEnv(Request{Exe: exe, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionUpdated)
	if e := entry(); e.Command != exe || rep.Relay || !strings.Contains(rep.RelayNote, "not of this build") {
		t.Errorf("entry %+v, report %+v", e, rep)
	}
	usable = true
	wantAction(t, runEnv(Request{Exe: exe, Hosts: []string{"cursor"}}), "cursor", KindMCP, ActionUpdated)
	wantAction(t, runEnv(Request{Remove: true, Hosts: []string{"cursor"}}), "cursor", KindMCP, ActionRemoved)
	// Without a relay lookup (the zero Env) the plain command is registered.
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	if e := entry(); e.Command != exe || rep.Relay {
		t.Errorf("entry %+v, relay %v", e, rep.Relay)
	}
}
