// Package setup registers Seelegent with the lead hosts on this machine: it adds the `seelegent mcp` server to each
// host's MCP configuration, installs the lead skill where the host reads skills, and optionally installs the
// Claude Code reminder hook. It detects the hosts, never touches an entry it did not write (unless forced), backs
// up every config file before changing it, and is idempotent: a second run reports "unchanged".
//
// Everything that reaches the machine (home directory, environment, PATH, host CLIs, clock) comes through Env, so
// tests run against temporary homes only.
package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Action is what setup did (or, in a dry run, would do) with one item.
type Action string

// Actions.
const (
	ActionAdded     Action = "added"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionRemoved   Action = "removed"
	// ActionSkipped: nothing to do (host not found, nothing to remove, an entry that is not Seelegent's on remove,
	// no skill folder for the host).
	ActionSkipped Action = "skipped"
	// ActionManual: setup cannot edit the file safely (comments in JSON, no host CLI); Snippet says what to add.
	ActionManual Action = "manual"
	// ActionConflict: another program's entry or skill uses Seelegent's name; nothing was written (see --force).
	ActionConflict Action = "conflict"
	// ActionError: the item failed; Detail says why. Nothing was written for it.
	ActionError Action = "error"
)

// Item kinds.
const (
	KindMCP   = "mcp"
	KindSkill = "skill"
	KindHook  = "hook"
	// KindFolder is an empty folder setup had created, removed by --remove.
	KindFolder = "folder"
)

// Item is the outcome for one host and one kind of thing (MCP entry, skill, hook).
type Item struct {
	Host   string `json:"host"`
	Kind   string `json:"kind"`
	Action Action `json:"action"`
	// Path is the file or folder the item is about.
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Backup is the copy of the file taken before it was changed.
	Backup string `json:"backup,omitempty"`
	// Snippet is what to add by hand (ActionManual).
	Snippet string `json:"snippet,omitempty"`
}

// Failed reports whether the item makes the run fail (conflict or error).
func (it Item) Failed() bool { return it.Action == ActionConflict || it.Action == ActionError }

// HostInfo says whether a known host was found on this machine and how.
type HostInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Detected bool   `json:"detected"`
	// Reason says how it was found (or why not).
	Reason string `json:"reason"`
	// Config is the MCP config file setup edits for the host.
	Config string `json:"config"`
}

// Request says what to do.
type Request struct {
	// Hosts limits the run to these host ids; empty means every detected host.
	Hosts []string
	// Exe is the absolute path of the seelegent executable the hosts start with the argument "mcp".
	Exe string
	// DryRun computes and reports every action without writing anything or running host CLIs.
	DryRun bool
	// Remove removes Seelegent's entries, skill and hook instead of adding them.
	Remove bool
	// Force replaces an entry or skill that is not Seelegent's but uses its name.
	Force bool
	// NoSkill leaves the skill alone.
	NoSkill bool
	// Hooks also installs the Claude Code reminder hook (removal always removes it).
	Hooks bool
	// Version and Port go into the installed skill.
	Version string
	Port    int
	// Created is the file where setup records the host files and folders it created (setup-created.json in the
	// data folder), so --remove can delete them once they are empty and never anything that existed before. Empty
	// records nothing.
	Created string
}

// Report is the result of a run.
type Report struct {
	DryRun bool `json:"dryRun"`
	Remove bool `json:"remove"`
	// Command and Args are the registered command line.
	Command  string     `json:"command"`
	Args     []string   `json:"args"`
	Hosts    []HostInfo `json:"hosts"`
	Items    []Item     `json:"items"`
	Warnings []string   `json:"warnings,omitempty"`
}

// Failed reports whether any item failed (conflict or error).
func (r Report) Failed() bool { return slices.ContainsFunc(r.Items, Item.Failed) }

// ErrUnknownHost is returned for a --host id setup does not know.
var ErrUnknownHost = errors.New("unknown host")

// ErrExeName is returned when the executable to register is not named seelegent (seelegent.exe on Windows): setup
// recognizes its own entries by that name, so it would treat what it wrote as another program's entry afterwards.
var ErrExeName = errors.New("the executable to register is not named seelegent")

