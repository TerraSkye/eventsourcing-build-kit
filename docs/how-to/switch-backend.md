# How to switch a generated project from SQLite to Postgres or KurrentDB

A project from this kit starts on SQLite: one database file holds the event
store, the event bus's subscription positions and the projection store, and
`go run .` needs no server. Every backend-specific line lives in `wiring.go`;
the slices, their tests and `app_gen.go` use the library's interfaces only:

| `infra` field | Interface | SQLite (default) | Postgres | KurrentDB |
|---|---|---|---|---|
| `Events` | `eventsourcing.EventStore` + `eventsourcing.GlobalReader` | `sqlite.NewEventStore` | `postgres.NewEventStore` | `kurrentdb.NewEventStore` |
| `Bus` | `eventsourcing.EventBus` | `sqlite.NewEventBus` | `postgres.NewEventBus` | `kurrentdb.NewEventBus` |
| `Projections` | `projection.Store` | `sqlite.NewProjectionStore` | `postgres.NewProjectionStore` | — none: use Postgres or SQLite |

So switching is: replace `wiring.go`, change `go.mod`, set up the schema, and
(if there is data) copy the events over. Nothing else changes, and esgen never
touches `wiring.go`.

The generated read models live in memory and replay the log on every start, so
`app_gen.go` gives their runners in-memory checkpoints whatever the backend;
`infra.Projections` is for read models you move into the database yourself.

## Postgres

1. Add the module and drop SQLite:

   ```bash
   go get github.com/terraskye/eventsourcing/postgres@v0.2.0
   ```

2. Replace `wiring.go` with:

   ```go
   // wiring.go is the only file that knows the backend: Postgres, one
   // database for the event store, the event bus and the projection store.

   package main

   import (
   	"context"
   	"os"
   	"time"

   	"github.com/jackc/pgx/v5/pgxpool"
   	"github.com/terraskye/eventsourcing"
   	"github.com/terraskye/eventsourcing/postgres"
   	"github.com/terraskye/eventsourcing/projection"
   )

   // eventStore is what the slices need from the event store: streams for the
   // command handlers, and the global log for the projection runners.
   type eventStore interface {
   	eventsourcing.EventStore
   	eventsourcing.GlobalReader
   }

   // infra is the backend.
   type infra struct {
   	Events      eventStore
   	Bus         eventsourcing.EventBus
   	Projections projection.Store

   	close func() error
   }

   // Close stops the event bus and closes the pool.
   func (i *infra) Close() error { return i.close() }

   // testDatabase is the database TestApp runs against, from
   // TEST_DATABASE_URL; TestApp is skipped without one. Point it at a
   // disposable database: TestApp writes events.
   func testDatabase() string { return os.Getenv("TEST_DATABASE_URL") }

   // openInfra connects to the Postgres database at dsn (for example
   // "postgres://app:app@localhost:5432/app"), creates its tables, and builds
   // the backend on it.
   func openInfra(ctx context.Context, dsn string) (*infra, error) {
   	pool, err := pgxpool.New(ctx, dsn)
   	if err != nil {
   		return nil, err
   	}
   	if _, err := pool.Exec(ctx, postgres.Schema); err != nil {
   		pool.Close()
   		return nil, err
   	}
   	bus := postgres.NewEventBus(pool, 200*time.Millisecond)
   	return &infra{
   		Events:      postgres.NewEventStore(pool),
   		Bus:         bus,
   		Projections: postgres.NewProjectionStore(pool),
   		close: func() error {
   			err := bus.Close()
   			pool.Close()
   			return err
   		},
   	}, nil
   }
   ```

3. Tidy, so `go.mod` gains `jackc/pgx/v5` and loses the SQLite modules:

   ```bash
   go mod tidy
   ```

