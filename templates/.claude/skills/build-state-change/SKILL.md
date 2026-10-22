---
name: build-state-change
description: Build a STATE_CHANGE slice for a Go service on github.com/terraskye/eventsourcing — the command, a state folded from the stream by a pure evolve, a pure decide holding the business rules, NewCommandHandler, a POST route, and one table-driven test case per Given/When/Then specification. Scaffolded by esgen, finished by hand.
---

# Build State Change Slice

> First step, always: read the slice definition from `.slices/{Context}/{slicename}/slice.json` before doing anything else.
> (The `.slices` folder is `.build-kit/.slices`.) It is the source of truth for every
> field, event and rule — never invent one that is not there.

## What a state change slice is

A command arrives, the handler loads the command's stream from the event store,
folds every event into a small state with `evolve`, asks `decide` for the events
to record, and appends them. `eventsourcing.NewCommandHandler` does the loading,
folding and appending; the slice owns three pure pieces:

```
state      what decide needs to know about the stream
evolve     (state, *eventsourcing.Envelope) → state      pure, no errors
decide     (state, Command) → ([]eventsourcing.Event, error)   pure; an error rejects the command
```

## Step 1 — Read the slice.json

From the slice definition, take:
- **title** and **folder** — the folder is the Go package (`slices/<folder>`)
- **commands[0]** — the command and its `fields` (an `idAttribute` field is the stream)
- **events[]** — the events the command leads to (`dependencies` OUTBOUND)
- **specifications[]** — given (events), when (the command), then (events, or a
  `SPEC_ERROR` rejection)
- **codeGen.backendPrompt** / **description** / **comments** — implementation hints

> Comments and descriptions: elements carry `comments` and a `description`; use them
> as hints, and resolve a comment you acted on via
> `POST <BASE_URL>/api/org/<ORG_ID>/boards/<BOARD_ID>/nodes/<nodeId>/comments/<commentId>/resolve`.

## Step 2 — Scaffold with esgen

New slice (no `slices/<folder>/` yet):

```
go -C .build-kit/gen run . -slice <folder>
```

It writes `events/*.go` (every event of the context, registered in `init` with
`eventsourcing.RegisterEvent`), `slices/<folder>/{command.go,command_test.go,http.go}`,
and `app_gen.go`, which registers the handler on the command bus and the route on
the mux. Read the `note:` lines it prints.

Changed slice (the folder exists and has hand-written rules): scaffold into a
scratch directory and port the differences — new fields, events, specifications —
by hand:

```
tmp=$(mktemp -d); cp go.mod "$tmp"/
go -C .build-kit/gen run . -slice <folder> -out "$tmp"
diff -ru slices/<folder> "$tmp/slices/<folder>"
```

Use `-force` instead only when `git diff slices/<folder>` shows no hand edits.

## Step 3 — Review what esgen derived

**Command** — one struct, fields in model order, JSON tags with the model's names.
`AggregateID()` returns the `idAttribute` field (else `aggregateId`/`id`, else the
first UUID); every command of one aggregate must return the same id, so that they
share a stream. `CommandType()` returns the Go name.

**Events** (`events/`, never edit) — one struct per event with
`AggregateID()`/`EventType()`. A field marked `generated` on the board is filled
in by `decide` (`time.Now()`, `uuid.New()`).

**decide's rules** come from the specifications. esgen keeps a count per event type
in `state`, turns each specification into a rule "after at least these events, the
command records these events / is rejected with this error", and checks the rules
from the most specific history to the least specific one. The specification with
an empty Given is the fallback; without one the command records every event it
leads to. Rejections are package variables:

```go
var errCartIsFull = errors.New("cart is full")

func decide(s state, cmd AddItem) ([]eventsourcing.Event, error) {
	// A cart holds at most three items
	if s.cartCreated > 0 && s.itemAdded >= 3 {
		return nil, errCartIsFull
	}
	// Adding an item to an existing cart
	if s.cartCreated > 0 && s.itemAdded > 0 {
		return []eventsourcing.Event{itemAdded(cmd)}, nil
	}
	// Adding the first item creates the cart
	return []eventsourcing.Event{cartCreated(cmd), itemAdded(cmd)}, nil
}
```

Counting events is a start, not the domain. Replace a count with real state
whenever the rule is about *which* thing, not *how many*: "item not in cart" wants
the set of item IDs (add on `ItemAdded`, remove on `ItemRemoved`), "already
submitted" wants a bool. Keep every case of the test table passing while you do.

**Event constructors** (`itemAdded(cmd)`) copy each event field from the command
field of the same name (or the field's `mapping`). A field the command does not
carry is a `// TODO:` — derive it from the state or the command.

## Step 4 — Business rules

- `evolve` must stay pure: no I/O, no clock, no errors.
- `decide` must stay pure: everything it needs comes from the state and the command.
  A value from elsewhere (a price, a reservation) belongs on the command.
- Return a plain error to reject: `NewCommandHandler` wraps it in
  `eventsourcing.BusinessRuleViolationError`, and `http.go` answers 422 with it.
  Never wrap it yourself.
- Return `nil, nil` when the work is already done (idempotent commands, e.g. an
  automation retrying): the handler then records nothing.
- Need concurrency guarantees beyond the default (`Any`, retried on conflict)?
  Pass `eventsourcing.WithStreamState(eventsourcing.NoStream{})` (create only) or
  `StreamExists{}` (update only) to `NewCommandHandler` in `NewHandler`.
- No `sqlite`, `postgres` or `kurrentdb` import in a slice. Ever.

## Step 5 — Specifications

`command_test.go` holds `Test_decide`: one case per specification, named
`spec: <Slice> - <Specification>`, with the given events as `history`, the command
as `cmd`, and the expected events (`want`) or rejection (`wantErr`). The test
replays the history through `evolve`, calls `decide`, and compares the events as
JSON, with `generated` fields cleared.

A case with `skip: "TODO: …"` is a specification esgen could not reproduce: two
specifications that expect different outcomes after the same events, or an event
field the command does not carry. Implement the rule, then delete the `skip`. Add a
case for every specification that slice.json gained since the file was generated.
The slice is not done while a `skip` remains.

## Quality gate

From the project root:

```
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./slices/<folder>/... .
```

`go test .` runs `TestApp`, which posts the first specification's command to the
route and expects 200 or 422.

## UI Contract

**Only when handover is on** (the task's `Handover: on` line, else
`"handover": true` in `.eventmodelers/config.json`): once the route works, invoke
the `ui-contract` skill (write contract, `POST`). Source for this stack: the route
in `slices/<folder>/http.go` (`POST <path>`), the request body is the command
struct's JSON, the response `{"version": n, "globalPosition": n}`, a rejection 422
`{"error": "..."}`.
