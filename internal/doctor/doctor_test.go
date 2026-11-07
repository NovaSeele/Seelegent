package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

func TestSmallChecks(t *testing.T) {
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.ListSeparator)
	if c := pathEntries([]string{"PATH=" + abs + sep + "bin" + sep + "." + sep}); c.Status != Warn ||
		!strings.Contains(c.Message, "bin, .") {
		t.Errorf("relative PATH = %+v", c)
	}
	if c := pathEntries([]string{"Path=" + abs}); c.Status != OK {
		t.Errorf("absolute PATH = %+v", c)
	}
	if c := dashboard(fstest.MapFS{"index.html": {Data: []byte("x")}}); c.Status != OK {
		t.Errorf("dashboard = %+v", c)
	}
	if c := dashboard(nil); c.Status != Warn {
		t.Errorf("no dashboard = %+v", c)
	}
	if c := git(func() (string, error) { return "", errors.New("not found") }); c.Status != Fail {
		t.Errorf("no git = %+v", c)
	}
	if !Failed([]Check{{Status: OK}, {Status: Fail}}) || Failed([]Check{{Status: Warn}}) {
		t.Error("Failed")
	}
}

func TestDataDir(t *testing.T) {
	p, err := config.NewPaths(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, false); c.Status != Warn {
		t.Errorf("missing = %+v", c)
	}
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, false); c.Status != OK {
		t.Errorf("writable = %+v", c)
	}
	entries, err := os.ReadDir(p.Home)
	if err != nil || len(entries) != 0 {
		t.Errorf("the probe was left behind: %v %v", entries, err)
	}
	// While its daemon runs nothing is written: the folder's modification time stays.
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p.Home, old, old); err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p, true); c.Status != OK || !strings.Contains(c.Message, "in use by its daemon") {
		t.Errorf("live = %+v", c)
	}
	if st, err := os.Stat(p.Home); err != nil || !st.ModTime().Equal(old) {
		t.Errorf("the live data directory changed: %v %v", st.ModTime(), err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p2, err := config.NewPaths(file)
	if err != nil {
		t.Fatal(err)
	}
	if c := dataDir(p2, false); c.Status != Fail {
		t.Errorf("a file = %+v", c)
	}
}
