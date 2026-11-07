package setup

import (
	"encoding/json"
	"strings"
	"testing"
)

// entryV is the value the splice tests set for the seelegent server.
const entryV = `{"command":"/x/seelegent","args":["mcp"]}`

var seelegentPath = jpath{"mcpServers", ServerName}

func setEntry(d *jsonDoc) error { return d.set(seelegentPath, json.RawMessage(entryV)) }
func delEntry(d *jsonDoc) error { return d.del(seelegentPath) }

// lines joins lines with \n (the files of the tests end with a newline unless they say otherwise).
func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

// Every case lists the whole file before and after: bytes outside the spliced member must not move.
func TestJSONSplice(t *testing.T) {
	// A one-line server right before and right after seelegent, with escapes next to the spliced range.
	around := lines(
		`{`,
		`  "mcpServers": {`,
		`    "a": {"command": "a", "args": ["ends with \\"]},`,
		`    "seelegent": {"command": "old", "args": ["mcp"]},`,
		`    "b": {"command": "b", "env": {"Q": "say \"hi\", }"}}`,
		`  },`,
		`  "theme": "dark"`,
		`}`)
	tests := []struct {
		name, in string
		edit     func(*jsonDoc) error
		want     string
	}{
		{name: "update between one-line servers", in: around, edit: setEntry, want: lines(
			`{`,
			`  "mcpServers": {`,
			`    "a": {"command": "a", "args": ["ends with \\"]},`,
			`    "seelegent": {`,
			`      "command": "/x/seelegent",`,
			`      "args": [`,
			`        "mcp"`,
			`      ]`,
			`    },`,
			`    "b": {"command": "b", "env": {"Q": "say \"hi\", }"}}`,
			`  },`,
			`  "theme": "dark"`,
			`}`)},
		{name: "remove the middle key", in: around, edit: delEntry, want: lines(
			`{`,
			`  "mcpServers": {`,
			`    "a": {"command": "a", "args": ["ends with \\"]},`,
			`    "b": {"command": "b", "env": {"Q": "say \"hi\", }"}}`,
			`  },`,
			`  "theme": "dark"`,
			`}`)},
		{name: "remove the first key", in: lines(
			`{"mcpServers": {`,
			`  "seelegent": {"command": "old", "args": ["mcp"]},`,
			`  "b": {"command": "b"}`,
			`}}`), edit: delEntry, want: lines(
			`{"mcpServers": {`,
			`  "b": {"command": "b"}`,
			`}}`)},
		{name: "remove the last key", in: lines(
			`{"mcpServers": {`,
			`  "a": {"command": "a"},`,
			`  "seelegent": {"command": "old", "args": ["mcp"]}`,
			`}}`), edit: delEntry, want: lines(
			`{"mcpServers": {`,
			`  "a": {"command": "a"}`,
			`}}`)},
		{name: "remove the only key", in: lines(
			`{`,
			`  "mcpServers": {`,
			`    "seelegent": {"command": "old", "args": ["mcp"]}`,
			`  }`,
			`}`), edit: delEntry, want: lines(
			`{`,
			`  "mcpServers": {}`,
			`}`)},
		{name: "remove every repeat of the key", in: `{"mcpServers": {"seelegent": 1, "a": 2, "seelegent": 3}}`,
			edit: delEntry, want: `{"mcpServers": {"a": 2}}`},
		{name: "add after a one-line server", in: lines(
			`{`,
			`  "mcpServers": {`,
			`    "a": {"command": "a", "args": ["\\\""]}`,
			`  }`,
			`}`), edit: setEntry, want: lines(
			`{`,
			`  "mcpServers": {`,
			`    "a": {"command": "a", "args": ["\\\""]},`,
			`    "seelegent": {`,
			`      "command": "/x/seelegent",`,
			`      "args": [`,
			`        "mcp"`,
			`      ]`,
			`    }`,
			`  }`,
			`}`)},
		{name: "add to an empty container", in: lines(
			`{`,
			`  "theme": "dark",`,
			`  "mcpServers": {}`,
			`}`), edit: setEntry, want: lines(
			`{`,
			`  "theme": "dark",`,
			`  "mcpServers": {`,
			`    "seelegent": {`,
			`      "command": "/x/seelegent",`,
			`      "args": [`,
			`        "mcp"`,
			`      ]`,
			`    }`,
			`  }`,
			`}`)},
		{name: "add with the container missing", in: lines(
			`{`,
			`  "theme": "dark"`,
			`}`), edit: setEntry, want: lines(
			`{`,
			`  "theme": "dark",`,
			`  "mcpServers": {`,
			`    "seelegent": {`,
			`      "command": "/x/seelegent",`,
			`      "args": [`,
			`        "mcp"`,
			`      ]`,
			`    }`,
			`  }`,
			`}`)},
		{name: "add with the container null", in: `{"mcpServers": null}`, edit: setEntry,
			want: `{"mcpServers": {"seelegent": {"command": "/x/seelegent", "args": ["mcp"]}}}`},
		{name: "add to a missing file", in: "", edit: setEntry, want: lines(
			`{`,
			`  "mcpServers": {`,
			`    "seelegent": {`,
			`      "command": "/x/seelegent",`,
			`      "args": [`,
			`        "mcp"`,
			`      ]`,
			`    }`,
			`  }`,
			`}`)},
		{name: "add to a one-line document", in: `{"theme": "dark"}`, edit: setEntry,
			want: `{"theme": "dark", "mcpServers": {"seelegent": {"command": "/x/seelegent", "args": ["mcp"]}}}`},
		{name: "add to a compact document", in: `{"a":1,"mcpServers":{"b":{"command":"b"}}}`, edit: setEntry,
			want: `{"a":1,"mcpServers":{"b":{"command":"b"},"seelegent":{"command":"/x/seelegent","args":["mcp"]}}}`},
		{name: "BOM, CRLF and tabs",
			in:   "\xef\xbb\xbf{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {\"command\": \"a\"}\r\n\t}\r\n}",
			edit: setEntry,
			want: "\xef\xbb\xbf{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {\"command\": \"a\"},\r\n" +
				"\t\t\"seelegent\": {\r\n\t\t\t\"command\": \"/x/seelegent\",\r\n\t\t\t\"args\": [\r\n" +
				"\t\t\t\t\"mcp\"\r\n\t\t\t]\r\n\t\t}\r\n\t}\r\n}"},
		{name: "remove with BOM, CRLF and tabs",
			in:   "\xef\xbb\xbf{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {\"command\": \"a\"},\r\n\t\t\"seelegent\": {}\r\n\t}\r\n}\r\n",
			edit: delEntry,
			want: "\xef\xbb\xbf{\r\n\t\"mcpServers\": {\r\n\t\t\"a\": {\"command\": \"a\"}\r\n\t}\r\n}\r\n"},
		{name: "append to a list of one-line groups", in: lines(
			`{`,
			`  "hooks": {`,
			`    "PreToolUse": [`,
			`      {"matcher": "Bash", "hooks": [{"type": "command", "command": "x \"y\""}]}`,
			`    ]`,
			`  }`,
			`}`),
			edit: func(d *jsonDoc) error { return d.appendTo(preToolUse, json.RawMessage(`{"matcher":"Agent"}`)) },
			want: lines(
				`{`,
				`  "hooks": {`,
				`    "PreToolUse": [`,
				`      {"matcher": "Bash", "hooks": [{"type": "command", "command": "x \"y\""}]},`,
				`      {`,
				`        "matcher": "Agent"`,
				`      }`,
				`    ]`,
				`  }`,
				`}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := loadJSONDoc([]byte(tt.in), tt.in != "")
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.edit(d); err != nil {
				t.Fatal(err)
			}
			if got := string(d.bytes()); got != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestJSONSpliceErrors(t *testing.T) {
	for _, in := range []string{`{"mcpServers": []}`, `{"mcpServers": "x"}`} {
		d, err := loadJSONDoc([]byte(in), true)
		if err != nil {
			t.Fatal(err)
		}
		if err := setEntry(d); err == nil {
			t.Errorf("%s: set worked", in)
		}
		if string(d.bytes()) != in {
			t.Errorf("%s changed to %s", in, d.bytes())
		}
	}
}

func TestOnlyEmpty(t *testing.T) {
	for in, want := range map[string]bool{
		`{}`:                                  true,
		`{"mcpServers": {}}`:                  true,
		`{"mcpServers": {"a": 1}}`:            false,
		`{"mcpServers": {}, "theme": "dark"}`: false,
		`{"other": {}}`:                       false,
	} {
		d, err := loadJSONDoc([]byte(in), true)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.onlyEmpty("mcpServers"); got != want {
			t.Errorf("onlyEmpty(%s) = %v", in, got)
		}
	}
}
