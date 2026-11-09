package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

// RelayOptions configure a Relay.
type RelayOptions struct {
	// Version is the server version reported to the host; Port the daemon's port (for the dashboard link).
	Version string
	Port    int
	// Tiers returns the current agy tier models (the daemon's settings).
	Tiers  func() config.TierModels
	Logger *slog.Logger
}

// Relay serves the MCP sessions the companion's relay (`seelegent-launch mcp`) opens on the daemon
// (POST /api/v1/mcp/relay): the same server as `seelegent mcp`, running inside the daemon. Its tools reach the
// daemon's API through the HTTP client of the session, which httpapi builds to call the daemon in process with the
// relay connection's caller, so the worker guard sees the relay's process, never the daemon's. The tool list is
// built once per tier models and shared by every session.
type Relay struct {
	o RelayOptions

	mu    sync.Mutex
	tiers config.TierModels
	set   *toolSet
}

// NewRelay returns the relayed-session server of the daemon.
func NewRelay(o RelayOptions) *Relay {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Tiers == nil {
		o.Tiers = func() config.TierModels { return config.Defaults().Backends.Agy.Tiers }
	}
	return &Relay{o: o}
}

// tools returns the shared tool set for the current tier models, building it when they changed.
func (r *Relay) tools() (*toolSet, error) {
	tiers := r.o.Tiers()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.set != nil && r.tiers == tiers {
		return r.set, nil
	}
	set, err := newToolSet(tiers, Unavailable())
	if err != nil {
		return nil, err
	}
	r.tiers, r.set = tiers, set
	return set, nil
}

// ServeMCP runs one relayed session on conn until either side closes it or ctx ends. env holds the host's session
// variables from the handshake (only mcpwire.SessionEnv is read; nothing comes from the daemon's environment), cwd
// is the relay's folder (relative paths resolve against it), and hc calls the daemon's API on behalf of the
// relay's process.
func (r *Relay) ServeMCP(ctx context.Context, conn io.ReadWriteCloser, env map[string]string, cwd string,
	hc *http.Client) error {
	set, err := r.tools()
	if err != nil {
		return err
	}
	self := selfDaemon{c: client.ForPort(r.o.Port, hc)}
	srv, err := newServer(Options{Daemon: self, Version: r.o.Version, Port: r.o.Port, Logger: r.o.Logger,
		Getenv: func(k string) string {
			if !mcpwire.Forwarded(k) {
				return ""
			}
			return env[k]
		},
		Getwd: func() (string, error) {
			if cwd == "" || !filepath.IsAbs(cwd) {
				return "", errors.New("the MCP relay did not send its working directory")
			}
			return cwd, nil
		}}, set)
	if err != nil {
		return err
	}
	return srv.Run(ctx, &mcp.IOTransport{Reader: conn, Writer: conn})
}

// selfDaemon is the daemon a relayed session runs in: always found, never started.
type selfDaemon struct{ c *client.Client }

func (d selfDaemon) Ensure(context.Context) (*client.Client, error) { return d.c, nil }
func (d selfDaemon) Find(context.Context) (*client.Client, error)   { return d.c, nil }
