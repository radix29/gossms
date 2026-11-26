# Review plan, 2026-11-26 (second pass)

A review of gossms, `~/go/gosmo` and `~/go/tuikit` after the first
2026-11-26 plan closed (`4326e0e`, gosmo `03bb1f2`, tuikit `71e53f9`).
W1 (B1, gosmo) and W2 (B2) are done. Close an item by deleting it; delete this file with the
last one. gosmo and tuikit may break back-compat where an item says so.

**Method.** `go vet`, `staticcheck`, `deadcode -test` and
`go test -race -count=1 ./...` in all three repos; the thirteen fuzz targets
the previous pass left unfinished, 120 s each in a scratch copy (no corpus in
the trees); two throwaway `go/packages` analyzers over all three repos (a
`range` over a map that appends or writes output, for nondeterministic
order; a `defer` inside a loop); Query-row/`rows.Close` pairing in gosmo;
`safety.Classify` probed with ~100 further statements (pass-through rowsets,
locking hints, session-state verbs, legacy text verbs, Service Broker, key
opening, implicit EXEC, odd spacing); reads of every commit since the last
plan and of the executor/session, gosmo retry and `AcquireConn`, capability
cache and flights, tuikit `loop`, `DataGrid` view preservation, `core`
string helpers, gosmo quoting and bulk value conversion, gossms cell
parsing. Every finding below was reproduced with a scratch test (deleted
afterwards) unless it says otherwise. No server was contacted and the binary
was not driven: every fix still needs the `docs/testing.md` drive.

**What came out clean.** vet, staticcheck, `defer`-in-loop (none), map-order
(every hit sorts afterwards or is order-free), Query/Close pairing (every
flagged site goes through `scanRows`), race tests (see the end), the fuzz
targets (see the end). The classifier probe found nothing to fix: every
pass-through rowset (`OPENQUERY`, `OPENROWSET`, `OPENDATASOURCE`, nested in a
subquery too) floors to write, implicit and remote `EXEC` are dynamic-sql,
`NEXT VALUE FOR` is write. `deadcode` lists gosmo's `QSStatistics` and 28
tuikit exports: library surface, kept deliberately (`CLAUDE.md`).

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W3 — B3** (tuikit, breaking): `DataGrid` carries view state across a
  column-set change by column, not by index; then gossms drops the Activity
  Monitor procedure tab's equal-columns guard.
- **W4 — A1** (gosmo, breaking): `AcquireConn` reports the session's
  database and owns the release, closing gosmo's open thread on parked
  sessions; `query.Execute` loses two round trips and its hand-written
  discard.
