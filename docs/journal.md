# Engineering journal

Dated record of the work behind goSSMS and gosmo **since the current tag**:
what was built, what bugs were found and how, and which decisions were made
deliberately. Trimmed at each release — entries for work that has shipped come
out, since `CHANGELOG.md` records what shipped and git history keeps the rest.
Trimmed to `v0.0.8` (2026-08-25) on 2026-08-25.

Nothing here is required reading. `CLAUDE.md` carries the rules that still
apply; `docs/open-threads.md` carries the work that is still open. Newest
entries at the bottom. A `slug` under a heading is a note's name from the
Claude Code memory store this file was migrated out of, kept for older
cross-references.

The v0.0.8 entries are in git history at the `v0.0.8` tag and its parent
commits — the least-privilege audit and its five phases (splitting the
connect-time read, the capability probe, the read paths that stopped lying,
the action gate, and the refusal-to-required-right table), the
`Script <object> as ▸` cascade and the menu submenus under it, gosmo's
scripter growing the rest of the object families, New Index and New
Statistics, the four object families that arrived in the tree, the
missing-index banner and `.sqlplan`, the Log File Viewer's search and
Recycle, job-step reordering, Column Rename and the object-op gaps beside
it, the ten by-name finders and the Schema Properties summary query, the
Object Explorer filter's server-side push-down, the write-path Properties
page tests in five phases and the fake-driver harness they needed, and the
cross-repo review passes of 2026-08-19 through 2026-08-25.

The v0.0.7 entries are at the `v0.0.7` tag — Always On in seven phases,
Backup and Restore learning to browse the server's own filesystem, the
Restore File Locations view, the SQL Server / Agent log viewer, the Object
Explorer folder filter, general Delete and Rename, the text-encoding work
behind File > Open and Save, the small-terminal dialog thread, the
grid-cursor sweep, and the busy-latch passes behind `safegoRepair`.

## The job step command became an editor, not a field

2026-08-26. A job step's `command` is a whole T-SQL script, and both places
that edited one — Job Properties > Steps and New Job > Steps — offered a
60-column single-line `propsheet.Text` row. Replaced with
`propsheet.NewEditorRow`, wrapping the same `controls.Editor` the query panel
uses: SQL highlighting, the line-number gutter (so a "line 12" error can be
found), and 11 lines of box.

`EditorRow` is `GridRow`'s problem again, one control further on: `Editor`
answers `true` to every arrow key, and to `Tab`, which it indents with. Wrapped
verbatim as a form row that would be a keyboard trap — `Tab` is the sheet's only
unconditional way out of a field. So the row refuses `Tab`/`Backtab` outright
(no indent inside a property sheet), and for the movement keys measures what
actually moved — cursor, then scroll, then the document version, since
`Ctrl+Shift+Up/Down` moves lines rather than the caret. Detect movement, never
predict it.

Two things the row does that are not obvious: its dirty baseline is read back
out of the editor after `SetText`, not from the string handed in, because
`SetText` normalizes (tabs expanded, CRLF folded) and a baseline taken from the
source marks the row dirty on load — File > Open's bug in row form; and `Dirty()`
short-circuits on the document's version counter, so an untouched page doesn't
rebuild the whole script on every draw.

Verified live against win10cli: created a throwaway job through New Job with a
three-line command, read it back out of `msdb.dbo.sysjobsteps` with the newlines
intact, added a fourth line through Job Properties > Steps > Apply, read that
back, and dropped the job.

## Query Store: gosmo's report layer, the tree's seven leaves, and the panel

2026-08-26. SSMS's seven Query Store views plus Force/Unforce Plan, built in
three stages: the data layer in gosmo, the folder and its Detail Browser grids,
then `QueryStorePanel`.

**The data layer is gosmo's** (`query_store_reports.go`), because it is SQL
Server management-object work and gossms is not the only thing that may want
it. Before a line was written, every DMV column was read off the live instance
rather than recalled — which corrected two design assumptions immediately:
`sys.query_store_wait_stats` carries no `count_executions` (so the wait report
LEFT JOINs runtime stats on plan + interval + execution type to get one), and
`sys.query_store_runtime_stats` has no total columns at all, so every "Total"
is `SUM(avg_x * count_executions)` and every "Avg" is that over
`SUM(count_executions)` — an unweighted `AVG(avg_x)` would weight a one-
execution interval like a thousand-execution one.

Three places wanted a pair of parallel tables and got a single slice of structs
instead: `qsMetricDefs` (metric → column stem → unit → first release carrying
it), `qsStatisticDefs` (statistic → the aggregate over runtime stats, and the
different one over wait stats), and gossms's own `queryStoreReports` (title →
description → default statistic → loader). Two lists that must agree cancel
their own faults out — a metric silently reading another metric's column, or a
report showing under another's title, survives every round-trip test.

Std dev is pooled across intervals rather than averaged:
`SQRT(ABS(E[X²] − E[X]²))` with both expectations execution-weighted. A plain
`AVG(stdev_x)` ignores the variance *between* intervals, which is the whole
subject of the High Variation report. The `ABS` is not covering a sign error —
the quantity is a variance, so a negative is float rounding at 1e-10 and
`SQRT` of it fails the batch.

**Regressed Queries was permanently empty, and it was a real bug.** gosmo's
default baseline is the equally long window immediately *before* `From`, which
is right for a caller that picked its own range — but a 24-hour report then
needs 48 hours of Query Store history before it can show a row, and on a young
database that reads as a broken report rather than a young one. Both surfaces
now compare the two halves of their own window
(`queryStoreRegressionOptions`). The panel's Window selector starts at 5 m and
15 m for the same reason: on an hour, the report can only see a change that
straddles the half-hour mark, and a development database's whole history is
minutes old. Verified live twice, with a regression staged three minutes apart:
`dbo.qs_read 0.04 ms → 8.53 ms, +8.49 ms, 60/60 executions`.

**One report layer, two surfaces.** Stage 2's loaders returned rendered
`[][]string` for the Detail Browser; the panel needed the query id behind each
row (to load its plans and force one) and a number to plot. Rather than a
second table of loaders beside the first, the loaders now return `qsResult` —
columns plus `qsResultRow{cells, label, value, queryID}` — and the Detail
Browser takes `.cells()`. `chartLabel` rides on the result too, because the two
ranking reports plot something other than their value column (the regression,
the variation) and a chart axis derived from the toolbar would quietly
disagree with the bars.

**The panel's reload had to keep the user's place.** `applyResult` used
`SetData`, so the reload after Force Plan reset the cursor and left the plan
pane on a different query — immediately after acting on the row being looked
at. Split into `Load` (resets; the rows now mean something else after a report,
metric or window change) and `Refresh`, which goes through `redrawGrid`. Force
and Unforce reload through `Refresh`.

Verified live on a throwaway database with a two-plan workload: all seven
reports, a metric change rendering pages as KB, Force Plan naming *plan 7 for
query 3* and the report coming back with `Forced Plan = 7`, Unforce round-
tripping it, Show Plan opening the stored seek plan in a `PlanPanel`, and
Script writing `EXEC sys.sp_query_store_unforce_plan @query_id = 3,
@plan_id = 7` into an editor. Offline, the force write's *parameters* are
pinned, not just its text: `sp_query_store_force_plan` takes both ids as
parameters, so swapping them reads identically in the statement and changes a
different query on the server.

## 2026-08-26 — Detach Database

gosmo already had the whole layer (`database_attach.go`, untracked at the time
of writing): `DetachDatabaseContext` with `DetachOptions`, `AttachDatabaseContext`
with `AttachSpec`, and `DetachedDatabaseInfoContext` over the undocumented
`DBCC CHECKPRIMARYFILE`. gossms had none of it. This built the Detach half —
`internal/tui/detach_database_dialog.go`, on the `newObjectDialog` shell, plus
the context-menu entry on a database node.

**The file grid is the feature, not decoration.** Detaching leaves the files on
disk and simultaneously destroys the only record of where they are that a
client can read: `sys.database_files` is behind a `USE`, and a detached
database has none to `USE`. So the page reads the file list up front and shows
it, primary data file first. gosmo orders by `type_desc`, which sorts `LOG`
ahead of `ROWS` — a grid built straight from that hands the user the log file's
path as the one to attach from, so `sortDatabaseFiles` reorders it and
`TestDetachFileGridStartsAtThePrimaryDataFile` pins it.

**`NewGridRow` needs three lines of chrome.** Sized `len(files)+1`, the grid
drew its header, its rule and its "2 rows" footer and no files at all — a live
capture caught it, nothing in the tests could have.

**The session count includes goSSMS's own pooled connections, and that is
correct.** `Database.withConn` returns a connection to the pool still pinned to
the database it ran `USE` on, so merely browsing a database leaves an idle
session in it — and an idle session blocks `sp_detach_db` exactly as a user's
would. Preflight refuses while sessions are present and names Drop connections
as the remedy; the note under that checkbox says outright that goSSMS's own
browsing counts, because otherwise "1 session is using the database" on a
database nobody else has touched reads as a bug.

**Both flags are inverted, and the tests pin the statement rather than the
option struct.** `@skipchecks` is the negation of "update statistics" and
`@keepfulltextindexfile` the negation of "drop the full-text index files", and
both are nvarchar `'true'`/`'false'` rather than bits. A test asserting
`DetachOptions` would agree with a flipped sense; `TestDetachWithEveryOptionOff`
and `TestDetachWithEveryOptionOn` assert the text that reaches the server for
both settings of each box. Verified by mutation: negating one mapping fails
both.

Verified live on win10cli against a throwaway `DetachProbe`: Script Changes
emitted the `SET SINGLE_USER WITH ROLLBACK IMMEDIATE` and the `sp_detach_db`
call, OK removed the database from `sys.databases` with both files still on
disk (`xp_fileexist` = 1), and the tree refreshed without it. `DBCC
CHECKPRIMARYFILE (…, 3)` was run by hand against the orphaned `.mdf` and came
back with status 2 for the data file and 66 for the log — the encoding gosmo's
`detachedFileIsLog` comment documents — so the Attach-side read is good on
SQL Server 2025 too. The database was attached back and dropped.

## 2026-08-26 — Attach Database

The other half, on the same shell: `internal/tui/attach_database_dialog.go`,
plus "Attach Database..." on the Databases folder. Browse picks a primary
`.mdf` on the *server's* filesystem (`newServerFS`, the picker Backup and
Restore already use), gosmo's `DetachedDatabaseInfoContext` reads the whole
file list back out of it, the grid lets any path that has moved be corrected,
and apply runs `CREATE DATABASE ... FOR ATTACH`.

**The recorded paths are stale by definition.** A detached database's file
list says where the files were when it was detached — and files that never
moved need no Attach dialog at all. So `attachEditableFiles` replaces the
primary data file's recorded path with the one the user actually browsed to;
following the recorded path there would send the attach back to the old
location for the very file just picked somewhere else. The secondaries are
deliberately *not* rewritten by guessing a sibling directory: they are the
user's to correct, and attaching a file nobody named is worse than asking.

**A refused DBCC is not a dead end.** `DBCC CHECKPRIMARYFILE` needs the rights
DBCC needs, and `FOR ATTACH` works from the primary file alone as long as the
other files are where it recorded them. A failed read leaves the hint saying
so and `attachFilePaths` falls back to the one path; only the correcting is
lost.

**`RebuildLog` has to drop the log from the file list**, not just add the
keyword: `ATTACH_REBUILD_LOG` builds a new log and rejects a statement naming
the old one. Both halves are pinned by
`TestAttachRebuildLogDropsTheLogFile`, and the path logic sits in the pure
`attachFilePaths`/`attachEditableFiles` so it can be tested without a server.
Verified by mutation: removing the log filter or the primary-path rewrite each
fails two tests.

Verified live on win10cli with a throwaway three-file `AttachProbe`, detached
by hand: Browse opened on the instance's default data directory and listed the
orphaned files, choosing the `.mdf` filled in "AttachProbe" and three grid
rows, Script Changes emitted the three-`FILENAME` `CREATE DATABASE ... FOR
ATTACH`, and OK brought it back ONLINE with all three files at their real
paths and `sa` as owner. Dropped afterwards.

A nested `FileDialog` over a `PropertySheet` is new — nothing else opens the
file picker from a property page — and the dialog stack handles it without
changes, since `topDialog()` is just the last thing that became visible.

## 2026-08-26 — Attach's moved-files path, live on both hosts

The moved-files case was still test-only: the earlier live run attached files
that had never left their directory. Closed it by actually moving them.

`xp_cmdshell` was turned on on **win10cli** to do the move on the server host,
and left on. It cannot be turned on on **ubusql1**: `sp_configure 'xp_cmdshell', 1`
there fails Msg 15392, "not supported by this edition" — SQL Server on Linux
has no xp_cmdshell at all — so the Linux half of the test moved the files over
ssh instead.

Both runs: create a three-file database (primary + a `FG2` secondary + log,
plus a probe row), `sp_detach_db`, move all three files to another directory,
then attach through the dialog.

- **win10cli** — path typed into "Primary data file", Read File List. DBCC
  CHECKPRIMARYFILE returned all three; `attachEditableFiles` had already
  rewritten the primary to `C:\temp\gossms_moved\...` while the log and the
  `.ndf` still pointed at the old `DATA` directory, which is exactly the state
  the dialog exists to let the user fix. Corrected both by selecting the grid
  row and editing "Path of selected file".
- **ubusql1** — same shape at `/var/opt/mssql/moved`, but the primary was
  picked through **Browse**, which exercised the server file browser on POSIX
  paths and the Browse → `readFiles` continuation.

Both attached, and `sys.database_files` came back with three moved paths and
the probe row intact. Two details worth having run for real: the grid commits
the edited path on *selection change* (`commitCurrent` via
`syncFromSelection`), and the **last** edit was deliberately left sitting in
the text field when OK was pressed — `applyFns[0]`'s own `commitCurrent()` is
what carries it into the statement, and it did.

Databases dropped and both moved directories removed afterwards. Left open:
an attach whose paths are *not* corrected — SQL Server's error in the hint —
which is still test-only.

## 2026-08-26 — A read-only Properties page stops looking editable

P3's gate made a page whose writes would be refused impossible to edit —
`Form.SetReadOnly` unfocusable, unclickable, never dirty — but every row still
drew its control: a Text row its `[value]` box, a Check row its `[ ]`, a
Select its arrow, a Radio its `(o)` markers, a ToggleGrid its `[x]` cells. It
read as a form the terminal was refusing to type into. `docs/open-threads.md`
carried it as a known limit of the gate layer; this closes it.

The mechanism is one optional row capability, `ReadOnlyDrawer`
(`propsheet/common.go`), alongside `Shrinkable`/`OverlayDrawer`. Threading a
flag through `Row.Draw` would have touched every row type and every caller;
instead `Form.SetReadOnly` pushes the state into the rows that implement
`SetDrawReadOnly`, and so do `Add` and `Prepend`, since a page can build rows
after the gate has decided. No page in `internal/tui` changed.

Each row's flat face: Text/Int a label/value pair with the unit after the
value, Password just its label (its value is `""` by construction, meaning
"leave unchanged"), Select the selected item, Radio the selected option on one
line — `Height` drops to 1 with it — Check and ToggleGrid's toggle cells a
`✓`/`✗`, and ButtonsRow dimmed. `EditorRow` keeps its box, because a T-SQL
script still needs to be scrollable and selectable, and remembers the page's
own `SetReadOnly` rather than clearing it: a job step whose command is not
T-SQL is read-only for its own reason.

Two things the live run caught that the tests as first written did not.

