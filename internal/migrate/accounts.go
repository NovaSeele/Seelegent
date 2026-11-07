package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// oldKeyFile is where the old version kept an API key: in plain text inside the profile's home.
const oldKeyFile = "seelegent-key.json"

// oldProvider is a provider in the old settings.json.
type oldProvider struct {
	Label         *string           `json:"label"`
	Harness       string            `json:"harness"`
	BaseURL       string            `json:"baseUrl"`
	WireAPI       string            `json:"wireApi"`
	Official      bool              `json:"official"`
	Enabled       *bool             `json:"enabled"`
	AllowedModels string            `json:"allowedModels"`
	Tiers         config.TierModels `json:"tiers"`
	Preset        string            `json:"preset"`
}

// importProviders imports the providers of the old settings.json.
func (m *migrator) importProviders(ctx context.Context, raw json.RawMessage, _ config.Settings) {
	var old map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		m.add(KindProvider, "", Error, "settings.json providers is not an object: %v", err)
		return
	}
	ids := make([]string, 0, len(old))
	for id := range old {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var op oldProvider
		if err := json.Unmarshal(old[id], &op); err != nil {
			m.add(KindProvider, id, Error, "unreadable: %v", err)
			continue
		}
		p, note := convertProvider(id, op)
		if err := p.Validate(); err != nil {
			m.add(KindProvider, id, Error, "%v", err)
			continue
		}
		have, err := m.accounts.GetProvider(ctx, id)
		switch {
		case err == nil && sameProvider(have, p):
			m.add(KindProvider, id, Unchanged, "already imported")
			continue
		case err == nil:
			m.add(KindProvider, id, Kept, "a provider with this id already exists and differs; it was left as it is")
			continue
		case !errors.Is(err, account.ErrNotFound):
			m.add(KindProvider, id, Error, "%v", err)
			continue
		}
		if _, err := m.accounts.CreateProvider(ctx, p); err != nil {
			m.add(KindProvider, id, Error, "%v", err)
			continue
		}
		m.add(KindProvider, id, Imported, "%s provider %q%s", p.Backend, p.Name, note)
	}
}

// convertProvider maps an old provider onto this version's model.
func convertProvider(id string, op oldProvider) (p account.Provider, note string) {
	p = account.Provider{ID: id, Backend: op.Harness, BaseURL: op.BaseURL, WireAPI: op.WireAPI,
		Official: op.Official, Enabled: op.Enabled == nil || *op.Enabled, Tiers: op.Tiers}
	if pre, ok := account.Presets()[op.Preset]; ok {
		p.Preset = op.Preset
		if p.Backend == "" {
			p.Backend = pre.Backend
		}
		if p.BaseURL == "" {
			p.BaseURL = pre.BaseURL
		}
		p.Official = pre.Official
		p.Name = pre.Name
	} else if op.Preset != "" {
		note = fmt.Sprintf("; preset %q is unknown to this version and was dropped", op.Preset)
	}
	if op.Label != nil && strings.TrimSpace(*op.Label) != "" {
		p.Name = cleanLabel(*op.Label)
	}
	if p.Backend == config.BackendClaude {
		p.WireAPI = ""
	}
	p.AllowedModels = op.AllowedModels
	if strings.TrimSpace(p.AllowedModels) == "" {
		// The old version allowed no model for a provider with an empty pattern; keep it to the tier models.
		p.AllowedModels = tierPattern(p.Tiers)
		note += "; its empty allowedModels allowed no model, now it allows only the tier models"
	}
	p.Models = account.ModelsOf(p.AllowedModels)
	return p, note
}

func sameProvider(a, b account.Provider) bool {
	return a.Name == b.Name && a.Backend == b.Backend && a.Preset == b.Preset && a.BaseURL == b.BaseURL &&
		a.WireAPI == b.WireAPI && a.Official == b.Official && a.Enabled == b.Enabled &&
		a.AllowedModels == b.AllowedModels && a.Tiers == b.Tiers
}

