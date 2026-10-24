# Review plan — 2026-10-24

The fix list from a fifth whole-repo review of gossms and gosmo. Everything
the 2026-10-23 (second pass) plan listed is done except its H1 (release
hygiene), carried below. Nothing settled in `docs/decisions.md`,
`~/go/gosmo/OPEN-THREADS.md` or either `CLAUDE.md` is re-raised, except where
the author's waiver for this pass applies: **gosmo may break backward
compatibility where it helps** (C1, and the options in B2 and Q2).

**Baseline (2026-10-24, gossms `HEAD` 7d5a298 / gosmo c73479b):** both repos
build; `go test -race ./...` passes in both; `go vet` and `staticcheck -checks
all` report nothing; an `x/tools` multichecker (the `modernize` suite,
`nilness`, `unusedwrite`, `defers`, `waitgroup`, `shadow`) finds nothing but
deliberate shadowing; `dupl -t 120` finds 8 small clone groups in gossms and 6
in gosmo, none worth merging; `deadcode ./cmd/gossms` reports the settled hits
plus `sqlparse.NarrowToDMLStatement` and `core.FillAreaByPut` (tuikit/sqlparse
API, test-reached); `govulncheck` finds nothing reachable (one advisory,
`x/crypto/openpgp`, in an indirect module neither repo calls).

How it was found: the core paths were read in full (`internal/db`,
`internal/query`, the App loop, `safego`/`latest`, config save/merge,
IntelliSense load lifecycle, Detail Browser runs and cache, both column sorts,
the XEvents feed; gosmo's helpers, `procRead`, retry, the retargeting
connector, `Database` exec/query, files, job history, and a sweep of every
literal built with `'%s'`). B1 was confirmed by driving the built binary
against win10cli (17.0.1145.4), and Q4's facts with `sqlcmd` there, on a
throwaway database, dropped. Feature dialogs outside those paths were sampled,
not read whole.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
The doc-link test (`TestNoDanglingDocReference`) checks every `.md` path named
here, so cite only paths that exist.

Every gosmo change follows `~/go/gosmo/CLAUDE.md`: build and test in gosmo
first; `go vet -tags livedb ./...` after any rename; `TestLiveVersionSweep` on
the oldest instance (win10cli\SQL2016) after any query change.

IDs: **B** bug, **C** consistency, **S** simplification or optimisation, **A**
architecture, **D** docs, **Q** question for the author, **H** release
hygiene.

---

## Bugs

### B1 — gossms: the Details pane calls most leaf objects "Object"

**Seen live:** Security ▸ Server Roles ▸ `##MS_DatabaseConnector##` shows

```
Name    | ##MS_DatabaseConnector##
Type    | Object
Database|
Schema  |
```

A leaf with no `detailLoaders` entry falls back to a Name/Type/Database/Schema
grid (`internal/tui/detail_browser_runs.go:233`, `fetchNodeDetails`), and the
module view does the same (`internal/tui/key_signatures_page.go:303`). Both
take the type from `nodeTypeName` (`internal/tui/tree_node.go:279`), which
names 11 of the 197 node types and answers "Object" for the rest. Leaves that
reach it today: server role, database role, schema, sequence, synonym, index,
statistic, key, check, foreign key, column, view column, linked server, AG
replica, AG database, AG listener, XEvent Profiler session, and the DML
trigger through `moduleDetail`. Server-scoped ones also get two empty rows,
Database and Schema, that can never hold anything.

**Fix:**

- Give every NodeType its display name, SSMS's singular noun ("Server Role",
  "Statistic", "Availability Replica"). With A2 this is one column of that
  table; without it, complete the switch.
- Leave out the Database and Schema rows when the value is empty.

**Verify:**

- A test over `0..nodeTypeCount` that every type reaching the fallback or
  `moduleDetail` names something other than "Object". Mutation-check it by
  deleting one case.
- tmux on 17: a server role, an index and a DML trigger show their own type,
  and the role shows no Database/Schema rows.

### B2 — gosmo: `FileGroups` leaves `SpaceUsedKB` at 0 (`TestLiveWrongValuesFileUnits`)

Recorded in `~/go/gosmo/OPEN-THREADS.md` § Three live tests fail on `HEAD` as
unexplained. The cause: `DatabaseFileInfo` is filled by three reads, each
filling a different subset.

| Read | `SpaceUsedKB` | `SizeOnDiskKB` | `FileGroup` |
|---|---|---|---|
| `Database.Files` (`database_files.go:73`) | yes | no | yes |
| `Server.DatabaseFiles` (`:108`) | no | yes | always "" |
| `Database.FileGroups` (`:357`) | no | no | yes |

The test compares a file's `SpaceUsedKB` across `Files` and `FileGroups`, and
`FileGroups` never reads `FILEPROPERTY`.

