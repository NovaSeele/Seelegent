package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/automation"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// oldAutomation is one record of the old automations.json (a JSON list).
type oldAutomation struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	Cwd      string `json:"cwd"`
	Schedule struct {
		Every   string `json:"every"`
		At      string `json:"at"`
		Weekday *int   `json:"weekday"`
		Cron    string `json:"cron"`
	} `json:"schedule"`
	Timezone string `json:"timezone"`
	Member   string `json:"member"`
	Backend  string `json:"backend"`
	Model    string `json:"model"`
	// Gate is null (the default gate), false (none), a command or a list of commands.
	Gate json.RawMessage `json:"gate"`
	// Lane is null (the default lane), false (no lane) or a list of paths.
	Lane     json.RawMessage `json:"lane"`
	Precheck string          `json:"precheck"`
	Quota    *struct {
		MinRemaining   *float64 `json:"minRemaining"`
		UseBeforeReset bool     `json:"useBeforeReset"`
	} `json:"quota"`
	GraceMinutes *int   `json:"graceMinutes"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"createdAt"`
}

// input turns the old record into this version's automation input.
func (a oldAutomation) input() (api.AutomationInput, error) {
	in := api.AutomationInput{Name: a.Name, Prompt: a.Prompt, Cwd: a.Cwd, Timezone: a.Timezone, Member: a.Member,
		Backend: a.Backend, Model: a.Model, Precheck: a.Precheck, GraceMinutes: a.GraceMinutes,
		Schedule: api.Schedule{Every: a.Schedule.Every, At: a.Schedule.At, Weekday: a.Schedule.Weekday,
			Cron: a.Schedule.Cron}}
	if a.Schedule.Cron != "" {
		in.Schedule = api.Schedule{Cron: a.Schedule.Cron}
	}
	switch string(a.Gate) {
	case "", "null", "true":
	case "false":
		off := false
		in.Gate = &off
	default:
		var one string
		if err := json.Unmarshal(a.Gate, &one); err == nil {
			in.GateCommands = []string{one}
		} else if err := json.Unmarshal(a.Gate, &in.GateCommands); err != nil {
			return in, errors.New("gate must be a command, a list of commands or false")
		}
	}
	switch l := string(a.Lane); l {
	case "", "null":
	case "false":
		in.NoLane = true
	default:
		if err := json.Unmarshal(a.Lane, &in.Lane); err != nil {
			return in, errors.New("lane must be a list of paths or false")
		}
	}
	if a.Quota != nil {
		q := api.AutomationQuota{MinRemaining: 20, UseBeforeReset: a.Quota.UseBeforeReset}
		if a.Quota.MinRemaining != nil {
			q.MinRemaining = *a.Quota.MinRemaining
		}
		in.Quota = &q
	}
	return in, nil
}

// importAutomations imports automations.json. Every automation arrives off, whatever it was in the old version
// (old bug D8): the user turns each on from the dashboard after reading what it runs. Run history is not imported.
func (m *migrator) importAutomations(ctx context.Context) {
	data, err := fsutil.ReadFile(m.old("automations.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		m.add(KindAutomation, "automations.json", Error, "%v", err)
		return
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		m.add(KindAutomation, "automations.json", Error, "not an automations file (it needs a list): %v", err)
		return
	}
	if len(list) == 0 {
		m.add(KindAutomation, "automations.json", Skipped, "no automations")
		return
	}
	for i, raw := range list {
		var a oldAutomation
		if err := json.Unmarshal(raw, &a); err != nil {
			m.add(KindAutomation, fmt.Sprintf("#%d", i+1), Error, "unreadable: %v", err)
			continue
		}
		id := a.ID
		if id == "" {
			id = fmt.Sprintf("#%d", i+1)
		}
		in, err := a.input()
		if err != nil {
			m.add(KindAutomation, id, Error, "%v", err)
			continue
		}
		outcome, v, err := m.automations.Import(ctx, a.ID, in, a.CreatedAt, m.o.DryRun)
		if err != nil {
			m.add(KindAutomation, id, Error, "%v", err)
			continue
		}
		switch outcome {
		case automation.Unchanged:
			m.add(KindAutomation, id, Unchanged, "already imported")
		case automation.Kept:
			m.add(KindAutomation, id, Kept, "an automation with this id already exists and differs; it was left "+
				"as it is")
		default:
			was := ""
			if a.Enabled {
				was = " (it was on in the old version)"
			}
			m.add(KindAutomation, id, Imported, "%q, off%s: turn it on from the dashboard", v.Name, was)
		}
	}
}
