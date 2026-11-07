package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// Loop check limits (old attention.js detectLoop).
const (
	// loopWindow is how many finished tool calls the check looks at.
	loopWindow = 12
	// loopRepeat is how often the same call must come back to be a loop.
	loopRepeat = 4
	// loopErrors is how many failed calls in a row are a loop.
	loopErrors = 5
	// maxLiveEdited caps the edited files kept for a running attempt.
	maxLiveEdited = 500
)

// loopStep is one finished tool call.
type loopStep struct {
	// key identifies the call: its tool and canonical parameters.
	key, name string
	failed    bool
	// edit marks a call of an edit tool (a successful one with parameters seen once is progress).
	edit bool
}

var (
	editToolName = regexp.MustCompile(`(?i)write|replace|edit|patch|create`)
	readToolName = regexp.MustCompile(`(?i)read|view|list|search|grep|find|glob`)
)

// stepKey is the identity of a tool call: its name and its parameters with sorted keys (or its command line).
func stepKey(t *event.Tool) string {
	params := t.Command
	if len(t.Input) > 0 {
		var v any
		if err := json.Unmarshal(t.Input, &v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				params = string(b)
			}
		} else {
			params = string(t.Input)
		}
	}
	if params == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t.Name + "\x00" + params))
	return hex.EncodeToString(sum[:12])
}

// addStep appends the finished call t (pending is the key of its call event, used when the result carries no
// parameters) and keeps the last loopWindow steps.
func addStep(steps []loopStep, t *event.Tool, pending string) []loopStep {
	key := stepKey(t)
	if key == "" {
		key = pending
	}
	if key == "" {
		key = t.Name
	}
	steps = append(steps, loopStep{key: key, name: t.Name, failed: t.State == event.ToolFailed,
		edit: editToolName.MatchString(t.Name) && !readToolName.MatchString(t.Name)})
	if len(steps) > loopWindow {
		steps = steps[len(steps)-loopWindow:]
	}
	return steps
}

// detectLoop reports a worker going round in circles over its last tool calls: loopErrors failed calls in a row,
// or the same call loopRepeat times. Calls that alternate with something different each time (reading a file
// between edits, bugs.md A9) are not a loop, and neither are repeats with progress between them: a successful
// edit seen only once in the window.
func detectLoop(steps []loopStep) (loop bool, detail string) {
	failed := 0
	for i := len(steps) - 1; i >= 0 && steps[i].failed; i-- {
		failed++
	}
	if failed >= loopErrors {
		return true, fmt.Sprintf("the last %d tool calls failed", failed)
	}
	pos := map[string][]int{}
	for i, st := range steps {
		pos[st.key] = append(pos[st.key], i)
	}
	for i, st := range steps {
		ps := pos[st.key]
		if ps[0] != i || len(ps) < loopRepeat {
			continue // each key once, at its first call
		}
		alternating := true
		for j := 1; j < len(ps); j++ {
			if ps[j]-ps[j-1] != 2 {
				alternating = false
			}
		}
		if alternating {
			mid := steps[ps[0]+1].key
			same := true
			for j := 0; j+1 < len(ps); j++ {
				if steps[ps[j]+1].key != mid {
					same = false
				}
			}
			if !same {
				continue
			}
		} else {
			progress := false
			for j := ps[0] + 1; j < ps[len(ps)-1]; j++ {
				if s := steps[j]; s.edit && !s.failed && len(pos[s.key]) == 1 {
					progress = true
				}
			}
			if progress {
				continue
			}
		}
		return true, fmt.Sprintf("the worker repeated the same %s call %d times in its last %d steps", st.name,
			len(ps), len(steps))
	}
	return false, ""
}

// accountHealth is what the attention rules know about the accounts of one backend.
type accountHealth struct {
	// known is false until the accounts were read.
	known bool
	// total counts the accounts; enabled the enabled ones; usable those that can run work (signed in, with a key
	// and a plan); needsLogin the enabled ones that need a sign-in.
	total, enabled, usable, needsLogin int
}

// refreshHealth reads the accounts' state for the attention rules.
func (s *Service) refreshHealth(ctx context.Context) {
	if s.o.AccountStates == nil {
		return
	}
	states, err := s.o.AccountStates(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("task: account states", "err", err)
		}
		return
	}
	by := map[string]accountHealth{}
	for i := range states {
		st := &states[i]
		h := by[st.Account.Backend]
		h.known = true
		h.total++
		if st.Account.Enabled {
			h.enabled++
			switch st.Status {
			case pool.StatusOK, pool.StatusNew, pool.StatusError, pool.StatusTwin:
				h.usable++
			case pool.StatusNeedsLogin:
				h.needsLogin++
			default:
			}
		}
		by[st.Account.Backend] = h
	}
	s.healthMu.Lock()
	s.health = by
	s.healthMu.Unlock()
}

// healthOf returns what is known about the accounts of a backend.
func (s *Service) healthOf(backendName string) accountHealth {
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	if s.health == nil {
		return accountHealth{}
	}
	h, ok := s.health[backendName]
	if !ok {
		return accountHealth{known: true}
	}
	return h
}