**Fix (recommended):** build `FileGroups` from one `sys.filegroups` read plus
`Files`, grouped in Go. One file scanner instead of two, and the files a
filegroup holds are by construction what `Files` returns. Two round trips
instead of one; `FileGroups` is a Properties-page read.

**Alternative (smaller):** add the `FILEPROPERTY` column to `FileGroups`'
query.

Either way, `DatabaseFiles` stays the read for a database that cannot be
entered, and its doc already says which fields it leaves empty. C2 states the
rule.

**Verify:** `TestLiveWrongValuesFileUnits` passes on 13, 14 and 17; Database
Properties ▸ Filegroups and ▸ Files show the same sizes in tmux.

### B3 — gosmo: `TestLiveBackupSetTypes` is stale

Also in OPEN-THREADS § Three live tests fail. `buildRestoreStatement` starts
`USE [master];` by design (its doc, `~/go/gosmo/restore.go:170`, and
`fromMaster`), and `live_backup_set_types_test.go:123` still wants the
statement to start `RESTORE LOG `. **Fix:** strip the `USE [master];` prefix
before the check (or check for `\nRESTORE LOG `), keeping the no-MOVE check.
Then delete both B2 and B3 from OPEN-THREADS, leaving the Msg 701 note.

### B4 — gossms: Stop during a commit says nothing is running

`endTransactions` (`internal/tui/query_panel_exec.go:142`) sets
`p.executing` but not `p.cancel`, so Query ▸ Cancel Executing Query while
"Committing open transactions…" answers "No query is currently executing"
(`CancelExecution`, `:48`). The commit must not be cancelled — an attention
mid-COMMIT leaves its outcome unknown — but the message is wrong. **Fix:** say
"Committing open transactions — this cannot be stopped" (or "Rolling back")
while `endTransactions` holds the latch. A test for the wording.

---

## Consistency

### C1 — gosmo (breaking): two methods still take adjacent bools

v0.1.0 turned every boolean mode parameter into a named type. Two pairs
remain, where a swapped argument compiles and does the opposite:

- `Table.SetChangeTracking(ctx, enable, trackColumns bool)`
  (`~/go/gosmo/change_tracking.go:116`). Its siblings already take a value:
  `Database.SetChangeTracking(ctx, ChangeTrackingInfo)` and
  `FullTextIndex.SetChangeTracking(ctx, FullTextChangeTracking)`.
- `Login.SetPasswordPolicy(ctx, checkPolicy, checkExpiration bool)`
  (`~/go/gosmo/login.go:274`).

**Fix:** `Table.SetChangeTracking(ctx, TableChangeTracking{Enabled,
TrackColumnsUpdated bool})` and `Login.SetPasswordPolicy(ctx,
PasswordPolicy{Check, CheckExpiration bool})`; keep each method's validation.
Three gossms call sites (`table_props.go:317`,
`database_props_change_tracking.go:127`, `login_props.go:233`). The write
tests in `script_*_write_test.go` move with them. Under **Migrating** in the
next CHANGELOG entry (H1).

`SetEnabled(ctx, on bool)` and `SetReadOnly(ctx, readOnly bool, term)` are a
single bool each, named by the method — not raised.

### C2 — gosmo: a field one read fills and another leaves zero

The `Ref` rule exists because a zero field that looks like data is a silent
bug. The same trap sits in result types filled by several reads:

- `DatabaseFileInfo` — B2's table.
- `JobHistoryEntry.JobName` (`~/go/gosmo/agent_job_history.go:85`) is ""
  from `Job.History`, though the job's name is in hand. **Fix:** set it to
  `j.Name`; free.
- `User.LoginName`, `LoginDisabled`, `MappedObject`
  (`~/go/gosmo/schema_user.go:135`–`142`) are filled by `UserByName` only.
  See Q2: the obvious join is not available on Azure SQL Database.

**Rule to add** to gosmo `CLAUDE.md` § Conventions, beside the `Ref` bullet:
a field a type declares is filled by every read returning that type, or its
doc names the reads that leave it zero *and* the zero is not a plausible
value. New code must not add a field that only one of several reads fills.

### C3 — gossms: the Job History leaf is not what its comment says

`agentJobHistoryDetail` (`internal/tui/agent_detail.go:227`) says it shows
"the latest job-level outcome of every job". It shows `JobHistory(ctx, 100)`:
the 100 most recent job-level runs across all jobs, so one job on a
one-minute schedule fills the list and every other job drops out of it. The
Jobs folder already shows each job's last outcome. **Fix:** correct the
comment ("the 100 most recent job runs, all jobs"); change the query only if
per-job is what the leaf is meant to be (Q3).

---

## Simplification and optimisation

