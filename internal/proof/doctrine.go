package proof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// DoctrineFile is where a repository keeps its doctrine: the checks every gate of the repository runs.
const DoctrineFile = ".seelegent/doctrine.json"

// ConstitutionFile is the Spec Kit constitution, part of the frozen bar.
const ConstitutionFile = ".specify/memory/constitution.md"

// maxDocBytes caps the spec documents, tasks.md and doctrine read.
const maxDocBytes = 4 << 20

// Check is one doctrine check: a command run from the repository root, for every gate or only when a changed file
// matches one of When.
type Check struct {
	ID        string   `json:"id"`
	Run       string   `json:"run"`
	Principle string   `json:"principle,omitempty"`
	When      []string `json:"when,omitempty"`
}

// ParseDoctrine parses a doctrine file: {"version": 1, "checks": [{id, run, principle?, when?}]}.
func ParseDoctrine(data []byte) ([]Check, error) {
	var doc struct {
		Version *int              `json:"version"`
		Checks  []json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if doc.Version == nil || *doc.Version != 1 {
		return nil, errors.New("expected version 1")
	}
	if doc.Checks == nil {
		return nil, errors.New("checks must be an array")
	}
	out := make([]Check, 0, len(doc.Checks))
	seen := map[string]bool{}
	for i, raw := range doc.Checks {
		var c Check
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("check[%d] must be an object with string fields", i)
		}
		c.ID, c.Run = strings.TrimSpace(c.ID), strings.TrimSpace(c.Run)
		switch {
		case c.ID == "":
			return nil, fmt.Errorf("check[%d].id must be a non-empty string", i)
		case seen[c.ID]:
			return nil, fmt.Errorf("duplicate check id %q", c.ID)
		case c.Run == "":
			return nil, fmt.Errorf("check[%d].run must be a non-empty string", i)
		}
		for _, w := range c.When {
			if strings.TrimSpace(w) == "" {
				return nil, fmt.Errorf("check[%d].when must hold non-empty strings", i)
			}
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, nil
}

// ReadDoctrine reads the doctrine of the repository at root; it returns no checks and no error when there is none.
func ReadDoctrine(root string) ([]Check, error) {
	data, _, err := fsutil.ReadRegular(filepath.Join(root, filepath.FromSlash(DoctrineFile)), maxDocBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseDoctrine(data)
}

// DoctrineFor returns the checks that apply to a gate over the changed files: those without When, and those with
// a When glob that matches one of the files.
func DoctrineFor(checks []Check, changed []string) []Check {
	var out []Check
	for _, c := range checks {
		if len(c.When) == 0 || slices.ContainsFunc(changed, func(f string) bool { return Contains(c.When, f) }) {
			out = append(out, c)
		}
	}
	return out
}

// barDocs are the feature documents of the frozen bar.
var barDocs = []string{"spec.md", "plan.md", "data-model.md", "research.md", "quickstart.md"}

// Bar is the frozen bar: hashes of what defines "done" for a task when it started (gate commands, constitution,
// doctrine, feature documents, contracts and the lines of its spec tasks without their tick). A worker that edits
// any of them shows up as bar_changed in the report.
type Bar map[string]string

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SnapshotBar hashes the bar of a task: gate commands, the constitution and doctrine under root, and when
// featureDir (absolute) is set its documents, contracts and the given task lines (id → raw line).
func SnapshotBar(root, featureDir string, taskLines map[string]string, gate []string) Bar {
	b := Bar{}
	g, _ := json.Marshal(append([]string{}, gate...))
	b["gate"] = hash(g)
	if root != "" {
		for _, rel := range []string{ConstitutionFile, DoctrineFile} {
			if data, _, err := fsutil.ReadRegular(filepath.Join(root, filepath.FromSlash(rel)), maxDocBytes); err == nil {
				b[rel] = hash(data)
			}
		}
	}
	if featureDir == "" {
		return b
	}
	for _, name := range barDocs {
		if data, _, err := fsutil.ReadRegular(filepath.Join(featureDir, name), maxDocBytes); err == nil {
			b[name] = hash(data)
		}
	}
	contracts := filepath.Join(featureDir, "contracts")
	_ = filepath.WalkDir(contracts, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, rerr := filepath.Rel(featureDir, p)
		if rerr != nil {
			return nil
		}
		if data, _, err := fsutil.ReadRegular(p, maxDocBytes); err == nil {
			b[filepath.ToSlash(rel)] = hash(data)
		}
		return nil
	})
	for id, line := range taskLines {
		b["tasks.md#"+id] = TaskLineHash(line)
	}
	return b
}

// TaskLineHash is the bar hash of one tasks.md line (the value of its "tasks.md#ID" key): the line without its tick.
func TaskLineHash(line string) string {
	return hash([]byte(untick(line)))
}

// untick turns "- [x] T001" into "- [ ] T001", so ticking a task does not change the bar.
func untick(line string) string {
	if t, ok := ParseTaskLine(line); ok && t.Done {
		i := strings.Index(line, "[")
		return line[:i+1] + " " + line[i+2:]
	}
	return strings.TrimRight(line, "\r")
}

// Diff returns the keys whose hash differs between the frozen bar and the current one, sorted.
func (b Bar) Diff(cur Bar) []string {
	var out []string
	for k, v := range b {
		if cur[k] != v {
			out = append(out, k)
		}
	}
	for k := range cur {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
