package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalMissingTail(t *testing.T) {
	dir := t.TempDir()
	base, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(filepath.Join(dir, "not", "yet", "there.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "not", "yet", "there.txt")
	if got != want {
		t.Fatalf("Canonical = %q, want %q", got, want)
	}
}

func TestCanonicalRelativeAndDots(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir("sub", 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(filepath.Join("sub", "..", "sub", "."))
	if err != nil {
		t.Fatal(err)
	}
	want, err := Canonical(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !filepath.IsAbs(got) {
		t.Fatalf("Canonical = %q, want %q", got, want)
	}
}

func TestEmptyPathErrors(t *testing.T) {
	calls := map[string]func() error{
		"Canonical":     func() error { _, err := Canonical(""); return err },
		"Key":           func() error { _, err := Key(""); return err },
		"SamePath a":    func() error { _, err := SamePath("", "x"); return err },
		"SamePath b":    func() error { _, err := SamePath("x", ""); return err },
		"Within root":   func() error { _, err := Within("", "x"); return err },
		"Within target": func() error { _, err := Within("x", ""); return err },
	}
	for name, call := range calls {
		if call() == nil {
			t.Errorf("%s(\"\"): want an error", name)
		}
	}
}

func TestSymlinkResolved(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	same, err := SamePath(filepath.Join(link, "file.go"), filepath.Join(target, "file.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("a path through a symlink must have the same key as the target path")
	}
}

func TestSamePathCase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Repo")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	same, err := SamePath(dir, filepath.Join(filepath.Dir(dir), "REPO"))
	if err != nil {
		t.Fatal(err)
	}
	if want := caseInsensitive(runtime.GOOS); same != want {
		t.Fatalf("SamePath(Repo, REPO) = %v, want %v on %s", same, want, runtime.GOOS)
	}
}

func TestWithin(t *testing.T) {
	dir := t.TempDir()
	in, err := Within(dir, filepath.Join(dir, "a", "b"))
	if err != nil || !in {
		t.Fatalf("Within(dir, dir/a/b) = %v, %v", in, err)
	}
	out, err := Within(filepath.Join(dir, "ui"), filepath.Join(dir, "ui-old", "x"))
	if err != nil || out {
		t.Fatalf("Within(ui, ui-old/x) = %v, %v", out, err)
	}
}

func TestWithinKey(t *testing.T) {
	tests := []struct {
		root, p string
		want    bool
	}{
		{"/data/ui", "/data/ui", true},
		{"/data/ui", "/data/ui/index.html", true},
		{"/data/ui", "/data/ui-old/index.html", false},
		{"/data/ui", "/data", false},
		{"/", "/etc", true},
		{"c:/", "c:/users", true},
	}
	for _, tt := range tests {
		if got := withinKey(tt.root, tt.p); got != tt.want {
			t.Errorf("withinKey(%q, %q) = %v, want %v", tt.root, tt.p, got, tt.want)
		}
	}
}

func TestKeyOf(t *testing.T) {
	tests := []struct {
		canonical, goos, want string
	}{
		{`C:\Users\JohnSmith\Repo`, "windows", "c:/users/johnsmith/repo"},
		{"/Users/Nova/Repo", "darwin", "/users/nova/repo"},
		{"/home/Nova/Repo", "linux", "/home/Nova/Repo"},
	}
	for _, tt := range tests {
		if got := keyOf(tt.canonical, tt.goos); got != tt.want {
			t.Errorf("keyOf(%q, %s) = %q, want %q", tt.canonical, tt.goos, got, tt.want)
		}
	}
}

func TestKeyIsStableForCanonicalInput(t *testing.T) {
	dir := t.TempDir()
	c, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	k1, err := Key(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := Key(c)
	if err != nil {
		t.Fatal(err)
	}
	if k1 != k2 || strings.Contains(k1, `\`) {
		t.Fatalf("Key(dir) = %q, Key(canonical) = %q", k1, k2)
	}
}
