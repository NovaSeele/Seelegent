package task

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// A turn whose end snapshot hit its limits records it and says so in an event (M18 hunt H5).
func TestApplyCheckRecordsTruncatedEndSnapshot(t *testing.T) {
	tk := &Task{ID: "t1"}
	tu := &Turn{N: 2, Snap: &TurnSnap{Root: "/r"}}
	evs := applyCheck(tk, tu, &turnCheck{truncated: true,
		risks: []api.RiskFile{{Path: "src/work.txt", Why: snapshot.WhyOutsideTools, Deleted: true}}})
	if !tu.Snap.EndTruncated || !tu.Snap.Checked {
		t.Fatalf("snap: %+v", tu.Snap)
	}
	if len(evs) != 2 || evs[0].Type != api.EventSnapshotTruncated || evs[1].Type != api.EventWorkAtRisk {
		t.Fatalf("events: %+v", evs)
	}
	d, ok := evs[0].Data.(api.SnapshotTruncatedData)
	if !ok || len(d.Snapshots) != 1 || d.Snapshots[0] != snapshot.KindEnd || d.MaxFiles != snapshot.MaxFiles {
		t.Fatalf("data: %+v", evs[0].Data)
	}

	// A start snapshot that was cut is named too; a turn without a cut has no such event.
	tu = &Turn{N: 1, Snap: &TurnSnap{Root: "/r", Truncated: true}}
	evs = applyCheck(tk, tu, &turnCheck{})
	if len(evs) != 1 || evs[0].Data.(api.SnapshotTruncatedData).Snapshots[0] != snapshot.KindStart {
		t.Fatalf("start cut: %+v", evs)
	}
	if evs := applyCheck(tk, &Turn{N: 3, Snap: &TurnSnap{Root: "/r"}}, &turnCheck{}); len(evs) != 0 {
		t.Fatalf("no cut: %+v", evs)
	}
}
