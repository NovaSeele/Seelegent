package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c",
		"commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseInclude(t *testing.T) {
	paths, skipped := ParseInclude("\ufeff# env\n.env\n./config/local.json\n*.key\n!x\n../up\n.git/config\n.env\n")
	if !slices.Equal(paths, []string{".env", "config/local.json"}) {
		t.Errorf("paths = %v", paths)
	}
	if len(skipped) != 4 {
		t.Errorf("skipped = %+v", skipped)
	}
	if _, err := NormalizeShared([]string{"node_modules/", "/node_modules", "vendor"}); err != nil {
		t.Error(err)
	}
	if _, err := NormalizeShared([]string{"../x"}); err == nil {
		t.Error("../x accepted")
	}
	if _, err := NormalizeShared([]string{"dist/*"}); err == nil {
		t.Error("glob accepted")
	}
}

func TestWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	write(t, filepath.Join(root, "a.txt"), "one\n")
	write(t, filepath.Join(root, ".gitignore"), ".env\nnode_modules\nbuild.log\n")
	write(t, filepath.Join(root, ".worktreeinclude"), ".env\na.txt\nmissing.env\nnode_modules\n")
	git(t, root, "init", "-q")
	git(t, root, "config", "core.autocrlf", "false")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	write(t, filepath.Join(root, ".env"), "SECRET=1\n")
	write(t, filepath.Join(root, "node_modules", "lib", "index.js"), "module.exports = 1\n")
	write(t, filepath.Join(root, "a.txt"), "one\nuncommitted\n")
	write(t, filepath.Join(root, "new.txt"), "untracked\n")

	repos := gitx.New(nil, nil)
	tree, err := repos.SnapshotTree(ctx, root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st := git(t, root, "status", "--porcelain"); !strings.Contains(st, "M a.txt") || !strings.Contains(st, "?? new.txt") {
		t.Fatalf("the snapshot changed the index:\n%s", st)
	}
	base := git(t, root, "commit-tree", tree, "-p", "HEAD", "-m", "base")
	trees := &Trees{Git: repos, Base: filepath.Join(dir, "worktrees")}
	if _, err := trees.Add(ctx, root, filepath.Join(dir, "elsewhere"), base); err == nil {
		t.Fatal("a worktree outside Base was made")
	}
	wt, err := trees.Add(ctx, root, filepath.Join(trees.Base, "c1", "V1"), base)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(b) != "one\nuncommitted\n" {
		t.Errorf("worktree a.txt = %q", b)
	}
	env := trees.Setup(ctx, root, wt, []string{"node_modules", "a.txt"})
	if !slices.Equal(env.Copied, []string{".env"}) {
		t.Errorf("copied = %v (skipped %+v)", env.Copied, env.Skipped)
	}
	if !slices.Equal(env.Linked, []string{"node_modules"}) {
		t.Errorf("linked = %v (skipped %+v)", env.Linked, env.Skipped)
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "node_modules", "lib", "index.js")); string(b) != "module.exports = 1\n" {
		t.Errorf("linked file = %q", b)
	}
	// The worker edits a file and un-ignores node_modules: the link must still not be committed (old bug D21).
	write(t, filepath.Join(wt, "a.txt"), "two\n")
	write(t, filepath.Join(wt, ".gitignore"), ".env\n")
	commit, err := trees.Seal(ctx, wt, &env, "variant")
	if err != nil {
		t.Fatal(err)
	}
	names := git(t, root, "diff", "--name-only", base, commit)
	if strings.Contains(names, "node_modules") || !strings.Contains(names, "a.txt") {
		t.Errorf("sealed diff = %q", names)
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "lib", "index.js")); err != nil {
		t.Fatalf("the shared folder lost its contents: %v", err)
	}
	if _, err := trees.Remove(ctx, root, wt, &env, "final"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules", "lib", "index.js")); err != nil {
		t.Fatalf("removing the worktree reached the shared folder: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "one\nuncommitted\n" {
		t.Errorf("user's a.txt = %q", b)
	}
	if st := git(t, root, "status", "--porcelain"); !strings.Contains(st, "M a.txt") || !strings.Contains(st, "?? new.txt") {
		t.Errorf("user's status changed:\n%s", st)
	}
	trees.RemoveEmpty(filepath.Join(trees.Base, "c1"))
	if _, err := os.Stat(filepath.Join(trees.Base, "c1")); !os.IsNotExist(err) {
		t.Errorf("contest folder left: %v", err)
	}
}

// The repository's hooks never run for internal commands, and a symlink git tracks survives the final commit.
func TestNoHooksAndTrackedLinks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	write(t, filepath.Join(root, "a.txt"), "one\n")
	git(t, root, "init", "-q")
	git(t, root, "config", "core.autocrlf", "false")
	tracked := os.Symlink("a.txt", filepath.Join(root, "link.txt")) == nil
	if tracked {
		git(t, root, "config", "core.symlinks", "true")
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	ran := filepath.Join(dir, "hook-ran")
	for _, h := range []string{"post-checkout", "post-commit"} {
		write(t, filepath.Join(root, ".git", "hooks", h), "#!/bin/sh\necho "+h+" >> '"+filepath.ToSlash(ran)+"'\n")
		if err := os.Chmod(filepath.Join(root, ".git", "hooks", h), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	trees := &Trees{Git: gitx.New(nil, nil), Base: filepath.Join(dir, "worktrees")}
	wt, err := trees.Add(ctx, root, filepath.Join(trees.Base, "c1", "V1"), git(t, root, "rev-parse", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt, "a.txt"), "two\n")
	commit, err := trees.Remove(ctx, root, wt, nil, "final")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(ran); err == nil {
		t.Errorf("hooks ran: %s", b)
	}
	if tracked {
		if names := git(t, root, "diff", "--name-only", "HEAD", commit); names != "a.txt" {
			t.Errorf("final commit changed %q; the tracked link must stay", names)
		}
	}
}

func TestCheckSafe(t *testing.T) {
	refused := [][]string{
		{"checkout", "x"}, {"reset", "--hard"}, {"stash"}, {"clean", "-fd"}, {"push"},
		{"-c", "core.hooksPath=/tmp/h", "commit-tree", "x"}, {"-c", "core.sshCommand=x", "status"},
		{"-C", "/", "status"}, {"worktree", "remove", "--force", "x"}, {"worktree", "add", "x", "HEAD"},
		{"apply", "--index", "p"}, {"apply", "--3way", "p"}, {"add", "-A"}, {"commit", "-m", "x"},
		{"read-tree", "HEAD"}, {"diff", "--output=x"}, {"diff", "--ext-diff"},
	}
	for _, a := range refused {
		if err := gitx.CheckSafe(gitx.ExecOptions{}, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if err := gitx.CheckSafe(gitx.ExecOptions{Owned: true}, "commit", "-a", "-m", "x"); err == nil {
		t.Error("commit -a accepted")
	}
	ok := [][]string{{"-c", "user.name=S", "commit-tree", "t", "-m", "x"}, {"worktree", "add", "--detach", "x", "c"},
		{"apply", "--check", "p"}, {"diff", "--numstat", "a", "b", "--", "--force"}}
	for _, a := range ok {
		if err := gitx.CheckSafe(gitx.ExecOptions{}, a...); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	idx := gitx.ExecOptions{Env: []string{"GIT_INDEX_FILE=/tmp/i"}}
	if gitx.CheckSafe(idx, "read-tree", "HEAD") != nil || gitx.CheckSafe(idx, "add", "-A") != nil ||
		gitx.CheckSafe(idx, "commit", "-m", "x") == nil {
		t.Error("temporary index rules")
	}
}
