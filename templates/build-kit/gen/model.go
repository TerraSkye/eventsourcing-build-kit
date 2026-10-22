package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The types below read the slice JSON that Event Modelers exports (the
// board's Code view, the CLI's fetch command, the slicedata endpoint). They
// follow EM Spec (github.com/dilgerma/event-modeling-spec) and ignore every
// field the generator does not use, so newer exports still load.

// Slice is one vertical slice of the event model.
type Slice struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	SliceType      string    `json:"sliceType"`
	Context        string    `json:"context"`
	Index          int       `json:"index"`
	Commands       []Element `json:"commands"`
	Events         []Element `json:"events"`
	Readmodels     []Element `json:"readmodels"`
	Screens        []Element `json:"screens"`
	Processors     []Element `json:"processors"`
	Specifications []Spec    `json:"specifications"`
	Aggregates     []string  `json:"aggregates"`

	// Folder is the slice's folder under the context directory; it names
	// the generated Go package.
	Folder string `json:"-"`
}

// Element is one sticky note: a command, event, read model, screen or
// automation.
type Element struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Type         string       `json:"type"`
	Fields       []Field      `json:"fields"`
	Dependencies []Dependency `json:"dependencies"`
	Description  string       `json:"description"`
	Aggregate    string       `json:"aggregate"`
	ListElement  bool         `json:"listElement"`
	APIEndpoint  string       `json:"apiEndpoint"`
}

// Field is one field of an element or of a specification step.
type Field struct {
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Example     any     `json:"example"`
	Subfields   []Field `json:"subfields"`
	Cardinality string  `json:"cardinality"`
	Mapping     string  `json:"mapping"`
	Optional    bool    `json:"optional"`
	IDAttribute bool    `json:"idAttribute"`
	Generated   bool    `json:"generated"`
}

// Dependency is a connector between two elements.
type Dependency struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // INBOUND or OUTBOUND
	Title       string `json:"title"`
	ElementType string `json:"elementType"`
}

// Spec is a Given/When/Then specification.
type Spec struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	LinkedID string `json:"linkedId"`
	Given    Steps  `json:"given"`
	When     Steps  `json:"when"`
	Then     Steps  `json:"then"`
}

// Steps is the Given, When or Then of a specification. Board exports write
// it as an array of steps, and sometimes a single rejection as one object;
// both load.
type Steps []Step

func (s *Steps) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '{' {
		var one Step
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = Steps{one}
		return nil
	}
	var many []Step
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

// Step is one row of a Given, When or Then.
type Step struct {
	ID              string           `json:"id"`
	Title           string           `json:"title"`
	Type            string           `json:"type"` // SPEC_EVENT, SPEC_COMMAND, SPEC_READMODEL, SPEC_ERROR
	Description     string           `json:"description"`
	LinkedID        string           `json:"linkedId"`
	Fields          []Field          `json:"fields"`
	Examples        []map[string]any `json:"examples"`
	ExpectEmptyList bool             `json:"expectEmptyList"`
}

// Step kinds, as Kind reports them.
const (
	stepEvent     = "EVENT"
	stepCommand   = "COMMAND"
	stepReadModel = "READMODEL"
	stepError     = "ERROR"
)

// Kind returns the step's kind without the SPEC_ prefix: EVENT, COMMAND,
// READMODEL or ERROR. EM Spec writes SPEC_EVENT and so on; board exports
// also write the bare element type. A step without a type is "".
func (s Step) Kind() string {
	return strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(s.Type)), "SPEC_")
}

// IsList reports whether the field holds a list.
func (f Field) IsList() bool { return strings.EqualFold(f.Cardinality, "List") }

// ExampleString returns the field's example value as text, or "" if it has
// none.
func (f Field) ExampleString() string { return exampleString(f.Example) }

func exampleString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		// An object example (for a Custom field) has no scalar value.
		return ""
	}
}

// dependencies returns the titles of e's dependencies of the given direction
// and element type, in model order.
func (e Element) dependencies(direction, elementType string) []string {
	var out []string
	for _, d := range e.Dependencies {
		if d.Type == direction && d.ElementType == elementType {
			out = append(out, d.Title)
		}
	}
	return out
}

// loadModel reads the slices of one context. path is either a .slices
// directory as the Event Modelers CLI writes it
// (<path>/<context>/<folder>/slice.json, with current_context.json naming the
// default context), or a single JSON export of the form {"slices": [...]}.
func loadModel(path, context string) (string, []*Slice, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() {
		return loadExport(path, context)
	}
	dir := path
	if context == "" {
		var cur struct {
			Name string `json:"name"`
		}
		switch err := readJSON(filepath.Join(path, "current_context.json"), &cur); {
		case err == nil:
			context = cur.Name
		case hasSlices(path):
			// The flat layout of older exports: <path>/<folder>/slice.json.
		default:
			return "", nil, fmt.Errorf("no -context given and %w", err)
		}
	}
	if context != "" {
		if dir, err = contextDir(path, context); err != nil {
			return "", nil, err
		}
	}

	// index.json, when present, orders the slices and names their folders.
	order := map[string]int{}
	var index struct {
		Slices []struct {
			Folder string `json:"folder"`
			Index  int    `json:"index"`
		} `json:"slices"`
	}
	if err := readJSON(filepath.Join(dir, "index.json"), &index); err == nil {
		for i, s := range index.Slices {
			order[s.Folder] = i
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, err
	}
	var slices []*Slice
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		file := filepath.Join(dir, e.Name(), "slice.json")
		if _, err := os.Stat(file); err != nil {
			continue
		}
		var s Slice
		if err := readJSON(file, &s); err != nil {
			return "", nil, err
		}
		s.Folder = e.Name()
		slices = append(slices, &s)
	}
	sort.SliceStable(slices, func(i, j int) bool {
		oi, iok := order[slices[i].Folder]
		oj, jok := order[slices[j].Folder]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		}
		return slices[i].Folder < slices[j].Folder
	})
	if len(slices) == 0 {
		return "", nil, fmt.Errorf("no slice.json files under %s", dir)
	}
	if context == "" {
		context = slices[0].Context
	}
	return context, slices, nil
}

// hasSlices reports whether dir holds slice folders directly.
func hasSlices(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "slice.json"))
	return len(matches) > 0
}

// contextDir finds the directory of context under root: the context name as
// is, or its lowercase slug.
func contextDir(root, context string) (string, error) {
	for _, name := range []string{context, folderName(context)} {
		dir := filepath.Join(root, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("context %q not found under %s", context, root)
}

func loadExport(path, context string) (string, []*Slice, error) {
	var export struct {
		Slices []*Slice `json:"slices"`
	}
	if err := readJSON(path, &export); err != nil {
		return "", nil, err
	}
	var slices []*Slice
	for _, s := range export.Slices {
		if context != "" && s.Context != context {
			continue
		}
		if context == "" {
			context = s.Context
		}
		s.Folder = folderName(s.Title)
		slices = append(slices, s)
	}
	if len(slices) == 0 {
		return "", nil, errors.New("the export holds no slices for that context")
	}
	return context, slices, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
