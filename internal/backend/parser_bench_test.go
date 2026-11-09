package backend

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/logx"
)

// streamLines reads the stream lines of a recorded stream (directive lines left out).
func streamLines(b *testing.B, backend, file string) (lines [][]byte, size int64) {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "streams", backend, file))
	if err != nil {
		b.Fatal(err)
	}
	for _, l := range bytes.Split(data, []byte("\n")) {
		if len(l) == 0 || bytes.HasPrefix(l, []byte("#!")) {
			continue
		}
		lines = append(lines, l)
		size += int64(len(l)) + 1
	}
	return lines, size
}

// BenchmarkParseStream feeds each backend's recorded success stream to a fresh parser, as the engine does for
// every worker line (task/follow.go), with and without the pattern redactor the engine adds when the turn's
// secrets are not known for sure.
func BenchmarkParseStream(b *testing.B) {
	for _, name := range []string{"agy", "claude", "codex"} {
		for _, masked := range []bool{false, true} {
			label := name
			if masked {
				label += "/redactor"
			}
			b.Run(label, func(b *testing.B) {
				be, err := New(name, "/usr/bin/"+name)
				if err != nil {
					b.Fatal(err)
				}
				lines, size := streamLines(b, name, "success.jsonl")
				redact := logx.NewRedactor().String
				b.SetBytes(size)
				b.ReportAllocs()
				events := 0
				for b.Loop() {
					p := be.NewParser([]string{"sk-bench-secret-value"})
					if masked {
						p.MaskPatterns(redact)
					}
					for _, l := range lines {
						events += len(p.Line(l))
					}
				}
				b.ReportMetric(float64(len(lines)), "lines/op")
				b.ReportMetric(float64(events)/float64(b.N), "events/op")
			})
		}
	}
}
