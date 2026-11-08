// Package httpapi is the daemon's HTTP API v1 (contracts/api-v1.md): JSON endpoints under /api/v1, the SSE stream
// at /api/v1/events and the embedded dashboard for every other GET.
//
// Every request passes, in this order: the Host check (421 misdirected_request, a DNS-rebinding defense), for
// mutating API requests the X-Seelegent header and same-origin check (403 forbidden), and the worker guard (a
// caller inside a worker's process tree may only use its allow-list, contract §1.3). Bodies are at most 1 MiB and
// unknown fields are rejected; errors share one shape (errors.go).
//
// The package declares the interfaces it uses (server.go); internal/app passes the concrete services.
package httpapi

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// apiPrefix is the base path of the API.
const apiPrefix = "/api/v1"

// Tasks is the task engine (task.Service).
type Tasks interface {
	TreeSource
	Delegate(ctx context.Context, req api.DelegateRequest) (api.TaskSummary, error)
	Followup(ctx context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error)
	Answer(ctx context.Context, id string, req api.AnswerRequest) (api.TaskSummary, error)
	Review(ctx context.Context, id string, req api.ReviewRequest) (api.TaskSummary, error)
	Cancel(ctx context.Context, id string, req api.CancelRequest) (api.TaskSummary, bool, error)
	DismissAttention(ctx context.Context, id string) (api.TaskSummary, error)
	Takeover(ctx context.Context, id string) (api.TaskSummary, error)
	Summary(ctx context.Context, id string) (api.TaskSummary, error)
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	List(ctx context.Context, q task.Query) (api.TaskPage, error)
	Events(ctx context.Context, id string, after int64, limit, tail int) (api.EventPage, error)
	Prompt(ctx context.Context, id string, turn, n int) (string, error)
	Report(ctx context.Context, id, cwd string) (api.TaskReport, error)
	Wait(ctx context.Context, req api.WaitRequest, reportCwd string, since time.Time) (api.WaitResult, error)
	Boot(ctx context.Context) ([]api.TaskSummary, error)
	Projects(ctx context.Context) ([]api.Project, error)
	GitScope(ctx context.Context, id string) (task.GitScope, error)
	OutsideDiff(ctx context.Context, id string, n int) ([]api.FileDiff, error)
	Restore(ctx context.Context, id string, n int, req api.RestoreRequest) (api.RestoreResult, error)
	Bus() *task.Bus
}

// Accounts is the account store (account.Manager).
type Accounts interface {
	Add(ctx context.Context, n account.New, links []string) (account.Account, []string, error)
	AddDirect(ctx context.Context, in account.DirectNew, links []string) (account.DirectResult, error)
	StoredKey(ctx context.Context, id string) (account.Account, string, error)
	RecordTest(ctx context.Context, id, result string, started time.Time) (bool, error)
	Get(ctx context.Context, id string) (account.Account, error)
	List(ctx context.Context) ([]account.Account, error)
	Update(ctx context.Context, id string, p account.Patch) (account.Account, error)
	Remove(ctx context.Context, id string) error
	SetKey(ctx context.Context, id, key string) (account.Account, error)
	ClearKey(ctx context.Context, id string) (account.Account, error)
	CreateProvider(ctx context.Context, p account.Provider) (account.Provider, error)
	UpdateProvider(ctx context.Context, p account.Provider) (account.Provider, error)
	GetProvider(ctx context.Context, id string) (account.Provider, error)
	ListProviders(ctx context.Context) ([]account.Provider, error)
	DeleteProvider(ctx context.Context, id string) error
}

// Pool is the account pool (pool.Pool).
type Pool interface {
	States(ctx context.Context) ([]pool.AccountState, error)
	Notify()
	Changed() <-chan struct{}
}

// Quota reads quota (quota.Refresher).
type Quota interface {
	Refresh(ctx context.Context, id string) (quota.Snapshot, error)
	RefreshAll(ctx context.Context) []quota.Result
	Request(id string)
}

// Usage is the usage history (usage.Store).
type Usage interface {
	Summary(ctx context.Context, days int, look usage.Lookups) (usage.Summary, error)
	History(ctx context.Context, q usage.HistoryQuery) (usage.History, error)
}

// Memory is the project memory (memory.Store).
type Memory interface {
	Get(ctx context.Context, cwd string) (memory.Note, error)
	Append(ctx context.Context, cwd, text string) (memory.Note, error)
	Replace(ctx context.Context, cwd, text string) (memory.Note, error)
}

