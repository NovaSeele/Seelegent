package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// openWorktreeImage itself, below gitx.CleanPath's checks (a path swapped to a link after them): os.Root refuses a
// Windows junction (mklink /J) or an absolute symbolic link that leads out of the repository, and also one that
// points back inside, since its target is absolute.
func TestOpenWorktreeImageRefusesEscapingLinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	outside := t.TempDir()
	for path, data := range map[string]string{
		filepath.Join(root, "art", "in.png"): "inside",
		filepath.Join(outside, "secret.png"): "secret",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if runtime.GOOS != "windows" {
			if err := os.Symlink(target, name); err != nil {
				t.Fatal(err)
			}
			return
		}
		if out, err := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", name, target).
			CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v\n%s", err, out)
		}
	}
	link(outside, filepath.Join(root, "outer"))
	link(filepath.Join(root, "art"), filepath.Join(root, "inner"))

	f, size, err := openWorktreeImage(root, "art/in.png")
	if err != nil || size != int64(len("inside")) {
		t.Fatalf("a plain file: size %d, %v", size, err)
	}
	_ = f.Close()
	for _, rel := range []string{"outer/secret.png", "inner/in.png"} {
		f, _, err := openWorktreeImage(root, rel)
		if err == nil {
			_ = f.Close()
			t.Fatalf("%s: opened through a link with an absolute target", rel)
		}
		if !strings.Contains(err.Error(), "escapes") {
			t.Fatalf("%s: %v", rel, err)
		}
	}
}
