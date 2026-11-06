package web

import (
	"io/fs"
	"testing"
)

func TestFS(t *testing.T) {
	fsys := FS()
	if fsys == nil {
		t.Fatal("FS() = nil")
	}
	if _, err := fs.Stat(fsys, ".gitkeep"); err != nil {
		t.Errorf("FS() lacks .gitkeep at its root: %v", err)
	}
	// After `pnpm build` the root holds the dashboard; it must be served from the root, not from dist/.
	if _, err := fs.Stat(fsys, "index.html"); err == nil {
		if _, err := fs.ReadFile(fsys, "index.html"); err != nil {
			t.Errorf("read index.html: %v", err)
		}
	}
	if _, err := fs.Stat(fsys, "dist"); err == nil {
		t.Error("FS() root still contains dist/; want the dist sub-tree")
	}
}
