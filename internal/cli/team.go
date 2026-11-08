package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// The team commands (milestone 14): members, consult, notes and hooks here; meeting and contest in meeting.go.

// group is a parent command whose subcommands do the work.
func group(use, short, usage string, subs ...*cobra.Command) *cobra.Command {
	name := strings.Fields(use)[0]
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown %s command %q; usage: %s", name, args[0], usage)
			}
			return usagef("usage: %s", usage)
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(subs...)
	return cmd
}

// leaf is a subcommand that talks to the daemon. op and action name it for the worker guard (ops.Mutating).
func leaf(d deps, use, short, op, action string, args cobra.PositionalArgs,
	run func(cmd *cobra.Command, c *client.Client, args []string) error) *cobra.Command {
	return &cobra.Command{
		Use:         use,
		Short:       short,
		Args:        args,
		Annotations: map[string]string{annOp: op, annAction: action},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := d.daemonClient(cmd)
			if err != nil {
				return err
			}
			return run(cmd, c, args)
		},
	}
}

// checked runs check after base: both before the daemon is called (cobra parses the flags before the arguments
// are checked).
func checked(base cobra.PositionalArgs, check func(cmd *cobra.Command, args []string) error) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := base(cmd, args); err != nil {
			return err
		}
		return check(cmd, args)
	}
}

// ---- members ----------------------------------------------------------------------------------------------------

const membersUsage = "seelegent members list|track|add|set|remove"

// memberFlags are the fields of members add and set.
type memberFlags struct {
	id, name, backend, provider, rank, tier, model, role string
	preferred, fallback                                  []string
	enabled                                              bool
}

func (f *memberFlags) add(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.StringVar(&f.id, "id", "", "member id (default: made from the name)")
	fs.StringVar(&f.name, "name", "", "display name")
	fs.StringVar(&f.backend, "backend", "", "agy, claude or codex")
	fs.StringVar(&f.provider, "provider", "", "API-key provider")
	fs.StringVar(&f.rank, "rank", "", "worker, senior or peer")
	fs.StringVar(&f.tier, "tier", "", "fast, standard, strong or max")
	fs.StringVar(&f.model, "model", "", "model id")
	fs.StringVar(&f.role, "role", "", "worker role (profile) id")
	fs.StringSliceVar(&f.preferred, "prefer", nil, "preferred account ids, comma separated")
	fs.StringSliceVar(&f.fallback, "fallback", nil, "fallback backends, comma separated")
	fs.BoolVar(&f.enabled, "enabled", true, "whether the member gets new work")
}

// patch is the JSON merge patch of the flags that were given.
func (f *memberFlags) patch(cmd *cobra.Command) map[string]any {
	p := map[string]any{}
	set := func(flag, key string, v any) {
		if cmd.Flags().Changed(flag) {
			p[key] = v
		}
	}
	set("name", "name", f.name)
	set("backend", "backend", f.backend)
	set("provider", "provider", f.provider)
	set("rank", "rank", f.rank)
	set("tier", "tier", f.tier)
	set("model", "model", f.model)
	set("role", "role", f.role)
	set("prefer", "preferredAccounts", splitList(f.preferred))
	set("fallback", "fallbackBackends", splitList(f.fallback))
	set("enabled", "enabled", f.enabled)
	return p
}

