//go:build windows

package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func shortPath(t *testing.T, p string) string {
	t.Helper()
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(name, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	return windows.UTF16ToString(buf[:n])
}

// Bug B11 of the old version: project memory keyed on C:\Users\NOVASE~1\... never matched the long path of the
// task cwd, so the notes were never injected into briefs.
func TestKeyExpandsShortNames(t *testing.T) {
	long := filepath.Join(t.TempDir(), "Long Directory Name")
	if err := os.Mkdir(long, 0o700); err != nil {
		t.Fatal(err)
	}
	short := shortPath(t, long)
	if strings.EqualFold(short, long) {
		t.Skip("8.3 short names are disabled on this volume")
	}
	canon, err := Canonical(short)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(canon, "~") || !strings.HasSuffix(canon, "Long Directory Name") {
		t.Fatalf("Canonical(%q) = %q, want the long form", short, canon)
	}
	tests := []struct{ a, b string }{
		{short, long},
		{filepath.Join(short, "missing.txt"), filepath.Join(long, "missing.txt")},
		{strings.ToLower(short), strings.ToUpper(long)},
	}
	for _, tt := range tests {
		same, err := SamePath(tt.a, tt.b)
		if err != nil {
			t.Fatal(err)
		}
		if !same {
			t.Errorf("SamePath(%q, %q) = false, want true", tt.a, tt.b)
		}
	}
}

func TestCanonicalRestoresCase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "MixedCase")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	want, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(strings.ToLower(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got != want || !strings.HasSuffix(got, "MixedCase") {
		t.Fatalf("Canonical(lower) = %q, want %q", got, want)
	}
}

func TestStripExtendedPrefix(t *testing.T) {
	tests := map[string]string{
		`\\?\C:\Users\x`:           `C:\Users\x`,
		`\\?\UNC\server\share\x`:   `\\server\share\x`,
		`C:\plain`:                 `C:\plain`,
		`\\server\share\no-prefix`: `\\server\share\no-prefix`,
	}
	for in, want := range tests {
		if got := stripExtendedPrefix(in); got != want {
			t.Errorf("stripExtendedPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveExistingFallbacks(t *testing.T) {
	bogus := `C:\definitely\not\here\seelegent-test`
	if got := resolveExisting(bogus); got != bogus {
		t.Fatalf("resolveExisting(%q) = %q, want it unchanged", bogus, got)
	}
	if _, err := finalPath("bad\x00name"); err == nil {
		t.Fatal("finalPath: want an error for a NUL in the name")
	}
	if _, err := longPath("bad\x00name"); err == nil {
		t.Fatal("longPath: want an error for a NUL in the name")
	}
	if _, err := longPath(bogus); err == nil {
		t.Fatal("longPath: want an error for a missing path")
	}
	long, err := longPath(shortPath(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(long, "~") {
		t.Fatalf("longPath kept a short name: %q", long)
	}
}
