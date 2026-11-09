package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/launchbin"
)

// The companion launcher of a fake release is the text "seelegent-launch <tag>"; fakeCheckCompanion accepts it next
// to the executable "seelegent <tag>" of the same tag.
func fakeCheckCompanion(exe, companion string) error {
	e, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	c, err := os.ReadFile(companion)
	if err != nil {
		return err
	}
	if strings.TrimPrefix(string(e), "seelegent ") != strings.TrimPrefix(string(c), "seelegent-launch ") {
		return fmt.Errorf("%q is not the launcher of %q", c, e)
	}
	return nil
}

// withCompanion rebuilds the archive of release tag with the companion launcher content next to the executable
// (one folder down outside Windows, as goreleaser packs them).
func withCompanion(t *testing.T, e *env, tag, content string) {
	t.Helper()
	r := e.server.releases[tag]
	dir := ""
	if e.goos != "windows" {
		dir = "seelegent_" + tag + "/"
	}
	files := []struct{ name, data string }{
		{dir + "README.md", "readme"},
		{dir + binaryName(e.goos), "seelegent " + tag},
		{dir + launchbin.FileName(e.goos), content},
	}
	var buf bytes.Buffer
	if e.goos == "windows" {
		zw := zip.NewWriter(&buf)
		for _, f := range files {
			w, err := zw.Create(f.name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(f.data))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, f := range files {
			if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)),
				Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			_, _ = tw.Write([]byte(f.data))
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	r.archive = buf.Bytes()
	sum := sha256.Sum256(r.archive)
	r.sums = hex.EncodeToString(sum[:]) + "  " + r.name + "\n"
}

func companionUpdater(t *testing.T, e *env, current string) *Updater {
	t.Helper()
	u := e.updater(t, current)
	u.o.CheckCompanion = fakeCheckCompanion
	return u
}

// companionOf is the companion launcher next to the env's executable.
func companionOf(e *env) string {
	return filepath.Join(filepath.Dir(e.exe), launchbin.FileName(e.goos))
}

// files lists what is next to the executable, with the content of each file.
func files(t *testing.T, e *env) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(e.exe))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, en := range entries {
		out[en.Name()] = readFile(t, filepath.Join(filepath.Dir(e.exe), en.Name()))
	}
	return out
}

func wantFiles(t *testing.T, e *env, want map[string]string) {
	t.Helper()
	got := files(t, e)
	for name, data := range want {
		if got[name] != data {
			t.Errorf("%s = %q, want %q", name, got[name], data)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("leftover %s = %q", name, got[name])
		}
	}
}

// TestApplyAndRollbackWithCompanion: the companion launcher is replaced and kept with the executable, put back by a
// rollback, and a release without a companion moves the installed one away (to the kept files).
func TestApplyAndRollbackWithCompanion(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			e := newEnv(t, goos, "v0.0.1", "v0.0.2", "v0.0.3")
			withCompanion(t, e, "v0.0.2", "seelegent-launch v0.0.2")
			exe, comp := filepath.Base(e.exe), filepath.Base(companionOf(e))
			if err := os.WriteFile(companionOf(e), []byte("seelegent-launch v0.0.1"), 0o755); err != nil {
				t.Fatal(err)
			}
			// A stale kept companion and leftovers of an interrupted update of the companion.
			for name, data := range map[string]string{comp + PreviousSuffix: "seelegent-launch v0.0.0",
				comp + newSuffix: "x", comp + oldInfix + "abc": "x"} {
				if err := os.WriteFile(filepath.Join(filepath.Dir(e.exe), name), []byte(data), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if _, err := companionUpdater(t, e, "v0.0.1").Apply(ctx, "v0.0.2"); err != nil {
				t.Fatal(err)
			}
			wantFiles(t, e, map[string]string{exe: "seelegent v0.0.2", exe + PreviousSuffix: "seelegent v0.0.1",
				comp: "seelegent-launch v0.0.2", comp + PreviousSuffix: "seelegent-launch v0.0.1"})

			// v0.0.3 has no companion: the installed one goes with the replaced executable.
			if _, err := companionUpdater(t, e, "v0.0.2").Apply(ctx, "v0.0.3"); err != nil {
				t.Fatal(err)
			}
			wantFiles(t, e, map[string]string{exe: "seelegent v0.0.3", exe + PreviousSuffix: "seelegent v0.0.2",
				comp + PreviousSuffix: "seelegent-launch v0.0.2"})

			if _, err := companionUpdater(t, e, "v0.0.3").Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			wantFiles(t, e, map[string]string{exe: "seelegent v0.0.2", comp: "seelegent-launch v0.0.2"})
		})
	}
}

