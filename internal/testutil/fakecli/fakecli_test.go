package fakecli

import (
	"fmt"
	"sync"
	"testing"
)

// Runs that start together each take a different script, and every script goes to one run only (bugs F23).
func TestTakeClaimsEachScriptOnce(t *testing.T) {
	q := NewQueue(t)
	const scripts, runs = 20, 40
	for i := range scripts {
		q.Push(t, fmt.Sprintf("script %d\n", i))
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		start = make(chan struct{})
		got   = map[string]string{}
		empty int
	)
	for range runs {
		wg.Go(func() {
			<-start
			name, data, err := take(q.Dir())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				t.Errorf("take: %v", err)
			case name == "":
				empty++
			case got[name] != "":
				t.Errorf("%s taken twice", name)
			default:
				got[name] = string(data)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(got) != scripts || empty != runs-scripts {
		t.Errorf("took %d scripts, %d runs found none", len(got), empty)
	}
	if n := q.Left(t); n != 0 {
		t.Errorf("%d scripts left", n)
	}
}
