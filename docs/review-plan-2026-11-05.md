# Fix plan — 2026-11-05

The twenty open items in gossms (`docs/open-threads.md`) and gosmo
(`~/go/gosmo/OPEN-THREADS.md`) that can be fixed with the estate as it is.
Delete this file once its items are done, and delete each item's entry in the
open-threads file it came from when it is fixed.

**Left out on purpose:**
- Verification gaps that need an instance the estate lacks: V2–V8 (Azure SQL
  Database, the Managed Instance, which is down, Standard, Web and Express
  editions), Windows shells, read-only routing, Entra, multi-volume hosts,
  multi-group CPUs, EKM, backups `TO URL`, PolyBase, majors 15 and 16, and a
  case-sensitive server.
- Whole features: the Generate Scripts wizard and more Format Document styles.
- The author's own calls: moving the rest of the CLI off the `Catalog` seam.
- Anything already recorded as settled in `docs/decisions.md` or as
  "do not re-raise" in gosmo.

Order: real bugs first, then wrong output, then contrast, then gaps and
cleanups. **G** = gosmo, **S** = gossms. A gosmo change is built and tested in
`~/go/gosmo` first (the `dev-with-local-gosmo` skill).

---

## Implementation order

Items that touch the same files or share a probe are done together. Each step
ends green (format, vet, both test suites, its live check) before the next
one starts.

1. **F7** — Make the live suite skip on Msg 701 first. Every later step's
   live run then reads red only for a real failure.
2. **F6** — Start the `-count=100` loop on 14 in the background. Read the
   results whenever it fails, and fix it in its own step.
3. **F8** — A small fix contained in `connect_dialog.go`.
4. **F12 → F13** — Two palette roles plus their `theme.Style…` functions,
   as one change to the palette and the contrast test.
5. **F14** — Drive both restylings with the mouse in the same tmux session
   used for F12/F13.
6. **F2 → F3** — The same "visibility hides rows" problem. Settle the gosmo
   VIEW DEFINITION probe and the comment wording in F2, then reuse them for the
   CLI warning and hint in F3.
7. **F4 → F5** — Both are `agent context`. Add the two gosmo probes, then fix
   the target-database rows, and run the live permission checks once for both.
8. **F1** — The largest gosmo change (job reads through `sp_help_job*`).
   It comes after the smaller permission work because it reuses the
   sysadmin-probe pattern. Run the version sweep on 13 afterwards.
9. **F10 → F11** — Both are sequences: export `LastUsedValue`, then carry
   the values as strings in the same dialog.
10. **F15 → F16** — Both are application roles: gosmo `ScriptApplicationRole`
    plus the Script menu, then the owner pickers.
11. **F9** — A gosmo log-floor read behind the existing `hasLogInfo` gate,
    then the Shrink note.
12. **F18** — Extended properties, one kind per commit, each replayed live.
13. **F17** — Starts by investigating go-mssqldb's COLLATE support, which
    decides between the two fixes.
14. **F19** — Mechanical: one family at a time, each driven in tmux.
15. **F20** — Only alongside a future audit-specification change; otherwise
    leave it.

## A. Functional bugs

### F1 — `job` commands and the Agent node need sysadmin (G+S)
**Problem:** `Jobs`, `JobByName`, `Steps`, `History` and `SearchJobHistory`
SELECT from msdb's `sysjobservers`/`sysjobactivity`/`sysjobhistory`. A
SQLAgentReaderRole/UserRole member gets Msg 229, which is exit 9 on the command
line and an empty or failed Agent node in the TUI.
**Fix:** in `~/go/gosmo/agent_job.go`, when the login is not sysadmin
(`IS_SRVROLEMEMBER('sysadmin') = 0`), read through `sp_help_job`,
`sp_help_jobstep` and `sp_help_jobhistory` (or the `sysjobs_view` family), and
map those rows onto the same structs. Keep the direct catalog read for
sysadmin. It is faster, and it is what the tests pin now. SQLAgentUserRole then
sees only its own jobs, as SSMS does.
**Verify:** on 17, `job list` / `job history` / the TUI Agent node as a
login in each of the three Agent roles. Run `TestLiveVersionSweep` on 13.

### F2 — A DROP script silently loses its schema-bound dependents comment (G)
**Problem:** `schemaBoundDependents` (`~/go/gosmo/scripter_table.go`) gets no
rows for a login without VIEW DEFINITION, so the warning that the DROP will fail
with Msg 3729 goes missing while the script itself looks complete.
**Fix:** probe `HAS_PERMS_BY_NAME(db, 'DATABASE', 'VIEW DEFINITION')`. When it
is missing, write a comment saying the dependents could not be read, and give
the reason. A comment is better than an error here: refusing would block a
script whose CREATE half is still correct.
**Verify:** a live test as a db_datareader login on 13/14/17: the comment is
present and the script is otherwise unchanged. Run the TUI Script As and
`script object` on the same table.

