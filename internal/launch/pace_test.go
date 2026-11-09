package launch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestTailWakesWhenTheWatchedLauncherEnds: a tail of a quiet worker watches its launcher and returns right after
// the launcher exits, well inside the polling bound (the worker ends about 1 s into the tail's longest wait, so
// polling alone would see it about 800 ms late); the tails share one watch, and it ends with the last of them.
func TestTailWakesWhenTheWatchedLauncherEnds(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	h, err := Start(context.Background(), testOptions(dir, "print:one", "sleep:1700", "exit:0"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := h.Tree(); !ok {
		t.Fatal("a started launch has no tree")
	}
	done, release := h.watchLauncher()
	if done == nil {
		t.Skip("the launcher cannot be watched here")
	}
	_, again := h.watchLauncher()
	h.watchMu.Lock()
	refs := h.watch.refs
	h.watchMu.Unlock()
	if refs != 2 {
		t.Fatalf("two watches hold %d shares, want one shared wait", refs)
	}
	release()
	again()
	h.watchMu.Lock()
	left := h.watch
	h.watchMu.Unlock()
	if left != nil {
		t.Fatal("the watch outlives its last share")
	}
	got := collect(t, h, 0)
	returned := time.Now()
	if fmt.Sprint(texts(got)) != "[one]" {
		t.Fatalf("lines %v", texts(got))
	}
	ex := wait(t, h)
	if late := returned.Sub(ex.EndedAt); late > exitCheckIdle {
		t.Fatalf("Tail returned %v after the launcher wrote its exit record, want it woken by the watch", late)
	}
}

// Goroutine stacks that tell the waits on a launcher apart.
const (
	reaperFrame = "created by github.com/NovaSeele/Seelegent/internal/launch.Start"
	watchFrame  = "internal/platform.watchProcess"
)

// goroutinesWith counts the goroutines whose stack holds frame.
func goroutinesWith(frame string) int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, frame) {
			count++
		}
	}
	return count
}

// sameChan reports whether a and b are the same channel.
func sameChan(a, b <-chan struct{}) bool { return a == b }

// settledWatches waits up to a few seconds for the watches of earlier tests to end and returns how many are left.
func settledWatches() int {
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := goroutinesWith(watchFrame)
		if n == 0 || time.Now().After(deadline) {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestStartedLaunchWaitsOnce: a launch this daemon started is waited for once, by the reaper (cmd.Wait): its tails
// share the reaper's channel and start no platform.WatchProcess, so a running launcher blocks one goroutine (and
// OS thread) of the daemon, not two. A handle attached to the same launch, as after a daemon restart, has no child
// to reap and watches the launcher instead.
func TestStartedLaunchWaitsOnce(t *testing.T) {
	watches := settledWatches()
	reapers := goroutinesWith(reaperFrame)
	dir := filepath.Join(t.TempDir(), "a1")
	h, err := Start(context.Background(), testOptions(dir, "print:one", "sleep:2000", "exit:0"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { // a failed check leaves no launcher holding the temp dir
		_ = h.Cancel()
		select {
		case <-h.reaped:
		case <-time.After(10 * time.Second):
		}
	})
	if n := goroutinesWith(reaperFrame) - reapers; n > 1 {
		t.Fatalf("Start left %d goroutines waiting on its launcher, want one", n)
	}
	type result struct {
		lines []string
		err   error
	}
	tailed := make(chan result, 1)
	go func() {
		var r result
		r.err = h.Tail(context.Background(), 0, func(b []byte, _ int64) error {
			r.lines = append(r.lines, string(b))
			return nil
		})
		tailed <- r
	}()
	deadline := time.Now().Add(5 * time.Second)
	var done <-chan struct{}
	for done == nil && time.Now().Before(deadline) {
		h.watchMu.Lock()
		if h.watch != nil {
			done = h.watch.done
		}
		h.watchMu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	if done == nil {
		t.Fatal("the tail does not watch the launcher")
	}
	if !sameChan(done, h.reaped) {
		t.Fatal("the tail watches the launcher with a wait of its own, want the reaper's")
	}
	if n := goroutinesWith(watchFrame); n != watches {
		t.Fatalf("%d process watches run during the tail, want %d: a started launch must not be watched twice", n,
			watches)
	}

	attached, err := Attach(dir)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if again, release := attached.watchLauncher(); again != nil {
		if again == done {
			t.Fatal("an attached handle shares the reaper of another handle")
		}
		if n := goroutinesWith(watchFrame); n != watches+1 {
			t.Fatalf("%d process watches with an attached tail, want %d", n, watches+1)
		}
		release()
	}

	if r := <-tailed; r.err != nil || fmt.Sprint(r.lines) != "[one]" {
		t.Fatalf("Tail: %v %v", r.lines, r.err)
	}
	select {
	case <-h.reaped:
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher was not reaped after its launch ended")
	}
	if h.reapErr != nil {
		t.Fatalf("reaping the launcher: %v", h.reapErr)
	}
}
