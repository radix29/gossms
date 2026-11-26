# Review plan, 2026-11-26 (second pass)

A review of gossms, `~/go/gosmo` and `~/go/tuikit` after the first
2026-11-26 plan closed (`4326e0e`, gosmo `03bb1f2`, tuikit `71e53f9`).
W1 (B1, gosmo), W2 (B2) and W3 (B3, tuikit) are done. Close an item by
deleting it; delete this file with the last one. gosmo and tuikit may break back-compat where an item says so.

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