// TestRollbackWithoutKeptCompanion: the kept version had no companion, so the installed one goes away.
func TestRollbackWithoutKeptCompanion(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.2", "v0.0.1")
	exe, comp := filepath.Base(e.exe), filepath.Base(companionOf(e))
	for name, data := range map[string]string{exe + PreviousSuffix: "seelegent v0.0.1",
		comp: "seelegent-launch v0.0.2"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(e.exe), name), []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := companionUpdater(t, e, "v0.0.2").Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, e, map[string]string{exe: "seelegent v0.0.1"})
}

// TestApplyRefusesMismatchedCompanion: a release whose companion is not of its executable's build installs nothing.
func TestApplyRefusesMismatchedCompanion(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	withCompanion(t, e, "v0.0.2", "seelegent-launch v0.0.9")
	if err := os.WriteFile(companionOf(e), []byte("seelegent-launch v0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := companionUpdater(t, e, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "is not of the downloaded seelegent's build") ||
		!strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("err = %v", err)
	}
	if len(e.daemon.calls) != 0 {
		t.Errorf("daemon touched: %v", e.daemon.calls)
	}
	wantFiles(t, e, map[string]string{"seelegent": "seelegent v0.0.1", "seelegent-launch": "seelegent-launch v0.0.1"})
}

// TestApplyStartFailurePutsCompanionBack: when the new daemon does not start, both files go back.
func TestApplyStartFailurePutsCompanionBack(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.1", "v0.0.2")
	withCompanion(t, e, "v0.0.2", "seelegent-launch v0.0.2")
	exe, comp := filepath.Base(e.exe), filepath.Base(companionOf(e))
	for name, data := range map[string]string{comp: "seelegent-launch v0.0.1",
		comp + PreviousSuffix: "seelegent-launch v0.0.0", exe + PreviousSuffix: "seelegent v0.0.0"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(e.exe), name), []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			return errors.New("exit status 1")
		}
		return nil
	}
	_, err := companionUpdater(t, e, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "v0.0.1 is back in place and its daemon runs") ||
		strings.Contains(err.Error(), "companion") {
		t.Fatalf("err = %v", err)
	}
	wantFiles(t, e, map[string]string{exe: "seelegent v0.0.1", exe + PreviousSuffix: "seelegent v0.0.0",
		comp: "seelegent-launch v0.0.1", comp + PreviousSuffix: "seelegent-launch v0.0.0"})
}

// TestReplacePairPutsCompanionBack: when the executable cannot be replaced, the companion is put back as it was.
func TestReplacePairPutsCompanionBack(t *testing.T) {
	dir := t.TempDir()
	exe, comp := filepath.Join(dir, "seelegent"), filepath.Join(dir, "seelegent-launch")
	for p, data := range map[string]string{comp: "old launcher", comp + newSuffix: "new launcher",
		PreviousPath(comp): "older launcher", exe: "old"} {
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := replacePair(exe, filepath.Join(dir, "missing"), comp, comp+newSuffix, true); err == nil {
		t.Fatal("replacePair succeeded without the new executable")
	}
	got := map[string]string{}
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		got[en.Name()] = readFile(t, filepath.Join(dir, en.Name()))
	}
	want := map[string]string{"seelegent": "old", "seelegent-launch": "old launcher",
		"seelegent-launch.new": "new launcher", "seelegent-launch.previous": "older launcher"}
	if len(got) != len(want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (files %v)", k, got[k], v, slices.Sorted(maps.Keys(got)))
		}
	}
}
