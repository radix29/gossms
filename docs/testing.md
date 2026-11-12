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
  before concluding a dialog closed. Judge alignment by visual width
  (combining marks 0, wide 2), not by how the captured text looks — a viewer
  counting a combining mark as a column fakes a shifted border. tmux 3.6 also
  disagrees with tcell on one emoji kind: it draws an emoji + skin-tone
  modifier (`👍🏽`) four columns wide where tcell gives two, so what follows it
  looks overwritten. `❤️`, flags, ZWJ sequences, CJK and combining marks agree
  — test cluster alignment with those.
- **Database** — a real SQL Server, not a mock (details not in the repo; ask).
  Create throwaway objects, exercise the real write, drop them — never mutate
  pre-existing ones.
- **Subtle fix** — A/B against a kept pre-fix binary: old reproduces, new
  doesn't.

Tests assert an outcome, not "nothing panicked". Mutate the covered code and
confirm the new test fails.

**Static checks are part of "done", not of a tag.** Both run clean today; keep
them clean (`go install honnef.co/go/tools/cmd/staticcheck@latest`,
`golang.org/x/tools/cmd/deadcode@latest`):

- gossms: `staticcheck ./...` and `deadcode -test ./...`. A test helper
  either finding names is usually a lost assertion, not just litter — check
  `git log -S` for whether a test once called it before deleting it (the
  2026-11-11 pass found a view-reorganize case that read its result and never
  checked it).
- gosmo: `staticcheck ./...` only. `deadcode` lists exported library surface
  and the unexported helpers only it reaches (`schedulerGroupSizes`) — kept
  deliberately, not a cleanup list.

**The race detector is part of "done" for anything that crosses goroutines.**
Nearly every UI result reaches the event loop through `postAndWake` from a
goroutine, so a change touching `safego`, `postAndWake`, `latest`, a channel,
or state a callback shares with a loader runs `go test -race -count=1` on the
packages it changed (gosmo: on `retry.go`, the pool, `gosmotest`). The whole
`go test -race -count=1 ./...` runs before a tag (below) and in the release
workflow's test step: about 6 minutes for gossms, 2 for gosmo (2026-11-11,
both clean). `-race` needs cgo; `CGO_ENABLED=0` makes it refuse to run, so the
workflow sets `CGO_ENABLED=1` for that step only — the shipped binaries stay
no-CGO.

**gosmo's live suite: run what the change touches, not the whole suite.**
The full `livedb` run takes about 16 minutes per instance (313 tests, serial,
several waiting on server timers). After a change, name the affected tests
with `-run '^(TestLiveA|TestLiveB)$'` — the ones that call the changed code,
plus any a plan names (`TestLiveVersionSweep` for a version gate). The
fake-backed `go test ./...` still runs whole; it takes seconds. For a broad
check, `go test -tags livedb -short` skips the dozen tests over 15 s
(`skipSlowInShort`). A full run without `-short` is for a tag. On several
instances, run them at the same time: they are separate servers.

**A live dry-run fidelity check needs its own event session.** `xevent
capture` leaves out every `goSSMS - CLI` connection, so it cannot watch
another gossms command. Create a session filtered on `client_app_name =
N'goSSMS - CLI'` with `sql_batch_completed` and `rpc_completed` only (gosmo
sends most writes through `sp_executesql`, so the statement is the RPC's
text, with its `@pN` bound after it). Adding the statement events floods the
ring buffer: `sp_updatestats` alone pushed its own batch out. Read
`target_data` into an `xml` variable before shredding it, or the read
outlives `--timeout`. Leave out the reads around the write: connect-time
reads, the pre-2019 `BackupDirectory` registry read, `RESTORE HEADERONLY`.

**After a lexer or parser change, fuzz it:** `go test ./internal/tuikit/sqltext
-run XXX -fuzz FuzzLexer -fuzztime 60s -fuzzminimizetime 0`, likewise `FuzzScope`
in `./internal/tui/sqlparse` (their seeds run in plain `go test`; without
`-fuzzminimizetime 0` the minimizer stalls them). A crasher is a bug to fix,
and its `testdata/fuzz/` file stays as the regression seed. A Format Document
formatter change gets the same with `FuzzFormatSQL`/`FuzzFormatXML`/
`FuzzFormatJSON` in `./internal/format` — five minutes for SQL: its first
eight bugs (a `go` identifier left alone on a line, `\f` read as a blank by
the GO rule, a label seen through a comment) each took the fuzzer under two.

