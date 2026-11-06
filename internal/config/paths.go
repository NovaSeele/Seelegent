// Package config owns where Seelegent keeps its data and what the user can configure: the data directory layout
// (Paths), the typed settings with their defaults and validation (Settings), and loading and saving config.json
// without ever losing or silently replacing what is on disk (Load, Config.Save, Update).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment variables that override the defaults.
const (
	// EnvHome selects the data directory instead of ~/.seelegent.
	EnvHome = "SEELEGENT_HOME"
	// EnvPort selects the daemon port instead of the port setting. It is never written to config.json.
	EnvPort = "SEELEGENT_PORT"
)

// DefaultDirName is the data directory name under the user's home directory.
const DefaultDirName = ".seelegent"

// dirPerm is the permission of every directory Seelegent creates under the data directory: it holds sign-in
// profiles and API keys, so only the owner may enter it.
const dirPerm = 0o700

// Paths lists every location under the data directory. Build it with NewPaths or ResolvePaths; the fields are
// absolute paths.
type Paths struct {
	// Home is the data directory itself.
	Home string
	// Config is config.json, the settings file.
	Config string
	// DB is seelegent.db, the SQLite database.
	DB string
	// Profiles holds one isolated CLI profile per worker account (profiles/<account-id>/), at the same place as the
	// old version so existing sign-ins keep working.
	Profiles string
	// Logs holds the daemon's rotated log files.
	Logs string
	// Run holds files that only matter while the daemon runs.
	Run string
	// Backups holds copies taken before migrations and imports.
	Backups string
	// Tasks holds one directory per task with the files of its worker attempts (launch spec, stream, stderr,
	// long briefs): tasks/<task-id>/t<turn>-a<attempt>/.
	Tasks string
	// LockFile is the single-instance lock of the daemon (run/daemon.lock).
	LockFile string
	// PortFile records the port the running daemon listens on (run/daemon.port).
	PortFile string
}

// NewPaths returns the layout of the data directory home. A relative home is made absolute.
func NewPaths(home string) (Paths, error) {
	if strings.TrimSpace(home) == "" {
		return Paths{}, errors.New("data directory: empty path")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return Paths{}, fmt.Errorf("data directory %s: %w", home, err)
	}
	run := filepath.Join(abs, "run")
	return Paths{
		Home:     abs,
		Config:   filepath.Join(abs, "config.json"),
		DB:       filepath.Join(abs, "seelegent.db"),
		Profiles: filepath.Join(abs, "profiles"),
		Logs:     filepath.Join(abs, "logs"),
		Run:      run,
		Backups:  filepath.Join(abs, "backups"),
		Tasks:    filepath.Join(abs, "tasks"),
		LockFile: filepath.Join(run, "daemon.lock"),
		PortFile: filepath.Join(run, "daemon.port"),
	}, nil
}

// ResolvePaths picks the data directory from the environment: $SEELEGENT_HOME when set, otherwise
// ~/.seelegent. getenv and userHomeDir are usually os.Getenv and os.UserHomeDir; tests pass fakes.
func ResolvePaths(getenv func(string) string, userHomeDir func() (string, error)) (Paths, error) {
	if home := strings.TrimSpace(getenv(EnvHome)); home != "" {
		return NewPaths(home)
	}
	userHome, err := userHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("data directory: %w (set %s)", err, EnvHome)
	}
	return NewPaths(filepath.Join(userHome, DefaultDirName))
}

// DefaultPaths is ResolvePaths with the process environment.
func DefaultPaths() (Paths, error) {
	return ResolvePaths(os.Getenv, os.UserHomeDir)
}

// EnsureDirs creates the data directory and its subdirectories (owner-only permissions) if they are missing.
// Directories are created when a command starts, never as a side effect of building Paths.
func (p Paths) EnsureDirs() error {
	for _, dir := range []string{p.Home, p.Profiles, p.Logs, p.Run, p.Backups, p.Tasks} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}

// ResolvePort returns the port the daemon should listen on: $SEELEGENT_PORT when set, otherwise s.Port.
// getenv is usually os.Getenv.
func ResolvePort(s Settings, getenv func(string) string) (int, error) {
	raw := strings.TrimSpace(getenv(EnvPort))
	if raw == "" {
		return s.Port, nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < minPort || port > maxPort {
		return 0, fmt.Errorf("%s=%q: %w: must be a number between %d and %d", EnvPort, raw, ErrInvalid, minPort, maxPort)
	}
	return port, nil
}
