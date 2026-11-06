package account

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestProfileLinksAndSafeRemove is the guard for the most dangerous file operation in the package: removing a
// profile whose home links into the user's real home must delete the links, never the real files behind them.
func TestProfileLinksAndSafeRemove(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	writeFile(t, filepath.Join(f.realHome, ".gitconfig"), "[user]\n")
	writeFile(t, filepath.Join(f.realHome, ".ssh", "id_test"), "secret")
	writeFile(t, filepath.Join(f.realHome, ".config", "gh", "hosts.yml"), "hosts")
	writeFile(t, filepath.Join(f.realHome, ".config", "other"), "other")

	links := []string{".gitconfig", ".ssh", ".config/gh", ".missing", "../escape", "", "/abs"}
	a, warnings, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindIsolated}, links)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 3 {
		t.Errorf("warnings = %q, want three (escape, empty, absolute)", warnings)
	}
	prof, err := f.m.Profile(a)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Home != filepath.Join(f.profiles, a.ID, "home") || prof.Dir != filepath.Join(f.profiles, a.ID) ||
		!prof.Isolated() || prof.RealHome != f.realHome {
		t.Fatalf("profile = %+v", prof)
	}
	if got := readFile(t, filepath.Join(prof.Home, ".ssh", "id_test")); got != "secret" {
		t.Errorf("linked .ssh reads %q", got)
	}
	if got := readFile(t, filepath.Join(prof.Home, ".config", "gh", "hosts.yml")); got != "hosts" {
		t.Errorf("linked .config/gh reads %q", got)
	}
	if got := readFile(t, filepath.Join(prof.Home, ".gitconfig")); got != "[user]\n" {
		t.Errorf("linked .gitconfig reads %q", got)
	}
	if _, err := os.Lstat(filepath.Join(prof.Home, ".missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".missing was linked: %v", err)
	}

	// Ensure again: existing links are left alone, no warnings for them.
	_, warnings, err = f.m.EnsureProfile(a, []string{".gitconfig", ".ssh"})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("second Ensure = %q, %v", warnings, err)
	}

	// A read-only file the CLI left behind must not stop the removal.
	ro := filepath.Join(prof.Home, "ro.txt")
	writeFile(t, ro, "x")
	if err := os.Chmod(ro, 0o400); err != nil {
		t.Fatal(err)
	}

	if err := f.m.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(prof.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile still there: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(f.realHome, ".gitconfig"):                 "[user]\n",
		filepath.Join(f.realHome, ".ssh", "id_test"):            "secret",
		filepath.Join(f.realHome, ".config", "gh", "hosts.yml"): "hosts",
		filepath.Join(f.realHome, ".config", "other"):           "other",
	} {
		if got := readFile(t, path); got != want {
			t.Errorf("%s = %q after removing the profile, want %q", path, got, want)
		}
	}
	if _, err := f.m.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("record left: %v", err)
	}
}

func TestProfilesEdgeCases(t *testing.T) {
	var none Profiles
	if _, err := none.Dir("acc1"); err == nil {
		t.Error("Dir without root succeeded")
	}
	f := newFixture(t)
	p := f.m.Profiles()
	if _, err := p.For(Account{ID: "../x", Kind: KindIsolated}); !errors.Is(err, ErrInvalid) {
		t.Errorf("For(bad id) = %v", err)
	}
	if p.Exists("../x") || p.Exists("acc1") {
		t.Error("Exists is wrong")
	}
	if err := p.Remove("acc7"); err != nil {
		t.Errorf("removing a missing profile: %v", err)
	}
	if err := p.Remove("../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Remove(bad id) = %v", err)
	}
	// Without a real home nothing is linked.
	noHome := NewProfiles(f.profiles, "")
	prof, warnings, err := noHome.Ensure(Account{ID: "acc5", Kind: KindIsolated}, []string{".ssh"})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Ensure = %v, %v", warnings, err)
	}
	if st, err := os.Stat(prof.Home); err != nil || !st.IsDir() {
		t.Errorf("home not created: %v", err)
	}
	// A profiles "directory" that is a file makes Ensure fail and Add roll the record back.
	blocker := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocker, "")
	bad := NewManager(f.db, NewProfiles(blocker, f.realHome), nil)
	if _, _, err := bad.Add(ctxT(t), New{Backend: "agy", Kind: KindIsolated}, nil); err == nil {
		t.Fatal("Add under a file succeeded")
	}
	list, err := bad.List(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("record not rolled back: %+v", list)
	}
}

func TestCleanLinkEntry(t *testing.T) {
	for in, want := range map[string]string{
		".ssh":       ".ssh",
		".config/gh": filepath.Join(".config", "gh"),
		"./a/../b":   "b",
		"go":         "go",
		"a/./b/":     filepath.Join("a", "b"),
	} {
		if got, ok := cleanLinkEntry(in); !ok || got != want {
			t.Errorf("cleanLinkEntry(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", " ", "/etc", `\x`, "..", "../a", "a/../..", "."} {
		if got, ok := cleanLinkEntry(in); ok {
			t.Errorf("cleanLinkEntry(%q) = %q, accepted", in, got)
		}
	}
}
