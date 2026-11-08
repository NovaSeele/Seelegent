package task

import (
	"context"
	"math"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// Report limits (old slimReport).
const (
	reportCommands     = 6
	reportCommandChars = 120
	reportErrorChars   = 300
	reportResponse     = 20000
)

// fileLink matches a markdown link to a file:// target.
var fileLink = regexp.MustCompile(`\[([^\]]+)\]\(file:/{2,3}[^)]*\)`)

// Report returns the lead-facing report of task id (the old slim report, contract §3.4). Files are shown relative
// to cwd when given (the lead's folder), else to the task's folder.
func (s *Service) Report(ctx context.Context, id, cwd string) (api.TaskReport, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskReport{}, err
	}
	return s.report(ctx, t, cwd), nil
}

func (s *Service) report(ctx context.Context, t *Task, cwd string) api.TaskReport {
	if cwd == "" {
		cwd = t.Cwd
	}
	rep := api.TaskReport{TaskID: t.ID, Status: string(t.Status), TokensUsed: t.TokensUsed(), Error: t.Error,
		FilesChanged: []string{}, WaitingUntil: api.FormatTime(t.WaitingUntil), Waiting: waitingOf(t),
		WaitDetail: waitDetailOf(t),
		DepsReason: t.DepsReason,
		Cancelling: t.CancelRequested && Active(t.Status)}
	if s.o.Dashboard != nil {
		rep.Dashboard = s.o.Dashboard(t.ID)
	}
	if t.Profile != nil {
		rep.Profile = t.Profile.ID
	}
	if t.Spec != nil {
		rep.Spec = &api.ReportSpec{Dir: t.Spec.Dir, TaskIDs: t.Spec.TaskIDs}
	}
	if t.Effort != "" && t.Effort != "default" {
		rep.Effort = t.Effort
	}
	rep.Model = t.Model
	tu := t.LastTurn()
	if tu != nil {
		rep.Turn = tu.N
		rep.TurnCostUSD = s.turnCost(ctx, t, tu)
		if tu.Model != "" {
			rep.Model = tu.Model
		}
		resp := tu.Response
		if r := []rune(resp); len(r) > reportResponse {
			resp = string(r[:reportResponse])
		}
		rep.Response = fileLink.ReplaceAllString(resp, "$1")
		for _, f := range tu.Files {
			rep.FilesChanged = append(rep.FilesChanged, relTo(cwd, t.Cwd, f))
		}
		if n := len(tu.Commands); n > 0 {
			for _, c := range tu.Commands[max(0, n-reportCommands):] {
				if r := []rune(c); len(r) > reportCommandChars {
					c = string(r[:reportCommandChars])
				}
				rep.CommandsRun = append(rep.CommandsRun, c)
			}
			if n > reportCommands {
				rep.CommandsTotal = n
			}
		}
		notOK := false
		for _, a := range tu.Attempts {
			if a.Status != AttemptOK {
				notOK = true
			}
		}
		if len(tu.Attempts) > 1 || notOK {
			for _, a := range tu.Attempts {
				e := a.Error
				if r := []rune(e); len(r) > reportErrorChars {
					e = string(r[:reportErrorChars])
				}
				rep.Attempts = append(rep.Attempts, api.ReportAttempt{Account: usage.SafeLabel(a.AccountLabel, a.AccountID),
					Model: a.Model, Result: a.Status, Error: e})
			}
		}
		if rep.Worker == "" {
			if a := tu.LastAttempt(); a != nil {
				rep.Worker = usage.SafeLabel(a.AccountLabel, a.AccountID)
			}
		}
		rep.Question = parseBlocked(tu.Response)
		if tu.By == ByUser {
			rep.AnsweredBy = ByUser
		}
		rep.Handback = tu.Handback
		rep.DeniedActions = tu.DeniedActions
		rep.Hygiene, rep.ScopeWarnings = findingsReport(tu.Hygiene), findingsReport(tu.Scope)
		rep.SnapshotTruncated = tu.Snap != nil && tu.Snap.Checked && (tu.Snap.Truncated || tu.Snap.EndTruncated)
	}
	gateReport(t, &rep)
	rep.WorkAtRisk = riskReport(t)
	for _, o := range s.fileOverlaps(ctx, t) {
		rep.FileOverlaps = append(rep.FileOverlaps, api.ReportOverlap{File: o.File, TaskID: o.TaskID, Title: o.Title,
			Status: string(o.Status), Severity: o.Severity})
	}
	var blocked []string
	for _, x := range t.Turns {
		for _, c := range x.BlockedCommands {
			if !slices.Contains(blocked, c) {
				blocked = append(blocked, c)
			}
		}
	}
	rep.BlockedCommands = blocked
	rep.Consults = consultsReport(t)
	if a := t.Attention; a != nil && (api.NeedsLead(a.State) || a.Reason == ReasonBackgroundWait) {
		rep.Attention = &api.ReportAttention{State: a.State, Reason: a.Reason, Detail: a.Detail}
	}
	return rep
}

// relTo shows a task file (relative to the task's folder, or absolute) relative to the lead's folder with '/'
// separators; a file outside that folder stays absolute.
func relTo(cwd, taskCwd, f string) string {
	p := filepath.FromSlash(f)
	if !filepath.IsAbs(p) {
		p = filepath.Join(taskCwd, p)
	}
	if rel, ok := relInside(cwd, p); ok {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// turnCost is the cost of the turn's attempts as the account spend counts them (account.AttemptCost): tokens ×
// the user's prices for the model, else the CLI's estimate on the official Anthropic API; 0 when no attempt has a
// cost it can trust.
func (s *Service) turnCost(ctx context.Context, t *Task, tu *Turn) float64 {
	if t.Provider == "" {
		return 0
	}
	p, err := s.o.Accounts.GetProvider(ctx, t.Provider)
	if err != nil {
		return 0
	}
	cost := 0.0
	for _, a := range tu.Attempts {
		if a.Usage == nil {
			continue
		}
		u := a.Usage
		_, usd := account.AttemptCost(&p, a.Backend, a.Model, u.InputTokens, u.CachedTokens, u.OutputTokens, u.CostUSD)
		cost += usd
	}
	return math.Round(cost*1e6) / 1e6
}
