---
name: build-automation
description: Build an AUTOMATION slice for a Go service on github.com/terraskye/eventsourcing — an automation.Definition that claims rows from a todo-list read model and sends a command for each, plus the slice's own command (built like a state change slice). Scaffolded by esgen, finished by hand.
---

# Build Automation Slice

> First step, always: read the slice definition from `.slices/{Context}/{slicename}/slice.json` before doing anything else.
> (The `.slices` folder is `.build-kit/.slices`.) It is the source of truth for every
> field, event and rule — never invent one that is not there.

## What an automation slice is

In Event Modeling an automation looks at a todo list (a read model of pending
work), does the work, and records the outcome by sending a command. The resulting
event removes the item from the todo list, which closes the loop:

```
CartSubmitted ──► carts to publish (todo list) ──► publish-submitted-carts automation
      ▲                                                     │
      └──────────── CartPublished ◄──── PublishCart ◄────────┘
```

The library's `automation` package runs it: it claims items only while the todo
list's projection is live, leases them, calls `Work` for each, and retries an item
whose work failed once its lease runs out. The automation never reacts to events
directly.

## Step 1 — Read the slice.json

From the slice definition, take:
- **processors[0]** — the automation: its INBOUND `READMODEL` dependency is the
  todo list (a STATE_VIEW slice of its own), its OUTBOUND `COMMAND` the command
- **commands[0]** and **events[]** — the command it sends and what it records
- **specifications[]** — scenarios for the command
- **codeGen.backendPrompt** / **description** / **comments** — hints, e.g. which
  external system the work calls

> Comments and descriptions: elements carry `comments` and a `description`; use them
> as hints, and resolve a comment you acted on via
> `POST <BASE_URL>/api/org/<ORG_ID>/boards/<BOARD_ID>/nodes/<nodeId>/comments/<commentId>/resolve`.

The todo list must exist before the automation: if its STATE_VIEW slice is not
built yet, build it first with `/build-state-view` (in the same iteration only when
the task request names this slice; otherwise note it with
`npx @eventmodelers/cli info "..." --kind limitation`).

## Step 2 — Scaffold with esgen

```
go -C .build-kit/gen run . -slice <folder>
```

It writes the command like a state change slice
(`slices/<folder>/{command.go,command_test.go}`, no HTTP route), the automation
(`automations/<name>/{automation.go,automation_test.go}`, `<name>` from the
processor's title), gives the todo list's read model a `Claim` method (in
`slices/<todo>/model.go`, when esgen writes that slice), and wires the automation
in `app_gen.go`:

```go
publishSubmittedCartsAutomation := publishsubmittedcarts.NewAutomation(cartsToPublishModel, cartsToPublishRunner, publishCartHandler)
a.background = append(a.background, automation.New(publishSubmittedCartsAutomation).Run)
```

## Step 3 — The command

Finish `slices/<folder>/command.go` as in `/build-state-change`, Steps 3–5. It
must be **idempotent**: the automation delivers work at least once, so a command
for work already recorded returns `nil, nil` (no events). Give it a specification
("Publishing again records nothing") if the board has none, and note that with
`info`.

## Step 4 — The automation

`automation.go` returns an `automation.Definition[<todo>.Row]`:

```go
return automation.Definition[cartstopublish.Row]{
	Name:     "publish-submitted-carts",
	TodoList: todoList,   // the todo list's *projection.Runner (automation.Dependency)
	Claim:    list.Claim, // leases pending rows
	Work: func(ctx context.Context, item cartstopublish.Row) error {
		_, err := handler(ctx, publishcart.PublishCart{CartID: item.CartID})
		return err
	},
}
```

- `Work` fills the command from the row by field name; a command field the row
  lacks is a `// TODO:` — add it to the todo list on the board, or look it up.
- Work that calls an external system goes in `Work`, before the command, with the
  row's ID as idempotency key. Give the call a deadline well below the lease
  (`Lease`, default one minute).
- Work that fails for good should record that as an event (a "…Failed" command),
  so the row leaves the todo list; there is no attempt limit.
- The todo list's `Claim` leases rows in memory and the rows leave the list when
  the command's events arrive (esgen wires those events to remove rows). Check
  that `projection_test.go` of the todo list covers it.

## Step 5 — Tests

- `slices/<folder>/command_test.go` — the command's specifications, as in
  `/build-state-change`.
- `automations/<name>/automation_test.go` — `TestWork` checks that a row turns
  into the right command. Extend it when `Work` does more than map fields.

## Quality gate

From the project root:

```
test -z "$(gofmt -l .)"
go vet ./...
go build ./...
go test ./slices/<folder>/... ./automations/... .
```
