package projectkey

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestKeysStayTheSame pins the formula: project keys are stored in data folders (roles, project memory, proof
// ledgers, usage), so a key that changed would orphan what is kept under the old one.
func TestKeysStayTheSame(t *testing.T) {
	for _, c := range []struct{ pathKey, name, want string }{
		{"c:/users/johnsmith/src/seelegent", "Seelegent", "seelegent-52349cdb"},
		{"/home/ann/work/api server", "api server", "api-server-9ac90327"},
		{"/srv/***", "***", "project-4e4c84cb"},
	} {
		if got := keyOf(c.pathKey, c.name); got != c.want {
			t.Errorf("keyOf(%q, %q) = %q, want %q", c.pathKey, c.name, got, c.want)
		}
	}
}

func TestKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Project!")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key, name, err := Key(dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "My Project!" {
		t.Errorf("name = %q", name)
	}
	if !strings.HasPrefix(key, "my-project-") || len(key) != len("my-project-")+8 {
		t.Errorf("key = %q", key)
	}
	again, _, err := Key(dir + string(filepath.Separator) + ".")
	if err != nil {
		t.Fatal(err)
	}
	if again != key {
		t.Errorf("same folder, other spelling: %q != %q", again, key)
	}
	if runtime.GOOS == "windows" {
		upper, _, err := Key(strings.ToUpper(dir))
		if err != nil {
			t.Fatal(err)
		}
		if upper != key {
			t.Errorf("letter case changed the key: %q != %q", upper, key)
		}
	}
	other, _, err := Key(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other == key {
		t.Error("two folders share a key")
	}
	if safeName("***") != "project" || safeName("a  b__c") != "a-b__c" {
		t.Errorf("safeName: %q %q", safeName("***"), safeName("a  b__c"))
	}
}

func TestOf(t *testing.T) {
	dir := t.TempDir()
	canonical, key, name, err := Of(dir)
	if err != nil || canonical == "" || name != filepath.Base(canonical) {
		t.Fatalf("Of = %q, %q, %q, %v", canonical, key, name, err)
	}
	if want, _, _ := Key(dir); key != want {
		t.Fatalf("key %q, want %q", key, want)
	}
	for _, bad := range []string{"", "rel", filepath.Join(dir, "missing")} {
		if _, _, _, err := Of(bad); err == nil {
			t.Errorf("Of(%q) succeeded", bad)
		}
	}
}
