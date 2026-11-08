package task

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// benchService is the part of the engine a follower writes through: the repository, the task locks and the bus.
func benchService(b *testing.B, r *Repo) *Service {
	b.Helper()
	root, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	return &Service{repo: r, bus: NewBus(), clk: clock.Real(), log: slog.New(slog.DiscardHandler),
		locks: map[string]*taskLock{}, root: root}
}

// benchStreamLine is line i of a worker stream like the load bench's: a tool call, its result, a text delta.
func benchStreamLine(i int) string {
	step := i/3 + 1
	path := fmt.Sprintf("/work/demo/pkg/file%d.go", step)
	switch i % 3 {
	case 0:
		return agyTool(step, "CALLING", "cat "+path)
	case 1:
		return agyTool(step, "DONE", "cat "+path+strings.Repeat(" ", i%7))
	}
	return agyDelta(step+1000, fmt.Sprintf("Reading %s to see how step %d fits. ", path, step))
}

// BenchmarkFollowerChunk feeds a follower read chunks of lines (as the tail delivers them) with its pump running,
// and reports the transactions per line: one per line before the group commit.
func BenchmarkFollowerChunk(b *testing.B) {
	for _, chunk := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("lines=%d", chunk), func(b *testing.B) {
			r := benchRepo(b)
			benchTask(b, r, "t1", benchEpoch)
			s := benchService(b, r)
			agy, err := backend.New(config.BackendAgy, "agy")
			if err != nil {
				b.Fatal(err)
			}
			f := s.newFollower(&run{id: "t1"}, agy.NewParser(nil), "t1", 1, 1, Offsets{})
			writes := 0
			inner := f.write
			f.write = func(evs []NewEvent, off Offsets) error { writes++; return inner(evs, off) }
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); f.pump(ctx) }()
			var next int64
			lines := 0
			b.ReportAllocs()
			for b.Loop() {
				for range chunk {
					l := benchStreamLine(lines)
					next += int64(len(l)) + 1
					if err := f.line(false, []byte(l), next); err != nil {
						b.Fatal(err)
					}
					lines++
				}
				// The tail looks for more and finds none: the writer catches up before the next chunk.
				if err := f.flush(); err != nil {
					b.Fatal(err)
				}
			}
			cancel()
			<-done
			if err := f.drain(); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(lines), "ns/line")
			b.ReportMetric(float64(writes)/float64(lines), "tx/line")
		})
	}
}