**`waitAndDrain` runs one callback, not "everything".** It returns after the
first queued UI callback, and the order goroutines post in shifts under
`-race`. When an action posts more than one (Apply's page reloads plus a node
reload, a showing's several loads), wait on the outcome with
`drainUntil(t, a, cond, what)` instead — two `-race`-only flakes came from
this.

**Opening a Properties dialog starts its first page's load on a goroutine**,
even in a test that never looks at the page. Script its reads (or fail them
all with a catch-all rule) and wait for the load to finish
(`len(a.propDialog.pageRuns) == 0`). Left running against a `gosmotest` server
with no rules, its read failed the test only when it beat the test's end:
`TestPropertiesApplyReloadsTheFolderListingTheObject` failed about 1 run in 20.

**A test package that can save config sandboxes `os.UserConfigDir` in
`TestMain`** (`internal/tui/main_test.go` sets `XDG_CONFIG_HOME`, `HOME`,
`AppData` to a temp dir). Without it, any `saveConfig` writes the developer's
real `config.json`. `TestOptionsApplySwitchesColorTheme` flipped the user's
theme to Light on every `go test ./...`.

**A timing test over pure channels and timers runs in `synctest.Test`.** The
bubble's clock is fake, so a window holds exactly the ticks the schedule
predicts: assert exact counts and wait with `synctest.Sleep`, not a
`time.Sleep` poll loop (`internal/activity/collector_test.go`). Only for code
that does no real I/O — a goroutine blocked on a tty, socket or live server is
not "durably blocked", so screen and live tests can't move.

**"Give it time, then assert nothing happened" is `synctest.Wait()`, not a
sleep.** A too-short sleep never fails such a test — it passes without having
checked anything. In a bubble, `Wait` returns once every goroutine is blocked
or gone, so a post or a goroutine that should not exist has shown itself
(`safego_test.go`, `prop_dialog_pageruns_test.go`; the `gosmotest` server works
in a bubble). If the goroutine always posts and the post must be *ignored*,
`waitAndDrain` for it instead. A goroutine parked on a `sync.Mutex` is not
durably blocked, so `Wait` hangs on it (`clipboard_test.go` keeps its sleeps).

**A `-race`-only failure can be a stale build cache.** After a mutation check
edited and restored a file, `go test -race` kept failing on byte-identical
source (the race archive from the mutated file was reused). Run `go test -race
-count=1 -a ./...` (or `go clean -cache`) before believing it, and A/B by
copying files aside rather than editing in place.

**Before a tag**, in both repos (gossms and `~/go/gosmo`), on top of the
everyday build/test/vet:

- `go test -race -count=1 ./...` — clean in both as of 2026-11-11 (the
  race-detector paragraph above).
- `govulncheck ./...` (`go install golang.org/x/vuln/cmd/govulncheck@latest`).
  "Your code is affected by 0 vulnerabilities" is the bar; a module-level hit
  your code doesn't call (GO-2026-5932, x/crypto's openpgp, no fix) is noise.
- `go vet -tags livedb ./...`.

gosmo's write-retry rule is a test, not a sweep:
`write_through_read_test.go` fails when SQL handed to `query`/`queryRow`/
`queryRowScan` contains `EXEC sp_send/add/update/delete…` or an
`INSERT`/`UPDATE`/`DELETE` on a real table.

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

**Drive a Properties page end to end with `internal/tui/gosmotest_bridge_test.go`
and `gosmotest_fixtures_test.go`.** `gosmo.NewServer` takes a caller `*sql.DB`; the harness
gives `newGConn` (a `*db.ServerConn` over a `gosmotest` server recording every
statement; the `fakeResponse` fixtures in `gosmotest_fixtures_test.go` script it),
`loadPageG`,
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
- **Both return the statement with its `@pN` bound** (`Query.Bound`), as
  gosmo's observer shows it: gosmo binds every Agent procedure's strings, so
  the raw text says only `@job_name = @p1`. `Reads` is raw — check a read's
  values with `ReadArgs`.
- `eachDatabase` (`db_scan.go`) and gosmo's `userMappingsIn` skip a database
  whose read fails, so an under-scripted fake gives an empty grid and a no-op
  apply. Assert rows loaded first.
- **A fake row that carries a gosmo key spells it through gosmo's helper**
  (`probeKey`, `gosmo.DatabaseSecurableKey`), never by hand — a hand-spelled
  key goes stale when gosmo's format changes, and "gossms builds no key
  literal" missed exactly these fixtures (review plan T26).

The harness shows the page asked the right things and built the right request —
never that the T-SQL is valid. Statement text is gosmo's tests; acceptance is a
live run.

**Every command-line group runs real gosmo over `gosmotest`.**
gosmo's exported fake driver answers each query from the first rule whose
text it contains, with a database read's transport stripped (`Query.Database`
says where it ran). `internal/cli/object_server_test.go` is the server: its
rows are positional, column for column in gosmo's SELECT order, each read's
built by one encoder (`columnRow`, `indexRow`, `statisticRow`, …), so a gosmo
change to a query's shape or scan fails the CLI test — a hand-written fake
of the gosmo method would pass it. A query no rule answers fails the test naming the query;
add a rule, don't loosen the match. Rules answer in registration order, so
a group whose reads overlap another's registers its more specific rules
first (`newObjectServerWith`; `script_server_test.go` is the example).
The row encoders are `internal/cli/clitest`'s, shared with package `cmd`'s
tests, whose `catalogServer` (`cmd/catalog_server_test.go`) answers the
resolution and table/view reads for a handler tested there (the dry-run
fidelity harness's `data import` case); `storageServer`
(`cmd/db_storage_test.go`: the databases and HAS_DBACCESS, under the
files, space, log-usage and table-sizes servers), `secServer`
(`cmd/security_server_test.go`), `maintServer`
(`cmd/maint_server_test.go`) and `monitorServer`
(`cmd/monitor_server_test.go`) build on it the way `scriptServer` builds on
`objectServer`. A list read answers in the query's ORDER BY, as the
server would — a fake's insertion order hid what the user sees — and a
filtering read applies its WHERE or HAVING. Their writes go to gosmotest
too: the server's write rule (`applyWrite`) records each statement as
sent, applies what it states to the server's fields — so the read after
it sees the change — and fails it as the test asks; tests assert the
statement text, not a fake's arguments. gosmotest answers a BACKUP or
RESTORE that reads progress messages (`Query.Messages`) as a message
stream, and a bulk load as one `INSERTBULK` Exec per row. Package `cli`'s
`dbFake` (`db_test.go`) serves `db list`/`db info` and `agent context`,
whose permission probes it answers through `clitest.ServerCapabilityRows`
and `DatabaseCapabilityRows`: a probe gosmo returns always carries its role
map, so "unprobed" is a failed probe, never a nil map.