// oldAccount is an account in the old accounts.json.
type oldAccount struct {
	ID        string `json:"id"`
	Backend   string `json:"backend"`
	Kind      string `json:"kind"`
	Auth      string `json:"auth"`
	Provider  string `json:"provider"`
	Label     string `json:"label"`
	Enabled   *bool  `json:"enabled"`
	Priority  int    `json:"priority"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

// importAccounts imports accounts.json with each account's profile folder and API key.
func (m *migrator) importAccounts(ctx context.Context, settings config.Settings) {
	data, err := fsutil.ReadFile(m.old("accounts.json"))
	if errors.Is(err, fs.ErrNotExist) {
		m.add(KindAccount, "", Skipped, "no accounts.json in the old folder")
		return
	}
	if err != nil {
		m.add(KindAccount, "", Error, "accounts.json: %v", err)
		return
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		var wrapped struct {
			Accounts []json.RawMessage `json:"accounts"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil || wrapped.Accounts == nil {
			m.add(KindAccount, "", Error, "accounts.json is not a list of accounts: %v", err)
			return
		}
		list = wrapped.Accounts
	}
	for i, raw := range list {
		var oa oldAccount
		if err := json.Unmarshal(raw, &oa); err != nil {
			m.add(KindAccount, fmt.Sprintf("#%d", i+1), Error, "unreadable: %v", err)
			continue
		}
		m.importAccount(ctx, oa, settings)
	}
}

func (m *migrator) importAccount(ctx context.Context, oa oldAccount, settings config.Settings) {
	if oa.Backend == "" {
		oa.Backend = config.BackendAgy
	}
	if oa.Backend == "opencode" || oa.Status == "removed" {
		m.add(KindAccount, oa.ID, Skipped, "OpenCode workers no longer exist")
		return
	}
	n := account.New{Backend: oa.Backend, Kind: account.KindIsolated, Label: cleanLabel(oa.Label),
		Enabled: oa.Enabled == nil || *oa.Enabled, Priority: min(max(oa.Priority, -1000), 1000)}
	switch {
	case oa.Kind == "system":
		n.Kind = account.KindSystem
	case oa.Auth == "apikey" || oa.Auth == "" && oa.Provider != "":
		n.Kind, n.Provider = account.KindAPIKey, oa.Provider
	}
	created, _ := time.Parse(time.RFC3339Nano, oa.CreatedAt)
	already := false
	a, err := m.accounts.Get(ctx, oa.ID)
	switch {
	case err == nil && (a.Backend != n.Backend || a.Kind != n.Kind || a.Provider != n.Provider):
		m.add(KindAccount, oa.ID, Kept, "an account with this id already exists as %s %s; it was left as it is "+
			"(its profile and key were not imported)", a.Backend, a.Kind)
		return
	case err == nil:
		already = true
		m.add(KindAccount, oa.ID, Unchanged, "already imported")
	case !errors.Is(err, account.ErrNotFound):
		m.add(KindAccount, oa.ID, Error, "%v", err)
		return
	default:
		if a, err = m.accounts.Import(ctx, oa.ID, n, created); err != nil {
			if errors.Is(err, account.ErrNotFound) && n.Provider != "" {
				err = fmt.Errorf("its provider %s was not imported", n.Provider)
			}
			m.add(KindAccount, oa.ID, Error, "%v", err)
			return
		}
		m.add(KindAccount, oa.ID, Imported, "%s %s account %q", a.Backend, a.Kind, a.Label)
	}
	if a.Kind != account.KindSystem {
		m.importProfile(a, settings.LinkIntoProfiles, already)
	}
	if a.Kind == account.KindAPIKey {
		m.importKey(ctx, a)
	}
}

// cleanLabel makes an old label valid: no control characters, at most 80 characters.
func cleanLabel(l string) string {
	l = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(l, ""))
	l = strings.TrimSpace(l)
	if r := []rune(l); len(r) > 80 {
		l = string(r[:80])
	}
	return l
}