- **W5 — N1**: nits (tuikit `LogFeed` ring, sequence dialog `%w`).
- **W6**: move what stays undone to `docs/open-threads.md` (and
  `~/go/gosmo/OPEN-THREADS.md` for W4's thread), then delete this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Bugs

### B3 — tuikit/gossms: a column added or hidden in the XEvent viewer moves widths and the cursor onto another column

**Where:** `~/go/tuikit/controls/datagrid.go`, `setSourcePreserving`
(widths via `ColumnWidthOverrides`/`restoreOverrideWidths`, `selCol`,
`selAnchorCol`, `sortCol`, `aligns`, all by index); callers
`internal/tui/xevent_viewer_rows.go` (`applyBatch`'s `grew` case,
`rebuild`, `catchUpAfterOverlay`).

**What:** `xevent.Store.Columns` is name, timestamp, *fields in arrival
order*, then actions. A new field arriving once actions exist is inserted
before them, and hiding a column removes one mid-list; both rebuild through
`SetSourceKeepingSelection`, which keeps everything by index. Reproduced
with a scratch grid: columns `name, timestamp, f1, act1`, `act1` dragged to
40 and the cursor on it; after `SetSourceKeepingSelection` with `f2` inserted,
the cursor is on `f2` and `f2` is 40 wide. A Shift-extended block keeps its
column range by index too, so Copy then takes cells of a column the user
never selected — the class of bug `SetSourceKeepingSelection`'s own doc
comment was written against, for rows. The Activity Monitor procedure tab
already knew (`activity_monitor_proctab.go`, `applyResult`: "widths and sort
are kept by index, so a changed column set starts afresh") and guards with
`slices.Equal`; the XEvent viewer does not.

**Fix (tuikit, breaking):** `setSourcePreserving` maps old columns to new
by header text, the n-th occurrence of a name to its n-th occurrence (a
result can repeat a name), and carries each per-column item through the map:
override widths, alignments, the sort indicator, `selCol` and
`selAnchorCol` (a column that vanished: the cursor clamps to the nearest
surviving column to its left, a block collapses, the indicator clears).
Change `ColumnWidthOverrides` to return the widths keyed the same way (or
make `restoreOverrideWidths` take the old header list) — whichever keeps
the exported surface smallest; note it in tuikit `CHANGELOG.md` *Migrating*.
Row state (scroll, marks, block rows) is unchanged. Then delete the
proctab's equal-columns branch (one `SetSourcePreservingView` call does both
cases) and its comment. **Tests (tuikit):** insert mid-list, remove
mid-list, rename (no match → defaults), duplicate names; each asserts widths,
cursor column, block columns. **Drive:** Watch Live Data on a session whose
events bring a new field after an action column exists (an
`sql_statement_completed` + `rpc_completed` pair with different actions
works), widen an action column, put the cursor on it, wait for the new
field; then hide a column left of the cursor.

---

## Architecture and simplification

### A1 — gosmo: `AcquireConn` hands the session's database back and owns the release

**Where:** `~/go/gosmo/retry.go`, `AcquireConn`; gossms
`internal/query/executor.go` (`Execute`) and `session.go` (`Open`).

**What:** `~/go/gosmo/OPEN-THREADS.md` § Idle pooled connections names the
one remaining way to park a session in a database: a connection a caller
switched with `AcquireConn` and closed back into the pool. gosmo hands out
the hazard and leaves the rule to each caller; gossms's `Execute` writes
it by hand (read `DB_NAME()` before, again after, `DiscardConn` unless
equal) at the price of two extra round trips on top of the `SELECT 1`
prologue — three per Activity Monitor procedure refresh. Separately, a
context cancelled during `AcquireConn`'s backoff returns `ctx.Err()` alone,
dropping the transient error, where `withRetry` joins the two.

**Fix (breaking):** the prologue becomes `SELECT DB_NAME()` (after `USE`
when a database is given, in the same batch) and `AcquireConn` returns a
small `*PinnedConn` (or `(conn, home string, err)`) whose `Release()` reads
`DB_NAME()` once and discards the connection unless it is still in `home`;
a read failure discards. `Session` keeps discarding unconditionally
(`DiscardConn` stays exported for it). `Execute`'s defer then calls
`Release`, its start-of-run read goes, and `Result.Database` comes from the
release's read when the run was not cancelled. Join the cause on the
backoff cancel. Close that open-thread paragraph in
`~/go/gosmo/OPEN-THREADS.md`. **Tests:** the existing `acquire_conn_test.go` fakes
gain a "left in another database → discarded" and "read failed →
discarded" case; gossms executor tests keep passing. **Drive:** Activity
Monitor's sp_whoisactive tab refresh on 17 with Extended Events or
`sys.dm_exec_sessions` confirming no session left in another database.

---

## Nits

### N1

- **tuikit `controls/editor/log_feed.go`, `prepend`:** every `Record`
  allocates and copies a fresh `max`-length slice (`append([]T{v}, s...)`)
  three times. A ring buffer (or appending oldest-first and reversing on
  `Lines`/`TakeAll`) makes it O(1); behaviour pinned by the existing
  `LogFeed` tests. Only worth it if a host records at a high rate (the key
  trace does, one per key).
- **gossms `internal/tui/new_sequence_dialog.go`, `valid`:**
  `fmt.Errorf("%s %v", label, err)` → `%w`, so a caller can still
  `errors.Is` a propsheet validation error; the only `%v`-wrapped error the
  sweep found in the three repos.

---

## Race and fuzz results

`go test -race -count=1 ./...`: clean in tuikit, gosmo and gossms.

Fuzz, 120 s each in a scratch copy, all passed with no new failing input:
`FuzzAuditNeverHoldsSQL`, `FuzzParseObjectName`, `FuzzParse`,
`FuzzFormatXML`, `FuzzFormatJSON`, `FuzzFormatSQL`, `FuzzLexersAgree`,
`FuzzLexer` (gossms); `FuzzExternalModuleVerb`, `FuzzSpans`, `FuzzNames`
(gosmo); `FuzzDrawTextLineMatchesTruncateLine`,
`FuzzRunesWidthAgreesWithDisplayWidth` (tuikit). With the previous pass's
five, every fuzz target in the three repos has now run clean.
