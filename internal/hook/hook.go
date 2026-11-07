// Package hook runs the user's event hooks (docs/configuration.md): shell commands the daemon starts on task,
// account and daemon events, with the event as JSON on stdin and SEELEGENT_EVENT, SEELEGENT_TASK_ID and
// SEELEGENT_PORT in the environment. Hooks live in SQLite with a stable id (old bug C-B20: a test ran the hook at a
// list index, so an edited list could run the wrong one). At most four hooks run at once and fifty events wait;
// beyond that the oldest waiting event is dropped and logged. Each run has a timeout that kills its process tree;
// its output start (4 KiB, secrets masked) goes to hooks.log and to the hook.ran stream event.
package hook

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Limits.
const (
	MaxHooks       = 50
	MaxRun         = 4000
	MaxName        = 100
	DefaultTimeout = 20
	MaxTimeout     = 600
	maxConcurrent  = 4
	maxQueue       = 50
	maxOutput      = 4096
	notifyBuffer   = 1024
)

// ErrInvalid and ErrNotFound are matched by the errors of the API calls.
var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Tasks is the task engine (task.Service).
type Tasks interface {
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	Bus() *task.Bus
}

// Options configure a Service. DB is required.
type Options struct {
	DB    DB
	Tasks Tasks
	// Port is the daemon's port, given to hooks as SEELEGENT_PORT.
	Port int
	// Log receives one line per run (hooks.log); Redact masks secrets in what is logged and published.
	Log    io.Writer
	Redact func(string) string
	// Ran is called after each run (hook.ran).
	Ran    func(api.HookRun)
	Now    func() time.Time
	Logger *slog.Logger
}

// Service stores the hooks and runs them.
type Service struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	queue   []job
	running int
	closed  bool
	logMu   sync.Mutex
	// seen is the last known state of each task, to tell transitions apart.
	seen map[string]taskState
}

type job struct {
	hook    api.Hook
	event   string
	taskID  string
	payload []byte
	cwd     string
	test    bool
	done    chan api.HookRun
}

type taskState struct {
	status   api.TaskStatus
	blocked  bool
	reviewed string
	manual   bool
}

// New returns a service; Start begins following the task events.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	if o.Ran == nil {
		o.Ran = func(api.HookRun) {}
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, ctx: ctx, cancel: cancel, seen: map[string]taskState{}}
}

// Start follows the task bus and fires daemon.started (daemon.reattached too when reattached is true).
func (s *Service) Start(reattached bool) {
	if s.o.Tasks != nil {
		// Subscribe before Start returns, so a task change published right after it is not missed.
		bus := s.o.Tasks.Bus()
		sub := bus.Subscribe(notifyBuffer)
		if !s.spawn(func() { s.follow(bus, sub) }) {
			sub.Close()
		}
	}
	s.Fire(api.HookDaemonStarted, "", nil)
	if reattached {
		s.Fire(api.HookDaemonReattached, "", nil)
	}
}

// Close stops following events and waits for running hooks (each ends at its timeout at the latest).
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.queue = nil
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *Service) spawn(fn func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn()
	}()
	return true
}

// ---- storage ----------------------------------------------------------------------------------------------------

// List returns the hooks in their order.
func (s *Service) List(ctx context.Context) ([]api.Hook, error) {
	out := []api.Hook{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `SELECT data FROM hooks ORDER BY position`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var h api.Hook
			if err := json.Unmarshal([]byte(data), &h); err != nil {
				return fmt.Errorf("hook record: %w", err)
			}
			h.On = api.NonNil(h.On)
			out = append(out, h)
		}
		return rows.Err()
	})
	return out, err
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "h" + hex.EncodeToString(b[:])
}

