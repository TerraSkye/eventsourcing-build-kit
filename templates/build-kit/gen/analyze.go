package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// This file turns the loaded slices into the facts the templates render:
// Go types for every element, the stream each command writes, the rules
// decide derives from the specifications, and what each read model does
// with each event it reads.

// goField is a model field with its Go spelling.
type goField struct {
	Name  string // Go field name
	JSON  string // the model's field name, used as JSON key
	Type  string // Go type expression
	Model Field
}

// nestedType is a struct generated for a Custom field with subfields.
type nestedType struct {
	Name   string
	Fields []goField
}

// typed is an element turned into a Go struct.
type typed struct {
	Title  string
	GoName string
	Fields []goField
	Nested []nestedType
	ID     *goField // the field AggregateID returns, nil if none
}

// field returns the field named name (compared loosely), or nil.
func (t *typed) field(name string) *goField {
	for i := range t.Fields {
		if sameName(t.Fields[i].JSON, name) || sameName(t.Fields[i].Name, name) {
			return &t.Fields[i]
		}
	}
	return nil
}

// AggregateExpr is the expression AggregateID returns, with recv as the
// receiver.
func (t *typed) AggregateExpr(recv string) string {
	if t.ID == nil {
		return strconv.Quote(kebab(t.Title))
	}
	return stringExpr(recv+"."+t.ID.Name, t.ID.Type)
}

func stringExpr(expr, typ string) string {
	switch typ {
	case "string":
		return expr
	case "uuid.UUID":
		return expr + ".String()"
	case "int":
		return "strconv.Itoa(" + expr + ")"
	case "int64":
		return "strconv.FormatInt(" + expr + ", 10)"
	default:
		return "fmt.Sprint(" + expr + ")"
	}
}

// newTyped converts el to a Go struct whose nested types are prefixed with
// prefix.
func newTyped(el Element) *typed {
	t := &typed{Title: el.Title, GoName: exportedName(el.Title)}
	t.Fields = goFields(el.Fields, t.GoName, &t.Nested)
	t.ID = pickID(t.Fields)
	return t
}

func goFields(fields []Field, owner string, nested *[]nestedType) []goField {
	var out []goField
	seen := map[string]bool{}
	for _, f := range fields {
		name := exportedName(f.Name)
		if f.Name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, goField{Name: name, JSON: f.Name, Type: goType(f, owner, nested), Model: f})
	}
	return out
}

// goType maps an EM Spec field type to Go.
func goType(f Field, owner string, nested *[]nestedType) string {
	var t string
	switch strings.ToLower(f.Type) {
	case "string":
		t = "string"
	case "boolean":
		t = "bool"
	case "double", "decimal", "number":
		t = "float64"
	case "long":
		t = "int64"
	case "int", "integer":
		t = "int"
	case "date", "datetime":
		t = "time.Time"
	case "uuid":
		t = "uuid.UUID"
	case "custom":
		if len(f.Subfields) == 0 {
			// No shape on the board: keep the raw JSON.
			return "json.RawMessage"
		}
		name := owner + exportedName(f.Name)
		*nested = append(*nested, nestedType{Name: name, Fields: goFields(f.Subfields, name, nested)})
		t = name
	default:
		t = "string"
	}
	if f.IsList() {
		return "[]" + t
	}
	return t
}

// comparable reports whether values of the Go type can be map keys.
func comparableType(t string) bool {
	switch t {
	case "string", "bool", "float64", "int64", "int", "time.Time", "uuid.UUID":
		return true
	}
	return false
}

// pickID chooses the field an element's AggregateID returns: the first
// idAttribute field, else aggregateId or id, else the first UUID field.
func pickID(fields []goField) *goField {
	for i := range fields {
		if fields[i].Model.IDAttribute && comparableType(fields[i].Type) {
			return &fields[i]
		}
	}
	for i := range fields {
		if sameName(fields[i].JSON, "aggregateId") || sameName(fields[i].JSON, "id") {
			return &fields[i]
		}
	}
	for i := range fields {
		if fields[i].Type == "uuid.UUID" {
			return &fields[i]
		}
	}
	return nil
}

// source finds the field of from that fills to: same name, or to's mapping
// naming it, with the same Go type.
func source(to goField, from *typed) *goField {
	for i := range from.Fields {
		f := &from.Fields[i]
		if (sameName(to.JSON, f.JSON) || sameName(to.Model.Mapping, f.JSON)) && to.Type == f.Type {
			return f
		}
	}
	return nil
}

