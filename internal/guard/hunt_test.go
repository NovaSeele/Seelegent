package guard

import (
	"strings"
	"testing"
)

// TestExeSuffixSubcommand (H1): Git for Windows strips ".exe" from the subcommand, so git reset.exe --hard runs
// reset --hard and git st.exe runs the alias st. Both the git shim (Git) and the hook (Command) read it as the
// subcommand it names; the same names without a refused meaning stay allowed.
func TestExeSuffixSubcommand(t *testing.T) {
	c := Checker{Alias: func(name string) (string, bool) {
		if name == "st" {
			return "stash", true
		}
		return "", false
	}}
	shim := map[string]bool{
		"reset.exe --hard":    true,
		"stash.exe":           true,
		"stash.exe clear":     true,
		"clean.exe -fdx":      true,
		"checkout.exe -- .":   true,
		"push.exe --force":    true,
		"RESET.EXE --hard":    true,
		"st.exe":              true,
		"status.exe":          false,
		"log.exe --oneline":   false,
		"stash.exe list":      false,
		"clean.exe -n":        false,
		"checkout.exe -b new": false,
	}
	for args, want := range shim {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("shim: git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	hook := map[string]bool{}
	for args, want := range shim {
		hook["git "+args] = want
	}
	hook["cd x && git clean.exe -fdx"] = true
	hook["git.exe reset.exe --hard"] = true
	expect(t, c, hook, POSIX)
	expect(t, c, hook, PowerShell)
}
