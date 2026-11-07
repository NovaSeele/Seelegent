package task

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// priorWork is what the task itself did to files in the turns before the one being checked, so the check does
// not report the task's own earlier work as lost, yet still sees the user's lines in those files.
type priorWork struct {
	// own holds, per file Key, the lines the task's earlier turns added (end minus start of each turn, as a multiset
	// of trimmed non-empty lines, the way snapshot.Analyze counts lines).
	own map[string]map[string]int
	// skip holds the files of an earlier turn whose own lines are unknown (no snapshots, unreadable content): they
	// are left out of the check as a whole, as before.
	skip map[string]bool
	// files holds every file the earlier turns' edit tools changed.
	files map[string]bool
}

// mine reports a finding the task's own earlier work explains: a file of an earlier turn that lost no line of
// anyone else (deleted or rewritten with only the task's lines in it), or a binary one, whose lines cannot be told
// apart.
func (w priorWork) mine(path string, lost int, binary bool) bool {
	return w.files[snapshot.Key(path)] && (binary || lost == 0)
}

// priorWorkOf gathers the task's own lines in the files its edit tools changed in the turns before tu.
func (s *Service) priorWorkOf(ctx context.Context, t *Task, tu *Turn) priorWork {
	w := priorWork{own: map[string]map[string]int{}, skip: map[string]bool{}, files: map[string]bool{}}
	for _, p := range t.Turns {
		if p.N >= tu.N {
			continue
		}
		files := turnFiles(t, p, liveState{})
		if len(files) == 0 {
			continue
		}
		start, end, ok := s.turnSnapshots(t, p)
		for _, rel := range files {
			k := snapshot.Key(rel)
			w.files[k] = true
			if !ok || w.skip[k] {
				w.skip[k] = true
				continue
			}
			before, okB := s.fileAt(ctx, start, rel)
			after, okA := s.fileAt(ctx, end, rel)
			if !okB || !okA {
				w.skip[k] = true
				delete(w.own, k)
				continue
			}
			added := subtractLines(countLines(after), countLines(before))
			if len(added) == 0 {
				continue
			}
			if w.own[k] == nil {
				w.own[k] = map[string]int{}
			}
			for line, n := range added {
				w.own[k][line] += n
			}
		}
	}
	return w
}

// turnSnapshots loads the start snapshot of turn p and the state it ended with (its end snapshot, else the next
// turn's start); ok is false when either is missing.
func (s *Service) turnSnapshots(t *Task, p *Turn) (start, end snapshot.Manifest, ok bool) {
	if p.Snap == nil {
		return start, end, false
	}
	start, err := s.o.Snapshots.Load(t.ID, p.N, snapshot.KindStart)
	if err != nil {
		return start, end, false
	}
	end, ok, err = s.turnEndState(t, p)
	if err != nil && !errors.Is(err, snapshot.ErrNotFound) {
		s.log.Warn("task: earlier turn's snapshot", "task", t.ID, "turn", p.N, "err", err)
	}
	return start, end, ok && err == nil
}

// fileAt returns a file's content in a snapshot: its stored copy, nothing for a missing file, HEAD at the time for
// a file the snapshot does not list (it was clean then). ok is false when the content cannot be known.
func (s *Service) fileAt(ctx context.Context, m snapshot.Manifest, rel string) ([]byte, bool) {
	if f, found := m.Find(rel); found {
		switch {
		case f.Missing:
			return nil, true
		case !f.Stored || f.Binary:
			return nil, false
		}
		data, err := s.o.Snapshots.Content(f)
		return data, err == nil
	}
	if m.Head == "" {
		return nil, true
	}
	data, _, err := s.o.Repos.Blob(ctx, m.Root, m.Head, rel, snapshot.MaxFileBytes, false)
	return data, err == nil
}

// headWithOwn returns HEAD content with the task's own earlier lines added, so snapshot.Analyze counts them as
// committed: lines the turn lost that the task itself wrote earlier are not someone else's work.
func headWithOwn(head []byte, own map[string]int) []byte {
	var b bytes.Buffer
	b.Write(head)
	for line, n := range own {
		for range n {
			b.WriteByte('\n')
			b.WriteString(line)
		}
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// countLines is the multiset of the trimmed, non-empty lines of data (snapshot.Analyze's measure).
func countLines(data []byte) map[string]int {
	m := map[string]int{}
	for _, l := range bytes.Split(data, []byte{'\n'}) {
		if t := strings.TrimSpace(string(l)); t != "" {
			m[t]++
		}
	}
	return m
}

func subtractLines(a, b map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range a {
		if d := v - b[k]; d > 0 {
			out[k] = d
		}
	}
	return out
}