// Roles is the role store (role.Store).
type Roles interface {
	Upsert(ctx context.Context, r role.Role, scope, cwd string) (role.Role, error)
	Delete(ctx context.Context, id, scope, project string) (bool, error)
	List(ctx context.Context, cwd string, all bool) ([]role.Role, error)
	Projects(ctx context.Context) ([]role.Project, error)
	Skills(cwd string) []role.Skill
}

// Settings holds the daemon's settings (internal/app).
type Settings interface {
	Settings() config.Settings
	// PatchSettings applies a JSON merge patch to config.json and to the running daemon (config.MergePatch).
	PatchSettings(ctx context.Context, patch []byte) (config.Settings, error)
}

// SignIn runs account sign-ins (signin.Service).
type SignIn interface {
	Start(ctx context.Context, id string) (api.LoginSession, error)
	Get(id string) (api.LoginSession, bool)
	SubmitCode(id, code string) (api.LoginSession, error)
	Cancel(id string)
	Logout(ctx context.Context, id string) (account.Account, error)
	Active(id string) bool
}

// Git runs git (gitx.Repos).
type Git interface {
	Root(ctx context.Context, dir string) (string, error)
	Status(ctx context.Context, cwd string) (api.RepoStatus, error)
	Branch(ctx context.Context, cwd string) (*api.BranchInfo, error)
	Diff(ctx context.Context, root, rel, base string, untracked bool) (api.FileDiff, error)
	TaskGitView(ctx context.Context, tf gitx.TaskFiles) (api.TaskGit, error)
	HeadFile(ctx context.Context, root, rel string, limit int) (data []byte, size int64, ok bool, err error)
	TaskCommit(ctx context.Context, tf gitx.TaskFiles, sha string) (api.GitCommitDetail, error)
	Commit(ctx context.Context, tf gitx.TaskFiles, message string, files []string) (api.CommitResult, error)
}

// Options configure a Server. Tasks, Accounts, Pool, Quota, Usage, Memory, Roles, Settings, SignIn and Git are
// required.
type Options struct {
	// Port is the port the server listens on; the Host check and the same-origin check use it.
	Port int
	// Version and Commit identify the daemon build; StartedAt is when it started.
	Version, Commit string
	StartedAt       time.Time
	PID             int
	// HomeID identifies the data directory (config.HomeID) in GET /health.
	HomeID string
	// FreeMemory reads the machine's free memory for GET /health (platform.Memory, or a cached sampler); nil
	// answers an unknown reading.
	FreeMemory func() platform.MemoryStatus
	// LowMemory reports whether the memory watchdog holds new starts (Health.LowMemory); nil answers false.
	LowMemory func() bool
	// Machine is the machine profile the auto resource settings are computed from (Health.Resources and
	// Health.Machine); nil answers an unknown machine.
	Machine func() config.Machine

	Tasks    Tasks
	Accounts Accounts
	Pool     Pool
	Quota    Quota
	Usage    Usage
	Memory   Memory
	Roles    Roles
	Settings Settings
	SignIn   SignIn
	Git      Git
	// Proof and Gates serve the proof ledger, lanes and contention (milestone 13); their routes answer 404 when nil.
	Proof Proof
	Gates Gates
	// Members, Consults, Notes, Meetings, Contests and Hooks are the team services (milestone 14); the paths of one
	// that is nil answer 501.
	Members  Members
	Consults Consults
	Notes    Notes
	Meetings Meetings
	Contests Contests
	Hooks    Hooks
	// Tickets and Automations are the GitHub flow and the automations (milestone 15); the paths of one that is
	// nil answer 501.
	Tickets     Tickets
	Automations Automations
	// Backends are the worker CLIs by name.
	Backends map[string]backend.Backend
	// Probe configures the key tests of POST /providers/test (the zero value in production).
	Probe backend.ProbeOptions
	// LogDir is the folder of daemon.log and hooks.log (GET /logs).
	LogDir string
	// Redact masks secrets in log lines (logx.Redactor.String); identity when nil.
	Redact func(string) string

	// Activity is told of every API request that is not a worker's (the dashboard, the CLI, the lead's MCP
	// calls), before it is served (quota.Refresher.Touch). It must not block. Nil tells nobody.
	Activity func()

	// Stop asks the daemon to shut down cleanly (POST /daemon/stop). It must only signal and return at once: the
	// shutdown waits for the answer to be written. Nil answers 503.
	Stop func()

	// Web is the built dashboard (web.FS()); nil serves 404 for the dashboard.
	Web fs.FS
	// Classifier decides who calls (a TreeClassifier on Tasks when nil).
	Classifier Classifier
	// Now is the clock (time.Now when nil).
	Now    func() time.Time
	Logger *slog.Logger
	// Heartbeat is the SSE ping interval (25 s when 0); tests shorten it.
	Heartbeat time.Duration
	// AccountsPoll is how often the account watcher looks for changes besides pool and task notices (30 s).
	AccountsPoll time.Duration
	// DevOrigins are extra browser origins accepted for development only (the Vite dev server, whose proxy keeps
	// the browser's Host and Origin): each must be http://localhost:<port>, http://127.0.0.1:<port> or
	// http://[::1]:<port>, and its host:port then also passes the Host check. Empty in normal runs.
	DevOrigins []string
}

