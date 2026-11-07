package account

import (
	"errors"
	"testing"
	"time"
)

func TestImportKeepsIDAndAdvancesCounter(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	repo := f.m.Repo()
	created := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	a, err := repo.Import(ctx, "acc7", New{Backend: "agy", Kind: KindIsolated, Label: "old", Enabled: true}, created)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "acc7" || !a.CreatedAt.Equal(created) {
		t.Fatalf("imported %+v", a)
	}
	if _, err := repo.Import(ctx, "system", New{Backend: "agy", Kind: KindSystem}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	next, err := repo.Create(ctx, New{Backend: "agy", Kind: KindIsolated}, nil)
	if err != nil || next.ID != "acc8" {
		t.Fatalf("next id %q, %v; want acc8", next.ID, err)
	}
	// A lower id does not move the counter back.
	if _, err := repo.Import(ctx, "acc2", New{Backend: "codex", Kind: KindIsolated}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if n, _ := repo.Create(ctx, New{Backend: "agy", Kind: KindIsolated}, nil); n.ID != "acc9" {
		t.Fatalf("next id %q; want acc9", n.ID)
	}
	if _, err := repo.Import(ctx, "acc7", New{Backend: "agy", Kind: KindIsolated}, time.Time{}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate id: %v", err)
	}
	for _, bad := range []struct {
		id string
		n  New
	}{
		{"acc0", New{Backend: "agy", Kind: KindIsolated}},
		{"acc07", New{Backend: "agy", Kind: KindIsolated}},
		{"foo", New{Backend: "agy", Kind: KindIsolated}},
		{"acc3", New{Backend: "agy", Kind: KindSystem}},
		{"acc3", New{Backend: "claude", Kind: KindAPIKey, Provider: "missing"}},
	} {
		if _, err := repo.Import(ctx, bad.id, bad.n, time.Time{}); err == nil {
			t.Errorf("Import(%s, %+v) succeeded", bad.id, bad.n)
		}
	}
}
