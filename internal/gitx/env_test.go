package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func TestEnvDropsStrayGitVars(t *testing.T) {
	base := []string{"PATH=/bin", "GIT_DIR=/x", "git_index_file=/y", "GIT_DIFF_OPTS=-u0", "GIT_SSH_COMMAND=ssh",
		"GIT_CONFIG_GLOBAL=/g", "GIT_CONFIG_NOSYSTEM=1", "GIT_DIRTY=keep", "=C:=C:\\"}
	got := Env(base, "LC_ALL=C")
	want := []string{"PATH=/bin", "GIT_SSH_COMMAND=ssh", "GIT_CONFIG_GLOBAL=/g", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_DIRTY=keep", "=C:=C:\\", "LC_ALL=C"}
	if !slices.Equal(got, want) {
		t.Fatalf("Env = %q\nwant  %q", got, want)
	}
}

// TestStrayGitEnv: a daemon that inherited GIT_DIFF_OPTS and a GIT_INDEX_FILE of another place still shows the
// usual 3 lines of context and commits through the repository's own index.
func TestStrayGitEnv(t *testing.T) {
	dir := newRepo(t)
	lines := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7", "l8", "l9"}
	write(t, filepath.Join(dir, "long.txt"), strings.Join(lines, "\n")+"\n")
	git(t, dir, "add", "long.txt")
	git(t, dir, "commit", "-q", "-m", "long")

	strayIndex := filepath.Join(t.TempDir(), "index")
	t.Setenv("GIT_DIFF_OPTS", "--unified=0")
	t.Setenv("GIT_INDEX_FILE", strayIndex)
	lines[4] = "L5"
	write(t, filepath.Join(dir, "long.txt"), strings.Join(lines, "\n")+"\n")

	r := New(nil, nil)
	ctx := context.Background()
	d, err := r.Diff(ctx, dir, "long.txt", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 1 || d.Hunks[0].OldStart != 2 || d.Hunks[0].OldLines != 7 {
		t.Fatalf("hunks = %+v", d.Hunks)
	}
	ctxLines := 0
	for _, l := range d.Hunks[0].Lines {
		if l.Kind == api.DiffContext {
			ctxLines++
		}
	}
	if ctxLines != 6 {
		t.Fatalf("%d context lines, want 6: %+v", ctxLines, d.Hunks[0].Lines)
	}

	tf := TaskFiles{Cwd: dir, Files: map[string]int{"long.txt": 1}}
	if _, err := r.Commit(ctx, tf, "Edit long", []string{"long.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strayIndex); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("git wrote the stray index (%v)", err)
	}
	if got := git(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("the repository's index does not match the commit:\n%s", got)
	}
	if got := git(t, dir, "show", "HEAD:long.txt"); !strings.Contains(got, "L5") {
		t.Fatalf("HEAD:long.txt = %q", got)
	}
}
