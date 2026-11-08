package guard

import (
	"strings"
)

// alwaysDenied are git subcommands that are refused with any arguments: each can discard, move or rewrite work
// in the working tree, the index or the branches others use.
var alwaysDenied = set("reset", "restore", "switch", "revert", "rebase", "merge", "pull", "push", "cherry-pick",
	"am", "rm", "filter-branch", "filter-repo", "update-ref", "prune", "mv", "update-index", "read-tree",
	"checkout-index", "merge-file", "replace",
	// send-pack . :refs/heads/x deletes a branch, +x:refs/heads/y moves one (H3).
	"send-pack")

// builtins are git's own commands; any other name may be an alias (resolved through Checker.Alias).
var builtins = set("add", "am", "annotate", "apply", "archive", "bisect", "blame", "branch", "bugreport", "bundle",
	"cat-file", "check-attr", "check-ignore", "check-mailmap", "check-ref-format", "checkout", "checkout-index",
	"cherry", "cherry-pick", "citool", "clean", "clone", "column", "commit", "commit-graph", "commit-tree", "config",
	"count-objects", "credential", "describe", "diagnose", "diff", "diff-files", "diff-index", "diff-tree",
	"difftool", "fast-export", "fast-import", "fetch", "fetch-pack", "filter-branch", "fmt-merge-msg",
	"for-each-ref", "for-each-repo", "format-patch", "fsck", "gc", "get-tar-commit-id", "grep", "gui",
	"hash-object", "help", "index-pack", "init", "instaweb", "interpret-trailers", "log", "ls-files", "ls-remote",
	"ls-tree", "mailinfo", "mailsplit", "maintenance", "merge", "merge-base", "merge-file", "merge-index",
	"merge-tree", "mergetool", "mktag", "mktree", "multi-pack-index", "mv", "name-rev", "notes", "pack-objects",
	"pack-refs", "patch-id", "prune", "prune-packed", "pull", "push", "range-diff", "read-tree", "rebase",
	"reflog", "remote", "repack", "replace", "request-pull", "rerere", "reset", "restore", "rev-list",
	"rev-parse", "revert", "rm", "send-email", "shortlog", "show", "show-branch", "show-index", "show-ref",
	"sparse-checkout", "stage", "stash", "status", "stripspace", "submodule", "switch", "symbolic-ref", "tag",
	"unpack-file", "unpack-objects", "update-index", "update-ref", "update-server-info", "var", "verify-commit",
	"verify-pack", "verify-tag", "version", "whatchanged", "worktree", "write-tree", "backfill", "refs", "replay",
	"last-modified", "repo")

// globalValued are git's global options that take the next word as their value.
var globalValued = set("-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env",
	"--attr-source")