### S1 — gosmo: `procParamType` allocates on every string argument

`proc_read.go:64` runs `len(utf16.Encode([]rune(v)))` for every string bound
to every pooled database read: two allocations proportional to the string.
UTF-8 is never shorter than UTF-16 in code units, so `len(v) <= 4000` decides
almost every call with no allocation; only a longer string needs the count
(`utf16.RuneLen` summed over the runes, still allocation-free). Behaviour
unchanged; the existing `procParamType` tests cover it, plus a case at 4000
and 4001 UTF-16 units made of surrogate pairs.

### S2 — nothing else from the tools

The clone groups `dupl` reports are under 20 lines each, in unrelated
features, and the two DDL-trigger files' twin is settled in
`~/go/gosmo/OPEN-THREADS.md`. No other simplification was found worth a
change on its own; the larger ones are A1 and A2.

---

## Architecture

### A1 — gosmo: send database-scoped writes as RPCs too, then drop gossms's idle override

The pool-camping thread (`~/go/gosmo/OPEN-THREADS.md` § Idle pooled
connections camp in the last database written to) is half closed: reads go
through `procRead`, but `Database.withConn` (`~/go/gosmo/database.go:95`,
every `Database.exec`) still runs `USE [db]` and returns the connection to the
pool inside `db`. That is the only reason gossms keeps `connMaxIdleTime` at
60 s against gosmo's 5-minute default (`internal/db/connection.go:40`), paying
a re-login after every idle minute.

**Spike, behind an unexported switch, as the read spike was:**

1. Run a pooled `Database.exec` as an RPC of `[db].sys.sp_executesql`, with
   `procRead`'s parameter declarations. Keep `useBatch` for `InTransaction`.
2. Check what the thread lists — `SET` options, `ExecProc`'s named RPC, the
   transaction path — and also: statements that must open a batch (`CREATE
   VIEW/PROCEDURE/FUNCTION/TRIGGER/SCHEMA`; inside `sp_executesql` they do),
   `execAtomic`'s multi-statement writes, and `execScan`'s OUTPUT parameters.
3. Live on 13, 14 and 17: the five `live_proc_read_test.go` rules mirrored for
   writes (session left home, another session's RESTORE not blocked, 911/924/942
   refusal, id swap, parameter collation), the `script_*_write` statements
   replayed through `-tags livedb`, and `TestLiveVersionSweep`.
4. Go: delete the switch and `withConn`'s USE path; gossms's
   `connMaxIdleTime` returns to the default (keep the comment's `AUTO_CLOSE`
   note if 60 s is still wanted for it). No-go: record which rule failed in
   the thread and delete the switch.

### A2 — gossms: one table for what a NodeType *is* (optional)

What each of the 197 node types is lives in parallel tables: three switches
in `tree_node.go` (`isContainerNode`, `hasChildren`, `nodeTypeName`), three
icon switches in `tree_node_icons.go`, and 13 `map[NodeType]…` tables across
loaders, menus, details, object ops, rights and scripting.
`TestEveryNodeTypeIsWired` holds them together, and they are complete today
(114 loaders, 85 leaves, no gap). But B1 is what a partial switch with a
default looks like.

**Recommended scope:** the intrinsic facts only — container or leaf, display
name, and the three icons — as one `nodeKinds [nodeTypeCount]nodeKind` table,
with a test that no entry is zero. The behaviour tables (loaders, menus,
rights, scripting) stay in their feature files, where they belong.
`docs/decisions.md` § By design closed *package* restructuring of
`internal/tui`; this changes no package. Do it with B1, or skip it and
complete the switch.

---

## Docs

