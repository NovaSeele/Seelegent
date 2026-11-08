package task

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// TestDelegateRelativeSpecStaysInCwd: a relative spec.dir that leaves cwd once cleaned or resolved (.., a link or
// junction inside cwd that points out) is refused, as the old version, GET /spec and proof refuse it; the same folder
// by its absolute path is accepted (A-L9h).
func TestDelegateRelativeSpecStaysInCwd(t *testing.T) {
	h := newHarness(t)
	outside := filepath.Join(h.dir, "workspace", "specs", "002-y")
	for _, d := range []string{outside, filepath.Join(h.cwd, "specs", "001-x")} {
		if err := mkdir(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := platform.LinkInto(filepath.Dir(outside), filepath.Join(h.cwd, "linked")); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(h.cwd, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{rel, filepath.ToSlash(rel), "specs/../../workspace/specs/002-y", "linked/002-y",
		"linked"} {
		_, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd,
			Spec: &api.SpecRequest{Dir: dir}})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("spec.dir %q: %v", dir, err)
		}
	}
	for _, dir := range []string{"specs/001-x", "./specs/../specs/001-x", outside} {
		sum, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd,
			Spec: &api.SpecRequest{Dir: dir}})
		if err != nil {
			t.Fatalf("spec.dir %q: %v", dir, err)
		}
		if _, _, err := h.s.Cancel(t.Context(), sum.ID, api.CancelRequest{WaitSeconds: 10}); err != nil {
			t.Fatal(err)
		}
	}
}
