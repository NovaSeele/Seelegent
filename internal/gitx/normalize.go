package gitx

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git object ids of SHA-1 repositories, not a security use
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// indexBlobMax caps the index version read for its carriage returns.
const indexBlobMax = 32 << 20

// Normalizes reports whether `git add` would store data, as the file rel (relative to root, '/' separators) of
// the repository at root, with every CRLF turned into LF: what core.autocrlf (true or input) and the text and eol
// attributes do, so the line endings of such a working copy never reach a commit. It hashes data through the
// clean filters with `git hash-object --path` (nothing is written) and compares the id with that of the LF form.
// Content without CRLF is never normalized; another clean filter (LFS, a custom driver) gives a different id and
// reports false.
//
// hash-object does not read the index, so it misses git's "safer autocrlf" rule: an automatic conversion
// (core.autocrlf, text=auto) is skipped when the index version of the file already has a CR, and `git add` keeps
// the CRLF lines. Normalizes applies that rule itself (autoEOL, the index blob read raw).
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
	if !ok || id != want {
		return false, nil
	}
	auto, err := r.autoEOL(ctx, root, rel)
	if err != nil {
		return false, err
	}
	if !auto {
		return true, nil
	}
	idx, err := r.indexBlob(ctx, root, rel)
	if err != nil {
		return false, err
	}
	return !bytes.Contains(idx, []byte("\r")), nil
}

// autoEOL reports whether the line ending conversion of rel is git's automatic one (convert.c CRLF_AUTO*), the
// one the safer autocrlf rule applies to: text=auto (with or without eol), or neither text nor eol set, which
// leaves it to core.autocrlf. An explicit text, -text or eol without text=auto is not automatic.
func (r *Repos) autoEOL(ctx context.Context, root, rel string) (bool, error) {
	out, _, err := r.run(ctx, root, ReadTimeout, 64<<10, "check-attr", "-z", "text", "eol", "--", rel)
	if err != nil {
		return false, err
	}
	// -z: path NUL attribute NUL value NUL, for each attribute.
	vals := map[string]string{}
	parts := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(parts); i += 3 {
		vals[parts[i+1]] = parts[i+2]
	}
	text, eol := vals["text"], vals["eol"]
	return text == "auto" || (text == "unspecified" && eol == "unspecified"), nil
}

// indexBlob returns the index version of rel as stored (no filters), nil when the index has none.
func (r *Repos) indexBlob(ctx context.Context, root, rel string) ([]byte, error) {
	out, _, err := r.run(ctx, root, ReadTimeout, indexBlobMax, "cat-file", "blob", ":"+rel)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && !ge.Timeout {
			return nil, nil // not in the index
		}
		return nil, err
	}
	return out, nil
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
