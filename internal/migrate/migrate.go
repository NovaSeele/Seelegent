// Package migrate imports the data of the old (Node) Seelegent into this version: settings, providers, accounts
// with their profile folders and API keys, project memory, review history, worker roles, department members, lead
// hooks, meeting records and automations (every one off). It never changes or deletes anything in the old data folder, a dry run writes
// nothing, and a second run reports "unchanged" for everything it imported before. Each piece is reported on its
// own, so one unreadable file does not stop the rest.
//
// Tasks (and with them their review notes), contests, automation run history, tickets, proof records, usage
// history and quota caches are not imported (see docs/setup.md).
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/automation"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/hook"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/member"
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
	KindSettings   = "settings"
	KindProvider   = "provider"
	KindAccount    = "account"
	KindProfile    = "profile"
	KindKey        = "key"
	KindMemory     = "memory"
	KindHistory    = "history"
	KindRole       = "role"
	KindData       = "data"
	KindMember     = "member"
	KindHook       = "hook"
	KindMeeting    = "meeting"
	KindAutomation = "automation"
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
var ErrDaemonRunning = errors.New("the Seelegent daemon is running; stop it (seelegent stop) and run migrate again")

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
	m.team = team{members: member.New(member.Options{DB: db, Providers: m.accounts, Now: o.Now}),
		meetings: meeting.New(meeting.Options{DB: db, Now: o.Now}), hooks: hook.New(hook.Options{DB: db, Now: o.Now})}
	defer m.team.meetings.Close()
	defer m.team.hooks.Close(context.WithoutCancel(ctx))
	m.automations = automation.New(automation.Options{DB: db, Clock: nowClock(o.Now)})
	defer m.automations.Close()
	settings, providers := m.importSettings()
	m.importProviders(ctx, providers, settings)
	m.importAccounts(ctx, settings)
	projects := m.projects()
	m.importMemory(ctx, projects)
	m.importHistory(ctx, projects)
	m.importRoles(ctx, projects)
	m.importMembers(ctx)
	m.importHooks(ctx)
	m.importMeetings(ctx)
	m.importAutomations(ctx)
	m.notImported()
	if err := ctx.Err(); err != nil {
		return rep, err
	}
	if !o.DryRun {
		if err := writeErrorsFile(o.Paths.Home, rep, o.Now()); err != nil {
			rep.Warnings = append(rep.Warnings, err.Error())
		}
	}
	return rep, nil
}

// ErrorsFile is the file a run that ended with errors leaves in the data folder, and a later run without errors
// removes: the installer runs migrate only while seelegent.db does not exist, so it reads this file to say that
// the last migrate did not import everything.
const ErrorsFile = "migrate-errors.json"

// errorsRecord is the content of ErrorsFile.
type errorsRecord struct {
	At     string `json:"at"`
	From   string `json:"from"`
	Errors int    `json:"errors"`
	Items  []Item `json:"items"`
}

// writeErrorsFile writes ErrorsFile into home when rep has errors, and removes it when it has none.
func writeErrorsFile(home string, rep Report, now time.Time) error {
	path := filepath.Join(home, ErrorsFile)
	var failed []Item
	for _, it := range rep.Items {
		if it.Action == Error {
			failed = append(failed, it)
		}
	}
	if len(failed) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(errorsRecord{At: now.UTC().Format(time.RFC3339), From: rep.From,
		Errors: len(failed), Items: failed}, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
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
	team     team
	// automations is never started: it only saves the imported automations.
	automations *automation.Service
}

// nowClock is Options.Now as a clock.
type nowClock func() time.Time

func (c nowClock) Now() time.Time                     { return c() }
func (nowClock) NewTimer(d time.Duration) clock.Timer { return clock.Real().NewTimer(d) }

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