// Validate checks one hook and fills its defaults.
func Validate(h api.Hook) (api.Hook, error) {
	h.Name = strings.TrimSpace(h.Name)
	h.Run = strings.TrimSpace(h.Run)
	if utf8.RuneCountInString(h.Name) > MaxName {
		return h, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, MaxName)
	}
	if h.Run == "" {
		return h, fmt.Errorf("%w: run is required", ErrInvalid)
	}
	if utf8.RuneCountInString(h.Run) > MaxRun {
		return h, fmt.Errorf("%w: run is longer than %d characters", ErrInvalid, MaxRun)
	}
	if strings.ContainsAny(h.Run, "\x00\r\n") {
		return h, fmt.Errorf("%w: run must be one line", ErrInvalid)
	}
	if len(h.On) == 0 {
		return h, fmt.Errorf("%w: on must name at least one event", ErrInvalid)
	}
	var on []string
	for _, e := range h.On {
		if !slices.Contains(api.HookEvents(), e) {
			return h, fmt.Errorf("%w: unknown event %q (use %s)", ErrInvalid, e, strings.Join(api.HookEvents(), ", "))
		}
		if !slices.Contains(on, e) {
			on = append(on, e)
		}
	}
	h.On = on
	if h.Project != "" {
		if !filepath.IsAbs(h.Project) {
			return h, fmt.Errorf("%w: project must be an absolute folder", ErrInvalid)
		}
		if c, err := fsutil.Canonical(h.Project); err == nil {
			h.Project = c
		}
	}
	if h.TimeoutSeconds == 0 {
		h.TimeoutSeconds = DefaultTimeout
	}
	if h.TimeoutSeconds < 1 || h.TimeoutSeconds > MaxTimeout {
		return h, fmt.Errorf("%w: timeoutSeconds must be from 1 to %d", ErrInvalid, MaxTimeout)
	}
	return h, nil
}

// Replace replaces the list: hooks keep the ids they come with (an unknown id is refused), new ones get one.
func (s *Service) Replace(ctx context.Context, hooks []api.Hook) ([]api.Hook, error) {
	if len(hooks) > MaxHooks {
		return nil, fmt.Errorf("%w: at most %d hooks", ErrInvalid, MaxHooks)
	}
	cur, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, h := range cur {
		known[h.ID] = true
	}
	out := make([]api.Hook, 0, len(hooks))
	seen := map[string]bool{}
	for i, h := range hooks {
		v, err := Validate(h)
		if err != nil {
			return nil, fmt.Errorf("hooks[%d]: %w", i, err)
		}
		switch {
		case v.ID == "":
			v.ID = newID()
		case !known[v.ID]:
			return nil, fmt.Errorf("%w: hooks[%d]: unknown id %q (leave it out for a new hook)", ErrInvalid, i, v.ID)
		case seen[v.ID]:
			return nil, fmt.Errorf("%w: hooks[%d]: id %q is listed twice", ErrInvalid, i, v.ID)
		}
		seen[v.ID] = true
		out = append(out, v)
	}
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM hooks`); err != nil {
			return err
		}
		for i := range out {
			data, err := json.Marshal(out[i])
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO hooks (id, position, data) VALUES (?, ?, ?)`, out[i].ID, i,
				string(data)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Import adds hooks kept by the old version (seelegent migrate) after the existing ones; an invalid hook is
// returned as skipped with its reason.
func (s *Service) Import(ctx context.Context, hooks []api.Hook) (added int, skipped []string, err error) {
	cur, err := s.List(ctx)
	if err != nil {
		return 0, nil, err
	}
	for i, h := range hooks {
		h.ID = ""
		v, err := Validate(h)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("hook %d: %v", i, err))
			continue
		}
		cur = append(cur, v)
		added++
	}
	if added == 0 {
		return 0, skipped, nil
	}
	if _, err := s.Replace(ctx, cur); err != nil {
		return 0, skipped, err
	}
	return added, skipped, nil
}

// ---- events -----------------------------------------------------------------------------------------------------

// follow turns task changes into hook events.
func (s *Service) follow(bus *task.Bus, sub *task.Subscription) {
	defer func() { sub.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case n, open := <-sub.C():
			if !open {
				if s.ctx.Err() != nil {
					return
				}
				sub = bus.Subscribe(notifyBuffer)
				continue
			}
			if n.Kind == task.NoticeTask {
				s.taskChanged(n.TaskID, n.Created)
			}
		}
	}
}

