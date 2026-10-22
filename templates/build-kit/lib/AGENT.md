# Agent Reference

What processing a task relies on: the shape of a task, where slices live, and
the board facts the skills use. Reusable learnings are recorded with
`npx @eventmodelers/cli learn "<one terse line>"`, not here.

## Tasks

- A task is a slice change the run loop queued: an `id` and a `payload` (a
  `SliceChangedPayload`). The task request names it.
- You never read or write a queue file: the run loop completes the task when
  your turn ends.

## SliceChangedPayload

```
event           always "slice:changed"
organizationId  org UUID or null
boardId         board UUID
sliceId         SLICE_BORDER node UUID — pass it to /load-slice
sliceTitle      the slice's name (may be null)
sliceStatus     e.g. "Created", "Planned", "InProgress", "Done", "Blocked" (may be null)
timestamp       unix ms of the change
```

## Slice files

```
.build-kit/.slices/current_context.json          {"name": "<context>"}
.build-kit/.slices/<context>/index.json           every slice: id, title, folder, status
.build-kit/.slices/<context>/<folder>/slice.json  one slice
```

`<folder>` is the slice title in lower case without spaces (`Add Item` →
`additem`); it is also the Go package under `slices/`.

## esgen

```
go -C .build-kit/gen run . -slice <folder>             # scaffold one slice (+ events, app_gen.go)
go -C .build-kit/gen run . -slice <folder> -force      # replace the slice's files
go -C .build-kit/gen run . -slice <folder> -out "$tmp" -module "$(go list -m)"
                                                       # scaffold into a scratch dir to diff against
go -C .build-kit/gen run . -h                          # all flags
```

esgen always rewrites `events/` and `app_gen*.go`; it writes slice and
automation files only when they do not exist yet (or with `-force`). It prints
a `note:` line for every part of the model it could not turn into code.

## Skill Usage

- Run `/connect` first to load the credentials from `.eventmodelers/config.json`.
- `/load-slice sliceId=<uuid>` fetches the slices again, refreshes the slice
  files, and returns the requested slice.
- Read the slice files directly when you know the context and folder and the
  file is recent.

## Board API

- Each payload's `boardId` and `organizationId` give the full context: pass them
  to the skills.
- Node events are `node:created`, `node:changed`, `node:deleted`, posted to
  `/api/org/:orgId/boards/:boardId/nodes/events`.
- A slice's title and status live on its SLICE_BORDER node under `meta.title`
  and `meta.sliceStatus`.
- `/update-slice-status` refuses to set the status a slice already has: another
  agent claimed it. Treat that as `ALREADY_IN_STATUS`, skip the slice, and take
  the next `Planned` one. This applies to queued tasks only; when the task
  request names the slice, the run loop owns its status.
