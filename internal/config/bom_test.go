package config

import (
	"bytes"
	"os"
	"testing"
)

// A config.json that PowerShell 5.1 or Notepad saved starts with a UTF-8 byte order mark: it loads, and a save
// writes the file without one (M18 hunt H9).
func TestLoadFileWithBOM(t *testing.T) {
	path := writeFile(t, "\xef\xbb\xbf"+`{"port": 47890, "stallMinutes": 9}`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.Port != 47890 || c.Settings.StallMinutes != 9 {
		t.Fatalf("settings: %+v", c.Settings)
	}
	c.Settings.Port = 47891
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		t.Errorf("the saved file starts with a BOM: %q", data[:8])
	}
	if c, err = Load(path); err != nil || c.Settings.Port != 47891 {
		t.Fatalf("reload: %+v, %v", c, err)
	}
}
