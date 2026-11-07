package config

import (
	"errors"
	"reflect"
	"testing"
)

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a *ValidationError", err)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err does not match ErrInvalid")
	}
	out := map[string]string{}
	for _, f := range ve.Fields {
		out[f.Field] = f.Message
	}
	return out
}

func TestMergePatchChangesAndResets(t *testing.T) {
	s := Defaults()
	s.StallMinutes = 42
	s.Backends.Agy.Tiers.Fast = "gemini-3.8-custom"
	got, err := MergePatch(s, []byte(`{"stallMinutes": null, "minRemaining": 0.2,
		"backends": {"agy": {"tiers": {"fast": null, "max": "gemini-3.8-max-x"}}, "codex": {"sandbox": "read-only"}},
		"linkIntoProfiles": [".gitconfig"]}`))
	if err != nil {
		t.Fatal(err)
	}
	def := Defaults()
	if got.StallMinutes != def.StallMinutes {
		t.Errorf("stallMinutes = %d, want the default %d", got.StallMinutes, def.StallMinutes)
	}
	if got.MinRemaining != 0.2 {
		t.Errorf("minRemaining = %v", got.MinRemaining)
	}
	if got.Backends.Agy.Tiers.Fast != def.Backends.Agy.Tiers.Fast || got.Backends.Agy.Tiers.Max != "gemini-3.8-max-x" {
		t.Errorf("agy tiers = %+v", got.Backends.Agy.Tiers)
	}
	if got.Backends.Agy.Tiers.Standard != def.Backends.Agy.Tiers.Standard {
		t.Errorf("an untouched tier changed: %+v", got.Backends.Agy.Tiers)
	}
	if got.Backends.Codex.Sandbox != SandboxReadOnly || got.Backends.Codex.Enabled != def.Backends.Codex.Enabled {
		t.Errorf("codex = %+v", got.Backends.Codex)
	}
	if !reflect.DeepEqual(got.LinkIntoProfiles, []string{".gitconfig"}) {
		t.Errorf("linkIntoProfiles = %v", got.LinkIntoProfiles)
	}
	if s.StallMinutes != 42 {
		t.Error("the input settings were changed")
	}
}

func TestMergePatchEmptyAndNullObject(t *testing.T) {
	s := Defaults()
	s.Backends.Claude.AllowedModels = "x"
	got, err := MergePatch(s, nil)
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("empty patch: %v, %+v", err, got)
	}
	got, err = MergePatch(s, []byte(`{"backends": {"claude": null}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Backends.Claude != Defaults().Backends.Claude {
		t.Errorf("claude = %+v, want the defaults", got.Backends.Claude)
	}
}

func TestMergePatchRejectsAllOrNothing(t *testing.T) {
	s := Defaults()
	_, err := MergePatch(s, []byte(`{"stallMinutes": -1, "lanes": "maybe", "nope": 1, "backends": {"agy": {"x": 1}}}`))
	fields := fieldsOf(t, err)
	for _, f := range []string{"nope", "backends.agy.x"} {
		if fields[f] == "" {
			t.Errorf("no error for %s: %v", f, fields)
		}
	}
	// Unknown keys stop before value checks; a second patch shows the value errors together.
	_, err = MergePatch(s, []byte(`{"stallMinutes": -1, "lanes": "maybe"}`))
	fields = fieldsOf(t, err)
	if fields["stallMinutes"] == "" || fields["lanes"] == "" {
		t.Errorf("fields = %v", fields)
	}
}

func TestMergePatchTypeAndPort(t *testing.T) {
	s := Defaults()
	_, err := MergePatch(s, []byte(`{"gates": "yes"}`))
	if fields := fieldsOf(t, err); fields["gates"] == "" {
		t.Errorf("fields = %v", fields)
	}
	_, err = MergePatch(s, []byte(`{"port": 47999}`))
	if fields := fieldsOf(t, err); fields["port"] == "" {
		t.Errorf("fields = %v", fields)
	}
	if _, err := MergePatch(s, []byte(`{"port": 47821}`)); err != nil {
		t.Errorf("an unchanged port is fine: %v", err)
	}
	if _, err := MergePatch(s, []byte(`[1]`)); !errors.Is(err, ErrParse) {
		t.Errorf("array patch: %v", err)
	}
	if _, err := MergePatch(s, []byte(`{"Port": 1}`)); err == nil {
		t.Error("a key in the wrong case was accepted")
	}
}
