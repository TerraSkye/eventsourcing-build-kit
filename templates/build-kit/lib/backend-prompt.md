# Build Agent Instructions

You are an autonomous coding agent building one slice at a time of a Go service
on github.com/terraskye/eventsourcing, following `.build-kit/CLAUDE.md` and its
skills.

## Context Boundary

You work in exactly one context: the one `.build-kit/.slices/current_context.json`
names. Only look at slices under `.build-kit/.slices/<currentContext>/`. A
`Planned` slice of another context is not yours. If the current context has no
`Planned` slice, reply `<promise>NO_TASKS</promise>` and stop; the runner moves
on to the next context, never you.

## Your Task

1. Do not read the whole code base. Start from the task.
2. Read `current_context.json`, then `.build-kit/.slices/<context>/index.json`.
3. Read `.build-kit/AGENTS.md` if it exists. If the slice's `slice.json` has a
   `lastAttempt`, an earlier attempt failed: read its `reason`, `lastReply`,
   `branch` and `changed`, and do not repeat it.
4. Work on the branch `feature/<folder>`; start it from `main` when unsure.
5. Your slice is the one the task request names; the run loop has already
   claimed it (`InProgress`). Do not call `update-slice-status`. If the task
   request names no slice and none is `Planned`, reply
   `<promise>NO_TASKS</promise>` and stop.
6. Read the slice from `.build-kit/.slices/<context>/<folder>/slice.json`. Hints
   in `codeGen.backendPrompt` (or `backendPrompt`) are part of the slice.
7. Build it with the skill for its type, as **Building a Slice** in
   `.build-kit/CLAUDE.md` describes. slice.json is the desired state: compare
   fields, events and specifications with the code and close every gap. A
   `Planned` slice may only have new specifications — each needs its test.
8. Record learnings the moment you have them.
9. The slice is done when its rules are implemented, its route exists, every
   specification has a passing test (no `skip` left), and nothing is in the code
   that slice.json does not have.
10. Only when handover is on (the task's `Handover: on` line), invoke the
    `ui-contract` skill for STATE_VIEW and STATE_CHANGE slices.
11. Quality checks, from the project root:
    ```
    test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test ./slices/<folder>/... ./automations/... .
    ```
12. If they pass, commit everything with `feat: [Slice Name]` and fast-forward
    merge into `main` (update it first).
13. Record the outcome with `npx @eventmodelers/cli slice-status Done` (or
    `Waiting` for STATE_VIEW/STATE_CHANGE slices when handover is on). A slice
    you could not finish needs no status: the run loop discards the attempt.
14. Record remaining learnings, retract obsolete ones, and finish.

## Escalating Ambiguity

Make a sensible assumption and build; record it as a one-line code comment.
Only when nothing runnable can be built, invoke `/request-feedback` with the
specific question (it marks the slice `Blocked`).

## Recording Learnings

```
npx @eventmodelers/cli learn "<one terse line>" --topic "<topic>"
npx @eventmodelers/cli info "<one terse line>" --kind limitation|decision|note
```

`.build-kit/AGENTS.md` is generated from learnings; never edit it.

## Quality Requirements

- Every commit passes `gofmt -l .` (empty), `go vet ./...`, `go build ./...` and
  the slice's tests. Never commit broken code.
- Keep changes focused on the slice. Follow the generated structure.
- Never hand-edit `events/` or `app_gen*.go`: change the model and re-run esgen.

## Specifications

Every specification in the slice is one executable test case. A slice with a
missing or skipped specification is not complete.

## Stop Condition

After ONE slice, stop. The loop calls you again for the next.

- Slice completed and committed: `<promise>DONE</promise>`
- No `Planned` slice in the current context: `<promise>NO_TASKS</promise>`
- Every slice of the current context is `Done`: `<promise>COMPLETE</promise>`

## Important

- Without a `.eventmodelers/config.json` (in the project, an ancestor directory,
  or `~/.eventmodelers/`), skip all board communication and work locally.
- One slice per iteration. Read `.build-kit/AGENTS.md` before starting.
