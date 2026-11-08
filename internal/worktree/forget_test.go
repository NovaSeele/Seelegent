package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// Removing a Seelegent worktree whose folder is gone forgets only that worktree: the user's own worktree whose
// folder is missing for a while (an unplugged drive) keeps its record, its index and its staged work (M18 hunt H6).
func TestRemoveGoneWorktreeKeepsUsersWorktrees(t *testing.T) {
	for _, op := range []string{"remove", "removeClean", "discard"} {
		t.Run(op, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			root := filepath.Join(dir, "repo")
			write(t, filepath.Join(root, "a.txt"), "one\n")
			git(t, root, "init", "-q")
			git(t, root, "add", "-A")
			git(t, root, "commit", "-q", "-m", "init")
			// The user's own worktree on a removable drive, with staged work; then the drive is unplugged.
			userWT := filepath.Join(dir, "usb", "feature-wt")
			git(t, root, "worktree", "add", "-q", "-b", "feature", userWT)
			write(t, filepath.Join(userWT, "staged.txt"), "staged work\n")
			git(t, userWT, "add", "staged.txt")
			moved := filepath.Join(dir, "unplugged")
			if err := os.Rename(filepath.Join(dir, "usb"), moved); err != nil {
				t.Fatal(err)
			}
			// One of Seelegent's own worktrees, whose folder is gone already.
			base := filepath.Join(dir, "data", "worktrees")
			trees := &Trees{Git: gitx.New(nil, nil), Base: base}
			scope := filepath.Join(base, "c1")
			ours := filepath.Join(scope, "v1")
			if _, err := trees.Add(ctx, root, ours, "HEAD"); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(ours); err != nil {
				t.Fatal(err)
			}
			var err error
			switch op {
			case "remove":
				_, err = trees.Remove(ctx, root, ours, nil, "seal")
			case "removeClean":
				err = trees.RemoveClean(ctx, root, ours, nil)
			default:
				err = trees.Discard(ctx, root, ours, scope, nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			list := git(t, root, "worktree", "list", "--porcelain")
			if strings.Contains(filepath.ToSlash(list), "c1/v1") {
				t.Errorf("our worktree is still known:\n%s", list)
			}
			if !strings.Contains(list, "feature") {
				t.Fatalf("the user's worktree was dropped:\n%s", list)
			}
			// Plug the drive back in: the staged file is still staged.
			if err := os.Rename(moved, filepath.Join(dir, "usb")); err != nil {
				t.Fatal(err)
			}
			if st := git(t, userWT, "status", "--short"); !strings.Contains(st, "A  staged.txt") {
				t.Errorf("status in the user's worktree: %q", st)
			}
		})
	}
}