// taskChanged fires the events of a task's transition. A task first seen after the daemon started fires nothing
// but task.created (only when it was just created).
func (s *Service) taskChanged(id string, created bool) {
	d, err := s.o.Tasks.Get(s.ctx, id)
	if err != nil || task.Hidden(d.Kind) {
		return
	}
	now := taskState{status: d.Status, blocked: d.Attention != nil && d.Attention.State == "blocked", manual: d.Manual}
	if d.Review != nil {
		now.reviewed = d.Review.Verdict
	}
	s.mu.Lock()
	prev, known := s.seen[id]
	s.seen[id] = now
	s.mu.Unlock()
	var events []string
	if created {
		events = append(events, api.HookTaskCreated)
	}
	if known || created {
		if prev.status != now.status {
			switch now.status {
			case api.StatusRunning:
				events = append(events, api.HookTaskStarted)
			case api.StatusDone:
				events = append(events, api.HookTaskDone)
			case api.StatusFailed:
				events = append(events, api.HookTaskFailed)
			case api.StatusCancelled:
				events = append(events, api.HookTaskCancelled)
			}
		}
		if !prev.blocked && now.blocked {
			events = append(events, api.HookTaskBlocked)
		}
		if prev.blocked && !now.blocked {
			events = append(events, api.HookTaskUnblocked)
		}
		if now.reviewed != "" && prev.reviewed != now.reviewed {
			events = append(events, api.HookTaskReviewed)
		}
		if !prev.manual && now.manual {
			events = append(events, api.HookTaskTakeover)
		}
		if prev.manual && !now.manual {
			events = append(events, api.HookTaskHandback)
		}
	}
	if task.Finished(now.status) && now.reviewed != "" {
		s.mu.Lock()
		delete(s.seen, id) // a reviewed, finished task changes rarely; forget it to keep the map small
		s.mu.Unlock()
	}
	for _, e := range events {
		s.fireTask(e, &d)
	}
}

func taskPayload(d *api.TaskDetail) map[string]any {
	return map[string]any{"id": d.ID, "title": d.Title, "status": d.Status, "cwd": d.Cwd, "session": d.Session,
		"member": d.Member, "accountId": d.AccountID}
}

func (s *Service) fireTask(event string, d *api.TaskDetail) {
	extra := map[string]any{"task": taskPayload(d), "attention": d.Attention}
	if d.Review != nil {
		extra["review"] = d.Review
	}
	s.dispatch(event, d.ID, d.Cwd, extra)
}

// Fire runs the hooks of an event that is not about a task's state (account and daemon events); taskID may be
// empty. data is merged into the payload.
func (s *Service) Fire(event, taskID string, data map[string]any) {
	s.dispatch(event, taskID, "", data)
}

func (s *Service) payload(event, taskID string, data map[string]any) []byte {
	p := map[string]any{"event": event, "ts": api.FormatTime(s.o.Now()), "task": nil, "attention": nil}
	if taskID != "" {
		p["taskId"] = taskID
	}
	for k, v := range data {
		p[k] = v
	}
	b, err := json.Marshal(p)
	if err != nil {
		b = []byte(`{"event":` + strconv.Quote(event) + `}`)
	}
	return b
}

func (s *Service) dispatch(event, taskID, cwd string, data map[string]any) {
	hooks, err := s.List(s.ctx)
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Warn("hook: list", "err", err)
		}
		return
	}
	var match []api.Hook
	for _, h := range hooks {
		if !h.Enabled || !slices.Contains(h.On, event) {
			continue
		}
		if h.Project != "" && !inProject(h.Project, cwd) {
			continue
		}
		match = append(match, h)
	}
	if len(match) == 0 {
		return
	}
	body := s.payload(event, taskID, data)
	for _, h := range match {
		s.enqueue(job{hook: h, event: event, taskID: taskID, payload: body, cwd: cwd})
	}
}

func inProject(project, cwd string) bool {
	if cwd == "" {
		return false
	}
	c, err := fsutil.Canonical(cwd)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(project, c)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (s *Service) enqueue(j job) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.queue) >= maxQueue {
		dropped := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		s.writeLog(fmt.Sprintf("%s WARNING hook event queue full (%d); dropped %s for hook %s", s.stamp(), maxQueue,
			dropped.event, dropped.hook.ID))
		s.mu.Lock()
	}
	s.queue = append(s.queue, j)
	s.mu.Unlock()
	s.pump()
}

// pump starts waiting jobs while fewer than maxConcurrent run.
func (s *Service) pump() {
	for {
		s.mu.Lock()
		if s.closed || s.running >= maxConcurrent || len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.running++
		s.mu.Unlock()
		ok := s.spawn(func() {
			run := s.run(j)
			s.mu.Lock()
			s.running--
			s.mu.Unlock()
			if j.done != nil {
				j.done <- run
			}
			s.pump()
		})
		if !ok {
			s.mu.Lock()
			s.running--
			s.mu.Unlock()
			if j.done != nil {
				close(j.done)
			}
			return
		}
	}
}

