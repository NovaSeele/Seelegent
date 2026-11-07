package ticket

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
)

// localRepo makes a repository with no origin whose pushes to the fake server's clone URLs land in bare
// repositories under remotes/ (url.<dir>.pushInsteadOf): nothing leaves the machine.
func localRepo(t *testing.T, e *env, name string) (dir, remotes string) {
	t.Helper()
	root := t.TempDir()
	dir, remotes = filepath.Join(root, name), filepath.Join(root, "remotes")
	if err := errors.Join(os.MkdirAll(dir, 0o700), os.MkdirAll(remotes, 0o700)); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.name", "Test")
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "core.autocrlf", "false")
	run(t, dir, "config", "url."+filepath.ToSlash(remotes)+"/.pushInsteadOf", e.gh.srv.URL+"/octo/")
	write(t, filepath.Join(dir, "README.md"), "# "+name+"\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "first")
	e.s.o.RemoteHost = github.Host{Name: e.gh.host(), Scheme: "http"}
	return dir, remotes
}

func connectLocal(t *testing.T, e *env, dir string) api.Repo {
	t.Helper()
	r, err := e.s.Connect(t.Context(), api.RepoConnectRequest{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func remoteEntries(t *testing.T, e *env, repoID string) []api.OutboxEntry {
	t.Helper()
	var list []api.OutboxEntry
	if err := e.db.Read(t.Context(), func(tx *sql.Tx) error {
		var err error
		list, err = listEntries(t.Context(), tx, scopeRepo, repoID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestCreateRemotePublishes(t *testing.T) {
	e := setupFlow(t)
	dir, remotes := localRepo(t, e, "pub")
	run(t, remotes, "init", "-q", "--bare", "pub.git")
	r := connectLocal(t, e, dir)
	got, err := e.s.CreateRemote(t.Context(), r.ID, api.RemoteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Remote != "origin" || len(e.gh.creates) != 1 || e.gh.repos["pub"]["private"] != true {
		t.Fatalf("repo = %+v, creates %v", got, e.gh.creates)
	}
	if want, have := run(t, dir, "rev-parse", "main"), run(t, filepath.Join(remotes, "pub.git"), "rev-parse",
		"main"); want != have {
		t.Fatalf("pushed %s, want %s", have, want)
	}
	list := remoteEntries(t, e, r.ID)
	if len(list) != 1 || list[0].Status != api.OutboxDone || createdName(list[0].Result) != "octo/pub" {
		t.Fatalf("outbox = %+v", list)
	}
}

// TestCreateRemoteScansHistory is the review's N2: the first publish sends every commit, so a secret committed and
// removed again refuses it, and so does a .env that holds the same bytes as an innocent file.
func TestCreateRemoteScansHistory(t *testing.T) {
	e := setupFlow(t)
	dir, _ := localRepo(t, e, "hist")
	write(t, filepath.Join(dir, "a.txt"), "same\n")
	write(t, filepath.Join(dir, "cfg.txt"), "token = "+testToken+"\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "oops")
	write(t, filepath.Join(dir, ".env"), "same\n")
	write(t, filepath.Join(dir, "cfg.txt"), "token = none\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "env")
	run(t, dir, "rm", "-q", ".env")
	run(t, dir, "commit", "-q", "-m", "fix")
	r := connectLocal(t, e, dir)
	_, err := e.s.CreateRemote(t.Context(), r.ID, api.RemoteRequest{})
	var pe *PushError
	if !errors.As(err, &pe) || pe.Reason != "history_unsafe" || len(pe.Findings) != 2 ||
		pe.Findings[0].Path != ".env" || pe.Findings[1].Path != "cfg.txt" {
		t.Fatalf("create = %v (%+v)", err, pe)
	}
	if strings.Contains(err.Error(), testToken) || len(e.gh.creates) != 0 || len(remoteEntries(t, e, r.ID)) != 0 {
		t.Fatalf("created %v", e.gh.creates)
	}
}

// TestCreateRemoteAdoption: a repository is adopted only after an attempt that may have made it, and only when it
// is empty (or holds exactly this push) with the asked visibility.
func TestCreateRemoteAdoption(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	dir, remotes := localRepo(t, e, "adopt")
	r := connectLocal(t, e, dir)
	mark := func(status string, result map[string]any) {
		t.Helper()
		if err := e.db.Write(ctx, func(tx *sql.Tx) error {
			list, err := listEntries(ctx, tx, scopeRepo, r.ID)
			if err != nil {
				return err
			}
			en := api.OutboxEntry{Kind: api.OutboxCreateRemote, Result: result, At: "2026-11-08T00:00:00Z"}
			if len(list) > 0 {
				en = list[len(list)-1]
				en.Result = result
			}
			en.Status = status
			_, err = saveEntry(ctx, tx, scopeRepo, r.ID, en)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	repo := func(private bool) map[string]any {
		return map[string]any{"name": "adopt", "private": private, "owner": map[string]any{"login": "octo"},
			"html_url": e.gh.srv.URL + "/octo/adopt", "clone_url": e.gh.srv.URL + "/octo/adopt.git"}
	}

	// A failed attempt that never recorded creating it: a repository of that name is someone's, never adopted.
	e.gh.repos["adopt"] = repo(true)
	mark(api.OutboxFailed, map[string]any{})
	if _, err := e.s.CreateRemote(ctx, r.ID, api.RemoteRequest{}); err == nil || len(e.gh.creates) != 0 {
		t.Fatalf("adopted after a failure: %v", err)
	}
	// After a stop (unknown): adopted only when the visibility is the one asked and it is empty.
	mark(api.OutboxUnknown, map[string]any{})
	no := false
	if _, err := e.s.CreateRemote(ctx, r.ID, api.RemoteRequest{Private: &no}); !errors.Is(err, ErrBadState) ||
		!strings.Contains(err.Error(), "private") {
		t.Fatalf("visibility: %v", err)
	}
	mark(api.OutboxUnknown, map[string]any{})
	e.gh.branches["adopt"] = []map[string]any{{"name": "dev", "commit": map[string]any{"sha": strings.Repeat("a", 40)}}}
	if _, err := e.s.CreateRemote(ctx, r.ID, api.RemoteRequest{}); !errors.Is(err, ErrBadState) ||
		!strings.Contains(err.Error(), "not empty") {
		t.Fatalf("not empty: %v", err)
	}
	mark(api.OutboxUnknown, map[string]any{})
	delete(e.gh.branches, "adopt")
	run(t, remotes, "init", "-q", "--bare", "adopt.git")
	if _, err := e.s.CreateRemote(ctx, r.ID, api.RemoteRequest{}); err != nil || len(e.gh.creates) != 0 {
		t.Fatalf("adopt an empty repository: %v, creates %v", err, e.gh.creates)
	}
	if run(t, filepath.Join(remotes, "adopt.git"), "rev-parse", "main") != run(t, dir, "rev-parse", "main") {
		t.Fatal("main was not pushed")
	}
}

// TestCreateRemoteRecordsCreation: a repository this service created is recorded at once, so an attempt that
// failed after creating it (the push) adopts it instead of creating another.
func TestCreateRemoteRecordsCreation(t *testing.T) {
	e := setupFlow(t)
	dir, remotes := localRepo(t, e, "again")
	r := connectLocal(t, e, dir)
	// No bare repository yet: the push fails after the creation.
	if _, err := e.s.CreateRemote(t.Context(), r.ID, api.RemoteRequest{}); err == nil {
		t.Fatal("the push did not fail")
	}
	list := remoteEntries(t, e, r.ID)
	if len(list) != 1 || list[0].Status != api.OutboxFailed || createdName(list[0].Result) != "octo/again" {
		t.Fatalf("outbox = %+v", list)
	}
	run(t, remotes, "init", "-q", "--bare", "again.git")
	if _, err := e.s.CreateRemote(t.Context(), r.ID, api.RemoteRequest{}); err != nil || len(e.gh.creates) != 1 {
		t.Fatalf("retry: %v, creates %v", err, e.gh.creates)
	}
}
