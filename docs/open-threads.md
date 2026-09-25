# Open threads

Open work only. Add an item when something is knowingly left undone; close it
by deleting it. Settled answers move to `docs/decisions.md`, not history here.

## Version support: the policy, and how it is held

Target: **SQL Server 2016 SP1 and later** (SP1 because `procedure.go`,
`scripter_module.go` and `internal/activity/block.go` emit `CREATE OR ALTER`).

Live on-premises instances: majors **13** (`win10cli\SQL2016` SP3), **14**
(`win10cli\SQL2017`), **17**. No 15/16 can run here (no Docker, 3 GB RAM), so
those are argued from catalog docs and pinned by tests. Azure SQL Managed
Instance is a fourth environment, not swept — `docs/decisions.md` § Azure SQL
Managed Instance.

**Standing check: `TestLiveVersionSweep`**
(`~/go/gosmo/live_versionsweep_test.go`) calls every gosmo read and reports
rejections. Run it on the *oldest* instance after any query change — a missing
column fails the whole read and `go test ./...` says nothing. Expected
refusals: every on-prem major refuses the six Azure-only DMV reads; 13 also
refuses six 2017+ reads with `ErrUnsupportedVersion` (two Query Store wait
reads, three external-library reads, graph tables). Compare failures, not call
totals (those vary with each instance's logins/jobs). Zero failures proves
nothing if a read was never reached — log the `call` labels to confirm.

The per-column gate table is gosmo's (`~/go/gosmo/OPEN-THREADS.md` § Version
support); this section covers only the environment.

## Release workflow: what is still open

**The homebrew job's Verify step hasn't run in CI.** The formula lost its
`version` line on 2026-09-24 and Verify now checks the archive URLs' tag.
Dry-run locally against a fake remote: passes a correct formula, fails
missing/stale/half-old ones; `brew audit --strict --online` clean on staged
v0.0.12, `brew info` scans `0.0.12`. Check that job's log on the next tag, then
delete this entry. The settled shape: `docs/decisions.md` § Release workflow.

## Environment: what the instances can and cannot do

- **win10cli can never be an availability replica.** `IsHadrEnabled` and
  `IsClustered` are 0, `sys.dm_os_cluster_nodes` is empty, and Windows 10 Pro
  has no Failover Clustering; a Windows replica in a Linux AG is unsupported
  anyway (only a distributed AG crosses platforms). Don't add it to AAG1.
- **It can host everything an AG is built on** — endpoints, certificates, their
  principals and CONNECT grants need no HADR; gossms checks HADR only on the
  instance the endpoint dialog opens from. That lets Add Replica's Connect read
  a real endpoint off a third instance.
- **It is ubusql1's mirroring peer, deliberately.** win10cli: database master
  key, `win10cli_Cert`, imported `ubusql1_Cert`, `ubusql1_login`/`ubusql1_user`,
  endpoint `AGEP` STARTED on 5022; ubusql1 has the matching `win10cli_*`
  objects. Inert; rebuilding costs a live run. Leave it.
- **`xp_cmdshell` is on on win10cli and impossible on ubusql1** (Linux: Msg
  15392); file moves on ubusql1/2 go through ssh. Linux still lists it in
  `sys.configurations` at 0, so `xpCmdshellRow` tests `ServerInfo.Platform`.
- **A live AG test's teardown runs `DROP AVAILABILITY GROUP` on a real
  cluster** — `liveDropGroupEverywhere` runs before the create too, so
  `-liveag-create-name` drops groups. It fatally refuses any group whose cluster
  type isn't NONE.
- **`TestLiveAvailabilityGroupOperations` skips Drop and RemoveReplica** on
  AAG1; only add/remove database, suspend/resume, listener round trip and the
  failover refusal run there.

## Fix order

Outstanding work by priority within each subsection. Each item points to where
the reasoning lives. **IDs are permanent, never reused or renumbered** (commit
messages cite them); a new item takes the next unused number and is inserted by
priority. **Maintained on request only** — don't regenerate it in ordinary
work; close an item by deleting it when fixed.

### Bugs and suspected defects

None outstanding.

### Verification gaps

None outstanding.

### Nice to have

- **N1 — IntelliSense query-tree shapes deliberately left out.**
  `sqlparse.ScopeAt`/`completion_relations.go` resolve CTEs, derived tables,
  sub-SELECTs, set-operator chains, `PIVOT`/`UNPIVOT` outputs, temp tables and
  table variables (`sqlparse.ScanBindings`, batch-scoped). Left out, answering
  *nothing* rather than a wrong list: table-valued function result shapes
  (need a grammar and catalog shapes it lacks), `OPENJSON`/`OPENROWSET` `WITH`
  lists (own grammar), cross-database three-part chains (need an inventory per
  database). Deliberate limits of what shipped: a temp-table binding doesn't
  survive `GO` (the table does), and `PIVOT` columns are untyped (aggregates
  aren't modelled). Each is its own pass if asked.
- **N3 — `BatchEndOffset`'s forward scan is O(script).** `sqlparse.PrefixCache`
  made the prefix scan incremental, but `sqlparse.BatchEndOffset`
  (`internal/tui/sqlparse/token.go`) still lexes from the cursor to the next
  bare `GO` (or buffer end), once per keystroke while the popup is open, on the
  UI goroutine. Not in `PrefixCache` because the boundaries are *ahead* of the
  cursor and would be invalidated by edits below it. Unmeasured; trigger is a
  benchmark with the cursor well above the end showing it matters.
- **N4 — Wrap mode re-segments the whole document per keystroke.**
  `Editor.buildVisualLines` (`internal/tuikit/controls/editor_wrap.go`)
  memoises on the document version, which every edit bumps, and
  `visualIndexForCursor` scans every visual row. 2026-09-24, 20 k-line script:
  6.5 ms per keystroke wrapped vs 0.45 ms unwrapped — inside a frame, not acted
  on. Fix when a measurement passes ~16 ms: re-wrap only edited lines, keep a
  per-line visual-row prefix sum.
