# Verifying a change in goSSMS

Read when writing or running tests, and before calling anything done.
Commands: `CLAUDE.md` § Build & verify.

`go test ./...` passing is not "the change works" — nearly every real bug was
caught by driving the built binary. For TUI or database changes, run it:

- **TUI** — headless tmux: `tmux new-session -d -s t -x 100 -y 30 <binary>`,
  `tmux send-keys -t t ...`, `tmux capture-pane -t t -p` (`-p -e` for SGR
  codes, to tell focused from selected). Send `Escape` in its **own**
  `send-keys` — batched, tcell reads `\x1b<key>` as Alt+key. Check focus by
  capture after each key rather than counting `Tab`s; capture full pane height
  before concluding a dialog closed.
- **Database** — a real SQL Server, not a mock (details not in the repo; ask).
  Create throwaway objects, exercise the real write, drop them — never mutate
  pre-existing ones.
- **Subtle fix** — A/B against a kept pre-fix binary: old reproduces, new
  doesn't.

Tests assert an outcome, not "nothing panicked". Mutate the covered code and
confirm the new test fails.

**A `-race`-only failure can be a stale build cache.** After a mutation check
edited and restored a file, `go test -race` kept failing on byte-identical
source (the race archive from the mutated file was reused). Run `go test -race
-count=1 -a ./...` (or `go clean -cache`) before believing it, and A/B by
copying files aside rather than editing in place.

**A round-trip test proves two functions are inverses, not that either is
right.** Where load and write share parallel label/code tables (schedule
dropdowns, permission states, any `items[]`/`values[]` pair) a fault cancels
out — swap two `weekdayBits` entries and Monday sets Tuesday's bit while
`populate`/`readFrequency` still agree. Pin by *name* (`"Monday"` ->
`gosmo.WeekdayMonday`), assert equal slice lengths, and pin the value reaching
the server (`agent_schedule_props_page_test.go` pins `@freq_interval`).

**A DSN test asserts what the driver parses**, not what gosmo writes — four
Entra methods shipped unable to connect because no test handed the string to
go-mssqldb. Build the connector (for Entra, the driver's parser and validator,
no dial) and assert on `msdsn.Parse(dsn).Parameters`. gossms's `toGosmoOptions`
tests go through `ConnectionString` → `azuread.NewConnector`.

**Drive a Properties page end to end with `internal/tui/fakedb_test.go`.**
`gosmo.NewServer` takes a caller `*sql.DB`; the harness gives `newFakeConn` (a
`*db.ServerConn` over a scripted driver recording every statement), `loadPage`,
and `textRow` (address a form by label). Example:
`database_props_files_page_test.go`. The `gosmo.WithScript` harness doesn't
work here — `load` and `apply` start with a by-name read, and `WithScript`
intercepts writes only. Rules, each from a test that passed for the wrong
reason:

- **Drive rows with `Edit`** (`editText`/`editSelect`/`editRadio`/
  `toggleByName`), never `SetValue`/`SetSelected` — those move the dirty
  baseline, so apply skips the row.
- **Address rows and cells by name, never index** — pages read grids back
  positionally, so an index test agrees with a misaligned page.
- With a filtered or pending-removal subset beside the full list, make *two*
  edits — they diverge only after the first.
- **Act on an object that isn't first in its list** — a page ignoring the
  selection passes on row 0.
- **Scope a `fakeResponse` with `db:`** when a query runs in several databases,
  or all answer alike and misalignment is invisible.
- **Scope by-name reads with `arg:`, placed *before* the list read** —
  responses match by substring in order, and `DatabaseByName`'s query also
  contains `FROM sys.databases`.
- **Assert with `StatementsIn(db)`, not `Statements()`,** for database-scoped
  writes (the bare `USE` is stripped); pair with `assertNoStatementsIn`.
- `eachDatabase` (`db_scan.go`) and gosmo's `userMappingsIn` skip a database
  whose read fails, so an under-scripted fake gives an empty grid and a no-op
  apply. Assert rows loaded first.

The harness shows the page asked the right things and built the right request —
never that the T-SQL is valid. Statement text is gosmo's tests; acceptance is a
live run.
