package mcpserver

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// toolsJSON is the tool list of the old MCP server (src/mcp.js TOOLS, as its tools/list printed it): names,
// descriptions and input schemas verbatim, because leads, the lead skill and host configurations rely on them.
// Two fixes of the old list are applied: followup requires only task_id (force makes message optional, old C-B8)
// and issues declares wake_on_blocked, which its handler reads (old C-B20). followup also takes member (milestone
// 14). The MODEL description names the agy tier models through {{tier.<name>}} placeholders filled from the
// settings at start.
//
//go:embed tools.json
var toolsJSON []byte

// toolDef is one tool of tools.json.
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// notYet is the note put in front of the description of a tool whose daemon side a later milestone builds.
const notYet = "Not available in this version of Seelegent yet. "

// loadTools returns the tool list with the tier models of tiers filled in. Tools named in unavailable get the
// notYet note.
func loadTools(tiers config.TierModels, unavailable map[string]bool) ([]toolDef, error) {
	raw := string(toolsJSON)
	for _, t := range config.Tiers() {
		m, _ := tiers.Model(t)
		if m == "" {
			m = "not set"
		}
		enc, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		raw = strings.ReplaceAll(raw, "{{tier."+t+"}}", strings.Trim(string(enc), `"`))
	}
	var defs []toolDef
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		return nil, fmt.Errorf("mcpserver: tools.json: %w", err)
	}
	for i := range defs {
		if unavailable[defs[i].Name] {
			defs[i].Description = notYet + defs[i].Description
		}
	}
	return defs, nil
}

// properties returns the names of the tool's arguments (its input schema's properties), sorted.
func (d toolDef) properties() ([]string, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
		return nil, fmt.Errorf("mcpserver: tools.json: %s: %w", d.Name, err)
	}
	names := make([]string, 0, len(schema.Properties))
	for n := range schema.Properties {
		names = append(names, n)
	}
	slices.Sort(names)
	return names, nil
}
