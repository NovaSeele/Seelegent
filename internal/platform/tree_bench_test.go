package platform

import (
	"os/exec"
	"runtime"
	"testing"
)

// trivialCommand is a command that exits at once.
func trivialCommand() *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd.exe", "/d", "/c", "exit 0") //nolint:noctx // a benchmark's own short command
	}
	return exec.Command("true") //nolint:noctx // a benchmark's own short command
}

// BenchmarkStartTree starts and reaps a trivial command as a process tree, the way every git, gh, quota, hook and
// gate command starts (gitx, github, quota, hook, gate), against a plain exec.Cmd start. The difference is the
// tree's own cost (Windows: a Job Object, and the suspended start, assignment and resume of the root).
func BenchmarkStartTree(b *testing.B) {
	b.Run("exec", func(b *testing.B) {
		for b.Loop() {
			if err := trivialCommand().Run(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("tree", func(b *testing.B) {
		for b.Loop() {
			tree, err := StartTree(trivialCommand())
			if err != nil {
				b.Fatal(err)
			}
			if err := tree.Wait(); err != nil {
				b.Fatal(err)
			}
			_ = tree.Close()
		}
	})
}
