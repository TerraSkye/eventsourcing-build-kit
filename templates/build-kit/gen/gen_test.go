package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kitRoot is the root of the build kit repository.
const kitRoot = "../../.."

// TestExampleUpToDate regenerates examples/cart/app from its model and
// fails when a checked-in file differs, so the example always shows what
// the kit generates. go.mod and go.sum are left out: go mod tidy owns them.
func TestExampleUpToDate(t *testing.T) {
	model := filepath.Join(kitRoot, "examples/cart/model")
	want := filepath.Join(kitRoot, "examples/cart/app")
	if _, err := os.Stat(model); err != nil {
		t.Skip("examples/cart is not part of this checkout")
	}
	got := t.TempDir()
	g := &generator{out: got}
	if err := g.run(model, "", "example.com/cart", "", true); err != nil {
		t.Fatal(err)
	}
	compareTrees(t, got, want, "go.mod", "go.sum")
}

// TestRootScaffoldUpToDate checks templates/root, the project scaffold the
// Event Modelers CLI copies into a new project, against esgen -init on an
// empty model.
func TestRootScaffoldUpToDate(t *testing.T) {
	want := filepath.Join(kitRoot, "templates/root")
	if _, err := os.Stat(want); err != nil {
		t.Skip("templates/root is not part of this checkout")
	}
	got := t.TempDir()
	g := &generator{out: got}
	if err := g.run(filepath.Join(got, "no-slices-yet"), "", "app", "", true); err != nil {
		t.Fatal(err)
	}
	compareTrees(t, got, want, "go.mod", "go.sum", "README.md")
}

func compareTrees(t *testing.T, got, want string, skip ...string) {
	t.Helper()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	files := func(root string) map[string][]byte {
		m := map[string][]byte{}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if skipped[rel] || strings.HasSuffix(rel, ".db") {
				return nil
			}
			b, err := os.ReadFile(path)
			m[rel] = b
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	g, w := files(got), files(want)
	for rel, b := range g {
		wb, ok := w[rel]
		switch {
		case !ok:
			t.Errorf("%s is generated but not checked in; run scripts/regenerate.sh", rel)
		case !bytes.Equal(b, wb):
			t.Errorf("%s differs from what esgen generates; run scripts/regenerate.sh", rel)
		}
	}
	for rel := range w {
		if _, ok := g[rel]; !ok {
			t.Errorf("%s is checked in but no longer generated", rel)
		}
	}
}

// TestBoardExportVariants loads the shapes real board exports use besides
// the EM Spec ones: step types without the SPEC_ prefix, a rejection
// written as a single object, and step fields without a type.
func TestBoardExportVariants(t *testing.T) {
	const export = `{"slices": [{
	  "id": "s1", "title": "Add Language", "sliceType": "STATE_CHANGE", "context": "settings",
	  "commands": [{"id": "c1", "title": "Add Language", "type": "COMMAND",
	    "fields": [{"name": "aggregateId", "type": "String", "idAttribute": true}, {"name": "locale", "type": "String"}],
	    "dependencies": [{"id": "e1", "type": "OUTBOUND", "title": "Language Added", "elementType": "EVENT"}]}],
	  "events": [{"id": "e1", "title": "Language Added", "type": "EVENT",
	    "fields": [{"name": "aggregateId", "type": "String", "idAttribute": true}, {"name": "locale", "type": "String"}]}],
	  "readmodels": [], "screens": [], "processors": [],
	  "specifications": [
	    {"id": "sp1", "title": "adds a language", "given": [],
	     "when": [{"id": "w", "title": "Add Language", "type": "COMMAND", "fields": [{"name": "aggregateId", "example": "settings"}, {"name": "locale", "example": "nl_NL"}]}],
	     "then": [{"id": "t", "title": "Language Added", "type": "EVENT", "fields": [{"name": "locale", "example": "nl_NL"}]}]},
	    {"id": "sp2", "title": "duplicate language cannot be added",
	     "given": [{"id": "g", "title": "Language Added", "type": "EVENT", "fields": [{"name": "locale", "example": "nl_NL"}]}],
	     "when": [{"id": "w", "title": "Add Language", "type": "COMMAND"}],
	     "then": {"id": "t", "title": "duplicate language cannot be added", "type": "SPEC_ERROR", "fields": []}}
	  ]}]}`
	path := filepath.Join(t.TempDir(), "export.json")
	if err := os.WriteFile(path, []byte(export), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, model, err := loadModel(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "settings" || len(model) != 1 {
		t.Fatalf("loaded context %q with %d slices, want settings with 1", ctx, len(model))
	}
	c := newCatalog(ctx, "example.com/x", model)
	cmd := c.Commands[0]
	if len(cmd.Specs) != 2 {
		t.Fatalf("got %d specifications, want 2", len(cmd.Specs))
	}
	for _, cs := range cmd.Specs {
		if cs.Skip != "" {
			t.Errorf("%s: skipped: %s", cs.Title, cs.Skip)
		}
	}
	if len(cmd.Rules) != 1 || cmd.Rules[0].Err == nil || cmd.Rules[0].Err.Var != "errDuplicateLanguageCannotBeAdded" {
		t.Errorf("rules = %+v, want one rejection errDuplicateLanguageCannotBeAdded", cmd.Rules)
	}
	if got := cmd.AggregateExpr("c"); got != "c.AggregateID" {
		t.Errorf("AggregateExpr = %s, want c.AggregateID", got)
	}
}

// TestConflictingSpecsAreSkipped checks that two specifications expecting
// different outcomes after the same events are left to a human.
func TestConflictingSpecsAreSkipped(t *testing.T) {
	step := func(kind, title string) Step { return Step{Title: title, Type: kind} }
	s := &Slice{
		Title: "Close", SliceType: "STATE_CHANGE", Folder: "close",
		Commands: []Element{{Title: "Close", Fields: []Field{{Name: "id", Type: "UUID", IDAttribute: true}},
			Dependencies: []Dependency{{Type: "OUTBOUND", Title: "Closed", ElementType: "EVENT"}}}},
		Events: []Element{{Title: "Closed", Fields: []Field{{Name: "id", Type: "UUID", IDAttribute: true}}}},
		Specifications: []Spec{
			{Title: "closes", Given: Steps{step("EVENT", "Closed")}, When: Steps{step("COMMAND", "Close")}, Then: Steps{step("EVENT", "Closed")}},
			{Title: "rejects", Given: Steps{step("EVENT", "Closed")}, When: Steps{step("COMMAND", "Close")}, Then: Steps{step("SPEC_ERROR", "already closed")}},
		},
	}
	c := newCatalog("x", "example.com/x", []*Slice{s})
	for _, cs := range c.Commands[0].Specs {
		if cs.Skip == "" {
			t.Errorf("%s: not skipped, want it left to a human", cs.Title)
		}
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string]string{
		"Add Item":           "AddItem",
		"cartId":             "CartID",
		"cart items":         "CartItems",
		"slice: Enable User": "SliceEnableUser",
		"is_default":         "IsDefault",
		"3d model":           "X3dModel",
	} {
		if got := exportedName(in); got != want {
			t.Errorf("exportedName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"CartID": "cartID", "ID": "id", "type": "type_", "Item Added": "itemAdded"} {
		if got := unexportedName(in); got != want {
			t.Errorf("unexportedName(%q) = %q, want %q", in, got, want)
		}
	}
}
