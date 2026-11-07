package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// logTailBytes bounds how much of a log GET /logs reads from its end.
const logTailBytes = 4 << 20

// backendNames are the display names of the worker CLIs.
var backendNames = map[string]string{
	config.BackendAgy:    "Antigravity",
	config.BackendClaude: "Claude Code",
	config.BackendCodex:  "Codex",
}

func (s *Server) systemRoutes() {
	s.handle("GET /health", func(http.ResponseWriter, *http.Request) (int, any, error) {
		return http.StatusOK, s.health(), nil
	})
	s.handle("GET /state", s.getState)
	s.handle("GET /backends", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		accounts, err := s.o.Accounts.List(r.Context())
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, s.backends(accounts), nil
	})
	s.handle("GET /models", s.getModels)
	s.handle("GET /logs", s.getLogs)
	s.handle("GET /projects", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		p, err := s.o.Tasks.Projects(r.Context())
		return http.StatusOK, api.NonNil(p), err
	})
	s.handle("GET /spec", s.getSpec)
	s.handle("POST /daemon/stop", func(http.ResponseWriter, *http.Request) (int, any, error) {
		if s.o.Stop == nil {
			return 0, nil, newError(api.CodeUnavailable, "this daemon cannot be stopped through the API; stop it "+
				"where it runs (Ctrl+C)")
		}
		s.log.Info("http: stop requested")
		// Stop only signals: the shutdown waits for this answer to be written (http.Server.Shutdown).
		s.o.Stop()
		return http.StatusAccepted, api.DaemonStop{Stopping: true, PID: s.o.PID}, nil
	})
}

func (s *Server) health() api.Health {
	return api.Health{OK: true, Version: s.o.Version, Commit: s.o.Commit, Build: s.build(), APIVersion: api.APIVersion,
		PID: s.o.PID, Port: s.o.Port, StartedAt: api.FormatTime(s.o.StartedAt)}
}

// getState is GET /state. lastEventId is read before the snapshot: every frame is published after its change is
// committed, so a frame newer than lastEventId may repeat a change the snapshot already holds (the client applies
// it again, which is harmless), but no change can be missing from both.
func (s *Server) getState(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	ctx := r.Context()
	st := api.BootState{Health: s.health(), LastEventID: s.hub.LastID(), Settings: s.o.Settings.Settings()}
	if st.Accounts, err = s.accounts(ctx); err != nil {
		return 0, nil, err
	}
	list, err := s.o.Accounts.List(ctx)
	if err != nil {
		return 0, nil, err
	}
	st.Backends = s.backends(list)
	if st.Tasks, err = s.o.Tasks.Boot(ctx); err != nil {
		return 0, nil, err
	}
	if st.Projects, err = s.o.Tasks.Projects(ctx); err != nil {
		return 0, nil, err
	}
	st.Tasks, st.Projects = api.NonNil(st.Tasks), api.NonNil(st.Projects)
	return http.StatusOK, st, nil
}

// accounts returns every account as the API shows it: priority first, then label.
func (s *Server) accounts(ctx context.Context) ([]api.Account, error) {
	states, err := s.o.Pool.States(ctx)
	if err != nil {
		return nil, err
	}
	providers, err := s.o.Accounts.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	v := &accountView{settings: s.o.Settings.Settings(), providers: map[string]account.Provider{},
		backends: s.o.Backends, signingIn: s.o.SignIn.Active, now: s.clock()}
	for i := range providers {
		p := &providers[i]
		v.providers[p.ID] = *p
	}
	out := make([]api.Account, 0, len(states))
	for i := range states {
		out = append(out, v.account(&states[i]))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	return out, nil
}

// account returns one account as the API shows it.
func (s *Server) account(ctx context.Context, id string) (api.Account, error) {
	list, err := s.accounts(ctx)
	if err != nil {
		return api.Account{}, err
	}
	for i := range list {
		if list[i].ID == id {
			return list[i], nil
		}
	}
	return api.Account{}, newError(api.CodeNotFound, "account %s not found", id)
}

// backends describes the worker CLIs.
func (s *Server) backends(accounts []account.Account) []api.Backend {
	set := s.o.Settings.Settings()
	out := make([]api.Backend, 0, 3)
	for _, name := range backend.Names() {
		cfg, _ := set.Backends.Backend(name)
		b := api.Backend{ID: name, Name: backendNames[name], Enabled: cfg.Enabled}
		if be, ok := s.o.Backends[name]; ok && be != nil {
			b.Path = be.Bin()
			b.Installed = b.Path != ""
			caps := be.Capabilities()
			b.Capabilities = api.BackendCapabilities{Resume: caps.ResumeFlag != "", APIKey: name != config.BackendAgy,
				Takeover: caps.ResumeFlag != "", ListModels: name == config.BackendCodex}
		}
		for i := range accounts {
			a := &accounts[i]
			if a.Backend != name {
				continue
			}
			b.Accounts++
			if a.Enabled {
				b.Active++
			}
		}
		out = append(out, b)
	}
	return out
}

// getModels is GET /models: the allowed models of each backend (its tier models, and the catalog models of its
// accounts that the backend's pattern allows).
func (s *Server) getModels(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	only := r.URL.Query().Get("backend")
	if err := oneOf("backend", only, true, backend.Names()...); err != nil {
		return 0, nil, err
	}
	set := s.o.Settings.Settings()
	states, err := s.o.Pool.States(r.Context())
	if err != nil {
		return 0, nil, err
	}
	out := []api.Model{}
	for _, name := range backend.Names() {
		if only != "" && name != only {
			continue
		}
		pol, err := backend.PolicyFor(name, set, nil)
		if err != nil {
			return 0, nil, err
		}
		seen := map[string]int{}
		add := func(id string) int {
			if i, ok := seen[strings.ToLower(id)]; ok {
				return i
			}
			out = append(out, api.Model{ID: id, Backend: name, Label: id, Tiers: []api.Tier{}})
			seen[strings.ToLower(id)] = len(out) - 1
			return len(out) - 1
		}
		for _, tier := range config.Tiers() {
			if m, ok := pol.Tiers.Model(tier); ok && m != "" {
				i := add(m)
				out[i].Tiers = append(out[i].Tiers, api.Tier(tier))
			}
		}
		for i := range states {
			st := &states[i]
			if st.Account.Backend != name || st.Account.Kind == account.KindAPIKey || !st.HasQuota {
				continue
			}
			for _, m := range st.Quota.Models {
				if pol.Allowed(m) == nil {
					add(m)
				}
			}
		}
	}
	return http.StatusOK, out, nil
}

// getLogs is GET /logs: the last lines of daemon.log or hooks.log, redacted.
func (s *Server) getLogs(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = api.LogDaemon
	}
	if err := oneOf("name", name, false, api.LogDaemon, api.LogHooks); err != nil {
		return 0, nil, err
	}
	n, err := queryInt(r, "tail", 500, 2000)
	if err != nil {
		return 0, nil, err
	}
	out := api.LogLines{Name: name, Lines: []string{}}
	if s.o.LogDir == "" {
		return http.StatusOK, out, nil
	}
	lines, err := tailLines(filepath.Join(s.o.LogDir, name+".log"), n)
	if errors.Is(err, fsutil.ErrNotRegular) {
		return 0, nil, newError(api.CodeUnavailable, "%s.log is not a regular file", name)
	}
	if err != nil {
		return 0, nil, err
	}
	for _, l := range lines {
		out.Lines = append(out.Lines, s.o.Redact(l))
	}
	return http.StatusOK, out, nil
}

