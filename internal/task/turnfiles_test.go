package task

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// A turn's Files are what its snapshots show it changed: files made through the shell count, a file made and
// removed within the turn does not, a tracked file it deleted does, and an edit-tool path that is gone does not.
// The turn_end event, the task view and the report agree.
func TestTurnFilesComeFromTheSnapshots(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "a\n", "gone.txt": "bye\n", "keep.txt": "k\n"})
	h.agy("w", 1)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-f"),
		agyEdit(1, filepath.Join(cwd, "a.txt")),
		fakeWrite(filepath.Join(cwd, "a.txt"), "a\nmore\n"),
		agyTool(2, "DONE", "npm init -y; mkdir src; echo x > src/main.ts"),
		fakeWrite(filepath.Join(cwd, "package.json"), "{}\n"),
		fakeWrite(filepath.Join(cwd, "src", "main.ts"), "x\n"),
		fakeWrite(filepath.Join(cwd, ".scratch", "check.mjs"), "probe\n"),
		"#!remove "+filepath.Join(cwd, ".scratch", "check.mjs"),
		"#!remove "+filepath.Join(cwd, "gone.txt"),
		agyEdit(3, filepath.Join(cwd, "tmp-edit.txt")), // edited, then removed by the shell
		agyResult("conv-f", "Changed a.txt, package.json and src/main.ts; removed gone.txt.")))
	sum := h.delegate(api.DelegateRequest{Title: "files"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Snap == nil || !tu.Snap.Checked {
		t.Fatalf("snapshots: %+v", tu.Snap)
	}
	want := []string{"a.txt", "gone.txt", "package.json", "src/main.ts"}
	if !slices.Equal(tu.Files, want) {
		t.Fatalf("turn files %v, want %v", tu.Files, want)
	}
	ends := eventsOf(h.events(sum.ID), api.EventTurnEnd)
	if len(ends) != 1 {
		t.Fatalf("turn_end events: %d", len(ends))
	}
	var end api.TurnEndData
	decode(t, ends[0], &end)
	if !slices.Equal(end.Files, want) {
		t.Fatalf("turn_end files %v, want %v", end.Files, want)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.FilesChanged, want) {
		t.Fatalf("report files %v, want %v", rep.FilesChanged, want)
	}
	if v, err := h.s.Get(t.Context(), sum.ID); err != nil || !slices.Equal(v.Turns[0].Files, want) {
		t.Fatalf("task view: %v %+v", err, v.Turns)
	}
}

// Without snapshots (not a repository) a turn keeps its edit-tool list, less the files that no longer exist.
func TestTurnFilesWithoutSnapshots(t *testing.T) {
	h := newHarness(t)
	h.agy("w", 1)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-n"),
		agyEdit(1, filepath.Join(cwd, "b.txt")),
		fakeWrite(filepath.Join(cwd, "b.txt"), "b\n"),
		agyEdit(2, filepath.Join(cwd, "a.txt")),
		fakeWrite(filepath.Join(cwd, "a.txt"), "a\n"),
		agyEdit(3, filepath.Join(cwd, "never.txt")),
		fakeWrite(filepath.Join(cwd, "package.json"), "{}\n"),
		agyResult("conv-n", "done")))
	sum := h.delegate(api.DelegateRequest{Title: "plain"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Snap != nil && tu.Snap.Checked {
		t.Fatalf("a folder without git has no snapshots: %+v", tu.Snap)
	}
	if want := []string{"a.txt", "b.txt"}; !slices.Equal(tu.Files, want) {
		t.Fatalf("turn files %v, want %v", tu.Files, want)
	}
}

// A truncated snapshot may miss files: the edit-tool list is added, and deleted files are left out.
func TestCheckedFilesTruncated(t *testing.T) {
	c := &turnCheck{files: []string{"a.txt", "gone.txt", "z.txt"}, deleted: map[string]bool{"gone.txt": true}}
	if got := checkedFiles(&TurnSnap{}, c, []string{"b.txt"}); !slices.Equal(got, c.files) {
		t.Fatalf("whole snapshots: %v", got)
	}
	c.truncated = true
	if got := checkedFiles(&TurnSnap{}, c, []string{"b.txt", "z.txt"}); !slices.Equal(got,
		[]string{"a.txt", "b.txt", "z.txt"}) {
		t.Fatalf("truncated end: %v", got)
	}
	c.truncated = false
	if got := checkedFiles(&TurnSnap{Truncated: true}, c, nil); !slices.Equal(got, []string{"a.txt", "z.txt"}) {
		t.Fatalf("truncated start: %v", got)
	}
}

// snapshotFiles keeps tracked deletions only, and shows paths relative to the task's folder.
func TestSnapshotFilesDisplay(t *testing.T) {
	root := t.TempDir()
	end := snapshot.Manifest{Files: []snapshot.File{
		{Path: "web/src/a.ts", Status: api.GitModified},
		{Path: "web/old.ts", Status: api.GitDeleted, Missing: true},
		{Path: "web/draft.txt", Missing: true},
		{Path: "go.mod", Status: api.GitModified},
	}}
	files, deleted := snapshotFiles(filepath.Join(root, "web"), root,
		end, []string{"go.mod", "web/draft.txt", "web/old.ts", "web/src/a.ts"})
	want := []string{filepath.ToSlash(filepath.Join(root, "go.mod")), "old.ts", "src/a.ts"}
	slices.Sort(want)
	if !slices.Equal(files, want) || !deleted["old.ts"] || len(deleted) != 1 {
		t.Fatalf("files %v deleted %v, want %v", files, deleted, want)
	}
}

// A repository with no commit yet (field report: `git init`, then a worker built the app): the snapshots, the
// post-turn checks with hygiene, and the turn's files all work while HEAD is unborn.
func TestTurnChecksOnUnbornHead(t *testing.T) {
	h := newHarness(t)
	gitIn(t, h.cwd, "init", "-q", "-b", "main")
	gitIn(t, h.cwd, "config", "core.autocrlf", "false")
	h.write("README.md", "draft\n") // untracked before the turn
	h.agy("w", 1)
	cwd := h.cwd
	h.queue.Push(t, script(agyInit("conv-u"),
		agyEdit(1, filepath.Join(cwd, "src", "App.vue")),
		fakeWrite(filepath.Join(cwd, "src", "App.vue"), "<template/>\n"),
		fakeWrite(filepath.Join(cwd, "src", "pages", "About.vue"), "\uFEFF<template/>\n"),
		fakeWrite(filepath.Join(cwd, "package.json"), "{}\n"),
		fakeWrite(filepath.Join(cwd, "notes.orig"), "x\n"),
		fakeWrite(filepath.Join(cwd, "README.md"), "draft\r\nmore\r\n"),
		agyResult("conv-u", "Added src/App.vue and `src/pages/`.")))
	sum := h.delegate(api.DelegateRequest{Title: "unborn"})
	tk := h.waitStatus(sum.ID, StatusDone)
	tu := tk.LastTurn()
	if tu.Snap == nil || !tu.Snap.Checked || tu.Snap.Head != "" {
		t.Fatalf("snapshots: %+v", tu.Snap)
	}
	if want := []string{"README.md", "notes.orig", "package.json", "src/App.vue", "src/pages/About.vue"}; !slices.Equal(
		tu.Files, want) {
		t.Fatalf("turn files %v, want %v", tu.Files, want)
	}
	kinds := map[string]string{}
	for _, f := range tu.Hygiene {
		kinds[f.Path] = f.Kind
	}
	// No tracked file says whether the repository uses a BOM; README.md was untracked and dirty before the turn.
	if kinds["notes.orig"] != api.FindingJunkFile || kinds["package.json"] != api.FindingUnreported ||
		len(kinds) != 2 {
		t.Fatalf("hygiene: %+v", tu.Hygiene)
	}
	if len(tu.WorkAtRisk) != 0 {
		t.Fatalf("work at risk: %+v", tu.WorkAtRisk)
	}
}
