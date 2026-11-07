package gitx

import "strings"

// strayGitVars are variables that point git at another repository, index or object store, or change how it
// prints a diff. A daemon started from a git hook or an editor's terminal can inherit them; every git the daemon
// runs must work on the repository it was asked about, with git's default output. GIT_CONFIG_GLOBAL,
// GIT_CONFIG_NOSYSTEM and GIT_SSH* are kept.
var strayGitVars = []string{
	"GIT_DIFF_OPTS", "GIT_EXTERNAL_DIFF",
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_PREFIX",
}

// Env returns base without the stray git variables, followed by extra. Names are compared without case, as
// Windows does.
func Env(base []string, extra ...string) []string {
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if !stray(name) {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

func stray(name string) bool {
	for _, v := range strayGitVars {
		if strings.EqualFold(name, v) {
			return true
		}
	}
	return false
}
