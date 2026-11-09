package gitx

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// isImage reports a path the Git view previews as an image (api.GitImageType).
func isImage(p string) bool {
	_, ok := api.GitImageType(strings.ToLower(path.Ext(p)))
	return ok
}

// stampImages sets the Stamp of the images among files (paths relative to root): the working tree file's size and
// modification time in nanoseconds (an Lstat; empty when the file is gone) and its blob id at HEAD from heads
// (empty for a new file). Neither reads the file, so a rewritten image changes its stamp at the cost of one Lstat.
func stampImages(root string, heads map[string]string, files []api.GitFile) {
	for i := range files {
		f := &files[i]
		if !isImage(f.Path) {
			continue
		}
		var work string
		if st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f.Path))); err == nil {
			work = strconv.FormatInt(st.Size(), 36) + "-" + strconv.FormatInt(st.ModTime().UnixNano(), 36)
		}
		f.Stamp = work + "." + heads[f.Path]
	}
}
