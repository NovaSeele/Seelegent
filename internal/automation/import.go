package automation

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// ImportOutcome says what Import did with one automation.
type ImportOutcome string

// Import outcomes.
const (
	// Imported: the automation was saved (with dryRun: would be).
	Imported ImportOutcome = "imported"
	// Unchanged: an automation with this id already does the same.
	Unchanged ImportOutcome = "unchanged"
	// Kept: an automation with this id exists and differs; it was left as it is.
	Kept ImportOutcome = "kept"
)

// importID is an id the old version or this one gives ("a" and 8 or 10 hex digits).
var importID = regexp.MustCompile(`^a[0-9a-f]{8,10}$`)

// Import saves an automation of the old version under its own id, always off and with no enable request: the
// user turns it on from the dashboard like any other (old bug D8). createdAt is kept when it is a valid time. An
// automation with this id that does the same thing is Unchanged, one that differs is Kept. With dryRun nothing is
// written. The stream is not told: the daemon is not running while data is imported.
func (s *Service) Import(ctx context.Context, id string, in api.AutomationInput, createdAt string,
	dryRun bool,
) (ImportOutcome, api.Automation, error) {
	if !importID.MatchString(id) {
		return "", api.Automation{}, invalidf("the automation has no valid id")
	}
	now := api.FormatTime(s.now())
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		createdAt = api.FormatTime(t)
	} else {
		createdAt = now
	}
	r := &rec{Automation: api.Automation{ID: id, Name: in.Name, Prompt: in.Prompt, Cwd: in.Cwd,
		Schedule: in.Schedule, Timezone: in.Timezone, Member: in.Member, Backend: in.Backend, Model: in.Model,
		Gate: in.Gate, GateCommands: in.GateCommands, Lane: in.Lane, NoLane: in.NoLane, Precheck: in.Precheck,
		Quota: api.AutomationQuota{MinRemaining: defaultMinLeft}, GraceMinutes: defaultGrace, CreatedAt: createdAt,
		UpdatedAt: now}}
	if in.Quota != nil {
		r.Quota = *in.Quota
	}
	if in.GraceMinutes != nil {
		r.GraceMinutes = *in.GraceMinutes
	}
	if err := s.validate(r); err != nil {
		return "", api.Automation{}, err
	}
	outcome := Imported
	write := func(tx *sql.Tx) error {
		cur, err := loadRec(ctx, tx, id)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			return err
		case sameImport(cur, r):
			outcome = Unchanged
			return nil
		default:
			outcome = Kept
			return nil
		}
		if dryRun {
			return nil
		}
		return saveRec(ctx, tx, r)
	}
	var err error
	if dryRun {
		err = s.o.DB.Read(ctx, write)
	} else {
		err = s.o.DB.Write(ctx, write)
	}
	if err != nil {
		return "", api.Automation{}, err
	}
	return outcome, r.Automation, nil
}

// sameImport reports whether cur does what the imported automation r does.
func sameImport(cur, r *rec) bool {
	return cur.contentHash() == r.contentHash() && cur.Name == r.Name && cur.Quota == r.Quota &&
		cur.GraceMinutes == r.GraceMinutes
}
