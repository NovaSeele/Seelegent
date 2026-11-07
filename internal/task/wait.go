package task

import (
	"context"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// maxWaitSeconds caps a wait (contract §3).
const maxWaitSeconds = 3000

// Wait waits until the tasks are finished (every one, or with Any the first), until a task that is still pending
// newly needs the lead (WakeOnBlocked, default true), until the timeout, or until ctx ends. Unknown ids fail
// before waiting. Ending the wait never touches a task or its worker (an MCP cancel stops only the wait, old bug).
// All waits share the engine's one notifier.
func (s *Service) Wait(ctx context.Context, req api.WaitRequest, reportCwd string) (api.WaitResult, error) {
	if len(req.IDs) == 0 {
		return api.WaitResult{}, invalidf("ids are required")
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > maxWaitSeconds {
		return api.WaitResult{}, invalidf("timeoutSeconds must be 0..%d", maxWaitSeconds)
	}
	wake := req.WakeOnBlocked == nil || *req.WakeOnBlocked
	// The flags that were raised before the wait began do not end it.
	seen := map[string]string{}
	tasks, err := s.load(ctx, req.IDs)
	if err != nil {
		return api.WaitResult{}, err
	}
	for _, t := range tasks {
		seen[t.ID] = flagKey(t)
	}
	timer := s.clk.NewTimer(time.Duration(req.TimeoutSeconds) * time.Second)
	defer timer.Stop()
	for {
		// Registered before reading, so no change between read and wait is missed; only these tasks' records wake it.
		changed, release := s.bus.TasksChanged(req.IDs)
		tasks, err := s.load(ctx, req.IDs)
		if err != nil {
			release()
			return api.WaitResult{}, err
		}
		pending, finished := 0, 0
		blocked := false
		for _, t := range tasks {
			if Pending(t.Status) {
				pending++
				if key := flagKey(t); wake && key != "" && key != seen[t.ID] {
					blocked = true
				}
			} else {
				finished++
			}
		}
		var res *api.WaitResult
		switch {
		case pending == 0 || req.Any && finished > 0:
			res = new(s.waitResult(tasks, true, api.WokeDone, req.Reports, reportCwd))
		case blocked:
			res = new(s.waitResult(tasks, false, api.WokeBlocked, req.Reports, reportCwd))
		default:
			select {
			case <-ctx.Done():
				res = new(s.waitResult(tasks, false, api.WokeCancelled, false, reportCwd))
			case <-timer.C():
				res = new(s.waitResult(tasks, false, api.WokeTimeout, req.Reports, reportCwd))
			case <-changed:
			}
		}
		release()
		if res != nil {
			return *res, nil
		}
	}
}

// flagKey identifies a raised flag that needs the lead ("" when none).
func flagKey(t *Task) string {
	a := t.Attention
	if a == nil {
		return ""
	}
	switch a.State {
	case api.AttentionBlocked, api.AttentionQuestion, api.AttentionStalled:
		return a.State + "|" + a.Reason + "|" + a.Since
	}
	return ""
}

func (s *Service) load(ctx context.Context, ids []string) ([]*Task, error) {
	out := make([]*Task, 0, len(ids))
	for _, id := range ids {
		t, err := s.repo.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) waitResult(tasks []*Task, done bool, woke string, reports bool, cwd string) api.WaitResult {
	res := api.WaitResult{Done: done, Woke: woke, Tasks: make([]api.TaskSummary, 0, len(tasks))}
	for _, t := range tasks {
		res.Tasks = append(res.Tasks, s.summary(t))
		if reports {
			res.Reports = append(res.Reports, s.report(t, cwd))
		}
	}
	return res
}
