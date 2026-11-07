//go:build windows

package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// TestScrubLocalShortNames is the review's G4: a tool that prints the 8.3 short form of the home folder or the
// worktree (C:\Users\NOVASE~1) is scrubbed like the long form.
func TestScrubLocalShortNames(t *testing.T) {
	// New folders often get no short name (creation is off on many volumes), but a parent of the temp folder may
	// have one (C:\Users\NOVASE~1\AppData\Local\Temp).
	home, err := fsutil.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	short := func(p string) string {
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
	sh, sr := short(home), short(repo)
	if strings.EqualFold(sh, home) {
		t.Skip("no folder above the temp folder has an 8.3 short name")
	}
	text := "built " + sr + `\src\a.go and wrote ` + sh + `\notes.txt`
	if got := scrubLocal(text, []string{repo}, home); got != `built src\a.go and wrote ~\notes.txt` {
		t.Fatalf("short names: %q", got)
	}
}
