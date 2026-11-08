package contest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestPickIgnoresTextconv: the winner's patch is built without textconv, so a repository whose diff driver
// converts a file (here: to upper case, through the shell git ships) still gets the variant's bytes, for a
// changed file (with textconv its patch does not apply) and for an added one (with textconv the converted text
// would be written).
func TestPickIgnoresTextconv(t *testing.T) {
	for name, text := range map[string]string{"old.dat": "base data\nmore data\n", "new.dat": "fresh data\n"} {
		t.Run(name, func(t *testing.T) { pickWithTextconv(t, name, text) })
	}
}

// pickWithTextconv picks a variant that writes text to name in a repository whose *.dat files have a textconv
// driver (old.dat is tracked), and checks the file picked against the variant's commit, byte for byte.
func pickWithTextconv(t *testing.T, name, text string) {
	e := setup(t)
	ctx := t.Context()
	git(t, e.root, "config", "diff.up.textconv", "tr a-z A-Z <")
	write(t, filepath.Join(e.root, ".gitattributes"), "*.dat diff=up\n")
	write(t, filepath.Join(e.root, "old.dat"), "base data\n")
	git(t, e.root, "add", ".gitattributes", "old.dat")
	git(t, e.root, "commit", "-q", "-m", "dat", "--", ".gitattributes", "old.dat")
	if got := git(t, e.root, "diff", "HEAD~1", "HEAD", "--", "old.dat"); !strings.Contains(got, "+BASE DATA") {
		t.Fatalf("the textconv driver is not in effect:\n%s", got)
	}
	c, err := e.s.Create(ctx, api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Data.", Cwd: e.root},
		Variants: 2, NoJudge: true})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c.Variants[0].Worktree, name), text)
	write(t, filepath.Join(c.Variants[1].Worktree, "a.txt"), "alpha\nhello\n")
	e.tasks.finish(c.Variants[0].TaskID, "Wrote "+name+".")
	e.tasks.finish(c.Variants[1].TaskID, "Changed a.txt.")
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	id := c.ID
	if c, err = e.s.Pick(ctx, id, api.ContestPickRequest{Winner: "V1"}); err != nil {
		after, _ := e.s.Get(ctx, id)
		t.Fatalf("pick: %v (last error %+v)", err, after.LastPickError)
	}
	if c.Applied == nil || c.Applied.Commit == "" {
		t.Fatalf("picked = %+v", c)
	}
	blob := git(t, e.root, "cat-file", "blob", c.Applied.Commit+":"+name)
	if blob+"\n" != text { // git() trims the one trailing newline
		t.Errorf("the variant commit holds %q, want %q", blob, text)
	}
	if got := read(t, filepath.Join(e.root, name)); got != text {
		t.Errorf("%s = %q, want the variant's %q", name, got, text)
	}
}
