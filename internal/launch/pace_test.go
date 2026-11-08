package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// TestBackoff: the interval doubles from min up to max and starts over after reset.
func TestBackoff(t *testing.T) {
	b := backoff{min: 100 * time.Millisecond, max: time.Second}
	var got []time.Duration
	for range 6 {
		got = append(got, b.next())
	}
	want := []time.Duration{100, 200, 400, 800, 1000, 1000}
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Fatalf("intervals %v, want %v ms", got, want)
		}
	}
	b.reset()
	if d := b.next(); d != 100*time.Millisecond {
		t.Fatalf("after reset %v, want 100ms", d)
	}
}

// TestTailPaceExitChecks: the exit record is looked for the first time at once, then at most every exitCheckBusy
// while every poll reads lines, and at most every exitCheckIdle once polls read nothing, while an idle tail backs
// off to maxPollInterval. Together they bound how late the end of a launch is seen: within exitCheckIdle plus one
// poll interval after the polls stop reading.
func TestTailPaceExitChecks(t *testing.T) {
	p := newTailPace()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	checks := 0
	// Lines keep coming: every poll reads, for 10 s.
	for end := now.Add(10 * time.Second); now.Before(end); {
		if p.checkDue(now, false) {
			checks++
		}
		p.poll.reset()
		now = now.Add(p.poll.next())
	}
	if want := int(10*time.Second/exitCheckBusy) + 1; checks > want {
		t.Fatalf("%d exit checks in 10 s of output, want at most %d", checks, want)
	}
	// Then nothing comes for a minute: the waits grow to maxPollInterval and every check is at most
	// exitCheckIdle+maxPollInterval after the one before, the first one included.
	last, wakeups := now, 0
	for end := now.Add(time.Minute); now.Before(end); wakeups++ {
		if p.checkDue(now, true) {
			if gap := now.Sub(last); gap > exitCheckIdle+maxPollInterval {
				t.Fatalf("exit checks %v apart", gap)
			}
			last = now
		}
		now = now.Add(p.poll.next())
	}
	if wakeups > 70 {
		t.Fatalf("%d wake-ups in an idle minute, want about one a second", wakeups)
	}
}

// TestTailSeesEndWithinBound: a tail that has backed off reads every line, also a last one without a line break
// written just before the exit record, and returns within the documented bound (exitCheckIdle plus
// maxPollInterval, with slack for a loaded machine) after the record appears. The launcher is played by the test,
// which holds the launch lock.
func TestTailSeesEndWithinBound(t *testing.T) {
	dir := t.TempDir()
	f := FilesIn(dir)
	if err := writeJSON(f.Spec, specFile{Spec: Spec{Argv: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	ready := Ready{LauncherPID: os.Getpid(), WorkerPID: os.Getpid(), StartedAt: time.Now().UTC()}
	if err := writeJSON(f.Ready, ready); err != nil {
		t.Fatal(err)
	}
	lock, err := fsutil.TryLock(f.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	stream, err := os.OpenFile(f.Stream, os.O_WRONLY|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	h, err := Attach(dir)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		lines []string
		err   error
		at    time.Time
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var lines []string
		err := h.Tail(ctx, 0, func(b []byte, _ int64) error {
			lines = append(lines, string(b))
			return nil
		})
		done <- result{lines, err, time.Now()}
	}()

	var want []string
	write := func(s string) {
		t.Helper()
		if _, err := stream.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 { // a burst, then a pause long enough for the tail to back off to its longest wait
		want = append(want, fmt.Sprintf("line %d", i))
		write(want[len(want)-1] + "\n")
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(3 * maxPollInterval)
	want = append(want, "after the pause", "last without break")
	write("after the pause\nlast without break")
	if err := writeJSON(f.Exit, Exit{Reason: ReasonExited, EndedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	ended := time.Now()

	r := <-done
	if r.err != nil {
		t.Fatalf("Tail: %v", r.err)
	}
	if fmt.Sprint(r.lines) != fmt.Sprint(want) {
		t.Fatalf("lines %q, want %q", r.lines, want)
	}
	if late := r.at.Sub(ended); late > exitCheckIdle+maxPollInterval+time.Second {
		t.Fatalf("Tail returned %v after the exit record", late)
	}
}

// TestExitedKeepsRecord: once a handle has read the exit record it keeps it, so the tails and the follower that
// share the handle do not open the file again.
func TestExitedKeepsRecord(t *testing.T) {
	dir := t.TempDir()
	f := FilesIn(dir)
	if err := writeJSON(f.Spec, specFile{Spec: Spec{Argv: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	h, err := Attach(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := h.Exited(); ok || err != nil {
		t.Fatalf("before the record: ended %v, err %v", ok, err)
	}
	if err := writeJSON(f.Exit, Exit{Reason: ReasonCanceled, Code: 1, EndedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if ex, ok, err := h.Exited(); !ok || err != nil || ex.Reason != ReasonCanceled {
		t.Fatalf("record: %+v %v %v", ex, ok, err)
	}
	if err := os.Remove(filepath.Join(dir, ExitFile)); err != nil {
		t.Fatal(err)
	}
	if ex, ok, err := h.Exited(); !ok || err != nil || ex.Reason != ReasonCanceled || ex.Code != 1 {
		t.Fatalf("kept record: %+v %v %v", ex, ok, err)
	}
}
