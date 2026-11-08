// Command tsgen writes web/src/api/types.gen.ts from the Go DTOs of internal/api (and the types they use from
// internal/event and internal/config). It reads the source with go/parser, so it needs no dependency and no
// network. Run it through `go generate ./internal/api`; a test fails when the committed file is stale.
//
// Mapping rules:
//   - a struct becomes an interface; an embedded struct becomes `extends`; an empty struct is Record<string, never>;
//   - a string type with constants becomes a union of their values (typed constants, or untyped ones whose name
//     starts with the type name, such as Verdict*); enums of other packages are written inline;
//   - omitempty / omitzero make a field optional; a pointer without them is `T | null`;
//   - time.Time is string, json.RawMessage and any are unknown, map[string]T is Record<string, T>;
//   - a `ts:"..."` struct tag replaces the field's type; `ts:"-"` leaves the field out.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func main() {
	out := flag.String("out", "", "output file (default: <module>/web/src/api/types.gen.ts)")
	flag.Parse()
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsgen:", err)
		os.Exit(1)
	}
	src, err := Generate(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tsgen:", err)
		os.Exit(1)
	}
	path := *out
	if path == "" {
		path = filepath.Join(root, OutputPath)
	}
	if err := os.WriteFile(path, src, 0o644); err != nil { //nolint:gosec // a source file of the repository
		fmt.Fprintln(os.Stderr, "tsgen:", err)
		os.Exit(1)
	}
}

// OutputPath is the generated file, relative to the module root.
const OutputPath = "web/src/api/types.gen.ts"

// printWidth is prettier's printWidth of web/.prettierrc.json; the output is already formatted the way prettier
// would format it, so `prettier --check` passes.
const printWidth = 100

// moduleRoot finds the directory of go.mod from the working directory up.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found")
		}
		dir = parent
	}
}

// key names a Go type: the short package name (api, event, config) and the type name.
type key struct{ pkg, name string }

// renames gives TypeScript names to types whose Go name clashes with another type or a JavaScript global, or that
// the contract names differently.
var renames = map[key]string{
	{"api", "TaskEvent"}:           "TaskEventBase",
	{"api", "AccountProblemData"}:  "AccountFailureData",
	{"api", "RecoveryData"}:        "ReattachData",
	{"api", "EmptyData"}:           "CancelData",
	{"event", "Event"}:             "WorkerEvent",
	{"event", "Session"}:           "WorkerSession",
	{"event", "Tool"}:              "WorkerTool",
	{"event", "File"}:              "WorkerFile",
	{"event", "Report"}:            "WorkerReport",
	{"event", "Error"}:             "WorkerError",
	{"event", "Usage"}:             "Usage", // the same shape as api.Usage, which is emitted
	{"config", "Backends"}:         "SettingsBackends",
	{"config", "Backend"}:          "SettingsBackend",
	{"config", "CodexBackend"}:     "SettingsCodexBackend",
	{"api", "LiveWorker"}:          "",
	{"api", "ErrorBody"}:           "ApiErrorBody",
	{"api", "ErrorInfo"}:           "ApiError",
	{"api", "BackendCapabilities"}: "BackendCapabilities",
}

// skipped types are never emitted (an empty rename).
func skipped(k key) bool {
	n, ok := renames[k]
	return ok && n == ""
}

// overrides are hand-written bodies for shapes Go cannot express (a discriminated union).
var overrides = map[key]string{
	{"api", "RepoStatus"}: "export type RepoStatus =\n  | { repo: false }\n  | {\n      repo: true\n      root: string\n" +
		"      branch: string\n      detached: boolean\n      dirty: boolean\n      files: GitFile[]\n" +
		"      upstream?: string\n      ahead?: number\n      behind?: number\n      upstreamGone?: boolean\n    }\n",
}

// extras are appended at the end: unions built from generated types.
const extras = `/** The data of an attention event: a raised flag, or {state: 'cleared'}. */
export type AttentionEventData = Attention | AttentionData
`

// packages lists the packages read, in emission order, with their directory relative to the module root.
var packages = []struct{ name, dir string }{
	{"api", "internal/api"},
	{"event", "internal/event"},
	{"config", "internal/config"},
}

type typeDecl struct {
	spec *ast.TypeSpec
	doc  string
}