// ---- Literals -------------------------------------------------------------

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// literal renders an example value as a Go literal of type typ. ok is false
// when there is no usable example.
func literal(typ, example string) (lit string, ok bool) {
	if example == "" {
		return "", false
	}
	switch typ {
	case "string":
		return strconv.Quote(example), true
	case "bool":
		b, err := strconv.ParseBool(example)
		return strconv.FormatBool(b), err == nil
	case "int", "int64":
		n, err := strconv.ParseInt(example, 10, 64)
		return strconv.FormatInt(n, 10), err == nil
	case "float64":
		f, err := strconv.ParseFloat(example, 64)
		if err != nil {
			return "", false
		}
		s := strconv.FormatFloat(f, 'g', -1, 64)
		return s, true
	case "uuid.UUID":
		if !uuidRE.MatchString(example) {
			return "", false
		}
		return `uuid.MustParse("` + strings.ToLower(example) + `")`, true
	case "time.Time":
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
			if _, err := time.Parse(layout, example); err == nil {
				return `mustTime("` + example + `")`, true
			}
		}
	}
	return "", false
}

// assign is one "Field: value" of a composite literal.
type assign struct {
	Field string
	Value string
}

// stepLiteral renders a spec step's example values as fields of t.
func stepLiteral(t *typed, step Step) []assign {
	var out []assign
	for _, f := range t.Fields {
		for _, sf := range step.Fields {
			if !sameName(sf.Name, f.JSON) {
				continue
			}
			if lit, ok := literal(f.Type, sf.ExampleString()); ok {
				out = append(out, assign{f.Name, lit})
			}
			break
		}
	}
	return out
}

func lookup(as []assign, field string) (string, bool) {
	for _, a := range as {
		if a.Field == field {
			return a.Value, true
		}
	}
	return "", false
}

// ---- The catalog -------------------------------------------------------------

// catalog is every element of the context, by Go name.
type catalog struct {
	Context  string
	Module   string
	events   map[string]*typed
	Events   []*typed
	Commands []*command
	Views    []*view
	Autos    []*auto
	Slices   []*sliceInfo
}

// sliceInfo is one slice and what was generated for it.
type sliceInfo struct {
	*Slice
	Package  string
	Commands []*command
	Views    []*view
	Autos    []*auto
	Notes    []string
}

func (c *catalog) event(title string) *typed { return c.events[exportedName(title)] }

func newCatalog(context, module string, model []*Slice) *catalog {
	c := &catalog{Context: context, Module: module, events: map[string]*typed{}}

	// Events are shared by the whole context: merge every copy of an event
	// on the board into one type.
	for _, s := range model {
		for _, el := range s.Events {
			t := newTyped(el)
			if prev, ok := c.events[t.GoName]; ok {
				for _, f := range t.Fields {
					if prev.field(f.JSON) == nil {
						prev.Fields = append(prev.Fields, f)
					}
				}
				prev.Nested = append(prev.Nested, t.Nested...)
				continue
			}
			c.events[t.GoName] = t
			c.Events = append(c.Events, t)
		}
	}
	// Events that only appear in specifications still need a type.
	for _, s := range model {
		for _, spec := range s.Specifications {
			for _, steps := range []Steps{spec.Given, spec.Then} {
				for _, st := range steps {
					if st.Kind() == stepEvent && c.event(st.Title) == nil {
						el := Element{Title: st.Title, Fields: st.Fields}
						t := newTyped(el)
						c.events[t.GoName] = t
						c.Events = append(c.Events, t)
					}
				}
			}
		}
	}
	sort.Slice(c.Events, func(i, j int) bool { return c.Events[i].GoName < c.Events[j].GoName })

	for _, s := range model {
		info := &sliceInfo{Slice: s, Package: packageName(s.Folder)}
		c.Slices = append(c.Slices, info)
		switch strings.ToUpper(s.SliceType) {
		case "STATE_VIEW":
			for i, rm := range s.Readmodels {
				if i > 0 {
					info.Notes = append(info.Notes, fmt.Sprintf("read model %q is not generated: esgen builds one read model per slice", rm.Title))
					continue
				}
				v := c.newView(info, rm)
				info.Views = append(info.Views, v)
				c.Views = append(c.Views, v)
			}
		case "STATE_CHANGE", "AUTOMATION", "TRANSLATION":
			for i, el := range s.Commands {
				if i > 0 {
					info.Notes = append(info.Notes, fmt.Sprintf("command %q is not generated: esgen builds one command per slice", el.Title))
					continue
				}
				cmd := c.newCommand(info, el, false)
				info.Commands = append(info.Commands, cmd)
				c.Commands = append(c.Commands, cmd)
			}
		default:
			if len(s.Commands) > 0 || len(s.Readmodels) > 0 || len(s.Processors) > 0 {
				info.Notes = append(info.Notes, fmt.Sprintf("slice type %q is not built; set it to STATE_CHANGE, STATE_VIEW or AUTOMATION", s.SliceType))
			}
		}
	}
	// Automations last: they need the views and commands they connect.
	for _, info := range c.Slices {
		for _, p := range info.Processors {
			a := c.newAuto(info, p)
			if a != nil {
				info.Autos = append(info.Autos, a)
				c.Autos = append(c.Autos, a)
			}
		}
	}
	// View specifications run once the automations have marked todo lists.
	for _, v := range c.Views {
		v.analyze(c)
	}
	return c
}