// git checks a git command given as its arguments (without "git").
func (c Checker) git(args []word, depth int) Result {
	if depth > maxDepth {
		return denyUnreadable()
	}
	i := 0
	for i < len(args) {
		a := args[i]
		t := a.text
		if a.dynamic {
			return denyUnreadable()
		}
		if !strings.HasPrefix(t, "-") {
			break
		}
		switch {
		case t == "--help" || t == "-h" || t == "--version":
			return allow
		case t == "-c" || t == "--config-env":
			if i+1 >= len(args) {
				return allow
			}
			if args[i+1].dynamic || riskyConfigKey(configKey(args[i+1].text)) {
				return denyGit(args)
			}
			i += 2
			continue
		case strings.HasPrefix(t, "--config-env="):
			if riskyConfigKey(configKey(strings.TrimPrefix(t, "--config-env="))) {
				return denyGit(args)
			}
		case strings.HasPrefix(t, "-c") && len(t) > 2:
			if riskyConfigKey(configKey(t[2:])) {
				return denyGit(args)
			}
		case globalValued[t]:
			i += 2
			continue
		}
		i++
	}
	if i >= len(args) {
		return allow
	}
	sub := subcommand(args[i].text)
	rest := args[i+1:]
	if len(rest) == 1 && !rest[0].dynamic && (rest[0].text == "-h" || rest[0].text == "--help") {
		return allow
	}
	deny := func() Result { return denyGit(args) }
	if alwaysDenied[sub] {
		if sub == "replace" && listsOnly(rest, "-l", "--list") {
			return allow
		}
		return deny()
	}
	switch sub {
	case "stash":
		p := scan(rest, opts{})
		if p.unknown {
			return deny()
		}
		if len(p.pos) > 0 && (p.pos[0].text == "list" || p.pos[0].text == "show") {
			return allow
		}
		return deny()
	case "clean":
		p := scan(rest, opts{short: "e", long: set("--exclude")})
		if p.unknown || !p.has("-n", "--dry-run") {
			return deny()
		}
	case "checkout":
		danger := []string{"-f", "--force", "-m", "--merge", "--ours", "--theirs", "-p", "--patch", "--overlay",
			"--pathspec-from-file", "--orphan"}
		p := scan(rest, opts{short: "bB", long: set("--orphan", "--conflict", "--pathspec-from-file"),
			danger: danger})
		// -b creates a branch and fails on an existing one; -B resets an existing one to the start point (H3).
		if p.unknown || !p.has("-b") || p.has("-B") || p.dashdash || len(p.pos) > 1 || p.has(danger...) {
			return deny()
		}
	case "branch":
		danger := []string{"-d", "-D", "-m", "-M", "-C", "-f", "--delete", "--move", "--force", "--copy"}
		p := scan(rest, opts{short: "u", long: set("--set-upstream-to", "--sort", "--format"), danger: danger})
		if p.unknown || p.has(danger...) && !p.has("-l", "--list") {
			return deny()
		}
	case "tag":
		danger := []string{"-d", "--delete", "-f", "--force"}
		p := scan(rest, opts{short: "mFu", long: set("--message", "--file", "--local-user", "--cleanup", "--sort",
			"--format", "--trailer"), danger: danger})
		if p.unknown || p.has(danger...) {
			return deny()
		}
	case "commit":
		danger := []string{"-a", "--all", "--amend"}
		p := scan(rest, opts{short: "mFcCt", long: set("--message", "--file", "--reuse-message",
			"--reedit-message", "--template", "--author", "--date", "--cleanup", "--fixup", "--squash", "--trailer",
			"--pathspec-from-file"), danger: danger})
		if p.unknown || p.has(danger...) {
			return deny()
		}
	case "apply":
		p := scan(rest, opts{short: "pC", long: set("--exclude", "--include", "--directory", "--whitespace"),
			danger: []string{"--apply"}})
		if p.unknown || p.has("--apply") || !p.has("--check", "--stat", "--numstat", "--summary") {
			return deny()
		}
	case "gc":
		// gc --prune=<date> (now, all, ...) deletes the unreachable objects that still hold lost work at once;
		// plain gc and gc --prune keep them for gc.pruneExpire (two weeks by default).
		p := scan(rest, opts{danger: []string{"--prune"}})
		if p.unknown || p.valued("--prune") {
			return deny()
		}
	case "reflog":
		// The stash is a reflog: reflog drop refs/stash (or --all) deletes every stashed change at once (H2).
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) > 0 && setHas(p.pos[0].text, "expire", "delete", "drop") {
			return deny()
		}
	case "worktree":
		p := scan(rest, opts{short: "bB", long: set("--reason")})
		if p.unknown || len(p.pos) > 0 && setHas(p.pos[0].text, "remove", "prune", "move") {
			return deny()
		}
		// worktree add -B resets an existing branch to the start point (H3).
		if len(p.pos) > 0 && p.pos[0].text == "add" && p.has("-B") {
			return deny()
		}
	case "replay":
		// git replay updates the branches it rewrites unless told only to print the updates (H3).
		if !replayPrintsOnly(rest) {
			return deny()
		}
	case "fetch":
		// --update-head-ok lets fetch move the checked-out branch under the working tree; a forced refspec (+src:dst,
		// or any refspec with --force) that writes a branch moves it anywhere, back in history included (H3).
		p := scan(rest, opts{short: "jo", long: set("--depth", "--deepen", "--shallow-since", "--shallow-exclude",
			"--jobs", "--upload-pack", "--server-option", "--refmap", "--negotiation-tip", "--filter",
			"--recurse-submodules-default", "--submodule-prefix"), danger: []string{"--update-head-ok", "--force"}})
		if p.unknown || p.has("-u", "--update-head-ok") {
			return deny()
		}
		force := p.has("-f", "--force")
		for i, r := range p.pos {
			if i > 0 && forcedBranchWrite(r.text, force) {
				return deny()
			}
		}
	case "repack":
		// repack -a -d drops the unreachable objects of the old packs, where lost work still is (a dropped stash, a
		// commit a reset left behind); -A and --cruft keep them.
		p := scan(rest, opts{short: "g", long: set("--window", "--depth", "--threads", "--window-memory",
			"--max-pack-size", "--filter", "--filter-to", "--expire-to", "--cruft-expiration", "--max-cruft-size",
			"--geometric", "--keep-pack", "--name-hash-version")})
		if p.unknown || p.has("-a") && p.has("-d") && !p.has("-A", "--cruft") {
			return deny()
		}
	case "config":
		return c.config(args, rest)
	case "submodule":
		p := scan(rest, opts{})
		if p.unknown {
			return deny()
		}
		if len(p.pos) > 0 {
			switch p.pos[0].text {
			case "deinit", "update", "absorbgitdirs":
				return deny()
			case "foreach":
				return c.Command(joinWords(p.pos[1:]), POSIX)
			}
		}
	case "sparse-checkout":
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) == 0 || !setHas(p.pos[0].text, "list", "check-rules") {
			return deny()
		}
	case "symbolic-ref":
		p := scan(rest, opts{short: "m", danger: []string{"--delete"}})
		if p.unknown || p.has("-d", "--delete") || len(p.pos) > 1 {
			return deny()
		}
	case "bisect":
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) == 0 || !setHas(p.pos[0].text, "log", "visualize", "view", "terms", "help") {
			return deny()
		}
	case "rerere":
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) > 0 && setHas(p.pos[0].text, "forget", "clear", "gc") {
			return deny()
		}
	case "notes":
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) > 0 && setHas(p.pos[0].text, "remove", "prune") {
			return deny()
		}
	case "refs":
		p := scan(rest, opts{})
		if p.unknown || len(p.pos) > 0 && p.pos[0].text == "migrate" {
			return deny()
		}
	default:
		if !builtins[sub] && c.Alias != nil {
			return c.alias(sub, rest, depth)
		}
	}
	return allow
}