type constDecl struct {
	name, typ, value string
}

type pkgInfo struct {
	types  map[string]*typeDecl
	order  []string
	consts []constDecl
}

type generator struct {
	pkgs    map[string]*pkgInfo
	names   map[key]string // TS name of each type reached so far
	emitted map[key]bool
	queue   []key
	out     bytes.Buffer
}

// Generate returns the content of types.gen.ts for the module at root.
func Generate(root string) ([]byte, error) {
	g := &generator{pkgs: map[string]*pkgInfo{}, names: map[key]string{}, emitted: map[key]bool{}}
	for _, p := range packages {
		info, err := parsePackage(filepath.Join(root, filepath.FromSlash(p.dir)))
		if err != nil {
			return nil, err
		}
		g.pkgs[p.name] = info
	}
	api := g.pkgs["api"]
	// Aliases of other packages' types (type Settings = config.Settings) take the alias's name.
	for _, name := range api.order {
		d := api.types[name]
		if d.spec.Assign == 0 {
			continue
		}
		if sel, ok := d.spec.Type.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				g.names[key{x.Name, sel.Sel.Name}] = name
			}
		}
	}
	g.out.WriteString("// Code generated by internal/api/tsgen from internal/api; DO NOT EDIT.\n" +
		"// Regenerate with `go generate ./internal/api` (contracts/api-v1.md is the contract).\n")
	for _, name := range api.order {
		if !ast.IsExported(name) {
			continue
		}
		if err := g.emit(key{"api", name}); err != nil {
			return nil, err
		}
	}
	// The worker event is the data of a "worker" task event (TaskEvent.Data is any in Go).
	if _, err := g.tsName(key{"event", "Event"}); err != nil {
		return nil, err
	}
	for len(g.queue) > 0 {
		k := g.queue[0]
		g.queue = g.queue[1:]
		if err := g.emit(k); err != nil {
			return nil, err
		}
	}
	g.out.WriteString("\n" + extras)
	return g.out.Bytes(), nil
}

func parsePackage(dir string) (*pkgInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		files = append(files, n)
	}
	sort.Strings(files)
	info := &pkgInfo{types: map[string]*typeDecl{}}
	fset := token.NewFileSet()
	for _, n := range files {
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.TYPE:
				for _, s := range gd.Specs {
					ts := s.(*ast.TypeSpec)
					doc := ts.Doc
					if doc == nil && len(gd.Specs) == 1 {
						doc = gd.Doc
					}
					info.types[ts.Name.Name] = &typeDecl{spec: ts, doc: doc.Text()}
					info.order = append(info.order, ts.Name.Name)
				}
			case token.CONST:
				info.consts = append(info.consts, constsOf(gd)...)
			}
		}
	}
	return info, nil
}

// constsOf returns the string constants of a const block with their declared type ("" when untyped).
func constsOf(gd *ast.GenDecl) []constDecl {
	var out []constDecl
	for _, s := range gd.Specs {
		vs := s.(*ast.ValueSpec)
		typ := ""
		if id, ok := vs.Type.(*ast.Ident); ok {
			typ = id.Name
		}
		for i, n := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out = append(out, constDecl{name: n.Name, typ: typ, value: v})
		}
	}
	return out
}

// enumValues returns the values of a string type's constants: the typed ones, else untyped constants whose
// name starts with the type's name.
func (g *generator) enumValues(k key) []string {
	p := g.pkgs[k.pkg]
	var vals []string
	for _, c := range p.consts {
		if c.typ == k.name {
			vals = append(vals, c.value)
		}
	}
	if len(vals) > 0 {
		return vals
	}
	for _, c := range p.consts {
		if c.typ == "" && strings.HasPrefix(c.name, k.name) && len(c.name) > len(k.name) {
			vals = append(vals, c.value)
		}
	}
	return vals
}

func literalUnion(vals []string) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = "'" + strings.ReplaceAll(v, "'", `\'`) + "'"
	}
	return strings.Join(parts, " | ")
}

