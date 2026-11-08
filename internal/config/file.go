package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// ErrParse is matched (errors.Is) by every error about a config file that is not readable as settings: broken
// JSON, a value of the wrong type, or a top level that is not an object.
var ErrParse = errors.New("config file cannot be parsed")

// filePerm is the permission of config.json.
const filePerm = 0o600

// Config is a loaded config.json: the typed settings plus what is needed to write the file back without losing
// keys this version does not know (from the old daemon or a newer Seelegent).
type Config struct {
	// Settings are the typed settings. Change them, then call Save.
	Settings Settings
	// Found is true when the settings came from a file; false means the file did not exist and Settings are the
	// defaults.
	Found bool

	doc     *object // the document as read, nil when there was no file
	unknown []string
}

// Load reads the config file at path. A missing file gives the defaults (Found is false); nothing is written.
// Any other problem returns an error and leaves the file untouched, so a broken or half-edited file is never
// replaced by defaults: the error matches ErrParse for unreadable JSON and ErrInvalid for out-of-range values.
// Keys this version does not know are kept for Save and listed by Unknown.
func Load(path string) (*Config, error) {
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{Settings: Defaults()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}
	return c, nil
}

// Parse reads settings from the content of a config file. Missing keys take their defaults. The result is
// validated.
func Parse(data []byte) (*Config, error) {
	// PowerShell 5.1 (Set-Content -Encoding UTF8) and Notepad write a UTF-8 byte order mark: skip it. Save never
	// writes one (M18 hunt H9).
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("%w: the file is empty", ErrParse)
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, parseError(data, err)
	}
	if s.LinkIntoProfiles == nil { // "linkIntoProfiles": null; write [] to link nothing
		s.LinkIntoProfiles = Defaults().LinkIntoProfiles
	}
	if s.GithubEnterpriseHosts == nil {
		s.GithubEnterpriseHosts = []string{}
	}
	doc, err := parseObject(data)
	if err != nil {
		return nil, parseError(data, err)
	}
	c := &Config{Settings: s, Found: true, doc: doc}
	if _, err := c.merged(); err != nil {
		return nil, err
	}
	if err := c.Settings.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Unknown returns the JSON paths of keys in the file that this version does not know, for example
// "providers" or "backends.agy.extra". They are kept when the file is saved.
func (c *Config) Unknown() []string { return slices.Clone(c.unknown) }

// Marshal validates the settings and returns the file content: the known settings in a fixed order followed by the
// unknown keys as they were read, indented, with a final newline.
func (c *Config) Marshal() ([]byte, error) {
	if err := c.Settings.Validate(); err != nil {
		return nil, err
	}
	merged, err := c.merged()
	if err != nil {
		return nil, err
	}
	compact, err := merged.marshal()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// Save validates the settings and writes them to path atomically (see fsutil.WriteFileAtomic), keeping the unknown
// keys that were read. Invalid settings are not written.
func (c *Config) Save(path string) error {
	data, err := c.Marshal()
	if err != nil {
		return fmt.Errorf("save config %s: %w", path, err)
	}
	if err := fsutil.WriteFileAtomic(path, data, filePerm); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	c.Found = true
	return nil
}

// Update changes the config file at path under a cross-process lock (path + ".lock"), so a CLI command and the
// daemon cannot overwrite each other's changes: it loads the file, calls fn on the settings, validates and saves.
// Nothing is written if fn or validation fails. The directory of path must exist.
func Update(ctx context.Context, path string, fn func(*Settings) error) (*Config, error) {
	lock, err := fsutil.Lock(ctx, path+".lock")
	if err != nil {
		return nil, fmt.Errorf("update config: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	c, err := Load(path)
	if err != nil {
		return nil, err
	}
	if err := fn(&c.Settings); err != nil {
		return nil, fmt.Errorf("update config: %w", err)
	}
	if err := c.Save(path); err != nil {
		return nil, err
	}
	return c, nil
}

// merged returns the document to write: the known settings overlaid on the document that was read. It also
// refreshes the list of unknown keys.
func (c *Config) merged() (*object, error) {
	s := c.Settings
	if s.LinkIntoProfiles == nil {
		s.LinkIntoProfiles = []string{}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	known, err := parseObject(raw)
	if err != nil {
		return nil, err
	}
	if c.doc == nil {
		c.unknown = nil
		return known, nil
	}
	merged, unknown, err := overlay(known, c.doc, "")
	if err != nil {
		return nil, err
	}
	c.unknown = unknown
	return merged, nil
}

// parseError wraps a JSON decoding error in ErrParse with the line and column where it happened.
func parseError(data []byte, err error) error {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		line, col := position(data, syntax.Offset-1) // Offset counts the offending byte
		return fmt.Errorf("%w: line %d, column %d: %w", ErrParse, line, col, err)
	case errors.As(err, &typ) && typ.Type != nil:
		line, col := position(data, typ.Offset)
		return fmt.Errorf("%w: line %d, column %d: %s must be %s, not a JSON %s: %w",
			ErrParse, line, col, typ.Field, jsonKind(typ.Type.Kind().String()), typ.Value, err)
	default:
		return fmt.Errorf("%w: %w", ErrParse, err)
	}
}

// position converts a byte offset into a 1-based line and column.
func position(data []byte, offset int64) (line, col int) {
	offset = max(0, min(offset, int64(len(data))))
	before := data[:offset]
	line = bytes.Count(before, []byte{'\n'}) + 1
	col = len(before) - bytes.LastIndexByte(before, '\n')
	return line, col
}

// jsonKind names a Go kind the way a config file author thinks of it.
func jsonKind(kind string) string {
	switch kind {
	case "int", "int64", "float64":
		return "a number"
	case "bool":
		return "true or false"
	case "string":
		return "a string"
	case "slice":
		return "a list"
	case "struct":
		return "an object"
	}
	return kind
}
