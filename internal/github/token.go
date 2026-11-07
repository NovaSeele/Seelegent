package github

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// ErrNoToken is matched when no token is stored.
var ErrNoToken = errors.New("no GitHub token is stored")

// ErrInvalid is matched by a bad request to this package (an empty token, a bad name).
var ErrInvalid = errors.New("invalid GitHub request")

// maxToken caps a token.
const maxToken = 512

// tokenPurpose keeps the token's protected blob apart from other secrets.
const tokenPurpose = "github-token"

// TokenStore keeps the GitHub token pasted in the dashboard in one private file: a DPAPI blob for the current
// Windows user, a 0600 file elsewhere. The token leaves it only through Load, for the REST client and the gh child;
// the user sees its hint.
type TokenStore struct {
	path string
}

// NewTokenStore keeps the token in dir/token.
func NewTokenStore(dir string) *TokenStore { return &TokenStore{path: filepath.Join(dir, "token")} }

// CheckToken validates a token as pasted (trimmed).
func CheckToken(token string) error {
	switch {
	case token == "":
		return fmt.Errorf("%w: the token is empty", ErrInvalid)
	case len(token) > maxToken:
		return fmt.Errorf("%w: the token is longer than %d characters", ErrInvalid, maxToken)
	case strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return fmt.Errorf("%w: the token must not contain spaces", ErrInvalid)
	}
	return nil
}

// Save stores token and returns its hint.
func (s *TokenStore) Save(token string) (string, error) {
	token = strings.TrimSpace(token)
	if err := CheckToken(token); err != nil {
		return "", err
	}
	blob, err := account.ProtectSecret([]byte(token), tokenPurpose)
	if err != nil {
		return "", fmt.Errorf("store the GitHub token: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return "", fmt.Errorf("store the GitHub token: %w", err)
	}
	if err := fsutil.WriteFileAtomic(s.path, blob, 0o600); err != nil {
		return "", fmt.Errorf("store the GitHub token: %w", err)
	}
	return account.KeyHint(token), nil
}

// Load returns the stored token, or an error matching ErrNoToken. A file that cannot be read or decrypted is a
// different error, never "no token".
func (s *TokenStore) Load() (string, error) {
	blob, err := fsutil.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", fmt.Errorf("read the GitHub token: %w", err)
	}
	plain, err := account.UnprotectSecret(blob, tokenPurpose)
	if err != nil {
		return "", fmt.Errorf("read the GitHub token: %w", err)
	}
	token := strings.TrimSpace(string(plain))
	if err := CheckToken(token); err != nil {
		return "", fmt.Errorf("read the GitHub token: the stored token is damaged: %w", err)
	}
	return token, nil
}

// Remove deletes the stored token; a missing one is not an error.
func (s *TokenStore) Remove() error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the GitHub token: %w", err)
	}
	return nil
}
