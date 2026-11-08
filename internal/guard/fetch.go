package guard

import (
	"strings"
)

// ConfigEntry is one configuration setting as git config --get-regexp prints it: the key (section and variable in
// lower case, a subsection as written) and its value.
type ConfigEntry struct {
	Key   string
	Value string
}

// FetchConfigPattern is the git config --get-regexp pattern of the settings Checker.Config returns: the fetch
// refspecs and prune settings of every remote, and the prune defaults.
const FetchConfigPattern = `^(remote\..*\.(fetch|prune|prunetags)|fetch\.(prune|prunetags))$`

// fetchPlan is what a fetch (or git remote prune/update) will write: the remotes it reads, the refspecs of the
// command line, the --refmap values that replace the configured refspecs, and whether it forces and prunes.
type fetchPlan struct {
	// remotes are the remote names, URLs or paths named; none (or every) means every configured remote.
	remotes []string
	every   bool
	// specs are the refspecs given on the command line.
	specs []string
	// refmap holds the --refmap values; refmapGiven is set when one was given (--refmap="" clears them).
	refmap      []string
	refmapGiven bool
	force       bool
	// prune is set by --prune, -p, --prune-tags or -P; noPrune by --no-prune. With neither the configuration
	// decides.
	prune, noPrune bool
}

// fetchCommand reads the arguments of git fetch into a plan. ok is false when they cannot be read: an expansion
// where an option could be, --stdin (refspecs the guard cannot see) or --update-head-ok, which lets fetch move the
// checked-out branch under the working tree.
func fetchCommand(rest []word) (f fetchPlan, ok bool) {
	p := scan(rest, opts{short: "jo", long: set("--depth", "--deepen", "--shallow-since", "--shallow-exclude",
		"--jobs", "--upload-pack", "--server-option", "--refmap", "--negotiation-tip", "--filter",
		"--recurse-submodules-default", "--submodule-prefix"),
		danger: []string{"--update-head-ok", "--force", "--prune", "--prune-tags", "--stdin", "--refmap"}})
	if p.unknown || p.has("-u", "--update-head-ok", "--stdin") {
		return f, false
	}
	refmap, given, dynamic := optionValues(rest, "--refmap", len("--refm"))
	if dynamic {
		return f, false
	}
	f.refmap, f.refmapGiven = refmap, given
	f.force = p.has("-f", "--force")
	f.prune = p.has("-p", "--prune", "-P", "--prune-tags")
	f.noPrune = p.has("--no-prune")
	pos := make([]string, len(p.pos))
	for i, w := range p.pos {
		pos[i] = w.text
	}
	if p.has("--all", "--multiple") {
		f.remotes, f.every = pos, p.has("--all")
		return f, true
	}
	if len(pos) == 0 {
		return f, true
	}
	f.remotes = pos[:1]
	for i := 1; i < len(pos); i++ {
		if pos[i] == "tag" && i+1 < len(pos) {
			i++ // tag <name>: refs/tags/<name>:refs/tags/<name>
			continue
		}
		f.specs = append(f.specs, pos[i])
	}
	return f, true
}

// optionValues returns the values given to the long option full, or to a prefix of it at least minLen long (git takes
// an unambiguous prefix), as --opt=value or --opt value; given reports whether it appeared, dynamic whether a value
// is an expansion.
func optionValues(args []word, full string, minLen int) (values []string, given, dynamic bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a.text == "--" && !a.dynamic {
			break
		}
		name, value, eq := strings.Cut(a.text, "=")
		if len(name) < minLen || !isAbbrev(name, full) {
			continue
		}
		given = true
		if !eq {
			if i+1 >= len(args) {
				break
			}
			i++
			a, value = args[i], args[i].text
		}
		dynamic = dynamic || a.dynamic
		values = append(values, value)
	}
	return values, given, dynamic
}

