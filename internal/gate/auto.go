package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// Limits of the syntax checks.
const (
	autoMaxBytes = 4 << 20
	nodeTimeout  = 30 * time.Second
)

// Checker runs the auto step's syntax checks without a shell (old bug D1: the old version built a shell command
// line from file names). JSON and Go are parsed in the daemon; JavaScript is checked by `node --check` with the
// path as a separate argument, when node is on the PATH of Env.
type Checker struct {
	Env []string
	// Head returns the HEAD version of a repository-relative file (ok false when it has none). A JSON file whose
	// HEAD version does not parse either (JSON with comments) is not checked.
	Head func(ctx context.Context, rel string) (data []byte, ok bool)
}

// jsonc reports whether a .json file is commonly JSON with comments.
func jsonc(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	return strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, "jsconfig") ||
		strings.HasPrefix(strings.ToLower(rel), ".vscode/") || strings.Contains(strings.ToLower(rel), "/.vscode/") ||
		base == ".eslintrc.json" || base == "devcontainer.json" || base == ".devcontainer.json"
}

// Check checks the files (relative to root) and returns those that fail. Deleted files are skipped.
func (c Checker) Check(ctx context.Context, root string, files []string) []api.GateFailure {
	var out []api.GateFailure
	node := ""
	for _, rel := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		st, err := os.Lstat(abs)
		if err != nil || !st.Mode().IsRegular() || st.Size() > autoMaxBytes {
			continue
		}
		switch proof.Ext(rel) {
		case ".json":
			if jsonc(rel) {
				continue
			}
			data, _, err := fsutil.ReadRegular(abs, autoMaxBytes)
			if err != nil {
				continue
			}
			if msg := jsonError(data); msg != "" {
				if c.Head != nil {
					if old, ok := c.Head(ctx, rel); ok && jsonError(old) != "" {
						continue // it was not plain JSON before either
					}
				}
				out = append(out, api.GateFailure{File: rel, Message: msg})
			}
		case ".go":
			data, _, err := fsutil.ReadRegular(abs, autoMaxBytes)
			if err != nil {
				continue
			}
			if _, err := parser.ParseFile(token.NewFileSet(), rel, data, parser.SkipObjectResolution); err != nil {
				out = append(out, api.GateFailure{File: rel, Message: goError(err)})
			}
		default: // .js, .cjs, .mjs
			if node == "" {
				if node = envx.ResolveCommand("node", c.Env); node == "" {
					node = "-"
				}
			}
			if node == "-" {
				continue
			}
			if msg := nodeCheck(ctx, node, root, abs, c.Env); msg != "" {
				out = append(out, api.GateFailure{File: rel, Message: msg})
			}
		}
	}
	return out
}

// jsonError returns why data is not valid JSON ("" when it is). A UTF-8 BOM is allowed.
func jsonError(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var v any
	err := json.Unmarshal(data, &v)
	if err == nil {
		return ""
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line := bytes.Count(data[:min(int(se.Offset), len(data))], []byte("\n")) + 1
		return "line " + strconv.Itoa(line) + ": " + se.Error()
	}
	return err.Error()
}

// goError keeps the first few syntax errors.
func goError(err error) string {
	var list scanner.ErrorList
	if errors.As(err, &list) && len(list) > 3 {
		list = list[:3]
		return list.Error()
	}
	return err.Error()
}

// nodeCheck runs node --check on one file, the path passed as its own argument (no shell).
func nodeCheck(ctx context.Context, node, root, abs string, env []string) string {
	ctx, cancel := context.WithTimeout(ctx, nodeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--check", abs)
	cmd.Dir, cmd.Env = root, env
	platform.HideWindowShared(cmd) // a fixed helper program
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err == nil {
		return ""
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return "" // node could not run: not the file's fault
	}
	return Tail(strings.TrimSpace(buf.String()), 600)
}