// Test runs one hook now with a hook.test event and returns its run.
func (s *Service) Test(ctx context.Context, id string) (api.HookRun, error) {
	hooks, err := s.List(ctx)
	if err != nil {
		return api.HookRun{}, err
	}
	var h *api.Hook
	for i := range hooks {
		if hooks[i].ID == id {
			h = &hooks[i]
		}
	}
	if h == nil {
		return api.HookRun{}, fmt.Errorf("%w: no hook %s", ErrNotFound, id)
	}
	done := make(chan api.HookRun, 1)
	s.mu.Lock()
	s.queue = append([]job{{hook: *h, event: api.HookTest, payload: s.payload(api.HookTest, "", map[string]any{
		"test": true}), test: true, done: done}}, s.queue...)
	s.mu.Unlock()
	s.pump()
	select {
	case r, ok := <-done:
		if !ok {
			return api.HookRun{}, errors.New("the daemon is stopping")
		}
		return r, nil
	case <-ctx.Done():
		return api.HookRun{}, ctx.Err()
	}
}

// ---- running ----------------------------------------------------------------------------------------------------

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		cmd := exec.CommandContext(ctx, comspec) //nolint:gosec // the user's own hook command, by design
		platform.SetCmdLine(cmd, `"`+comspec+`" /d /s /c "`+line+`"`)
		return cmd
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", line)
}

func (s *Service) run(j job) api.HookRun {
	start := s.o.Now()
	r := api.HookRun{HookID: j.hook.ID, Event: j.event, TaskID: j.taskID, StartedAt: api.FormatTime(start),
		Test: j.test}
	timeout := time.Duration(max(j.hook.TimeoutSeconds, 1)) * time.Second
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), timeout)
	defer cancel()
	cmd := shellCommand(context.Background(), j.hook.Run)
	cmd.Cancel = nil
	dir := j.cwd
	if st, err := os.Stat(dir); dir == "" || err != nil || !st.IsDir() {
		dir, _ = os.UserHomeDir()
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SEELEGENT_EVENT="+j.event, "SEELEGENT_TASK_ID="+j.taskID,
		"SEELEGENT_PORT="+strconv.Itoa(s.o.Port))
	cmd.Stdin = bytes.NewReader(j.payload)
	out := &capped{limit: maxOutput}
	cmd.Stdout, cmd.Stderr = out, out
	platform.HideWindow(cmd)
	tree, err := platform.StartTree(cmd)
	if err != nil {
		r.Error = err.Error()
	} else {
		done := make(chan error, 1)
		go func() { done <- tree.Wait() }()
		select {
		case err = <-done:
		case <-ctx.Done():
			r.TimedOut = true
			_ = tree.Kill(time.Second)
			err = <-done
		}
		_ = tree.Close()
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			r.ExitCode = &code
		}
		if err != nil && r.ExitCode == nil && !r.TimedOut {
			r.Error = err.Error()
		}
	}
	r.EndedAt = api.FormatTime(s.o.Now())
	r.Output = s.o.Redact(out.String())
	r.Error = s.o.Redact(r.Error)
	exit := "-"
	if r.ExitCode != nil {
		exit = strconv.Itoa(*r.ExitCode)
	}
	line := fmt.Sprintf("%s hook=%s event=%s task=%s exit=%s timedOut=%t ms=%d", s.stamp(), r.HookID, r.Event,
		cmpOr(r.TaskID, "-"), exit, r.TimedOut, s.o.Now().Sub(start).Milliseconds())
	if r.Error != "" {
		line += " error=" + strconv.Quote(r.Error)
	}
	if r.Output != "" {
		line += " output=" + strconv.Quote(r.Output)
	}
	s.writeLog(line)
	s.o.Ran(r)
	return r
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Service) stamp() string { return s.o.Now().UTC().Format(time.RFC3339) }

func (s *Service) writeLog(line string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	_, _ = io.WriteString(s.o.Log, s.o.Redact(line)+"\n")
}

// capped keeps the first limit bytes written to it and notes the rest.
type capped struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
	over  bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - c.buf.Len()
	if room <= 0 {
		c.over = c.over || len(p) > 0
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.over = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := strings.ToValidUTF8(c.buf.String(), "")
	if c.over {
		s += "… (truncated)"
	}
	return s
}