// subcommand is the git subcommand a word names: lower-case, and without the ".exe" that Git for Windows strips
// from it before it looks up builtins and aliases (git reset.exe --hard runs reset --hard, git st.exe the alias st;
// H1). git strips only a lower-case ".exe", once, and takes no other suffix, trailing dot or space; any case is
// stripped here, since refusing a name git would not run costs nothing.
func subcommand(s string) string {
	s = strings.ToLower(s)
	return strings.TrimSuffix(s, ".exe")
}

// replayPrintsOnly reports whether git replay is told to print the ref updates instead of making them
// (--ref-action=print, the last one given; git takes an unambiguous prefix of the option).
func replayPrintsOnly(args []word) bool {
	prints := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a.dynamic {
			return false
		}
		name, value, eq := strings.Cut(a.text, "=")
		if name == "--no-ref-action" {
			prints = false
			continue
		}
		if len(name) < len("--ref") || !strings.HasPrefix("--ref-action", name) {
			continue
		}
		if !eq {
			if i+1 >= len(args) || args[i+1].dynamic {
				return false
			}
			i++
			value = args[i].text
		}
		prints = value == "print"
	}
	return prints
}

// forcedBranchWrite reports whether a fetch refspec forcibly writes a branch: +src:dst (or src:dst under --force)
// whose dst is under refs/heads/ or a short name, which git resolves to a branch (+old:main moves main).
func forcedBranchWrite(spec string, force bool) bool {
	if strings.HasPrefix(spec, "+") {
		force, spec = true, spec[1:]
	}
	if !force || strings.HasPrefix(spec, "^") {
		return false
	}
	_, dst, ok := strings.Cut(spec, ":")
	if !ok || dst == "" {
		return false
	}
	return !strings.HasPrefix(dst, "refs/") || strings.HasPrefix(dst, "refs/heads/")
}

