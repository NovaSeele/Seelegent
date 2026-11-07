package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Caller is who sent a request (contract §1.3).
type Caller struct {
	// Worker is true for a process inside a worker's process tree, and when that cannot be ruled out.
	Worker bool
	// TaskID is the worker's task; "" for a worker whose task is unknown (it may only call GET /health).
	TaskID string
}

// Classifier decides who is on the other end of a connection.
type Classifier interface {
	Classify(local, remote netip.AddrPort, accepted time.Time) Caller
}

// TreeSource lists the process trees of the live workers, each with its task id as ID (task.Service.WorkerTrees).
type TreeSource interface {
	WorkerTrees() []*platform.WorkerTree
}

// TreeClassifier classifies callers by the worker process trees (platform.SocketOwnerInTreesSince). With no live
// worker every caller is the lead; on systems without socket lookup (macOS, the BSDs) too.
type TreeClassifier struct {
	trees TreeSource
	log   *slog.Logger

	mu     sync.Mutex
	logged map[string]bool
}

// NewTreeClassifier returns the classifier of the daemon.
func NewTreeClassifier(trees TreeSource, log *slog.Logger) *TreeClassifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &TreeClassifier{trees: trees, log: log, logged: map[string]bool{}}
}

// Classify implements Classifier. Errors fail closed: a worker with no task (only GET /health), except
// errors.ErrUnsupported, which means the system cannot tell (every caller is the lead, as the contract accepts
// for macOS). A tree whose Windows job cannot be opened is logged once per tree.
func (c *TreeClassifier) Classify(local, remote netip.AddrPort, accepted time.Time) Caller {
	trees := c.trees.WorkerTrees()
	if len(trees) == 0 {
		return Caller{}
	}
	id, isWorker, err := platform.SocketOwnerInTreesSince(local, remote, trees, accepted)
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return Caller{}
		}
		var ju *platform.JobUnavailableError
		if errors.As(err, &ju) {
			c.mu.Lock()
			first := !c.logged[ju.Tree]
			c.logged[ju.Tree] = true
			c.mu.Unlock()
			if first {
				c.log.Warn("worker guard: a worker's job cannot be opened; callers count as workers until it ends",
					"task", ju.Tree, "job", ju.Job, "launcher", ju.RootPID, "err", ju.Err)
			}
		} else {
			c.log.Debug("worker guard: caller unknown, failing closed", "remote", remote.String(), "err", err)
		}
		return Caller{Worker: true}
	}
	if !isWorker {
		return Caller{}
	}
	return Caller{Worker: true, TaskID: id}
}

// connInfo is what the server knows about one connection; the caller is classified once and kept for the
// connection's lifetime (keep-alive requests reuse it).
type connInfo struct {
	accepted      time.Time
	local, remote netip.AddrPort
	once          sync.Once
	caller        Caller
}

type connKey struct{}

// ConnContext is http.Server.ConnContext: it records the accept time and the addresses of each connection.
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, &connInfo{accepted: s.clock(), local: addrPort(c.LocalAddr()),
		remote: addrPort(c.RemoteAddr())})
}

func addrPort(a net.Addr) netip.AddrPort {
	if ta, ok := a.(*net.TCPAddr); ok {
		return ta.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}

// callerOf returns the caller of r, classifying its connection on first use. A request without connection info
// (a test handler called directly) is classified from RemoteAddr alone.
func (s *Server) callerOf(r *http.Request) Caller {
	info, ok := r.Context().Value(connKey{}).(*connInfo)
	if !ok {
		remote, _ := netip.ParseAddrPort(r.RemoteAddr)
		return s.classifier.Classify(netip.AddrPort{}, remote, time.Time{})
	}
	info.once.Do(func() { info.caller = s.classifier.Classify(info.local, info.remote, info.accepted) })
	return info.caller
}

// workerAllowed is the worker allow-list: GET /health, GET /tasks/{own} and /tasks/{own}/events, and GET /spec
// and /memory for the own task's folder or its repository root. Everything else is refused.
func (s *Server) workerAllowed(r *http.Request, c Caller) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := strings.TrimSuffix(r.URL.Path, "/")
	if p == apiPrefix+"/health" {
		return true
	}
	if c.TaskID == "" {
		return false
	}
	switch p {
	case apiPrefix + "/tasks/" + c.TaskID, apiPrefix + "/tasks/" + c.TaskID + "/events":
		return true
	case apiPrefix + "/spec", apiPrefix + "/memory":
		return s.ownFolder(r.Context(), c.TaskID, r.URL.Query().Get("cwd"))
	}
	return false
}

// ownFolder reports whether cwd is the task's folder or its repository root.
func (s *Server) ownFolder(ctx context.Context, taskID, cwd string) bool {
	if cwd == "" {
		return false
	}
	g, err := s.o.Tasks.GitScope(ctx, taskID)
	if err != nil {
		return false
	}
	for _, own := range []string{g.Cwd, g.RepoRoot} {
		if own == "" {
			continue
		}
		if same, err := fsutil.SamePath(own, cwd); err == nil && same {
			return true
		}
	}
	return false
}

// workerDenied answers a worker's request outside its allow-list.
func (s *Server) workerDenied(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, translated(api.CodeForbidden, "worker").with("caller", "worker"))
}