// importProfile copies the account's profile home from the old folder, unless it is the same folder. already says
// the account was imported by an earlier run, whose copy an existing profile folder then is.
func (m *migrator) importProfile(a account.Account, links []string, already bool) {
	src := m.old("profiles", a.ID, "home")
	prof, err := m.profiles.For(a)
	if err != nil {
		m.add(KindProfile, a.ID, Error, "%v", err)
		return
	}
	if same, err := fsutil.SamePath(src, prof.Home); err == nil && same {
		m.add(KindProfile, a.ID, Unchanged, "the profile is already in place (%s)", prof.Home)
		return
	}
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		m.add(KindProfile, a.ID, Skipped, "no profile folder in the old data; sign the account in again")
		return
	}
	if exists(prof.Home) && already {
		m.add(KindProfile, a.ID, Unchanged, "the profile folder %s is already there", prof.Home)
		return
	}
	if exists(prof.Home) {
		m.add(KindProfile, a.ID, Kept, "%s already exists; it was left as it is", prof.Home)
		return
	}
	files, skipped, err := copyTree(src, prof.Home, m.o.DryRun)
	if err != nil {
		m.add(KindProfile, a.ID, Error, "copy %s: %v", src, err)
		return
	}
	detail := fmt.Sprintf("copied %d files to %s", files, prof.Home)
	if skipped > 0 {
		detail += fmt.Sprintf(" (%d entries left out: links, special files and the old plain-text key file)", skipped)
	}
	if !m.o.DryRun {
		if _, warnings, err := m.profiles.Ensure(a, links); err != nil {
			detail += "; linking home entries failed: " + err.Error()
		} else if len(warnings) > 0 {
			detail += "; " + strings.Join(warnings, "; ")
		}
	}
	m.add(KindProfile, a.ID, Imported, "%s", detail)
}

// copyTree copies the regular files and folders of src into dst (which must not exist), leaving out links (the
// old version linked entries of the real home; they are made again) and the old plain-text key file. A dry run
// only counts.
func copyTree(src, dst string, dryRun bool) (files, skipped int, err error) {
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0 || isLinkLike(info):
			skipped++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		case d.IsDir():
			if dryRun {
				return nil
			}
			return os.MkdirAll(target, 0o700)
		case !info.Mode().IsRegular():
			skipped++
			return nil
		case rel == oldKeyFile:
			skipped++
			return nil
		}
		files++
		if dryRun {
			return nil
		}
		return copyFile(path, target, info.Mode().Perm()|0o600)
	})
	return files, skipped, err
}

// importKey stores the API key of the old version's plain-text key file in this version's key store. The key is
// never shown; the report gives only its hint.
func (m *migrator) importKey(ctx context.Context, a account.Account) {
	path := m.old("profiles", a.ID, "home", oldKeyFile)
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		m.add(KindKey, a.ID, Skipped, "the old version had no API key stored; add it in the dashboard")
		return
	}
	if err != nil {
		m.add(KindKey, a.ID, Error, "%s: %v", path, err)
		return
	}
	var f struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &f); err != nil || strings.TrimSpace(f.Key) == "" {
		m.add(KindKey, a.ID, Error, "%s holds no readable key", path)
		return
	}
	hint := account.KeyHint(strings.TrimSpace(f.Key))
	if m.keys.Has(a.ID) {
		if a.KeyHint == hint {
			m.add(KindKey, a.ID, Unchanged, "the key (%s) is already stored", hint)
		} else {
			m.add(KindKey, a.ID, Kept, "a key (%s) is already stored; it was left as it is", a.KeyHint)
		}
		return
	}
	note := "; the old plain-text key file stays in the old profile until you delete it"
	if m.o.DryRun {
		m.add(KindKey, a.ID, Imported, "would store the key (%s) encrypted for this user%s", hint, note)
		return
	}
	if _, err := m.keys.Save(a.ID, f.Key); err != nil {
		m.add(KindKey, a.ID, Error, "%v", err)
		return
	}
	if err := m.accounts.SetKeyHint(ctx, a.ID, hint); err != nil {
		m.add(KindKey, a.ID, Error, "%v", err)
		return
	}
	m.add(KindKey, a.ID, Imported, "stored the key (%s)%s", hint, note)
}

// isLinkLike reports Windows junctions (which Go reports as irregular files) and other special files.
func isLinkLike(info fs.FileInfo) bool {
	return info.Mode()&(fs.ModeIrregular|fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0
}
