package member

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/store"
)

type fakeProviders map[string]account.Provider

func (f fakeProviders) GetProvider(_ context.Context, id string) (account.Provider, error) {
	p, ok := f[id]
	if !ok {
		return account.Provider{}, account.ErrNotFound
	}
	return p, nil
}

func newStore(t *testing.T, prov fakeProviders) (s *Store, calls *int) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	changes := 0
	return New(Options{DB: st, Providers: prov, Now: func() time.Time { at = at.Add(time.Minute); return at },
		Changed: func() { changes++ }}), &changes
}

func TestCreateValidateAndList(t *testing.T) {
	ctx := context.Background()
	s, changes := newStore(t, fakeProviders{"glm": {ID: "glm", Backend: "claude", Enabled: true}})
	m, err := s.Create(ctx, api.MemberInput{Name: "Nguyễn Văn An", Backend: "agy", Rank: api.RankPeer,
		Tier: "strong", PreferredAccounts: []string{"a1", "a1", "a2"}, FallbackBackends: []string{"codex", "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "nguyen-van-an" || !m.Enabled || len(m.PreferredAccounts) != 2 || len(m.FallbackBackends) != 1 ||
		m.CreatedAt == "" || *changes != 1 {
		t.Fatalf("created %+v (changes %d)", m, *changes)
	}
	// The same name again gets a numbered id; an explicit id that exists is a conflict.
	m2, err := s.Create(ctx, api.MemberInput{Name: "Nguyen Van An", Backend: "codex", Rank: api.RankWorker})
	if err != nil || m2.ID != "nguyen-van-an-2" {
		t.Fatalf("second = %+v, %v", m2, err)
	}
	if _, err := s.Create(ctx, api.MemberInput{ID: "nguyen-van-an", Name: "X", Backend: "agy",
		Rank: api.RankWorker}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate id: %v", err)
	}
	// A provider member takes the provider's backend; a mismatch or fallbacks are refused.
	p, err := s.Create(ctx, api.MemberInput{Name: "GLM", Provider: "glm", Rank: api.RankSenior})
	if err != nil || p.Backend != "claude" {
		t.Fatalf("provider member = %+v, %v", p, err)
	}
	for _, in := range []api.MemberInput{
		{Name: "x", Provider: "glm", Backend: "codex", Rank: api.RankPeer},
		{Name: "x", Provider: "glm", Rank: api.RankPeer, FallbackBackends: []string{"agy"}},
		{Name: "x", Provider: "nope", Rank: api.RankPeer},
		{Name: "x", Backend: "agy", Rank: "boss"},
		{Name: "", Backend: "agy", Rank: api.RankPeer},
		{Name: "x", Rank: api.RankPeer},
		{Name: "x", Backend: "gpt", Rank: api.RankPeer},
		{Name: "x", Backend: "agy", Rank: api.RankPeer, Tier: "huge"},
		{Name: "x", Backend: "agy", Rank: api.RankPeer, FallbackBackends: []string{"agy"}},
		{Name: "x", Backend: "agy", Rank: api.RankPeer, Role: "Not A Slug"},
		{ID: "Bad Id", Name: "x", Backend: "agy", Rank: api.RankPeer},
	} {
		if _, err := s.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) = %v, want invalid", in, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 3 || list[0].ID != "glm" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}

func TestUpdateUsableDelete(t *testing.T) {
	ctx := context.Background()
	prov := fakeProviders{"glm": {ID: "glm", Backend: "claude", Enabled: true}}
	s, _ := newStore(t, prov)
	m, err := s.Create(ctx, api.MemberInput{ID: "rev", Name: "Reviewer", Provider: "glm", Rank: api.RankPeer})
	if err != nil {
		t.Fatal(err)
	}
	in := Input(m)
	off := false
	in.Enabled = &off
	in.Name = "Strict reviewer"
	u, err := s.Update(ctx, "rev", in)
	if err != nil || u.Name != "Strict reviewer" || u.Enabled || u.CreatedAt != m.CreatedAt || u.UpdatedAt == m.UpdatedAt {
		t.Fatalf("Update = %+v, %v", u, err)
	}
	var de *DisabledError
	if _, err := s.Usable(ctx, "rev"); !errors.As(err, &de) || !errors.Is(err, ErrDisabled) ||
		de.APIDetails()["reason"] != "member_disabled" {
		t.Fatalf("Usable(disabled) = %v", err)
	}
	on := true
	in.Enabled = &on
	if _, err := s.Update(ctx, "rev", in); err != nil {
		t.Fatal(err)
	}
	delete(prov, "glm")
	if g, err := s.Get(ctx, "rev"); err != nil || g.Invalid == "" {
		t.Fatalf("Get after the provider went = %+v, %v", g, err)
	}
	if _, err := s.Usable(ctx, "rev"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Usable(invalid) = %v", err)
	}
	if err := s.Delete(ctx, "rev"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "rev"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	if _, err := s.Update(ctx, "rev", api.MemberInput{Name: "x", Backend: "agy", Rank: api.RankPeer}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of a deleted member = %v", err)
	}
	// A deleted member named for work is a bad argument that says which member (member_missing).
	var me *MissingError
	if _, err := s.Usable(ctx, "rev"); !errors.As(err, &me) || !errors.Is(err, ErrInvalid) ||
		errors.Is(err, ErrNotFound) || me.APIDetails()["reason"] != "member_missing" || me.APIDetails()["member"] != "rev" {
		t.Fatalf("Usable(deleted) = %v", err)
	}
}

func TestImportKeepsExisting(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t, nil)
	in := api.MemberInput{ID: "dev", Name: "Dev", Backend: "agy", Rank: api.RankWorker}
	if added, err := s.Import(ctx, in); err != nil || !added {
		t.Fatalf("Import = %v, %v", added, err)
	}
	in.Name = "Other"
	if added, err := s.Import(ctx, in); err != nil || added {
		t.Fatalf("second Import = %v, %v", added, err)
	}
	if m, _ := s.Get(ctx, "dev"); m.Name != "Dev" {
		t.Fatalf("import replaced the member: %+v", m)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Trần Thị Bích Đào": "tran-thi-bich-dao", "  API -- guy ": "api-guy", "日本": "member",
		"Senior Reviewer #2": "senior-reviewer-2",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
