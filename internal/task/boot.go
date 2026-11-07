package task

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// bootNewest is how many of the newest tasks GET /state includes besides the ones that need attention.
const bootNewest = 100

// Boot returns the tasks of the dashboard's first paint (GET /state): every pending task, every task with an
// attention flag, every done task without a review, plus the newest 100 others, newest first.
func (s *Service) Boot(ctx context.Context) ([]api.TaskSummary, error) {
	// Hidden runs (verifier, consult, meeting, judge) are read through what started them, never listed.
	var args []any
	for _, k := range hiddenKinds() {
		args = append(args, k)
	}
	args = append(args, string(StatusWaitingDeps), string(StatusQueued), string(StatusRunning),
		string(StatusWaitingQuota), string(StatusDone), bootNewest)
	shown := `coalesce(json_extract(data, '$.kind'), '') NOT IN (` + placeholders(len(hiddenKinds())) + `)`
	var tasks []*Task
	err := s.repo.db.Read(ctx, func(tx *sql.Tx) error {
		tasks = nil
		rows, err := tx.QueryContext(ctx, `WITH shown AS (SELECT * FROM tasks WHERE `+shown+`)
			SELECT `+taskColumns+` FROM shown
			WHERE status IN (?, ?, ?, ?)
			   OR json_extract(data, '$.attention') IS NOT NULL
			   OR (status = ? AND json_extract(data, '$.review') IS NULL)
			   OR id IN (SELECT id FROM shown ORDER BY created_at DESC, id DESC LIMIT ?)
			ORDER BY created_at DESC, id DESC`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			tasks = append(tasks, t)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return loadTurns(ctx, tx, tasks)
	})
	if err != nil {
		return nil, err
	}
	out := make([]api.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, s.summary(t))
	}
	return out, nil
}

// Projects returns the folders tasks ran in, newest activity first (GET /projects). Cwd, name and repository
// root come from the project's most recently updated task.
func (s *Service) Projects(ctx context.Context) ([]api.Project, error) {
	out := []api.Project{}
	err := s.repo.db.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		// SQLite takes the bare columns of an aggregate query with MAX() from the row that has the maximum.
		rows, err := tx.QueryContext(ctx, `SELECT project_key, cwd,
				COALESCE(json_extract(data, '$.projectName'), ''), COALESCE(json_extract(data, '$.repoRoot'), ''),
				MAX(updated_at), COUNT(*), SUM(status IN (?, ?, ?))
			FROM tasks GROUP BY project_key ORDER BY 5 DESC, project_key`,
			string(StatusQueued), string(StatusRunning), string(StatusWaitingQuota))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p api.Project
			var last string
			if err := rows.Scan(&p.Key, &p.Cwd, &p.Name, &p.RepoRoot, &last, &p.TaskCount, &p.ActiveCount); err != nil {
				return err
			}
			if at, err := parseTime(last); err == nil {
				p.LastActivityAt = api.FormatTime(at)
			}
			if p.Name == "" {
				p.Name = filepath.Base(p.Cwd)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GitScope is what the git endpoints need to know about a task (contract §3.3).
type GitScope struct {
	ID       string
	Status   Status
	Manual   bool
	Cwd      string
	RepoRoot string
	// Baseline lists the files (absolute paths) that already had uncommitted changes when the task started.
	Baseline []string
	// Files are the files the task's turns touched, relative to Cwd with '/' separators, each with the last
	// turn that touched it.
	Files map[string]int
	// Title and LastResponse feed a commit message suggestion.
	Title        string
	LastResponse string
	UpdatedAt    time.Time
}

// GitScope returns the git view of task id.
func (s *Service) GitScope(ctx context.Context, id string) (GitScope, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return GitScope{}, err
	}
	g := GitScope{ID: t.ID, Status: t.Status, Manual: t.Manual, Cwd: t.Cwd, RepoRoot: t.RepoRoot,
		Baseline: append([]string(nil), t.Baseline...), Files: map[string]int{}, Title: t.Title,
		UpdatedAt: t.UpdatedAt}
	for _, tu := range t.Turns {
		for _, f := range tu.Files {
			g.Files[f] = tu.N
		}
		if tu.Response != "" {
			g.LastResponse = tu.Response
		}
	}
	return g, nil
}
