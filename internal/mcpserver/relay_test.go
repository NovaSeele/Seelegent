package mcpserver

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// TestRelaySharesTools: the relayed sessions share one tool list per tier models; it is built again only when the
// tiers change.
func TestRelaySharesTools(t *testing.T) {
	tiers := config.Defaults().Backends.Agy.Tiers
	r := NewRelay(RelayOptions{Port: 47831, Tiers: func() config.TierModels { return tiers }})
	a, err := r.tools()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.tools()
	if a != b || len(a.defs) != 17 {
		t.Fatalf("tool sets %p %p (%d tools)", a, b, len(a.defs))
	}
	tiers.Fast = "other-model"
	c, _ := r.tools()
	if c == a {
		t.Error("changed tiers kept the old tool list")
	}
}