// ---- State change ------------------------------------------------------------

// command is a command of a STATE_CHANGE or AUTOMATION slice, with the
// decide rules derived from its specifications.
type command struct {
	*typed
	Slice  *sliceInfo
	Prefix string // "" or the command's name, when a slice has several
	Emits  []*typed
	Route  string

	Counted []*typed // event types the state counts
	Rules   []*rule  // checked in order
	Default *rule    // when no rule matches
	Errors  []*ruleError
	Builds  []*builder // one constructor per event decide emits
	Specs   []*cmdSpec
	Notes   []string
	id      string
}

type rule struct {
	Given  map[string]int // event Go name → minimum count
	Order  []string       // Given's keys in first-seen order
	Err    *ruleError
	Events []*typed
	Specs  []string
}

func (r *rule) total() int {
	n := 0
	for _, v := range r.Given {
		n += v
	}
	return n
}

func (r *rule) matches(history map[string]int) bool {
	for k, n := range r.Given {
		if history[k] < n {
			return false
		}
	}
	return true
}

func (r *rule) sameOutcome(o *rule) bool {
	if (r.Err == nil) != (o.Err == nil) {
		return false
	}
	if r.Err != nil {
		return r.Err.Var == o.Err.Var
	}
	if len(r.Events) != len(o.Events) {
		return false
	}
	for i := range r.Events {
		if r.Events[i].GoName != o.Events[i].GoName {
			return false
		}
	}
	return true
}

// Condition renders the rule's history requirement as a Go condition on
// state s.
func (r *rule) Condition() string {
	var parts []string
	for _, k := range r.Order {
		n := r.Given[k]
		if n == 1 {
			parts = append(parts, fmt.Sprintf("s.%s > 0", unexportedName(k)))
		} else {
			parts = append(parts, fmt.Sprintf("s.%s >= %d", unexportedName(k), n))
		}
	}
	return strings.Join(parts, " && ")
}

type ruleError struct {
	Var     string
	Message string
}

// builder is the constructor decide uses for one event.
type builder struct {
	Func   string
	Event  *typed
	Assign []builderField
}

type builderField struct {
	Field string
	Expr  string // "" when the command does not carry it
	Note  string
}

// cmdSpec is one specification rendered as a test case.
type cmdSpec struct {
	Title   string
	Given   []eventLit
	When    []assign
	Want    []eventLit
	WantErr string
	Skip    string
	Notes   []string
}

type eventLit struct {
	Event  *typed
	Fields []assign
}

func (c *catalog) newCommand(info *sliceInfo, el Element, prefixed bool) *command {
	cmd := &command{typed: newTyped(el), Slice: info, id: el.ID}
	if prefixed {
		cmd.Prefix = unexportedName(cmd.GoName)
	}
	cmd.Route = el.route()
	if cmd.Route == "" {
		cmd.Route = "/api/" + kebab(cmd.Title)
	}
	for _, title := range el.dependencies("OUTBOUND", "EVENT") {
		if ev := c.event(title); ev != nil {
			cmd.Emits = append(cmd.Emits, ev)
		} else {
			cmd.Notes = append(cmd.Notes, fmt.Sprintf("%q is connected to the command but not modeled as an event", title))
		}
	}
	if len(cmd.Emits) == 0 {
		// Fall back to the slice's own events.
		for _, e := range info.Events {
			if ev := c.event(e.Title); ev != nil {
				cmd.Emits = append(cmd.Emits, ev)
			}
		}
	}
	c.deriveRules(cmd, info.Specifications, len(info.Commands) > 1)
	return cmd
}

