# Review plan — 2026-10-23

The fix list from a third whole-repo review of gossms and gosmo, the day after
the 2026-10-22 plan was implemented and retired. Everything that plan listed is
done (C1–C3, B1, S1, D1–D3) except its H1, which waits for a release and is
carried forward as H1 below, not re-raised. Nothing settled in
`docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is
re-raised either; two items below are corrections to `docs/decisions.md`
itself, where the text no longer matches the code.

**Baseline (2026-10-23, `HEAD` 9b1174c / gosmo 1b7bf8c):** both repos build;
`go test ./...` passes in both; `go test -race ./...` passes in both with no
race reported; `go vet` and `go vet -tags livedb` pass; `staticcheck` reports
nothing (plain and `-tags livedb`); `gofmt -l` is empty. `deadcode
./cmd/gossms` reports the six settled hits (`NarrowToDMLStatement` is the test
oracle its doc comment says it is). `dupl` at 90–150 tokens finds only small
clones, none diverged into a bug. `govulncheck` reports one advisory in a
required module (GO-2026-5932, `golang.org/x/crypto/openpgp`), not reached and
with no fixed version — nothing to do. The existing fuzz targets ran 45 s each
(gosmo `FuzzNames`, `FuzzSpans`; gossms `FuzzParseFilter`, `FuzzScope`,
`FuzzLexer`, `FuzzFormatSQL`) — see § Verification baseline.

Not done in this pass: no live server and no tmux drive of the binary. Every
bug below was found by reading and is reproducible offline; each fix still
needs the drive `docs/testing.md` asks for.

So this pass found **one shipped user-visible bug** (B1, in the Details-pane
sort that landed with 25ce7e2), a precision limit in the result-grid sort, a gosmo retry
gap that gossms already works around, one architectural lead from gosmo's open
threads, a set of gosmo naming inconsistencies (breaking, which is allowed this
pass), and three places where the documents contradict the code.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
The doc-link test (`TestNoDanglingDocReference`) checks every `.md` path named
here, so cite only paths that exist.

Every gosmo change still follows `~/go/gosmo/CLAUDE.md`:

- build and test in gosmo first;
- run `go vet -tags livedb ./...` after any rename;
- run `TestLiveVersionSweep` on the oldest instance (win10cli\SQL2016) after
  any query change (A1 only).

IDs: **B** bug or hazard, **C** consistency, **A** architecture, **H** release
hygiene, **D** docs.

---

## Bugs and hazards

### B1 — gossms: the Details pane sorts a mixed-unit size column by its mantissa — ✅ DONE 2026-10-23

**Done:** `byteUnitScale` in `detail_browser_sort.go` scales B…EB to bytes;
`TestDetailSortScalesByteUnits` pins it (fails with the scaling dropped).
Driven on win10cli: SQL Server Logs, Size ▲ 29.4 KB … 457.2 KB … 1.5 MB, ▼ the reverse.

`parseDetailNumber` (`internal/tui/detail_browser_sort.go:220`) strips a short
trailing unit (`isUnitSuffix`, `:237`) and compares what is left. That is
right for `formatMB`, which always writes MB, but not for `formatBytes`
(`internal/tui/backup_common.go:47`), which picks B, KB, MB, GB… per value.

`errorLogFilesDetail` (`internal/tui/detail_browser_server.go:171`) fills the
SQL Server Logs and SQL Server Agent Error Logs folders' **Size** column with
`formatBytes`. Sorted ascending, `4.0 KB` (read as 4) comes before `512 B`
(512), and `1.2 MB` (1.2) before both. The other `formatBytes` cells in the
pane (Resource Governor pool, assembly files) are in Property/Value views,
which never sort.

**Fix:** in `parseDetailNumber`, scale the binary byte units `formatBytes`
writes (`B`, `KB`, `MB`, `GB`, `TB`, `PB`, `EB`) by 1024ⁿ before comparing;
leave every other unit (`ms`, `rows`) as today. `formatMB`'s column keeps its
order, as every cell in it scales by the same factor. Add cases to
`detail_browser_sort_test.go` with `512 B`, `4.0 KB`, `1.2 MB`, `3.0 GB`, and
mutation-check by dropping the scaling. Drive it: open SQL Server Logs, click
Size twice.

### B2 — gossms: the result-grid sort compares decimals as float64 — ✅ DONE 2026-10-23

**Done (with D4):** `query_panel_sort.go` sorts decimal/numeric/money by
`decimalSortKey` (sign × integer-length rank, then the trimmed digits, read
once per cell) and `datetimeoffset` by instant (Unix seconds + nanos; same
instant in two offsets ties, as ORDER BY). `TestResultsSortComparesDecimalsExactly`
and `TestResultsSortComparesDatetimeoffsetByInstant` fail with either kind
routed back to float/text. 1M rows: decimal 1.86 s, float 1.6 s,
datetimeoffset 2.3 s (the parse). Driven on win10cli: two `decimal(38,0)`
values differing in the last digit, `money` …5806/…5807, and three
datetimeoffsets whose wall and instant orders disagree, all sort by value.

`resultSortKindOf` (`internal/tui/query_panel_sort.go:204`) sends `decimal`,
`numeric`, `money` and `smallmoney` to `sortAsFloat`, and `newResultSortKey`
(`:268`) parses them with `strconv.ParseFloat`. A float64 keeps 15–17
significant digits; `decimal(38, s)` has 38 and `money` has 19. Two values
that differ only past the 17th digit compare equal, and the tie-break on row
index then keeps the server's order instead of sorting them. The comment at
`compareResultSortKeys` already makes the same argument for `bigint` past
2⁵³; decimals need it too. Rare in practice, wrong when it happens.

`datetimeoffset` has the matching text-sort problem: cells in different
offsets compare by local wall time, not by instant.

**Fix:**

- Decimal kinds: compare the rendered digit string exactly — sign, then
  integer-part length, then the digits lexically, then the fraction padded to
  equal length. The executor already renders these columns from the
  driver's digit bytes (`decimalLike`, `appendMoney` in
  `internal/query/executor.go`), so no parse is needed at all.
- `datetimeoffset`: parse the cell into a `time.Time` key and compare
  instants; on a parse failure fall back to text as today.
- Pin with cases in `query_panel_sort_test.go`: two `decimal(38,0)` values
  that differ in the last digit, negatives, a money value at its maximum, and
  two `datetimeoffset` cells where wall-time order and instant order disagree.

Low severity. Do it while B1 has the sort code open.

### B3 — gosmo: read retries skip deadlock victims and Azure transient errors — ✅ DONE 2026-10-23

**Done:** gosmo `retry.go` adds `isTransientServerError` (the listed numbers,
matched on the error and every `All` entry) and exports `IsTransient` =
`IsRetryable` || it; `withRetry` and `AcquireConn`'s prologue retry on
`IsTransient`, `IsRetryable` keeps its meaning. 1222 deliberately absent.
`TestIsTransient` and `TestDeadlockVictimReadIsRetriedOutsideTransactionOnly`
(fake driver raising `mssql.Error{Number: 1205}`: three tries on
`Server.query`/`queryRow`, `Database.query`, `AcquireConn`; one inside
`InTransaction`) fail with the rule routed back to `IsRetryable`. gosmo
`ARCHITECTURE.md` updated. gossms keeps the full-text follow tolerance; its
comment now says it counts reads that lost all of gosmo's retries.

`withRetry` (`~/go/gosmo/retry.go:78`) retries a read only when `IsRetryable`
(`:46`) says so, and `IsRetryable` recognises connection-level failures only.
A read chosen as a **deadlock victim (Msg 1205)** fails at once, though
re-running it is exactly SQL Server's advice and the read is idempotent. So do
Azure SQL's documented transient errors (40197, 40501, 40613, 49918–49920,
10928, 10929, 4221), which the Managed Instance in the estate can raise.

gossms already works around the first one locally: the full-text population
follow tolerates three failed reads in a row because its poll "can be picked
as a deadlock victim (Msg 1205, seen on 17…)"
(`internal/tui/fulltext_index_ops.go:131`). Every other read in gossms just
fails on one.

**Fix:** add capability to gosmo instead of working around it in gossms
(`CLAUDE.md` § Changing gosmo):

- Add an unexported `isTransientServerError(err)` that matches those error
  numbers on the error and every entry of its `All`, as `IsAlreadyExists`
  does.
- Have `withRetry` retry when either `IsRetryable` or it is true. Leave
  `IsRetryable`'s meaning alone: it says "the connection failed", and its doc
  tells callers that only idempotent operations may be retried blindly.
- Optionally export the combination as `IsTransient(err)` for callers running
  their own reads.
- `InTransaction` reads stay unretried: a 1205 there has already rolled the
  transaction back.
- Writes stay out entirely (`write_through_read_test.go` already enforces it).
- Test it with a fake driver error that carries `mssql.Error{Number: 1205}`.
  Assert three attempts, and that a 1205 inside `InTransaction` returns at
  once.

Keep gossms's follow tolerance. A poll that keeps losing is a different
signal, and three tries in gosmo plus three in the follow is fine.

---

## Consistency

All of these are **breaking** in gosmo. They are mechanical renames, with no
change to the SQL. Do them in one sitting, so one tag carries them. Then run
`go vet -tags livedb ./...` and update `~/go/gosmo/ARCHITECTURE.md`'s feature
map.

### C1 — gosmo: patch structs use three names for one idea — ✅ DONE 2026-10-23

**Done (C1–C3, hard break, no aliases):** `MailAccountChanges`, `MailProfileChanges`, `QueueChanges` (helper `mirrorQueueChanges`), `RouteChanges`, `DatabaseFileChanges` (doc now states its zero-means-unchanged rule), `ServerAuditDefinition`, `Database.Roles`, `Login.AddToServerRole`/`RemoveFromServerRole`. gosmo `CLAUDE.md` § Conventions: the `Spec` rule became `XDefinition`, plus a new `XChanges` rule; gosmo `ARCHITECTURE.md` and gossms `docs/decisions.md` follow, and a stale `srv.Add|RemoveServerRoleMember` row now names the real methods. CHANGELOGs untouched (H1 lists these under Migrating). gosmo build/vet/`-tags livedb` vet/test and gossms build/vet/test clean; gofmt empty. No behaviour change, so no tmux drive.

An `Alter` argument whose nil fields mean "leave as stored" is called
`…Changes` in the Agent family (`JobChanges`, `AlertChanges`,
`OperatorChanges`, `ScheduleChanges`, `AgentMailChanges`), but:

- `MailAccountOptions` and `MailProfileOptions`
  (`~/go/gosmo/database_mail_write.go:258`, `:426`) are documented as "a
  change to an account/profile" and work exactly like `JobChanges`;
- `QueueSettings` (`service_broker_queue.go:385`) and `RouteSettings`
  (`service_broker_routing.go:193`) are "what ALTER … can change", also with
  nil-means-unchanged;
- `FileModify` (`database_files.go:259`) is the same idea, written with zero
  values instead of pointers.

**Fix:** rename them to `MailAccountChanges`, `MailProfileChanges`,
`QueueChanges`, `RouteChanges` and `DatabaseFileChanges`. Field names and
semantics stay; `FileModify` keeps its zero-means-unchanged rule, and its doc
says so.

Leave these alone, because they are not patches:

- `ResourcePoolOptions`, `WorkloadGroupOptions` and
  `ExternalResourcePoolOptions` are the shared WITH list of CREATE *and* ALTER.
- `CredentialOptions` requires `Identity`.
- `QueryStoreOptions` and `IndexSetOptions` mirror T-SQL's `SET (…)`.
- The operation options (`BackupOptions`, `RestoreOptions`, `ShrinkFileOptions`,
  `ScriptOptions`) are not changes to an object.

gossms call sites: about 14 in all (`grep -rw` for each name).

### C2 — gosmo: `ServerAuditSpec` is not a server audit specification — ✅ DONE 2026-10-23

`ServerAuditSpec` (`~/go/gosmo/audit.go:189`) describes a **server audit**: it
is what `CreateServerAudit` takes and what `ServerAudit.Alter` takes. But
`ServerAuditSpecification` is a different object, the audit specification,
with its own `CreateServerAuditSpecificationRequest`. In SQL Server's own
vocabulary, "audit spec" means the second, so the short name reads as the
wrong type.

**Fix:** rename `ServerAuditSpec` to `ServerAuditDefinition`. Two gossms call
sites. `EventSessionSpec` doesn't collide with anything and stays.

### C3 — gosmo: role membership and the database role listing — ✅ DONE 2026-10-23

- `Database.DatabaseRoles` (`~/go/gosmo/database_role.go:29`) is the only
  `Database` listing that repeats its receiver. Its siblings are already
  `RoleByName` and `RoleRef`, and `Server`'s triple is
  `ServerRoles`/`ServerRoleByName`/`ServerRoleRef`. Rename it to `Roles`
  (16 gossms call sites).
- `Login.AddServerRoleMember`/`RemoveServerRoleMember`
  (`~/go/gosmo/login.go:133`, `:138`) take a *role* name, so they read
  backwards: they look like adding a member to the login. Their database twin
  is `User.AddToRole`/`RemoveFromRole`, and the role side is
  `ServerRole.AddMember`. Rename them to `AddToServerRole`/
  `RemoveFromServerRole` (3 gossms call sites).

### C4 — gosmo: two families have no `*ByName` (additive, low value) — ✅ DONE 2026-10-23

**Done:** `Database.UserDefinedFunctionByName` (`function.go`) and `Database.TriggerByName` (`trigger.go`). Built on the listings' own queries (`moduleListWhere`, `triggersWhere`) as `StoredProcedureByName` is, not on `readByName` — the trigger listing's events column has no single-row form, and one query per family can't drift. Offline row-serving test `module_byname_test.go`; both added to `schema_required_test.go` and the collation by-name test (a missing `COLLATE` mutation fails it); hand-driven lines plus `sweepMustCall` entries in the version sweep; gosmo `ARCHITECTURE.md` feature map rows. gosmo build/vet/`-tags livedb` vet/staticcheck/test clean; `TestLiveVersionSweep` on win10cli\SQL2016 (13.0.6500.1): 402 calls, 0 failed. gossms build/vet/test clean with `replace` active. gossms doesn't call them; no tmux drive (no gossms change).

Every `Database` family pairs its listing with `*ByName` and `*Ref`, except:

- `UserDefinedFunctions` (only `UserDefinedFunctionRef`);
- DML `Triggers` (only `TriggerRef`).

**Fix:** add `UserDefinedFunctionByName(ctx, schema, name)` and
`TriggerByName(ctx, schema, name)`, matching `StoredProcedureByName` and
built on `readByName`. Each gets an offline row-serving test and a line in
the version sweep. gossms doesn't need them today, so do this only with C1–C3.

Not raised: `Endpoint` has `EndpointByName` but no `EndpointRef`, and that is
deliberate. gossms reads the endpoint before Drop and Change State
(`internal/tui/explorer_object_ops.go:641`) because `IsSystem` comes from the
read.

---

## Architecture

### A1 — gosmo: idle pooled connections camp in the last database read (investigate, then decide)

This is `~/go/gosmo/OPEN-THREADS.md` § Idle pooled connections camp in the
last database read, which is still marked "not investigated". It is the one
architectural lead in either repo worth a spike.

A database-scoped read runs `USE [db]; …` on a pooled connection, and the
connection goes back to the pool still holding a shared database lock. Until
it is reused or expires, someone else's `RESTORE`, `SET OFFLINE` (without
ROLLBACK) or `SET SINGLE_USER` fails with "Exclusive access could not be
obtained", and an `AUTO_CLOSE` database never closes. Today that is only
mitigated: `connMaxIdleTime` is 60 s in gossms's `internal/db/connection.go`.

**Spike, not a fix:** add a read path that runs the batch as `EXEC @proc` with
`@proc = N'[db].sys.sp_executesql'`, as `perDatabaseExec` already does. The
session then never leaves its login database. Put the path behind an
unexported switch so both paths run side by side in tests.

Before it can replace `useBatch`, prove the three rules the open thread lists:

- the Msg 924 compile-time resolution (hence the variable);
- whether a cached plan under `[db].sys.sp_executesql` keeps a stale database
  id across a drop/recreate;
- parameter collation inside the target database, against
  `collation_lookup_test.go`.

Gate: `TestLiveVersionSweep` on 13, the collation tests, and a live test that
a `RESTORE … WITH REPLACE` from a second session succeeds immediately after a
gossms read of that database.

**Payoff if it holds:** the camping goes away, most of `releaseIdle`'s reason
goes with it, and `connMaxIdleTime` can return to the default. If any rule
fails, record the result in the open thread and close the spike.

---

## Release hygiene

### H1 — carried from the 2026-10-22 plan, unchanged

gosmo `HEAD` is 15 commits past `v0.1.0` and `CHANGELOG.md` has no entry above
it. The `TableKindAll` renumbering is still `iota`-based
(`~/go/gosmo/table_kind.go:27`). C1–C4, if done, add to the breaks the next
entry must list under **Migrating**.

Per `CLAUDE.md`, do this only when a release is asked for. If `TableKind` is
to get explicit values or a string type, do it before that tag.

---

## Docs

- **D1 — ✅ DONE 2026-10-23. `docs/decisions.md:1555` forbids what shipped.** "A Grid/Text result
  can exhaust memory — no Max Result Rows, no `maxRows` … Don't add a cap."
  But Max fetch rows shipped on 2026-10-22 (a715e99): `config.MaxFetchRows`,
  `query.WithMaxRows`, and the `docs/db-rules.md:175` rule on cancelling a cut
  set. Rewrite the bullet to say what is now settled: unlimited by default, as
  SSMS; an opt-in cap per result set that cancels the rest of the script; and
  Results To File bounded by the file whatever the cap.
- **D2 — ✅ DONE 2026-10-23. `docs/decisions.md:1444` says Shrink's Locking rows are "SQL
  2025+".** The code gates them at 2022 (`ServerInfo.SupportsShrinkLowPriority`
  in gosmo `types.go`; `internal/tui/shrink_database_dialog.go:72`), and
  `WAIT_AT_LOW_PRIORITY` on `DBCC SHRINKDATABASE`/`SHRINKFILE` is documented
  from SQL Server 2022. Change it to 2022+.
- **D3 — ✅ DONE 2026-10-23 ("among them"). `ARCHITECTURE.md` § Latest-only loads reads as a complete owner
  list, and isn't one.** It is missing at least the result-grid sort
  (`sortRun`), the Live Query Statistics run, the Replication Monitor reads,
  the Extended Events feed and column reads, the linked-server and
  cross-database completion loads, the update check and the Properties
  dialog. Either say "among them" or make it complete; the first is cheaper
  and won't drift.
- **D4 — ✅ DONE 2026-10-23 (with B2).** `internal/tui/query_panel_sort.go:24` claims the default
  collations' order.** The code lower-cases each cell and compares bytes,
  which is not `Latin1_General_CI_AS` order: `é` sorts after `z` here and
  beside `e` there, and a Windows collation's word sort ignores a hyphen
  that byte order counts. Say "case-insensitive code-point order". Making it collation-true would need `x/text/collate`,
  which isn't worth it for a display sort.

---

## Verification baseline

| Check | gossms | gosmo |
|---|---|---|
| `go build ./...` | clean | clean |
| `go test ./...` | pass | pass |
| `go test -race ./...` | pass, no races | pass, no races |
| `go vet ./...` | clean | clean (also `-tags livedb`) |
| `staticcheck` | clean | clean (also `-tags livedb`) |
| `gofmt -l` | empty | empty |
| `deadcode` | 6 hits, all settled | — |
| `dupl` (90–150 tokens) | small clones only | small clones only |
| `govulncheck` | 1 unreached advisory, no fix exists | same |
| fuzz, 45 s per target | `FuzzParseFilter`, `FuzzScope`, `FuzzLexer`, `FuzzFormatSQL`: pass, no crasher | `FuzzNames`, `FuzzSpans`: pass, no crasher |

---

## Recommended implementation order

Fix the user-visible bug first. Then do the gosmo work in one sitting: B3 is
additive, and the C items break the API, so the next tag carries them all.
Docs go with the item they describe, or at once where they stand alone. The
spike comes after that, and the release last, only on request.

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | ✅ **B1** unit-aware detail sort (done) | gossms | The only shipped user-visible bug; small, offline-testable; one tmux drive of SQL Server Logs. |
| 2 | ✅ **B2** exact decimal and `datetimeoffset` keys, + **D4** (done) | gossms | Same code as B1, still open; the comment fix rides along. |
| 3 | ✅ **D1, D2, D3** (done) | gossms | Pure doc corrections; no dependency, so do them at once. |
| 4 | ✅ **B3** transient-error read retry (done) | gosmo → gossms | Additive and behaviour-only; fake-driver test; then reconsider whether gossms needs its own 1205 note. |
| 5 | ✅ **C1, C2, C3** renames (done) | gosmo → gossms | Breaking; one sitting, then `go vet -tags livedb` in gosmo and a full build and test of gossms with `replace` active. |
| 6 | ✅ **C4** `UserDefinedFunctionByName`, `TriggerByName` (done) | gosmo | Only with step 5, while gosmo is open; otherwise skip. |
| 7 | **A1** pool-camping spike | gosmo | Needs live servers (13, 14, 17) and the version sweep; go/no-go recorded in `~/go/gosmo/OPEN-THREADS.md`. |
| 8 | **H1** changelog, optional `TableKind` hardening, tags | gosmo → gossms | Only when a release is asked for; list C1–C4 under Migrating. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet in gosmo after any rename);
- the checks named in the item have run;
- for a user-visible change (B1, B2), the built binary has been driven in tmux,
  per `docs/testing.md`.