// tailLines returns the last n lines of a file (none when it does not exist), reading at most logTailBytes.
func tailLines(path string, n int) ([]string, error) {
	// OpenRegular: a FIFO or link put in place of a log cannot hang or redirect the read.
	f, err := fsutil.OpenRegular(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(st.Size()-logTailBytes, 0)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, logTailBytes))
	if err != nil {
		return nil, err
	}
	if start > 0 { // the first line is cut
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), logTailBytes)
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, sc.Err()
}

// Spec Kit tasks.md lines (same rules as the old parseTasksMd).
var (
	specPhase = regexp.MustCompile(`^##\s+(Phase\b.*)$`)
	specTask  = regexp.MustCompile(`^\s*-\s*\[( |x|X)\]\s*(T\d+)\s*(\[P\])?\s*(?:\[US\d+\])?\s*(.*)$`)
)

// maxSpecBytes is the largest tasks.md GET /spec reads.
const maxSpecBytes = 4 << 20

// getSpec is GET /spec: the parsed tasks.md of a feature folder; a relative dir must stay inside cwd.
func (s *Server) getSpec(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	q := r.URL.Query()
	cwd, err := absDir(q.Get("cwd"))
	if err != nil {
		return 0, nil, err
	}
	raw := q.Get("dir")
	if raw == "" {
		return 0, nil, invalid("dir is required").withField("dir")
	}
	dir := raw
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	st, err := os.Stat(dir) //nolint:gosec // the spec folder the caller names
	if err != nil || !st.IsDir() {
		return 0, nil, invalid("dir must be an existing folder (got %q)", raw).withField("dir")
	}
	if dir, err = fsutil.Canonical(dir); err != nil {
		return 0, nil, invalid("dir: %v", err).withField("dir")
	}
	inside, err := fsutil.Within(cwd, dir)
	if err != nil {
		return 0, nil, err
	}
	if !filepath.IsAbs(raw) && !inside {
		return 0, nil, invalid("dir %q leaves cwd", raw).withField("dir")
	}
	// A worker can reach /spec and can leave a FIFO or a huge file as tasks.md: read it without blocking and with a
	// limit.
	data, truncated, err := fsutil.ReadRegular(filepath.Join(dir, "tasks.md"), maxSpecBytes)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return 0, nil, newError(api.CodeNotFound, "%s has no tasks.md", filepath.ToSlash(dir))
	case errors.Is(err, fsutil.ErrNotRegular):
		return 0, nil, invalid("tasks.md in %q is not a regular file", raw).withField("dir")
	case err != nil:
		return 0, nil, err
	case truncated:
		return 0, nil, invalid("tasks.md in %q is larger than %d MiB", raw, maxSpecBytes>>20).withField("dir")
	}
	view := api.SpecView{Dir: filepath.ToSlash(dir), Feature: filepath.Base(dir), Tasks: parseTasks(string(data))}
	if inside {
		if rel, err := filepath.Rel(cwd, dir); err == nil {
			view.Dir = filepath.ToSlash(rel)
		}
	}
	return http.StatusOK, view, nil
}

// parseTasks reads the tasks of a Spec Kit tasks.md.
func parseTasks(text string) []api.SpecTask {
	out := []api.SpecTask{}
	phase := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := specPhase.FindStringSubmatch(line); m != nil {
			phase = strings.TrimSpace(m[1])
			continue
		}
		if m := specTask.FindStringSubmatch(line); m != nil {
			out = append(out, api.SpecTask{ID: m[2], Title: strings.TrimSpace(m[4]), Done: m[1] != " ",
				Phase: phase, Parallel: m[3] != ""})
		}
	}
	return slices.Clip(out)
}
