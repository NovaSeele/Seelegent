package mcpserver

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// geminiGroup is the agy quota group the old slim status read gemini5h and geminiWeekly from.
const geminiGroup = "Gemini Models"

// statusArgs are the arguments of status.
type statusArgs struct {
	Full optBool `json:"full"`
}

// slimAccount is an account in the slim status. The fields of the old slim view come first; remaining is new and
// gives every backend's capacity (old C-B11: only Gemini quota was shown).
type slimAccount struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Status       string `json:"status"`
	Running      int    `json:"running"`
	Gemini5h     *int   `json:"gemini5h"`
	GeminiWeekly *int   `json:"geminiWeekly"`
	// Remaining is the lowest remaining percentage per quota window ("5h", "weekly", ...) over the account's groups.
	Remaining    map[string]int `json:"remaining,omitempty"`
	Resting      bool           `json:"resting,omitempty"`
	RestingUntil string         `json:"restingUntil,omitempty"`
	Backend      string         `json:"backend"`
	Provider     *string        `json:"provider"`
	Auth         string         `json:"auth"`
	KeyHint      *string        `json:"keyHint"`
	// ModelPerTask marks an API-key account whose provider has no tier models: a delegate on it must pass model.
	ModelPerTask bool `json:"modelPerTask,omitempty"`
	// TestResult is an API-key account's key test state (not_tested, ok, refused, ...).
	TestResult string `json:"testResult,omitempty"`
}

// slimTask is an active task in the slim status.
type slimTask struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Status string  `json:"status"`
	Reason *string `json:"reason"`
}

// slimBackend is a backend in the status.
type slimBackend struct {
	ID        string `json:"id"`
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Accounts  int    `json:"accounts"`
	Active    int    `json:"active"`
}

// slimStatus is the default status answer.
type slimStatus struct {
	Accounts    []slimAccount `json:"accounts"`
	ActiveTasks []slimTask    `json:"active_tasks"`
	Backends    []slimBackend `json:"backends,omitempty"`
}

// fullStatus is status with full: true (the daemon's own shapes).
type fullStatus struct {
	Dashboard   string            `json:"dashboard"`
	Accounts    []api.Account     `json:"accounts"`
	ActiveTasks []api.TaskSummary `json:"active_tasks"`
	Backends    []api.Backend     `json:"backends,omitempty"`
}

func (s *Server) status(ctx context.Context, k *call) (result, error) {
	var a statusArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return result{}, err
	}
	tasks, err := ops.ActiveTasks(ctx, c)
	if err != nil {
		return result{}, err
	}
	backends, err := c.Backends(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return result{}, err
		}
		backends = nil // the old server left backends out when they could not be read
	}
	sum := statusSummary(accounts, tasks)
	if a.Full.or(false) {
		return result{value: fullStatus{Dashboard: s.dashboard(), Accounts: api.NonNil(accounts),
			ActiveTasks: api.NonNil(tasks), Backends: backends}, summary: sum + " Dashboard: " + s.dashboard()}, nil
	}
	out := slimStatus{Accounts: make([]slimAccount, 0, len(accounts)), ActiveTasks: make([]slimTask, 0, len(tasks))}
	for i := range accounts {
		out.Accounts = append(out.Accounts, slimAccountOf(accounts[i]))
	}
	for i := range tasks {
		t := &tasks[i]
		out.ActiveTasks = append(out.ActiveTasks, slimTask{ID: t.ID, Title: t.Title, Status: string(t.Status),
			Reason: ops.TaskReason(*t)})
	}
	for _, b := range backends {
		out.Backends = append(out.Backends, slimBackend{ID: b.ID, Enabled: b.Enabled, Installed: b.Installed,
			Accounts: b.Accounts, Active: b.Active})
	}
	return result{value: out, summary: sum}, nil
}

// slimAccountOf builds the slim view of an account.
func slimAccountOf(a api.Account) slimAccount {
	out := slimAccount{ID: a.ID, Label: a.Label, Status: string(a.Status), Running: a.Busy, Backend: a.Backend,
		Auth: "login", Resting: a.Resting, RestingUntil: a.RestingUntil, ModelPerTask: a.ModelPerTask,
		TestResult: a.TestResult}
	if a.Kind == "api-key" {
		out.Auth = "api-key"
	}
	if a.Provider != "" {
		out.Provider = &a.Provider
	}
	if a.KeyHint != "" {
		out.KeyHint = &a.KeyHint
	}
	for _, g := range a.Quota.Groups {
		for _, w := range g.Windows {
			pct := int(math.Round(w.Remaining * 100))
			if g.Name == geminiGroup {
				switch w.Window {
				case "5h":
					out.Gemini5h = &pct
				case "weekly":
					out.GeminiWeekly = &pct
				}
			}
			if out.Remaining == nil {
				out.Remaining = map[string]int{}
			}
			if cur, ok := out.Remaining[w.Window]; !ok || pct < cur {
				out.Remaining[w.Window] = pct
			}
		}
	}
	return out
}

// statusSummary is the one-line summary of status.
func statusSummary(accounts []api.Account, tasks []api.TaskSummary) string {
	ready := 0
	for i := range accounts {
		if a := &accounts[i]; (a.Status == api.AccountOK || a.Status == api.AccountError) && !a.Resting {
			ready++
		}
	}
	need := 0
	for i := range tasks {
		if ops.Blocked(tasks[i]) {
			need++
		}
	}
	s := fmt.Sprintf("%d account%s (%d ready), %d active task%s", len(accounts), plural(len(accounts)), ready,
		len(tasks), plural(len(tasks)))
	if need > 0 {
		verb := "need"
		if need == 1 {
			verb = "needs"
		}
		s += fmt.Sprintf(", %d %s the lead", need, verb)
	}
	s += "."
	if perTask := modelPerTaskProviders(accounts); len(perTask) > 0 {
		s += fmt.Sprintf(" Model per task (pass model in delegate): provider %s.", strings.Join(perTask, ", "))
	}
	return s
}

// modelPerTaskProviders lists the providers, once each and in account order, whose API-key accounts have no
// default model: a delegate with that provider must name its model.
func modelPerTaskProviders(accounts []api.Account) []string {
	var out []string
	for i := range accounts {
		if a := &accounts[i]; a.ModelPerTask && a.Provider != "" && !slices.Contains(out, a.Provider) {
			out = append(out, a.Provider)
		}
	}
	return out
}
