package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedFileIsCurrent fails when web/src/api/types.gen.ts does not match internal/api.
func TestGeneratedFileIsCurrent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	want, err := Generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OutputPath)))
	if err != nil {
		t.Fatal(err)
	}
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale: run `go generate ./internal/api` and commit the result", OutputPath)
	}
}

func TestFormatting(t *testing.T) {
	long := "'running' | 'ok' | 'quota' | 'auth' | 'transient' | 'background_wait' | 'error' | 'cancelled'"
	if got := property("  ", "status: ", "string"); got != "  status: string\n" {
		t.Errorf("short property = %q", got)
	}
	got := property("  ", "status: ", long)
	if !strings.HasPrefix(got, "  status:\n    'running' |") {
		t.Errorf("property on next line = %q", got)
	}
	longer := long + " | 'something_else' | 'and_more'"
	got = property("  ", "status: ", longer)
	if !strings.HasPrefix(got, "  status:\n    | 'running'\n    | 'ok'\n") {
		t.Errorf("property broken per member = %q", got)
	}
	if parts := unionParts("{ a: 'x | y' } | (A | B)[] | null"); len(parts) != 3 {
		t.Errorf("unionParts = %q", parts)
	}
}
