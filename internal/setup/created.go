package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// CreatedFile is the name of the record, in the data folder, of the host files and folders setup created.
const CreatedFile = "setup-created.json"

// createdVersion is the format version of the record.
const createdVersion = 1

// folderHost is the host of the folder items: the folders belong to no single host.
const folderHost = "setup"

// created is the record of the host config files and folders setup created (they did not exist before it wrote
// them). `setup --remove` deletes only those, and only once nothing but Seelegent's entry was in them, so it never
// deletes a file or folder that existed before setup.
type created struct {
	path    string        // the record file; "" records nothing
	Version int           `json:"version"`
	Paths   []createdPath `json:"created"`
	changed bool
}

// createdPath is one recorded file or folder.
type createdPath struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir,omitempty"`
}

// loadCreated reads the record; a missing file is an empty record, an unreadable one an error.
func loadCreated(path string) (*created, error) {
	c := &created{path: path, Version: createdVersion}
	if path == "" {
		return c, nil
	}
	data, err := fsutil.ReadFile(path)
	if notExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s is not readable (%w); fix or delete it", path, err)
	}
	if c.Version != createdVersion {
		return nil, fmt.Errorf("%s has format version %d; this seelegent reads version %d", path, c.Version,
			createdVersion)
	}
	return c, nil
}

// save writes the record when it changed.
func (c *created) save() error {
	if c == nil || c.path == "" || !c.changed {
		return nil
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(c.path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	c.changed = false
	return nil
}

// key compares paths the way the file system does.
func pathKey(p string) string {
	if k, err := fsutil.Key(p); err == nil {
		return k
	}
	return filepath.Clean(p)
}

func (c *created) index(p string) int {
	if c == nil {
		return -1
	}
	k := pathKey(p)
	return slices.IndexFunc(c.Paths, func(e createdPath) bool { return pathKey(e.Path) == k })
}

// has reports whether setup created p.
func (c *created) has(p string) bool { return c.index(p) >= 0 }

// add records that setup created p.
func (c *created) add(p string, dir bool) {
	if c == nil || c.path == "" || c.has(p) {
		return
	}
	c.Paths = append(c.Paths, createdPath{Path: p, Dir: dir})
	c.changed = true
}

// drop forgets p.
func (c *created) drop(p string) {
	if i := c.index(p); i >= 0 {
		c.Paths = slices.Delete(c.Paths, i, i+1)
		c.changed = true
	}
}

// mkdirAll creates dir and its missing parents, recording each folder it creates.
func (c *created) mkdirAll(dir string, perm os.FileMode) error {
	var missing []string
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(d); err == nil {
			break
		}
		missing = append(missing, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	for _, d := range missing {
		c.add(d, true)
	}
	return nil
}

// removeFolders deletes the recorded folders that are empty now, deepest first, and forgets the ones that are gone.
// Folders that still hold something stay recorded, so a later remove can try again.
func (c *created) removeFolders(dryRun bool) []Item {
	if c == nil {
		return nil
	}
	var dirs []string
	for _, e := range c.Paths {
		if e.Dir {
			dirs = append(dirs, e.Path)
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) })
	var items []Item
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		switch {
		case notExist(err):
			c.drop(d)
		case err != nil || len(entries) > 0:
		case dryRun:
			items = append(items, Item{Host: folderHost, Kind: KindFolder, Action: ActionRemoved, Path: d,
				Detail: "setup created this folder and it is empty"})
		default:
			if err := os.Remove(d); err != nil && !errors.Is(err, os.ErrNotExist) {
				items = append(items, failItem(Item{Host: folderHost, Kind: KindFolder, Path: d}, err))
				continue
			}
			c.drop(d)
			items = append(items, Item{Host: folderHost, Kind: KindFolder, Action: ActionRemoved, Path: d,
				Detail: "removed the empty folder setup had created"})
		}
	}
	return items
}

// writeHostFile writes a host config file after a change; on remove, a file setup created that holds nothing but
// Seelegent's entry any more (bare says so, for the new content and for the backup) is deleted instead, with a
// backup of it that holds nothing else either. note is added to the item's detail.
func (o op) writeHostFile(f hostFile, data []byte, bare func([]byte) bool) (backup, note string, err error) {
	if !o.req.Remove || !f.existed || !o.created.has(f.real) || !bare(data) {
		backup, err = f.write(data, o.created)
		return backup, "", err
	}
	if err := os.Remove(f.real); err != nil {
		return "", "", err
	}
	o.created.drop(f.real)
	note = "; deleted the file, which setup had created and which holds nothing else"
	bak := f.real + BackupSuffix
	if old, err := fsutil.ReadFile(bak); err == nil && bare(old) {
		if os.Remove(bak) == nil {
			note += " (and its backup)"
		}
	}
	return "", note, nil
}

// blank reports whether text is only white space.
func blank(data []byte) bool {
	return strings.TrimSpace(strings.TrimPrefix(string(data), utf8BOM)) == ""
}
