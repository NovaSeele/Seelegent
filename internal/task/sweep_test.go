package task

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSweepKeepsOldVersionTaskFolders: after an in-place migrate the old Node version's task folders sit in TasksDir
// with no row in the store. The sweep keeps them, and anything else it cannot prove is this version's, while an
// orphan with this version's layout still goes (final verification N1).
func TestSweepKeepsOldVersionTaskFolders(t *testing.T) {
	h := newHarness(t)
	tasks := filepath.Join(h.dir, "tasks")
	put := func(rel, text string) {
		t.Helper()
		p := filepath.Join(tasks, filepath.FromSlash(rel))
		if err := mkdir(filepath.Dir(p)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The old version's layout: <id>.json and <id>.jsonl next to <id>/ with the attempts' stream and stderr.
	put("t-old.json", `{"id":"t-old"}`)
	put("t-old.jsonl", "{}\n")
	put("t-old/a1-1.stream.jsonl", "{}\n")
	put("t-old/a1-1.stderr.log", "x\n")
	// An old folder whose task file is gone: its files are not this version's either.
	put("t-old-bare/a1-1.stream.jsonl", "{}\n")
	// This version's layout, but the old version's task file sits next to it.
	put("t-twin.jsonl", "{}\n")
	put("t-twin/t1-a1/launch/stream.jsonl", "{}\n")
	// This version's layout with something foreign in it.
	put("t-mixed/t1-a1/launch/stream.jsonl", "{}\n")
	put("t-mixed/notes.txt", "mine\n")
	// A real orphan of this version: attempt and console folders and gate logs, no row in the store.
	put("t-orphan/t1-a1/launch/stream.jsonl", "{}\n")
	put("t-orphan/t2-a3/prompt.txt", "p\n")
	put("t-orphan/console-1731000000000/spec.json", "{}\n")
	put("t-orphan/gate-t1-1.log", "ok\n")
	if err := mkdir(filepath.Join(tasks, "t-empty")); err != nil {
		t.Fatal(err)
	}

	h.s.sweep(t.Context())

	for _, rel := range []string{"t-old.json", "t-old.jsonl", "t-old/a1-1.stream.jsonl", "t-old/a1-1.stderr.log",
		"t-old-bare/a1-1.stream.jsonl", "t-twin/t1-a1/launch/stream.jsonl", "t-mixed/notes.txt",
		"t-mixed/t1-a1/launch/stream.jsonl"} {
		if _, err := os.Stat(filepath.Join(tasks, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was swept: %v", rel, err)
		}
	}
	for _, rel := range []string{"t-orphan", "t-empty"} {
		if _, err := os.Stat(filepath.Join(tasks, rel)); !os.IsNotExist(err) {
			t.Errorf("the orphan %s was kept: %v", rel, err)
		}
	}
}
