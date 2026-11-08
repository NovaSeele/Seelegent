package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// dirLink makes link point at the folder target the way a contest variant's shared folder is linked: a junction on
// Windows (what Setup and a worker's tools make without privileges, and what git walks into), a symbolic link
// elsewhere.
func dirLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	out, err := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, out)
	}
}

func treeFiles(t *testing.T, dir, tree string) []string {
	t.Helper()
	return strings.Fields(git(t, dir, "ls-tree", "-r", "--name-only", tree))
}

// D21: the shared-folder exclude was never applied, because GIT_LITERAL_PATHSPECS=1 (on for every git call) turns
// the ":(exclude)" magic into a plain file name. A worker that stops ignoring the shared folder then had its link's
// contents in the variant's tree, its diff and its pick. The exclude now holds whatever .gitignore says, and stays
// literal: a glob character in the excluded path matches nothing else.
func TestSnapshotTreeExcludesSharedFolder(t *testing.T) {
	dir := newRepo(t)
	write(t, filepath.Join(dir, ".gitignore"), "deps\nmods\\[1]\n") // escaped: a literal name
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "ignore the shared folders")
	shared := t.TempDir()
	write(t, filepath.Join(shared, "lib.txt"), "lib\n")
	dirLink(t, shared, filepath.Join(dir, "deps"))
	// A second shared folder whose name is a glob that would also match the real folder mods1.
	dirLink(t, shared, filepath.Join(dir, "mods[1]"))
	write(t, filepath.Join(dir, "mods1", "own.txt"), "the worker's own\n")
	write(t, filepath.Join(dir, "new.txt"), "new\n")
	r := New(nil, nil)

	snap := func(name string) []string {
		t.Helper()
		tree, err := r.SnapshotTree(t.Context(), dir, t.TempDir(), "deps", "mods[1]")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return treeFiles(t, dir, tree)
	}
	check := func(name string, files []string) {
		t.Helper()
		for _, f := range files {
			if f == "deps" || strings.HasPrefix(f, "deps/") || strings.HasPrefix(f, "mods[1]") {
				t.Errorf("%s: the shared folder is in the tree: %s (%v)", name, f, files)
			}
		}
		for _, want := range []string{"new.txt", "mods1/own.txt", "a.txt", ".gitignore"} {
			if !slices.Contains(files, want) {
				t.Errorf("%s: %s is missing from %v", name, want, files)
			}
		}
	}

	// Still ignored: nothing to exclude, and the add must not fail on an ignored excluded path.
	check("ignored", snap("ignored"))
	// The worker empties .gitignore: the links are no longer ignored and only the exclude keeps them out.
	write(t, filepath.Join(dir, ".gitignore"), "")
	check("not ignored", snap("not ignored"))
	// The repository's own index and files are left alone.
	if got := git(t, dir, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("the index changed: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(shared, "lib.txt")); err != nil || string(got) != "lib\n" {
		t.Errorf("the shared folder changed: %q %v", got, err)
	}
}
