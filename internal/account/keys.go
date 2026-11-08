package account

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// keyFileName is the file under profiles/<id>/ (next to the CLI home, not inside it, so the worker's own tools
// cannot read it) that holds the API key of an API-key account.
const keyFileName = "seelegent-key"

// maxKeyLen caps an API key.
const maxKeyLen = 512

// Keys stores the API keys of API-key accounts, one file per account under its profile directory. On Windows the
// file holds a DPAPI blob that only the current Windows user can decrypt; elsewhere it holds the key in a file with
// mode 0600 (see the open question about the macOS Keychain in docs).
//
// The key leaves this type only through Load, for the environment of a worker turn; everything shown to the user
// goes through KeyHint.
type Keys struct {
	profiles Profiles
}

// NewKeys returns the key store of the profiles under profiles.
func NewKeys(profiles Profiles) Keys { return Keys{profiles: profiles} }

// path returns the key file of the account.
func (k Keys) path(id string) (string, error) {
	dir, err := k.profiles.Dir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFileName), nil
}

// Save cleans and validates key (NormalizeKey), stores it for the account and returns its hint.
func (k Keys) Save(id, key string) (hint string, err error) {
	key, _, err = NormalizeKey(key)
	if err != nil {
		return "", err
	}
	path, err := k.path(id)
	if err != nil {
		return "", err
	}
	blob, err := protect([]byte(key), id)
	if err != nil {
		return "", fmt.Errorf("store API key of %s: %w", id, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", fmt.Errorf("store API key of %s: %w", id, err)
	}
	if err := fsutil.WriteFileAtomic(path, blob, 0o600); err != nil {
		return "", fmt.Errorf("store API key of %s: %w", id, err)
	}
	return KeyHint(key), nil
}

// Load returns the stored key of the account, or an error matching ErrNoKey when none is stored. A key file that
// exists but cannot be read or decrypted is a different error: it is never treated as "no key".
func (k Keys) Load(id string) (string, error) {
	path, err := k.path(id)
	if err != nil {
		return "", err
	}
	blob, err := fsutil.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("account %s: %w", id, ErrNoKey)
	}
	if err != nil {
		return "", fmt.Errorf("read API key of %s: %w", id, err)
	}
	plain, err := unprotect(blob, id)
	if err != nil {
		return "", fmt.Errorf("read API key of %s: %w", id, err)
	}
	key := strings.TrimSpace(string(plain))
	if err := validateKey(key); err != nil {
		return "", fmt.Errorf("read API key of %s: stored key is damaged: %w", id, err)
	}
	return key, nil
}

// Has reports whether a key file exists for the account.
func (k Keys) Has(id string) bool {
	path, err := k.path(id)
	if err != nil {
		return false
	}
	_, err = os.Lstat(path)
	return err == nil
}

// Remove deletes the stored key; a missing key is not an error.
func (k Keys) Remove(id string) error {
	path, err := k.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove API key of %s: %w", id, err)
	}
	return nil
}

// KeyHint returns what the user may see of a key: "…" and its last four characters, or only "…" for a key shorter
// than eight characters (showing four of them would show too much).
func KeyHint(key string) string {
	r := []rune(key)
	if len(r) < 8 {
		return "…"
	}
	return "…" + string(r[len(r)-4:])
}

// ProtectSecret encrypts a secret of the daemon (such as the GitHub token) the way API keys are stored: DPAPI for
// the current Windows user, the bytes as they are elsewhere (the caller keeps them in a 0600 file). purpose keeps
// blobs of different secrets apart.
func ProtectSecret(data []byte, purpose string) ([]byte, error) {
	return protect(data, "secret:"+purpose)
}

// UnprotectSecret decrypts a blob made by ProtectSecret with the same purpose.
func UnprotectSecret(blob []byte, purpose string) ([]byte, error) {
	return unprotect(blob, "secret:"+purpose)
}
