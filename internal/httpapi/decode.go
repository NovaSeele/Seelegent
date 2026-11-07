package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// maxBody is the largest request body accepted (contract §1).
const maxBody = 1 << 20

// readBody reads the request body (at most 1 MiB); an empty body reads as {}.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, translated(api.CodeTooLarge, "too_large")
		}
		return nil, invalid("the request body could not be read")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		data = []byte("{}")
	}
	return data, nil
}

// decode reads a JSON body into v. Unknown fields, a wrong type and data after the object are 400 with the field.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	data, err := readBody(w, r)
	if err != nil {
		return err
	}
	return decodeBytes(data, v)
}

func decodeBytes(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var te *json.UnmarshalTypeError
		var se *json.SyntaxError
		switch {
		case errors.As(err, &te):
			field := te.Field
			return invalid("%s has the wrong type (got a JSON %s)", fieldName(field), te.Value).withField(field)
		case errors.As(err, &se):
			return invalid("the body is not valid JSON (at byte %d)", se.Offset)
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			field, _ := strconv.Unquote(strings.TrimPrefix(err.Error(), "json: unknown field "))
			return invalid("unknown field %q", field).withField(field)
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return invalid("the body is not valid JSON")
		}
		return invalid("the body is not valid JSON: %v", err)
	}
	if dec.More() {
		return invalid("unexpected data after the JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return invalid("unexpected data after the JSON object")
	}
	return nil
}

func fieldName(f string) string {
	if f == "" {
		return "the body"
	}
	return f
}

// queryInt reads an integer query parameter: def when absent, else it must be within 1..hi.
func queryInt(r *http.Request, name string, def, hi int) (int, error) {
	const lo = 1
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, invalid("%s must be a whole number from %d to %d", name, lo, hi).withField(name)
	}
	return n, nil
}

// queryBool reads a boolean query parameter (absent: false).
func queryBool(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, invalid("%s must be true or false", name).withField(name)
	}
	return b, nil
}

// absDir checks the cwd of a request: an absolute path to an existing folder (relative paths are resolved by the
// caller, never by the daemon: old C-B13). It returns the canonical path (fsutil).
func absDir(raw string) (string, error) {
	const field = "cwd"
	if raw == "" {
		return "", invalid("%s is required", field).withField(field)
	}
	if !filepath.IsAbs(raw) {
		return "", invalid("%s must be an absolute path (got %q)", field, raw).withField(field)
	}
	st, err := os.Stat(raw) //nolint:gosec // checking a folder the caller names is the purpose
	if err != nil || !st.IsDir() {
		return "", invalid("%s must be an existing folder (got %q)", field, raw).withField(field)
	}
	c, err := fsutil.Canonical(raw)
	if err != nil {
		return "", invalid("%s: %v", field, err).withField(field)
	}
	return c, nil
}

// oneOf checks an enum value; "" is allowed when optional.
func oneOf(field, got string, optional bool, allowed ...string) error {
	if got == "" && optional {
		return nil
	}
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return invalid("%s must be one of %s (got %q)", field, strings.Join(allowed, ", "), got).withField(field)
}
