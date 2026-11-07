package task

import (
	"context"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Contention lists the files of cwd's repository that two or more tasks changed within contentionWindowHours
// (GET /contention): the same data as the file overlaps of a task (M12), seen from the repository.
func (s *Service) Contention(ctx context.Context, cwd string) (api.ContentionView, error) {
	canonical, _, _, err := fsutil.ProjectOf(cwd)
	if err != nil {
		return api.ContentionView{}, invalidf("%v", err)
	}
	root := s.gitRoot(ctx, canonical)
	view := api.ContentionView{RepoRoot: root, WindowHours: s.settings().ContentionWindowHours,
		Files: []api.ContentionFile{}}
	if root == "" {
		view.RepoRoot = canonical
		return view, nil
	}
	tasks, err := s.repoTasks(ctx, root)
	if err != nil {
		return api.ContentionView{}, err
	}
	now := s.clk.Now()
	type entry struct {
		path  string
		tasks []api.ContentionTask
	}
	files := map[string]*entry{}
	for _, t := range tasks {
		for k, f := range s.touched(t, now) {
			var last time.Time
			for _, sp := range f.spans {
				if sp[1].After(last) {
					last = sp[1]
				}
			}
			e := files[k]
			if e == nil {
				e = &entry{path: f.path}
				files[k] = e
			}
			e.tasks = append(e.tasks, api.ContentionTask{TaskID: t.ID, Title: t.Title, Status: t.Status,
				At: api.FormatTime(last)})
		}
	}
	for _, e := range files {
		if len(e.tasks) < 2 {
			continue
		}
		sort.Slice(e.tasks, func(i, j int) bool { return e.tasks[i].At > e.tasks[j].At })
		view.Files = append(view.Files, api.ContentionFile{File: e.path, Tasks: e.tasks})
	}
	sort.Slice(view.Files, func(i, j int) bool {
		a, b := view.Files[i], view.Files[j]
		if len(a.Tasks) != len(b.Tasks) {
			return len(a.Tasks) > len(b.Tasks)
		}
		return a.File < b.File
	})
	if len(view.Files) > maxOverlaps {
		view.Files = view.Files[:maxOverlaps]
	}
	return view, nil
}
