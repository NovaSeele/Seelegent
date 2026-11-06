package role

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// skillCacheTTL is how long a discovered skill list is reused.
const skillCacheTTL = 30 * time.Second

// skillDescriptionChars caps a skill's description.
const skillDescriptionChars = 200

// Skill is a skill found on disk: a folder holding SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Path is the skill folder, File its SKILL.md.
	Path string `json:"path"`
	File string `json:"file"`
	// Root names where it was found: project, agents, gemini or claude.
	Root string `json:"root"`
}

type skillCache struct {
	at     time.Time
	skills []Skill
}

// Skills lists the skills a task in cwd can use, first match by name wins: the nearest .agents/skills folder at or
// above cwd, then ~/.agents/skills, ~/.gemini/config/skills and ~/.claude/skills. The list is cached for 30 s per
// folder.
func (s *Store) Skills(cwd string) []Skill {
	key := "\x00global"
	if cwd != "" {
		if k, err := fsutil.Key(cwd); err == nil {
			key = k
		}
	}
	now := s.now()
	s.mu.Lock()
	if c, ok := s.skills[key]; ok && now.Sub(c.at) < skillCacheTTL && !now.Before(c.at) {
		s.mu.Unlock()
		return c.skills
	}
	s.mu.Unlock()

	type root struct{ name, dir string }
	var roots []root
	if cwd != "" {
		for cur := filepath.Clean(cwd); ; {
			candidate := filepath.Join(cur, ".agents", "skills")
			if isDir(candidate) {
				roots = append(roots, root{"project", candidate})
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	if s.home != "" {
		roots = append(roots, root{"agents", filepath.Join(s.home, ".agents", "skills")},
			root{"gemini", filepath.Join(s.home, ".gemini", "config", "skills")},
			root{"claude", filepath.Join(s.home, ".claude", "skills")})
	}
	skills := []Skill{}
	seen := map[string]bool{}
	for _, r := range roots {
		entries, err := os.ReadDir(r.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(r.dir, e.Name())
			file := filepath.Join(dir, "SKILL.md")
			if !isFile(file) {
				continue
			}
			name, desc := frontMatter(file, e.Name())
			if seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			skills = append(skills, Skill{Name: name, Description: desc, Path: dir, File: file, Root: r.name})
		}
	}
	s.mu.Lock()
	s.skills[key] = skillCache{at: now, skills: skills}
	s.mu.Unlock()
	return skills
}

// Brief returns the role as a worker brief shows it, with its skills resolved for cwd: a skill is a name looked up
// in Skills(cwd), or an absolute path to a skill folder.
func (s *Store) Brief(r Role, cwd string) *brief.Role {
	out := &brief.Role{ID: r.ID, Name: r.Name, Description: r.Description, Rules: r.Rules, OutputStyle: r.OutputStyle}
	if len(r.Skills) == 0 {
		return out
	}
	byName := map[string]Skill{}
	for _, sk := range s.Skills(cwd) {
		byName[strings.ToLower(sk.Name)] = sk
	}
	for _, item := range r.Skills {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if filepath.IsAbs(item) {
			file := filepath.Join(item, "SKILL.md")
			if !isFile(file) {
				out.Missing = append(out.Missing, item)
				continue
			}
			name, desc := frontMatter(file, filepath.Base(item))
			out.Skills = append(out.Skills, brief.Skill{Name: name, Description: desc, File: filepath.Clean(file)})
			continue
		}
		if sk, ok := byName[strings.ToLower(item)]; ok {
			out.Skills = append(out.Skills, brief.Skill{Name: sk.Name, Description: sk.Description, File: sk.File})
		} else {
			out.Missing = append(out.Missing, item)
		}
	}
	return out
}

var (
	nameLine = regexp.MustCompile(`^name:\s*(.+)$`)
	descLine = regexp.MustCompile(`^description:\s*(.+)$`)
)

// frontMatter reads name and description from the YAML front matter of a SKILL.md (fallback when it has no name).
func frontMatter(file, fallback string) (name, description string) {
	name = fallback
	f, err := os.Open(file)
	if err != nil {
		return name, ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return name, ""
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "---" {
			break
		}
		if m := nameLine.FindStringSubmatch(line); m != nil {
			if v := unquote(m[1]); v != "" {
				name = v
			}
		} else if m := descLine.FindStringSubmatch(line); m != nil {
			description = unquote(m[1])
			if r := []rune(description); len(r) > skillDescriptionChars {
				description = string(r[:skillDescriptionChars])
			}
		}
	}
	return name, description
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		s = s[1 : len(s)-1]
	}
	return strings.TrimSpace(s)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
