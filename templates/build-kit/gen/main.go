// Command esgen scaffolds Go code for github.com/terraskye/eventsourcing
// from an Event Modelers slice export.
//
// It reads the slices of one context, as the Event Modelers CLI writes them
// to .build-kit/.slices/<context>/<slice>/slice.json, and writes into a Go
// module:
//
//	events/<event>.go                one file per event, registered with RegisterEvent
//	slices/<slice>/command.go        STATE_CHANGE: command, state, evolve, decide, NewHandler
//	slices/<slice>/command_test.go   the slice's Given/When/Then specifications
//	slices/<slice>/http.go           POST route for the command
//	slices/<slice>/model.go          STATE_VIEW: read model (and Claim, for a todo list)
//	slices/<slice>/projection.go     projection.Definition with one OnEvent handler per event
//	slices/<slice>/query.go          query and query handler
//	slices/<slice>/projection_test.go  the slice's Given/Then specifications
//	automations/<name>/automation.go AUTOMATION: automation.Definition working off a todo list
//	app_gen.go                       registers every slice; regenerated on every run
//
// With -init it also writes go.mod, main.go and wiring.go (the SQLite
// backend) when they do not exist yet.
//
// Files under events/ and app_gen*.go are regenerated on every run. Slice
// and automation files are written once and then belong to you: esgen
// leaves an existing file alone unless -force is given.
//
// Run it from the project root as
//
//	go -C .build-kit/gen run . -slice additem
//
// where the defaults point -slices at .build-kit/.slices and -out at the
// project root.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