### F3 — `object dependencies`' permission hint is wrong (S)
**Problem:** the PERMISSION_DENIED hint and `docs/cli.md` § Commands say only
db_owner can read `sys.sql_expression_dependencies` by default. Live, a
db_datareader login reads it and gets an *empty* set, because metadata
visibility hides the edges.
**Fix:** first find which setup actually produces exit 9 (try an explicit
DENY SELECT on the view). Rewrite the hint and the row to describe that
setup. Then add the same "visibility hides rows" note as F2. When VIEW
DEFINITION is missing, warn on stderr or in the JSON envelope that the
result may be incomplete, instead of returning a silent empty list.
**Verify:** the command line on 17 as db_datareader, as db_datareader with a
DENY, and as db_owner. Check the exit codes against `docs/cli.md`.

### F4 — `agent context` reports false missing permissions (G+S)
**Problem:** CREATE DATABASE granted in master alone reads as a lack of
CREATE ANY DATABASE (`db create`, `maint restore`), and IMPERSONATE ANY LOGIN
is never probed.
**Fix:** add a gosmo probe for CREATE DATABASE as a database-level right in
master and for IMPERSONATE ANY LOGIN. Have `agent context` treat either form
as satisfying the requirement.
**Verify:** on 17, a login granted only CREATE DATABASE in master: the row is
no longer in `missing_permissions`, and `db create` succeeds.

### F5 — `agent context` answers `maint backup`/`checkdb` for the wrong database (S)
**Problem:** both rows answer for the connection's database, not for the
`<database>` argument the command will act on.
**Fix:** take the target database from the argument (or the `--database`
the command uses) and probe BACKUP DATABASE / db_owner there.
**Verify:** connect to master as a login that is db_backupoperator in one user
database only. The row is right for that database and for another.

### F6 — `ScriptDatabase` intermittently drops `SET CHANGE_TRACKING` on 14 (G)
**Problem:** about 1 run in 18–30 of `TestLiveScriptDatabaseOptionsRoundTrip`
fails. The cause is undiagnosed, and the diagnostics are already in place.
**Fix:** loop the test (`-count=100`) on 14 until it fails and read the logged
state. Suspect a read of `sys.change_tracking_databases` racing the test's
own `ALTER DATABASE`, or a stale plan for the server-scope read. If the cause
proves to be a race, read change tracking from inside the database instead,
as is already done for the other options.
**Verify:** 200 runs on 14 with no failure, and 13 and 17 still green.

### F7 — `TestLiveScriptTableKinds` and friends fail Msg 701 under memory pressure (G, tests)
**Problem:** the host's free memory, not gosmo, fails memory-optimized replays
late in a full-suite run on 13 and 14. That makes the suite red for no code
reason.
**Fix:** a `skipOnHostMemory(t, err)` helper. On Msg 701 it calls `t.Skip`
with the pool name. Apply it only to the memory-optimized replay paths, so any
other error still fails.
**Verify:** a full `livedb` run on 13 is green or skips with a named reason.

## B. Wrong or clipped display

### F8 — A History row clips the login off (S)
**Problem:** `matchServerWidth` 14 + `matchDatabaseWidth` 8
(`internal/tui/connect_dialog.go`) fill the 30-column pane without counting the
ListBox's two-column marker, so a long server and database cut `,sa`.
**Fix:** make the login part never clip. Budget the server and database from
`paneWidth - marker - DisplayWidth(","+login)`, shrinking the database first,
then the server.
**Verify:** a unit test with `win10cli.fritz.box` / `HealthClinic` / `sa`,
then the tmux harness to see the row.

### F9 — Shrink > Files shows a log's used space as its minimum (G+S)
**Problem:** a log shrinks only down to its last active VLF, so "Minimum is
N MB" is a lower bound, not the floor.
**Fix:** gosmo already gates `sys.dm_db_log_info` (`hasLogInfo`,
`~/go/gosmo/database_space.go`). Add a read of
`MAX(vlf_begin_offset + vlf_size_mb)` over the active VLFs (`DBCC LOGINFO`
before 2016 SP2, or no figure there). gossms's `shrink_files_dialog.go` shows
that figure as the real minimum for a log file.
**Verify:** on 13 (SP3) and 17, a log with an active tail VLF: the dialog
figure matches what `DBCC SHRINKFILE` actually reaches.

### F10 — Sequence Properties has no Last Used Value row (G+S)
**Problem:** gosmo's `Sequence.noLastUsed` is unexported, and the value is
not exposed.
**Fix:** export `LastUsedValue string` (empty when the server predates it or
nothing has been drawn) in `~/go/gosmo/sequence_synonym.go`. Add a static row
in Sequence Properties.
**Verify:** on 17 and 13 (the column exists on both), a fresh sequence and one
after `NEXT VALUE FOR`.

