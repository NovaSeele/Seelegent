package proof

import (
	"slices"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Entry sources.
const (
	SourceTask       = "task"
	SourceRegression = "regression"
)

// Entry is the ledger's record of one spec task: its last gate result and when it was last green.
type Entry struct {
	TaskID      string
	State       string // green, red or error
	At          time.Time
	Source      string // task or regression
	Ref         string // the Seelegent task id or the regression run id
	GitHead     string
	Cmds        []EntryCmd
	LastGreenAt time.Time
}

// EntryCmd is one command behind an entry.
type EntryCmd struct {
	Cmd   string `json:"cmd"`
	Exit  *int   `json:"exit,omitempty"`
	Ms    int64  `json:"ms"`
	State string `json:"state"`
}

// covering is a spec task covering a requirement, as the status rules see it.
type covering struct {
	done, hasGate bool
	entry         *Entry
}

func bad(e *Entry) bool { return e != nil && (e.State == api.GateRed || e.State == api.GateError) }

// status decides how far a requirement is proven from the tasks that cover it.
func status(cov []covering) api.ProofStatus {
	switch {
	case len(cov) == 0:
		return api.ProofUnmapped
	case slices.ContainsFunc(cov, func(c covering) bool { return !c.done }):
		return api.ProofInProgress
	case slices.ContainsFunc(cov, func(c covering) bool {
		return bad(c.entry) && c.entry.Source == SourceRegression && !c.entry.LastGreenAt.IsZero()
	}):
		return api.ProofRegressed
	case slices.ContainsFunc(cov, func(c covering) bool { return bad(c.entry) }):
		return api.ProofFailing
	case slices.ContainsFunc(cov, func(c covering) bool {
		return !c.hasGate || c.entry == nil || c.entry.State != api.GateGreen
	}):
		return api.ProofUnproven
	}
	return api.ProofProven
}

// BuildMatrix builds the requirement matrix of a feature from its spec, its tasks and the ledger entries of its
// spec tasks (by task id).
func BuildMatrix(feature string, spec Spec, tasks Tasks, entries map[string]Entry) api.ProofMatrix {
	m := api.ProofMatrix{Feature: feature, Title: spec.Title, Rows: []api.ProofRow{}, Stories: []api.ProofStory{},
		UncoveredTasks: []string{}, FeatureGate: api.NonNil(tasks.FeatureGate)}
	featureGate := len(tasks.FeatureGate) > 0
	for i := range tasks.Tasks {
		t := &tasks.Tasks[i]
		m.Tasks.Total++
		if t.Done {
			m.Tasks.Done++
		}
		if len(t.Covers) == 0 {
			m.UncoveredTasks = append(m.UncoveredTasks, t.ID)
		}
	}
	for _, r := range spec.Requirements {
		row := api.ProofRow{ID: r.ID, Kind: r.Kind, Text: r.Text, Stories: []string{}, Tasks: []api.ProofRowTask{}}
		var cov []covering
		for i := range tasks.Tasks {
			t := &tasks.Tasks[i]
			if !slices.Contains(t.Covers, r.ID) {
				continue
			}
			c := covering{done: t.Done, hasGate: len(t.Gates) > 0 || featureGate}
			rt := api.ProofRowTask{ID: t.ID, Done: t.Done, Gates: api.NonNil(t.Gates)}
			if e, ok := entries[t.ID]; ok {
				c.entry = &e
				rt.State, rt.At, rt.Source, rt.Ref = e.State, api.FormatTime(e.At), e.Source, e.Ref
			}
			cov = append(cov, c)
			row.Tasks = append(row.Tasks, rt)
			if t.Story != "" && !slices.Contains(row.Stories, t.Story) {
				row.Stories = append(row.Stories, t.Story)
			}
		}
		sort.Strings(row.Stories)
		row.Status = status(cov)
		m.Counts.Add(row.Status)
		m.Rows = append(m.Rows, row)
	}
	for _, st := range spec.Stories {
		ps := api.ProofStory{ID: st.ID, Title: st.Title, Priority: st.Priority}
		for _, row := range m.Rows {
			if slices.Contains(row.Stories, st.ID) {
				ps.Counts.Add(row.Status)
			}
		}
		m.Stories = append(m.Stories, ps)
	}
	return m
}