// route is the element's API endpoint when the board gives one as a path.
func (e Element) route() string {
	ep := strings.TrimSpace(e.APIEndpoint)
	if !strings.HasPrefix(ep, "/") || strings.ContainsAny(ep, " \t{}") {
		return ""
	}
	return ep
}

// errorTitle is the message of a rejection step.
func errorTitle(spec Spec, st Step) string {
	for _, s := range []string{st.Title, st.Description, spec.Title} {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return "rejected"
}

func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) > 1 && r[0] >= 'A' && r[0] <= 'Z' && !(r[1] >= 'A' && r[1] <= 'Z') {
		r[0] = r[0] - 'A' + 'a'
	}
	return string(r)
}

// deriveRules builds decide from the specifications of cmd. Each
// specification says: after these events, this command records these
// events (or is rejected). decide keeps a count per event type and checks
// the specifications from the most specific history to the least; the
// generator then replays every specification through that chain and marks
// the ones it cannot reproduce for a human.
func (c *catalog) deriveRules(cmd *command, specs []Spec, several bool) {
	type candidate struct {
		spec Spec
		r    *rule
		cs   *cmdSpec
	}
	var cands []candidate
	errs := map[string]*ruleError{}

	for _, spec := range specs {
		if !c.specTargets(spec, cmd, several) {
			continue
		}
		cs := &cmdSpec{Title: spec.Title}
		cmd.Specs = append(cmd.Specs, cs)
		r := &rule{Given: map[string]int{}, Specs: []string{spec.Title}}

		for _, st := range spec.Given {
			switch st.Kind() {
			case stepEvent, "":
				ev := c.event(st.Title)
				if ev == nil {
					cs.Skip = fmt.Sprintf("given %q is not an event of this context", st.Title)
					continue
				}
				if _, ok := r.Given[ev.GoName]; !ok {
					r.Order = append(r.Order, ev.GoName)
				}
				r.Given[ev.GoName]++
				cs.Given = append(cs.Given, eventLit{ev, stepLiteral(ev, st)})
			default:
				cs.Notes = append(cs.Notes, fmt.Sprintf("given step %q (%s) is not an event; left out", st.Title, st.Type))
			}
		}
		for _, st := range spec.When {
			if st.Kind() == stepCommand || st.Kind() == "" {
				cs.When = stepLiteral(cmd.typed, st)
			}
		}
		for _, st := range spec.Then {
			switch st.Kind() {
			case stepError:
				msg := lowerFirst(errorTitle(spec, st))
				v := "err" + exportedName(msg)
				e, ok := errs[v]
				if !ok {
					e = &ruleError{Var: v, Message: msg}
					errs[v] = e
					cmd.Errors = append(cmd.Errors, e)
				}
				r.Err = e
			case stepEvent, "":
				ev := c.event(st.Title)
				if ev == nil {
					cs.Skip = fmt.Sprintf("then %q is not an event of this context", st.Title)
					continue
				}
				r.Events = append(r.Events, ev)
				cs.Want = append(cs.Want, eventLit{ev, stepLiteral(ev, st)})
			default:
				cs.Notes = append(cs.Notes, fmt.Sprintf("then step %q (%s) is not checked by decide", st.Title, st.Type))
			}
		}
		if r.Err != nil {
			r.Events = nil
			cs.Want = nil
			cs.WantErr = r.Err.Var
		}
		if cs.Skip == "" {
			cands = append(cands, candidate{spec, r, cs})
		}
	}

	// One rule per distinct history; specifications that disagree about
	// the same history are left to a human.
	byKey := map[string]*rule{}
	var keys []string
	conflict := map[string]bool{}
	for _, cd := range cands {
		k := historyKey(cd.r.Given)
		if prev, ok := byKey[k]; ok {
			if !prev.sameOutcome(cd.r) {
				conflict[k] = true
			}
			prev.Specs = append(prev.Specs, cd.spec.Title)
			continue
		}
		byKey[k] = cd.r
		keys = append(keys, k)
	}
	var rules []*rule
	for _, k := range keys {
		if conflict[k] {
			continue
		}
		r := byKey[k]
		if len(r.Given) == 0 {
			cmd.Default = r
			continue
		}
		rules = append(rules, r)
	}
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].total() > rules[j].total() })
	cmd.Rules = rules
	if cmd.Default == nil {
		cmd.Default = &rule{Given: map[string]int{}, Events: cmd.Emits}
	}

	// Replay every specification through the chain.
	for _, cd := range cands {
		if conflict[historyKey(cd.r.Given)] {
			cd.cs.Skip = "other specifications expect a different outcome after the same events; decide this rule by hand"
			continue
		}
		got := cmd.Default
		for _, r := range cmd.Rules {
			if r.matches(cd.r.Given) {
				got = r
				break
			}
		}
		if !got.sameOutcome(cd.r) {
			cd.cs.Skip = fmt.Sprintf("the rule derived from %q also matches these events; decide this case by hand", got.Specs[0])
		}
	}

	// The state counts every event type a rule looks at.
	counted := map[string]bool{}
	for _, r := range cmd.Rules {
		for _, k := range r.Order {
			if !counted[k] {
				counted[k] = true
				cmd.Counted = append(cmd.Counted, c.events[k])
			}
		}
	}

	// One constructor per emitted event.
	built := map[string]bool{}
	for _, r := range append(slices.Clone(cmd.Rules), cmd.Default) {
		for _, ev := range r.Events {
			if built[ev.GoName] {
				continue
			}
			built[ev.GoName] = true
			cmd.Builds = append(cmd.Builds, newBuilder(cmd, ev))
		}
	}

	// Expected events hold what decide derives: the spec's value where it
	// gives one, else the command's.
	for _, cd := range cands {
		if cd.cs.Skip != "" {
			continue
		}
		for i, w := range cd.cs.Want {
			cd.cs.Want[i].Fields = expectedFields(cmd, cd.cs, w, &cd.cs.Skip)
		}
	}
}