// tsName returns the TS name of a type and queues it for emission.
func (g *generator) tsName(k key) (string, error) {
	if n, ok := g.names[k]; ok {
		return n, nil
	}
	if k.pkg == "event" && k.name == "Usage" {
		return "Usage", nil
	}
	d, ok := g.pkgs[k.pkg].types[k.name]
	if !ok {
		return "", fmt.Errorf("unknown type %s.%s", k.pkg, k.name)
	}
	// Enums of other packages are written inline.
	if k.pkg != "api" {
		if id, ok := d.spec.Type.(*ast.Ident); ok && id.Name == "string" {
			if vals := g.enumValues(k); len(vals) > 0 {
				return literalUnion(vals), nil
			}
			return "string", nil
		}
	}
	name := k.name
	if r, ok := renames[k]; ok && r != "" {
		name = r
	}
	g.names[k] = name
	if !g.emitted[k] {
		g.queue = append(g.queue, k)
	}
	return name, nil
}

func (g *generator) emit(k key) error {
	if g.emitted[k] || skipped(k) {
		return nil
	}
	g.emitted[k] = true
	d := g.pkgs[k.pkg].types[k.name]
	name, err := g.tsName(k)
	if err != nil {
		return err
	}
	// Remove k from the queue tsName may have added it to; emitted guards a second emit anyway.
	spec := d.spec
	if spec.Assign != 0 {
		sel, ok := spec.Type.(*ast.SelectorExpr)
		if !ok {
			return nil // a Go-only alias
		}
		target := key{sel.X.(*ast.Ident).Name, sel.Sel.Name}
		g.emitted[target] = true
		td, ok := g.pkgs[target.pkg].types[target.name]
		if !ok {
			return fmt.Errorf("alias %s: unknown %s.%s", k.name, target.pkg, target.name)
		}
		return g.writeDecl(target, k.name, td.spec.Type, d.doc)
	}
	return g.writeDecl(k, name, spec.Type, d.doc)
}

func (g *generator) writeDecl(k key, name string, typ ast.Expr, doc string) error {
	g.out.WriteString("\n")
	writeDoc(&g.out, doc, "")
	if o, ok := overrides[k]; ok {
		g.out.WriteString(o)
		return nil
	}
	switch t := typ.(type) {
	case *ast.StructType:
		return g.writeStruct(k, name, t)
	case *ast.Ident:
		if t.Name == "string" {
			if vals := g.enumValues(k); len(vals) > 0 {
				g.out.WriteString(typeAlias(name, literalUnion(vals)))
				return nil
			}
		}
	}
	ts, err := g.tsType(k.pkg, typ)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	g.out.WriteString(typeAlias(name, ts))
	return nil
}

func (g *generator) writeStruct(k key, name string, st *ast.StructType) error {
	var extends []string
	var body bytes.Buffer
	for _, f := range st.Fields.List {
		tag := reflect.StructTag("")
		if f.Tag != nil {
			raw, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				return err
			}
			tag = reflect.StructTag(raw)
		}
		jsonName, opts, _ := strings.Cut(tag.Get("json"), ",")
		tsTag := tag.Get("ts")
		if jsonName == "-" || tsTag == "-" {
			continue
		}
		if len(f.Names) == 0 {
			base, err := g.tsType(k.pkg, f.Type)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			extends = append(extends, base)
			continue
		}
		omit := strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero")
		_, pointer := f.Type.(*ast.StarExpr)
		for _, n := range f.Names {
			if !ast.IsExported(n.Name) {
				continue
			}
			field := jsonName
			if field == "" {
				field = n.Name
			}
			ts := tsTag
			if ts == "" {
				var err error
				if ts, err = g.tsType(k.pkg, f.Type); err != nil {
					return fmt.Errorf("%s.%s: %w", name, n.Name, err)
				}
			}
			if pointer && !omit {
				ts += " | null"
			}
			sep := ": "
			if omit {
				sep = "?: "
			}
			writeDoc(&body, f.Doc.Text(), "  ")
			body.WriteString(property(field+sep, ts))
		}
	}
	if body.Len() == 0 && len(extends) == 0 {
		g.out.WriteString(typeAlias(name, "Record<string, never>"))
		return nil
	}
	head := "export interface " + name
	if len(extends) > 0 {
		head += " extends " + strings.Join(extends, ", ")
	}
	g.out.WriteString(head + " {\n")
	g.out.Write(body.Bytes())
	g.out.WriteString("}\n")
	return nil
}

