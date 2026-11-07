package httpapi_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

const proofSpec = "# Feature Specification: Widgets\n\n### User Story 1 - Make widgets (Priority: P1)\n\n" +
	"- **FR-001**: Widgets are made.\n- **FR-002**: Widgets are counted.\n- **SC-001**: Nobody covers this.\n"

// The gate commands are shell builtins, so they run the same under cmd.exe and sh and touch nothing.
const proofTasks = "# Tasks\n\n" +
	"- [x] T001 [US1] Make widgets Covers: FR-001 Gate: `exit 0`\n" +
	"- [ ] T002 [US1] Count widgets Covers: FR-002\n"

// TestProofRoutes covers the M13 routes in a temp repository with a spec feature: index, matrix, a regression run
// (started, polled to its end, recorded in the matrix, cancel of a finished run), lanes, contention, stats, and the
// argument errors.
func TestProofRoutes(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	feature := filepath.Join(e.cwd, "specs", "001-widgets")
	if err := os.MkdirAll(feature, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"spec.md": proofSpec, "tasks.md": proofTasks} {
		if err := os.WriteFile(filepath.Join(feature, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, e.cwd, "add", "specs")
	gitIn(t, e.cwd, "commit", "-q", "-m", "spec")
	cwd := url.QueryEscape(e.cwd)

	var ix api.ProofIndex
	e.must(e.get("/api/v1/proof/index?cwd="+cwd), http.StatusOK).json(t, &ix)
	if len(ix.Features) != 1 || ix.Features[0].Dir != "specs/001-widgets" || ix.Features[0].Tasks.Total != 2 {
		t.Fatalf("index = %+v", ix)
	}
	var m api.ProofMatrix
	e.must(e.get("/api/v1/proof/matrix?cwd="+cwd+"&feature=specs/001-widgets"), http.StatusOK).json(t, &m)
	if m.Title != "Widgets" || len(m.Rows) != 3 || m.Counts.Unproven != 1 || m.Counts.InProgress != 1 ||
		m.Counts.Unmapped != 1 {
		t.Fatalf("matrix = %+v", m)
	}

	var run api.ProofRun
	e.must(e.post("/api/v1/proof/run", map[string]any{"cwd": e.cwd}), http.StatusAccepted).json(t, &run)
	if run.ID == "" || run.RepoRoot == "" {
		t.Fatalf("run = %+v", run)
	}
	deadline := time.Now().Add(limit)
	for run.State == api.RunRunning && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		e.must(e.get("/api/v1/proof/runs/"+run.ID+"?cwd="+cwd), http.StatusOK).json(t, &run)
	}
	if run.State != api.RunGreen || run.Counts.Green != 1 || run.Total != 1 {
		t.Fatalf("finished run = %+v", run)
	}
	e.must(e.get("/api/v1/proof/matrix?cwd="+cwd+"&feature=specs/001-widgets"), http.StatusOK).json(t, &m)
	if m.Counts.Proven != 1 || m.Rows[0].Tasks[0].Source != "regression" || m.Rows[0].Tasks[0].Ref != run.ID {
		t.Fatalf("matrix after the run = %+v", m)
	}
	var cancelled api.ProofRun
	e.must(e.post("/api/v1/proof/runs/"+run.ID+"/cancel", map[string]any{"cwd": e.cwd}), http.StatusOK).json(t, &cancelled)
	if cancelled.State != api.RunGreen {
		t.Fatalf("cancel of a finished run = %+v", cancelled)
	}

	var lanes api.LanesView
	e.must(e.get("/api/v1/lanes?cwd="+cwd), http.StatusOK).json(t, &lanes)
	if lanes.Mode == "" || len(lanes.Claims) != 0 {
		t.Fatalf("lanes = %+v", lanes)
	}
	var cont api.ContentionView
	e.must(e.get("/api/v1/contention?cwd="+cwd), http.StatusOK).json(t, &cont)
	if cont.WindowHours <= 0 || len(cont.Files) != 0 {
		t.Fatalf("contention = %+v", cont)
	}
	var st api.ProofStats
	e.must(e.get("/api/v1/proof/stats?cwd="+cwd+"&since=2020-01-01T00:00:00Z"), http.StatusOK).json(t, &st)
	if st.Since == "" || st.Tasks != 0 {
		t.Fatalf("stats = %+v", st)
	}

	e.wantError(e.get("/api/v1/lanes"), http.StatusBadRequest, api.CodeInvalidArgument, "cwd")
	e.wantError(e.get("/api/v1/proof/matrix?cwd="+cwd), http.StatusBadRequest, api.CodeInvalidArgument, "feature")
	e.wantError(e.get("/api/v1/proof/stats?since=yesterday"), http.StatusBadRequest, api.CodeInvalidArgument, "since")
	e.wantError(e.get("/api/v1/proof/runs/r-nope?cwd="+cwd), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/proof/matrix?cwd="+cwd+"&feature=../outside"), http.StatusBadRequest,
		api.CodeInvalidArgument, "")
}
