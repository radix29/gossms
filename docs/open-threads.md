# Open threads

Open work — nothing else. Close an item by deleting it; add one whenever
something is knowingly left undone. Settled decisions and the "do not
re-raise" sections are not here: they live in `docs/decisions.md`. An item
that gets answered moves there rather than staying here as history.

## Version support: the policy, and how it is held

The target is **SQL Server 2016 SP1 and later**. SP1 rather than RTM because
`procedure.go`, `scripter.go` and gossms's `internal/activity/block.go` emit
`CREATE OR ALTER`.

Three real on-premises instances exist — majors **13** (`win10cli\SQL2016`,
SP3), **14** (`win10cli\SQL2017`) and **17**. There is no major **15 or 16**
and no way to run one here (no Docker, 3 GB RAM), so those two are argued from
the catalog documentation and pinned by tests. A live Azure SQL Managed
Instance is the fourth environment; it is not a major in this sense and is not
swept — see `docs/decisions.md` § Azure SQL Managed Instance.

**The standing check is `TestLiveVersionSweep`** (`~/go/gosmo/live_versionsweep_test.go`):
it calls every read gosmo exposes and reports what the server rejects. Run it
on the *oldest* instance available after any query change. A query naming a
column the instance lacks fails the whole read, and `go test ./...` says
nothing.

Refusals are gates working, not
defects: every on-premises major refuses the six Azure-only DMV reads, and 13
additionally refuses six 2017+ reads outright with `ErrUnsupportedVersion` —
the two Query Store wait reads, the three external-library reads and graph
tables. Call counts differ between instances for reasons other than version
(the logins and jobs swept are whatever each instance has), so compare
failures, not totals.

**The per-column gate table lives in gosmo** — `~/go/gosmo/OPEN-THREADS.md`
§ Version support; every gate it names is a gosmo file. What stays here is
environmental rather than library: which instances exist and what each refuses.

A sweep of 0 failures is not proof on its own — it passes just as happily if a
read was never reached. When re-verifying, confirm the reads were actually
*called*: the sweep's `call` helper takes a label, and logging it lists every
method swept.

## Release workflow: what is still open

**`brew audit --strict --online` reports "`version …` is redundant with version
scanned from URL"** (on Linux Homebrew; `brew style` and the `livecheck` block
are clean). Left as is — the homebrew job's Verify step greps for that `version`
line, so dropping it means changing the verify step too.

The shape those two jobs have to keep, and why the formula is binary, are
settled: `docs/decisions.md` § Release workflow.

## Environment: what the instances can and cannot do

- **win10cli can never be a third availability replica.**
  `SERVERPROPERTY('IsHadrEnabled')` and `IsClustered` are 0 and
  `sys.dm_os_cluster_nodes` is empty; the host is **Windows 10 Pro**, which has
  no Failover Clustering feature, and SQL Server will not enable Always On for a
  non-WSFC node. Even with it, a Windows replica in a Linux availability group
  is unsupported — a distributed AG is the only cross-platform shape. Do not try
  to add it to AAG1.
- **It can do everything an availability group is built *on top of*.**
  Mirroring endpoints, certificates, the principals owning them and the CONNECT
  grants between them are plain T-SQL with no HADR precondition, and gossms
  checks HADR only on the instance the endpoint dialog is *opened from*. That is
  what lets Add Replica's Connect read a real endpoint off a third instance.
- **It is ubusql1's mirroring peer, deliberately left that way.** win10cli holds
  a database master key, `win10cli_Cert`, ubusql1's imported `ubusql1_Cert`,
  `ubusql1_login`/`ubusql1_user`, and endpoint `AGEP` STARTED on 5022; ubusql1
  holds the matching `win10cli_*` principals and certificate. A third instance
  with a STARTED endpoint is exactly what Add Replica's Connect needs, and
  rebuilding it costs a live run. It is inert.