func newMembersCommand(d deps) *cobra.Command {
	list := leaf(d, "list", "List the department members", "members", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			ms, err := c.Members(cmd.Context())
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(ms), func(w io.Writer) { printMembers(w, ms) })
		})
	track := leaf(d, "track", "What each member's meetings and consults came to", "members", "track", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			ts, err := c.MemberTrack(cmd.Context())
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(ts), func(w io.Writer) {
				for i := range ts {
					t := &ts[i]
					_, _ = fmt.Fprintf(w, "%-20s meetings %d, dissents %d (vindicated %d, dismissed %d), consults %d, "+
						"objections %d\n", t.Member, t.Meetings, t.Dissents, t.Vindicated, t.Dismissed, t.Consults,
						t.Objections)
				}
			})
		})
	var af memberFlags
	add := leaf(d, "add --name N --backend B --rank R [flags]", "Add a member", "members", "add", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			en := af.enabled
			m, err := c.CreateMember(cmd.Context(), api.MemberInput{ID: af.id, Name: af.name, Backend: af.backend,
				Provider: af.provider, Rank: af.rank, Tier: af.tier, Model: af.model, Role: af.role,
				PreferredAccounts: splitList(af.preferred), FallbackBackends: splitList(af.fallback), Enabled: &en})
			if err != nil {
				return err
			}
			return emit(cmd, m, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Member %s added.\n", m.ID) })
		})
	af.add(add)
	var sf memberFlags
	set := leaf(d, "set <id> [flags]", "Change a member (only the flags given)", "members", "set",
		checked(exactArgs(1, "seelegent members set <id> [flags]"), func(cmd *cobra.Command, _ []string) error {
			if len(sf.patch(cmd)) == 0 {
				return usagef("nothing to change: pass at least one flag")
			}
			return nil
		}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			m, err := c.PatchMember(cmd.Context(), args[0], sf.patch(cmd))
			if err != nil {
				return err
			}
			return emit(cmd, m, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Member %s updated.\n", m.ID) })
		})
	sf.add(set)
	remove := leaf(d, "remove <id>", "Remove a member", "members", "remove",
		exactArgs(1, "seelegent members remove <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if err := c.DeleteMember(cmd.Context(), args[0]); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"ok": true, "removed": args[0]}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "Member %s removed.\n", args[0])
			})
		})
	remove.Aliases = []string{"rm"}
	return group("members <command>", "Department members: named workers with a backend, rank and role",
		membersUsage, list, track, add, set, remove)
}

func printMembers(w io.Writer, ms []api.Member) {
	if len(ms) == 0 {
		_, _ = fmt.Fprintln(w, "No members.")
		return
	}
	for i := range ms {
		m := &ms[i]
		state := "on"
		switch {
		case m.Invalid != "":
			state = "invalid: " + m.Invalid
		case !m.Enabled:
			state = "off"
		}
		model := m.Model
		if model == "" {
			model = m.Tier
		}
		_, _ = fmt.Fprintf(w, "%-20s %-24s %-6s %-7s %-20s %s\n", m.ID, m.Name, m.Backend, m.Rank, model, state)
	}
}

// ---- consult ----------------------------------------------------------------------------------------------------

