package gitx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Normalizes says what `git add` stores: each case is checked against git itself, adding the file and reading the
// index back. The files were committed with core.autocrlf=false, CRLF ones kept as CRLF, and autocrlf turned on
// afterwards: git's safer autocrlf keeps CRLF where the index already has a CR, unless the attributes say text.
func TestNormalizesMatchesGitAdd(t *testing.T) {
	gitExe(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, autocrlf := range []string{"true", "input", "false"} {
		t.Run("autocrlf="+autocrlf, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "-q", "-b", "main")
			git(t, dir, "config", "user.name", "Test")
			git(t, dir, "config", "user.email", "test@example.invalid")
			git(t, dir, "config", "commit.gpgsign", "false")
			git(t, dir, "config", "core.autocrlf", "false")
			git(t, dir, "config", "core.safecrlf", "false")
			committed := map[string]string{"crlf.txt": "a\r\nb\r\n", "lf.txt": "a\nb\n", "auto.txt": "a\r\nb\r\n",
				"text.txt": "a\r\nb\r\n", "eol.txt": "a\r\nb\r\n", "binary.txt": "a\r\nb\r\n"}
			for name, text := range committed {
				write(t, filepath.Join(dir, name), text)
			}
			git(t, dir, "add", ".")
			git(t, dir, "commit", "-q", "-m", "init")
			write(t, filepath.Join(dir, ".gitattributes"),
				"auto.txt text=auto eol=crlf\ntext.txt text\neol.txt eol=lf\nbinary.txt -text\n")
			git(t, dir, "config", "core.autocrlf", autocrlf)
			r := New(nil, nil)
			for _, name := range []string{"crlf.txt", "lf.txt", "auto.txt", "text.txt", "eol.txt", "binary.txt",
				"lonecr.txt", "new.txt"} {
				data := []byte("a\r\nb\r\nc\r\n")
				write(t, filepath.Join(dir, name), string(data))
				got, err := r.Normalizes(context.Background(), dir, name, data)
				if err != nil {
					t.Fatal(err)
				}
				git(t, dir, "add", "--", name)
				stored := git(t, dir, "cat-file", "blob", ":"+name)
				if want := !bytes.Contains([]byte(stored), []byte("\r")); got != want {
					t.Errorf("%s: Normalizes %v, git add stored %q", name, got, stored)
				}
			}
		})
	}
}
