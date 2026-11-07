// Package migrate imports the data of the old (Node) Seelegent into this version: settings, providers, accounts
// with their profile folders and API keys, project memory, review history and worker roles. It never changes or
// deletes anything in the old data folder, a dry run writes nothing, and a second run reports "unchanged" for
// everything it imported before. Each piece is reported on its own, so one unreadable file does not stop the rest.
//
// Tasks, members, notes, meetings, contests, automations, hooks, proof records, usage history and quota caches are
// not imported (see docs/setup.md).
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// Action is what happened to one item.
type Action string

// Actions.
const (
	// Imported: the item was copied into this version (in a dry run: would be).
	Imported Action = "imported"
	// Unchanged: this version already holds the same thing (an earlier run imported it).
	Unchanged Action = "unchanged"
	// Kept: this version already holds something different under the same name; it was left as it is.
	Kept Action = "kept"
	// Skipped: there is nothing to import, or this version does not import that kind of data.
	Skipped Action = "skipped"
	// Error: the item could not be read or imported; Detail says why.
	Error Action = "error"
)

// Item kinds.
const (
	KindSettings = "settings"
	KindProvider = "provider"
	KindAccount  = "account"
	KindProfile  = "profile"
	KindKey      = "key"
	KindMemory   = "memory"
	KindHistory  = "history"
	KindRole     = "role"
	KindData     = "data"
)

// Item is the outcome for one piece of old data.
type Item struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Action Action `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// Report is the result of a run.
type Report struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	DryRun   bool     `json:"dryRun"`
	Items    []Item   `json:"items"`
	Warnings []string `json:"warnings,omitempty"`
}

// Failed reports whether any item failed.
func (r Report) Failed() bool {
	return slices.ContainsFunc(r.Items, func(it Item) bool { return it.Action == Error })
}

// Options say what to import from where.
type Options struct {
	// From is the old data folder (the old version used $SEELEGENT_HOME or ~/.seelegent, the same place as this
	// version).
	From string
	// Paths is this version's data folder.
	Paths config.Paths
	// RealHome is the user's home folder: the source of the entries linked into copied profiles.
	RealHome string
	// DryRun reports what would be imported without writing anything.
	DryRun bool
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// ErrNotOldData is returned when From does not look like an old Seelegent data folder.
var ErrNotOldData = errors.New("not an old Seelegent data folder")

// ErrDaemonRunning is returned when the daemon holds the data folder.
var ErrDaemonRunning = errors.New("the Seelegent daemon is running; stop it (seelegent daemon stop) and run migrate again")

// oldMarkers are files only the old version wrote.
var oldMarkers = []string{"settings.json", "accounts.json", "roles.json", "memory"}

// Run imports the old data folder into this version.
func Run(ctx context.Context, o Options) (Report, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	from, err := filepath.Abs(o.From)
	if err != nil {
		return Report{}, err
	}
	o.From = from
	rep := Report{From: from, To: o.Paths.Home, DryRun: o.DryRun, Items: []Item{}}
	if st, err := os.Stat(from); err != nil || !st.IsDir() {
		return rep, fmt.Errorf("%w: %s is not a folder", ErrNotOldData, from)
	}
	if !slices.ContainsFunc(oldMarkers, func(n string) bool { return exists(filepath.Join(from, n)) }) {
		return rep, fmt.Errorf("%w: %s has none of %v", ErrNotOldData, from, oldMarkers)
	}
	dbPath := o.Paths.DB
	if o.DryRun {
		if held(o.Paths.LockFile) {
			rep.Warnings = append(rep.Warnings, "the daemon is running; stop it before the real migrate")
		}
		tmp, err := os.MkdirTemp("", "seelegent-migrate-")
		if err != nil {
			return rep, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		if dbPath, err = copyDB(o.Paths.DB, tmp); err != nil {
			return rep, fmt.Errorf("copy the database for the dry run: %w", err)
		}
	} else {
		if err := o.Paths.EnsureDirs(); err != nil {
			return rep, err
		}
		lock, err := fsutil.TryLock(o.Paths.LockFile)
		if errors.Is(err, fsutil.ErrLocked) {
			return rep, ErrDaemonRunning
		}
		if err != nil {
			return rep, err
		}
		defer func() { _ = lock.Unlock() }()
	}
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return rep, err
	}
	defer func() { _ = db.Close() }()
	profiles := account.NewProfiles(o.Paths.Profiles, o.RealHome)
	m := &migrator{o: o, rep: &rep, accounts: account.NewRepo(db, o.Now), profiles: profiles,
		keys: account.NewKeys(profiles), memory: memory.New(db, o.Now),
		roles: role.New(role.Options{DB: db, Home: o.RealHome, Now: o.Now})}
	settings, providers := m.importSettings()
	m.importProviders(ctx, providers, settings)
	m.importAccounts(ctx, settings)
	projects := m.projects()
	m.importMemory(ctx, projects)
	m.importHistory(ctx, projects)
	m.importRoles(ctx, projects)
	m.notImported()
	return rep, ctx.Err()
}

// migrator carries one run.
type migrator struct {
	o        Options
	rep      *Report
	accounts *account.Repo
	profiles account.Profiles
	keys     account.Keys
	memory   *memory.Store
	roles    *role.Store
}

func (m *migrator) add(kind, id string, a Action, format string, args ...any) {
	m.rep.Items = append(m.rep.Items, Item{Kind: kind, ID: id, Action: a, Detail: fmt.Sprintf(format, args...)})
}

// old joins elem onto the old data folder.
func (m *migrator) old(elem ...string) string {
	return filepath.Join(append([]string{m.o.From}, elem...)...)
}

// held reports whether another process holds the lock file (without creating it).
func held(path string) bool {
	if !exists(path) {
		return false
	}
	l, err := fsutil.TryLock(path)
	if err != nil {
		return errors.Is(err, fsutil.ErrLocked)
	}
	_ = l.Unlock()
	return false
}

// copyDB copies the database (with its WAL files) into dir for a dry run; a missing database gives a new one there.
func copyDB(src, dir string) (string, error) {
	dst := filepath.Join(dir, filepath.Base(src))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(src+suffix, dst+suffix, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return dst, nil
}

// copyFile copies one regular file.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
