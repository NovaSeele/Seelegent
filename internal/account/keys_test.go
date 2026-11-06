package account

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeysRoundTrip(t *testing.T) {
	f := newFixture(t)
	keys := NewKeys(f.m.Profiles())
	const key = "sk-test-0123456789abcdef"
	hint, err := keys.Save("acc1", "  "+key+"\n")
	if err != nil {
		t.Fatal(err)
	}
	if hint != "…cdef" {
		t.Errorf("hint = %q", hint)
	}
	if !keys.Has("acc1") || keys.Has("acc2") {
		t.Error("Has is wrong")
	}
	got, err := keys.Load("acc1")
	if err != nil || got != key {
		t.Fatalf("Load = %q, %v", got, err)
	}
	path := filepath.Join(f.profiles, "acc1", keyFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if bytes.Contains(raw, []byte(key)) {
			t.Error("the key file holds the key in plain text on Windows")
		}
	} else {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("key file mode = %v, want 0600", st.Mode().Perm())
		}
	}
	// The file sits next to the CLI home, not inside it.
	if strings.HasPrefix(path, filepath.Join(f.profiles, "acc1", "home")) {
		t.Error("key file inside the CLI home")
	}

	if err := keys.Remove("acc1"); err != nil {
		t.Fatal(err)
	}
	if err := keys.Remove("acc1"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	if _, err := keys.Load("acc1"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Load after Remove = %v, want ErrNoKey", err)
	}
}

func TestKeysBlobBoundToAccount(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI entropy only on Windows")
	}
	f := newFixture(t)
	keys := NewKeys(f.m.Profiles())
	if _, err := keys.Save("acc1", "sk-test-0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(f.profiles, "acc1", keyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.profiles, "acc2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.profiles, "acc2", keyFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.Load("acc2"); err == nil || errors.Is(err, ErrNoKey) {
		t.Fatalf("a blob copied to another account decrypted or looked missing: %v", err)
	}
}

func TestKeysDamagedFile(t *testing.T) {
	f := newFixture(t)
	keys := NewKeys(f.m.Profiles())
	dir := filepath.Join(f.profiles, "acc1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"", "has space"} {
		if err := os.WriteFile(filepath.Join(dir, keyFileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := keys.Load("acc1"); err == nil || errors.Is(err, ErrNoKey) {
			t.Errorf("damaged file %q: err = %v", content, err)
		}
	}
}

func TestKeysValidation(t *testing.T) {
	f := newFixture(t)
	keys := NewKeys(f.m.Profiles())
	for _, k := range []string{"", "   ", "a b", "a\x00b", strings.Repeat("k", maxKeyLen+1)} {
		if _, err := keys.Save("acc1", k); !errors.Is(err, ErrInvalid) {
			t.Errorf("Save(%q) = %v", k, err)
		}
	}
	if _, err := keys.Save("../x", "sk-12345678"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
	if _, err := keys.Load("../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id load: %v", err)
	}
	if err := keys.Remove("../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id remove: %v", err)
	}
	if keys.Has("../x") {
		t.Error("Has(bad id)")
	}
}

func TestKeyHint(t *testing.T) {
	cases := map[string]string{"": "…", "short": "…", "1234567": "…", "12345678": "…5678", "sk-ąęśćłńóż": "…łńóż"}
	for in, want := range cases {
		if got := KeyHint(in); got != want {
			t.Errorf("KeyHint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManagerKeys(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	f.provider(t, "ds", "deepseek")
	a, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindAPIKey, Provider: "ds", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Credentials(ctx, a); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key yet: %v", err)
	}
	a, err = f.m.SetKey(ctx, a.ID, "sk-abcdefgh1234")
	if err != nil {
		t.Fatal(err)
	}
	if a.KeyHint != "…1234" || a.KeySetAt.IsZero() {
		t.Errorf("KeyHint = %q, KeySetAt = %s", a.KeyHint, a.KeySetAt)
	}
	// Other edits do not move KeySetAt.
	keySet := a.KeySetAt
	label := "renamed"
	if a, err = f.m.Update(ctx, a.ID, Patch{Label: &label}); err != nil || !a.KeySetAt.Equal(keySet) {
		t.Fatalf("Update = %+v, %v; KeySetAt moved from %s", a, err, keySet)
	}
	p, key, err := f.m.Credentials(ctx, a)
	if err != nil || key != "sk-abcdefgh1234" || p.ID != "ds" {
		t.Fatalf("Credentials = %+v, %q, %v", p, key, err)
	}
	a, err = f.m.ClearKey(ctx, a.ID)
	if err != nil || a.KeyHint != "" {
		t.Fatalf("ClearKey = %+v, %v", a, err)
	}
	if _, err := f.m.SetKey(ctx, a.ID, "bad key"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key: %v", err)
	}
	if _, err := f.m.SetKey(ctx, "acc9", "sk-abcdefgh"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing account: %v", err)
	}
	if _, err := f.m.ClearKey(ctx, "acc9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing account: %v", err)
	}
	iso, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindIsolated}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.SetKey(ctx, iso.ID, "sk-abcdefgh"); !errors.Is(err, ErrInvalid) {
		t.Errorf("key on isolated account: %v", err)
	}
	if _, _, err := f.m.Credentials(ctx, iso); !errors.Is(err, ErrInvalid) {
		t.Errorf("credentials of isolated account: %v", err)
	}

	// Provider disabled -> unavailable, before the key is even read.
	if _, err := f.m.SetKey(ctx, a.ID, "sk-abcdefgh1234"); err != nil {
		t.Fatal(err)
	}
	pr, err := f.m.GetProvider(ctx, "ds")
	if err != nil {
		t.Fatal(err)
	}
	pr.Enabled = false
	if _, err := f.m.UpdateProvider(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Credentials(ctx, a); !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("disabled provider: %v", err)
	}
	ghost := a
	ghost.Provider = "gone"
	if _, _, err := f.m.Credentials(ctx, ghost); !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("missing provider: %v", err)
	}
	pr.Enabled = true
	if _, err := f.m.UpdateProvider(ctx, pr); err != nil {
		t.Fatal(err)
	}
	wrong := a
	wrong.Backend = "codex"
	if _, _, err := f.m.Credentials(ctx, wrong); !errors.Is(err, ErrProviderUnavailable) {
		t.Errorf("backend mismatch: %v", err)
	}

	// Removing the account removes the key with the profile.
	if err := f.m.Remove(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.profiles, a.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("profile left after Remove: %v", err)
	}
}