// Server is the HTTP API. Build it with New, serve Handler (with ConnContext) and Close it at shutdown.
type Server struct {
	o          Options
	log        *slog.Logger
	classifier Classifier
	clock      func() time.Time
	hub        *Hub
	web        *webFiles
	mux        *http.ServeMux
	// devHosts and devOrigins hold Options.DevOrigins, lower case.
	devHosts   map[string]bool
	devOrigins map[string]bool
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	mu           sync.Mutex
	suggestions  map[string]api.Suggestion
	accountsKick chan struct{}
	closed       bool

	acc accountCache
}

// New builds the server and starts its background feeds (task bus, account watcher). Close stops them.
func New(o Options) (*Server, error) {
	if o.Tasks == nil || o.Accounts == nil || o.Pool == nil || o.Quota == nil || o.Usage == nil ||
		o.Memory == nil || o.Roles == nil || o.Settings == nil || o.SignIn == nil || o.Git == nil {
		return nil, fmt.Errorf("httpapi: a required service is missing")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = 25 * time.Second
	}
	if o.AccountsPoll <= 0 {
		o.AccountsPoll = 30 * time.Second
	}
	if o.Classifier == nil {
		o.Classifier = NewTreeClassifier(o.Tasks, o.Logger)
	}
	devHosts, devOrigins, err := parseDevOrigins(o.DevOrigins)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{o: o, log: o.Logger, classifier: o.Classifier, clock: o.Now, ctx: ctx, cancel: cancel,
		devHosts: devHosts, devOrigins: devOrigins,
		suggestions: map[string]api.Suggestion{}, accountsKick: make(chan struct{}, 1),
		acc: accountCache{last: map[string]sentAccount{}}}
	hub, err := NewHub(HubOptions{Now: o.Now})
	if err != nil {
		cancel()
		return nil, err
	}
	s.hub = hub
	if o.Web != nil {
		if s.web, err = newWebFiles(o.Web); err != nil {
			cancel()
			return nil, err
		}
	}
	s.mux = http.NewServeMux()
	s.routes()
	s.startFeeds()
	return s, nil
}

// Hub returns the SSE hub (the composition root publishes account.login and other events through it).
func (s *Server) Hub() *Hub { return s.hub }

// Handler returns the HTTP handler with every check in front of the routes.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.serve)
}

// Close stops the feeds and ends every SSE stream.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.hub.Close()
	s.wg.Wait()
}

// HTTPServer returns an http.Server for addr with this handler, ConnContext and sane timeouts.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ConnContext:       s.ConnContext,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(s.ctx) },
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
	}
}

