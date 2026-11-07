package cli

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestWarnDevOrigins: --dev-origin leaves one Warn line naming every extra origin; without it nothing is logged.
func TestWarnDevOrigins(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	warnDevOrigins(log, nil)
	if buf.Len() != 0 {
		t.Fatalf("logged without dev origins: %s", buf.String())
	}
	warnDevOrigins(log, []string{"http://localhost:5173", "http://127.0.0.1:5174"})
	out := buf.String()
	if strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=WARN") ||
		!strings.Contains(out, "http://localhost:5173") || !strings.Contains(out, "http://127.0.0.1:5174") {
		t.Fatalf("warning = %q", out)
	}
}
