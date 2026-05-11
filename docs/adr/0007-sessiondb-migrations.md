# ADR 7: In-house Versioned Migrations for sessiondb (Defer golang-migrate)

## Status: Accepted (2026-05-11)

## Context

`pkg/sessiondb` is the FTS5-backed SQLite index for session search.
Until R3 it had no migration system at all — `Open` would:

1. Execute the embedded `schema` constant (full DDL with `CREATE
   ... IF NOT EXISTS`).
2. Then *blindly* attempt `ALTER TABLE messages ADD COLUMN hash …`
   and `ALTER TABLE messages ADD COLUMN pos …`.
3. Swallow the error if the message contained `"duplicate column"`.

Two real problems with that pattern:

- **Error-string sniffing is fragile.** Any change in the
  glebarez/go-sqlite driver's error wording silently turns a benign
  case into a process-killing failure (or vice versa).
- **No bookkeeping.** We can't tell whether an existing database has
  ever been touched by the new schema, so future migrations can't
  rely on "is migration N applied?" — they have to re-discover state
  every boot.

The R4 plan called for adopting `golang-migrate/migrate` as a
"proper" migration framework. We decided not to.

## Decision

### Build a tiny in-house versioned migration runner

`pkg/sessiondb/migrate.go` adds:

- A `migration` struct (`version int`, `name string`, `sql string`,
  optional `guard func(*sql.DB) (skip bool, error)`).
- A bookkeeping table `schema_migrations(version, name, applied_at)`.
- A `runMigrations` driver that applies any unapplied migration in
  one transaction per version, recording the version on commit.
- A baseline-detection special case: when the bookkeeping table is
  empty *and* `messages.hash` already exists (a database opened by
  the pre-migration binary), record migrations 1+2 as applied and
  skip them.
- A `splitStatements` helper that handles SQLite trigger
  `BEGIN ... END;` blocks correctly when slicing multi-statement
  SQL.

The whole runner is ~180 LOC including tests.

### Deferred: golang-migrate

We chose **not** to pull in `golang-migrate/migrate` because:

- **Dependency surface.** The driver pulls in 8+ transitive
  packages. Our schema is two tables, one virtual FTS5 table, three
  triggers, and two indices — pinned for the foreseeable future.
- **No deployment-side migration tool needed.** sessiondb is
  embedded inside the saker binary; there is no operator-run
  `migrate up` workflow, no separate migration container, no CI job
  to run migrations against staging. golang-migrate's CLI value
  proposition does not apply.
- **No multi-database support needed.** sessiondb is SQLite-only.
  We don't need cross-engine migration files, sql/postgres-aware
  syntax, or pgx connection strings.
- **Stricter testing for less code.** Forty lines of test cover
  every branch of the in-house runner (fresh DB, idempotent reopen,
  baseline detection, statement splitting, column existence). The
  golang-migrate equivalent would need integration-test plumbing
  for the migrate CLI surface that we'd never use.

### Migration file format

Migrations live as Go structs in `migrate.go`, not as filesystem
files:

```go
{version: 1, name: "initial_schema", sql: schema}
{version: 2, name: "messages_hash_pos", sql: alterStmts, guard: ...}
```

This is fine because:

- Schema changes are infrequent (two changes in two years of
  sessiondb existence).
- Migrations need to ship inside the saker binary anyway (no
  external CLI exists), so embedding via `embed.FS` would buy
  nothing over a Go literal.
- Code review of a migration is just a code review of a struct
  literal — no separate file convention to learn.

When the migration list passes ~10 entries, revisit moving to
`embed.FS` of `*.sql` files. Until then, Go struct literals are
shorter and easier to test.

## Consequences

### Positive

- **No more error-string sniffing.** Migration #2 has an explicit
  guard that probes the actual schema via `pragma_table_info`.
- **Visible bookkeeping.** Operators can inspect
  `schema_migrations` to know what's been applied; previously
  state was inferred from column existence at boot.
- **Idempotent and safe.** Repeated `Open()` calls (e.g. test
  reuse) re-run `runMigrations` cheaply: the
  `schema_migrations` lookup short-circuits each version.
- **Backward compatible.** Pre-existing databases (no
  bookkeeping table, columns already present) get auto-baselined
  on first boot of the new binary.

### Negative

- **Reinvented wheel.** A team familiar with golang-migrate has
  one extra small thing to learn. Mitigated by the runner being
  ~80 LOC and tested.
- **No down migrations.** Forward-only by design — sessiondb is
  an index, not a source of truth, so "rollback" means delete the
  file and re-index. If we ever need rollback we'll add a `down`
  field to `migration` and a CLI verb to apply it.
- **No dry-run / version-pinning CLI.** golang-migrate's `force`,
  `version`, and `goto` commands aren't available. If we ever
  need them, swap to golang-migrate at that point — the
  `schema_migrations` bookkeeping table is intentionally
  compatible with golang-migrate's default schema (`version` +
  `dirty` would need to be added).

### Pointers

- `pkg/sessiondb/migrate.go` — runner + migration list
- `pkg/sessiondb/migrate_test.go` — fresh / idempotent / baseline
  / split / column-exists coverage
- `pkg/sessiondb/store.go::Open` — single call site
- ADR-0006 — distributed-lock decision (related deferred
  infrastructure choice)