func historyKey(g map[string]int) string {
	var parts []string
	for k, n := range g {
		parts = append(parts, fmt.Sprintf("%s=%d", k, n))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// specTargets reports whether spec exercises cmd.
func (c *catalog) specTargets(spec Spec, cmd *command, several bool) bool {
	if spec.LinkedID != "" && spec.LinkedID == cmd.idOf() {
		return true
	}
	for _, st := range spec.When {
		if sameName(st.Title, cmd.Title) {
			return true
		}
	}
	// A slice with one command: a spec with no recognizable When still
	// belongs to it.
	return !several && len(spec.When) == 0 && hasThenEventOrError(spec)
}

func hasThenEventOrError(spec Spec) bool {
	for _, st := range spec.Then {
		if st.Kind() == stepEvent || st.Kind() == stepError {
			return true
		}
	}
	return false
}

func (cmd *command) idOf() string { return cmd.id }

func newBuilder(cmd *command, ev *typed) *builder {
	b := &builder{Func: unexportedName(ev.GoName), Event: ev}
	if cmd.Prefix != "" {
		b.Func = cmd.Prefix + ev.GoName
	}
	for _, f := range ev.Fields {
		bf := builderField{Field: f.Name}
		switch {
		case source(f, cmd.typed) != nil:
			bf.Expr = "cmd." + source(f, cmd.typed).Name
		case f.Model.Generated && f.Type == "time.Time":
			bf.Expr = "time.Now()"
		case f.Model.Generated && f.Type == "uuid.UUID":
			bf.Expr = "uuid.New()"
		default:
			bf.Note = fmt.Sprintf("%s is not on the command; derive it from the state or the command", f.Name)
		}
		b.Assign = append(b.Assign, bf)
	}
	return b
}

// expectedFields is the want literal for one event of a test case.
func expectedFields(cmd *command, cs *cmdSpec, w eventLit, skip *string) []assign {
	var out []assign
	for _, f := range w.Event.Fields {
		want, hasWant := lookup(w.Fields, f.Name)
		src := source(f, cmd.typed)
		switch {
		case f.Model.Generated:
			// Not compared: the test clears generated fields.
		case src != nil:
			got, hasGot := lookup(cs.When, src.Name)
			switch {
			case hasWant && hasGot && want != got:
				*skip = fmt.Sprintf("%s.%s is expected to be %s but the command carries %s; derive it by hand", w.Event.GoName, f.Name, want, got)
			case hasWant && !hasGot:
				*skip = fmt.Sprintf("%s.%s is expected to be %s but the command does not carry it; derive it by hand", w.Event.GoName, f.Name, want)
			case hasGot:
				out = append(out, assign{f.Name, got})
			}
		case hasWant:
			cs.Notes = append(cs.Notes, fmt.Sprintf("the specification expects %s.%s = %s, which decide does not derive yet", w.Event.GoName, f.Name, want))
		}
	}
	return out
}

// HasGenerated reports whether an event decide builds has a field the
// system fills in.
func (cmd *command) HasGenerated() bool {
	for _, b := range cmd.Builds {
		if len(b.Event.Generated()) > 0 {
			return true
		}
	}
	return false
}

// hoist replaces the UUID and time literals of the given assignments with
// variables named after the first field that uses them, and returns the
// variables.
func hoist(lists ...[]assign) []assign {
	var vars []assign
	byLit := map[string]string{}
	used := map[string]int{}
	for _, list := range lists {
		for i := range list {
			lit := list[i].Value
			if !strings.HasPrefix(lit, "uuid.MustParse(") && !strings.HasPrefix(lit, "mustTime(") {
				continue
			}
			name, ok := byLit[lit]
			if !ok {
				base := unexportedName(list[i].Field)
				used[base]++
				name = base
				if n := used[base]; n > 1 {
					name = fmt.Sprintf("%s%d", base, n)
				}
				byLit[lit] = name
				vars = append(vars, assign{name, lit})
			}
			list[i].Value = name
		}
	}
	return vars
}

// testVars hoists the literals of the command's test cases.
func (cmd *command) testVars() []assign {
	var lists [][]assign
	for _, cs := range cmd.Specs {
		lists = append(lists, cs.When)
		for _, e := range cs.Given {
			lists = append(lists, e.Fields)
		}
		for _, e := range cs.Want {
			lists = append(lists, e.Fields)
		}
	}
	return hoist(lists...)
}

// testVars hoists the literals of the view's test cases.
func (v *view) testVars() []assign {
	var lists [][]assign
	for _, vs := range v.Specs {
		for _, e := range vs.Given {
			lists = append(lists, e.Fields)
		}
		lists = append(lists, vs.Want...)
	}
	return hoist(lists...)
}

// Generated lists the fields of t that the system fills in; tests do not
// compare them.
func (t *typed) Generated() []goField {
	var out []goField
	for _, f := range t.Fields {
		if f.Model.Generated {
			out = append(out, f)
		}
	}
	return out
}

// ---- State view ----------------------------------------------------------------

type view struct {
	*typed
	Slice    *sliceInfo
	Name     string // projection name
	Route    string
	Keys     []goField
	Handlers []*viewHandler
	Ignored  []string
	Todo     *auto // set when an automation works off this read model
	Specs    []*viewSpec
	Notes    []string
	element  Element
}

type viewHandler struct {
	Event  *typed
	Delete bool
	KeyMap []pair // row key field ← event field; all keys when upserting
	Copy   []pair // row field ← event field
}

type pair struct{ To, From string }

// FullKey reports whether the handler sets every key field.
func (h *viewHandler) FullKey(v *view) bool { return len(h.KeyMap) == len(v.Keys) }

type viewSpec struct {
	Title string
	Given []eventLit
	Want  [][]assign
	Skip  string
	Notes []string
}

func (c *catalog) newView(info *sliceInfo, el Element) *view {
	v := &view{typed: newTyped(el), Slice: info, Name: kebab(el.Title), element: el}
	v.Route = "/api/" + kebab(el.Title)
	for _, f := range v.Fields {
		if f.Model.IDAttribute && comparableType(f.Type) {
			v.Keys = append(v.Keys, f)
		}
	}
	if len(v.Keys) == 0 && v.ID != nil {
		v.Keys = []goField{*v.ID}
	}
	if len(v.Keys) == 0 {
		v.Notes = append(v.Notes, "the read model has no idAttribute field; mark one on the board so rows can be keyed")
	}
	return v
}

// analyze decides what the view does with each event and checks it
// against the view's specifications.
func (v *view) analyze(c *catalog) {
	var removers map[string]bool
	if v.Todo != nil {
		removers = map[string]bool{}
		for _, ev := range v.Todo.Command.Emits {
			removers[ev.GoName] = true
		}
	}
	for _, title := range v.element.dependencies("INBOUND", "EVENT") {
		ev := c.event(title)
		if ev == nil {
			v.Notes = append(v.Notes, fmt.Sprintf("%q feeds the read model on the board but is not modeled as an event", title))
			continue
		}
		h := &viewHandler{Event: ev}
		for _, k := range v.Keys {
			if src := source(k, ev); src != nil {
				h.KeyMap = append(h.KeyMap, pair{k.Name, src.Name})
			}
		}
		for _, f := range v.Fields {
			if v.isKey(f.Name) {
				continue
			}
			if src := source(f, ev); src != nil {
				h.Copy = append(h.Copy, pair{f.Name, src.Name})
			}
		}
		h.Delete = removers[ev.GoName]
		switch {
		case len(h.KeyMap) == 0:
			v.Ignored = append(v.Ignored, ev.GoName)
			continue
		case !h.FullKey(v):
			// Part of the key only: the event can remove rows, not
			// write one. The specifications decide below whether it does.
			h.Delete = true
		}
		v.Handlers = append(v.Handlers, h)
	}

	// Collect the specifications.
	var specs []Spec
	for _, s := range v.Slice.Specifications {
		if v.specTargets(s) {
			specs = append(specs, s)
		}
	}
	// An event that may upsert or delete takes whichever the
	// specifications agree with most.
	for _, h := range v.Handlers {
		if !h.FullKey(v) {
			continue
		}
		before := v.passing(c, specs)
		h.Delete = !h.Delete
		if v.passing(c, specs) <= before {
			h.Delete = !h.Delete
		}
	}
	// An event that carries part of the key removes rows only when the
	// specifications show it; a todo list's done-events always do.
	for _, h := range slices.Clone(v.Handlers) {
		if h.FullKey(v) || (v.Todo != nil && removers[h.Event.GoName]) {
			continue
		}
		with := v.passing(c, specs)
		v.Handlers = slices.DeleteFunc(v.Handlers, func(x *viewHandler) bool { return x == h })
		if with > v.passing(c, specs) {
			v.Handlers = append(v.Handlers, h)
			continue
		}
		v.Ignored = append(v.Ignored, h.Event.GoName)
	}
	sort.SliceStable(v.Handlers, func(i, j int) bool { return v.order(v.Handlers[i]) < v.order(v.Handlers[j]) })
	for _, s := range specs {
		v.Specs = append(v.Specs, v.renderSpec(c, s))
	}
}

// order is the position of h's event among the read model's inputs.
func (v *view) order(h *viewHandler) int {
	for i, title := range v.element.dependencies("INBOUND", "EVENT") {
		if exportedName(title) == h.Event.GoName {
			return i
		}
	}
	return 0
}

func (v *view) isKey(name string) bool {
	for _, k := range v.Keys {
		if k.Name == name {
			return true
		}
	}
	return false
}

func (v *view) handler(ev string) *viewHandler {
	for _, h := range v.Handlers {
		if h.Event.GoName == ev {
			return h
		}
	}
	return nil
}

func (v *view) specTargets(s Spec) bool {
	for _, st := range s.Then {
		if st.Kind() == stepReadModel && sameName(st.Title, v.Title) {
			return true
		}
	}
	return len(v.Slice.Readmodels) == 1 && len(s.When) == 0
}

func (v *view) passing(c *catalog, specs []Spec) int {
	n := 0
	for _, s := range specs {
		if v.renderSpec(c, s).Skip == "" {
			n++
		}
	}
	return n
}

// renderSpec simulates the projection on the specification's events and
// compares the rows it ends with to the expected ones.
func (v *view) renderSpec(c *catalog, s Spec) *viewSpec {
	vs := &viewSpec{Title: s.Title}
	type row struct {
		key    string
		fields []assign
	}
	var rows []*row
	keyOf := func(as []assign) string {
		var parts []string
		for _, k := range v.Keys {
			val, _ := lookup(as, k.Name)
			parts = append(parts, val)
		}
		return strings.Join(parts, "|")
	}
	set := func(r *row, field, val string) {
		for i := range r.fields {
			if r.fields[i].Field == field {
				r.fields[i].Value = val
				return
			}
		}
		r.fields = append(r.fields, assign{field, val})
	}

	for _, st := range s.Given {
		if st.Kind() != stepEvent && st.Kind() != "" {
			continue
		}
		ev := c.event(st.Title)
		if ev == nil {
			vs.Skip = fmt.Sprintf("given %q is not an event of this context", st.Title)
			return vs
		}
		h := v.handler(ev.GoName)
		if h == nil {
			vs.Notes = append(vs.Notes, fmt.Sprintf("%s is left out: the projection does not handle it", ev.GoName))
			continue
		}
		lit := stepLiteral(ev, st)
		vs.Given = append(vs.Given, eventLit{ev, lit})
		var key []assign
		for _, p := range h.KeyMap {
			val, ok := lookup(lit, p.From)
			if !ok {
				vs.Skip = fmt.Sprintf("given %s has no example for key %s", ev.GoName, p.From)
				return vs
			}
			key = append(key, assign{p.To, val})
		}
		if h.Delete {
			kept := rows[:0]
			for _, r := range rows {
				match := true
				for _, k := range key {
					if val, _ := lookup(r.fields, k.Field); val != k.Value {
						match = false
					}
				}
				if !match {
					kept = append(kept, r)
				}
			}
			rows = kept
			continue
		}
		k := keyOf(key)
		var r *row
		for _, x := range rows {
			if x.key == k {
				r = x
			}
		}
		if r == nil {
			r = &row{key: k, fields: slices.Clone(key)}
			rows = append(rows, r)
		}
		for _, p := range h.Copy {
			if val, ok := lookup(lit, p.From); ok {
				set(r, p.To, val)
			}
		}
	}

	// The expected rows.
	var want [][]assign
	for _, st := range s.Then {
		if st.Kind() != stepReadModel {
			continue
		}
		if st.ExpectEmptyList {
			continue
		}
		if len(st.Examples) > 0 {
			for _, ex := range st.Examples {
				var fs []Field
				for name, val := range ex {
					fs = append(fs, Field{Name: name, Example: val})
				}
				want = append(want, stepLiteral(v.typed, Step{Fields: fs}))
			}
			continue
		}
		if lit := stepLiteral(v.typed, st); len(lit) > 0 {
			want = append(want, lit)
		} else {
			vs.Skip = "the specification shows the read model without example values; add them on the board"
			return vs
		}
	}

	if len(want) != len(rows) {
		vs.Skip = fmt.Sprintf("the generated projection ends with %d rows where the specification expects %d; adjust the handlers by hand", len(rows), len(want))
		return vs
	}
	for _, w := range want {
		k := keyOf(w)
		var r *row
		for _, x := range rows {
			if x.key == k {
				r = x
			}
		}
		if r == nil {
			vs.Skip = "the generated projection ends with other rows than the specification expects; adjust the handlers by hand"
			return vs
		}
		for _, a := range w {
			if got, _ := lookup(r.fields, a.Field); got != a.Value {
				vs.Skip = fmt.Sprintf("the specification expects %s = %s, the generated projection gives %q; adjust the handlers by hand", a.Field, a.Value, got)
				return vs
			}
		}
	}
	// Want the simulated rows: they hold every value the projection sets.
	for _, r := range rows {
		vs.Want = append(vs.Want, r.fields)
	}
	return vs
}

// ---- Automation ----------------------------------------------------------------

type auto struct {
	Package string
	Var     string // variable name in app_gen.go
	Title   string
	Name    string // automation name
	Slice   *sliceInfo
	Todo    *view
	Command *command
	Work    []builderField
	Notes   []string
}

func (c *catalog) newAuto(info *sliceInfo, p Element) *auto {
	a := &auto{Title: p.Title, Name: kebab(p.Title), Slice: info, Package: packageName(p.Title), Var: unexportedName(p.Title)}
	switch a.Package {
	case "", "automation", "processor", "slice":
		a.Name = kebab(info.Title)
		a.Package = packageName(info.Folder) + "automation"
		a.Var = unexportedName(info.Title)
	}
	for _, title := range p.dependencies("INBOUND", "READMODEL") {
		for _, v := range c.Views {
			if sameName(v.Title, title) {
				a.Todo = v
			}
		}
	}
	for _, title := range p.dependencies("OUTBOUND", "COMMAND") {
		for _, cmd := range c.Commands {
			if sameName(cmd.Title, title) {
				a.Command = cmd
			}
		}
	}
	if a.Command == nil && len(info.Commands) == 1 {
		a.Command = info.Commands[0]
	}
	if a.Todo == nil || a.Command == nil {
		info.Notes = append(info.Notes, fmt.Sprintf("automation %q needs a todo-list read model (INBOUND) and a command (OUTBOUND) on the board; not generated", p.Title))
		return nil
	}
	a.Todo.Todo = a
	for _, f := range a.Command.Fields {
		bf := builderField{Field: f.Name}
		if src := source(f, a.Todo.typed); src != nil {
			bf.Expr = "item." + src.Name
		} else {
			bf.Note = fmt.Sprintf("%s is not on the todo list; look it up or add it to %q on the board", f.Name, a.Todo.Title)
		}
		a.Work = append(a.Work, bf)
	}
	return a
}
