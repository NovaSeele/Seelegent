package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// The mask list of an attempt: the secrets (an API-key account's key) its output is masked for. It is kept like the
// stored API keys (account.ProtectSecret: DPAPI on Windows, a plain 0600 file elsewhere) in the attempt's folder
// while the worker runs, so a daemon that reattaches after a restart masks the same values even when the account's
// key was removed or changed meanwhile (bugs.md B15.11). It is removed when the attempt ends.
const (
	maskFile    = "mask"
	maskPurpose = "attempt-mask"
)

// maskNotice is the recovery event's notice for an attempt whose mask list could not be read.
const maskNotice = "the attempt's mask list could not be read; its output is masked by key patterns as well"

// attemptMask is what an attempt's output is masked for: literal secrets and, when its mask list could not be read
// after a restart, the key patterns of logx.Redactor as well (fail closed).
type attemptMask struct {
	secrets  []string
	patterns bool
}

// saveMask stores the mask list of the attempt in dir; nothing is written for an empty list.
func saveMask(dir string, secrets []string) error {
	if len(secrets) == 0 {
		return nil
	}
	data, err := json.Marshal(secrets)
	if err != nil {
		return err
	}
	blob, err := account.ProtectSecret(data, maskPurpose)
	if err != nil {
		return fmt.Errorf("protect the mask list: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, maskFile), blob, 0o600)
}

// loadMask returns the mask list stored in dir (none when the attempt had none).
func loadMask(dir string) ([]string, error) {
	blob, err := fsutil.ReadFile(filepath.Join(dir, maskFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	data, err := account.UnprotectSecret(blob, maskPurpose)
	if err != nil {
		return nil, err
	}
	var secrets []string
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("mask list: %w", err)
	}
	return secrets, nil
}

// removeMask deletes the mask list of an attempt that ended.
func removeMask(dir string) error {
	if err := os.Remove(filepath.Join(dir, maskFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// mergeSecrets returns the non-empty values of both lists, each once.
func mergeSecrets(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