**The affinity grid came up blank.** `SetDrawReadOnly` re-rendered through
`renderPreservingView`, and `Form.SetReadOnly` runs before the sheet has ever
laid the grid out: `SetDataPreservingView` ends in `SetSelectedCell`, whose
`ensureVisible` scrolls against a zero rect and lands past every row. Server
Properties > Processors drew its header, four blank lines and "4 rows". The
unit test missed it because `newTestToggleGrid` lays the grid out first —
`TestAReadOnlyToggleGridStillDrawsItsRows` deliberately does not, which is how
a Properties page actually builds one. Fixed by rendering with `render`; the
view is worth nothing at build time. The underlying trap —
`SetDataPreservingView` before the first layout blanks a grid — is now in
`docs/open-threads.md`.

**A 30-column label touched its value**: "Maximum concurrent connections0" on
the Connections page. `LabelWidth` is exactly what a label may occupy, so the
shared `drawFlatValue` starts the value one column past it. `StaticRow` now
draws through the same helper, which moves every static value column one to
the right and puts it where the input boxes on the same page already were.

Verified live against win10cli through a throwaway `gossms_ro_test` login
holding only VIEW SERVER STATE/ANY DEFINITION/ANY DATABASE, A/B'd against a
binary built from HEAD: the same five Server Properties pages, `[x]`/`[ ]` and
`[0           ]` before, `✓`/`✗` and flat values after, with the grids intact
and the writable pages unchanged as `sa`. Login dropped afterwards.

## 2026-08-26 — `ensureVisible` on a grid with no viewport

The trap the read-only work left in `docs/open-threads.md`, fixed at its
source. `DataGrid.ensureVisible(dataH)` is called with `rect.H - 3`, so a grid
that has never been laid out passes **-3**: `selRow >= scrollRow + dataH` is
then true for every row, the scroll jumps past the end of the list, and the
next Draw paints the header and the "N rows" status over blank lines. Both
guards are one line each — `ensureVisible` returns when `dataH <= 0`,
`ensureVisibleCol` when the available width is not positive.

**The column half was not in the original report and is the same bug.**
`ensureVisibleCol` computes `avail := rect.W - gutterWidth()`, negative before
layout, and walks `scrollCol` all the way up to `selCol` — so a
`SetSelectedCell(row, 1)` on an unlaid-out grid scrolls the first column off as
well. The vertical guard alone left
`TestSelectionOnAnUnlaidOutGridDoesNotScrollPastTheRows` failing on the two
cell-cursor cases, which is how it surfaced; a test asserting `ScrollRow` only
would have shipped half a fix.

Covered by `datagrid_zerorect_test.go`, one subtest per caller that reaches
`ensureVisible` before layout — `SetSelectedRow`, `SetSelectedCell` and
`SetDataPreservingView` — each asserting `ScrollRow`/`ScrollCol` are still 0
*and* that the rows actually paint once real bounds arrive (a `runeScreen`
captures the text; the three-row fixture is what makes the blanking visible,
since a 30-row grid scrolled past its cursor still draws *something*).
`TestSelectionOnALaidOutGridStillScrolls` pins that the guards cost a real
viewport nothing. Both guards were mutation-checked by removing them.

Verified live on win10cli: Server Properties > Permissions, whose two grids and
`ToggleGridRow` are the `SetDataPreservingView` callers — both lists rendered,
a wheel-scrolled permissions grid kept its position through a state toggle
(`ALTER ANY ENDPOINT` → Grant, four rows down), and the query panel's results
grid scrolled normally. Nothing applied.

## 2026-08-26 — A database's file paths, readable in any state

`Database.FilesContext` reads `sys.database_files` through a `USE`, and an
OFFLINE, SUSPECT or RECOVERY_PENDING database refuses the `USE`. So the Detach
dialog's file grid — the whole record of where a database's files went, once
the detach has removed the only catalog row naming them — was blank in exactly
the state someone is most likely to be detaching from. The page said so
instead of showing an empty grid, which is honest and not much use.

gosmo gained `Server.DatabaseFiles`/`DatabaseFilesContext`, the same read off
`sys.master_files`, which is server-scoped and answers for a database in any
state. `FileGroup` is always `""` there: `sys.filegroups` is database-scoped
and cannot be joined from the server catalog, which is also why this does not
simply replace the database-scoped read. `detachPrefetch` now falls back to it
whenever the first read produced nothing.

Three live tests in gosmo (`live_masterfiles_test.go`, `-tags livedb`): the
two reads agree field by field while the database is ONLINE, the server-scoped
one still answers after `SET OFFLINE` — with an assertion that
`FilesContext` *fails* there, so the day the `USE` stops being required this
method's reason to exist is questioned rather than silently gone — and an
unknown database reads as no rows rather than an error.

Two mutants killed on the gossms half: dropping the fallback (the OFFLINE page
lists nothing) and running it unconditionally (the ONLINE page loses the
filegroup column, which only `sys.database_files` can fill).

Verified live on win10cli against a three-file database put OFFLINE: Detach
Database listed all three, primary data file first.

## 2026-08-26 — The uncorrected attach path, and what the server's answer looks like

The last untested corner of Attach — pressing OK with a secondary or log path
still pointing where the files no longer are — turned out to answer two
questions at once. The file list *does* survive the failure (the dialog stays
open, the grid keeps its rows, the paths can be corrected and OK pressed
again), and SQL Server's error is unusable where it lands:

    gosmo: attach database "zz_attach_probe": mssql: Unable to open the physical file "C:\Program Files\Microsof

The dialog's message line is one line. The whole information content of that
error is a long path, and the line clips it a few characters before the part
that says which file.

So the attach now probes each path with `Server.FileSystemExistsContext` — the
same `xp_fileexist` behind Browse — from inside `applyFns[0]`, on the
background goroutine, and refuses naming the files rather than the paths:

    not on the server: zz_attach_probe_log.ldf, zz_attach_probe_2.ndf — correct the paths in the Files grid

Three rules, each with a test and both directions of the scripting guard
mutation-checked. `gosmo.Scripting(ctx)` skips the probe: scripting the attach
now and copying the files later is a legitimate order to do this in. A probe
that *fails* is not a refusal — `xp_fileexist` can be denied where the attach
is not, and the attach then reports whatever it finds. And the probe responses
in the test are `arg:`-scoped, since every probe runs the identical statement
and one unscoped answer would serve every file.

Verified live on win10cli, `xp_cmdshell` moving all three files of a detached
throwaway database to `C:\SQLTEST\B`: OK with the two stale paths gave the
refusal above, in full, inside the line; correcting both through the grid and
pressing OK attached the database. Both databases and the directory dropped
afterwards.

## 2026-08-26 — New Login grows the other three sources

`docs/open-threads.md` had it as "the gosmo half is done, the gossms UI is
not": `CreateLoginOptions.Source` covered SQL, Windows, external provider,
certificate and asymmetric key, and the New Login dialog offered two of them.

The General page now offers all five as one radio group, labelled with
`loginAuthLabel`'s own wording so a login created here reads the same way when
reopened in Login Properties. The group drives the rest of the page, which is
what needed the one tuikit addition: **`propsheet.RadioRow.SetOnChange`**,
mirroring `SelectRow`'s down to firing from `Revert` — a group that drives
other rows has to drive them back too, or Ctrl+Z leaves the page showing the
password fields of a source it no longer has selected. Password and confirm
and the new Entra-object-id row are `SetEnabled`-gated on the source; the
"Mapped to" picker has its *items* replaced instead, between master's
certificates and its asymmetric keys, because a certificate name left showing
under "Windows Authentication" is exactly what the apply would then have to
guess about.

gosmo gained two things. `Database.AsymmetricKeys`/`AsymmetricKeyByName`
(`asymmetric_key.go`), for the picker — the read side only, and deliberately:
CREATE ASYMMETRIC KEY imports FROM FILE, FROM ASSEMBLY or FROM PROVIDER, all
of which read the server's own filesystem, so unlike CREATE CERTIFICATE ...
FROM BINARY there is nothing a client library can create. And
`CreateLoginOptions.ObjectID`, emitting `CREATE LOGIN ... FROM EXTERNAL
PROVIDER WITH OBJECT_ID = '...'` — the one WITH option that clause takes, so
`DEFAULT_DATABASE` still cannot join it and still moves to the following ALTER.
A stray `ObjectID` on any other source is an error rather than a silently
dropped field.

Three things settled by probing rather than reasoning:

- **The OBJECT_ID grammar is real.** win10cli has no Entra, so a live create is
  impossible — but the bare `FROM EXTERNAL PROVIDER` and the OBJECT_ID form
  fail with the *same* Msg 37525, "Azure Active Directory is not configured for
  this instance". The parser accepted both. That is as far as this instance can
  take it, and further than the unit test could.
- **A mapped login can have neither a default database nor a default language**,
  in CREATE or ALTER: "Cannot use the parameter DEFAULT_LANGUAGE for a
  certificate or asymmetric key login". gosmo already refused the database; the
  page now refuses both, and only when the user actually changed one, so the
  refusal arrives before the login exists rather than after.
- **`masterMappableNames` swallows its two reads' errors on purpose.**
  `sys.certificates` and `sys.asymmetric_keys` need permission on master, and a
  login without it must still create ordinary SQL and Windows logins. An empty
  picker turns into a refusal naming what is missing, not a dialog that will
  not open.

`new_login_general_page_test.go` drives all five sources over
`fakedb_test.go` — the New-X dialogs' usual `WithScript` harness is not needed
here, since this apply reads nothing. Three mutants killed: the picker not
following the source, the mapped-defaults refusal removed, and the object id
sent for every source (which the "typed under Entra, then switched to Windows"
test catches — gosmo refuses it, so the create produces nothing at all).

Verified end to end under tmux against win10cli. Script Changes on the
certificate source produced exactly `CREATE LOGIN [gossms_ui_certlogin] FROM
CERTIFICATE [gossms_ui_cert]`; OK created it, and the asymmetric-key source
created its own. Both read back from `sys.server_principals` as
`CERTIFICATE_MAPPED_LOGIN` and `ASYMMETRIC_KEY_MAPPED_LOGIN`, and both, along
with the throwaway certificate and key, were dropped.

Separately: the xfce4-terminal line came off README's Known Issues at the
author's request. It is a property of one terminal, not of gossms, and Key
Diagnostics is the answer to it.

## 2026-08-26 — The Query Store Avg wait statistic was integer division

Found in a cross-repo review, fixed in gosmo. `qsStatisticDefs`' Avg entry
rendered the wait expression as

    SUM(ws.total_query_wait_time_ms) / NULLIF(SUM(rs.count_executions), 0)

and both of those columns are `bigint` — confirmed against win10cli's
`sys.system_columns`, along with the fact that `avg_`/`stdev_query_wait_time_ms`
are `float` and `min_`/`max_`/`last_` are not. So the quotient was integer
division. Every wait category averaging under a millisecond per execution came
back as 0, and `QueryStoreWaitCategoriesContext`'s `ORDER BY value DESC` then
tied all of them: the ranking the Query Wait Statistics report exists for was
arbitrary among exactly the categories a fast workload produces. Live, over the
real types: the old expression returns `0` where the average is `0.762` ms.

Avg was the only entry in the table without a cast — Min, Max and Total all had
one, and Std dev divides by way of `avg_`/`stdev_`, which are float already. The
cast goes on the *numerator*, inside the division; wrapping the whole quotient
instead casts a value that has already been truncated, which is the plausible
wrong fix and is why the test asserts on the text either side of the `/` rather
than on the expression as a whole.

Two tests. `TestQSWaitStatisticDefsArePinnedByName` pins all five wait
expressions by name, mirroring the runtime-stats half, which had such a pin
where the wait half had only substring checks — the substring assertions passed
both before and after the fix, which is how this survived. And
`TestQSWaitAveragesDoNotTruncateToWholeMilliseconds` walks each `/` back to the
aggregate it divides and requires a cast on any that reads a bigint wait column.
That one counts what it checked and fails on a count other than one, because a
helper that stopped recognising the expressions would otherwise pass it by
finding nothing.

Three mutants killed: the cast removed, the cast moved outside the division, and
(by the count assertion) the checker finding nothing. `go test -tags livedb -run
TestLiveQueryStore` passes against win10cli — all eleven metrics × five
statistics, both wait reports included — and dropped its throwaway database.

## 2026-08-26 — Open Query Store... stopped throwing away the panel's report

Found in the same cross-repo review. `showQueryStorePanelFor` took a report
title and, for a panel that was already open, called `ShowReport(title)`
unconditionally. The Query Store folder's own **Open Query Store...** names no
report and passes `""`; `queryStoreReportIndex` maps anything it does not
recognise — `""` included — to report 0. So the folder re-pointed an open panel
at Regressed Queries and re-ran it, losing the view and the place.

Mapping an unnamed report to the first one is right for a panel being *created*
and wrong for one already open, so the guard is on the branch rather than in
`queryStoreReportIndex` or `ShowReport`, both of which are correct as they
stand. `else if title != ""`.

The gesture matters more than it sounds: the folder sits directly above its
seven leaves in the tree, so a click on it is the most likely thing to follow
reading one of them.

`TestOpeningTheFolderKeepsAnOpenPanelOnItsReport` drives all three entry points
through `showQueryStorePanelFor` over `fakedb_test.go` and asserts both
directions — the folder leaves the panel alone, a leaf still re-points it, and
neither opens a second tab. Two mutants killed: the guard removed (the original
bug) and the guard widened to skip `ShowReport` outright, which the leaf half
catches.

A/B'd live on win10cli under tmux, against HealthClinic's real Query Store data.
Pre-fix binary: open the Tracked Queries leaf, then Open Query Store... on the
folder — the toolbar reads `Report: Regressed Queries`. Fixed binary, same
keystrokes: `Report: Tracked Queries`, one tab throughout, and clicking the
Queries With Forced Plans leaf afterwards still re-points it.

## 2026-08-26 — Attach picked the primary data file by list order

`attachEditableFiles` (`attach_database_dialog.go`) replaced the recorded path of
"the first non-log file DBCC returned" with the path the user browsed to. The
primary is file_id 1; DBCC CHECKPRIMARYFILE's row order is undocumented and
nothing says the primary comes back first. On a list that came back
secondary-first, the browsed path landed on the *secondary* and the primary kept
its stale detached path — an attach with both data files pointed somewhere the
server will not find them, which is the exact scenario the dialog exists for.

