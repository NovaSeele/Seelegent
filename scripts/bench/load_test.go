//go:build bench

package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func jsonLine(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func initLine(conv string) string {
	return jsonLine(map[string]any{"event": "init", "conversation_id": conv,
		"init": map[string]any{"model": "gemini-3.8-flash-medium"}})
}

func resultLine(conv string) string {
	return jsonLine(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS",
		"response": "Done.", "conversation_id": conv,
		"usage": map[string]any{"input_tokens": 1000, "output_tokens": 100, "total_tokens": 1100}}})
}

// streamLine is line i of a worker's stream: a text delta, then a tool call and its result, with arguments that
// never repeat (so loop detection stays quiet).
func streamLine(task, i int) string {
	step := i/3 + 1
	path := fmt.Sprintf("/work/demo/pkg%d/file%d.go", task, step)
	switch i % 3 {
	case 0:
		return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
			"step_type": "agent_response", "state": "RUNNING",
			"text_delta": fmt.Sprintf("Reading %s to see how step %d fits. ", path, step)}})
	case 1:
		return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
			"step_type": "tool", "state": "CALLING", "tool_name": "view_file",
			"tool_info": map[string]any{"parameters": map[string]any{"AbsolutePath": path, "StartLine": 1,
				"EndLine": 80}}}})
	default:
		return jsonLine(map[string]any{"event": "step_update", "step_update": map[string]any{"step_index": step,
			"step_type": "tool", "state": "DONE", "tool_name": "view_file", "duration_seconds": 0.02,
			"tool_info": map[string]any{"parameters": map[string]any{"AbsolutePath": path, "StartLine": 1,
				"EndLine": 80}, "output": strings.Repeat(fmt.Sprintf("// line of %s\n", path), 6)}}})
	}
}

// shortScript is a seed task's stream: a few steps and a result.
func shortScript(n int) string {
	conv := fmt.Sprintf("seed-%04d", n)
	var b strings.Builder
	b.WriteString(initLine(conv) + "\n")
	for i := range 9 {
		b.WriteString(streamLine(1000+n, i) + "\n")
	}
	b.WriteString(resultLine(conv) + "\n")
	return b.String()
}

// longScript streams lines lines with a pause of every between them, then a result.
func longScript(task, lines int, every time.Duration) string {
	conv := fmt.Sprintf("load-%04d", task)
	var b strings.Builder
	b.WriteString(initLine(conv) + "\n")
	ms := max(int(every.Milliseconds()), 1)
	for i := range lines {
		fmt.Fprintf(&b, "#!sleep %d\n", ms)
		b.WriteString(streamLine(task, i) + "\n")
	}
	b.WriteString(resultLine(conv) + "\n")
	return b.String()
}

// latencies records request times of one endpoint.
type latencies struct {
	mu     sync.Mutex
	d      []time.Duration
	errors int
}

func (l *latencies) add(d time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.errors++
		return
	}
	l.d = append(l.d, d)
}

// latencyStats are percentiles in milliseconds.
type latencyStats struct {
	Count  int     `json:"count"`
	Errors int     `json:"errors"`
	P50    float64 `json:"p50ms"`
	P95    float64 `json:"p95ms"`
	P99    float64 `json:"p99ms"`
	Max    float64 `json:"maxms"`
}

func (l *latencies) stats() latencyStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := latencyStats{Count: len(l.d), Errors: l.errors}
	if len(l.d) == 0 {
		return s
	}
	d := append([]time.Duration(nil), l.d...)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	at := func(p float64) float64 {
		return float64(d[min(len(d)-1, int(p*float64(len(d))))].Microseconds()) / 1000
	}
	s.P50, s.P95, s.P99, s.Max = at(0.50), at(0.95), at(0.99), float64(d[len(d)-1].Microseconds())/1000
	return s
}

// loadGen drives the load scenario: the tasks, the SSE clients and the list/detail pollers.
type loadGen struct {
	e   *env
	lat map[string]*latencies
	ids []string

	sseFrames atomic.Int64
	sseBytes  atomic.Int64
	sseConns  atomic.Int64
	events    int64
}

// start queues one long script per task, delegates the tasks and starts the pollers.
func (g *loadGen) start(ctx context.Context, wg *sync.WaitGroup) error {
	o := g.e.o
	perTask := o.rate / float64(o.tasks)
	every := time.Duration(float64(time.Second) / perTask)
	// Long enough for the measured time, the CPU profile and a margin; a task that is still running at the end is
	// cancelled.
	span := o.duration + o.cpuProfile + 30*time.Second
	lines := int(span / every)
	for i := range o.tasks {
		if err := g.e.push(longScript(i+1, lines, every)); err != nil {
			return err
		}
	}
	for i := range o.tasks {
		id, err := g.e.delegate(ctx, fmt.Sprintf("load %d", i+1))
		if err != nil {
			return err
		}
		g.ids = append(g.ids, id)
	}
	wg.Go(func() { g.poll(ctx, "list", func(int) string { return "/tasks?limit=50" }) })
	wg.Go(func() {
		g.poll(ctx, "detail", func(n int) string { return "/tasks/" + g.ids[n%len(g.ids)] })
	})
	return nil
}

func (g *loadGen) poll(ctx context.Context, name string, path func(int) string) {
	t := time.NewTicker(g.e.o.poll)
	defer t.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		d, err := g.e.call(ctx, http.MethodGet, path(n), nil, nil)
		if ctx.Err() != nil {
			return
		}
		g.lat[name].add(d, err)
	}
}

// sse is one dashboard-like stream client: it reads every frame and reconnects until ctx ends.
func (g *loadGen) sse(ctx context.Context) {
	client := &http.Client{} // no timeout: the stream lives as long as the run
	for ctx.Err() == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.e.api+"/events", http.NoBody)
		if err != nil {
			return
		}
		resp, err := client.Do(req)
		if err == nil {
			g.sseConns.Add(1)
			rd := bufio.NewReaderSize(resp.Body, 64<<10)
			for {
				line, err := rd.ReadSlice('\n')
				g.sseBytes.Add(int64(len(line)))
				if len(line) > 6 && string(line[:6]) == "event:" {
					g.sseFrames.Add(1)
				}
				if err != nil && !errors.Is(err, bufio.ErrBufferFull) {
					break
				}
			}
			_ = resp.Body.Close()
		}
		_ = sleep(ctx, time.Second)
	}
}

// finish counts the events the load tasks stored and cancels the tasks that are still running.
func (g *loadGen) finish(ctx context.Context) {
	for _, id := range g.ids {
		var sum api.TaskSummary
		if _, err := g.e.call(ctx, http.MethodGet, "/tasks/"+id, nil, &sum); err != nil {
			continue
		}
		g.events += sum.LastEventSeq
		if !terminal(sum.Status) {
			if _, err := g.e.call(ctx, http.MethodPost, "/tasks/"+id+"/cancel", api.CancelRequest{WaitSeconds: 20},
				nil); err != nil {
				fmt.Fprintln(os.Stderr, "bench: cancel", id+":", err)
			}
		}
	}
	if len(g.ids) > 0 {
		if err := g.e.waitTerminal(ctx, g.ids, time.Minute); err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
		}
	}
}
