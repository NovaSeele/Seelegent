package account

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/config"
)

func TestCreateAllocatesIDsNeverReused(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	// A profile left on disk (for example by the old version) keeps its id from being handed out.
	if err := os.MkdirAll(filepath.Join(f.profiles, "acc2"), 0o700); err != nil {
		t.Fatal(err)
	}
	a1, _, err := f.m.Add(ctx, New{Backend: "agy", Kind: KindIsolated, Label: "one", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := f.m.Add(ctx, New{Backend: "codex", Kind: KindIsolated, Priority: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a1.ID != "acc1" || a2.ID != "acc3" {
		t.Fatalf("ids = %s, %s; want acc1, acc3", a1.ID, a2.ID)
	}
	if err := f.m.Remove(ctx, a2.ID); err != nil {
		t.Fatal(err)
	}
	a3, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindIsolated}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a3.ID != "acc4" {
		t.Fatalf("after removing acc3 the next id is %s, want acc4", a3.ID)
	}
	got, err := f.m.Get(ctx, "acc1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "one" || !got.Enabled || got.Kind != KindIsolated || got.Backend != "agy" ||
		!got.CreatedAt.Equal(a1.CreatedAt) {
		t.Errorf("Get = %+v, want %+v", got, a1)
	}
}

func TestCreateBadCounter(t *testing.T) {
	f := newFixture(t)
	if err := f.db.SetMeta(ctxT(t), nextSeqKey, "zero"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Repo().Create(ctxT(t), New{Backend: "agy", Kind: KindIsolated}, nil); err == nil {
		t.Fatal("Create with a damaged counter succeeded")
	}
}

func TestSystemAccount(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a, _, err := f.m.Add(ctx, New{Backend: "agy", Kind: KindSystem, Label: "me"}, []string{".gitconfig"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != SystemID {
		t.Fatalf("id = %s", a.ID)
	}
	if _, _, err := f.m.Add(ctx, New{Backend: "agy", Kind: KindSystem}, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("second system account: %v", err)
	}
	prof, err := f.m.Profile(a)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Home != f.realHome || prof.Dir != "" || prof.Isolated() {
		t.Errorf("system profile = %+v", prof)
	}
	marker := filepath.Join(f.realHome, "keep")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Remove(ctx, SystemID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("removing the system account touched the real home: %v", err)
	}
	if err := f.m.Profiles().Remove(SystemID); !errors.Is(err, ErrInvalid) {
		t.Errorf("Profiles.Remove(system) = %v", err)
	}
}

func TestListUpdateDelete(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	for _, n := range []New{
		{Backend: "agy", Kind: KindIsolated, Label: "low"},
		{Backend: "agy", Kind: KindIsolated, Label: "high", Priority: 10},
		{Backend: "agy", Kind: KindIsolated, Label: "low2"},
	} {
		if _, _, err := f.m.Add(ctx, n, nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := f.m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, a := range list {
		labels = append(labels, a.Label)
	}
	if len(labels) != 3 || labels[0] != "high" || labels[1] != "low" || labels[2] != "low2" {
		t.Fatalf("order = %v", labels)
	}

	label, enabled, prio := "renamed", true, -3
	a, err := f.m.Update(ctx, "acc1", Patch{Label: &label, Enabled: &enabled, Priority: &prio})
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != label || !a.Enabled || a.Priority != prio || !a.UpdatedAt.After(a.CreatedAt) {
		t.Errorf("Update = %+v", a)
	}
	bad := "x\x01"
	if _, err := f.m.Update(ctx, "acc1", Patch{Label: &bad}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad label: %v", err)
	}
	big := 5000
	if _, err := f.m.Update(ctx, "acc1", Patch{Priority: &big}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad priority: %v", err)
	}
	if _, err := f.m.Update(ctx, "acc9", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	if err := f.m.Repo().Delete(ctx, "acc9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing: %v", err)
	}
	if err := f.m.Remove(ctx, "acc9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove missing: %v", err)
	}
	if err := f.m.Repo().SetKeyHint(ctx, "acc9", "…"); !errors.Is(err, ErrNotFound) {
		t.Errorf("hint missing: %v", err)
	}
}

func TestProvidersCRUD(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	p := f.provider(t, "zai", "zai")
	if _, err := f.m.CreateProvider(ctx, p); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := f.m.CreateProvider(ctx, Provider{ID: "BAD"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	got, err := f.m.GetProvider(ctx, "zai")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != p.BaseURL || got.Tiers != p.Tiers || !got.Enabled || got.Preset != "zai" {
		t.Errorf("GetProvider = %+v, want %+v", got, p)
	}
	if _, err := f.m.GetProvider(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}

	// An account that uses it pins its backend and blocks deletion.
	if _, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindAPIKey, Provider: "zai"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Add(ctx, New{Backend: "codex", Kind: KindAPIKey, Provider: "zai"}, nil); !errors.Is(err,
		ErrInvalid) {
		t.Errorf("backend mismatch: %v", err)
	}
	if _, _, err := f.m.Add(ctx, New{Backend: "codex", Kind: KindAPIKey, Provider: "none"}, nil); !errors.Is(err,
		ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
	moved := got
	moved.Backend = "codex"
	if _, err := f.m.UpdateProvider(ctx, moved); !errors.Is(err, ErrConflict) {
		t.Errorf("backend change in use: %v", err)
	}
	if err := f.m.DeleteProvider(ctx, "zai"); !errors.Is(err, ErrConflict) {
		t.Errorf("delete in use: %v", err)
	}

	got.Enabled = false
	got.Name = "Z"
	upd, err := f.m.UpdateProvider(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Enabled || upd.Name != "Z" || !upd.CreatedAt.Equal(got.CreatedAt) || !upd.UpdatedAt.After(got.CreatedAt) {
		t.Errorf("UpdateProvider = %+v", upd)
	}
	if _, err := f.m.UpdateProvider(ctx, Provider{ID: "nope", Backend: "claude", BaseURL: "https://a.test"}); !errors.Is(
		err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}

	if err := f.m.Remove(ctx, "acc1"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.DeleteProvider(ctx, "zai"); err != nil {
		t.Fatal(err)
	}
	if err := f.m.DeleteProvider(ctx, "zai"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	f.provider(t, "b", "openai")
	f.provider(t, "a", "anthropic")
	list, err := f.m.ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" || !list[0].Official {
		t.Errorf("ListProviders = %+v", list)
	}
}

// The allow-list is stored as a list and read back exactly; rows from before the column existed (NULL) get the list
// their pattern was built from, or none for a hand-written pattern; a damaged list is an error.
func TestProviderModelsStored(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	p, err := FromPreset("ds", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	p.Models = []string{"deepseek-v4", "deepseek-r2"}
	p.Tiers = config.TierModels{Fast: "deepseek-chat"}
	if _, err := f.m.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := f.m.GetProvider(ctx, "ds")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Models, p.Models) || got.AllowedModels != `^(?:deepseek-v4|deepseek-r2)$` ||
		got.Tiers.Fast != "deepseek-chat" {
		t.Fatalf("stored provider = %+v", got)
	}
	got.Models = nil
	got.AllowedModels = "^glm-"
	if _, err := f.m.UpdateProvider(ctx, got); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.m.GetProvider(ctx, "ds"); got.Models == nil || len(got.Models) != 0 || got.AllowedModels != "^glm-" {
		t.Fatalf("raw pattern = %+v", got)
	}

	set := func(models any, allowed string) {
		t.Helper()
		if err := f.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE providers SET models = ?, allowed_models = ?", models, allowed)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	set(nil, `^(?:glm-4\.6|glm-5)$`)
	if got, err = f.m.GetProvider(ctx, "ds"); err != nil || !slices.Equal(got.Models, []string{"glm-4.6", "glm-5"}) {
		t.Fatalf("legacy list = %+v, %v", got.Models, err)
	}
	set(nil, "^glm-")
	if got, err = f.m.GetProvider(ctx, "ds"); err != nil || got.Models == nil || len(got.Models) != 0 {
		t.Fatalf("legacy pattern = %+v, %v", got.Models, err)
	}
	set("{oops", "")
	if _, err := f.m.GetProvider(ctx, "ds"); err == nil {
		t.Fatal("a damaged models list was read")
	}
}

func TestDamagedRowsAreErrors(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	f.provider(t, "p", "deepseek")
	if _, _, err := f.m.Add(ctx, New{Backend: "agy", Kind: KindIsolated}, nil); err != nil {
		t.Fatal(err)
	}
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET created_at = 'yesterday'"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE providers SET updated_at = 'later'")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.List(ctx); err == nil {
		t.Error("List with a damaged row succeeded")
	}
	if _, err := f.m.Get(ctx, "acc1"); err == nil {
		t.Error("Get with a damaged row succeeded")
	}
	if _, err := f.m.ListProviders(ctx); err == nil {
		t.Error("ListProviders with a damaged row succeeded")
	}
}

func TestClosedDatabase(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	_ = f.db.Close()
	if _, err := f.m.List(ctx); err == nil {
		t.Error("List on a closed database succeeded")
	}
	if _, err := f.m.ListProviders(ctx); err == nil {
		t.Error("ListProviders on a closed database succeeded")
	}
	if _, _, err := f.m.Add(ctx, New{Backend: "agy", Kind: KindIsolated}, nil); err == nil {
		t.Error("Add on a closed database succeeded")
	}
	if err := f.m.DeleteProvider(ctx, "x"); err == nil {
		t.Error("DeleteProvider on a closed database succeeded")
	}
}
