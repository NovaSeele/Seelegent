package gitx

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// collapseUntracked takes out of files (sorted by path) the untracked files of each folder that git shows as one
// entry under --untracked-files=normal (a folder with no tracked file inside, "dir/") when it holds more than
// api.GitCollapseOver of them, and returns those folders as rows. The extra git call runs only when some
// top-level folder holds that many untracked files, and only over those folders; `git ls-files --others` reads
// no tracked file's state, so it costs less than the status that found the files.
func (r *Repos) collapseUntracked(ctx context.Context, root string, files []api.GitFile) ([]api.GitFile,
	[]api.GitDir, error) {
	// Every folder over the limit lies in a top-level folder over the limit: those bound git's walk.
	tops := map[string]int{}
	for _, f := range files {
		if top, _, ok := strings.Cut(f.Path, "/"); ok && f.Status == api.GitUntracked {
			tops[top]++
		}
	}
	args := []string{"--literal-pathspecs", "ls-files", "-z", "--others", "--directory", "--no-empty-directory",
		"--exclude-standard", "--"}
	n := len(args)
	for top, count := range tops {
		if count > api.GitCollapseOver {
			args = append(args, top)
		}
	}
	if len(args) == n {
		return files, []api.GitDir{}, nil
	}
	out, _, err := r.run(ctx, root, ReadTimeout, 0, args...)
	if err != nil {
		return nil, nil, err
	}
	whole := map[string]bool{} // folders git shows as one entry, with their trailing '/'
	for _, rec := range strings.Split(string(out), "\x00") {
		if strings.HasSuffix(rec, "/") {
			whole[rec] = true
		}
	}
	groups := map[string][]api.GitFile{}
	kept := make([]api.GitFile, 0, len(files))
	for _, f := range files {
		if dir := wholeDir(whole, f); dir != "" {
			groups[dir] = append(groups[dir], f)
			continue
		}
		kept = append(kept, f)
	}
	dirs := []api.GitDir{}
	for dir, in := range groups {
		if len(in) <= api.GitCollapseOver {
			continue
		}
		d := api.GitDir{Path: dir, Count: len(in), Files: in[:min(len(in), api.GitDirFiles)]}
		for _, f := range in {
			if st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(f.Path))); err == nil {
				d.Bytes += st.Size()
			}
		}
		dirs = append(dirs, d)
		delete(groups, dir)
	}
	if len(groups) > 0 { // folders at or under the limit stay as files, in path order
		kept = kept[:0]
		for _, f := range files {
			if dir := wholeDir(whole, f); dir == "" || groups[dir] != nil {
				kept = append(kept, f)
			}
		}
	}
	slices.SortFunc(dirs, func(a, b api.GitDir) int { return strings.Compare(a.Path, b.Path) })
	return kept, dirs, nil
}

// wholeDir returns the folder of whole (with its '/') that holds untracked file f, or "".
func wholeDir(whole map[string]bool, f api.GitFile) string {
	if f.Status != api.GitUntracked {
		return ""
	}
	for dir := path.Dir(f.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if whole[dir+"/"] {
			return dir + "/"
		}
	}
	return ""
}