- **`xp_cmdshell` is on on win10cli and can never be on on ubusql1.**
  `sp_configure 'xp_cmdshell', 1` on Linux fails Msg 15392 ("not supported by
  this edition"); file moves on ubusql1/ubusql2 go through ssh. Anything
  depending on xp_cmdshell is Windows-only by construction. Linux still *lists*
  it in `sys.configurations` at 0, which is why `xpCmdshellRow` tests
  `ServerInfo.Platform` rather than the missing-option path.
- **A live AG test's teardown reaches `DROP AVAILABILITY GROUP` on a real
  cluster.** `liveDropGroupEverywhere` runs *before* the create as well as
  after, so `-liveag-create-name` is a flag that drops groups. It refuses,
  fatally, any group whose cluster type is not NONE. Know this before touching
  that test.
- **`TestLiveAvailabilityGroupOperations` deliberately skips Drop and
  RemoveReplica** against AAG1; only add/remove database, suspend/resume, the
  listener round trip and the failover refusal run there.
- **A named-instance connection to `win10cli\sql2017` fails on the *second*
  concurrent connection.** Connecting by name and then doing anything that
  needs a second pooled connection while the first is pinned — a listing whose
  rows are still open when a per-row read runs, `SecurityPolicies` being
  the one the sweep hits — fails with `acquire connection: no instance matching
  'sql2017' returned from host 'win10cli.fritz.box'`. It is the SQL Browser
  declining the second resolution, not a gosmo defect: the identical run
  against `win10cli.fritz.box:55253` is clean, and `sql2016`, whose Browser
  answers reliably, is clean by name. **Connect to 2017 by port for anything
  multi-connection**, and do not chase it as a query-compatibility finding — it
  names no column and no version.

## Fix order

The work still outstanding, ordered by priority within each subsection: bugs
and suspected defects first, then verification gaps, then nice-to-have. Each
item is a pointer — the reasoning lives in the section it names.

**IDs are permanent and are never reused.** A missing number is a closed item,
not a typo — the IDs that remain are cited from commit messages, so nothing is
ever renumbered. A new item takes the next unused number in its series,
whatever position it lands in.

**Both subsections are in priority order, not ID order.** Insert a new item
where its priority puts it, not at the end.

**Maintained on request only.** Do not regenerate this list as part of ordinary
work; the author asks for a refresh. An item is closed by *deleting* it here
when the underlying issue is fixed.

### Bugs and suspected defects

None outstanding.

### Verification gaps

None outstanding.

### Nice to have

- **N1 — IntelliSense query-tree shapes deliberately left out.**
  `sqlparse.ScopeAt` and `completion_relations.go` resolve CTEs, derived
  tables, sub-SELECTs, `UNION`/`EXCEPT`/`INTERSECT` chains, `PIVOT`/`UNPIVOT`
  output columns, and temp tables and table variables (`sqlparse.ScanBindings`,
  batch-scoped). Three shapes are still left out on purpose and answer with
  *nothing* rather than a wrong list. Table-valued function result shapes and
  `OPENJSON`/`OPENROWSET` `WITH` column lists each need their own grammar, the
  first also needing result shapes the catalog does not carry. Cross-database
  three-part chains need an inventory per database, not the two
  `completionInventory`s the provider carries. None is a defect in what
  shipped; each is its own pass if the user asks for it.

  Two limits the shipped halves carry, both deliberate: a temp table survives
  `GO` in the real session but its binding does not, so a `CREATE TABLE #t` in
  one batch and a `SELECT ... FROM #t` in the next answers with nothing; and a
  `PIVOT`'s new columns are untyped, since the aggregate decides the type and
  the package does not model aggregates.

- **N3 — `BatchEndOffset`'s forward scan is still O(script).**
  The prefix scan is incremental (`sqlparse.PrefixCache`), but
  `sqlparse.BatchEndOffset` (`internal/tui/sqlparse/token.go`) still lexes
  forward from the cursor to the next bare `GO`, and to the end of the buffer
  when there is none — so a cursor in the last batch of a large script pays for
  nothing, and one near the top pays for everything below it. It runs once per
  keystroke while the popup is open, on the same UI goroutine.
  Deliberately not in `PrefixCache`: the boundaries it needs are *ahead* of
  the cursor, which is exactly the half `PrefixCache` does not record, and
  caching them would have to be invalidated by every edit below the cursor
  rather than above it. Not currently measured — the trigger is a benchmark
  showing it matters, with the cursor well above the end.