// HostIDs lists the ids of the hosts setup knows, in the order it handles them.
func HostIDs() []string {
	var ids []string
	for _, h := range allHosts() {
		ids = append(ids, h.id())
	}
	return ids
}

// Run detects the hosts and adds (or removes) Seelegent's MCP entry, skill and hook. Problems with single items are
// reported as items; the error is only for a request that cannot run at all (unknown host, bad executable path).
func Run(ctx context.Context, env Env, req Request) (Report, error) {
	if env.Home == "" || !filepath.IsAbs(env.Home) {
		return Report{}, fmt.Errorf("home directory %q is not an absolute path", env.Home)
	}
	if !req.Remove {
		if err := checkExe(req.Exe); err != nil {
			return Report{}, err
		}
		if err := checkExeName(req.Exe, env.goos()); err != nil {
			return Report{}, err
		}
	}
	hosts := allHosts()
	byID := map[string]host{}
	for _, h := range hosts {
		byID[h.id()] = h
	}
	for _, id := range req.Hosts {
		if _, ok := byID[id]; !ok {
			return Report{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownHost, id, strings.Join(HostIDs(), ", "))
		}
	}
	made, err := loadCreated(req.Created)
	if err != nil {
		return Report{}, err
	}
	rep := Report{DryRun: req.DryRun, Remove: req.Remove, Command: req.Exe, Args: []string{"mcp"},
		Warnings: exeWarnings(req.Exe), Items: []Item{}}
	detected := map[string]bool{}
	for _, h := range hosts {
		ok, why := h.detect(env)
		detected[h.id()] = ok
		rep.Hosts = append(rep.Hosts, HostInfo{ID: h.id(), Name: h.name(), Detected: ok, Reason: why,
			Config: h.configPath(env)})
	}
	var selected []host
	for _, h := range hosts {
		switch {
		case len(req.Hosts) > 0 && !slices.Contains(req.Hosts, h.id()):
		case !detected[h.id()] && req.Remove && leftBehind(env, h):
			// H22: a host that is gone (gemini uninstalled) can still hold Seelegent's entry or skill in its files.
			selected = append(selected, h)
		case !detected[h.id()]:
			if len(req.Hosts) > 0 {
				rep.Items = append(rep.Items, Item{Host: h.id(), Kind: KindMCP, Action: ActionSkipped,
					Path: h.configPath(env), Detail: h.name() + " was not found on this machine"})
			}
		default:
			selected = append(selected, h)
		}
	}
	o := op{env: env, req: req, created: made}
	skillDone := map[string]string{} // skill folder -> host that handled it in this run
	var cancelled error
	for _, h := range selected {
		if cancelled = ctx.Err(); cancelled != nil {
			break // H22: what this run created so far is still recorded below, so --remove can delete it later
		}
		want := desired(req.Exe, h.id())
		if req.Remove {
			rep.Items = append(rep.Items, h.remove(ctx, o))
		} else {
			rep.Items = append(rep.Items, h.install(ctx, o, want))
		}
		if !req.NoSkill {
			rep.Items = append(rep.Items, o.skillItem(h, selected, detected, skillDone, byID))
		}
		if hk, ok := h.(hookHost); ok && (req.Remove || req.Hooks) {
			if req.Remove {
				rep.Items = append(rep.Items, hk.removeHook(o))
			} else {
				rep.Items = append(rep.Items, hk.installHook(o))
			}
		}
	}
	if req.Remove && cancelled == nil {
		rep.Items = append(rep.Items, made.removeFolders(req.DryRun)...)
	}
	if !req.DryRun {
		if err := made.save(); err != nil {
			rep.Warnings = append(rep.Warnings, "could not update "+req.Created+": "+err.Error())
		}
	}
	return rep, cancelled
}

// leftBehind reports whether a host that was not detected still has its config file or Seelegent's skill folder,
// which a remove can clean up without the host's program.
func leftBehind(env Env, h host) bool {
	if exists(h.configPath(env)) {
		return true
	}
	dir := h.skillDir(env)
	return dir != "" && isDir(filepath.Join(dir, ServerName))
}

// op carries the run's environment and request to the host handlers.
type op struct {
	env Env
	req Request
	// created records the files and folders setup creates (never nil inside Run).
	created *created
}

