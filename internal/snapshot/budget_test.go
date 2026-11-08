package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// setTime gives path a modification time of its own, so tests never depend on the file system's resolution.
func setTime(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// Large files are hashed on one read budget per snapshot (M18 review G3): past it a file is recorded by size and
// modification time, the manifest says the check was weaker, and a change of the same size is still caught by its
// modification time.
func TestHashBudgetFallsBackToSizeAndTime(t *testing.T) {
	root := newRepo(t)
	ctx := t.Context()
	s := New(t.TempDir(), gitx.New(nil, nil), nil)
	data := bytes.Repeat([]byte("0123456789abcdef"), (MaxFileBytes/16)+1)
	s.readBudget = int64(len(data)) + 1 // one large file fits, not two
	then := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, name := range []string{"a.bin", "b.bin"} {
		write(t, filepath.Join(root, name), string(data))
		setTime(t, filepath.Join(root, name), then)
	}
	start, err := s.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := start.Find("a.bin")
	b, _ := start.Find("b.bin")
	if a.Hash == "" || b.Hash != "" || b.ModTime == 0 || !start.HashBudgetSpent {
		t.Fatalf("start: a %+v, b %+v, spent %v", a, b, start.HashBudgetSpent)
	}
	if r := analyze(t, s, start, start); len(r) != 0 {
		t.Fatalf("unchanged: %+v", r)
	}
	// b.bin: same size, other bytes, a later modification time.
	data[100] = 'X'
	write(t, filepath.Join(root, "b.bin"), string(data))
	setTime(t, filepath.Join(root, "b.bin"), then.Add(time.Minute))
	end, err := s.Take(ctx, root, []string{"a.bin", "b.bin"})
	if err != nil {
		t.Fatal(err)
	}
	risks := analyze(t, s, start, end)
	if len(risks) != 1 || risks[0].Path != "b.bin" || !risks[0].Weak || risks[0].Why != WhyOutsideTools {
		t.Fatalf("risks: %+v", risks)
	}
}

// The files of extra share that read budget (M18 review G4): past it a small file is not read either, and is
// compared by size and modification time.
func TestExtraFilesShareTheReadBudget(t *testing.T) {
	root := newRepo(t)
	ctx := t.Context()
	s := New(t.TempDir(), gitx.New(nil, nil), nil)
	a := filepath.Join(root, "a.txt")
	then := time.Now().Add(-time.Hour).Truncate(time.Second)
	write(t, a, "one\ntwo\nthree\nmine\n")
	setTime(t, a, then)
	start, err := s.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.readBudget = 4
	end, err := s.Take(ctx, root, []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := end.Find("a.txt")
	if f.Stored || f.Hash != "" || f.ModTime == 0 || !end.HashBudgetSpent {
		t.Fatalf("a.txt past the budget: %+v, spent %v", f, end.HashBudgetSpent)
	}
	if r := analyze(t, s, start, end); len(r) != 0 {
		t.Fatalf("unchanged, by size and time: %+v", r)
	}
	write(t, a, "one\ntwo\nthree\nMINE\n")
	setTime(t, a, then.Add(time.Minute))
	end, err = s.Take(ctx, root, []string{"a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if r := analyze(t, s, start, end); len(r) != 1 || !r[0].Weak || r[0].Why != WhyOutsideTools {
		t.Fatalf("changed past the budget: %+v", r)
	}
}