The answer belongs in gosmo, next to `LogFiles`/`DataFiles`: `DetachedDatabase.
PrimaryFile()` returns file_id 1, falling back to the first data file only when
the fileid column came back NULL (nil there would read as "this database has no
primary"). The dialog asks for it instead of scanning.

The old tests could not have caught this — `attachTestFiles()` listed the primary
first, so every one of them passed on the bug. The fixture now lists secondary,
log, primary, and the assertions address files by logical name rather than index.
With the fixture reordered, restoring the old scan fails four tests, and the
CREATE DATABASE the fake driver records shows the bug directly:
`(FILENAME = N'D:\Moved\AppDB_2.ndf'), ..., (FILENAME = N'C:\Old\AppDB.mdf')`.

Live on win10cli, `TestLiveDetachAttachRoundTrip` now pins `PrimaryFile` against
a real detached three-file database and logs the DBCC rows. Honest result: this
server returns them *in* file_id order (1, 2, 3), so the old code was right here
in practice — the fix removes a bet on an undocumented ordering rather than
repairing an observed failure.

## 2026-08-26 — One definition of "GO" for the editor, two of them measured

Three things in the tree decide what a batch separator is. Two are ours and had
drifted apart: `controls.isGoSeparatorLine` (what Ctrl+Enter selects and what the
executor is handed) accepted `GO 5` and a trailing `-- comment`;
`sqlparse.goSeparatorLineAt` (completion scoping) accepted a bare `GO` only, with
a comment calling the difference deliberate. It isn't: a script with `GO 5` or
`GO -- run it` executes as two batches and completed as one, so an alias from the
batch above stayed in scope below it.

`sqlparse` now applies the same rule. The two stay duplicated — tuikit must not
import tui — so the pin is `goSeparatorLineCases`, the same 34-line table in both
packages' tests; drop the comment branch from either implementation and that
package's `TestGoSeparatorLineCases` fails on `GO--x`, `GO -- 5 items` and
`GO 5 -- twice`.

The third is `batch.Split` (go-mssqldb v1.11.0), the actual executor, and it is
deliberately *not* copied. Measured rather than assumed, it also splits on `GO;`,
`GO x`, `GO_` and `GO/*c*/` — each leaving the junk at the head of the next batch
for the server to reject — reads `GO5` as a repeat count of 5, and refuses
`GO -- 5 items` because of the digit inside the comment. Those quirks are written
down above the case table instead of being reproduced.

A/B'd live on win10cli/HealthClinic under tmux, cursor after `WHERE d.`:

    SELECT * FROM dbo.Doctors d
    GO -- run it
    WHERE d.

Pre-fix binary offers DoctorID, Email, FirstName… — the alias from the batch
above. Fixed binary offers nothing for `d.`, and still completes the same three
lines without the `GO` line, and treats `GO 5` as a boundary too. The golden file
records the same change: the `GO 5` script's second line moves from batch=0 to
batch=14, while `XGO` and `GOO` still do not split.

## 2026-08-26 — Review findings 5–7: small warts, dead code, two allocations

Cleanups from the same review pass, each verified rather than assumed.

**`DetachDatabaseDialog.node` was stored and never read**, and its doc claimed
the tree node's parent folder gets reloaded once the database is gone. It
doesn't — the dialog's `refresh` reloads the whole Databases folder, which is
right for an object that has left it. Field, parameter and the sentence all gone.

**`loadPlans` used `safego` where it latches UI state.** It writes "Reading
plans for query N..." into the plan pane before the read starts, and nothing
else writes that pane until another query is picked — so a panic skipping the
completion callback leaves the pane claiming to read a query it gave up on. Now
`safegoRepair` with `plansPanicked`, `planSeq`-guarded exactly like
`readPanicked`: drop the guard and `TestAStalePlanPanicLeavesTheCurrentPaneAlone`
fails, because a superseded read's repair blanks the pane a newer one filled.

**`EditorRow.pageReadOnly` had no way to be set.** `SetDrawReadOnly` saved and
restored a page-level read-only that no page ever applied — the comment even
named the case (a job step whose command is not T-SQL) as though it were
implemented. It is now: `EditorRow.SetReadOnly` is the page's own gate, kept
independent of the form's permission gate in both directions, and the Steps page
sets it per selected step along with dropping `SQLHighlighter` — highlighting a
PowerShell script as T-SQL claims it is one. This closes a "keypress that does
nothing": the box took typing that `commitCurrent` silently discarded. The
step's *other* fields still do; see `docs/open-threads.md`.

Verified live on win10cli against a throwaway job with a PowerShell step and a
T-SQL step (dropped afterwards): clicking into the PowerShell step's command and
typing `XYZ` leaves `Get-Date` untouched; the T-SQL step, same gesture, becomes
`SELECT 1XYZ`. Cancelling wrote nothing — both commands unchanged on the server.

**A flat value sat one column left of the same row's editable text.**
`drawFlatValue` put it at `LabelWidth+1` while an editable row pads its label to
`LabelWidth`, draws `[` after it and the text one further — so every value on a
page jogged sideways when the permission gate closed. Now `flatValueX`, pinned
against `InputField.InputX()+1` by `TestFlatValueStartsWhereAnEditableValueDoes`
(35 vs 36 with the old arithmetic).

**Dead code, and what turned out not to be.** `looksLikeXML`/`looksLikeJSON`
were one-line wrappers over `classifyCellValue` whose only caller was a test
asserting that they equal what they return; both gone with it.
`permissionDeniedMessage` had no non-test caller either — the assertions it
carried are worth keeping, so it moved into the test file as `permissionDenied`.
`newCompletionInventory` is *not* dead: it is the type's constructor and six
tests build inventories with it. `sqlparse.isGoSeparatorLine` is likewise now
the entry point for the shared GO case table. The `tuikit` candidates
(`core.NewRect`, `core.JoinPath`, `theme.SetPalette`, `query.formatGUID`) are
left where they are.

**Two allocations per draw.** `allowsAction` built `append([]string{r.name},
r.alt...)` per right per call — it runs for every menu item and toolbar cell on
every draw — and now asks the name and its alternates separately. `qsResult.bars`
takes a reusable buffer (`QueryStorePanel.barBuf`), rebuilt each frame rather
than cached, so there is no invalidation to get wrong when the palette or the
report changes.

Also: `panel_toolbar.go`'s header named two panels and Query Store makes three,
with a third idiom for dimming (a predicate per cell, asked per draw), and
`internal/tuikit/README.md`'s propsheet file list was missing `editorrow.go`.

## 2026-08-26 — Plan graph: parent tiles top-align with their first child

`layoutGraph` centered each node between its first and last child, so the root
"Top" operator floated to the vertical middle of the canvas with a screenful of
dead space above it. SSMS top-aligns instead: a parent sits on the same row as
its first child, which puts the root on the canvas's first row and makes the
root→first-child connector a straight horizontal run. One line
(`selfY := firstY`); nothing else moved — hit-testing, `ensureTileVisible` and
the scrollbars read `rects`/`canvasH` and are position-agnostic, and `drawEdge`
already handled a zero-height trunk. `TestLayoutGraph_ParentCentered…` became
`TestLayoutGraph_ParentTopAlignedWithFirstChild`, which also pins `root.Y == 0`
— the part the user could actually see. Verified by A/B: the pre-fix binary put
`Top` seventeen rows down, the fixed one on row one.

## 2026-08-26 — gosmo: a failed exclusive-access write no longer strands SINGLE_USER

Three `Server` writes open by taking exclusive access with `SET SINGLE_USER
WITH ROLLBACK IMMEDIATE`, which locks the database to one login. Nothing else
puts it back, so the release is part of each method's contract — and two of the
three got it wrong in different ways.

**`DropDatabaseContext(force=true)` had no release at all.** Its two siblings in
the same repo both did (`RenameDatabaseContext`, `DetachDatabaseContext`), which
is what made the omission visible. A `DROP DATABASE` genuinely can fail after
the alter succeeded — another session takes the single-user slot, the database
is in an availability group, the login may set state but not drop — and Object
Explorer > Delete on a database always passes `force=true`
(`explorer_object_ops.go`'s `objectOps[NodeDatabase]`), so the everyday gesture
was the exposed one. Gated on `force`: without it nothing set the access mode,
and a `MULTI_USER` on the way out would silently undo a `RESTRICTED_USER` the
database was deliberately left in.

**The other two issued the release on the caller's own context**, which is the
one context guaranteed to be dead in the case the release exists for.
`SET SINGLE_USER WITH ROLLBACK IMMEDIATE` waits out the rollback of every
transaction it killed, so the statement before the release is exactly the one
likely to have spent the caller's deadline — and gossms drives all of these on
`childFetchTimeout`, 30 seconds, a budget named and sized for tree metadata
reads. On the expired context the repair never reaches the server.
`Server.restoreMultiUser` now holds both halves, deriving its context with
`context.WithoutCancel` plus a 10s bound. The idiom was already in both repos —
gosmo's `capturePlan` (`executionplan.go`) and, on the gossms side,
`restore_dialog_ops.go`'s post-restore cleanup, with a comment saying why. These
two were the inconsistent ones. `WithoutCancel` keeps ctx's values, so a caller
under `WithScript` still captures the statement instead of running it — pinned
by the existing "RenameDatabase force releases under the new name" case.

Found by reading, not by hitting it: the tell was one file containing three
statements of the same shape with three different endings.

**The tests needed a deadline that expires mid-operation**, which cancelling
before the call cannot reproduce — the statement that takes SINGLE_USER has to
have succeeded first, or there is nothing to repair. `detRecorder` grew
`cancelOn`/`cancel` (`database_attach_test.go`): the nominated statement cancels
the operation's context and then fails with `ctx.Err()`, the way a real driver
does. Four mutants killed — repair on `ctx` again (all three "…EvenWhenThe
ContextIsGone" tests fail), the drop's repair removed, its `if force` guard
removed, and the repair moved out of the error branch.

Not yet verified live. The unit side is a scripted driver, so it shows the right
statement is issued on a context that survives — never that the server accepts
it in that state. Staging a real failed forced drop (a snapshot on the database,
or a login that may alter but not drop) is the acceptance run.

## 2026-08-26 — Object Explorer writes stop borrowing a folder listing's budget

The other half of the SINGLE_USER stranding above. Delete, Rename and Move to
Schema all bounded themselves with `childFetchTimeout` — 30 seconds, named and
sized for "a single Object Explorer expand/refresh" and shared with roughly
thirty reads across the package. A write is not a read: a drop or a rename waits
on a lock another session holds, and a database write waits for `SET SINGLE_USER
WITH ROLLBACK IMMEDIATE` to roll back every transaction it just killed, which is
routinely minutes. On the read budget the statement was abandoned part-done —
and that abandonment is what put gosmo's `MULTI_USER` repair on an already-dead
context, the bug fixed on the gosmo side the same day.

`objectWriteTimeout` (5 minutes) and `objectWriteContext` now hold it, in
`explorer_object_ops.go` next to the three writes that use it. Bounded rather
than unlimited: nothing on screen is blocked while a write runs, so a generous
bound costs only a late status message, but a dead connection still has to
report rather than leaving the line pending forever. The schema listing that
Move to Schema opens with is a read and keeps `childFetchTimeout`.

A *helper*, not a second constant spelled at three call sites — the whole
failure mode is that the read's timeout is what every neighbour in the file
uses, so there should be nothing per-site to reach for the wrong one of.

**Two tests, three mutants killed.**
`TestObjectWriteContextOutlivesAFolderRead` measures the deadline the helper
actually produces rather than comparing the two constants: the mutant worth
killing is `objectWriteContext` quietly going back to `childFetchTimeout`, which
leaves `objectWriteTimeout` declared and a constants-only comparison still
passing. It also fails if the helper stops setting a deadline at all.
`TestOnlyTheSchemaListingUsesTheReadTimeout` parses the file and counts real
references to `childFetchTimeout` (identifiers in the AST, so the doc comments
naming it don't count), pinning it at one — a new write reaching for the read
budget takes the count to two. Both verified by mutation.

Not verified live in the sense that matters: reproducing the old failure needs a
write that takes longer than 30 seconds against a real instance (a lock held by
another session, or a large rollback). What was checked is that the app builds,
vets, tests clean under `-race`, and still starts and takes a query panel under
tmux — this changes a budget, not a behaviour.

## 2026-08-26 — Undo stops re-measuring the whole document

`Document.touch` invalidated every version-keyed cache from line 0 down, because
the two callers it was written for — `setLines` and `edit` — can move any line
anywhere. `replaceRange` went through `edit`, so undo and redo did too, and each
step dropped the entire per-line display-width cache. The next Draw's horizontal
scrollbar then re-measured every rune in the buffer: ~8 ms per step on a
20,000-line script, once per keystroke with Ctrl+Z held down.

`replaceRange` knows better than `edit` does. The lines above its span are the
same slices they were, so `touch` now takes the first line the mutation could
have changed and truncates `lineW` there instead of emptying it; `edit` and
`setLines` pass 0 and behave exactly as before. `maxDisplayWidth` extends the
cache to the new line count rather than rebuilding it, which is what makes the
surviving prefix reachable — and drops the `make([]int, n)` it used to allocate
per call purely to `append` away.

`BenchmarkEditorUndoRedo20k` (new): ~7.8 ms/op → ~1.7 ms/op, 4.6×. The benchmark
types its edit through `HandleKey` rather than staging it with `pushUndo` — that
is what decides the span, since typing records a keystroke-sized one
(`pushUndoLocal`) while `pushUndo` covers the whole document and splices from
line 0, where there is nothing above the span to keep. Written the other way
first, it measured 15 ms/op of `cloneLines` and reported the change as 6%.

The second half is `prefixStates.at`, whose resume branch keys off the same
`dirtyFrom`. It was previously reachable only from `setLine`, which cannot change
the line count — so `replaceRange` naming a non-zero line makes it reachable for a
splice that grows or shrinks the buffer, where the states array is indexed by a
line number that just moved. The `len(c.states) != doc.Len()` test in `at` was
already there, described as belt-and-braces; it is now load-bearing, and its
comment says so.

Mutants killed: `touch(0)` in `replaceRange` (the whole point); `lineW[:from+1]`,
keeping the width of the one line whose text just changed; dropping the length
test in `at`, which mis-colours line 7 after a splice that grows. One mutant
survived — a `len(lineW) > len(lines)` guard in `maxDisplayWidth` — and on
looking at why, `from` is never past the end of the new buffer (the lines before
a splice survive it), so the guard was unreachable. Removed, with the invariant
written on `touch`.

Verified live under tmux, which is the check the unit tests can't make: with
`select 3` sitting inside an open block comment, closing the comment on the line
above turns it from comment green to SQL colours, and Ctrl+Z turns it back —
i.e. the resumed prefix states after an undo agree with a full replay on screen.

## 2026-08-26 — Two guards that were copied instead of shared

Both halves are the same shape: a precondition every call site restated, where
the cost of one site forgetting is a click that does nothing or does the wrong
thing.

**`PropDialog.show` now owns the connection guard.** All twenty-three Properties
entry points opened with the same `if !a.requireConn(sc) { return }`, and the
one that forgets it opens a dialog whose every page then fails to load — one
error per page, instead of the status line saying the obvious thing. `show`'s
`pages` parameter became `func() []propPage` in the same change, and that is not
cosmetic: the entry points evaluated the page set *as an argument*, so moving the
guard inwards without the thunk would have left the builders running against a
closed connection — the thing the guard was in front of. 69 lines of copied
guard out; `TestPropertiesOnAClosedConnectionNeverBuildsItsPages` pins both
halves, and its twin pins that the guard doesn't swallow the live case.

**`App.withQueryPanel` replaces seven copies of the same else-branch** in
`app_panel_actions.go` (Execute, Execute Selection, Display Estimated Plan,
Cancel, Reconnect, Refresh IntelliSense, Results To ...). The wording is now
`noActiveQueryPanelMessage`, next to `notConnectedMessage`, and the three sites
in `find_replace_dialog.go` that do more than one call use the constant while
keeping their own early returns. `TestQueryActionsReportWhenThereIsNoQueryPanel`
drives all seven with no panels at all.

Mutants killed: dropping the guard from `show`; calling `pages()` before it;
`withQueryPanel` staying silent; `withQueryPanel` never calling `fn`.

Live check is partial and worth naming. The Query half's second line is hard to
reach on purpose — the `Enabled` predicates grey the toolbar button and menu
item out, so a click never arrives, which is why the table test drives the
functions directly. The Properties half cannot be exercised at all without a
server: opening any Properties dialog needs a live connection. What was checked
under tmux is that the app builds, starts, opens a query panel, and that F5 on
an unconnected panel still reports "Not connected — use File > Connect" in the
results notice.

## 2026-08-26 — The permission gate's alternates only counted at server scope

`allowsAction` (`internal/tui/permission_gate.go`) consulted `requiredRight.alt`
on its server-scope branch and not on its database-scope one — the database case
was a `switch` arm testing `Permits(r.name)` and nothing else, so a right that
declared alternates would have had them honoured at one scope and silently
dropped at the other. Nothing at run time tells that apart from a real denial:
the action just isn't offered, and the note in the shortcut column names the
permission the login was in fact refused.

Not a shipped bug — every database-scope right declared today has an empty
`alt`, so the loop is unreachable outside its test. It is a trap rather than a
defect, and the reason to close it is that the thing that created the server-scope
`alt` in the first place (SQL Server 2022 splitting VIEW SERVER STATE into a
performance half and a security half) is exactly as likely to happen next at
database scope, and the person adding the right would have no way to know the
field they filled in does nothing there.

The arm became a `default:` block reading the cached capabilities once and then
walking `alt` with `Permits`, matching the server branch's shape — including
its reason for asking each name separately rather than joining them, since this
runs per menu item per draw.

`TestAnAlternateSatisfiesADatabaseRight` declares the split right locally rather
than adding one to the package: `BACKUP DATABASE` with `BACKUP LOG` as its
alternate, both real names in gosmo's `ProbedDatabasePermissions` — a made-up
name reads back `CapabilityUnknown` and would pass by failing open, which is the
same silent hole `permission_gate_names_test.go` exists to catch. Three
assertions, because the first alone is satisfied by a gate that allows
everything: alternate granted with the named right denied allows; both denied
withholds; and an *inaccessible* database withholds even though its alternate is
unknown.

Three mutants, all killed: the alt loop deleted (the pre-fix code) fails the
first assertion; `Allows` in place of `Permits` and an unconditional `return
true` both fail the inaccessible-database assertion — which is the one that
pins that accessibility outranks an unknown alternate, the rule
`gosmo.DatabaseCapabilities.Permits` exists for.

No live verification: reaching the new loop needs a database-scope right that
declares an alternate, and there isn't one to exercise against a server.

## 2026-08-26 — A job step reorder became one transaction

gosmo's `Job.ReorderStepsContext` moved a step by deleting it and re-inserting
it at the target position, as two separate `execContext` calls — msdb has no
procedure that renumbers a step in place. Between them the step's whole
definition existed nowhere but in that function's local variable. A failed
insert, a dropped connection, or a context that ran out of budget (which is
exactly what Batch 2's write-timeout work was about) left the job one step
shorter, permanently, with no way to get the step back.

The reference-repair pass at the end is in the same boat and is easy to miss:
`sp_delete_jobstep` silently resets any "go to step N" reference naming the
step it deleted, so a reorder that stops after the moves but before the repair
leaves the job's control flow rewritten to "quit with success" and says nothing.
Atomicity had to span the whole operation, not each move.

`ReorderStepsContext` now builds every statement first and issues one
`atomicBatch` (`script.go`). Three statement builders came out of the methods
that used to issue them — `deleteStepStmt`, `addStepStmt`, `setFlowStmt`, each
byte-identical to the text it replaced, so `WithScript` output for every *other*
job-step write is unchanged.

Three decisions inside `atomicBatch`, each of which a plausible simplification
undoes:

- **TRY/CATCH, not checked return codes.** SSMS's own generated job scripts test
  `@ReturnCode`, but that needs a batch-scoped `DECLARE`, and a `ScriptCollector`
  concatenates its statements into one batch — two scripted reorders would then
  collide on the variable, the same way a second `DECLARE @p1` does (the
  collision `bindScriptArgs` already documents). msdb's job procedures raise
  before they return non-zero, so the CATCH sees the failure regardless. That
  last clause is the one I could not verify offline; it is what the live test
  is for.
- **`SET XACT_ABORT ON`.** Not for server-side errors — TRY/CATCH already has
  those. It is for the *client* going away: a cancelled context sends an
  attention, which aborts the running statement and, with XACT_ABORT off, leaves
  the transaction open. Checked in go-mssqldb's source rather than assumed that
  it doesn't leak onto the next user of the pooled connection: `ResetSession`
  sets `resetSession`, and `tdsBuffer.BeginPacket` turns that into TDS status
  bit 0x8 on the next batch/RPC packet, which is `sp_reset_connection` — SET
  options back to their login defaults, open transactions rolled back.
- **`THROW`, not a `RAISERROR` of our own**, so the caller gets msdb's message
  about what actually failed.

Eight mutants, all killed: per-statement execution (the pre-fix code); the
reference repair moved into a second batch after the COMMIT; `SET XACT_ABORT
ON` dropped; `THROW` before the rollback; the rollback unguarded by
`@@TRANCOUNT`; statements not semicolon-terminated; the "nothing moved" early
return removed; and `checkReorder` short-circuited — that last one to confirm
the permutation test was not passing because the canned step read failed. It
had been: `cannedRow` without `cols` yields one column, `Scan` fails with 23
destinations, and `ReorderStepsContext` returns *an* error, which is all a
"want an error" assertion looks at. Naming 23 columns fixed it, and the same
trap is why `TestReorderStepsIsOneAtomicBatch` asserts on statement text rather
than on an error being nil.

gossms's `TestJobStepsMoveUpReordersOnTheServer` caught the change immediately —
it counted two statements. It now asserts one batch, that it is a transaction,
and that the delete precedes the insert inside it, which is what the two-statement
form was really pinning.

**Not live-verified.** Two things need a server, and `live_atomicbatch_test.go`
is written for both: that a failure part-way through the batch rolls back what
already succeeded *and* still returns an error (a batch that rolls back and
reports success would be worse than the bug being fixed), and that msdb's job
procedures tolerate running inside an explicit transaction at all — if they did
not, every reorder would now fail outright, which is the one regression this
change could introduce.

## 2026-08-26 — Live acceptance for the three review batches, on win10cli

All of it against SQL Server 2025 17.0.1125.2 on `win10cli`, throwaway objects
only, instance left exactly as found (six databases, all MULTI_USER, no jobs,
no tempdb leftovers).

**#4, the transactional job step reorder.** `TestLiveAtomicBatch*` and
`TestLiveJobReorder*` all pass. The regression risk is settled: msdb's
`sp_add_jobstep` and `sp_delete_jobstep` do run inside an explicit transaction,
so the batch does not break the ordinary path. And the rollback works — a batch
whose second `INSERT` fails leaves the table with the rows it started with *and*
returns SQL Server's own conversion error, which was the pairing that mattered.
A batch that rolled back and reported success would have been worse than the bug.

**Batch 1, SINGLE_USER stranding.** New `live_singleuser_test.go`, three tests,
two of which are real A/Bs — reverted to the pre-fix code, both fail:

- A forced drop whose `DROP` fails. Staged with a database snapshot, which SQL
  Server refuses to let its source be dropped from under (Msg 3709) — a failure
  that lands *after* the access mode has already changed, which is the only
  shape that tests anything. Pre-fix the database is left `SINGLE_USER`; the
  test says so in as many words. Confirmed by hand first, with `sqlcmd`, before
  writing the test.
- `restoreMultiUser` on an expired context. The test issues the same `ALTER` on
  the same dead context both ways: the plain `execContext` returns "context
  canceled" without reaching the server (asserted, so the A/B cannot be
  vacuous), and the `WithoutCancel` form arrives. Reverting the `WithoutCancel`
  fails it.
- The ordinary forced rename with a second session holding an open transaction
  inside the database. A regression guard, not an A/B — the pre-fix code passes
  it too, because the context is not exhausted there.

**Batch 2, the Object Explorer write budget.** Measured rather than argued, with
a throwaway probe calling `RenameDatabaseContext(force)` under each budget while
a second session held an uncommitted transaction the rename had to roll back:

| rollback staged | budget | result |
|---|---|---|
| 1.6 GB log | 30s (`childFetchTimeout`) | succeeded in **27.3s** |
| 3.8 GB log | 30s (`childFetchTimeout`) | **failed** at 30.0s — "set single user: context deadline exceeded" |
| 3.8 GB log | 5m (`objectWriteTimeout`) | succeeded in **1m12s** |

The first row is the interesting one. 27.3s is 91% of the old budget for a
rename nobody would call unusual — the pre-fix code was one modest transaction
away from abandoning writes, not a pathological case away. The second row is the
bug reproducing, and the third is it fixed. Each staging is single-use: the
attention that kills the client's `ALTER` does not stop the rollback it already
triggered, so the next attempt finds the work done and returns immediately.
Re-stage before every measurement, or a fast second run reads as a pass.

## 2026-08-27 — The Steps page gates its whole edit panel, not just the command

`TextRow` and `SelectRow` gained `SetReadOnly`/`ReadOnly` — the page's own gate,
the one `EditorRow` already had, and deliberately a second field beside
`drawReadOnly` rather than one flag: whichever of the two gates is set last must
not cancel the other out, or lifting a permission gate makes a non-T-SQL step
editable. It closes every way in, not just the drawn one — `Edit`, `HandleKey`,
`HandleMouse` and `Focusable` — because a row that only *draws* flat still goes
dirty and still gets written. `SelectRow.SetReadOnly` also closes an open list:
the overlay is drawn last and takes every event first, so one left open floats
over a row nothing routes to any more.

Job Properties > Steps then gates the panel as one (`setPanelReadOnly`). What
was actually broken: the Command box had been read-only since 2026-08-26, but
Step name, Database, both on-success/on-failure pairs, the retry counts and the
output file all still took typing that `commitCurrent` dropped on the floor.
Nothing reached the server either way — `commitCurrent`'s `editable()` guard
predates this — so the bug was entirely in what the page invited the user to do.

**The New button had to be dealt with, or the fix would have broken it.** New
seeds a step from the edit panel, so on a mixed job — a PowerShell step
selected, every row now refusing typing — there was no way to type a name and
New went dead for the whole visit. First press on a read-only step now clears
and unlocks the panel and says "Type a name for the new step, then press New
again"; the second press adds the step as before. Only `commitCurrent` reads
`current`, so dropping it there loses nothing.

Pinned by `TestANonTSQLStepsWholeEditPanelRefusesTyping` (both halves — the
gate holding, and lifting again on a T-SQL step, which a page that never
re-enabled the rows would otherwise pass) and two propsheet tests. Nine mutants
killed across the two files; two survived the first attempt and are worth
recording, since both would recur: a row's `HandleKey` guard looks untested
because `InputField`/`DropDown` ignore keys they are not focused for, so the
test has to focus the widget by hand, and the select's guard needs `Enter` —
`Down` on a closed dropdown falls through on purpose.

Live on win10cli against a throwaway job with a T-SQL step and a PowerShell one:
the T-SQL step draws its boxes, selecting the PowerShell step flattens all ten
rows (retry interval keeps its "minutes" unit, the hint names the subsystem),
New clears and unlocks, and Cancel left `sysjobsteps` byte-identical. Job
dropped afterwards.

## 2026-08-27 — The object-ops gate can see a grant on one schema

Rename, Move to Schema and Delete were gated on database-wide ALTER, CONTROL or
ALTER ANY SCHEMA — sufficient conditions, none of them what SQL Server actually
checks for a schema object, which is ALTER on the object's *schema*. A login
granted ALTER on one schema and nothing else holds no database-wide permission
at all, so every right the gate asked about read as denied and all three items
vanished from objects it could in fact rename.

**gosmo grew the probe rather than gossms working around it.** The database
capability probe now carries a third block —

    SELECT CONCAT('S:', n.v), s.name, HAS_PERMS_BY_NAME(QUOTENAME(s.name), 'SCHEMA', n.v)
    FROM sys.schemas AS s CROSS JOIN (VALUES (@p33)) AS n(v)

— unioned onto the roles and permissions it already asked for, so the cost is
one extra block per database probe, not the query-per-schema this was costed at
in `open-threads.md`. `ProbedSchemaPermissions` holds one name: HAS_PERMS_BY_NAME
folds in the permissions that imply the one it is asked about, so CONTROL on the
schema, ALTER ANY SCHEMA and db_owner all answer 1 for ALTER without being asked
separately. `DatabaseCapabilities` gained `SchemaPermission`/`HasOnSchema`/
`AllowsOnSchema`/`PermitsOnSchema`, the last folding in accessibility exactly as
`Permits` does at the scope above it.

Three shapes worth keeping:

- **The permission travels in the kind column and the schema in the name
  column**, not the other way round. A permission name is ours and fixed; a
  schema name is user data, and a schema called `P` read back the other way
  round becomes a database-scope permission answer that overwrites a real one.
  The test creates a schema named `ALTER` for exactly this.
- **QUOTENAME on the securable.** A schema whose name needs quoting is otherwise
  asked about as a different securable, and HAS_PERMS_BY_NAME answers NULL for
  one that does not exist — which fails open, so the wrong answer is the
  permissive one.
- **NULL is not a denial** (`capabilityStateOf`), the rule the database-scope
  half already followed.

In gossms `requiredRight` gained a `schema` flag, `allowsActionOn`/`gateOn` take
the schema the node lives in, and `objectOpRights` lists `rightAlterOnSchema`
beside the three wider rights. A schema *node* answers `""` and keeps the old
list: ALTER on a schema does not permit dropping or renaming the schema itself
(that is CONTROL on it, or ALTER ANY SCHEMA), so answering with the node's own
name would offer three items the server then refuses.

**Two mutants survived the first scripted pass, both about query text a fake
cannot see** — numbering the schema block's placeholders from 1 instead of the
next free one, and swapping its two string columns. The fake answers whatever is
scripted however the probe asks, so the driver now records the database probe's
text and args and the test asserts on them. Live, the same two are visible for
free, which is why the live test exists.

Live on win10cli, A/B against a pre-fix binary. A throwaway login with
`GRANT ALTER ON SCHEMA::Sales` and `GRANT SELECT ON SCHEMA::Archive` (plus
VIEW SERVER STATE, without which connect-time `loadInfo` fails and the tree comes
up empty): the old binary greys all three items on `Sales.Orders` with "needs
ALTER"; the new one offers them there, still greys them on `Archive.OldOrders`,
and the rename it now offers went through — `sys.tables` reads
`Sales.OrdersRenamed`. gosmo's `live_schemacaps_test.go` pins the same thing at
the library level, with the server's own acceptance of a rename in each schema
as the oracle. Database and login dropped afterwards.

## 2026-08-27 — New Availability Group asks before it writes

Creating a group is one statement on the primary and then an
`ALTER ... JOIN` on each secondary, run over a peer connection. Everything after
the CREATE can fail, and when it does the group exists with a replica missing —
which is what `open-threads.md` carried as "no rollback of a partly created
group".

**The decision, taken with the author: preflight, never unwind.** Before the
CREATE, every secondary is asked whether it could join —

- reachable at all (the peer connection opens),
- Always On enabled there (`Info().IsHADREnabled`),
- a database mirroring endpoint that exists and is STARTED,
- an endpoint still at the address the dialog recorded when the replica was
  added, which may have been minutes ago,
- and a login that `Allows` ALTER ANY AVAILABILITY GROUP there — Allows, not
  Has, so a peer whose probe never ran is let through to try.

Any failure refuses with nothing created. Every replica is asked even though
only the first problem fits the one-line message, so the count is honest: being
sent back three times, once per instance, is worse than being told there are
three. The preflight is skipped under `gosmo.Scripting` — the script is what the
user takes to the instances that are not up yet, and a preflight there would
refuse to write the very thing that fixes them.

**Why not unwind, settled by the live run rather than by argument.** Dropping
`zz_gossms_pf` from ubusql1 left it in *ubusql2's* `sys.availability_groups`; the
secondary needed its own DROP. So a rollback cannot be completed from the
primary against a secondary that has already joined and then become unreachable —
which is the exact case a rollback would exist for. It would also destroy a group
the user asked for on the strength of one bad peer. The post-CREATE errors keep
naming the instance and saying the group exists.

`peerFor` is a new seam on the dialog, the counterpart of
`new_endpoint_dialog.go`'s `peerServerFor`: a test cannot open a second
connection, and without it only the self-named-replica path was reachable. Eight
mutants died, including running the preflight *after* the CREATE — which returns
almost the same error and is caught only by asserting on the statements that
reached the server.

Live A/B against ubusql1, with win10cli (Always On off, since Windows 10 Pro has
no Failover Clustering) as the unjoinable replica. The pre-fix binary created
`zz_gossms_pf` and then reported the JOIN failure, leaving the group behind — the
bug, reproduced. The new one refuses with "Always On is not enabled on win10cli,
so it cannot host a replica. Nothing was created" and
`sys.availability_groups` still held only AAG1. Script Changes then produced the
full labelled script with the same unjoinable peer in the list. And the happy
path still works: the same dialog with ubusql2 as the replica created the group
and both replicas came up CONNECTED. Group dropped from both nodes afterwards;
AAG1 verified HEALTHY on both, `testdb_1` and `HealthClinic` SYNCHRONIZED.

One thing to know before driving this dialog headlessly: the replica list's
"Instance to add" row sits directly above a `ButtonsRow`, and the Tab that looks
like it lands on the field lands on `[ Add Replica ]` — typing then goes nowhere
and reads as a broken text field. Backtab once and type.

## 2026-08-27 — `requiresText` names each role once

`requiresText(rightAlterDatabase, rightControlDB, rightAlterAnyDatabase)` read
"Requires ALTER (db_owner) or CONTROL (db_owner) or ALTER ANY DATABASE
(dbcreator)." — db_owner twice, and three permissions presented as six things to
go and ask for. The alternatives for one action mostly share a role, so the roles
are now gathered into one trailing clause: "Requires ALTER, CONTROL or ALTER ANY
DATABASE (db_owner, dbcreator)." 85 characters became 68, which is what takes the
read-only banner under one line at 80 columns.

A right with no role stays outside the clause. `rightAlterOnSchema` is the only
one today and it is deliberately role-less — ALTER on a schema is granted on the
schema, and naming db_ddladmin beside it would send the user after a role that
confers much more than what is missing. It joins the sentence after the
parentheses instead: "… (db_owner, db_ddladmin) or ALTER on the object's schema."

`requiredRight.String()` still exists and still names one right with its role —
`permission_error.go` uses it for the single-right sentence behind Msg 5011.
`TestRequiresTextFitsTheReadOnlyBanner` pins the length against the prefix the
banner adds, over the rights lists that actually reach it; a context menu's
rights never do, since `gateOn` shortens those to "needs <first right>".

## 2026-08-27 — Query Store: the two filters, Tracked Queries, and plan comparison

The three items `open-threads.md` listed as deliberately out of the Query Store
work, built in one pass.

**The execution floor and the regression threshold.** Both are pushed into the
query, never applied to the rows afterwards: `Top` has already thrown away
everything below the cap by the time rows exist, so a client-side floor filters
the survivors of a ranking the floor should have changed. gosmo already carried
`MinExecCount`; `MinRegressionPct` is new, and lands in the outer SELECT of the
Regressed Queries CTE pair where both windows' values are in scope — a HAVING
inside either CTE would compare a window against itself. It is a *percentage* of
the baseline rather than an amount because the same report is read under eleven
metrics in four different units: a threshold of "100" would mean 100 microseconds
under Duration and 100 8-KB pages under Logical reads. The comparison multiplies
rather than divides (`(r.value - b.value) >= b.value * @p / 100`), because
`b.value > 0` excludes a zero baseline but nothing orders the two predicates.

Which reports carry which filter is a `filters` field in the `queryStoreReports`
table, not a switch on the title: Overall Resource Consumption groups by interval
and Query Wait Statistics by wait category, so neither has a per-query execution
count to floor. A selector on a report that ignores it is dimmed and says why —
a control that changes a number the next read drops is the silent wrong-thing the
context-gating rule exists to prevent. `TestTheFlagsTableMatchesTheQueryEachReportRuns`
loads all seven and greps the statement each one actually ran, which is what keeps
the hand-written table honest.

**A toolbar cell that does not fit is not drawn at all.** Both filters went on
the selector row first, and at the real pane width — 140 columns, with Object
Explorer taking the rest — "Refresh" fell off the end. `layoutToolButtons` gives
an overflowing cell a zero rect, and a zero rect is neither drawn nor clickable,
so nothing said anything. They now lead the *action* row, which was 85 columns
wide. Worth remembering for any panel toolbar: an added cell can silently push
the last one out of existence, and only a live run at a realistic width shows it.

**Tracked Queries now tracks.** The set is per server and per database, in
`tracked_queries.json` beside `config.json` (`internal/config/tracked.go`) — its
own file because config.json is connection profiles and settings, every save of
it rewrites the file holding the encrypted passwords, and a tracked list that
goes bad costs four keystrokes where config.json's does not. It is a process-wide
singleton because two surfaces read it — the panel and the Detail Browser's leaf
grid — and neither owns the other. The ids reach the report through gosmo's new
`Options.QueryIDs`, applied by the four per-query reports only: `QueryStorePlans`
and `QueryStoreTrackedQuery` name the one query they are about, and a second id
predicate there would answer nothing for any query the list did not contain.
That one is pinned by a test, because it is invisible until a user pins a query
and the plan pane goes empty.

A tracked query that has left the store still gets a row saying so — four rows
for five tracked queries reads as a broken report. That row carries its query id,
which the first version did not: without one, Untrack Query cannot act on it and
the pin is permanent, since the row is the only place the query still appears.
Found live, by clearing Query Store under a pinned query.

**Plan comparison** is `showplan.CompareStatements` (pure data, with the pairing
rules) plus `PlanComparePanel` (two grids: statement properties above, paired
operators below). Two grids rather than two plan graphs side by side — an
operator tile is eighteen columns wide, and the question a comparison answers is
a list, not a picture. Operators pair on physical operator plus object *without*
the index: a seek that changed index is the comparison this exists for, and
keying on the index would split it into two one-sided rows with nothing to read
against each other. A seek that became a scan deliberately does not pair — that
is what happened, and pairing it would bury it under a row of property changes.
Estimates compare with a 1% tolerance, or a plan re-costed against refreshed
statistics reads as "everything changed". The entry point is a two-press
Compare Plans in the Query Store panel: the two plans of one query are two rows
of the same pane, and there is nowhere to select both at once. The marked plan is
parsed at the mark, not held as a row index — the plan grid is rebuilt by every
reload.

**Live, against a throwaway `zz_gossms_qs` on win10cli** with a real workload:
a floor of 10 took Top Resource Consuming Queries from 8 rows to 1 and named
itself in the summary; the same cell on Overall Resource Consumption refused with
"An execution floor applies to the reports that rank queries, not to Overall
Resource Consumption"; Regressed Queries over 15 minutes went 5 rows → 2 at ≥25%
→ 1 at ≥100%, dropping exactly the queries whose regression/baseline ratio fell
short. Track pinned query 2, the file appeared under `~/.config/gossms`, the view
showed that query alone, and after a full restart it was still pinned. Clearing
Query Store turned it into the "Not in Query Store for this window" row, and
untracking from that row emptied the view and the file. Compare Plans on the two
plans of one query — one from before an index was added, one from after —
produced the Clustered Index Scan / Index Seek pair as "Only in A" / "Only in B"
under a Stream Aggregate whose cost differed 1.1251 vs 0.0035. Database dropped
and the tracked file removed afterwards.

One known limit, deliberate: the tree's Tracked Queries leaf is cached like every
other Detail Browser node, so a pin made in the panel shows there after a refresh
rather than immediately.

## 2026-08-27 — The four ungated write actions

`docs/open-threads.md` § Permission gating listed four write actions the gate
layer could not reach because the permission behind each was not one gosmo
probed: both Always Encrypted key dialogs, Security Policies' Enable/Disable,
and New Index / New Statistics. All four are gated now.

**Three names went into gosmo's `ProbedDatabasePermissions`** — `ALTER ANY
COLUMN MASTER KEY`, `ALTER ANY COLUMN ENCRYPTION KEY`, `ALTER ANY SECURITY
POLICY` — and each becomes one `requiredRight` on the item that needs it.
`HAS_PERMS_BY_NAME` folds in the permissions that imply the one it is asked
about, so a single name covers every wider right and no alternative list is
needed. What the live probe on win10cli settled: db_owner answers 1 to all
three, **db_ddladmin answers 0 to all three**, and a database-wide `ALTER`
answers 1 for the two key permissions but **0 for `ALTER ANY SECURITY POLICY`**
— so the toggle really does need its own name, and db_owner is the only fixed
role any of the three can send the user after.

**New Index and New Statistics take the object-write rights**, the set
Rename/Move/Delete already used, now named `objectWriteRights()` and shared
between the two call sites: database-wide ALTER, CONTROL, ALTER ANY SCHEMA, or
the schema-scoped ALTER. Both folders carry their table's schema
(`loadTableChildren` propagates it), so `gateOn` can ask about it. The set stops
at the schema and that is a real limit: a principal granted ALTER on one *table*
reads 0 at schema and database scope alike (verified live), so it loses both
items. Recorded in `open-threads.md` — probing it needs a query per object,
where the schema block costs one per database.

**A live run turned up a second bug in the menu layer.** `New Index` is a
cascade, and `menuRowSuffix` returned the `▸` marker before it considered the
note — so the newly gated item greyed out and said nothing about why, which is
the exact failure `MenuItem.Note` exists to prevent. A disabled cascade never
opens (both `ContextMenu.HandleMouse` and `menuCascade` refuse a disabled row),
so the marker points at a submenu the user cannot reach; the note now outranks
it. `TestADisabledItemShowsItsNoteInsteadOfItsShortcut` had pinned the old
behaviour on purpose ("it has a submenu, not a reason") and its cascade
assertion is now the enabled case only.

**Live on win10cli**, throwaway `gate_live_test` and `gate_probe_login` —
db_datareader plus VIEW DEFINITION, `GRANT ALTER ON SCHEMA::app`, nothing else.
Both key folders showed `needs ALTER ANY COLUMN MASTER KEY` /
`... ENCRYPTION KEY`; the policy node showed `Disable  needs ALTER ANY SECURITY
POLICY` beside a correctly withheld `Delete...`. `New Index` and `New
Statistics...` were live on `app.t1` and read `needs ALTER` on `dbo.t2` — the
schema grant, both directions, from one login in one session. The positive
control matters as much: New Statistics on `app.t1` opened its dialog and built
the form. Database and login dropped afterwards.

**gosmo gained a live test for the probe lists themselves**
(`live_probednames_test.go`): every name in all three `Probed*` lists must read
back something other than `CapabilityUnknown` for a sysadmin. A misspelt or
non-existent name is silent — `HAS_PERMS_BY_NAME` answers NULL, the state is
Unknown, and Unknown fails open forever — so nothing but a live run can tell it
from a login that holds the right. Mutation-checked by misspelling one name.

## 2026-08-27 — SQL Agent's four New-X actions are gated on msdb role membership

The last of P3's deliberate exclusions with real code behind it. What permits
New Job / New Schedule / New Alert / New Operator is membership of an msdb
role, which grants EXECUTE on individual procedures rather than the
database-scope EXECUTE the permission probe reads — so the gate had nothing to
ask about and the four items were left ungated. `open-threads.md` costed the
remaining work as "deciding what *or above* means". That turned out to be the
smaller half.

**The real blocker was that a role test cannot fail open.** The whole gate
layer rests on withholding only when the server denied *every* right, and the
permission accessors honour it — `Permits` reads unknown as allowed. `InRole`
cannot: it answers false for a role never asked about exactly as it does for
one the login is not in. Gating on `InRole("SQLAgentUserRole")` would have
withheld all four items from every connection whose msdb probe had not landed.
gosmo had already met this at server scope — `Capabilities.Probed` exists with
a comment saying precisely this — but `DatabaseCapabilities` had no
counterpart, so it gained one. It reads `Roles != nil` rather than
`Accessible`, because an inaccessible database is a real answer: the probe ran
and reported that nothing inside could be asked.

Three facts settled live on win10cli, each of which breaks the gate if assumed
the other way:

- **A sysadmin reads `IS_ROLEMEMBER` = 0 for all three `SQLAgent*` roles.** It
  maps to `dbo`, and `dbo` is a member of none of them. Gating on the Agent
  roles alone would have emptied the Agent menus for the one login that
  certainly may use them, which is why `CONTROL SERVER` is in the set.
- **The roles nest and `IS_ROLEMEMBER` resolves the nesting** — a member of
  `SQLAgentOperatorRole` reads 1 for `SQLAgentUserRole`. So the narrowest role
  is the whole test for "or above"; Reader and Operator need no separate check.
  That is the answer to the question the entry left open.
- **msdb `db_owner` is a real non-sysadmin case** covered by neither.

Two things the live run caught that no test would have:

**msdb was never primed.** `primeDatabaseCapabilities` returns early when the
node carries no `DBName`, and the Agent tree hangs off the server, so nothing
ever warmed msdb and all four gates would have failed open for the whole
session — passing every unit test while gating nothing. `isAgentNode` now maps
the contiguous `NodeAgent*` block to msdb.

**The withheld note named the wrong right.** `gate` shows only `rights[0]` in a
disabled item's note, and the set led with `CONTROL SERVER`, so the first live
capture read `New Schedule...  needs CONTROL SERVER` — telling someone who
wants to create a job to go and ask for sysadmin. The order of an alternatives
list is a message, not just a test; the narrowest sufficient right goes first,
and `TestTheSQLAgentNoteNamesTheNarrowestRight` pins it.

Verified end to end under tmux against win10cli with three throwaway logins
(`SQLAgentUserRole` only, `SQLAgentOperatorRole` only, and no role at all),
since dropped: all four items dim to `needs SQLAgentUserRole` for the no-role
login, stay enabled for the role holder, and stay enabled with no note for
`sa`. The no-role login's *withholding* is also what proves the msdb priming
runs — an unprimed msdb fails open.

Three mutants killed: dropping the `Probed` guard, dropping `CONTROL SERVER`
from the set, and unwiring the gate from New Job.

## 2026-08-27 — Object-scoped grants are probed after all, in one query per database

P3's remaining "cost decision": a principal granted ALTER on one *table* holds
nothing at schema or database scope, so `objectWriteRights()` denied every
right it asked about and Rename / Move to Schema / Delete / New Index / New
Statistics went from an object SQL Server would have let it alter. This is the
one direction the gate layer otherwise avoids. It was left standing because an
OBJECT-scope probe was costed at a query per object.

**That premise was wrong, and only true of `HAS_PERMS_BY_NAME.`** That function
answers for one securable per call, but the catalog answers for a whole
database at once. gosmo's database probe gained a fourth `UNION ALL` block —
`objectCapabilityQuery` — reading `sys.database_permissions` and `sys.objects`
against a recursive CTE of every principal the login's permissions can arrive
through. Measured live: 4 rows, 5 ms, and no extra round trip, since the probe
was already batching roles, permissions and schemas into one query.

Four parts of that read are load-bearing, each found by it being wrong first:

- **`sys.objects` is not redundant with `sys.database_permissions`.** An
  object's owner holds implicit CONTROL and has *no* permission row at all —
  `HAS_PERMS_BY_NAME` said 1 while the grants query returned nothing.
- **`public` is in the principal set, so the permission filter must stay.**
  Without it every catalog view's SELECT grant to public comes back: 235 rows
  on a stock database against the 3 that matter.
- **`minor_id = 0`** keeps column-level grants out; they share `class = 1` and
  would report a column grant as a grant on the table.
- **A DENY comes back with a NULL name.** The deny leaves no permission behind,
  metadata visibility then hides the object, and `CONCAT` would key it on a
  bare `"."`. Dropped — such an object is equally invisible in any listing
  built from the same catalog.

**The map is sparse, and that is the whole safety argument.** Unlike the role,
permission and schema blocks, it holds a row only for an object explicitly
granted, denied or owned. Read the way the others are read — "not denied means
allowed" — it would report every object in the database as permitted. So
`HasOnObject` is the only accessor, and `rightAlterOnObject` can only ever
*add* permission: an object with no row leaves the four wider rights to answer
exactly as they did before. Nothing that was offered can be withheld by this
change, which is what made it safe to add to five existing call sites.

Verified live against `HealthClinic` with a throwaway login holding no schema
or database right, since dropped. In one menu: a directly granted table, a
table granted through *two* levels of role nesting, and a table the login owns
outright all keep Rename/Move/Delete; a table with no grant in the same schema
and a table under an explicit DENY both lose them to "needs ALTER". `sa` is
unaffected. Two mutants killed: reading the map as `Permits` rather than `Has`,
and unthreading the object name at the call sites.

## 2026-08-27 — The P4 refusal path is reachable live, and Msg 1088 was unclassified

`open-threads.md` recorded that the `withPermissionAdvice` append could not be
reached end to end: the gates now withhold the write actions before they can be
attempted, so only the *read* refusals were ever verified live. Reaching it
needed "a login that holds a right the gate reads but not the one the statement
checks", and the schema-scoped ALTER that used to be the candidate stopped
being one when the gate learned to read it. Point 2 removed another.

**One exists, and it is the general shape rather than a curiosity: a grant at a
wider scope with a DENY at a narrower one.** A login holding database-wide
`ALTER` and `DENY ALTER` on one table reads `db_ALTER=1 schema_ALTER=1
obj_ALTER=0`. Every right the gate asks about answers yes, the item is offered,
and the statement is refused. The new object block does not close it either,
and deliberately: it can only add permission, so the DENY it records cannot
withhold what the wider rights allow.

Driven live under tmux against a throwaway table, both halves of the mapping
reached for the first time:

- **Rename → Msg 297**, appearing on the status line with the server's sentence
  and no advice, exactly as `advice()`'s comment says it should. That comment
  is right and stays: 297's text names nothing, and a refused KILL and
  `sp_readerrorlog` raise it alone, so any right named there would be invented.
- **Move to Schema → Msg 15151**, with the append working:
  `… you do not have permission. (15151) — The object zz_p3_target does not
  exist, or this login cannot transfer it.`

**Msg 1088 was in no table at all.** `ALTER TABLE` and `CREATE INDEX` on a
denied object both raise `Cannot find the object "dbo.x" because it does not
exist or you do not have permissions.` — textbook `refusalAmbiguous`, and it
was falling through as raw text. It now has its own entry and its own pattern,
because its wording differs from the 3701/15151 sentence in three ways at once:
double quotes around the identifier, no comma before "because", and
"permissions" plural. Reusing `reCannotBecause` matches none of them, which a
mutant confirms. The advice is phrased around the permission rather than the
verb — SQL Server's verb here is "find", and "this login cannot find it" reads
as a lookup failure rather than the refusal it is.

**A mistake worth recording.** Hunting the mismatch, `DROP TABLE dbo.Invoices`
was run as the throwaway login expecting a refusal. It succeeded: `DENY ALTER`
on a table does not block a drop, which is permitted by ALTER on the *schema* —
a permission that login had. The table was rebuilt from the two sources that
survived (`todo/healthclinic_sample_data.sql` section 9 and the still-present
`vw_UnpaidInvoices`) to 12,440 rows matching the 12,440 billable appointments,
schema exact, values regenerated to the original distribution; the registered
backup file was no longer on disk. The rule that would have prevented it: a
destructive statement goes against a throwaway object even when the expectation
is that it will be refused.

## 2026-08-27 — xp_cmdshell is offered on Linux, where it can never be on

Found while re-reading `docs/open-threads.md` for a known-issues list, from
what the Detach/Attach entry already said in passing: SQL Server on Linux has
no xp_cmdshell at all, and `sp_configure 'xp_cmdshell', 1` there fails Msg
15392, "not supported by this edition".

**The reason it was not already handled** is the interesting half.
`newConfigBoolEditor` already has a missing-option path — an option absent from
`sys.configurations` renders as a disabled `N/A` text row rather than a live
checkbox left out of the tracked list. xp_cmdshell does not take it: Linux
*lists* the option, at 0. Confirmed on ubusql1 rather than assumed:
`SELECT name, value, value_in_use FROM sys.configurations WHERE name LIKE
'xp_cmdshell%'` returns a row. So the page rendered an ordinary checkbox that
ticked cleanly and produced the server's raw refusal on OK — the ungated-action
case § Application rules exists for.

`xpCmdshellRow` (`server_props_advanced.go`) branches on
`ServerInfo.Platform`, which gosmo derives from `@@VERSION` and nothing else,
so the check costs no query and works on a pre-2017 instance where
`sys.dm_os_host_info` does not exist. On Linux it hands back the same disabled
row shape the missing-option path uses.

**The unit test passed and the live run failed anyway**, which is the entry's
point. `TestXpCmdshellIsNotEditableOnLinux` asserts through the widget — the
value says why, an `Edit` leaves it clean, and apply writes nothing — and
`TestXpCmdshellStaysEditableOnWindows` guards the other direction, since
withholding the option everywhere satisfies the first test on its own. Both
pass over `newFakeConnOnLinux`, a `serverInfoResponse` with `@@VERSION` saying
Linux. Driven under tmux against ubusql1 the row came out
`xp_cmdshell [ot available on Linux ]`: the field was sized 22 for a 22-column
value, and `widgets.InputField` scrolled it by one. Widened to 24. No test
sees this — the value the test reads back is the whole string either way.

## 2026-08-28 — A Query Store report's statement opens in a query panel, and the panel was never drawing its grid overlays

The Query column of every per-query Query Store report was cut to 80 columns in
the *data*: `queryStoreOneLine` flattened the statement onto one line and then
`core.Truncate`d it, so the full text existed nowhere in the UI and "Show
Value" showed the same 80 characters the grid did.

The cut was unnecessary. `DataGrid` clamps a column's width
(`maxCellWidthOrDefault`) and truncates what it *draws* (`drawRow`'s
`core.Truncate` to the available width), so the cell can hold the whole
statement and still render exactly as before. `queryStoreOneLine` now only
flattens — the newline is the part that would break a grid row — and the two
hosts route the column to a query panel:

- `App.showSQLCellValue` is the `OnShowValue` hook, claiming the cell only for
  the `qsQueryColumn` ("Query") column and opening it as `Query.sql` with the
  SQL highlighter, the same treatment an `xml`/`json` cell gets. It shares
  `openValuePanel` with `openCellValuePanel`, including the
  `savedText = editor.Text()` seeding that keeps a panel opened to *read* a
  value from being born dirty.
- Wired on both grids the reports appear in: `newQSGrid` (the Query Store
  panel's report and plan grids) and the Detail Browser's, via a new
  `App.newDetailBrowser` that also folds in the `OnRefresh` wiring both
  construction sites were duplicating.

**Found by the live run, as usual.** In the Detail Browser the right-click →
Show Value → panel path worked first try. In the Query Store *panel* nothing
happened at all — no menu, no popup, on right-click or Ctrl+Space, while
keyboard navigation clearly worked. `QueryStorePanel.Draw` called
`grid.Draw`/`plansGrid.Draw` and never `DrawOverlay`, so both grids' context
menu and value popup were being opened and then not painted: an invisible menu
holding every keystroke until Escape. Shipped that way — the panel has had a
cell context menu ("Copy", "Show Value") since it was written and it has never
been reachable. `Draw` now ends with both `DrawOverlay` calls, after both
grids, so an overlay is on top of either.

Pinned by `TestShowSQLCellValueOpensTheWholeStatement` (a 500-rune statement
comes back whole in a non-dirty panel; a non-Query column and a blank cell are
left to the grid's own popup) and by the report test, which now asserts
`queryStoreOneLine` returns the whole statement — mutated back to a truncating
version, it fails. The missing `DrawOverlay` has no unit test; it is a draw
call whose absence is visible only on a real screen.

The same audit (`grep` for a `DataGrid.Draw(s)` with no `DrawOverlay` beside it)
turned up one more: `PlanComparePanel`, which even has an `overlayGrid` for
input routing and drew neither grid's overlay. Fixed the same way. Nothing else
in `internal/tui` is missing the call.

## 2026-08-28 — Query Store review: the flattened cell, and the uncancelled plan read

A read-only review of `query_store_panel.go`, `query_store_reports.go` and the
gosmo report layer behind them turned up two bugs worth fixing ahead of the
rest, plus a list of gating gaps and inconsistencies still open (see
`docs/open-threads.md`).

**The Query column's cell was not a statement.** `queryStoreOneLine` joins the
statement onto one line — it has to, a raw newline breaks the grid row — and
both surfaces wire `OnShowValue` to `showSQLCellValue`, which opens the cell in
a query panel with SQL highlighting and a working Execute. A statement whose
first line ends in `-- comment` therefore opened as
`SELECT 1 -- pick one FROM dbo.t`: valid SQL, silently missing its FROM clause.
The function's own comment claimed the cell "keeps the whole statement for Show
Value to open", which was true of the *characters* and false of the meaning.

Two different fixes, because the two surfaces hold different things. The panel
already keeps typed rows, so `qsResultRow` gained `queryText` and the panel
wires its own `showValue` — no round trip. The Detail Browser's grid is
`[][]string` shared with ~30 node types behind a cache and a progressive-load
layer, and threading raw text through that for one report family is exactly the
speculative widening the scope rule warns about; instead
`showQueryStoreValue` re-reads the statement by the row's `Query ID` through
gosmo's existing `QueryStoreQueryTextContext`, which was built for this. Every
path that cannot produce an id falls back to the cell, which is then all there
is.

The subtle half was `OnShowValue(col int, ...)` — the first parameter is the
**column** index. `DataGrid.openViewer` reads the cell at `selRow`/`selCol`, so
`SelectedRow()` is the row being shown, and that is where the row comes from.

**Plan reads were never cancelled.** `load` stores a `cancel` and `cancelRead`
aborts it, with a comment saying why: uncancelled, the query runs on the shared
host connection until `qsReadTimeout`. `loadPlans` had no equivalent — and it
fires from the report grid's `OnSelectRow`, so holding Down through a 25-row
ranking started 25 plan reads, each with a 120 s ceiling, none cancelled;
`planSeq` discarded the results while the queries all ran. Closing the panel
cancelled the report read and left the plan reads going. Added `planCancel`
beside `cancel` — two, not one, for the same reason there are two sequence
numbers.

**What the tests needed.** The fake driver answers instantly and ignores the
context, so a completed read has already cancelled itself via its own `defer`
and a panel that cancelled nothing would have passed. `fakeResponse` gained a
`block` channel that holds a matching query inside the driver, and
`fakeInstance` now records each read's context and parameters (`ReadContext`,
`ReadArgs`) — the args because the Detail Browser hook reaching the *wrong row*
still hits the server, and with every id answered alike the bound id is the only
witness. Each of the four tests was checked by mutating the fix back; the
row-addressing one only started failing once it asserted the bound id.

## 2026-08-28 — Query Store: a status line that describes the query that ran

Three status-line bugs from the same review, all one defect underneath: the
panel composed its summary from what the *toolbar* said, and the toolbar is not
the authority on what a report read.

- **Query Wait Statistics announced a metric it never used.** Query Store records
  wait time and nothing else per category, so the metric selector does not reach
  that query — gosmo says so outright — yet the line was built from `p.metric`
  and read "Avg CPU time" over a grid of milliseconds. The chart label already
  got this right, because it comes from the loader.
- **Regressed Queries named twice the range its rows covered.** The report
  compares the two halves of the window; the line said "over the last 24 h".
- **Query Store switched off counted its own explanation as a report** — "2 rows,
  Avg Duration over the last 24 h" for a query that never ran. Nothing tracked
  yet did the same.

Fixed by extending the idiom `chartLabel` already established in this file — the
loader that filled a value names it, so the label cannot disagree with the
column. `qsResult` gained `valueLabel` (what the value column carries, set from
the same string that became the header) and `note` (a status line supplied
outright, for grids whose rows are prose).

**The window needed one owner.** `queryStoreRegressionOptions` was applied inside
`regressedQueriesReport`, so the split was invisible to anyone else — and the
plan pane, reading `p.options()` independently, covered the whole window while
the rows above it covered half. One plan therefore reported more executions than
its query. Moved the call out to `queryStoreReport.effectiveOptions`, applied by
the caller exactly once: the two report callers and the plan pane now derive
their window from one function instead of three copies of one rule. It is *not*
idempotent — a second application splits the recent half again — which is why
the loader now takes its window as given and says so.

**Testing.** The alignment bug could only be asserted where the two windows
actually meet: the parameters reaching the server, via the `ReadArgs` recorder
added in the previous pass. Both windows come from separate `time.Now()` calls,
so they are never equal — the test allows two seconds and separately asserts the
plan window is *not* the baseline's, which is the shape of the bug (a twelve-hour
gap). All five tests were checked by mutating each fix back; the double-split a
re-added call in the loader would cause is caught by the same test.

One wrinkle worth remembering: `fakeResponse` matches by substring **in order**,
and every report's query contains the per-query `FROM`, so the interval report
was being handed twelve-column rows and coming back empty. The table-driven test
scripts the narrower answers first — otherwise it silently covered six of seven.

## 2026-08-28 — Query Store: the two selectors that did nothing

Third pass of the review. The `qsFilters` table already existed and already did
the right thing for the two filters on the action row — it just stopped two
controls short, so Metric and Top stayed live on reports whose queries do not
read them. Confirmed against gosmo rather than assumed:
`QueryStoreOverallConsumptionContext` renders no `TOP` at all (it was asked for a
time range, and dropping intervals out of the middle would misdraw the chart),
and `QueryStoreWaitCategoriesContext` renders no metric column (Query Store
records wait time per category and nothing else).

Added `qsFilterTop` and `qsFilterMetric`, gated `selDisabled` on them, and gave
the selector row the `selReason` half the action row already had — a dimmed cell
that only greys out is the same dead press with extra steps.

**Tracked Queries needed more than a flag.** Its floor was real: it reads the
same gosmo query the rankings do, so a floor left set on another view was
carried in and dropped a pinned query — which then showed up as "Not in Query
Store for this window", an explanation with nothing to do with why it went. So
the option is now gated at the caller (`filterValue`) rather than left to gosmo,
and the table and the query agree again.

Its Top is a different shape of dead: the query *does* carry a `TOP`, but
`trackedQueriesReport` raises it to fit the pinned set, so the selector can never
change the answer. That forced the meaning of the table to be stated properly —
"the toolbar controls that change what this report returns", not "the options
that reach gosmo" — and forced the test to pin *effect* rather than syntax.

**The test that nearly passed for the wrong reason.** Checking "is 25 among the
bound parameters" matched a tracked query whose id happened to be 25. Top is
`@p1` on every per-query report, so the check is now on the first parameter
alone; the tracked ids moved to 101+ as well. Same failure mode as the
`fakeResponse` ordering trap from the previous pass — a green table-driven test
covering less than it claimed.

Four mutations checked: ungating the selectors, ungating the floor, and claiming
in the table that Tracked honours Top or that Wait honours Metric. Each failed in
the test meant to catch it. `newTestApp` also gained the `contextMenu` that
`App.buildUI` builds — a panel popping a selector menu reached a nil one and
crashed instead of failing its assertion.

## 2026-08-28 — Query Store: the toolbar buttons that were never drawn

Fourth pass. Three small inconsistencies, and one that turned out to be a
shipped bug hiding behind them.

**The statistic a report opens on.** `defaultStat` was applied in
`NewQueryStorePanel` only, so opening a report leaf gave Total on a panel
created for it and whatever the previous view left behind on one already open —
the same click, two different numbers. Fixed with a `statChosen` flag: until the
user picks a statistic from the toolbar the panel follows each report's own
default; once they pick one it is theirs, which is what the metric already did
unconditionally. Resetting on every report change would have been simpler and
would have thrown away a deliberate choice.

**`qsDefaultTopIdx`** claimed to be gosmo's `QSDefaultTop` with nothing pinning
it, while its sibling `qsDefaultWindowIdx` had a test. Now pinned the same way,
including through `options()`.

**`drawChart`'s comment** described six of the seven orderings — Tracked Queries
sorts by last execution, being a pinned list rather than a ranking.

**The one that mattered.** The fourth item was to make `Script`'s label say
which statement it would produce, the way `Track Query`'s label follows the
cursor. Measuring first — because CLAUDE.md says a toolbar cell that does not
fit is not drawn at all — showed the action row already wanted 119 columns in a
pane that gets 70% of the terminal, and that `Compare Plans` was therefore
invisible below a 170-column terminal, `Track Query` below 132, and `Script`
below 120. None of the three has a key binding: `HandleKey` handles F5, Tab and
Ctrl+Up/Down and then delegates to the grid. So two features built in this same
working tree could not be invoked at all on an ordinary terminal, and adding
eight columns for `Script Unforce` would have made it worse.

This is the rule's own example turned around: the two filters were moved onto
the action row to stop the *selector* row dropping Refresh, and that pushed the
action row's own tail off instead. Moving the problem rather than solving it.

Fixed generally with `layoutToolButtonsOverflow`, which collapses whatever does
not fit behind a "More ▾" cell and returns the hidden indexes; both Query Store
rows use it, and each menu entry carries its button's own gate plus its reason
as `MenuItem.Note`, which is shown precisely while an item is disabled. Every
action is now reachable at every width, and the stateful `Script` label became
affordable. `layoutToolButtons` itself is untouched, so Activity Monitor and the
Log File Viewer keep their current behaviour — both should probably adopt this
too, which is noted in `docs/open-threads.md`.

**Two tests that passed for the wrong reason.** The suffix check ("the menu holds
the row's tail, not a scattered subset") passed against a deliberately broken
layout when run at a single width: the squeeze needs a long button followed by a
shorter one at exactly the wrong boundary. Sweeping 20..170 catches it. And the
gate test asserted on `Show Plan`, which still fitted at the width chosen — and
read `Untrack Query` because the process-wide pin set had leaked from the test
before it, which is the exact thing `useTempTracked` exists to prevent.

## Cross-repo review: the grid-width loss, the extended-property read, and three small ones

2026-08-28. A full read-through of both repos for bugs, inconsistencies and
refactoring. Worth recording what the sweep found *and* what it did not: every
mechanical check was already clean in both repos (`go vet`, `gofmt -l`,
`go test`, `go test -race`, and staticcheck down to cosmetics), there is no
`TODO`/`FIXME` in either tree, and every invariant in CLAUDE.md that can be
checked by grep held — no `SetData` inside an `OnSelectRow`, no grid drawn
without `DrawOverlay`, no `safego` where a latch needs `safegoRepair`, no
identifier reaching a T-SQL literal unbracketed, no filterable Detail Browser
loader missing `filterObjects`. Five things came out of it.

**Seventeen pages threw away a dragged column width, and the deliberate cursor
is what hid it.** The `redrawGrid` rule already says never to hand-roll
`SetData` plus a restore. What it did not cover is the *other* case: a page
whose row set changed and which therefore wants the cursor placed deliberately
— an Add selecting the row it just appended, a Remove falling back to row 0.
Seventeen sites wrote that as `grid.SetData(...)` then `grid.SetSelectedRow(n)`,
and `SetSource` clears `colWidthOverride` on the way through
(`datagrid.go:224`). So every Add, Remove and Revert on Role Members, Job Steps
(both the Properties page and New Job), Extended Properties, Database Files, and
the two permission matrices snapped a dragged column back to its computed width.

The reason this survived is instructive: the shipped symptom of the *other* half
of the same mistake is a keyboard trap — the cursor jumps to row 0, `GridRow`
diffs `SelectedCell`, reports "not handled", and `Form` moves focus out on the
first arrow key. Setting the cursor explicitly suppresses all of that. Nothing
jumps, nothing traps; a column just quietly narrows. No test could see it either,
because none of them had a width to lose.

`resetGrid` (`prop_grid_helpers.go`) is now the third member of the family —
`SetDataPreservingView` then `SetSelectedRow` — and `redrawGrid`'s note in
CLAUDE.md names it. The scroll behaviour is a small bonus: the restored scroll
plus `SetSelectedRow`'s own `ensureVisible` means appending a row scrolls just
far enough to show it, where `SetData`'s zero scroll drove `ensureVisible` from
the top and landed the new row against the bottom edge.

Pinned by `prop_grid_reset_test.go`, including a test that asserts `SetData`
*does* discard the width — so if `DataGrid` ever changes, the reason `resetGrid`
exists fails loudly rather than going stale.

**gosmo's extended-property read could not express database level.**
`ExtendedPropertiesContext` spelled level 0 with hard-coded quotes
(`N'%s', N'%s'` over `escapeSingle`) while `AddExtendedProperty`,
`SetExtendedProperty` and `DropExtendedProperty` all ran the same two arguments
through `nullableStr`. A zero `ExtendedPropertyLevel` therefore *wrote*
`@level0type = NULL` and *read* `fn_listextendedproperty(NULL, N'', N'', ...)`,
which SQL Server reads as a level named by the empty string — the property was
written against the database and read back as no rows, silently, on both sides.

gossms never hit it: `database_props_permissions.go` passes a zero level to the
form but reads through `DatabaseExtendedPropertiesContext`, which is a plain
`sys.extended_properties` query. So this was a trap for the next caller rather
than a live bug, which is exactly the kind gosmo has to fix — it is a library
first. Now `nullableStr` throughout, pinned by `extended_properties_read_test.go`
over the capture driver in `identifier_quoting_test.go`; the write side was
already pinned by `script_extended_properties_write_test.go`, and `WithScript`
cannot reach a read.

**Three small ones.** `QueryStorePanel.comparePlans` parsed `QueryPlanXML`
without the empty check `showPlan` makes twenty lines above, so a plan row Query
Store no longer holds the XML for reported a document error instead of saying
there is no plan. `newObjectDialog.runScript` had no zero-statement guard where
`PropDialog.runScript` has one. And `sqlStringLiteral` (`backup_common.go`)
hand-rolled quote-doubling that is `gosmo.QuoteLiteral` — now `"N" +
QuoteLiteral(s)`, with the existing injection-case test passing unchanged
against it, which is the whole verification for that one.

**A claim from the review that was wrong.** `deadcode ./cmd/...` reported
`formatValue`, `formatGUID` and `formatFloat` in `internal/query/executor.go` as
unreachable and the review wrote them up as dead. They are not: `deadcode` walks
the production build only, and all three are used by `executor_test.go` and
`bench_scan_test.go`. Removing two of them broke the build immediately. Their
doc comments now say what they are for, so the next `deadcode` run does not
raise them again. The only genuinely dead declaration the sweep found was an
unused `serverScope` field in `permissions_pages_test.go`.

**Four `requiredRight` values are unused on purpose** — `ALTER ANY LINKED
SERVER`, `ALTER ANY USER`, `ALTER ANY ROLE`, `CREATE TABLE` — because there is
no New User, New Role, New Table or linked-server feature to gate. They are
declared in that block rather than at a future call site because
`permission_gate_names_test.go` checks every literal there against gosmo's
`Probed*` list, and a name misspelled later would read back as
`CapabilityUnknown` forever and gate nothing. Now commented as such.

Left open, and recorded in `docs/open-threads.md`: `withRequires` reaches only
Database and Server Properties, and six dialogs hand-roll the same `dragField`
mouse routing.

## 2026-08-28 — every writable Properties page now declares its rights

The A2 half of the same review. `withRequires` was called from
`database_props.go` and `server_props.go` and nowhere else, so seventeen other
dialogs opened fully editable for a login whose writes the server would refuse.
All nineteen are wired now, per *page* rather than per dialog, and the choice of
right is the page's own: Login General and Status take `ALTER ANY LOGIN` while
Login Server Roles takes `ALTER ANY SERVER ROLE` and Login Securables takes
`CONTROL SERVER`, because those are three different answers to "who may do
this".

**The interesting part was not the wiring — it was that the check was a second
copy of the rule.** `pageReadOnlyReason` understood server-scope and
database-scope rights and nothing else. `allowsActionOn`, the menus' gate, also
understood membership rights (SQL Agent's msdb roles), schema-scoped rights and
object-scoped ones. Wiring `objectWriteRights()` into Table/Index/Key would have
shipped the divergence as a bug: a principal granted `ALTER` on one table reads
0 for every database- and schema-wide permission there is, so all five rights in
the set would have denied and the page they *can* write would have opened with a
banner telling them they cannot. `agentWriteRights()` would have failed the
other way — a membership read as a server permission comes back
`CapabilityUnknown`, which fails open, so the banner would never have appeared
for anyone.

So both now go through one function, `rightsAllow`. The only thing that differs
between the callers is how a database's capabilities are reached: the menus pass
`CachedDatabaseCapabilities`, because they run on the UI goroutine while a menu
is being drawn, and a page passes the probing `DatabaseCapabilities`, because
its `load` is already on a background goroutine. `propPage` gained
`requiresSchema`/`requiresObject` and `withRequiresOn` to carry the securable an
object-scoped right is asked about — for an index, a statistic or a key that is
the *table*, which is what SQL Server checks and what the probe records.

`prop_page_requires_test.go` is what makes `withRequires`'s docstring true.
It builds every dialog's page set and fails on a page that declares no rights
unless it is named in `pagesThatOnlyRead`, fails on a stale entry in that list,
fails on an object-scoped page that used plain `withRequires` (which compiles
and looks right), and parses the package back with `go/ast` to fail when a new
`[]propPage` constructor is not listed. All four were checked by mutation.
Twenty-nine pages are exempt because they have no `apply` at all; the one
exemption that is not a read-only page is Login > User Mapping, whose writes
create and drop *database* users, so what permits them is `ALTER ANY USER` in
each mapped database — a different answer per row of the grid, which one
page-level banner cannot state. A wrong banner there would be worse than none.

Verified live on win10cli with a throwaway login (`gossms_a2`, dropped
afterwards) holding `VIEW SERVER STATE`/`VIEW ANY DEFINITION`/`VIEW ANY
DATABASE` and, in `HealthClinic`, `ALTER` on `dbo.Doctors` and nothing else:

- Table Properties > Change Tracking on `dbo.Doctors` — editable dropdowns,
  OK/Cancel/Apply/Script Changes. The object-scope grant is honoured.
- The same page on `dbo.Invoices` — the banner, flat `OFF` renderings,
  Close/Script Changes.
- Login Properties > General on itself — "Requires ALTER ANY LOGIN
  (securityadmin)."
- As `sa`, both `dbo.Invoices` and Job Properties came up fully editable, which
  is the half that proves the gate is not just always-on.

One thing the live run showed that no test would have: the
`objectWriteRights()` banner wraps to *three* lines in Table Properties'
~83-column form. It is not clipped there, and `TestRequiresTextFitsTheReadOnlyBanner`
now carries a second, looser cap for the two sets that cannot fit one line,
with a note that Shrinkable clips a note's trailing lines first — so the line
that survives is always the one saying the page is read-only.

## 2026-08-28 — dialogs.FieldGesture, one copy of the drag-latch protocol

The B1 half of the review, and the last of it. Seven dialogs — `ConnectDialog`,
`FindReplaceDialog`, `LogSearchDialog`, `FilterDialog`, `BackupDialog`,
`RestoreDialog` and `dialogs.FileDialog` — each carried their own `dragField`
and their own three-part protocol for the text-selection gesture a click in an
`InputField` starts. `ARCHITECTURE.md` § The mouseDragging idiom is the one
piece of this codebase that says an idiom goes wrong when copied, and it had
been copied seven times.

`dialogs.FieldGesture` now holds it: `Release`, `Replay`, `Claim`, `Clear`. What
is worth having in one place is not the code — each body is two lines — but the
*placement*, none of which is local to the call it constrains. `Release` has to
run above `ConsumeOutsideClick` **and** above any early return for a dialog mode,
because both return without looking at the latch and a release outside the
dialog is exactly what strands it. `Replay` has to run after
`ConsumeOutsideClick` and before any hit-test, because hit-testing motion ends
the selection at the box edge and letting it reach `ButtonClicked` fires a
button when the drag wanders over the button row. Seven comments each restated a
different one of those; the type states all three once.

**What was deliberately not extracted.** The plan proposed a
`Route(ev, fields, buttons, onButton)` covering the whole `HandleMouse`. That
would have been worse: the middles genuinely differ — ConnectDialog hit-tests a
match-list overlay before `ConsumeOutsideClick`, RestoreDialog switches on four
modes and has a second entry point in its Files view, FilterDialog offers an
open dropdown first refusal, and the six set focus by three different mechanisms
(`focusIdx` by index, `focusTo(w)`, `setFocus(ffPath)`). Forcing them into one
router would have replaced honest duplication with a parameter list. Hit-testing
and focus stayed with each dialog, which is where they legitimately differ.

Coverage went from three dialogs to six. `dialog_drag_test.go` gained
`LogSearchDialog`, `FilterDialog` and `FindReplaceDialog` — the last being the
one the other five copied their comment from, and the only one with no drag test
of its own. Mutating each of the three methods in turn fails every one of the
seven dialogs' tests, which is the check that the shared helper is actually on
the path. The two "Show clears the latch" tests now arm the latch with a real
press instead of by assignment, so they also pin that a press arms one at all;
that needed the fake screen set *before* the dialog is constructed, since
`InitModal` captures it and a dialog with a zero rect treats every press as
outside.

Verified live under tmux on the real terminal path, which the unit tests cannot
reach (they synthesise events; tmux sends xterm SGR, and motion arrives as
button 32, not Button1): in Connect > Server, a press then a drag two rows down
over Port/Auth selected `abcde` and neither widget below stole it; the release
landed at (5,45), well outside the dialog, and the next single click cleared the
selection — which it can only do if the press was not swallowed as a stale drag.
Same sequence in File > Open's Path field, dragging down into the file list.

## 2026-08-28 — the other two panel toolbars stopped dropping buttons

`layoutToolButtonsOverflow` had one caller. Activity Monitor and the Log File
Viewer still used `layoutToolButtons`, which gives an overflowing button a zero
rect — neither painted nor hit-tested. Both were measured rather than reasoned
about, and both overflow at ordinary sizes:

- **The Log Viewer wants 121 columns** once both selectors carry the labels a
  real instance produces (`File: Current — 2026-08-28 00:00 ▾` is 34 of them),
  85 with the placeholder labels a panel that has not enumerated yet shows. The
  pane gets 70% of the terminal, so Search, Recycle and Export were unreachable
  on every terminal narrower than ~173 columns, and only Refresh has a key
  binding (F5). Confirmed live on win10cli at 110×32: the three sat behind the
  new More menu, and Search opened its dialog from it.
- **Activity Monitor wants 47** on the two dashboard tabs (rate selector plus
  Pause/Continue) and 30 on the procedure-backed ones. Pause has no key binding
  either, so it went off the row below a ~68-column terminal. Confirmed live at
  62×30: `10 s` and `Pause` behind the menu, and choosing Pause paused the feed
  — the entry read `Continue` when the menu was reopened.

Three things came out of the work.

`layoutToolButtonsOverflow` gained the prefix `layoutToolButtons` already took,
because Activity Monitor's row opens with `Refresh rate:` — 14 of the 23 columns
its own More cell needs. **The More cell displaces the prefix on a row too
narrow for both**: a label naming controls that are not on the row is worse than
no label, and drawn together the label's tail survives beside the cell that
replaced it. `ActivityMonitor.prefixVisible` is the draw side of that. Verified
at 28 columns, where the row is the More cell alone.

It also returns the end column now, which is not cosmetic: the Log Viewer puts
its filter field there and Activity Monitor fits the collector's state line into
what is left, and both must start past the More cell rather than under it.

The menu builder moved to `panel_toolbar.go` as `toolOverflowItems` — three
copies of "same gate as the button, reason as the item's Note" was one too many.
It marks a `selected` button with the bullet the drawn cell shows, since a rate
selector collapsed into a menu still has to say which of four is in force; only
Activity Monitor sets that field, so Query Store's rows are unchanged.

Each panel got the two tests Query Store's row already had — every cell drawn or
in the menu at every width from 20 to 200, the hidden set a suffix, the More cell
inside the pane — plus an end-to-end one that presses the cell and runs the
entry, and a gate test (Recycle withheld from a login refused CONTROL SERVER is
withheld in the menu too, with the right named in its Note). Mutating each
panel's `layoutTools` back to `layoutToolButtons` fails all of them.

## 2026-08-28 — the Details pane got a write path: Delete over a selection

Object Explorer's Delete had one shape — one node, one object — because
`controls.TreeView` has one selection. The Details pane's grid has had block
selection all along and no route to an action, so its selection fed only the
clipboard. That is the whole of what was missing, and it is now built.

**Three pieces, one of them in tuikit.** `DataGrid.OnMenuItems` lets a host add
entries to the grid's own cell context menu, below a divider after Copy / Show
Value, asked afresh each time the menu opens so an entry can describe the
selection in force. A second menu of the pane's own would have been drawn
outside the grid's overlay and would have fought the grid's for every key —
`DrawOverlay` and `OverlayActive` exist precisely because that menu is not on
the grid's rect.

**The pane deletes objects, not rows.** A detail grid is `[][]string` shared
with every other node type, and its Name cell is a *rendering* — a decorated
label (`IX_x (Nonclustered, Unique)`), or a schema-qualified name a parse could
get subtly wrong. So `detailResult` carries `objs []nodeData`, one per row,
filled by the loaders whose rows are objects: `fetchChildObjectsDetail` hands
over each child's own `nodeData` (which is what covers thirteen families at
once), and the Views, Stored Procedures, Tables, Databases and Logins loaders
build theirs from the listing they already made. `fetchNodeDetails` takes it as
an out-parameter rather than a fourth result, because only a handful of its
twenty-odd arms have anything to say about it. `setRowObjects` refuses a
mapping whose length does not match the rows: the pane deletes by row index, so
one short would drop the object the *next* row describes, and losing Delete is
the safe failure.

**One delete path, not two.** `deleteObject` is now a one-object call into
`confirmDeleteObjects`, which both surfaces share. A second copy would be where
a warning, a typed confirmation or the foreign-key option quietly stopped
applying — the difference between deleting a table from the tree and from the
pane has to be where the click landed and nothing else. The batch runs
sequentially on one goroutine and reports `Deleted 2 of 3 — dbo.T3 failed: …`
rather than a single error standing for the whole selection.

Three rules the batch needed that a single delete never did. A **typed**
op — a database, a column master or encryption key — is refused in a multi-row
selection and named, because a typed confirmation asks for *one* object's name.
The **drop option** checkbox is offered only when every selected object carries
the same one, since one tick drives every drop. And the shared **warning** is
introduced ("Each object: All of its data is deleted with it.") rather than
repeated, because every op's warning is written about a single object.

**Gating is per object, not per folder.** `rightAlterOnSchema` and
`rightAlterOnObject` answer about a named securable, so a selection spanning
two schemas can be permitted in one and refused in the other; one answer for
the batch would be right about at most one of them. The withheld item's note
names the object that is the problem — "needs ALTER" over a forty-row selection
says nothing about what to deselect. `nodeData.IsSystem` is checked the same
way, and re-checked inside `confirmDeleteObjects`, which is the function that
issues the DROP.

**What the live run changed.** Two things no unit test had an opinion about.
The confirmation's object list was laid out with newlines, and `ConfirmDialog`
wraps and centres — so it arrived as `…This cannot be undone. sales.Beta
sales.Gamma All of its…`, prose with the names run into it. It is one flowing
sentence now, names comma-separated. And `DetailBrowser.SetActive` never
reached the grid, so its cell cursor drew in the inactive palette; harmless
while the selection meant nothing, wrong now that it is what Delete acts on.

Driven end to end against win10cli on a throwaway database: three tables
selected with Shift+Down and dropped (the two unselected ones untouched, both
verified in `sys.tables`), the same selection cancelled with No and nothing
dropped, two databases refused with `Database "backup_test" has to be deleted
on its own — select just that row`, the System Databases view offering no
Delete at all, and finally the throwaway database itself deleted from the pane
through the typed confirmation — which is what cleaned up after the test.

Mutation-checked: acting on the first rows instead of the selected ones,
dropping only the first object, gating on only the first object, and removing
the typed refusal each fail a test. `selectPaneRows` in the test file carries
its own warning — `SetSelectedCell` moves the cursor and leaves the previous
block selection's anchor where it was, so a test that used it twice selected
from the old anchor and passed for the wrong reason once already.

## 2026-08-28 — multi-select Delete stops at the schema boundary

The pane's batch Delete was refused only for a `typed` op — a database, either
Always Encrypted key. That left logins, server roles, users and database roles
batch-deletable, which is not what a Delete over a selection is for: dropping a
login orphans every database user mapped to it, dropping a user or a role
silently removes memberships and the permissions that came with them, and the
batch confirmation carries *one* shared warning for the whole set, written
about one object. A schema-scoped object — table, view, procedure, index — is
the case where a set genuinely reads as a set.

So `objectOp` gained `solo`, "deleted one at a time", set on Login, Server
Role, User and Database Role, with `typed` implying it (`deletedAlone`).
`confirmDeleteObjects` refuses on that instead of on `typed`, and the pane's
menu now *shows* the refusal rather than waiting for the click: the item is
built, disabled, and given `soloDeleteReason` as its Note, naming the row to
keep. Leaving it out of the menu entirely was the alternative and is worse — a
Delete that disappears when a second row is selected reads as the pane having
no Delete. Both halves take their wording from the one function, so the
withheld item and the status line cannot drift apart.

The rule is the selection's, not the type's: one login selected in the pane
still deletes through the same path, and the tree — single selection, always
the `len(objs) == 1` branch — is untouched.

`TestOnlySchemaScopedObjectsAreDeletedAsASelection` pins both buckets by node
type, so a new principal added to `objectOps` without `solo` fails; the pane
tests cover the withheld item, the single-login delete that still works, and
`confirmDeleteObjects` refusing a batch it is handed directly, since the menu
gate is not the enforcement. Mutation-checked by dropping `|| op.solo`: four
failures, one per surface.

## 2026-08-28 — a pin now reaches the tree's Tracked Queries leaf

Tracked Queries shipped with a known limit: the tree's leaf is a Detail Browser
node like any other, its rows cached per node, so pinning a query in the Query
Store panel left the leaf listing the set from before — which reads as the pin
not having worked, since the leaf is where you go to see what is pinned.

`DetailBrowser` had `Invalidate(app, node)` for one known pointer. What a
toggle needs is a class: gained `InvalidateWhere(app, match)`, which drops every
matching cache and pending entry and refetches `currentNode` when it matches —
currentNode separately, because a node whose fetch failed or is still in flight
is not in the cache map and is still the one on screen.

`App.trackedQueriesChanged(server, dbName)` is the single entry point, and
covers Query Store panels as well as the tree: one panel exists per (connection,
database), so a second connection to the same instance has its own panel reading
the same file-backed set. Both are matched on the *server address* through the
new `config.SameServer`, which folds it the way the sets are keyed —
`HOST\SQL2022` and `host\sql2022` are one set, and a string comparison in the
caller would have made them two. The tracked leaf is recognised by its report's
`qsFilterTracked` flag rather than by the title "Tracked Queries", so the flag
stays the one definition of which view reads the pinned set.

`toggleTracked` calls it on the failed-save path too — the in-memory set took
the toggle either way, and that is what every view reads.

A/B'd against win10cli on a throwaway `l3demo` (Query Store on, capture mode
ALL, since AUTO records nothing for four trivial statements): visit the leaf
first so it caches "None yet", pin query 10 in the panel, come back to the leaf.
The pre-fix binary still says "None yet"; this one lists query 10. Three tests,
each killed by its own mutation: dropping the call from `toggleTracked`, and
comparing the two addresses with `==` instead of `SameServer`.

One tmux lesson worth keeping: `awk 'index($0,"Track Query")'` over a
`capture-pane` line gives a *byte* offset, and the Object Explorer's folder
emoji make that several columns off — every click on the panel toolbar landed
in empty space and looked like a dead button. Compute the display column
(east-asian width) before building the SGR sequence.

## 2026-08-28 — the certificate exchange got tested

`importPeerCertificate` was the endpoint pipeline's one untested phase. Every
existing test drives `configure` under `WithScript`, and that is precisely the
run where the exchange does *not* happen: nothing was really created, so no
public key can be read and each peer is recorded as skipped. The exchange proper
had only ever run against live instances.

It turns out not to need one. `newFakeConn` hands back a real `*gosmo.Server`
over the scripted driver, so a peer can be built the way `configure` builds it —
`ensureCertificate` first, against an instance scripted as already having a
master key and its own certificate, which fills `cert` and `encoded` — and the
exchange then runs for real, emitting statements the fake records.

Six tests, each mutation-checked against the defect it exists for: importing
`p.encoded` instead of `other.encoded` (the direction), dropping the thumbprint
comparison on an existing certificate (the reinstalled-peer case, which
otherwise reports success and leaves the endpoint refusing the peer), treating
every login lookup failure as absence (which reports the CREATE LOGIN error
instead of the permission error that really stopped the pipeline), recording the
skip on the peer rather than on the instance whose script is missing the import,
creating the user unconditionally (CREATE USER is not idempotent, and a
half-configured pair is the ordinary second run), and skipping the existing-
certificate read.

The one that took a second attempt to state properly is the direction. Asserting
"a CREATE CERTIFICATE ... FROM BINARY ran" passes against an instance importing
its own key, so the test compares the hex against `other.encoded` *and* asserts
this instance's own bytes do not appear — the two peers are scripted with
distinguishable keys (`UBUSQL1-public-key`, `UBUSQL2-public-key`) for exactly
that. It also asserts nothing at all was written to the peer being read from.

The harness's limits still hold: this says the phase asked for the right things
and built the right statements, never that the T-SQL is valid — the live run of
2026-08-22 is what says that.

## 2026-08-28 — the two sp_delete_jobstep renderings became one

`Job.deleteStepAt` had been uncalled since `ReorderStepsContext` became one
transactional batch, and the open thread left the question as the author's:
delete it, or keep it. Neither was needed. `JobStep.DeleteContext` was building
the same `sp_delete_jobstep` call inline — it predates `deleteStepStmt` — so it
now reads `return s.job.deleteStepAt(ctx, s.StepID)`. The uncalled function has
a caller, the duplication is gone, and one function renders that procedure call
for every path that deletes a step: the method, the number-only form, and the
reorder batch through `deleteStepStmt`. Statement text and error text are
unchanged, which is what makes this behaviour-preserving at the API surface.

Nothing was removed. The no-removal rule is about exported surface, but the
point generalises: an uncalled unexported helper is better answered by finding
its missing caller than by arguing over its deletion.

Three tests in gosmo, each mutation-checked: sending step 1 instead of the
step's own number (a delete that removes a step the caller never named, and
succeeds doing it), dropping `escapeSingle` from the job name, and reinstating
the inline duplicate with a drifted spelling — `@step_id=%d` without the space
fails all three at once, which is precisely the drift the fold prevents.

Verified live against win10cli through gosmo directly rather than through the
dialog: a throwaway three-step job, step 2 deleted with `JobStep.DeleteContext`,
msdb left holding "one" and "three" renumbered to 1 and 2. Driving the Steps
page under tmux was the first attempt and was abandoned — injected SGR clicks on
the page's `[ Delete ]` button never registered, while clicks on the tree, the
context menu and the panel tabs all did. Worth a look if a later test needs that
button; the statement path itself is the same either way.

## 2026-08-28 — Ctrl+click in the Details pane, and a Script button on every delete

Two requests, both about the same gesture: picking objects to delete and
deciding what happens to them.

**Ctrl+click.** The Details grid already had Shift+click and drag — a block
selection, an anchor and a cursor and everything between. What it could not
express is "these two and not the one between them", which is exactly what a
selection built for deleting needs. `DataGrid` grew `markedRows`/`marking`
beside the block fields: Ctrl+click folds the selection in force into a set of
row indices and then toggles the clicked row, and anything that is *not* a
Ctrl+click — a plain click, a Shift+click, an arrow key, a new row set — drops
the set, which is the rule a file manager's list follows.

Three details that are not obvious:

- The fold-in includes the lone cursor row. This grid always highlights one, and
  every host that acts on the selection acts on that row when nothing else is
  picked, so a first Ctrl+click that dropped it would deselect a row the user can
  see is selected.
- Unpicking the last marked row leaves an empty selection, not a fall back to the
  cursor's row — hence `marking` as a flag separate from `len(markedRows) > 0`.
  The menu then offers nothing, which is right.
- One toggle per *press*. tcell resends `Button1` for as long as the button is
  held, and without the `mouseDragging` latch the second resend undoes the first
  before the user lets go: the row flickers and ends up unselected.

The half that matters outside the widget is `SelectedRows()`. A host reading
`SelectionBounds()` gets a rectangle — rows 1 and 3 come back as 1..3 — so the
Details pane would have deleted the row the user deliberately left out while the
confirmation named only the two they picked. `selectedRowObjects` was the one
caller and now walks the row list; the rule is in `CLAUDE.md`.

**The Script button.** `dbOf`'s comment had said for months that it uses
`Server.Database` rather than `DatabaseByName` because "Script Changes on Delete
needs it" — a feature that did not exist yet. It does now, and it needed no new
statement-building code: `scriptDeletes` runs the same `objectOp.drop` /
`dropWithOption` closures under `gosmo.WithScript` and opens what they would have
executed. A second rendering built beside the drops is exactly the thing that
drifts from them.

`ConfirmDialog` and `TypedConfirmDialog` both answered by button *index* — Escape
was `ConfirmAnswer(n - 1)`, the last button. Adding a third button would have made
Escape answer Script, so both dialogs now carry a `buttons`/`answers` pair and an
explicit escape answer. In the typed dialog Script is not gated on the retyped
name: it runs nothing, so there is nothing for the retyping to protect.

Found while testing: `answerConfirm` in the tests reached the checkbox by
Tabbing twice past the buttons, which broke the moment there were three. One
Backtab reaches it whatever the button count is — the checkbox sits last in the
cycle.

**Live on win10cli**, throwaway `ctrldemo` with four tables. Ctrl+click on the
first and third rows of four gave `Delete 2 Tables...` and dropped exactly those
two; Shift+click still gave a run of three; Script on that pair opened the two
`DROP TABLE`s with the tables still on the server afterwards; Script on the
database's typed confirmation, with the name field left empty, produced the
`SET SINGLE_USER WITH ROLLBACK IMMEDIATE` that a hand-written script would have
forgotten. The database was then dropped through the dialog itself, which also
showed the typed gate still refusing an empty Confirm.

## 2026-08-28 — Shift+click was never reaching the app

Reported straight after the Ctrl+click work landed: Shift+click does not extend
the selection in the Details pane, while drag and Shift+arrows both do. The code
was right — the same gesture, injected as an SGR sequence under tmux, selected a
run of three rows. That is the whole diagnosis: **a tmux harness bypasses the
terminal.**

xfce4-terminal is VTE, and VTE keeps Shift+mouse for its own text selection
whenever an application has mouse reporting on. It forwards nothing, so the app
sees no event at all — indistinguishable, from inside, from a binding that does
nothing. It is not a preference and cannot be turned off; it is how VTE decides
the user meant the terminal's selection rather than the app's. Ctrl and Alt are
delivered.

Two changes, neither of them to the selection logic:

- `extendSelectionMods` (`datagrid_input.go`) is `ModShift | ModAlt`. Alt+click
  extends exactly as Shift+click does, and is the gesture that works where Shift
  is taken. The test runs both modifiers through the same case, because a
  fallback that behaves differently from the gesture it stands in for is a
  second thing to learn rather than a way round the terminal.
- Key Diagnostics logs mouse events — button, modifiers, position — because
  nothing else in the app can answer "did the terminal deliver this". Drag
  resends collapse: only a change of buttons or modifiers is recorded, so a
  drag is two lines and not two hundred, and the press that started it stays
  visible. Verified live: plain, Shift, Alt and Ctrl clicks each logged one
  press and one release with the right `Mod=`.

The rule went into `CLAUDE.md`: a Shift+mouse gesture needs a second modifier
meaning the same thing and a keyboard route as well, and a green tmux run proves
nothing about whether a modifier survives the terminal.
