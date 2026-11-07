package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/migrate"
)

func TestMigrateCommand(t *testing.T) {
	home := fakeHome(t)
	data := filepath.Join(home, ".seelegent")
	writeFile(t, filepath.Join(data, "settings.json"), `{"stallMinutes": 11}`)
	writeFile(t, filepath.Join(data, "accounts.json"), `[{"id": "acc1", "backend": "agy", "label": "One"}]`)

	out, _, err := execute(t, "migrate", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	var rep migrate.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !rep.DryRun || rep.From != data || len(rep.Items) == 0 {
		t.Fatalf("report %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(data, "config.json")); err == nil {
		t.Fatal("dry run wrote config.json")
	}

	out, _, err = execute(t, "migrate")
	if err != nil || !strings.Contains(out, "imported") {
		t.Fatalf("migrate (%v):\n%s", err, out)
	}
	out, _, err = execute(t, "migrate")
	if err != nil || !strings.Contains(out, "0 imported") {
		t.Errorf("second migrate (%v):\n%s", err, out)
	}
	if _, _, err := execute(t, "migrate", "--from", filepath.Join(home, "nothing")); err == nil {
		t.Error("migrate from a missing folder succeeded")
	}
}
