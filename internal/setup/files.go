package setup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// BackupSuffix is added to a host config file's name for the copy taken before setup changes it. It is not plain
// ".bak" because some hosts keep their own <file>.bak (Codex does), which setup must not overwrite.
const BackupSuffix = ".seelegent.bak"

// newFilePerm is the permission of a host config file setup creates: it may hold environment values.
const newFilePerm = 0o600

// hostFile is a host config file as read from disk.
type hostFile struct {
	// path is the configured location; real is where the bytes live (the target of a symbolic link, so writing
	// never replaces a user's dotfile link with a plain file).
	path, real string
	data       []byte
	existed    bool
	perm       fs.FileMode
}

// readHostFile reads a host config file; a missing file is not an error.
func readHostFile(path string) (hostFile, error) {
	f := hostFile{path: path, real: path, perm: newFilePerm}
	// Only a link is resolved: EvalSymlinks would also rewrite a Windows short (8.3) path to its long form.
	if st, err := os.Lstat(path); err == nil && st.Mode()&fs.ModeSymlink != 0 {
		if r, err := filepath.EvalSymlinks(path); err == nil {
			f.real = r
		}
	}
	st, err := os.Stat(f.real)
	switch {
	case notExist(err):
		return f, nil
	case err != nil:
		return f, err
	case st.IsDir():
		return f, fmt.Errorf("%s is a directory", path)
	}
	f.perm = st.Mode().Perm()
	f.data, err = fsutil.ReadFile(f.real)
	if err != nil {
		return f, err
	}
	f.existed = true
	return f, nil
}

// write replaces the file with data atomically. An existing file is first copied to <file>.seelegent.bak, whose
// path is returned. A new file, and the folders made for it, are recorded in c.
func (f hostFile) write(data []byte, c *created) (backup string, err error) {
	if f.existed {
		backup = f.real + BackupSuffix
		if err := fsutil.WriteFileAtomic(backup, f.data, f.perm); err != nil {
			return "", fmt.Errorf("back up %s: %w", f.real, err)
		}
	} else if err := c.mkdirAll(filepath.Dir(f.real), 0o700); err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(f.real, data, f.perm); err != nil {
		return backup, err
	}
	if !f.existed {
		c.add(f.real, false)
	}
	return backup, nil
}

// backupOnly copies an existing file to <file>.seelegent.bak (before a host CLI changes it).
func (f hostFile) backupOnly() (string, error) {
	if !f.existed {
		return "", nil
	}
	backup := f.real + BackupSuffix
	if err := fsutil.WriteFileAtomic(backup, f.data, f.perm); err != nil {
		return "", fmt.Errorf("back up %s: %w", f.real, err)
	}
	return backup, nil
}
