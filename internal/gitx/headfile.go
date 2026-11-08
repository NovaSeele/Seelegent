package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// HeadFile reads rel (relative to root, '/' separators, from CleanPath) as committed at HEAD: its size, and its
// content when that is at most limit bytes (nil otherwise: the caller refuses it with the size). ok is false when
// HEAD is unborn or holds no file at rel. The size is read first (`cat-file --batch-check`), so a large blob is
// never read; the read itself is capped at limit too. No filter applies: an LFS file reads as its pointer.
func (r *Repos) HeadFile(ctx context.Context, root, rel string, limit int) (data []byte, size int64, ok bool,
	err error) {
	if strings.ContainsAny(rel, "\n\r\x00") {
		return nil, 0, false, fmt.Errorf("%w: path %q", ErrInvalid, rel)
	}
	out, _, err := r.runIn(ctx, root, nil, []byte("HEAD:"+rel+"\n"), ReadTimeout, 4096, "cat-file",
		"--batch-check")
	if err != nil {
		return nil, 0, false, err
	}
	// "<oid> blob <size>"; "HEAD:<rel> missing" when there is no such object (an unborn HEAD included).
	f := strings.Fields(strings.TrimSpace(string(out)))
	if len(f) != 3 || f[1] != "blob" || !fullSHARe.MatchString(f[0]) {
		return nil, 0, false, nil
	}
	if size, err = strconv.ParseInt(f[2], 10, 64); err != nil || size < 0 {
		return nil, 0, false, fmt.Errorf("gitx: cat-file size %q", f[2])
	}
	if size > int64(limit) {
		return nil, size, true, nil
	}
	data, over, err := r.run(ctx, root, ReadTimeout, limit, "cat-file", "blob", f[0])
	if err != nil {
		return nil, 0, false, err
	}
	if over {
		return nil, int64(limit) + 1, true, nil
	}
	return data, int64(len(data)), true, nil
}
