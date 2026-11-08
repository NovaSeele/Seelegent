package task

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// BenchmarkBusPublish publishes one worker event notice to 6 subscribers that drain their channel, the fan-out
// of every worker event (SSE feed, waits, the report followers).
func BenchmarkBusPublish(b *testing.B) {
	for _, subs := range []int{1, 6} {
		b.Run(fmt.Sprintf("subscribers=%d", subs), func(b *testing.B) {
			bus := NewBus()
			var wg sync.WaitGroup
			list := make([]*Subscription, subs)
			for i := range list {
				list[i] = bus.Subscribe(4096)
				wg.Go(func() {
					for range list[i].C() { // drain
					}
				})
			}
			ev := &api.TaskEvent{Seq: 1, TS: "2026-11-08T09:00:00.000Z", TaskID: "t1", Turn: 1, Attempt: 1,
				Type: api.EventWorker, Data: json.RawMessage(`{"kind":"tool","text":"view_file"}`)}
			b.ReportAllocs()
			for b.Loop() {
				bus.Publish(Notice{Kind: NoticeEvent, TaskID: "t1", Event: ev})
			}
			b.StopTimer()
			for _, s := range list {
				if s.Overflowed() {
					b.Log("a subscriber fell behind")
				}
				s.Close()
			}
			wg.Wait()
		})
	}
}
