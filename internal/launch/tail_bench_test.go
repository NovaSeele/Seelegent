package launch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// benchStream is n worker-like JSON lines of about 400 bytes.
func benchStream(n int) []byte {
	var b bytes.Buffer
	for i := range n {
		fmt.Fprintf(&b, `{"event":"step_update","step_update":{"step_index":%d,"step_type":"tool","state":"DONE",`+
			`"tool_name":"view_file","tool_info":{"parameters":{"AbsolutePath":"/work/demo/pkg/file%d.go"},`+
			`"output":"%s"}}}`+"\n", i, i, bytes.Repeat([]byte("x"), 260))
	}
	return b.Bytes()
}

// BenchmarkDeliverLines splits a 64 KiB read into lines, as the tailer does for every chunk of a stream file.
func BenchmarkDeliverLines(b *testing.B) {
	data := benchStream(1000)
	chunk := data[:64<<10]
	lines := bytes.Count(chunk, []byte("\n"))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for b.Loop() {
		n := 0
		if _, _, err := deliverLines(chunk, 0, func([]byte, int64) error { n++; return nil }); err != nil {
			b.Fatal(err)
		}
		if n != lines {
			b.Fatalf("%d lines, want %d", n, lines)
		}
	}
}

// BenchmarkTailerRead reads a whole stream file through the tailer (open, 64 KiB reads, line delivery).
func BenchmarkTailerRead(b *testing.B) {
	data := benchStream(10000)
	path := filepath.Join(b.TempDir(), "stream.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		n := 0
		t := tailer{path: path, fn: func([]byte, int64) error { n++; return nil }, buf: make([]byte, tailChunk)}
		if _, err := t.readAvailable(); err != nil {
			b.Fatal(err)
		}
		t.close()
		if n != 10000 {
			b.Fatalf("%d lines", n)
		}
	}
}
