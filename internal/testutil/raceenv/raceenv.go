// Package raceenv keeps race-enabled test binaries that a test starts as child processes (launchers, fake CLIs,
// fake daemons) from lingering at their exit.
//
// A program built with -race sleeps atexit_sleep_ms (1000 ms by default) when it exits while other goroutines
// exist, so the race detector can report late races. A test binary that runs itself as a launcher or a fake worker
// then ends about a second after its work is done: every turn of a fake worker takes a second or two longer, and a
// test that expects a launcher's end to be seen right after it wrote its exit record sees it a second late. The
// children's races are not lost: a race found before the exit is still reported (and fails the child).
package raceenv

import (
	"os"
	"strings"
)

// sleepOption is the GORACE option that sets the sleep at exit.
const sleepOption = "atexit_sleep_ms"

// QuietExit makes the race-enabled child processes this test binary starts from now on exit at once: it adds
// atexit_sleep_ms=0 to GORACE unless GORACE already sets that option. It does nothing in a build without -race. Call
// it first thing in TestMain, before tests start children with os.Environ().
func QuietExit() {
	if !enabled {
		return
	}
	cur := os.Getenv("GORACE")
	if strings.Contains(cur, sleepOption) {
		return
	}
	_ = os.Setenv("GORACE", strings.TrimSpace(cur+" "+sleepOption+"=0"))
}
