package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// fixedCaller classifies every connection as one caller.
type fixedCaller struct{ c Caller }

func (f fixedCaller) Classify(netip.AddrPort, netip.AddrPort, time.Time) Caller { return f.c }

// Options.Activity hears the API calls of people and leads (the quota idle policy), not the dashboard's files and
// not the workers.
func TestActivityCountsAPICallsOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		worker bool
		want   int
	}{
		{"api", apiPrefix + "/health", false, 1},
		{"dashboard file", "/index.html", false, 0},
		{"worker", apiPrefix + "/health", true, 0},
	} {
		n := 0
		s := &Server{o: Options{Port: 47831, Activity: func() { n++ }}, log: slog.New(slog.DiscardHandler),
			mux: http.NewServeMux(), classifier: fixedCaller{Caller{Worker: tc.worker}}}
		s.mux.HandleFunc(apiPrefix+"/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, http.NoBody)
		req.Host = "127.0.0.1:47831"
		s.serve(httptest.NewRecorder(), req)
		if n != tc.want {
			t.Errorf("%s: %d activity calls, want %d", tc.name, n, tc.want)
		}
	}
}