func newConsultCommand(d deps) *cobra.Command {
	var member, focus string
	var wait int
	cmd := leaf(d, "consult <task-id> --member M [--focus T] [--wait N]",
		"Ask a senior or peer member for a second opinion on a finished task", "consult", "",
		checked(exactArgs(1, "seelegent consult <task-id> --member <id> [--focus text] [--wait seconds]"),
			func(*cobra.Command, []string) error {
				if member == "" {
					return usagef("missing required --member <id>")
				}
				if wait < 0 {
					return usagef("--wait must be 0 or more")
				}
				return nil
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			rec, err := c.Consult(cmd.Context(), args[0], api.ConsultRequest{Member: member, Focus: focus})
			if err != nil {
				return err
			}
			if rec, err = pollConsult(cmd, c, args[0], rec, wait); err != nil {
				return err
			}
			return emit(cmd, rec, func(w io.Writer) { printConsult(w, &rec) })
		})
	cmd.Flags().StringVar(&member, "member", "", "member id (rank senior or peer)")
	cmd.Flags().StringVar(&focus, "focus", "", "what to look at hardest")
	cmd.Flags().IntVar(&wait, "wait", defaultWait, "seconds to wait for the verdict (0: return at once)")
	return cmd
}

func printConsult(w io.Writer, c *api.Consult) {
	_, _ = fmt.Fprintf(w, "Consult %s by %s (%s): %s", c.ID, c.Member, c.Rank, c.Status)
	if c.Verdict != "" {
		_, _ = fmt.Fprintf(w, ", verdict %s", c.Verdict)
	}
	_, _ = fmt.Fprintln(w)
	for i := range c.Findings {
		f := &c.Findings[i]
		_, _ = fmt.Fprintf(w, "  - %s\n", f.Text)
		for _, ct := range f.Cites {
			_, _ = fmt.Fprintf(w, "      %s:%d %s\n", ct.Path, ct.Line, ct.State)
		}
	}
	for _, r := range c.Required {
		_, _ = fmt.Fprintf(w, "  required: %s\n", r)
	}
	if c.Error != "" {
		_, _ = fmt.Fprintf(w, "Error: %s\n", c.Error)
	}
	if c.Verdict == api.ConsultObject && c.Rank == api.RankPeer && c.AnsweredObjection == nil {
		_, _ = fmt.Fprintln(w, "A peer objects: `review <id> accepted` needs --answer-objection.")
	}
}

// ---- notes ------------------------------------------------------------------------------------------------------

const notesUsage = "seelegent notes list|add|resolve|reopen|delete|send <task-id> ..."

func newNotesCommand(d deps) *cobra.Command {
	list := leaf(d, "list <task-id>", "A task's review notes", "notes", "list",
		exactArgs(1, "seelegent notes list <task-id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			ns, err := c.Notes(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(ns), func(w io.Writer) {
				if len(ns) == 0 {
					_, _ = fmt.Fprintln(w, "No notes.")
				}
				for i := range ns {
					n := &ns[i]
					_, _ = fmt.Fprintf(w, "%-6s %-11s %s:%d  %s\n", n.ID, n.Status, n.File, n.Line, n.Body)
				}
			})
		})
	var file, side string
	var line, endLine int
	add := leaf(d, "add <task-id> --file F --line N <body|@file>", "Add a note on a line of the task's diff", "notes",
		"add", checked(minArgs("seelegent notes add <task-id> --file <path> --line <n> <body|@file>"),
			func(_ *cobra.Command, args []string) error {
				if file == "" || line <= 0 || len(args) < 2 {
					return usagef("notes add needs --file, --line and a body")
				}
				return nil
			}),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			body, err := fileArg(strings.Join(args[1:], " "), "")
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return usagef("the note body is empty")
			}
			n, err := c.AddNote(cmd.Context(), args[0], api.NoteInput{File: file, Line: line, EndLine: endLine,
				Side: side, Body: body, By: "lead"})
			if err != nil {
				return err
			}
			return emit(cmd, n, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Note %s added.\n", n.ID) })
		})
	add.Flags().StringVar(&file, "file", "", "path relative to the repository root")
	add.Flags().IntVar(&line, "line", 0, "line number")
	add.Flags().IntVar(&endLine, "end-line", 0, "last line of a range")
	add.Flags().StringVar(&side, "side", "", "new (default) or old")
	state := func(name, to string) *cobra.Command {
		return leaf(d, name+" <task-id> <note-id>", strings.ToUpper(name[:1])+name[1:]+" a note", "notes", name,
			exactArgs(2, "seelegent notes "+name+" <task-id> <note-id>"),
			func(cmd *cobra.Command, c *client.Client, args []string) error {
				n, err := c.PatchNote(cmd.Context(), args[0], args[1], api.NotePatch{State: to})
				if err != nil {
					return err
				}
				return emit(cmd, n, func(w io.Writer) { _, _ = fmt.Fprintf(w, "Note %s: %s.\n", n.ID, n.Status) })
			})
	}
	del := leaf(d, "delete <task-id> <note-id>", "Delete a note", "notes", "delete",
		exactArgs(2, "seelegent notes delete <task-id> <note-id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			if err := c.DeleteNote(cmd.Context(), args[0], args[1]); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"ok": true, "deleted": args[1]}, func(w io.Writer) {
				_, _ = fmt.Fprintf(w, "Note %s deleted.\n", args[1])
			})
		})
	var extra string
	var wait int
	send := leaf(d, "send <task-id> [--extra T] [--wait N]", "Send the draft notes to the worker as one followup",
		"notes", "send", exactArgs(1, "seelegent notes send <task-id> [--extra text|@file] [--wait seconds]"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			text, err := fileArg(extra, "")
			if err != nil {
				return err
			}
			mark := time.Now()
			res, err := c.SendNotes(cmd.Context(), args[0], api.NotesSendRequest{Extra: text, By: "lead"})
			if err != nil {
				return err
			}
			if wait <= 0 {
				return emit(cmd, res, func(w io.Writer) {
					_, _ = fmt.Fprintf(w, "%d note(s) sent to task %s.\n", len(res.Sent), args[0])
				})
			}
			return waitAndPrint(cmd, d, c, args[0], wait, mark,
				fmt.Sprintf("%d note(s) sent to task %s.", len(res.Sent), args[0]))
		})
	send.Flags().StringVar(&extra, "extra", "", "text after the notes (or @file)")
	send.Flags().IntVar(&wait, "wait", defaultWait, "seconds to wait for the report (0: return at once)")
	return group("notes <command>", "Review notes on a task's diff, sent to the worker as one followup", notesUsage,
		list, add, state("resolve", api.NoteResolved), state("reopen", api.NoteOpen), del, send)
}

