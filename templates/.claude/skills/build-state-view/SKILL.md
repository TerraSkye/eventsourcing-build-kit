---
name: build-state-view
description: Build a STATE_VIEW slice for a Go service on github.com/terraskye/eventsourcing — an in-memory read model, a projection.Definition with one eventsourcing.OnEvent handler per event (grouped in an EventGroupProcessor), a query handler, a GET route, and one table-driven test case per Given/Then specification. Scaffolded by esgen, finished by hand.
---

# Build State View Slice

> First step, always: read the slice definition from `.slices/{Context}/{slicename}/slice.json` before doing anything else.
> (The `.slices` folder is `.build-kit/.slices`.) It is the source of truth for every
> field and event — never invent one that is not there.

## What a state view slice is

Events written by other slices flow into a read model that is cheap to query.
`projection.Runner` reads the global log, routes every event in the projection's
`EventTypes` to its handler in log order, and remembers how far it got:

```
events (global log) ──► projection.Runner ──► Handler (OnEvent per event type) ──► read model ──► QueryHandler ──► GET route
```

## Step 1 — Read the slice.json

From the slice definition, take:
- **title** and **folder** — the folder is the Go package (`slices/<folder>`)
- **readmodels[0]** — its `fields` (the `idAttribute` fields key the rows) and its
  INBOUND `EVENT` dependencies (the events that feed it)
- **specifications[]** — given (events), then (the read model: `fields` with
  examples for one row, `examples` for several, `expectEmptyList` for none)
- **codeGen.backendPrompt** / **description** / **comments** — implementation hints

> Comments and descriptions: elements carry `comments` and a `description`; use them
> as hints, and resolve a comment you acted on via
> `POST <BASE_URL>/api/org/<ORG_ID>/boards/<BOARD_ID>/nodes/<nodeId>/comments/<commentId>/resolve`.

## Step 2 — Scaffold with esgen

```
go -C .build-kit/gen run . -slice <folder>
```

It writes `slices/<folder>/{model.go,projection.go,query.go,http.go,projection_test.go}`
and adds a projection runner and the route to `app_gen.go`. For a changed slice,
scaffold into a scratch directory and port the differences (see
`build-state-change`, Step 2).

## Step 3 — Review what esgen derived

**model.go** — `Row` (the read model's fields), `key` (its `idAttribute` fields),
and the read model itself: rows in a map behind a `sync.RWMutex`. A read model an
automation works off also gets `Claim` (see `build-automation`).

**projection.go** — `NewProjection` returns a `projection.Definition`:

```go
handlers := eventsourcing.NewEventGroupProcessor(
	eventsourcing.OnEvent(m.onItemAdded),
	eventsourcing.OnEvent(m.onItemRemoved),
)
return projection.Definition{
	Name:       "cart-items",
	Handler:    handlers,
	EventTypes: handlers.StreamFilter(),
	Version:    1,
	Reset:      m.reset,
}
```

What each handler does follows from the fields and the specifications:
- an event carrying every key field **adds or updates** its row, copying the fields
  it shares with `Row` (same name, or the field's `mapping`);
- an event carrying the key **removes** the row instead when the specifications say
  so (e.g. `ItemRemoved`);
- an event carrying only part of the key removes every matching row when the
  specifications say so (e.g. `CartCleared` removes all items of the cart);
- an event carrying no key field is not handled; `NewProjection`'s comment names
  it. Handle it by hand if it should change rows.

Totals, counters and status changes are not derived: add them to the handlers.

**query.go** — `Query` has the key fields; a zero field matches every row.
`HandleQuery` returns the matching rows ordered by key. The route
(`GET /api/<read-model>`) takes the key fields as query parameters.

## Step 4 — Rules for projections

- Handlers apply one event to the read model and nothing else: no commands, no
  calls to other systems (those belong in an automation — at-least-once side
  effects in a projection repeat on every retry and rebuild).
- When you change how a handler writes rows, bump `Version`: the runner rebuilds
  the read model (through `reset`) on the next start. Adding an event type does
  this automatically.
- The read model lives in memory, so `app_gen.go` gives its runner in-memory
  checkpoints and it replays the log on every start. To keep a read model in the
  database instead, write its rows through the store's transaction (see the
  library's `projection` package docs); that is backend code, so keep it behind an
  interface the slice owns and implement it in `wiring.go`.
- A handler that cannot apply an event returns an error: the runner stalls on it
  and retries. Return `eventsourcing.SkippedEventError` to decline an event on
  purpose.

## Step 5 — Specifications

`projection_test.go` holds `TestProjection`: one case per specification, named
`spec: <Slice> - <Specification>`, with the given events and the expected rows.
The test feeds the events to the projection's handler, queries all rows, and
compares them as JSON.

A case with `skip: "TODO: …"` is a specification the generated handlers do not
satisfy — usually a total, a status, or a removal esgen could not infer. Fix the
handlers, then delete the `skip`. The slice is not done while a `skip` remains.

## Quality gate

From the project root:

```
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./slices/<folder>/... .
```

## UI Contract

**Only when handover is on** (the task's `Handover: on` line, else
`"handover": true` in `.eventmodelers/config.json`): once the route works, invoke
the `ui-contract` skill (read contract, `GET`). Source for this stack: the route in
`slices/<folder>/http.go` (`GET <path>`, key fields as query parameters), the
response is a JSON array of `Row`.
