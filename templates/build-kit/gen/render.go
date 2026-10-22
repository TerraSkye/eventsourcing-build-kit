package main

import (
	"bytes"
	"embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

var funcs = template.FuncMap{
	"quote":     strconv.Quote,
	"join":      strings.Join,
	"lowerName": unexportedName,
	"varName":   unexportedName,
	"zero":      zeroValue,
	"parse":     parseParam,
}

// render executes the named template with data.
func render(name string, data any, extra template.FuncMap) ([]byte, error) {
	t := template.New(name).Funcs(funcs)
	if extra != nil {
		t = t.Funcs(extra)
	}
	t, err := t.ParseFS(templateFS, "templates/"+name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// renderGo renders a Go file: the template, its imports worked out from
// what the code uses, and gofmt.
func renderGo(name string, data any, extra template.FuncMap, local map[string]string) ([]byte, error) {
	src, err := render(name, data, extra)
	if err != nil {
		return nil, err
	}
	if bytes.Contains(src, []byte("mustTime(")) && !bytes.Contains(src, []byte("func mustTime(")) {
		src = append(src, []byte(mustTimeFunc)...)
	}
	src, err = addImports(src, local)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	out, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("%s: gofmt: %w\n%s", name, err, src)
	}
	return out, nil
}

const mustTimeFunc = `
// mustTime parses an example time from the event model.
func mustTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	panic("bad time " + s)
}
`

// stdImports are the packages generated code may use, by name.
var stdImports = map[string]string{
	"bytes":         "bytes",
	"cmp":           "cmp",
	"context":       "context",
	"errors":        "errors",
	"fmt":           "fmt",
	"http":          "net/http",
	"httptest":      "net/http/httptest",
	"io":            "io",
	"json":          "encoding/json",
	"os":            "os",
	"signal":        "os/signal",
	"slices":        "slices",
	"slog":          "log/slog",
	"strconv":       "strconv",
	"strings":       "strings",
	"sync":          "sync",
	"syscall":       "syscall",
	"testing":       "testing",
	"time":          "time",
	"uuid":          "github.com/google/uuid",
	"eventsourcing": "github.com/terraskye/eventsourcing",
	"automation":    "github.com/terraskye/eventsourcing/automation",
	"logging":       "github.com/terraskye/eventsourcing/logging",
	"memory":        "github.com/terraskye/eventsourcing/memory",
	"projection":    "github.com/terraskye/eventsourcing/projection",
	"sqlite":        "github.com/terraskye/eventsourcing/sqlite",
}

// addImports adds an import declaration for every package src refers to
// but does not import, looked up in local (the project's own packages) and
// stdImports.
func addImports(src []byte, local map[string]string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse: %w\n%s", err, src)
	}
	imported := map[string]bool{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		imported[p] = true
	}
	need := map[string]bool{}
	// "//esgen:import <spec>" lines ask for an import the code does not
	// refer to, such as a blank import.
	var extra []string
	var kept [][]byte
	for _, line := range bytes.Split(src, []byte("\n")) {
		if spec, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("//esgen:import ")); ok {
			extra = append(extra, string(spec))
			continue
		}
		kept = append(kept, line)
	}
	src = bytes.Join(kept, []byte("\n"))
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok || id.Obj != nil {
			return true
		}
		path, ok := local[id.Name]
		if !ok {
			path, ok = stdImports[id.Name]
		}
		if ok && !imported[path] {
			need[path] = true
		}
		return true
	})
	if len(need) == 0 && len(extra) == 0 {
		return src, nil
	}
	// Three groups, as goimports -local would write them: the standard
	// library, other modules, this module.
	var groups [3][]string
	for p := range need {
		switch {
		case !strings.Contains(strings.SplitN(p, "/", 2)[0], "."):
			groups[0] = append(groups[0], strconv.Quote(p))
		case isLocal(p, local):
			groups[2] = append(groups[2], strconv.Quote(p))
		default:
			groups[1] = append(groups[1], strconv.Quote(p))
		}
	}
	groups[2] = append(groups[2], extra...)
	var b strings.Builder
	b.WriteString("\nimport (\n")
	first := true
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		sort.Strings(g)
		if !first {
			b.WriteString("\n")
		}
		first = false
		for _, spec := range g {
			b.WriteString("\t" + spec + "\n")
		}
	}
	b.WriteString(")\n")
	fset = token.NewFileSet()
	if f, err = parser.ParseFile(fset, "", src, parser.ParseComments); err != nil {
		return nil, err
	}

	// Insert after the package clause.
	pos := fset.Position(f.Name.End()).Offset
	if nl := bytes.IndexByte(src[pos:], '\n'); nl >= 0 {
		pos += nl + 1
	}
	out := append([]byte{}, src[:pos]...)
	out = append(out, b.String()...)
	return append(out, src[pos:]...), nil
}

func isLocal(path string, local map[string]string) bool {
	for _, p := range local {
		if p == path {
			return true
		}
	}
	return false
}

func zeroValue(typ string) string {
	switch typ {
	case "string":
		return `""`
	case "bool":
		return "false"
	case "int", "int64", "float64":
		return "0"
	case "time.Time":
		return "time.Time{}"
	case "uuid.UUID":
		return "uuid.Nil"
	default:
		return "*new(" + typ + ")"
	}
}

// parseParam renders the code that parses query parameter v into
// recv.<field>.
func parseParam(f goField, recv string) string {
	target := recv + "." + f.Name
	bad := `writeJSON(w, http.StatusBadRequest, map[string]string{"error": "` + f.JSON + `: " + err.Error()})
				return`
	parse := func(call string) string {
		return fmt.Sprintf("x, err := %s\n\t\t\tif err != nil {\n\t\t\t\t%s\n\t\t\t}\n\t\t\t%s = x", call, bad, target)
	}
	switch f.Type {
	case "string":
		return target + " = v"
	case "uuid.UUID":
		return parse("uuid.Parse(v)")
	case "int":
		return parse("strconv.Atoi(v)")
	case "int64":
		return parse("strconv.ParseInt(v, 10, 64)")
	case "float64":
		return parse("strconv.ParseFloat(v, 64)")
	case "bool":
		return parse("strconv.ParseBool(v)")
	case "time.Time":
		return parse("time.Parse(time.RFC3339, v)")
	}
	return "// " + f.JSON + " cannot be parsed from a query parameter"
}
