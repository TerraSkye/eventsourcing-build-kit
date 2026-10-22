# eventsourcing-build-kit

A build kit for [Event Modelers](https://app.eventmodelers.ai) that turns the
slices on an event-modeling board into a Go service on
[github.com/terraskye/eventsourcing](https://github.com/TerraSkye/eventsourcing)
(v0.2.0), the way the library's
[todolist example](https://github.com/TerraSkye/eventsourcing/tree/master/examples/todolist)
and tutorials structure one: vertical slices, `decide`/`evolve` command
handlers, projections run by `projection.Runner`, automations working off todo
lists, and a Given/When/Then test for every specification on the board.

It follows the platform's
[Build Your Own Build Kit](https://app.eventmodelers.ai/documentation#build-your-own)
format, so the Event Modelers CLI installs it like any built-in stack and its
runner loop builds `Planned` slices with it.

## What a build kit is

Every Event Modelers build kit is three things: a `CLAUDE.md` with the stack's
conventions, three skills (`build-state-change`, `build-state-view`,
`build-automation`) that turn a slice definition into code, and a runner loop
that watches the board and hands each `Planned` slice to the matching skill. The
CLI ships the runner loop; a kit ships the rest, in this layout:

```
stack.json                         # label, kitSubdir, useShared, needsBoardId
templates/
├── build-kit/                     # → .build-kit/ in the project
│   ├── CLAUDE.md                  #   stack conventions + the build-a-slice flow
│   ├── lib/AGENT.md               #   task shape, slice files, board facts
│   ├── lib/prompt.md              #   instructions for board-triggered tasks
│   ├── lib/backend-prompt.md      #   instructions for the build loop
│   └── gen/                       #   esgen, the code generator (Go module)
├── .claude/skills/                # → .claude/skills/ in the project
│   ├── build-state-change/SKILL.md
│   ├── build-state-view/SKILL.md
│   └── build-automation/SKILL.md
└── root/                          # → the project root: a runnable, empty service
    ├── go.mod, go.sum
    ├── main.go                    #   HTTP server, signal handling
    ├── wiring.go                  #   the backend (SQLite); the only backend-specific file
    ├── app_gen.go                 #   slice registrations (esgen rewrites it)
    └── app_gen_test.go
```

The skills do not ask the agent to write a slice from scratch. Each one runs
**esgen** first — a deterministic generator in `.build-kit/gen`, Go and
`text/template`, no dependencies — which reads the slice's `slice.json` and
writes the code, then has the agent review it and finish what esgen cannot know
(see [What it generates](#what-it-generates)). esgen also runs without the
platform or an agent, on any slice export.

## Install it

The kit is installed from its git repository by the Event Modelers CLI
([docs](https://app.eventmodelers.ai/documentation#build-your-own)):

```bash
mkdir my-service && cd my-service && git init
npx @eventmodelers/cli init --stack go-eventsourcing --git <url-of-this-repo>
# a branch other than the default:
npx @eventmodelers/cli init --stack go-eventsourcing --git <url> --branch <name>
```

`go-eventsourcing` is a local label of your choice (it must not be a built-in
stack name). The CLI shallow-clones the repository, copies `templates/root` into
the project, `templates/build-kit` into `.build-kit/` (plus its own runner), and
`templates/.claude/skills` into `.claude/skills/`, and asks for the board's
credentials. Then:

```bash
go mod edit -module github.com/you/my-service   # name the module before generating code
npx @eventmodelers/cli run                      # start the runner loop
```

and set a slice to `Planned` on the board. `npx @eventmodelers/cli status` shows
what is installed; `npx @eventmodelers/cli uninstall --build-kit` removes
`.build-kit/`.

To pull the board's slices without the loop — for example to run esgen on a
whole context by hand — use `npx @eventmodelers/cli fetch --context <name>`, or
`npx @eventmodelers/cli listen` and the board's **Code → Create config**, which
write `.build-kit/.slices/<context>/<slice>/slice.json`.

## Run esgen by hand

From the project root (the defaults read `.build-kit/.slices` and write to the
project root):

```bash
go -C .build-kit/gen run .                    # every slice of the current context
go -C .build-kit/gen run . -slice additem     # one slice (folder name)
go -C .build-kit/gen run . -h                 # all flags
go mod tidy && go test ./...
```

| Flag | Default | |
|---|---|---|
| `-slices` | `../.slices` | a `.slices` directory, or a JSON export `{"slices": [...]}` |
| `-context` | `current_context.json` | the context to build |
| `-out` | `../..` | the Go module to write into |
| `-module` | from `<out>/go.mod` | the module path |
| `-slice` | all | comma-separated slice folders to write slice files for |
| `-force` | off | replace slice files that already exist |
| `-init` | off | also write `go.mod`, `main.go`, `wiring.go`, `.gitignore` when missing |

`events/` and `app_gen*.go` are rewritten on every run (they carry `DO NOT
EDIT`); slice and automation files are written once and then belong to you.

## What it generates

| Slice type | Files | What is in them |
|---|---|---|
| every event of the context | `events/<event>.go` | the event struct, `AggregateID`, `EventType`, `RegisterEvent` in `init` |
| `STATE_CHANGE` | `slices/<slice>/command.go` | the command (`AggregateID` from the `idAttribute` field, `CommandType`), `state`, `evolve`, `decide`, event constructors, `NewHandler` via `eventsourcing.NewCommandHandler` |
| | `slices/<slice>/command_test.go` | `Test_decide`: one case per specification — given events replayed through `evolve`, `decide` called with the command, events compared as JSON or the rejection checked with `errors.Is` |
| | `slices/<slice>/http.go` | `POST /api/<command>`: JSON body → command bus; 422 on a rule violation |
| `STATE_VIEW` | `slices/<slice>/model.go` | `Row`, its `key` (the `idAttribute` fields), the in-memory read model; `Claim` when an automation works off it |
| | `slices/<slice>/projection.go` | `projection.Definition` with one `eventsourcing.OnEvent` handler per event in an `EventGroupProcessor`, `Version`, `Reset` |
| | `slices/<slice>/query.go`, `http.go` | `Query` (key fields as filters), `QueryHandler`, `GET /api/<read-model>` |
| | `slices/<slice>/projection_test.go` | `TestProjection`: one case per specification — given events through the handler, rows compared |
| `AUTOMATION` | `slices/<slice>/command.go`, `command_test.go` | the command it sends, as for `STATE_CHANGE` (no route) |
| | `automations/<name>/automation.go` | `automation.Definition[<todo>.Row]`: `TodoList` = the todo list's runner, `Claim`, `Work` sending the command |
| | `automations/<name>/automation_test.go` | `TestWork`: a row becomes the right command |
| wiring | `app_gen.go`, `app_gen_test.go` | command bus + handlers, a `projection.Runner` per read model, `automation.New` per automation, routes; `TestApp` boots it all on `testDatabase()` and calls every route |
| `-init` | `main.go`, `wiring.go`, `go.mod` | the server, and the SQLite backend: event store, event bus, projection store from one database |

### How the code follows from the model

- **decide's rules come from the specifications.** `state` counts the events of
  the stream that the specifications mention; each specification becomes a rule
  "after at least these events, the command records these events / is rejected
  with this error"; rules are checked from the most specific history to the least
  specific; the specification with an empty Given is the fallback (without one,
  the command records every event it leads to). esgen then replays every
  specification through that chain. One it cannot reproduce — two specifications
  expecting different outcomes after the same events, or an expected value the
  command does not carry — becomes a test case with `skip: "TODO: …"`, which the
  skills tell the agent to implement before the slice counts as done.
- **Event fields** are copied from the command field with the same name (or the
  field's `mapping`); `generated` fields get `time.Now()`/`uuid.New()`; anything
  else is a `// TODO:`.
- **Read model handlers**: an event carrying every key field adds or updates its
  row; whether it removes the row instead, and whether an event carrying part of
  the key (e.g. `CartCleared` for cart items) removes the matching rows, is
  decided by which choice satisfies the view's specifications. For a todo list,
  the events the automation's command records remove rows.
- Slice code depends only on the library's interfaces; `sqlite` appears in
  `wiring.go` alone. To move to Postgres or KurrentDB, see
  [docs/how-to/switch-backend.md](docs/how-to/switch-backend.md).

### Board export variants esgen accepts

Besides EM Spec ([schema](https://github.com/dilgerma/event-modeling-spec)),
esgen reads what real board exports contain: step types `EVENT`/`COMMAND`/
`READMODEL` as well as `SPEC_EVENT`/…, a rejection written as a single `then`
object instead of an array, step fields without a `type` (taken from the
element), the flat `.slices/<folder>/slice.json` layout of older exports, and a
whole-board JSON export (`-slices export.json -context <name>`).

## Example

[`examples/cart`](examples/cart) is a shopping cart modeled on the documentation's
examples: four state changes (Add Item, Remove Item, Clear Cart, Submit Cart),
two views (Cart Items, Carts To Publish) and an automation (publish submitted
carts), with 17 specifications. `model/` is the input as the CLI writes it,
`app/` what esgen generates from it — untouched, all 17 specifications pass.

```bash
cd examples/cart/app
go test ./...
go run .   # :8080, events.db
curl -XPOST localhost:8080/api/add-item -d '{"cartId":"5f6c4bde-4e2f-4a37-9a2c-1c7f0d8b9a01","itemId":"0b7e3d2a-6a4f-4c1e-8f3b-2d9c5e7a1b11","productId":"9a1d7c55-3e2b-4f6a-b8c4-7e5d3a2f1c01","description":"Beans","price":12.5}'
curl localhost:8080/api/cart-items
curl -XPOST localhost:8080/api/submit-cart -d '{"cartId":"5f6c4bde-4e2f-4a37-9a2c-1c7f0d8b9a01"}'
curl localhost:8080/api/carts-to-publish   # empty again once the automation published the cart
```

## Developing the kit

```bash
./scripts/regenerate.sh
```

regenerates `examples/cart/app` and `templates/root` with esgen, runs
`go mod tidy`, `gofmt -l`, `go vet`, `go build` and `go test` on them, and runs
esgen's own tests — which fail when a checked-in file differs from what esgen
generates. Change a template, run the script, commit the template and the
regenerated files together.
