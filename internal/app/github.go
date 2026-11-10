package app

import (
	"context"
	"path/filepath"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/automation"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/ticket"
)

// buildGitHub wires the GitHub flow and the automations (milestone 15) after the team services. The gh CLI is
// looked up on the daemon's PATH at each use (never when Options.NoGh is set, as in tests); the token is a
// private file in <home>/github.
func (a *App) buildGitHub(o Options) {
	gh := github.ExecGh{Env: func() []string { return a.env }, Path: func() string {
		if o.NoGh {
			return ""
		}
		if o.Gh != "" {
			return o.Gh
		}
		return envx.ResolveCommand("gh", a.env)
	}}
	a.GitHub = github.NewDriver(github.DriverOptions{Tokens: github.NewTokenStore(filepath.Join(a.Paths.Home, "github")),
		Gh: gh, EnterpriseHosts: func() []string { return a.Settings().GithubEnterpriseHosts }, Now: o.Clock.Now})
	a.Tickets = ticket.New(ticket.Options{DB: a.DB, Git: a.Git, Tasks: a.Tasks, Meetings: a.Meetings,
		GitHub: a.GitHub, WorktreeBase: filepath.Join(a.Paths.Home, "worktrees", "tickets"),
		PollMinutes: func() int { return a.Settings().GithubPollMinutes }, UserHome: o.RealHome,
		TicketChanged: func(t api.TicketSummary) { a.withAPI(func(s *httpapi.Server) { s.PublishTicket(t) }) },
		IssuesChanged: func(id string) { a.withAPI(func(s *httpapi.Server) { s.PublishIssuesChanged(id) }) },
		Now:           o.Clock.Now, Logger: a.log})
	a.Automations = automation.New(automation.Options{DB: a.DB, Tasks: a.Tasks, Members: a.Members,
		Quota: automation.PoolQuota{States: a.Pool.States, Model: a.defaultModel, Group: a.quotaGroup},
		Clock: o.Clock, MasterOn: func() bool { return a.Settings().AutomationsEnabled },
		ConfirmURL: a.automationLink, Logger: a.log,
		Tree:    func() platform.TreeOptions { return task.GateTree(a.Settings()) },
		Changed: func(x api.Automation) { a.withAPI(func(s *httpapi.Server) { s.PublishAutomation(x) }) },
		Deleted: func(id string) { a.withAPI(func(s *httpapi.Server) { s.PublishAutomationDeleted(id) }) }})
}

// defaultModel is the model a run of backend uses when none is given.
func (a *App) defaultModel(name, model string) string {
	if model != "" {
		return model
	}
	set := a.Settings()
	p, err := backend.PolicyFor(name, set, nil)
	if err != nil {
		return ""
	}
	m, err := p.Resolve("", "", set.DefaultTier)
	if err != nil {
		return ""
	}
	return m
}

// quotaGroup is the quota group of model on backend name.
func (a *App) quotaGroup(name, model string) string {
	if b := a.Backends[name]; b != nil {
		return b.QuotaGroup(model)
	}
	return name
}

// automationLink is the dashboard page where the user confirms an automation.
func (a *App) automationLink(id string) string {
	return a.dashboardBase() + "/automations?confirm=" + id
}

// startGitHub recovers the ticket outbox and starts the poller and the scheduler. Failures are logged.
func (a *App) startGitHub(ctx context.Context) {
	if err := a.Tickets.Start(ctx); err != nil {
		a.log.Warn("app: start the GitHub flow", "err", err)
	}
	if err := a.Automations.Start(ctx); err != nil {
		a.log.Warn("app: start the automations", "err", err)
	}
}

// closeGitHub stops them (before the task engine).
func (a *App) closeGitHub() {
	a.Automations.Close()
	a.Tickets.Close()
}
