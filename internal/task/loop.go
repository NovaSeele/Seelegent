package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// Loop check limits (old attention.js detectLoop).
const (
	// loopWindow is how many finished tool calls the check looks at. How often the same call must come back to be
	// a loop is the loopRepeat setting.
	loopWindow = 12
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

// editPathKeys are the parameters that name an edit's file (old EDIT_PATH_KEYS).
var editPathKeys = []string{"Path", "TargetFile", "AbsolutePath", "File", "path", "file"}

// pathEditToolName matches the edit tools whose path-only calls get a file mark (old isPathOnlySuccessfulEdit).
var pathEditToolName = regexp.MustCompile(`(?i)write|replace|edit|sed`)

// fileMarkMax is the largest file the loop check hashes.
const fileMarkMax = 2 << 20

// pathOnlyEdit returns the file of a successful edit call whose parameters name only its file (agy often reports
// edits this way), or "".
func pathOnlyEdit(t *event.Tool) string {
	if t.State == event.ToolFailed || !pathEditToolName.MatchString(t.Name) || len(t.Input) == 0 {
		return ""
	}
	var params map[string]any
	if json.Unmarshal(t.Input, &params) != nil || len(params) == 0 {
		return ""
	}
	file := ""
	for k, v := range params {
		if !slices.Contains(editPathKeys, k) {
			return ""
		}
		if s, ok := v.(string); ok && s != "" && file == "" {
			file = s
		}
	}
	return file
}

// fileMark is the content hash of file (relative to cwd), or "" when it cannot be read or is larger than
// fileMarkMax.
func fileMark(cwd, file string) string {
	if !filepath.IsAbs(file) {
		if cwd == "" {
			return ""
		}
		file = filepath.Join(cwd, file)
	}
	st, err := os.Stat(file)
	if err != nil || !st.Mode().IsRegular() || st.Size() > fileMarkMax {
		return ""
	}
	data, err := os.ReadFile(file) //nolint:gosec // a file the worker reported editing, only hashed
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

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
// parameters) and keeps the last loopWindow steps. mark, when set, is part of the call's identity: the content
// hash of the file a path-only edit changed, or a number unique to that edit.
func addStep(steps []loopStep, t *event.Tool, pending, mark string) []loopStep {
	key := stepKey(t)
	if key == "" {
		key = pending
	}
	if key == "" {
		key = t.Name
	}
	if mark != "" {
		key += "\x00" + mark
	}
	steps = append(steps, loopStep{key: key, name: t.Name, failed: t.State == event.ToolFailed,
		edit: editToolName.MatchString(t.Name) && !readToolName.MatchString(t.Name)})
	if len(steps) > loopWindow {
		steps = steps[len(steps)-loopWindow:]
	}
	return steps
}

// loopRepeatOf is the loopRepeat setting, or its default when it is outside the range the settings allow.
func loopRepeatOf(n int) int {
	if n < config.LoopRepeatMin || n > config.LoopRepeatMax {
		return config.LoopRepeatDefault
	}
	return n
}

// detectLoop reports a worker going round in circles over its last tool calls: loopErrors failed calls in a row,
// or the same call repeat times (the loopRepeat setting; the window holds loopWindow calls, so a larger value
// never fires). Like the old detectLoop, every run of repeat consecutive occurrences of a call is judged on its own:
// calls that alternate with something different each time (reading a file between edits, bugs.md A9) are not a
// loop, and neither are repeats with progress between them: a successful edit seen only once in that run.
func detectLoop(steps []loopStep, repeat int) (loop bool, detail string) {
	repeat = loopRepeatOf(repeat)
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
		if ps[0] != i || len(ps) < repeat {
			continue // each key once, at its first call
		}
		for w := 0; w+repeat <= len(ps); w++ {
			if loopRun(steps, ps[w:w+repeat]) {
				return true, fmt.Sprintf("the worker repeated the same %s call %d times in its last %d steps",
					st.name, len(ps), len(steps))
			}
		}
	}
	return false, ""
}

// loopRun judges one run of occurrences (positions in steps) of the same call.
func loopRun(steps []loopStep, run []int) bool {
	alternating := true
	for j := 1; j < len(run); j++ {
		if run[j]-run[j-1] != 2 {
			alternating = false
		}
	}
	if alternating {
		// Only a loop when the call in between is the same each time.
		mid := steps[run[0]+1].key
		for j := 0; j+1 < len(run); j++ {
			if steps[run[j]+1].key != mid {
				return false
			}
		}
		return true
	}
	first, last := run[0], run[len(run)-1]
	count := map[string]int{}
	for j := first; j <= last; j++ {
		count[steps[j].key]++
	}
	for j := first + 1; j < last; j++ {
		if s := steps[j]; s.edit && !s.failed && count[s.key] == 1 {
			return false // progress
		}
	}
	return true
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