func main() {
	var (
		slicesPath = flag.String("slices", "../.slices", "the .slices directory, or a JSON export of the form {\"slices\": [...]}")
		context    = flag.String("context", "", "the context to build (default: the one current_context.json names)")
		out        = flag.String("out", "../..", "the root of the Go module to write into")
		module     = flag.String("module", "", "the module path (default: read from <out>/go.mod; required with -init when there is none)")
		only       = flag.String("slice", "", "comma-separated slice folders to write slice files for (default: all)")
		force      = flag.Bool("force", false, "overwrite slice files that already exist")
		initRoot   = flag.Bool("init", false, "also write go.mod, main.go, wiring.go and .gitignore when missing")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: esgen [flags]\n\nScaffolds terraskye/eventsourcing code from Event Modelers slices.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	g := &generator{out: *out, force: *force}
	if err := g.run(*slicesPath, *context, *module, *only, *initRoot); err != nil {
		fmt.Fprintln(os.Stderr, "esgen:", err)
		os.Exit(1)
	}
}

type generator struct {
	out   string
	force bool
	log   []string
}

func (g *generator) run(slicesPath, context, module, only string, initRoot bool) error {
	if module == "" {
		module = readModule(filepath.Join(g.out, "go.mod"))
	}
	if module == "" {
		return fmt.Errorf("no module path: pass -module, or run from a module with a go.mod")
	}

	var model []*Slice
	if _, err := os.Stat(slicesPath); err == nil || !initRoot {
		context, model, err = loadModel(slicesPath, context)
		if err != nil {
			return err
		}
	}
	if context == "" {
		context = "app"
	}
	c := newCatalog(context, module, model)

	if initRoot {
		if err := g.writeRoot(c); err != nil {
			return err
		}
	}

	selected := map[string]bool{}
	for _, s := range strings.Split(only, ",") {
		if s = strings.TrimSpace(s); s != "" {
			selected[packageName(s)] = true
		}
	}
	for _, s := range c.Slices {
		if len(selected) > 0 && !selected[s.Package] {
			continue
		}
		if err := g.writeSlice(c, s); err != nil {
			return err
		}
	}
	if err := g.writeEvents(c); err != nil {
		return err
	}
	if err := g.writeApp(c); err != nil {
		return err
	}
	for _, s := range c.Slices {
		notes := s.Notes
		for _, cmd := range s.Commands {
			notes = append(notes, cmd.Notes...)
		}
		for _, v := range s.Views {
			notes = append(notes, v.Notes...)
		}
		for _, n := range notes {
			g.log = append(g.log, fmt.Sprintf("note: %s: %s", s.Title, n))
		}
	}
	for _, l := range g.log {
		fmt.Println(l)
	}
	return nil
}

func readModule(gomod string) string {
	b, err := os.ReadFile(gomod)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// local maps the project's package names to import paths.
func (c *catalog) local() map[string]string {
	m := map[string]string{"events": c.Module + "/events"}
	for _, s := range c.Slices {
		m[s.Package] = c.Module + "/slices/" + s.Package
	}
	for _, a := range c.Autos {
		m[a.Package] = c.Module + "/automations/" + a.Package
	}
	return m
}

// write writes a file. A file that belongs to the user (owned) is only
// replaced with -force.
func (g *generator) write(rel string, content []byte, owned bool) error {
	path := filepath.Join(g.out, rel)
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, content) {
			return nil
		}
		if owned && !g.force {
			g.log = append(g.log, "kept    "+rel+" (exists; -force replaces it)")
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	g.log = append(g.log, "wrote   "+rel)
	return nil
}

func (g *generator) writeGo(rel, tmpl string, data any, extra template.FuncMap, c *catalog, owned bool) error {
	src, err := renderGo(tmpl, data, extra, c.local())
	if err != nil {
		return err
	}
	return g.write(rel, src, owned)
}

func (g *generator) writeRoot(c *catalog) error {
	data := map[string]any{"Module": c.Module, "Context": c.Context, "Name": filepath.Base(c.Module)}
	gomod, err := render("go.mod.tmpl", data, nil)
	if err != nil {
		return err
	}
	if err := g.write("go.mod", gomod, true); err != nil {
		return err
	}
	ignore, err := render("gitignore.tmpl", data, nil)
	if err != nil {
		return err
	}
	if err := g.write(".gitignore", ignore, true); err != nil {
		return err
	}
	if err := g.writeGo("main.go", "main.go.tmpl", data, nil, c, true); err != nil {
		return err
	}
	return g.writeGo("wiring.go", "wiring.go.tmpl", data, nil, c, true)
}

func (g *generator) writeEvents(c *catalog) error {
	if len(c.Events) == 0 {
		return nil
	}
	if err := g.writeGo("events/doc.go", "events_doc.go.tmpl", map[string]any{"Context": c.Context}, nil, c, false); err != nil {
		return err
	}
	for _, e := range c.Events {
		data := map[string]any{"Context": c.Context, "E": e}
		if err := g.writeGo("events/"+snake(e.GoName)+".go", "event.go.tmpl", data, nil, c, false); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) writeSlice(c *catalog, s *sliceInfo) error {
	dir := "slices/" + s.Package + "/"
	for _, cmd := range s.Commands {
		builder := template.FuncMap{"builder": func(e *typed) string {
			for _, b := range cmd.Builds {
				if b.Event.GoName == e.GoName {
					return b.Func
				}
			}
			return "missing" + e.GoName
		}}
		data := map[string]any{"Pkg": s.Package, "Slice": s, "Cmd": cmd, "HasGenerated": cmd.HasGenerated(), "Vars": cmd.testVars()}
		files := [][2]string{{"command.go", "command.go.tmpl"}, {"command_test.go", "command_test.go.tmpl"}}
		if strings.EqualFold(s.SliceType, "STATE_CHANGE") {
			files = append(files, [2]string{"http.go", "command_http.go.tmpl"})
		}
		for _, f := range files {
			if err := g.writeGo(dir+f[0], f[1], data, builder, c, true); err != nil {
				return err
			}
		}
	}
	for _, v := range s.Views {
		data := map[string]any{"Pkg": s.Package, "Slice": s, "View": v, "Vars": v.testVars()}
		for _, f := range [][2]string{
			{"model.go", "view_model.go.tmpl"},
			{"projection.go", "view_projection.go.tmpl"},
			{"query.go", "view_query.go.tmpl"},
			{"http.go", "view_http.go.tmpl"},
			{"projection_test.go", "view_test.go.tmpl"},
		} {
			if err := g.writeGo(dir+f[0], f[1], data, nil, c, true); err != nil {
				return err
			}
		}
	}
	for _, a := range s.Autos {
		item, want := a.sample()
		data := map[string]any{"Pkg": a.Package, "Slice": s, "A": a, "Item": item, "Want": want}
		adir := "automations/" + a.Package + "/"
		if err := g.writeGo(adir+"automation.go", "automation.go.tmpl", data, nil, c, true); err != nil {
			return err
		}
		if err := g.writeGo(adir+"automation_test.go", "automation_test.go.tmpl", data, nil, c, true); err != nil {
			return err
		}
	}
	return nil
}

// writeApp writes app_gen.go for the slices whose code exists.
func (g *generator) writeApp(c *catalog) error {
	exists := func(dir string) bool {
		_, err := os.Stat(filepath.Join(g.out, dir))
		return err == nil
	}
	app := *c
	app.Views, app.Commands, app.Autos = nil, nil, nil
	for _, v := range c.Views {
		if exists("slices/" + v.Slice.Package) {
			app.Views = append(app.Views, v)
		}
	}
	for _, cmd := range c.Commands {
		if exists("slices/" + cmd.Slice.Package) {
			app.Commands = append(app.Commands, cmd)
		}
	}
	for _, a := range c.Autos {
		if exists("automations/"+a.Package) && slicesContains(app.Views, a.Todo) && slicesContains(app.Commands, a.Command) {
			app.Autos = append(app.Autos, a)
		}
	}
	type post struct{ Route, Body string }
	var posts []post
	for _, cmd := range app.Commands {
		if strings.EqualFold(cmd.Slice.SliceType, "STATE_CHANGE") {
			posts = append(posts, post{cmd.Route, cmd.sampleJSON()})
		}
	}
	data := map[string]any{"C": &app, "Posts": posts}
	if err := g.writeGo("app_gen.go", "app_gen.go.tmpl", data, nil, c, false); err != nil {
		return err
	}
	return g.writeGo("app_gen_test.go", "app_gen_test.go.tmpl", data, nil, c, false)
}

func slicesContains[T comparable](s []T, v T) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// sampleJSON is a request body for the command: the example values of its
// first specification.
func (cmd *command) sampleJSON() string {
	m := map[string]any{}
	var when Steps
	for _, s := range cmd.Slice.Specifications {
		for _, st := range s.When {
			if sameName(st.Title, cmd.Title) && when == nil {
				when = s.When
			}
		}
	}
	for _, st := range when {
		for _, sf := range st.Fields {
			f := cmd.field(sf.Name)
			if f == nil {
				continue
			}
			ex := sf.ExampleString()
			if _, ok := literal(f.Type, ex); !ok {
				continue
			}
			switch f.Type {
			case "bool":
				m[f.JSON], _ = strconv.ParseBool(ex)
			case "int", "int64", "float64":
				m[f.JSON] = json.Number(ex)
			default:
				m[f.JSON] = ex
			}
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// sample returns a todo-list row with a value in every field the command
// takes from it, and the command Work should send for it.
func (a *auto) sample() (item, want []assign) {
	i := 0
	for _, w := range a.Work {
		if w.Expr == "" {
			continue
		}
		src := a.Todo.field(strings.TrimPrefix(w.Expr, "item."))
		if src == nil {
			continue
		}
		i++
		lit, ok := sampleLiteral(src.Type, i)
		if !ok {
			continue
		}
		item = append(item, assign{src.Name, lit})
		want = append(want, assign{w.Field, lit})
	}
	return item, want
}

func sampleLiteral(typ string, i int) (string, bool) {
	switch typ {
	case "string":
		return strconv.Quote(fmt.Sprintf("example %d", i)), true
	case "bool":
		return "true", true
	case "int", "int64":
		return strconv.Itoa(i), true
	case "float64":
		return strconv.Itoa(i) + ".5", true
	case "uuid.UUID":
		return fmt.Sprintf(`uuid.MustParse("00000000-0000-4000-8000-%012d")`, i), true
	case "time.Time":
		return fmt.Sprintf("time.Date(2026, 1, %d, 9, 30, 0, 0, time.UTC)", i), true
	}
	return "", false
}

// snake turns a Go name into a file name: "CartCreated" → "cart_created".
func snake(goName string) string {
	ws := words(goName)
	for i, w := range ws {
		ws[i] = strings.ToLower(w)
	}
	return strings.Join(ws, "_")
}
