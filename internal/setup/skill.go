package setup

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// skillTemplate is the lead skill. It teaches only what this version can do: the tools and options that answer
// not_available are named once, as such (old F10).
//
//go:embed skill/SKILL.md.tmpl
var skillTemplate string

// SkillFile is the file name of a skill inside its folder.
const SkillFile = "SKILL.md"

// skillPerm is the permission of the installed skill file (it holds no secret; hosts must read it).
const skillPerm = 0o644

// RenderSkill returns the lead skill for this version, pointing at the dashboard on port.
func RenderSkill(version string, port int) (string, error) {
	t, err := template.New("skill").Option("missingkey=error").Parse(skillTemplate)
	if err != nil {
		return "", err
	}
	if version == "" {
		version = "dev"
	}
	var b bytes.Buffer
	err = t.Execute(&b, struct{ Version, Dashboard string }{
		Version:   strings.ReplaceAll(version, `"`, ""),
		Dashboard: fmt.Sprintf("http://127.0.0.1:%d/", port),
	})
	return b.String(), err
}

// isOurSkill reports whether a SKILL.md is Seelegent's: its front matter names the skill seelegent and says
// metadata.generator: seelegent.
func isOurSkill(data []byte) bool {
	text := strings.ReplaceAll(strings.TrimPrefix(string(data), utf8BOM), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return false
	}
	name, generator, inMeta := false, false, false
	for line := range strings.SplitSeq(text[4:4+end], "\n") {
		unquote := func(v string) string { return strings.Trim(strings.TrimSpace(v), `"'`) }
		switch {
		case strings.HasPrefix(line, "name:"):
			name = unquote(line[5:]) == ServerName
			inMeta = false
		case strings.HasPrefix(line, "metadata.generator:"):
			generator = unquote(line[len("metadata.generator:"):]) == ServerName
		case strings.TrimSpace(line) == "metadata:":
			inMeta = true
		case inMeta && strings.HasPrefix(strings.TrimSpace(line), "generator:") && line != strings.TrimSpace(line):
			generator = unquote(strings.TrimSpace(line)[len("generator:"):]) == ServerName
		case line != "" && line == strings.TrimLeft(line, " \t"):
			inMeta = false
		}
	}
	return name && generator
}

// installSkill writes the skill into <dir>/seelegent/SKILL.md.
func (o op) installSkill(hostID, dir string) Item {
	path := filepath.Join(dir, ServerName, SkillFile)
	it := Item{Host: hostID, Kind: KindSkill, Path: path}
	want, err := RenderSkill(o.req.Version, o.req.Port)
	if err != nil {
		return failItem(it, err)
	}
	have, err := fsutil.ReadFile(path)
	switch {
	case notExist(err):
		it.Action, it.Detail = ActionAdded, "installed the lead skill"
	case err != nil:
		return failItem(it, err)
	case strings.ReplaceAll(string(have), "\r\n", "\n") == want:
		it.Action, it.Detail = ActionUnchanged, "the skill is current"
		return it
	case isOurSkill(have):
		it.Action, it.Detail = ActionUpdated, "updated the lead skill"
	case !o.req.Force:
		it.Action, it.Detail = ActionConflict, "a skill named seelegent that is not Seelegent's is there; nothing "+
			"was written (rerun with --force to replace it)"
		return it
	default:
		it.Action, it.Detail = ActionUpdated, "replaced the skill that was not Seelegent's (--force)"
		it.Backup = path + BackupSuffix
	}
	if o.req.DryRun {
		it.Backup = ""
		return planned(it)
	}
	if it.Backup != "" {
		if err := fsutil.WriteFileAtomic(it.Backup, have, skillPerm); err != nil {
			return failItem(it, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return failItem(it, err)
	}
	if err := fsutil.WriteFileAtomic(path, []byte(want), skillPerm); err != nil {
		return failItem(it, err)
	}
	return it
}

// removeSkill deletes Seelegent's SKILL.md and its folder when nothing else is left in it.
func (o op) removeSkill(hostID, dir string) Item {
	folder := filepath.Join(dir, ServerName)
	path := filepath.Join(folder, SkillFile)
	it := Item{Host: hostID, Kind: KindSkill, Path: path}
	have, err := fsutil.ReadFile(path)
	switch {
	case notExist(err):
		it.Action, it.Detail = ActionSkipped, "not installed"
		return it
	case err != nil:
		return failItem(it, err)
	case !isOurSkill(have):
		it.Action, it.Detail = ActionSkipped, "the skill there is not Seelegent's; left alone"
		return it
	}
	it.Action, it.Detail = ActionRemoved, "removed the lead skill"
	if o.req.DryRun {
		return planned(it)
	}
	if err := os.Remove(path); err != nil {
		return failItem(it, err)
	}
	if err := os.Remove(folder); err != nil && !errors.Is(err, os.ErrNotExist) {
		it.Detail = "removed SKILL.md; the folder keeps other files"
	}
	return it
}