// alias resolves a git alias and checks what it runs.
func (c Checker) alias(name string, rest []word, depth int) Result {
	value, ok := c.Alias(name)
	if !ok {
		return allow
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "!") {
		// A shell alias: git runs it with sh and appends the arguments.
		return c.Command(value[1:] + " " + joinWords(rest))
	}
	segs, err := parse(value, POSIX, depth+1)
	if err != nil || len(segs) != 1 {
		return denyUnreadable()
	}
	return c.git(append(append([]word{}, segs[0].words...), rest...), depth+1)
}

// config checks git config: writing a key that makes git run a command (an alias, a hook path, an editor, a
// filter) is refused; reading is allowed.
func (c Checker) config(args, rest []word) Result {
	p := scan(rest, opts{short: "f", long: set("--file", "--blob", "--type", "--default", "--comment", "--value"),
		danger: configWrites})
	if p.has("--get", "--get-all", "--get-regexp", "--get-urlmatch", "-l", "--list", "--get-color",
		"--get-colorbool", "--show-origin", "--show-scope") {
		return allow
	}
	pos := p.pos
	if len(pos) > 0 && !pos[0].dynamic && setHas(pos[0].text, "get", "list") {
		return allow
	}
	// The editor can write any key, and a renamed section takes its keys along (foo.clean to filter.x.clean).
	if p.has("-e", "--edit", "--rename-section") ||
		len(pos) > 0 && !pos[0].dynamic && setHas(pos[0].text, "edit", "rename-section") {
		return denyGit(args)
	}
	sub := false
	if len(pos) > 0 && !pos[0].dynamic && setHas(pos[0].text, "set", "unset", "remove-section") {
		pos, sub = pos[1:], true
	}
	if len(pos) == 0 {
		return allow
	}
	if pos[0].dynamic {
		return denyGit(args)
	}
	if !sub && len(pos) == 1 && !p.has(configWrites...) {
		return allow // git config <key> reads it
	}
	if riskyConfigKey(strings.ToLower(pos[0].text)) {
		return denyGit(args)
	}
	return allow
}

// configWrites are the options of git config that write with a key alone or with a value.
var configWrites = []string{"--add", "--unset", "--unset-all", "--replace-all", "--edit", "--rename-section",
	"--remove-section"}

// configKey returns the lower-case key of a "key=value" pair.
func configKey(kv string) string {
	if eq := strings.IndexByte(kv, '='); eq >= 0 {
		kv = kv[:eq]
	}
	return strings.ToLower(strings.TrimSpace(kv))
}

