package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewPaths(t *testing.T) {
	home := t.TempDir()
	p, err := NewPaths(home)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Config":   filepath.Join(home, "config.json"),
		"DB":       filepath.Join(home, "seelegent.db"),
		"Profiles": filepath.Join(home, "profiles"),
		"Logs":     filepath.Join(home, "logs"),
		"Run":      filepath.Join(home, "run"),
		"Backups":  filepath.Join(home, "backups"),
		"LockFile": filepath.Join(home, "run", "daemon.lock"),
		"PortFile": filepath.Join(home, "run", "daemon.port"),
	}
	got := map[string]string{
		"Config": p.Config, "DB": p.DB, "Profiles": p.Profiles, "Logs": p.Logs, "Run": p.Run,
		"Backups": p.Backups, "LockFile": p.LockFile, "PortFile": p.PortFile,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if p.Home != home {
		t.Errorf("Home = %q, want %q", p.Home, home)
	}
}

func TestNewPathsRelativeAndEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := NewPaths("data")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p.Home) || filepath.Base(p.Home) != "data" {
		t.Fatalf("Home = %q, want an absolute path ending in data", p.Home)
	}
	if _, err := NewPaths("  "); err == nil {
		t.Fatal("want an error for an empty data directory")
	}
}

func TestResolvePaths(t *testing.T) {
	userHome := t.TempDir()
	envHome := filepath.Join(t.TempDir(), "custom")
	errNoHome := errors.New("no home")
	tests := []struct {
		name     string
		env      map[string]string
		homeErr  error
		wantHome string
		wantErr  bool
	}{
		{name: "default", wantHome: filepath.Join(userHome, DefaultDirName)},
		{name: "SEELEGENT_HOME", env: map[string]string{EnvHome: envHome}, wantHome: envHome},
		{name: "blank SEELEGENT_HOME is ignored", env: map[string]string{EnvHome: " "}, wantHome: filepath.Join(userHome, DefaultDirName)},
		{name: "SEELEGENT_HOME without user home", env: map[string]string{EnvHome: envHome}, homeErr: errNoHome, wantHome: envHome},
		{name: "no user home", homeErr: errNoHome, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(k string) string { return tt.env[k] }
			homeDir := func() (string, error) { return userHome, tt.homeErr }
			p, err := ResolvePaths(getenv, homeDir)
			if tt.wantErr {
				if !errors.Is(err, errNoHome) {
					t.Fatalf("err = %v, want it to wrap the home directory error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Home != tt.wantHome {
				t.Fatalf("Home = %q, want %q", p.Home, tt.wantHome)
			}
		})
	}
}

func TestDefaultPaths(t *testing.T) {
	home := filepath.Join(t.TempDir(), "h")
	t.Setenv(EnvHome, home)
	p, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if p.Home != home {
		t.Fatalf("Home = %q, want %q", p.Home, home)
	}
}

func TestEnsureDirs(t *testing.T) {
	p, err := NewPaths(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("NewPaths must not create directories")
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("second EnsureDirs: %v", err)
	}
	for _, dir := range []string{p.Home, p.Profiles, p.Logs, p.Run, p.Backups} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", dir, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != dirPerm {
			t.Errorf("%s mode = %v, want %v", dir, info.Mode().Perm(), os.FileMode(dirPerm))
		}
	}
}

func TestEnsureDirsFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := NewPaths(filepath.Join(file, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureDirs(); err == nil {
		t.Fatal("want an error when the data directory cannot be created")
	}
}

func TestResolvePort(t *testing.T) {
	s := Defaults()
	s.Port = 40000
	tests := []struct {
		env     string
		want    int
		wantErr bool
	}{
		{env: "", want: 40000},
		{env: "47831", want: 47831},
		{env: " 47831 ", want: 47831},
		{env: "0", wantErr: true},
		{env: "65536", wantErr: true},
		{env: "port", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ResolvePort(s, func(k string) string {
			if k == EnvPort {
				return tt.env
			}
			return ""
		})
		if tt.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("SEELEGENT_PORT=%q: err = %v, want ErrInvalid", tt.env, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("SEELEGENT_PORT=%q: got %d, %v, want %d", tt.env, got, err, tt.want)
		}
	}
}