- **D1 — `ARCHITECTURE.md` § How a query runs** (line 66) says package-level
  `Execute`/`ExecuteWithPlan` check out a pooled connection. There is no
  package-level `ExecuteWithPlan`: plan capture and row sinks are
  Session-only (`internal/query/executor.go`, `Execute`'s comment). Say
  "package-level `Execute`", and that `runScript`'s plan capture is reached
  only from a Session.
- **D2 — with B2, B3:** prune `~/go/gosmo/OPEN-THREADS.md` § Three live tests
  fail on `HEAD` to the Msg 701 note.
- **D3 — with C2:** the gosmo `CLAUDE.md` rule.

---

## Questions for the author

- **Q1 — The Details pane caches every node it ever showed, for the
  connection's life.** `db.cache[node] = result`
  (`internal/tui/detail_browser_runs.go:213`) is evicted only by Refresh,
  Reload or disconnect, so arrowing through a large server holds every grid
  seen, and a revisited node shows data as old as its first visit. Is that
  deliberate (SSMS-like, cheap reselect)? If so, a line in
  `docs/decisions.md`. If not, an LRU cap (a few hundred nodes) bounds the
  memory; an age limit would also bound the staleness.
- **Q2 — `User.LoginName` from a listing.** `Users` could fill it with a LEFT
  JOIN to `sys.server_principals` on SID, as `UserByName` does — except on
  Azure SQL Database, where a user database cannot see server principals.
  Options: (a) join where the server allows it, leaving it "" on Azure SQL
  Database, documented; (b) move the three login fields to a `UserLogin`
  value `UserByName` returns alongside; (c) leave it and document it under
  C2's rule. (b) is the breaking one the waiver allows; (a) is the cheapest.
- **Q3 — Job History leaf:** 100 most recent runs (today), or each job's
  latest run (a `ROW_NUMBER() OVER (PARTITION BY job_id …)` read)? C3 only
  fixes the comment.
- **Q4 — Show a log's "unlimited" maximum size as Unlimited?** SQL Server
  stores a log's unlimited `max_size` as 268435456 pages (2 TB), not -1.
  Probed on 17 with a throwaway database: `MODIFY FILE (… MAXSIZE =
  UNLIMITED)` on the log leaves 268435456, and HealthClinic's log (created
  with the default) holds the same. `normalizeFileGrowth`
  (`~/go/gosmo/database_files.go:495`) maps only -1 to `MaxSizeKB = -1`, so
  Database Properties ▸ Files shows such a log as "2097152 MB"
  (`maxSizeText`, `internal/tui/database_props_files.go:210`), and choosing
  Unlimited for a log reopens as Limited to 2097152 MB. As far as this review
  recalls, SSMS shows the same "Limited to 2097152 MB" — so today's behaviour
  is SSMS parity, not a bug; check SSMS 21 before deciding. If Unlimited is
  wanted: pass the file type into `normalizeFileGrowth` and map LOG/268435456
  to -1 in all three file reads (a log capped at exactly 2 TB is
  indistinguishable on the server, and 2 TB is the log ceiling, so nothing is
  lost); offline scanner case plus mutation check, live on 13/14/17, tmux
  round trip Unlimited → 100 MB → Unlimited. If parity wins, a line in
  `docs/decisions.md`.

---

## Author's answers (2026-10-24)

- **B2:** build `FileGroups` from `Files` (the recommended fix).
- **A2:** yes — do it with B1.
- **Q1:** deliberate — a `docs/decisions.md` line, no cap.
- **Q2:** (a) — join where the server allows it, "" on Azure SQL Database,
  documented.
- **Q3:** keep the 100 most recent runs; C3 fixes only the comment.
- **Q4:** keep SSMS parity — a `docs/decisions.md` line, no code change.

---

## Release hygiene

### H1 — carried

gosmo `HEAD` is 22 commits past `v0.1.0` with no CHANGELOG entry. C1 goes
under **Migrating**. Only when a release is asked for.

---

## Recommended implementation order

| # | Item | Repo | Why here |
|---|------|------|----------|
| 1 | ✅ **B2** `FileGroups` from `Files`, + **B3** stale test, + **D2** — done 2026-10-24 | gosmo | Clears two of the three live failures, so the `livedb` suite is green again before A1 leans on it. |
| 2 | ✅ **B1** Details pane type names, with **A2** (`nodeKinds` in `internal/tui/tree_node_kinds.go`) — done 2026-10-24 | gossms | Visible on most leaves, confirmed live; decide A2 first, since it changes how B1 is done. |
| 3 | **Q4** log Unlimited max size, once answered | gosmo → gossms | Same file as B2 if it goes ahead; one function. |
| 4 | **C1** typed change-tracking and password-policy arguments | gosmo → gossms | Breaking and mechanical; one sitting, one tag's worth of Migrating notes. |
| 5 | **C2** `JobName` + the rule (**D3**), after **Q2** is answered | gosmo | Rule first, so A1 and later work follow it. |
| 6 | **A1** RPC writes spike | gosmo → gossms | Largest change; needs the green `livedb` suite from step 2 and the sweep on 13. |
| 7 | **S1** `procParamType` fast path | gosmo | Trivial; any commit touching `proc_read.go` (A1 does). |
| 8 | **B4**, **C3**, **D1** | gossms | Small, independent; any time. |
| 9 | **Q1**, **Q3** | gossms | Wait for the author; each is a decisions.md line or a small change. |
| 10 | **H1** changelog, tags | gosmo → gossms | Only when a release is asked for. |

Each step is done only when:

- `go build`, `go test ./...`, `gofmt` and `go vet` are clean in both repos
  (`-tags livedb` vet in gosmo after any rename);
- the live checks named in the item have run;
- for a user-visible change (B1, B4, Q4 if changed), the built binary has been
  driven in tmux, per `docs/testing.md`.
