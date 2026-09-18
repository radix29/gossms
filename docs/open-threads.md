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

**gosmo must be tagged before gossms can be tagged at all.** `go.mod` requires
`v0.0.13`, which predates `AcquireConn`, `ShowplanColumn`, the
`LinkedServer`/`ServerRole` work and the Service Broker routing families that
`internal/tui` now calls — so a build with the `replace` commented out does not
resolve. Tag gosmo, bump `require`, then comment the `replace` out, in that
order; `RELEASE.md` owns the steps.

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
  rows are still open when a per-row read runs, `SecurityPoliciesContext` being
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
not a typo: B1-B6 and V1-V8 have all been fixed or settled and deleted, and
the IDs that remain are cited from commit messages and from review plans, so
nothing is ever renumbered. A new item takes the next unused number in its
series, whatever position it lands in.

**Both subsections are in priority order, not ID order** — that is what the
list is for; V8 sat above V7 while both were open. Insert a new item where its
priority puts it.

**Maintained on request only.** Do not regenerate this list as part of ordinary
work; the author asks for a refresh. An item is closed by *deleting* it here
when the underlying issue is fixed.

### Bugs and suspected defects

- **B7 — ~20 gosmo `Ref` method docs still carry the over-broad `WithScript`
  claim.** Review plan P5 corrected the four prose sites it named
  (`CLAUDE.md`, `docs/decisions.md`, `dbOf`, and the authority
  bullet in `~/go/gosmo/CLAUDE.md`), but the same sentence — "the only usable
  form under a `WithScript` context" — is repeated on the `Ref` method doc
  comments themselves: `database_trigger.go:123`, `agent_job.go:247`,
  `server_trigger.go:122`, `plan_guide.go:175`, `backup_device.go:116`,
  `agent_alert.go:181`, `agent_operator.go:93`, `agent_schedule.go:188`,
  `database_credential.go:119`, `database_audit_specification.go:178`,
  `table.go:50`, `database_snapshot.go:52`, `login.go:591`,
  `server_role.go:117`, `server_config.go:103`, `statistics.go:112`,
  `availability_group.go:203`, `database.go:705`, `server.go:404` and
  neighbours. `WithScript` intercepts writes only, so a by-name read under one
  reaches the real server — `script.go:60` says so — and the claim is true only
  when there is nothing to read yet (no connection, or an object the script is
  about to create). Left undone deliberately: ~20 doc comments in a second
  repository, each needing the narrow wording, is its own pass. Mechanical, no
  code change, and `go doc` is where a caller reads it, so it is worth doing.

### Verification gaps

None outstanding.

### Nice to have

- **N1 — IntelliSense query-tree shapes deliberately left out.**
  `sqlparse.ScopeAt` and `completion_relations.go` resolve CTEs, derived
  tables, sub-SELECTs and `UNION`/`EXCEPT`/`INTERSECT` chains; five shapes
  were left out on purpose and answer with *nothing* rather than a wrong
  list. Temp tables and table variables (`#t`, `@t`) need `CREATE TABLE` /
  `DECLARE` shapes plus sigils the tokenizer drops, and they outlive the
  statement, so they need a batch-wide binding table the package does not
  have. Table-valued function result shapes, `PIVOT`/`UNPIVOT` output
  columns, and `OPENJSON`/`OPENROWSET` `WITH` column lists each need their
  own grammar. Cross-database three-part chains need an inventory per
  database, not the two `completionInventory`s the provider carries. None is
  a defect in what shipped; each is its own pass if the user asks for it.

- **N2 — completion's prefix scan is O(script) on every keystroke.**
  `sqlparse.ScanPrefix` (`internal/tui/sqlparse/token.go:440`) lexes the whole
  prefix with tokens off before tokenizing the cursor's own statement, because
  an unterminated block comment thousands of lines up moves where that
  statement starts. The requirement is real and the existing pass is already
  the optimised one — `BenchmarkCompletionPrefixScan_*` against
  `BenchmarkCompletionPrefixScanReference_*` in
  `internal/tui/sqlparse/bench_test.go` is the measurement. On the author's
  machine (i5-2500K, `-benchtime 200x`): **0.57 ms per keystroke on a
  100-statement script, 5.7 ms on 1000**, one alloc either way, all of it on
  the UI goroutine while the popup is open, and linear from there.
  The fix, if it is ever worth it: cache the lexer state at each top-level `;`
  and `GO` boundary keyed by `Document.Version()`
  (`internal/tuikit/controls/document.go:99`) and restart the first pass from
  the last valid boundary at or before the cursor, so an edit below it costs
  O(statement). `TokenizeRangeFrom` (`token.go:124`) already takes an explicit
  start state, so both halves exist.
  **Deliberately not scheduled.** Nobody has reported it, and the correctness
  argument means a stale cache is a silently wrong completion rather than a
  slow one. The trigger is someone editing a script big enough to feel it;
  re-run the benchmarks above first.