### F11 — Sequences can't hold a `decimal(38,0)` range past bigint (G+S)
**Problem:** sequence values are int64 in the dialog and write path.
**Fix:** carry values as decimal strings end to end. gosmo already scans them
as strings, so validate with `math/big` in the dialog. Compare with `big.Int`.
**Verify:** create a `decimal(38,0)` sequence with MAXVALUE 10^37 on 17 and
round-trip it through Properties.

## C. Theme contrast

### F12 — Dashboard header's instance name is unreadable in mc/retro (S)
**Problem:** `ChartCyan` on `MenuBar` in `internal/tui/dashboard/common.go`
is 2.2:1 (mc), 2.3:1 (retro) and 3.5:1 (Light).
**Fix:** add a palette role for an emphasised word on a `MenuBar` ground
(`internal/tuikit/theme/palette.go`), with a value per palette, and a
`theme.Style…` function so `TestEveryStyleMeetsTheContrastFloor` covers it.
**Verify:** the contrast test, then tmux captures in all palettes.

### F13 — Results truncation alert is 3.1:1 in mc - retro (S)
**Problem:** bold white on `Error` (`QueryPanel.resultsStatusStyle`).
**Fix:** route it through a `theme.Style…` function that the contrast test
covers. In retro, use the VGA dark red ground (or black-on-bright-red), and
keep mc's white on red only where it passes.
**Verify:** the contrast test, plus a tmux capture of a truncated result.

### F14 — Two B1 restylings never driven with the mouse (S)
**Problem:** toolbar button hover and Activity Monitor's selected tab now
call `StyleMenuSelected`, and no run has clicked either one.
**Fix:** no code change unless the run finds one. Drive both through the tmux
harness with mouse events (see `docs/testing.md`).
**Verify:** the captures show the selected style in each palette.

## D. Gaps in existing features

### F15 — Application roles have no Script menu (G+S)
**Fix:** export `ScriptApplicationRole` in gosmo (Script Database Security
already emits the one-role form privately, so lift it), and add the Script As
CREATE/DROP/DROP AND CREATE menu on the node. Never script the password.
Emit a placeholder with a comment, as the login script does.
**Verify:** a replay test in gosmo on 13/17, then the TUI menu.

### F16 — Owner pickers never offer an application role (S)
**Fix:** extend `principalNames` (`internal/tui/role_props.go`) to take
application roles. Feed it to the pickers whose securable an app role may own
(schemas: Owned Schemas, New Schema, Schema Properties). Leave out the pickers
where SQL Server refuses an app role as owner. Check each live before adding
it.
**Verify:** on 17, transfer a schema to an app role from each dialog.

### F17 — A bulk load can't carry characters outside the database's code page (G)
**Problem:** INSERT BULK declares columns without COLLATE, so char bytes are
read in the database's default code page. gosmo now refuses such a value by
name, so nothing is silently corrupted, but it still can't be loaded.
**Fix:** declare each varchar/char column with its own `COLLATE` in the
INSERT BULK column list (from `sys.columns.collation_name`), and encode the
value in that column's code page. First check that go-mssqldb's bulk copy
passes a COLLATE clause through. If it does not, the fallback is to send such
columns as nvarchar and let the server convert.
**Verify:** the 6.i reproduction (`Ж` into a Cyrillic varchar column of a
Latin1 database) on 17 and 13.

### F18 — Extended properties left out of eight script kinds (G)
**Fix:** one `extendedPropertiesAt` call each, at the right class and level,
for CLR types (class 6, SCHEMA/TYPE), rules, defaults, assemblies, plan
guides, Service Broker objects, filegroups and files.
**Verify:** a replay test per kind on 17, plus the version sweep on 13.

### F19 — Group by Schema covers only the five main families (S)
**Fix:** for each family (Sequences, Types, System Views/Procedures/Functions,
the Tables sub-folders), add one `groupedFolderTypes` entry
(`internal/tui/tree_node_kinds.go`) and its Details check
(`detail_browser_group_schema.go`).
**Verify:** a tmux run with the option on, expanding each family.

## E. Cleanup (no bug)

### F20 — Audit specification scopes duplicate their write mechanics (G)
**Fix:** an unexported `auditSpecScope{keyword, exec, windowKey}` shared by
`audit_specification.go` and `database_audit_specification.go`
(`withSpecificationDisabled`, `SetAudit`). Exported API unchanged. The open
thread says to do this only alongside another change in those files. No other
item here touches them, so it waits for a later audit change.

---

**Not in the twenty but worth a decision:** the audit log is never rotated
(`audit.jsonl`). `docs/decisions.md` records this as "No rotation yet",
deferred. The likely form is a size cap with one `.1` generation, taken under
the same lock. It is fixable, but whether to do it is the author's call.

**Before calling any item done:** `gofmt -w . && go vet ./...`,
`go test ./...` in both repos, the item's live/tmux verification above
(`docs/testing.md`), and delete the item's open-threads entry.
