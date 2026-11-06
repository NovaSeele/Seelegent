package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// dirPerm is the permission of profile directories: they hold sign-in tokens.
const dirPerm = 0o700

// Profiles manages the isolated CLI profiles under the profiles directory: profiles/<id>/ for the account's own
// files (the API key) and profiles/<id>/home, the home directory the worker CLI sees.
type Profiles struct {
	root     string
	realHome string
}

// NewProfiles returns the profiles under root (config.Paths.Profiles); realHome is the user's real home directory,
// used by system accounts and as the source of linked entries.
func NewProfiles(root, realHome string) Profiles {
	return Profiles{root: root, realHome: realHome}
}

// Profile is where one account's CLI keeps its state.
type Profile struct {
	// AccountID is the account the profile belongs to.
	AccountID string
	// Kind is the account kind.
	Kind Kind
	// Dir is profiles/<id>; empty for a system account.
	Dir string
	// Home is the home directory the CLI sees: Dir/home, or the real home for a system account.
	Home string
	// RealHome is the user's real home directory.
	RealHome string
}

// Isolated reports whether the profile has its own home directory.
func (p Profile) Isolated() bool { return p.Kind != KindSystem }

// Dir returns profiles/<id>, refusing ids that could leave the profiles directory.
func (p Profiles) Dir(id string) (string, error) {
	if !validID(id) {
		return "", invalidf("account id %q", id)
	}
	if p.root == "" {
		return "", errors.New("profiles directory not set")
	}
	return filepath.Join(p.root, id), nil
}

// Exists reports whether something already occupies profiles/<id> (for example a profile left by the old version);
// new accounts never get such an id.
func (p Profiles) Exists(id string) bool {
	dir, err := p.Dir(id)
	if err != nil {
		return false
	}
	_, err = os.Lstat(dir)
	return err == nil
}

// For returns the profile of the account without touching the disk.
func (p Profiles) For(a Account) (Profile, error) {
	prof := Profile{AccountID: a.ID, Kind: a.Kind, RealHome: p.realHome}
	if a.Kind == KindSystem {
		prof.Home = p.realHome
		return prof, nil
	}
	dir, err := p.Dir(a.ID)
	if err != nil {
		return Profile{}, err
	}
	prof.Dir = dir
	prof.Home = filepath.Join(dir, "home")
	return prof, nil
}

// Ensure creates the profile directories of an isolated or API-key account and links the configured entries of the
// real home into its home (see config.Settings.LinkIntoProfiles). Linking is best effort: entries missing from the
// real home are skipped, entries already present in the profile are left alone, and other failures come back as
// warnings rather than an error, because a worker can still run without them.
func (p Profiles) Ensure(a Account, links []string) (prof Profile, warnings []string, err error) {
	prof, err = p.For(a)
	if err != nil || !prof.Isolated() {
		return prof, nil, err
	}
	if err := os.MkdirAll(prof.Home, dirPerm); err != nil {
		return Profile{}, nil, fmt.Errorf("create profile of %s: %w", a.ID, err)
	}
	if p.realHome == "" {
		return prof, nil, nil
	}
	for _, entry := range links {
		rel, ok := cleanLinkEntry(entry)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("link %q: not a path inside the home directory", entry))
			continue
		}
		src := filepath.Join(p.realHome, rel)
		if _, err := os.Stat(src); err != nil {
			continue // nothing to link
		}
		dst := filepath.Join(prof.Home, rel)
		if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
			warnings = append(warnings, fmt.Sprintf("link %s: %v", rel, err))
			continue
		}
		if err := platform.LinkInto(src, dst); err != nil && !errors.Is(err, fs.ErrExist) {
			warnings = append(warnings, err.Error())
		}
	}
	return prof, warnings, nil
}

// cleanLinkEntry turns a linkIntoProfiles entry into a clean relative path, refusing anything that leaves home.
func cleanLinkEntry(entry string) (string, bool) {
	if strings.TrimSpace(entry) == "" || strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, `\`) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(entry))
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

// Remove deletes profiles/<id> and everything in it. Links into the real home (symbolic links, junctions) are
// removed as links: the walk never descends into them, so the user's real files are never touched. Hard-linked
// files lose only their profile name. A missing profile is not an error.
func (p Profiles) Remove(id string) error {
	if id == SystemID {
		return invalidf("the system account has no profile to remove")
	}
	dir, err := p.Dir(id)
	if err != nil {
		return err
	}
	if err := removeTree(dir); err != nil {
		return fmt.Errorf("remove profile of %s: %w", id, err)
	}
	return nil
}

// removeTree removes path without following links.
func removeTree(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() || isLink(fi) {
		return removeEntry(path, fi)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, removeTree(filepath.Join(path, e.Name())))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return removeEntry(path, fi)
}

// removeEntry removes one file, empty directory or link, clearing a read-only bit if that is what stops it.
func removeEntry(path string, fi fs.FileInfo) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if !isLink(fi) && fi.Mode().Perm()&0o200 == 0 {
		if os.Chmod(path, fi.Mode().Perm()|0o200) == nil {
			if err2 := os.Remove(path); err2 == nil {
				return nil
			}
		}
	}
	return err
}
