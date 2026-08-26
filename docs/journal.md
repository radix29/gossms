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
