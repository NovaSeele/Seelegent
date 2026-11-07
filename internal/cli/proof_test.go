package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// gitInit makes dir a git repository with one commit (temp repositories only).
func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitPath, err := gitx.NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"},
		{"config", "core.hooksPath", filepath.Join(dir, ".no-hooks")}, {"add", "README.md"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.CommandContext(t.Context(), gitPath, args...)
		cmd.Dir = dir
		cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestGateFlagsAndProof: delegate passes --gate, --lane and --verify to the daemon (the gate runs, green, in a temp
// repository with a shell builtin), the report text shows it, and the proof commands answer.
func TestGateFlagsAndProof(t *testing.T) {
	d := newTestDaemon(t)
	td := testDeps(nil)
	gitInit(t, d.cwd)

	for _, args := range [][]string{
		{"delegate", "--no-gate", "--gate", "exit 0", "x"},
		{"delegate", "--no-gate", "--gate-detect", "x"},
		{"delegate", "--no-lane", "--lane", "a", "x"},
		{"proof"}, {"proof", "bogus"}, {"proof", "matrix"}, {"proof", "run-status"},
	} {
		wantErr(t, td, ExitUsage, "", args...)
	}

	d.queue.Push(t, script("conv-g", "Done."))
	var rep api.TaskReport
	must(t, td, "delegate", "--cwd", d.cwd, "--gate", "exit 0", "--lane", "README.md", "--verify=false", "--json",
		"Look only.").json(t, &rep)
	if rep.Status != string(api.StatusDone) || rep.Gate == nil || rep.Gate.State != "green" || rep.Lane == nil ||
		len(rep.Lane.Paths) != 1 || rep.Lane.Paths[0] != "README.md" || rep.Verifier != nil {
		t.Fatalf("gated report = %+v", rep)
	}
	var detail api.TaskDetail
	must(t, td, "task", rep.TaskID, "--full", "--json").json(t, &detail)
	if detail.Proof == nil || detail.Proof.Verify {
		t.Fatalf("detail proof = %+v", detail.Proof)
	}

	var lanes api.LanesView
	must(t, td, "proof", "lanes", "--cwd", d.cwd, "--json").json(t, &lanes)
	if len(lanes.Claims) != 1 || lanes.Claims[0].TaskID != rep.TaskID {
		t.Fatalf("lanes = %+v", lanes)
	}
	if out := must(t, td, "proof", "lanes", "--cwd", d.cwd).stdout; !strings.Contains(out, rep.TaskID) {
		t.Fatalf("lanes text:\n%s", out)
	}
	var st api.ProofStats
	must(t, td, "proof", "stats", "--cwd", d.cwd, "--json").json(t, &st)
	if st.GatedTasks != 1 || st.GateRuns != 1 {
		t.Fatalf("stats = %+v", st)
	}
	var ix api.ProofIndex
	must(t, td, "proof", "index", "--cwd", d.cwd, "--json").json(t, &ix)
	if ix.RepoRoot == "" || len(ix.Features) != 0 {
		t.Fatalf("index = %+v", ix)
	}
	var run api.ProofRun
	must(t, td, "proof", "run", "--cwd", d.cwd, "--wait", "30", "--json").json(t, &run)
	if run.ID == "" || run.State == api.RunRunning {
		t.Fatalf("run = %+v", run)
	}
	must(t, td, "proof", "run-status", run.ID, "--cwd", d.cwd)
	must(t, td, "proof", "contention", "--cwd", d.cwd)
}
