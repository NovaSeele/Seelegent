package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// A dirty file over MaxFileBytes that a shell command changed is at risk (M18 hunt H18): it is compared by a hash
// taken in a stream, its content never kept, and by size when it is too large even to hash.
func TestLargeFileChangeIsAtRisk(t *testing.T) {
	root := newRepo(t)
	ctx := t.Context()
	s := New(t.TempDir(), gitx.New(nil, nil), nil)
	big := filepath.Join(root, "data.bin")
	data := bytes.Repeat([]byte("0123456789abcdef"), (MaxFileBytes/16)+1)
	if err := os.WriteFile(big, data, 0o600); err != nil {
		t.Fatal(err)
	}
	start, err := s.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := start.Find("data.bin")
	if !ok || f.Stored || f.Hash == "" {
		t.Fatalf("large file in the start snapshot: %+v %v", f, ok)
	}
	// Same size, other bytes.
	data[100] = 'X'
	if err := os.WriteFile(big, data, 0o600); err != nil {
		t.Fatal(err)
	}
	end, err := s.Take(ctx, root, []string{"data.bin"})
	if err != nil {
		t.Fatal(err)
	}
	risks := analyze(t, s, start, end)
	if len(risks) != 1 || risks[0].Path != "data.bin" || risks[0].Why != WhyOutsideTools {
		t.Fatalf("risks: %+v", risks)
	}
	// Unchanged: nothing to say.
	if r := analyze(t, s, end, end); len(r) != 0 {
		t.Fatalf("unchanged large file: %+v", r)
	}
	// Edited by the worker's tools: its own business.
	if r := analyze(t, s, start, end, "data.bin"); len(r) != 0 {
		t.Fatalf("edited large file: %+v", r)
	}
}

// Without a hash (too large to hash, or an older manifest) a large file is compared by size.
func TestLargeFileWithoutHashComparedBySize(t *testing.T) {
	start := Manifest{Files: []File{{Path: "data.bin", Status: "modified", Size: 20 << 20}}}
	end := Manifest{Files: []File{{Path: "data.bin", Status: "modified", Size: 9 << 20}}}
	in := Input{Start: start, End: end, Edited: map[string]bool{}, Others: map[string]bool{},
		Content: func(File) ([]byte, error) { return nil, ErrNotFound }}
	if r := Analyze(in); len(r) != 1 || r[0].Why != WhyOutsideTools {
		t.Errorf("a large file that shrank outside the edit tools: %+v", r)
	}
	in.End = start
	if r := Analyze(in); len(r) != 0 {
		t.Errorf("same size: %+v", r)
	}
}

// A temp file left by a crash between WriteFileAtomic's write and its rename does not stop the sweep, and stale
// ones are removed, in task folders and in the blob store (M18 hunt H19).
func TestSweepSkipsAndRemovesLeftoverTempFiles(t *testing.T) {
	root := newRepo(t)
	clk := &clock{t: time.Now()}
	dir := t.TempDir()
	s := New(dir, gitx.New(nil, nil), clk.now)
	write(t, filepath.Join(root, "a.txt"), "changed\n")
	m, err := s.Take(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a-running", "z-old"} {
		if err := s.Save(id, 1, KindStart, m); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	staleManifest := filepath.Join(dir, "tasks", "a-running", ".t1-end.json.123456.tmp")
	freshManifest := filepath.Join(dir, "tasks", "a-running", ".t2-end.json.654321.tmp")
	staleBlob := filepath.Join(dir, "blobs", "ab", ".ab12.777.tmp")
	freshBlob := filepath.Join(dir, "blobs", "ab", ".ab34.888.tmp")
	for _, p := range []string{staleManifest, freshManifest, staleBlob, freshBlob} {
		write(t, p, `{"version":1,"fi`)
	}
	for _, p := range []string{staleManifest, staleBlob} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.Sweep(t.Context(), func(id string) bool { return id == "a-running" })
	if err != nil || n != 1 {
		t.Fatalf("sweep: removed %d, err %v", n, err)
	}
	if _, err := s.Load("z-old", 1, KindStart); err == nil {
		t.Error("the old task's snapshots are still there")
	}
	if _, err := s.Load("a-running", 1, KindStart); err != nil {
		t.Errorf("the kept task's snapshot: %v", err)
	}
	for p, want := range map[string]bool{staleManifest: false, staleBlob: false, freshManifest: true,
		freshBlob: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