func (g *generator) tsType(pkg string, e ast.Expr) (string, error) {
	switch t := e.(type) {
	case *ast.Ident:
		switch t.Name {
		case "string":
			return "string", nil
		case "bool":
			return "boolean", nil
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64",
			"float32", "float64":
			return "number", nil
		case "any":
			return "unknown", nil
		}
		return g.tsName(key{pkg, t.Name})
	case *ast.SelectorExpr:
		x, _ := t.X.(*ast.Ident)
		if x == nil {
			return "", fmt.Errorf("unsupported selector")
		}
		switch x.Name + "." + t.Sel.Name {
		case "time.Time":
			return "string", nil
		case "json.RawMessage":
			return "unknown", nil
		}
		if _, ok := g.pkgs[x.Name]; !ok {
			return "", fmt.Errorf("unsupported type %s.%s", x.Name, t.Sel.Name)
		}
		return g.tsName(key{x.Name, t.Sel.Name})
	case *ast.StarExpr:
		return g.tsType(pkg, t.X)
	case *ast.ArrayType:
		el, err := g.tsType(pkg, t.Elt)
		if err != nil {
			return "", err
		}
		if strings.Contains(el, " | ") {
			el = "(" + el + ")"
		}
		return el + "[]", nil
	case *ast.MapType:
		kt, err := g.tsType(pkg, t.Key)
		if err != nil {
			return "", err
		}
		vt, err := g.tsType(pkg, t.Value)
		if err != nil {
			return "", err
		}
		return "Record<" + kt + ", " + vt + ">", nil
	case *ast.InterfaceType:
		return "unknown", nil
	}
	return "", fmt.Errorf("unsupported type expression %T", e)
}

// writeDoc writes a Go doc comment as a JSDoc block.
func writeDoc(b *bytes.Buffer, doc, indent string) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return
	}
	doc = strings.ReplaceAll(doc, "*/", "* /")
	lines := strings.Split(doc, "\n")
	if len(lines) == 1 {
		b.WriteString(indent + "/** " + lines[0] + " */\n")
		return
	}
	b.WriteString(indent + "/**\n")
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			b.WriteString(indent + " *\n")
			continue
		}
		b.WriteString(indent + " * " + l + "\n")
	}
	b.WriteString(indent + " */\n")
}

// unionParts splits a type at its top-level " | " (outside brackets, braces, parentheses and quotes).
func unionParts(t string) []string {
	var parts []string
	depth, start := 0, 0
	inQuote := false
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case inQuote:
			switch c {
			case '\\':
				i++
			case '\'':
				inQuote = false
			}
		case c == '\'':
			inQuote = true
		case c == '(' || c == '[' || c == '{' || c == '<':
			depth++
		case c == ')' || c == ']' || c == '}' || c == '>':
			depth--
		case depth == 0 && strings.HasPrefix(t[i:], " | "):
			parts = append(parts, t[start:i])
			start = i + 3
			i += 2
		}
	}
	return append(parts, t[start:])
}

// property formats `name: type` of an interface the way prettier does: on one line when it fits, else the type on
// the next line (indented) when that fits, else one union member per line.
func property(head, typ string) string {
	const indent = "  "
	if len(indent)+len(head)+len(typ) <= printWidth {
		return indent + head + typ + "\n"
	}
	inner := indent + "  "
	parts := unionParts(typ)
	if len(parts) < 2 {
		return indent + head + typ + "\n"
	}
	if len(inner)+len(typ) <= printWidth {
		return indent + strings.TrimRight(head, " ") + "\n" + inner + typ + "\n"
	}
	var b strings.Builder
	b.WriteString(indent + strings.TrimRight(head, " ") + "\n")
	for _, p := range parts {
		b.WriteString(inner + "| " + p + "\n")
	}
	return b.String()
}

// typeAlias formats `export type name = typ` the way prettier does.
func typeAlias(name, typ string) string {
	head := "export type " + name + " = "
	if len(head)+len(typ) <= printWidth {
		return head + typ + "\n"
	}
	parts := unionParts(typ)
	if len(parts) < 2 {
		return head + typ + "\n"
	}
	if 2+len(typ) <= printWidth {
		return "export type " + name + " =\n  " + typ + "\n"
	}
	var b strings.Builder
	b.WriteString("export type " + name + " =\n")
	for _, p := range parts {
		b.WriteString("  | " + p + "\n")
	}
	return b.String()
}