// riskyConfigKey reports a configuration key whose value git runs as a command or that redefines commands.
func riskyConfigKey(k string) bool {
	switch k {
	case "core.hookspath", "core.fsmonitor", "core.sshcommand", "core.editor", "core.pager", "core.askpass",
		"core.gitproxy", "core.worktree", "sequence.editor", "diff.external", "gpg.program", "credential.helper",
		"uploadpack.packobjectshook", "include.path", "alias",
		// Not a program, but the same as gc --prune=now for every later gc, including git's automatic one.
		"gc.pruneexpire",
		// Makes git run the subcommand it guesses from a typo (git stsh: stash), which the guard reads as an
		// unknown command; a later -c would also override the git shim's own -c help.autocorrect=0.
		"help.autocorrect":
		return true
	}
	for _, prefix := range []string{"alias.", "includeif.", "include.", "pager."} {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	for _, suffix := range []string{".clean", ".smudge", ".process", ".textconv", ".command", ".driver",
		".program", ".cmd", ".helper", ".uploadpack", ".receivepack", ".proxy",
		// gc.reflogExpire=now (or gc.<pattern>.reflogExpire) makes gc drop reflogs at once, refs/stash included.
		".reflogexpire", ".reflogexpireunreachable"} {
		if strings.HasSuffix(k, suffix) {
			return true
		}
	}
	return false
}

// listsOnly reports whether args hold one of the listing flags.
func listsOnly(args []word, flags ...string) bool {
	for _, a := range args {
		if setHas(a.text, flags...) {
			return true
		}
	}
	return false
}

func setHas(s string, names ...string) bool {
	for _, n := range names {
		if s == n {
			return true
		}
	}
	return false
}

// opts describes the options of a git subcommand that take a value.
type opts struct {
	// short lists the one-letter options that take a value (the rest of the cluster or the next word).
	short string
	// long lists the long options that take the next word as their value (without =).
	long map[string]bool
	// danger lists the options the caller refuses. git accepts any unambiguous prefix of a long option
	// (git branch --del x deletes), so a long option that is a prefix of one of these counts as it too. An
	// ambiguous prefix counts as well: git refuses it, so refusing it first costs nothing.
	danger []string
}

// parsed is a scanned argument list.
type parsed struct {
	// flags holds "-x" for every short option and "--name" for every long one.
	flags []string
	// withValue holds the long options given with "=value".
	withValue []string
	pos       []word
	// dashdash is set when "--" separated paths.
	dashdash bool
	// unknown is set when an expansion stands where an option could be.
	unknown bool
}

func (p parsed) has(names ...string) bool {
	for _, f := range p.flags {
		if setHas(f, names...) {
			return true
		}
	}
	return false
}

// valued reports whether the long option name was given a value with "=".
func (p parsed) valued(name string) bool { return setHas(name, p.withValue...) }

// long records the long option name ("--x"), with the dangerous options it abbreviates (git takes a prefix of
// one letter as well: git branch --d x deletes).
func (p *parsed) long(name string, value bool, o opts) {
	names := []string{name}
	if len(name) > 2 && !setHas(name, o.danger...) {
		for _, d := range o.danger {
			if strings.HasPrefix(d, "--") && strings.HasPrefix(d, name) {
				names = append(names, d)
			}
		}
	}
	p.flags = append(p.flags, names...)
	if value {
		p.withValue = append(p.withValue, names...)
	}
}

// scan splits git subcommand arguments into options and positional arguments.
func scan(args []word, o opts) parsed {
	var p parsed
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if t == "--" && !a.dynamic {
			p.dashdash = true
			p.pos = append(p.pos, args[i+1:]...)
			break
		}
		if a.dynamic {
			eq := strings.IndexByte(t, '=')
			dyn := strings.IndexAny(t, "$%`(@")
			if strings.HasPrefix(t, "--") && eq > 2 && (dyn < 0 || eq < dyn) {
				p.long(t[:eq], true, o)
				continue
			}
			p.unknown = true
			p.pos = append(p.pos, a)
			continue
		}
		switch {
		case strings.HasPrefix(t, "--"):
			if eq := strings.IndexByte(t, '='); eq > 0 {
				p.long(t[:eq], true, o)
				continue
			}
			if o.long[t] {
				i++
			}
			p.long(t, false, o)
		case strings.HasPrefix(t, "-") && len(t) > 1:
			for j := 1; j < len(t); j++ {
				p.flags = append(p.flags, "-"+string(t[j]))
				if strings.IndexByte(o.short, t[j]) >= 0 {
					if j == len(t)-1 {
						i++
					}
					break
				}
			}
		default:
			p.pos = append(p.pos, a)
		}
	}
	return p
}
