package httpapi

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// BenchmarkHubPublish encodes one task.event frame and queues it to n SSE clients that drain their queue, the
// cost of every worker event with the dashboard open (3 tabs) and with many clients (50).
func BenchmarkHubPublish(b *testing.B) {
	for _, n := range []int{3, 50} {
		b.Run(fmt.Sprintf("clients=%d", n), func(b *testing.B) {
			h, err := NewHub(HubOptions{Instance: "bench"})
			if err != nil {
				b.Fatal(err)
			}
			var wg sync.WaitGroup
			for range n {
				sub, ok := h.subscribe("")
				if !ok {
					b.Fatal("subscribe")
				}
				c := sub.client
				wg.Go(func() {
					for f := range c.ch {
						c.queued.Add(-int64(len(f.wire)))
					}
				})
			}
			ev := &api.TaskEvent{Seq: 1, TS: "2026-11-08T09:00:00.000Z", TaskID: "t1", Turn: 1, Attempt: 1,
				Type: api.EventWorker, Data: json.RawMessage(`{"kind":"tool","step":3,"text":"view_file /work/demo/a.go",` +
					`"tool":{"name":"view_file","output":"package a\n\nfunc A() {}\n"}}`)}
			b.ReportAllocs()
			for b.Loop() {
				if err := h.Publish(api.StreamTaskEvent, ev); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if got := h.Clients(); got != n {
				b.Logf("%d of %d clients fell behind and were dropped", n-got, n)
			}
			h.Close()
			wg.Wait()
		})
	}
}