// ---- hooks ------------------------------------------------------------------------------------------------------

const hooksUsage = "seelegent hooks list|set <file.json>|test <id>"

func newHooksCommand(d deps) *cobra.Command {
	list := leaf(d, "list", "The event hooks", "hooks", "list", noArgs,
		func(cmd *cobra.Command, c *client.Client, _ []string) error {
			hs, err := c.Hooks(cmd.Context())
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(hs), func(w io.Writer) {
				if len(hs) == 0 {
					_, _ = fmt.Fprintln(w, "No hooks.")
				}
				for i := range hs {
					h := &hs[i]
					state := "on"
					if !h.Enabled {
						state = "off"
					}
					_, _ = fmt.Fprintf(w, "%-10s %-3s %-40s %s\n", h.ID, state, strings.Join(h.On, ","), h.Run)
				}
			})
		})
	set := leaf(d, "set <file.json>", "Replace the hook list with a JSON file ({\"hooks\": [...]} or a list)",
		"hooks", "set", exactArgs(1, "seelegent hooks set <file.json>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var req api.HooksRequest
			if err := json.Unmarshal(data, &req); err != nil || req.Hooks == nil {
				if err := json.Unmarshal(data, &req.Hooks); err != nil {
					return usagef("%s is neither {\"hooks\": [...]} nor a list of hooks", args[0])
				}
			}
			hs, err := c.ReplaceHooks(cmd.Context(), req.Hooks)
			if err != nil {
				return err
			}
			return emit(cmd, api.NonNil(hs), func(w io.Writer) { _, _ = fmt.Fprintf(w, "%d hook(s) saved.\n", len(hs)) })
		})
	test := leaf(d, "test <id>", "Run a hook now with a hook.test event", "hooks", "test",
		exactArgs(1, "seelegent hooks test <id>"),
		func(cmd *cobra.Command, c *client.Client, args []string) error {
			r, err := c.TestHook(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emit(cmd, r, func(w io.Writer) {
				code := "none"
				if r.ExitCode != nil {
					code = fmt.Sprint(*r.ExitCode)
				}
				_, _ = fmt.Fprintf(w, "Hook %s: exit %s, timed out %v\n%s\n", r.HookID, code, r.TimedOut,
					strings.TrimRight(r.Output, "\n"))
			})
		})
	return group("hooks <command>", "Commands the daemon runs on task, account and daemon events", hooksUsage,
		list, set, test)
}
