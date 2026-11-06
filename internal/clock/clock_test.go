package clock

import (
	"testing"
	"time"
)

func TestFakeTimers(t *testing.T) {
	start := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	f := NewFake(start)
	a := f.NewTimer(time.Minute)
	b := f.NewTimer(2 * time.Minute)
	c := f.NewTimer(3 * time.Minute)
	if f.Pending() != 3 {
		t.Fatalf("pending = %d", f.Pending())
	}
	if !c.Stop() || c.Stop() {
		t.Fatal("Stop should succeed once")
	}
	f.Advance(90 * time.Second)
	select {
	case got := <-a.C():
		if !got.Equal(start.Add(90 * time.Second)) {
			t.Fatalf("fired at %v", got)
		}
	default:
		t.Fatal("timer a did not fire")
	}
	select {
	case <-b.C():
		t.Fatal("timer b fired early")
	default:
	}
	if a.Stop() {
		t.Fatal("Stop of a fired timer reported true")
	}
	f.Advance(time.Minute)
	<-b.C()
	if f.Pending() != 0 {
		t.Fatalf("pending = %d", f.Pending())
	}
	if !f.Now().Equal(start.Add(150 * time.Second)) {
		t.Fatalf("now = %v", f.Now())
	}
	z := f.NewTimer(0)
	<-z.C()
}

func TestFakeWaitTimers(t *testing.T) {
	f := NewFake(time.Unix(0, 0))
	go func() {
		time.Sleep(10 * time.Millisecond)
		f.NewTimer(time.Second)
	}()
	if !f.WaitTimers(1, 5*time.Second) {
		t.Fatal("WaitTimers did not see the timer")
	}
	if f.WaitTimers(2, 20*time.Millisecond) {
		t.Fatal("WaitTimers reported a timer that does not exist")
	}
}

func TestReal(t *testing.T) {
	c := Real()
	if time.Since(c.Now()) > time.Minute {
		t.Fatal("real clock is off")
	}
	tm := c.NewTimer(time.Millisecond)
	<-tm.C()
	if tm.Stop() {
		t.Fatal("Stop after fire")
	}
}