// skillItem installs or removes the skill of h, once per skill folder (Codex and OpenCode share one).
func (o op) skillItem(h host, selected []host, detected map[string]bool, done map[string]string,
	byID map[string]host) Item {
	dir := h.skillDir(o.env)
	if dir == "" {
		return Item{Host: h.id(), Kind: KindSkill, Action: ActionSkipped,
			Detail: h.name() + " has no skill folder that setup knows; the MCP tools describe themselves"}
	}
	key := strings.ToLower(filepath.Clean(dir))
	if first, ok := done[key]; ok {
		return Item{Host: h.id(), Kind: KindSkill, Action: ActionSkipped, Path: filepath.Join(dir, ServerName),
			Detail: "same skill folder as " + first}
	}
	done[key] = h.id()
	if !o.req.Remove {
		return o.installSkill(h.id(), dir)
	}
	// Keep a shared skill while another detected host that is not being removed still uses it.
	var keepFor []string
	for id, other := range byID {
		if id == h.id() || !detected[id] || other.skillDir(o.env) == "" {
			continue
		}
		if strings.ToLower(filepath.Clean(other.skillDir(o.env))) != key {
			continue
		}
		if !slices.ContainsFunc(selected, func(s host) bool { return s.id() == id }) {
			keepFor = append(keepFor, id)
		}
	}
	if len(keepFor) > 0 {
		slices.Sort(keepFor)
		return Item{Host: h.id(), Kind: KindSkill, Action: ActionSkipped, Path: filepath.Join(dir, ServerName),
			Detail: "kept: also used by " + strings.Join(keepFor, ", ")}
	}
	return o.removeSkill(h.id(), dir)
}

// checkExe makes sure the registered executable is an absolute path to a file.
func checkExe(exe string) error {
	if exe == "" || !filepath.IsAbs(exe) {
		return fmt.Errorf("the seelegent executable %q is not an absolute path", exe)
	}
	st, err := os.Stat(exe)
	if err != nil {
		return fmt.Errorf("the seelegent executable %s: %w", exe, err)
	}
	if st.IsDir() {
		return fmt.Errorf("the seelegent executable %s is a folder", exe)
	}
	return nil
}

// checkExeName makes sure the executable is named seelegent (seelegent.exe on Windows), the name isOurs recognizes.
func checkExeName(exe, goos string) error {
	want := ServerName
	if goos == "windows" {
		want += ".exe"
	}
	base := path.Base(strings.ReplaceAll(exe, `\`, "/"))
	if strings.EqualFold(base, want) {
		return nil
	}
	return fmt.Errorf("%w: %s is named %q, and setup would not recognize the entries it writes for it as its own; "+
		"install or copy the binary as %s, or pass --exe <path to a %s executable>", ErrExeName, exe, base, want,
		ServerName)
}

// exeWarnings flags executable paths that will not survive: a `go run` build or a versioned install folder.
func exeWarnings(exe string) []string {
	n := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	var out []string
	if strings.Contains(n, "/go-build") {
		out = append(out, "the executable is a temporary `go run` build; pass --exe with the installed seelegent")
	}
	if strings.Contains(n, "/versions/") {
		out = append(out, "the executable is inside a versioned folder; an update would leave the hosts on the old "+
			"version (pass --exe with the stable launcher path)")
	}
	return out
}

// host is one lead host setup knows.
type host interface {
	id() string
	name() string
	// detect reports whether the host is installed and how it was recognized.
	detect(Env) (bool, string)
	// configPath is the MCP config file of the host.
	configPath(Env) string
	// skillDir is the folder the host reads skills from ("" when setup knows none); the skill goes into
	// <skillDir>/seelegent/.
	skillDir(Env) string
	install(ctx context.Context, o op, want Entry) Item
	remove(ctx context.Context, o op) Item
}

// hookHost is a host that can run the reminder hook (Claude Code).
type hookHost interface {
	installHook(o op) Item
	removeHook(o op) Item
}

// planned returns the item of an action that a dry run only reports (the report's DryRun says so).
func planned(it Item) Item {
	it.Backup = ""
	return it
}
