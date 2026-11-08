package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// A profile copy that fails halfway leaves no profile folder, so the next run copies it again instead of calling
// the partial copy complete (M18 hunt H11).
func TestPartialProfileCopyIsNotKept(t *testing.T) {
	f := newFixture(t)
	f.write("accounts.json", `[{"id": "acc1", "backend": "agy", "label": "A"}]`)
	f.write("profiles/acc1/home/a-first.txt", "a")
	f.write("profiles/acc1/home/m-credentials.json", "secret")
	f.write("profiles/acc1/home/z-last.txt", "z")
	release := unreadable(t, filepath.Join(f.old, "profiles", "acc1", "home", "m-credentials.json"))
	rep := f.run(false)
	release()
	if it := find(t, rep, KindProfile, "acc1"); it.Action != Error {
		t.Fatalf("run 1: profile %s: %s", it.Action, it.Detail)
	}
	home := filepath.Join(f.paths.Profiles, "acc1", "home")
	if _, err := os.Stat(home); err == nil {
		t.Fatalf("the failed copy left %s behind", home)
	}
	if left, _ := filepath.Glob(filepath.Join(f.paths.Profiles, "acc1", "*.migrating")); len(left) != 0 {
		t.Fatalf("temp folders left: %v", left)
	}

	rep = f.run(false)
	if it := find(t, rep, KindProfile, "acc1"); it.Action != Imported {
		t.Fatalf("run 2: profile %s: %s", it.Action, it.Detail)
	}
	if data, err := os.ReadFile(filepath.Join(home, "m-credentials.json")); err != nil || string(data) != "secret" {
		t.Fatalf("the credentials were not copied: %q, %v", data, err)
	}
	if it := find(t, f.run(false), KindProfile, "acc1"); it.Action != Unchanged {
		t.Fatalf("run 3: profile %s: %s", it.Action, it.Detail)
	}
}

// An account this version created itself under an id the old data also uses is not the old account: the old one
// is reported kept, with the reason, and nothing of it lands on the new account (M18 hunt H12). An account an
// earlier run imported is still "already imported", even after the user renamed it.
func TestClashingAccountIDIsNotTakenForTheOldOne(t *testing.T) {
	f := newFixture(t)
	f.write("accounts.json", `[{"id": "acc1", "backend": "agy", "label": "Old work account", "createdAt": "2026-01-02T03:04:05.000Z"}]`)
	f.write("profiles/acc1/home/creds.json", "old")
	if err := f.paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), f.paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	repo := account.NewRepo(db, nil)
	a, err := repo.Create(context.Background(), account.New{Backend: "agy", Kind: account.KindIsolated,
		Label: "New personal", Enabled: true}, nil)
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "acc1" {
		t.Skipf("this version named its first account %s; no clash to test", a.ID)
	}
	rep := f.run(false)
	if it := find(t, rep, KindAccount, "acc1"); it.Action != Kept {
		t.Fatalf("account %s: %s", it.Action, it.Detail)
	}
	for _, it := range rep.Items {
		if it.ID == "acc1" && it.Kind != KindAccount {
			t.Errorf("%s of the old account touched the new one: %s %s", it.Kind, it.Action, it.Detail)
		}
	}
	if _, err := os.Stat(filepath.Join(f.paths.Profiles, "acc1", "home", "creds.json")); err == nil {
		t.Error("the old profile was copied into the new account's profile")
	}
}

func TestImportedAccountStaysAlreadyImported(t *testing.T) {
	f := newFixture(t)
	f.write("accounts.json", `[{"id": "acc1", "backend": "agy", "label": "Work", "createdAt": "2026-01-02T03:04:05.000Z"},
  {"id": "acc2", "backend": "agy", "label": "No date"}]`)
	if it := find(t, f.run(false), KindAccount, "acc1"); it.Action != Imported {
		t.Fatalf("run 1: %s %s", it.Action, it.Detail)
	}
	// The user renames acc1 in this version; the creation time still says it is the old account.
	db, err := store.Open(context.Background(), f.paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	repo := account.NewRepo(db, nil)
	label := "Renamed"
	_, err = repo.Update(context.Background(), "acc1", account.Patch{Label: &label})
	_ = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	rep := f.run(false)
	for _, id := range []string{"acc1", "acc2"} {
		if it := find(t, rep, KindAccount, id); it.Action != Unchanged {
			t.Errorf("run 2, %s: %s %s", id, it.Action, it.Detail)
		}
	}
}
