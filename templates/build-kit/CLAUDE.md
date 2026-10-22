# Project Configuration

This project is a Go service built on
[github.com/terraskye/eventsourcing](https://github.com/TerraSkye/eventsourcing)
(v0.2.0): commands handled with `decide`/`evolve`, read models kept up to date by
`projection.Runner`, automations working off todo lists with `automation`. Read
`events/` to see every event of the context; it is the domain at a glance.

Most code is scaffolded by **esgen**, the generator in `.build-kit/gen`, straight
from the slice definitions. The skills below run it first and then finish what
it cannot know.

## File Structure

```
go.mod
main.go                  # yours: starts the service (written once by esgen -init)
wiring.go                # yours: the backend — the ONLY file that imports sqlite/postgres/kurrentdb
app_gen.go               # generated on every esgen run: registers every slice. Never edit.
app_gen_test.go          # generated: boots the app on testDatabase() and calls every route
events/<event>.go        # generated on every esgen run, one file per event. Never edit.
slices/<slice>/          # one folder per board slice (the slice's folder name)
  command.go             #   STATE_CHANGE / AUTOMATION: command, state, evolve, decide, NewHandler
  command_test.go        #   the slice's Given/When/Then specifications, table-driven
  http.go                #   POST (command) or GET (read model) route
  model.go               #   STATE_VIEW: the read model (+ Claim when it is a todo list)
  projection.go          #   STATE_VIEW: projection.Definition, one OnEvent handler per event
  query.go               #   STATE_VIEW: Query and QueryHandler
  projection_test.go     #   STATE_VIEW: the slice's Given/Then specifications
automations/<name>/      # AUTOMATION: automation.Definition working off a todo list
.build-kit/gen/          # esgen (its own Go module; `go build ./...` at the root skips it)
```

- **Strict path limitation**: work inside `slices/<slice>/` and
  `automations/<name>/` of the slice you are building. Touch `events/` and
  `app_gen*.go` only by re-running esgen. Touch `wiring.go` and `main.go` only
  when asked.
- **Slice organization**: one folder per slice, named after the slice's folder
  in `.build-kit/.slices/<context>/` (`Add Item` → `slices/additem`).
- Slice names are case-insensitive; the folder name is the slice title in lower
  case without spaces.

## Code Standards

- **Language**: Go 1.24+.
- **Module system**: Go modules. `go mod tidy` after adding imports.
- **Type safety**: no `any` in commands, events or rows except the
  `json.RawMessage` esgen uses for a Custom field without subfields.
- Import the library as `github.com/terraskye/eventsourcing` (package name
  `eventsourcing`), events as `<module>/events`.
- Slices depend only on the library's interfaces (`eventsourcing.EventStore`,
  `eventsourcing.Dispatcher`, `projection.Definition`, `automation.Dependency`)
  — never on `sqlite`, `postgres` or `kurrentdb`. The backend lives in
  `wiring.go` alone.
- `evolve` is pure. `decide` is pure and returns plain errors for rejections:
  `NewCommandHandler` wraps them in `eventsourcing.BusinessRuleViolationError`.
- Tests use the standard `testing` package, table-driven, in the slice's own
  package; one case per specification, named `spec: <Slice> - <Spec>`.

## Development Guidelines

1. Each slice is self-contained: its command or read model, its rules, its
   route, its tests.
2. The slice.json is the desired state; the code follows it. No field, event
   or rule that is not in slice.json.
3. A specification esgen could not turn into code is a test with
   `skip: "TODO: …"`. A slice is not done while one is left: implement the rule,
   then delete the `skip`.
4. Keep the generated structure; add to it rather than reshaping it, so the
   next esgen run (into a scratch directory) still diffs cleanly.

At the start of every session, read `.build-kit/AGENTS.md` if it exists to load
the project's accumulated learnings. It is generated: record a new learning with
`npx @eventmodelers/cli learn "<one terse line>" --topic <topic>`, never by
editing the file.

When working from a queued task, invoke the `update-slice-status` skill with
`InProgress` before anything else. When the task request names the slice to
build (a run-loop build), the run loop has already set it to `InProgress` and
pushes the final status itself: do not call `update-slice-status`; record the
outcome with `npx @eventmodelers/cli slice-status <status>`.

## Building a Slice

**Always build a slice through its skill. Never implement a slice without one.**
**Every field, event, command and rule comes from slice.json. Never invent one.**

When a detail is unclear (an example value, a field type, an event another
slice has not modeled yet), make the most sensible assumption from slice.json,
its specifications and the rest of the model, build it, and record the
assumption as a one-line code comment. Only when nothing runnable can be built
at all, invoke `/request-feedback` with the specific question; it comments on
the slice and marks it `Blocked`.

1. Read the slice definition from `.build-kit/.slices/<context>/<slicename>/slice.json`.
2. Pick the skill by `sliceType`:
   - `STATE_CHANGE` → `/build-state-change`
   - `STATE_VIEW` → `/build-state-view`
   - `AUTOMATION` → `/build-automation` (it builds the slice's command with
     `/build-state-change` first)
   - `TRANSLATION` → read `description` and `notes` for hints; default to
     `/build-automation`
   - anything else (e.g. `UNDEFINED`): a slice with only events needs nothing
     but its events, which esgen writes on every run — run esgen, check
     `events/`, done.
3. Follow the skill completely.
4. **Baseline gate**: run `npx @eventmodelers/cli build-baseline check --scope backend`
   first and act on its verdict only — `UNCHANGED`: do not rebuild, run the
   slice's tests and finish; `NO_BASELINE`/`UNKNOWN`: diff slice.json against
   the code field by field; `CHANGED`: diff and fix exactly the elements it
   lists. After the checks pass and before committing, run
   `npx @eventmodelers/cli build-baseline record --scope backend`.
5. **Verify against slice.json**: every command field, event field and
   specification in slice.json has its counterpart in the code, and nothing
   else is there — also when a `Done` slice comes back as `Planned`.
6. Quality checks, from the project root:
   ```
   test -z "$(gofmt -l .)"
   go vet ./...
   go build ./...
   go test ./slices/<slice>/... ./automations/... .
   ```
   (`.` runs `TestApp`, which boots every slice on the test database.)
7. If the checks pass, commit with `feat: [Slice Name]` and set the slice to
   `Done` — or `Waiting` for STATE_VIEW/STATE_CHANGE slices when handover is on
   (see the `ui-contract` skill, Step 4).

## Example Slice Structure

```
slices/additem/
├── command.go        # AddItem, state, evolve, decide, NewHandler
├── command_test.go   # Test_decide: one case per specification
└── http.go           # POST /api/add-item
slices/cartitems/
├── model.go          # Row, CartItems (in-memory read model)
├── projection.go     # NewProjection: OnEvent handlers per event
├── query.go          # Query, QueryHandler
├── http.go           # GET /api/cart-items
└── projection_test.go
automations/publishsubmittedcarts/
├── automation.go     # automation.Definition[cartstopublish.Row]
└── automation_test.go
```

`examples/cart` in the build kit's repository is a complete generated project.

## UI Contract

`build-state-change` and `build-state-view` end by invoking the shared
`ui-contract` skill when handover is on. The contract is derived from the
slice's route in `http.go`: the method and path in `RegisterRoutes`, the JSON
of the command struct (request) or of `Row` (response, a JSON array).
