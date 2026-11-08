package gitx

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git object ids of SHA-1 repositories, not a security use
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// Normalizes reports whether `git add` would store data, as the file rel (relative to root, '/' separators) of
// the repository at root, with every CRLF turned into LF: what core.autocrlf (true or input) and the text and eol
// attributes do, so the line endings of such a working copy never reach a commit. It hashes data through the
// clean filters with `git hash-object --path` (nothing is written) and compares the id with that of the LF form.
// Content without CRLF is never normalized; another clean filter (LFS, a custom driver) gives a different id and
// reports false.
func (r *Repos) Normalizes(ctx context.Context, root, rel string, data []byte) (bool, error) {
	crlf := []byte("\r\n")
	if !bytes.Contains(data, crlf) {
		return false, nil
	}
	out, _, err := r.runIn(ctx, root, nil, data, ReadTimeout, 256, "hash-object", "--stdin", "--path="+rel)
	if err != nil {
		return false, err
	}
	id := strings.TrimSpace(string(out))
	want, ok := blobID(bytes.ReplaceAll(data, crlf, []byte("\n")), len(id))
	return ok && id == want, nil
}

// blobID returns git's object id of a blob with content data, for a repository whose ids have hexLen digits
// (40: SHA-1, 64: SHA-256).
func blobID(data []byte, hexLen int) (string, bool) {
	var h hash.Hash
	switch hexLen {
	case 40:
		h = sha1.New() //nolint:gosec // git object ids of SHA-1 repositories, not a security use
	case 64:
		h = sha256.New()
	default:
		return "", false
	}
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), true
}
