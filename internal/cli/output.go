package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// Error codes the command line adds to the API's (printed with --json).
const (
	codeUsage        = "usage"
	codeNotAvailable = "not_available"
	codeDaemon       = "daemon_unavailable"
	codeLocked       = "daemon_locked"
	codeError        = "error"
)

// usageError is a wrong command line: exit 2.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func usagef(format string, a ...any) error { return &usageError{err: fmt.Errorf(format, a...)} }

// quietError ends the command with exit 1 after it printed everything itself (doctor with a failed check).
type quietError struct{ msg string }

func (e *quietError) Error() string { return e.msg }

// errorOut is an error as --json prints it.
type errorOut struct {
	Error   string         `json:"error"`
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// classify maps an error onto its exit code and printed form.
func classify(err error) (int, errorOut) {
	out := errorOut{Error: err.Error(), Code: codeError}
	var apiErr *client.Error
	var foreign *client.ForeignError
	var stuck *client.StuckError
	switch {
	case errors.As(err, new(*usageError)):
		return ExitUsage, errorOut{Error: err.Error(), Code: codeUsage}
	case errors.As(err, new(*daemonRunningError)), errors.As(err, new(*portTakenError)):
		return ExitDaemonLocked, errorOut{Error: err.Error(), Code: codeLocked}
	case errors.Is(err, ops.WorkerError{}):
		return ExitError, errorOut{Error: ops.WorkerMessage, Code: string(api.CodeForbidden),
			Details: map[string]any{"caller": "worker"}}
	case errors.As(err, &apiErr) && apiErr.Worker():
		return ExitError, errorOut{Error: ops.WorkerMessage, Code: string(apiErr.Code), Details: apiErr.Details}
	case errors.As(err, &apiErr) && (apiErr.Code == api.CodeNotImplemented || apiErr.Status == 501):
		u := &ops.UnavailableError{What: "this part of the command", Detail: apiErr.Message}
		return ExitError, errorOut{Error: u.Error(), Code: codeNotAvailable}
	case errors.As(err, &apiErr):
		return ExitError, errorOut{Error: apiErr.Message, Code: string(apiErr.Code), Details: apiErr.Details}
	case errors.As(err, new(*ops.UnavailableError)):
		out.Code = codeNotAvailable
	case errors.As(err, &foreign), errors.As(err, &stuck), errors.Is(err, client.ErrNotRunning),
		errors.Is(err, client.ErrUnreachable):
		out.Code = codeDaemon
	}
	return ExitError, out
}

// report prints err (as JSON on stdout with --json, else on stderr) and returns the exit code.
func report(err error, asJSON bool, stdout, stderr io.Writer) int {
	code, out := classify(err)
	if q, isQuiet := errors.AsType[*quietError](err); isQuiet {
		if asJSON {
			return code // the command printed its JSON already
		}
		_, _ = fmt.Fprintln(stderr, q.msg)
		return code
	}
	if asJSON {
		_ = printJSON(stdout, out)
		return code
	}
	if out.Error == ops.WorkerMessage {
		_, _ = fmt.Fprintln(stderr, out.Error)
		return code
	}
	_, _ = fmt.Fprintf(stderr, "Error: %s\n", out.Error)
	return code
}

// printJSON writes v as indented JSON without HTML escaping.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// emit prints v as JSON with --json, else calls text.
func emit(cmd *cobra.Command, v any, text func(w io.Writer)) error {
	if jsonFlag(cmd) {
		return printJSON(cmd.OutOrStdout(), v)
	}
	text(cmd.OutOrStdout())
	return nil
}

// fileArg returns s, or the content of the file s names when it starts with @ (@path, relative to base when not
// absolute). base is the --cwd of the command, else the process's folder.
func fileArg(s, base string) (string, error) {
	if !strings.HasPrefix(s, "@") || len(s) == 1 {
		return s, nil
	}
	p := s[1:]
	if !filepath.IsAbs(p) {
		if base == "" {
			wd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			base = wd
		}
		p = filepath.Join(base, p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", s, err)
	}
	return string(data), nil
}

// absDir resolves a --cwd value against the process's folder ("" stays "").
func absDir(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Abs(p)
}

// noArgs, minArgs (at least one) and rangeArgs check positional arguments and report usage errors (exit 2).
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usagef("unknown argument %q for %q", args[0], cmd.CommandPath())
	}
	return nil
}

func minArgs(usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return usagef("usage: %s", usage)
		}
		return nil
	}
}

func rangeArgs(lo, hi int, usage string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < lo || len(args) > hi {
			return usagef("usage: %s", usage)
		}
		return nil
	}
}
