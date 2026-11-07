package gitx_test

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

func TestCheckSafePushAndSetup(t *testing.T) {
	push := gitx.ExecOptions{PushBranch: "seelegent/t1-fix"}
	ok := [][]string{
		{"push", "origin", "seelegent/t1-fix"},
		{"push", "-u", "--porcelain", "origin", "refs/heads/seelegent/t1-fix:refs/heads/seelegent/t1-fix"},
		{"ls-remote", "--heads", "origin", "seelegent/t1-fix"},
		{"remote", "get-url", "origin"}, {"remote", "get-url", "--push", "origin"},
		{"symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"},
		{"rev-list", "--count", "a..b"},
		{"worktree", "add", "-b", "seelegent/t1-fix", "dir", "abc123"},
		{"push", "--porcelain", "origin", "0123456789abcdef0123456789abcdef01234567:refs/heads/seelegent/t1-fix"},
		{"fetch", "-q", "--no-tags", "origin", "refs/heads/main"},
		{"fetch", "--quiet", "/tmp/origin.git", "refs/heads/main", "refs/heads/seelegent/t1-fix"},
		{"merge-base", "--is-ancestor", "a", "b"}, {"diff-tree", "-r", "-z", "--no-renames", "a", "b"},
	}
	for _, a := range ok {
		if err := gitx.CheckSafe(push, a...); err != nil {
			t.Errorf("%v: %v", a, err)
		}
	}
	refused := [][]string{
		{"push", "origin", "main"}, {"push", "--force", "origin", "seelegent/t1-fix"},
		{"push", "origin", "+seelegent/t1-fix"}, {"push", "--delete", "origin", "seelegent/t1-fix"},
		{"push", "--mirror", "origin"}, {"push", "--tags", "origin", "seelegent/t1-fix"},
		{"push", "upstream", "seelegent/t1-fix"}, {"push", "origin", "seelegent/t1-fix", "main"},
		{"push", "--receive-pack=x", "origin", "seelegent/t1-fix"},
		{"push", "origin", "refs/heads/main:refs/heads/seelegent/t1-fix"},
		{"push", "origin", ":seelegent/t1-fix"},
		{"ls-remote", "--upload-pack=x", "origin"}, {"ls-remote", "ext::sh -c x"},
		{"remote", "add", "origin", "--", "https://x"}, {"remote", "set-url", "origin", "x"},
		{"remote", "remove", "origin"}, {"init"}, {"symbolic-ref", "HEAD", "refs/heads/x"},
		{"symbolic-ref", "--delete", "HEAD"},
		{"worktree", "add", "-B", "seelegent/x", "dir"}, {"worktree", "add", "-b", "main", "dir"},
		{"worktree", "add", "-b", "seelegent/", "dir"}, {"worktree", "add", "-b", "seelegent/a..b", "dir"},
		{"worktree", "add", "--orphan", "-b", "seelegent/x", "dir"},
		{"commit", "--allow-empty", "-m", "x"},
		{"push", "origin", "0123456789abcdef0123456789abcdef01234567:refs/heads/main"},
		{"push", "origin", "HEAD:refs/heads/seelegent/t1-fix"},
		{"push", "origin", "0123456789abcdef:refs/heads/seelegent/t1-fix"},
		{"fetch", "origin"}, {"fetch", "origin", "main"}, {"fetch", "origin", "refs/heads/main:refs/heads/main"},
		{"fetch", "origin", "+refs/heads/main"}, {"fetch", "--force", "origin", "refs/heads/main"},
		{"fetch", "--upload-pack=x", "origin", "refs/heads/main"}, {"fetch", "ext::sh -c x", "refs/heads/main"},
		{"fetch", "--prune", "origin", "refs/heads/main"},
		{"diff-tree", "--ext-diff", "a", "b"},
	}
	for _, a := range refused {
		if err := gitx.CheckSafe(push, a...); err == nil {
			t.Errorf("%v accepted", a)
		}
	}
	if err := gitx.CheckSafe(gitx.ExecOptions{}, "push", "origin", "x"); err == nil {
		t.Error("push without PushBranch accepted")
	}
	setup := gitx.ExecOptions{Setup: true}
	for _, a := range [][]string{{"init"}, {"init", "-q", "-b", "main"}, {"remote", "add", "origin", "--", "https://h/o/r.git"},
		{"commit", "--allow-empty", "-m", "first"}} {
		if err := gitx.CheckSafe(setup, a...); err != nil {
			t.Errorf("setup %v: %v", a, err)
		}
	}
	for _, a := range [][]string{{"init", "--template=/x"}, {"init", "--separate-git-dir=/x"},
		{"remote", "add", "origin", "--", "ext::sh"}, {"remote", "add", "origin", "--", "--upload-pack=x"},
		{"commit", "-m", "x"}} {
		if err := gitx.CheckSafe(setup, a...); err == nil {
			t.Errorf("setup %v accepted", a)
		}
	}
}

func TestValidBranch(t *testing.T) {
	for _, b := range []string{"seelegent/t1-fix-login", "main", "feat/x_y.z"} {
		if !gitx.ValidBranch(b) {
			t.Errorf("%q refused", b)
		}
	}
	for _, b := range []string{"", "-x", "a..b", "a/", "a.lock", "a//b", "a@{1}", "a b", "a:b", "+a", "a~1", "a^", "a\\b"} {
		if gitx.ValidBranch(b) {
			t.Errorf("%q accepted", b)
		}
	}
}
