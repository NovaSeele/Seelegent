// Package logx sets up structured logging (log/slog) for Seelegent: JSON lines to a size-rotated file for the
// daemon, text to stderr for the CLI, and a redacting handler in front of both so that API keys, tokens and email
// addresses never reach a log.
package logx

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
)

// Defaults of FileOptions.
const (
	DefaultFileName = "daemon.log"
	DefaultMaxBytes = 10 << 20 // 10 MiB per file
	DefaultKeep     = 5        // rotated copies kept, so at most about 60 MiB of logs
)

// FileOptions configures NewFileLogger. Zero values take the defaults.
type FileOptions struct {
	// Dir is the log directory (the data directory's logs/). Required; it must exist.
	Dir string
	// Name is the file name inside Dir; default daemon.log.
	Name string
	// MaxBytes is the size at which the file rotates; default 10 MiB.
	MaxBytes int64
	// Keep is the number of rotated copies kept; default 5. Use NoBackups to keep none.
	Keep int
	// Level is the minimum level written; default Info.
	Level slog.Leveler
	// Console, when set, also receives every record as text (for a daemon running in the foreground).
	Console io.Writer
	// Secrets are literal values to mask in addition to the built-in patterns.
	Secrets []string
}

// NoBackups as FileOptions.Keep rotates by truncating the file instead of keeping copies.
const NoBackups = -1

// NewFileLogger returns a logger that writes redacted JSON lines to a rotating file (and, if o.Console is set,
// redacted text to the console). Close the returned closer at shutdown.
func NewFileLogger(o FileOptions) (*slog.Logger, io.Closer, error) {
	if o.Dir == "" {
		return nil, nil, errors.New("log directory not set")
	}
	name := o.Name
	if name == "" {
		name = DefaultFileName
	}
	maxBytes := o.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	keep := o.Keep
	switch {
	case keep == NoBackups:
		keep = 0
	case keep <= 0:
		keep = DefaultKeep
	}
	file, err := OpenRotatingFile(filepath.Join(o.Dir, name), maxBytes, keep)
	if err != nil {
		return nil, nil, err
	}
	opts := &slog.HandlerOptions{Level: level(o.Level)}
	var h slog.Handler = slog.NewJSONHandler(file, opts)
	if o.Console != nil {
		h = slog.NewMultiHandler(h, slog.NewTextHandler(o.Console, opts))
	}
	return slog.New(NewRedactingHandler(h, NewRedactor(o.Secrets...))), file, nil
}

// NewConsoleLogger returns a logger that writes redacted text records to w, normally os.Stderr for CLI commands.
func NewConsoleLogger(w io.Writer, lvl slog.Leveler, secrets ...string) *slog.Logger {
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level(lvl)})
	return slog.New(NewRedactingHandler(h, NewRedactor(secrets...)))
}

// level returns l, or Info when l is nil.
func level(l slog.Leveler) slog.Leveler {
	if l == nil {
		return slog.LevelInfo
	}
	return l
}
