package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
)

type stubReader struct{}

func (stubReader) ReadQuota(context.Context, account.Profile, []string) (Quota, error) {
	return Quota{Entitled: true}, nil
}

func TestWithQuotaReader(t *testing.T) {
	for _, n := range Names() {
		b, err := New(n, "", WithQuotaReader(stubReader{}))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := b.QuotaReader().(stubReader); !ok {
			t.Errorf("%s: QuotaReader = %T", n, b.QuotaReader())
		}
	}
}

func TestFitModel(t *testing.T) {
	catalog := []string{"gpt-6-luna", "gpt-6.2-terra", "gpt-6.1-terra", "gpt-6-sol"}
	noSol := func(m string) bool { return !strings.Contains(m, "sol") }
	tests := []struct {
		name      string
		backend   string
		requested string
		available []string
		allowed   func(string) bool
		want      string
		ok        bool
	}{
		{"not codex", "agy", "gemini-x", []string{"other"}, nil, "gemini-x", true},
		{"unknown catalog", "codex", "gpt-6-astra", nil, nil, "gpt-6-astra", true},
		{"available", "codex", "gpt-6-sol", catalog, nil, "gpt-6-sol", true},
		{"closest not above", "codex", "gpt-6-astra", catalog, nil, "gpt-6-sol", true},
		{"newer version on a tie", "codex", "gpt-7-terra", catalog, nil, "gpt-6.2-terra", true},
		{"allowed filter", "codex", "gpt-6-astra", catalog, noSol, "gpt-6.2-terra", true},
		{"only higher models", "codex", "gpt-6-luna", []string{"gpt-6-astra", "gpt-6-sol"}, nil, "gpt-6-sol", true},
		{"unknown family ranks middle", "codex", "gpt-mystery", []string{"gpt-6-sol", "gpt-6-terra"}, nil, "gpt-6-terra", true},
		{"nothing allowed", "codex", "gpt-6-sol", catalog, func(string) bool { return false }, "", false},
		{"empty catalog", "codex", "gpt-6-sol", []string{}, nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FitModel(tt.backend, tt.requested, tt.available, tt.allowed)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("FitModel = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