// serve runs the checks of every request, then the routes.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler { //nolint:errorlint // a sentinel panic value
				panic(v)
			}
			s.writeError(w, r, fmt.Errorf("panic: %v", v))
		}
	}()
	// Every answer, refusals included: no page may frame the dashboard (clickjacking), and no link leaks the
	// daemon's URL to another site.
	h := w.Header()
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	if !s.hostAllowed(r.Host) {
		s.writeError(w, r, translated(api.CodeMisdirected, "misdirected"))
		return
	}
	isAPI := r.URL.Path == apiPrefix || strings.HasPrefix(r.URL.Path, apiPrefix+"/")
	if isAPI && mutating(r.Method) {
		if r.Header.Get("X-Seelegent") != "1" {
			s.writeError(w, r, translated(api.CodeForbidden, "no_header"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !s.ownOrigin(origin) {
			s.writeError(w, r, translated(api.CodeForbidden, "origin"))
			return
		}
	}
	c := s.callerOf(r)
	if c.Worker && !s.workerAllowed(r, c) {
		s.workerDenied(w, r)
		return
	}
	if !isAPI {
		s.serveWeb(w, r)
		return
	}
	if s.o.Activity != nil && !c.Worker {
		s.o.Activity()
	}
	s.mux.ServeHTTP(w, r)
}

func mutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// hostAllowed is the Host check: 127.0.0.1, localhost or [::1] with the server's port.
func (s *Server) hostAllowed(host string) bool {
	port := strconv.Itoa(s.o.Port)
	switch strings.ToLower(host) {
	case "127.0.0.1:" + port, "localhost:" + port, "[::1]:" + port:
		return true
	}
	return s.devHosts[strings.ToLower(host)]
}

// parseDevOrigins checks Options.DevOrigins: loopback http origins with a port and nothing else. It returns their
// host:port values and the origins themselves, lower case.
func parseDevOrigins(list []string) (hosts, origins map[string]bool, err error) {
	hosts, origins = map[string]bool{}, map[string]bool{}
	for _, raw := range list {
		o := strings.ToLower(strings.TrimSuffix(raw, "/"))
		u, perr := url.Parse(o)
		if perr != nil || u.Scheme != "http" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" ||
			u.Fragment != "" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") {
			return nil, nil, fmt.Errorf("httpapi: dev origin %q: must be http://localhost:<port>, "+
				"http://127.0.0.1:<port> or http://[::1]:<port>", raw)
		}
		if p, perr := strconv.Atoi(u.Port()); perr != nil || p < 1 || p > 65535 {
			return nil, nil, fmt.Errorf("httpapi: dev origin %q: the port must be 1..65535", raw)
		}
		hosts[u.Host], origins[o] = true, true
	}
	return hosts, origins, nil
}

// ownOrigin reports whether origin is the dashboard's own origin.
func (s *Server) ownOrigin(origin string) bool {
	port := strconv.Itoa(s.o.Port)
	switch strings.ToLower(strings.TrimSuffix(origin, "/")) {
	case "http://127.0.0.1:" + port, "http://localhost:" + port, "http://[::1]:" + port:
		return true
	}
	return s.devOrigins[strings.ToLower(strings.TrimSuffix(origin, "/"))]
}

// handle registers an API route; fn returns the status and body, or an error.
func (s *Server) handle(pattern string, fn func(w http.ResponseWriter, r *http.Request) (int, any, error)) {
	method, path, _ := strings.Cut(pattern, " ")
	s.mux.HandleFunc(method+" "+apiPrefix+path, func(w http.ResponseWriter, r *http.Request) {
		status, body, err := fn(w, r)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if status == http.StatusNoContent {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			return
		}
		if status == 0 {
			return // the handler wrote the answer itself
		}
		writeJSON(w, status, body)
	})
}

// routes registers every endpoint of the contract.
func (s *Server) routes() {
	s.systemRoutes()
	s.taskRoutes()
	s.gitRoutes()
	s.accountRoutes()
	s.settingsRoutes()
	s.proofRoutes()
	s.teamRoutes()
	s.githubRoutes()
	s.automationRoutes()
	s.mux.HandleFunc("GET "+apiPrefix+"/events", s.serveEvents)
	s.mux.HandleFunc(apiPrefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if allow := s.allowed(r); len(allow) > 0 {
			// A route that exists, asked with another method: 405 with the methods it takes (final verification N4).
			w.Header().Set("Allow", strings.Join(allow, ", "))
			s.writeErrorStatus(w, r, http.StatusMethodNotAllowed, translated(api.CodeInvalidArgument, "method").
				with("reason", "method_not_allowed").with("allow", allow))
			return
		}
		s.writeError(w, r, translated(api.CodeNotFound, "not_found"))
	})
}

// allowed returns the methods a registered route other than the catch-all takes on r's path (none: no such route).
func (s *Server) allowed(r *http.Request) []string {
	var out []string
	for _, m := range [...]string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete} {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := s.mux.Handler(probe); pattern != "" && pattern != apiPrefix+"/" {
			out = append(out, m)
		}
	}
	return out
}

// background runs fn until Close.
func (s *Server) background(fn func(ctx context.Context)) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.ctx)
	}()
}