// fetchUnsafe reports whether a fetch can move a branch back or delete one (H3, review N1): every refspec that will
// apply is checked, those of the command line and the --refmap values, or the configured remote.<name>.fetch
// refspecs of the remotes it reads when no --refmap is given (git uses them for the command-line refspecs too).
// A refspec is safe when its destination (compared in lower case: refs/HEADS/main is refs/heads/main on a disk that
// ignores case) is empty, FETCH_HEAD, or under refs/remotes/ or refs/tags/. Outside those, a wildcard destination is
// refused, and so is any destination when the refspec is forced (+ or --force), when the fetch prunes (deleting what
// the remote no longer has; --prune, --prune-tags, or fetch.prune / remote.<name>.prune in the configuration), and
// under refs/ anywhere but refs/heads/ (git updates such refs whatever their history, as if forced). A branch
// destination whose name matches an existing branch in all but case is refused too (caseClash). A configuration
// that cannot be read refuses the fetch.
func (c Checker) fetchUnsafe(globals []string, f fetchPlan) bool {
	var cfg []ConfigEntry
	if c.Config != nil && (!f.refmapGiven || !f.prune && !f.noPrune) {
		entries, err := c.Config(globals)
		if err != nil {
			return true
		}
		cfg = entries
	}
	selected := f.selected(cfg)
	specs := append([]string{}, f.specs...)
	if f.refmapGiven {
		specs = append(specs, f.refmap...)
	}
	prune := f.prune
	for _, e := range cfg {
		section, sub, name := splitKey(e.Key)
		switch {
		case section == "remote" && name == "fetch" && !f.refmapGiven && selected(sub):
			specs = append(specs, e.Value)
		case !f.noPrune && (section == "remote" && selected(sub) || section == "fetch" && sub == "") &&
			(name == "prune" || name == "prunetags") && !isFalse(e.Value):
			prune = true
		}
	}
	var branches []word
	for _, s := range specs {
		if unsafeRefspec(s, f.force, prune) {
			return true
		}
		s = strings.TrimPrefix(strings.TrimSpace(s), "+")
		if _, dst, ok := strings.Cut(s, ":"); ok && !strings.HasPrefix(s, "^") {
			if ref := fetchBranchRef(dst); ref != "" {
				branches = append(branches, word{text: ref})
			}
		}
	}
	// A fast-forward into Main beside main writes over main on a disk that ignores case (see caseClash).
	return c.caseClash(globals, branches)
}

// selected returns whether the fetch reads the configured remote sub: every remote when it names none, or names one
// the configuration does not know (a remote group, or a remote with no fetch settings); none for a URL or a path.
func (f fetchPlan) selected(cfg []ConfigEntry) func(sub string) bool {
	all := func(string) bool { return true }
	if f.every || len(f.remotes) == 0 {
		return all
	}
	var names []string
	for _, r := range f.remotes {
		if r == "." || r == ".." || strings.ContainsAny(r, `/\:`) {
			continue
		}
		known := false
		for _, e := range cfg {
			if section, sub, _ := splitKey(e.Key); section == "remote" && strings.EqualFold(sub, r) {
				known = true
				break
			}
		}
		if !known {
			return all
		}
		names = append(names, r)
	}
	return func(sub string) bool {
		for _, n := range names {
			if strings.EqualFold(sub, n) {
				return true
			}
		}
		return false
	}
}

// splitKey splits a configuration key into its section and variable (lower case) and the subsection between them.
func splitKey(key string) (section, sub, name string) {
	first, last := strings.IndexByte(key, '.'), strings.LastIndexByte(key, '.')
	if first < 0 {
		return strings.ToLower(key), "", ""
	}
	section, name = strings.ToLower(key[:first]), strings.ToLower(key[last+1:])
	if last > first {
		sub = key[first+1 : last]
	}
	return section, sub, name
}

// isFalse reports a boolean configuration value that is false (an empty value counts as false).
func isFalse(v string) bool {
	return setHas(strings.ToLower(strings.TrimSpace(v)), "", "false", "no", "off", "0")
}

// unsafeRefspec reports whether the fetch refspec spec can move a branch back or delete one (see fetchUnsafe).
func unsafeRefspec(spec string, force, prune bool) bool {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "+") {
		force, spec = true, spec[1:]
	}
	if spec == "" || strings.HasPrefix(spec, "^") {
		return false // a negative refspec only leaves refs out
	}
	_, dst, _ := strings.Cut(spec, ":")
	low := strings.ToLower(dst)
	if dst == "" || low == "fetch_head" || strings.HasPrefix(low, "refs/remotes/") ||
		strings.HasPrefix(low, "refs/tags/") {
		return false
	}
	if strings.Contains(dst, "*") || force || prune {
		return true
	}
	// Without force git takes only a fast-forward into refs/heads/; a short name is a branch (refs/heads/<dst>, or
	// refs/<dst> for heads/ and remotes/), and any other ref under refs/ is updated whatever its history.
	return strings.HasPrefix(dst, "refs/") && !strings.HasPrefix(dst, "refs/heads/")
}

// refConfigKey reports a configuration key that decides what a fetch writes or deletes: remote.<name>.fetch,
// .mirror, .prune and .pruneTags, and fetch.prune and fetch.pruneTags.
func refConfigKey(k string) bool {
	section, sub, name := splitKey(k)
	switch section {
	case "remote":
		return sub != "" && setHas(name, "fetch", "mirror", "prune", "prunetags")
	case "fetch":
		return sub == "" && setHas(name, "prune", "prunetags")
	}
	return false
}

// refConfigRisky reports a value of a refConfigKey key that lets a later plain git fetch move or delete a branch: a
// refspec whose destination is not under refs/remotes/, a mirror or a prune setting that is not false.
func refConfigRisky(k, v string) bool {
	if _, _, name := splitKey(k); name == "fetch" {
		v = strings.TrimPrefix(strings.TrimSpace(v), "+")
		if strings.HasPrefix(v, "^") {
			return false
		}
		_, dst, _ := strings.Cut(v, ":")
		return dst != "" && !strings.HasPrefix(dst, "refs/remotes/")
	}
	return !isFalse(v)
}
