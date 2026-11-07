package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// detectMaxBytes caps the project files Detect reads.
const detectMaxBytes = 1 << 20

// scriptOrder is the order of the package.json scripts Detect runs: cheap checks first.
var scriptOrder = []string{"lint", "typecheck", "test", "build"}

var makeTargetRe = regexp.MustCompile(`(?m)^(test|check|lint):`)

// Detect finds the test, lint and build commands of the project that holds dir: it walks from dir up to root and
// stops at the first folder with a known project file (go.mod, package.json, Cargo.toml, a pytest setup, a Makefile
// with a test target). The commands run in that folder (Dir relative to root). Nothing found gives nil.
func Detect(root, dir string) []api.GateCommand {
	for d := dir; ; {
		if cmds := detectIn(d); len(cmds) > 0 {
			rel, err := filepath.Rel(root, d)
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				rel = ""
			}
			for i := range cmds {
				cmds[i].Dir, cmds[i].Kind = filepath.ToSlash(rel), api.StepDetect
			}
			return cmds
		}
		if in, err := fsutil.Within(root, d); err != nil || !in {
			return nil
		}
		if same, _ := fsutil.SamePath(root, d); same {
			return nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil
		}
		d = parent
	}
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func read(p string) string {
	data, _, err := fsutil.ReadRegular(p, detectMaxBytes)
	if err != nil {
		return ""
	}
	return string(data)
}

func detectIn(d string) []api.GateCommand {
	var cmds []api.GateCommand
	add := func(c ...string) {
		for _, x := range c {
			cmds = append(cmds, api.GateCommand{Cmd: x})
		}
	}
	if exists(filepath.Join(d, "go.mod")) {
		add("go build ./...", "go vet ./...", "go test ./...")
	}
	if exists(filepath.Join(d, "package.json")) {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal([]byte(read(filepath.Join(d, "package.json"))), &pkg) == nil {
			pm := "npm"
			switch {
			case exists(filepath.Join(d, "pnpm-lock.yaml")):
				pm = "pnpm"
			case exists(filepath.Join(d, "yarn.lock")):
				pm = "yarn"
			case exists(filepath.Join(d, "bun.lockb")), exists(filepath.Join(d, "bun.lock")):
				pm = "bun"
			}
			for _, s := range scriptOrder {
				if v, ok := pkg.Scripts[s]; ok && strings.TrimSpace(v) != "" && !strings.Contains(v, "no test specified") {
					add(pm + " run " + s)
				}
			}
		}
	}
	if exists(filepath.Join(d, "Cargo.toml")) {
		add("cargo build", "cargo test")
	}
	if exists(filepath.Join(d, "pytest.ini")) || strings.Contains(read(filepath.Join(d, "pyproject.toml")), "[tool.pytest") {
		add("python -m pytest -q")
	}
	if len(cmds) == 0 {
		for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
			if m := makeTargetRe.FindStringSubmatch(read(filepath.Join(d, name))); m != nil {
				add("make " + m[1])
				break
			}
		}
	}
	return cmds
}
