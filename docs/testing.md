# Verifying a change in goSSMS

How to prove a change works. Loaded when writing or running tests, and before
calling anything done. `CLAUDE.md` § Build & verify has the commands.


`go test ./...` passing means the unit tests pass, not that the change works.
Nearly every real bug in this project's history was caught by driving the built
binary. For anything touching the TUI or the database layer, verify by running it:

- **TUI behavior** — drive the binary headlessly under tmux:
  `tmux new-session -d -s t -x 100 -y 30 <binary>`, `tmux send-keys -t t ...`,
  then `tmux capture-pane -t t -p` (plain) or `-p -e` (SGR codes, to tell focused
  from merely selected). Send `Escape` in its **own** `send-keys` call — batched
  with a following key, tcell reads `\x1b<key>` as Alt+key. Verify focus against a
  capture after each keystroke instead of counting `Tab`s, and capture the full
  pane height before concluding a dialog closed.
- **Database behavior** — a real SQL Server instance, not a mock. Connection
  details are deliberately not in the repo; ask for them. Create throwaway
  databases/logins, exercise the real write path, then drop them — never mutate
  pre-existing ones.
- When a fix is subtle, A/B it: keep a pre-fix binary, show the old behavior
  reproducing and the new one not.

Tests must assert an outcome, not that nothing panicked. Check a new test by
mutating the code it covers and confirming it fails.

**A round-trip test proves two functions are inverses, never that either is
right.** Where a load half and a write half share parallel label/code tables
(schedule dropdowns, permission-state tables, any `items[]`/`values[]` pair), a
fault in the shared table cancels out: swap two entries in `weekdayBits` and the
checkbox labelled Monday sets Tuesday's bit while `populate`/`readFrequency` still
agree. Pin such a pair by *naming* it (`"Monday"` -> `gosmo.WeekdayMonday`) and by
asserting both slices are the same length; add a page test that pins the value
reaching the server (`agent_schedule_props_page_test.go` pins `@freq_interval`).

**A Properties page can be driven end to end from a test — use
`internal/tui/fakedb_test.go`.** `gosmo.NewServer` takes a caller-supplied
`*sql.DB`; the harness gives `newFakeConn` (a `*db.ServerConn` over a scripted
driver that records every statement), `loadPage`, and `textRow` for addressing a
form by label. `database_props_files_page_test.go` is the worked example. The
`gosmo.WithScript` harness the New-X dialogs use does *not* work here — a
Properties page's `load` and `apply` both open with a by-name read, and
`WithScript` intercepts writes only. Rules, each learned from a test that passed
for the wrong reason:

- **Drive a row with `Edit`, never `SetValue`/`SetSelected`** — the latter move the
  dirty baseline with the value, so apply skips the row and a "nothing was written"
  assertion passes wrongly. Use `editText`/`editSelect`/`editRadio`/`toggleByName`.
- **Address rows and grid cells by name, never by index** — these pages read their
  grid back positionally, so an index-based test agrees with a misaligned page.
- Where a page keeps a filtered or pending-removal subset alongside the full list,
  exercise *two* edits: the lists only diverge after the first.
- **Act on an object that is not first in its list** — a page that ignores the
  selection still passes when the test picks row 0.
- **Scope a `fakeResponse` with `db:`** when the page reads the same query in
  several databases, or every database returns the identical answer and the
  misalignment the test exists to catch is unreachable.
- **Scope one with `arg:` for a by-name read, placed *before* the list read** —
  responses match by substring in order, and `DatabaseByName`'s query also contains
  `FROM sys.databases`, so otherwise every object resolves to whichever row sorts
  first.
- **Assert with `StatementsIn(db)`, not `Statements()`, for anything
  database-scoped** — the bare `USE` is stripped as plumbing, and with it the only
  record of where the write landed; pair it with `assertNoStatementsIn`.
- `eachDatabase` (`db_scan.go`) and gosmo's `userMappingsIn` drop a database whose
  read fails rather than failing the page, so an under-scripted fake yields an
  empty grid and an apply that writes nothing. Assert the rows loaded first.

The harness is bounded: queries match by substring and are answered with whatever
the test scripted, so it shows the page asked for the right things and built the
right request — never that the T-SQL is valid. Statement text is gosmo's own
tests; acceptance is a live run.

