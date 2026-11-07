package gitx

import "testing"

func TestSplitDiff(t *testing.T) {
	text := "diff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n x\n-old\n+new\n" +
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n" +
		"diff --git a/old.md b/new.md\nsimilarity index 90%\nrename from old.md\nrename to new.md\n" +
		"diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n" +
		"diff --git a/c.go b/c.go\n--- a/c.go\n+++ b/c.go\n@@ -1 +1,2 @@\n c\n+par"
	files := SplitDiff(text, true)
	if len(files) != 5 {
		t.Fatalf("files = %+v", files)
	}
	a := files[0]
	if a.Path != "a.go" || a.OldPath != "" || len(a.Hunks) != 1 || len(a.Hunks[0].Lines) != 3 || a.Truncated {
		t.Errorf("a = %+v", a)
	}
	if files[1].Path != "gone.txt" || len(files[1].Hunks) != 1 {
		t.Errorf("deleted = %+v", files[1])
	}
	if files[2].Path != "new.md" || files[2].OldPath != "old.md" {
		t.Errorf("renamed = %+v", files[2])
	}
	if files[3].Path != "img.png" || !files[3].Binary {
		t.Errorf("binary = %+v", files[3])
	}
	// Only the last file is cut, and its partial last line is dropped.
	if c := files[4]; c.Path != "c.go" || !c.Truncated || len(c.Hunks[0].Lines) != 1 {
		t.Errorf("truncated = %+v", c)
	}
	if got := SplitDiff("", false); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v", got)
	}
}