4. Schema: `postgres.Schema` creates the events, subscription, projection
   segment and dead-letter tables and is idempotent, so `openInfra` applies it on
   every start. To run it with your migrations instead, take the same SQL from
   `schema.sql` in the library's
   [postgres package](https://github.com/TerraSkye/eventsourcing/tree/v0.2.0/postgres)
   and drop the `Exec` above; the tables are described in the
   [Postgres store reference](https://github.com/TerraSkye/eventsourcing/blob/v0.2.0/docs/reference/implementations/postgres-store.md).

5. Run it: `main.go` passes `DATABASE` to `openInfra`.

   ```bash
   docker run -d --name pg -e POSTGRES_USER=app -e POSTGRES_PASSWORD=app -e POSTGRES_DB=app -p 5432:5432 postgres:16
   DATABASE=postgres://app:app@localhost:5432/app go run .
   TEST_DATABASE_URL=postgres://app:app@localhost:5432/app go test ./...
   ```

## KurrentDB (+ a projection store)

KurrentDB keeps the events and delivers them to subscribers; it has no
projection store, so pair it with SQLite (below) or Postgres (take
`postgres.NewProjectionStore` from the Postgres wiring) for checkpoints.

1. Add the module:

   ```bash
   go get github.com/terraskye/eventsourcing/kurrentdb@v0.2.0
   ```

2. Replace `wiring.go` with:

   ```go
   // wiring.go is the only file that knows the backend: KurrentDB for the
   // events and the event bus, and SQLite for the projection store, which
   // KurrentDB does not provide.

   package main

   import (
   	"context"
   	"errors"
   	"os"

   	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
   	"github.com/terraskye/eventsourcing"
   	kdb "github.com/terraskye/eventsourcing/kurrentdb"
   	"github.com/terraskye/eventsourcing/projection"
   	"github.com/terraskye/eventsourcing/sqlite"
   )

   // eventStore is what the slices need from the event store: streams for the
   // command handlers, and the global log for the projection runners.
   type eventStore interface {
   	eventsourcing.EventStore
   	eventsourcing.GlobalReader
   }

   // infra is the backend.
   type infra struct {
   	Events      eventStore
   	Bus         eventsourcing.EventBus
   	Projections projection.Store

   	close func() error
   }

   // Close stops the event bus and closes the connections.
   func (i *infra) Close() error { return i.close() }

   // testDatabase is the KurrentDB TestApp runs against, from
   // TEST_KURRENTDB_URL; TestApp is skipped without one. Point it at a
   // disposable instance: TestApp writes events.
   func testDatabase() string { return os.Getenv("TEST_KURRENTDB_URL") }

   // openInfra connects to KurrentDB at dsn (for example
   // "esdb://localhost:2113?tls=false") and opens the SQLite database named by
   // PROJECTIONS_DATABASE (default projections.db) for the projection store.
   func openInfra(ctx context.Context, dsn string) (*infra, error) {
   	settings, err := kurrentdb.ParseConnectionString(dsn)
   	if err != nil {
   		return nil, err
   	}
   	client, err := kurrentdb.NewClient(settings)
   	if err != nil {
   		return nil, err
   	}
   	path := os.Getenv("PROJECTIONS_DATABASE")
   	if path == "" {
   		path = "projections.db"
   	}
   	db, err := sqlite.Open(path)
   	if err != nil {
   		client.Close()
   		return nil, err
   	}
   	if _, err := db.ExecContext(ctx, sqlite.Schema); err != nil {
   		db.Close()
   		client.Close()
   		return nil, err
   	}
   	store := kdb.NewEventStore(client)
   	bus := kdb.NewEventBus(client, 256)
   	return &infra{
   		Events:      store,
   		Bus:         bus,
   		Projections: sqlite.NewProjectionStore(db),
   		close: func() error {
   			return errors.Join(bus.Close(), store.Close(), db.Close(), client.Close())
   		},
   	}, nil
   }
   ```

3. `go mod tidy` adds `kurrent-io/KurrentDB-Client-Go` and keeps
   `eventsourcing/sqlite` for the projection store.

4. Schema: KurrentDB needs none. `sqlite.Schema` creates the projection tables
   (it also creates the SQLite event tables, which stay empty); see the
   [SQLite reference](https://github.com/TerraSkye/eventsourcing/blob/v0.2.0/docs/reference/implementations/sqlite.md).
   KurrentDB serializes events as JSON by their registered name: the generated
   `events` package registers every event in `init`, and `app_gen.go` imports it.
   See also the library's
   [KurrentDB how-to](https://github.com/TerraSkye/eventsourcing/blob/v0.2.0/docs/how-to/use-kurrentdb.md).

5. Run it:

   ```bash
   docker run -d --name kurrentdb -p 2113:2113 -e KURRENTDB_INSECURE=true kurrent/kurrentdb:latest --insecure
   DATABASE='esdb://localhost:2113?tls=false' go run .
   ```

## Keeping the events you already have

`sqlite.Import(ctx, src, dst)` copies every event of any `eventsourcing.GlobalReader`
into a SQLite event store, keeping each event's ID, stream, order within its
stream, metadata and time. That moves a log *into* SQLite (for example from a
`memory` store or an older file store); run it from a program that imports the
`events` package, since reading decodes every event.

The reverse — out of SQLite into Postgres or KurrentDB — has no helper in v0.2.0.
Copy it with the same loop `Import` uses: page through `src.ReadAll(ctx, after,
eventsourcing.ReadOptions{})`, group consecutive envelopes by `StreamID`, and
`dst.Save` each group with `eventsourcing.Revision(version - 1)` of its first
envelope, so a conflict stops the copy instead of reordering a stream. Start
from an empty destination and stop the service while copying. Global positions
are assigned anew by the destination; the in-memory read models replay anyway.

## Check it

The kit's `examples/cart/app` was switched with exactly these files:

```bash
cp -r examples/cart/app /tmp/cart-pg && cd /tmp/cart-pg
# replace wiring.go with the Postgres version above
go get github.com/terraskye/eventsourcing/postgres@v0.2.0 && go mod tidy
go vet ./... && go build ./...
TEST_DATABASE_URL=postgres://app:app@localhost:5432/app go test ./...
```
