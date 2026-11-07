package gitx

import (
	"path/filepath"
	"testing"
)

func TestSnapshotReads(t *testing.T) {
	dir := newRepo(t)
	r := New(nil, nil)
	ctx := t.Context()
	root, err := r.Root(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	write(t, filepath.Join(dir, "new.txt"), "fresh\n")
	head, files, err := r.Dirty(ctx, root)
	if err != nil || len(head) != 40 || len(files) != 2 {
		t.Fatalf("dirty: %q %+v %v", head, files, err)
	}
	data, ok, err := r.Blob(ctx, root, head, "a.txt", 1<<20, false)
	if err != nil || !ok || string(data) != "one\ntwo\nthree\n" {
		t.Fatalf("blob: %q %v %v", data, ok, err)
	}
	if _, ok, err := r.Blob(ctx, root, head, "new.txt", 1<<20, false); ok || err != nil {
		t.Fatalf("blob of a file HEAD lacks: %v %v", ok, err)
	}
	if tracked, err := r.Tracked(ctx, root, ".TXT", 2); err != nil || len(tracked) != 2 {
		t.Fatalf("tracked: %v %v", tracked, err)
	}
	for _, tc := range []struct {
		name                string
		before, after       string
		beforeMiss, afterMs bool
		adds, dels          int
	}{
		{"modified", "a\nb\n", "a\nc\n", false, false, 1, 1},
		{"added", "", "x\ny\n", true, false, 2, 0},
		{"deleted", "x\n", "", false, true, 0, 1},
	} {
		d, err := r.DiffContent(ctx, "f.txt", []byte(tc.before), []byte(tc.after), tc.beforeMiss, tc.afterMs)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		adds, dels := 0, 0
		for _, h := range d.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case "add":
					adds++
				case "del":
					dels++
				}
			}
		}
		if d.Path != "f.txt" || adds != tc.adds || dels != tc.dels {
			t.Fatalf("%s: %+v", tc.name, d)
		}
	}
	if d, err := r.DiffContent(ctx, "b.bin", []byte("a\x00b"), []byte("c"), false, false); err != nil || !d.Binary {
		t.Fatalf("binary: %+v %v", d, err)
	}
}
