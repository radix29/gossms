# Review plan — 2026-11-04

The fix list from the eighth whole-repo review of gossms and gosmo. Since the
2026-11-03 plan, both repos took the whole command line (`internal/cli`, about
25,000 lines), `internal/scripting`, 21 gosmo commits behind it (about 8,500
lines: space, maintenance, plan cache, deadlock reports, trace flags, config
defaults) and the colour-theme refactor. This pass spent most of its time on
those. Nothing settled in `docs/decisions.md`, `~/go/gosmo/OPEN-THREADS.md` or
either `CLAUDE.md` is re-raised. The author's waiver applies again: **gosmo
may break backward compatibility where it helps** (A2, B4).

**Baseline (2026-11-04, gossms `HEAD` 976a430 / gosmo 3db4039):** both repos
build; `go test ./...` passes in both; `go vet ./...` passes in both, and so
does `go vet -tags livedb ./...` in gosmo; `staticcheck ./...` is clean in
both; `govulncheck` finds nothing reachable (the x/crypto advisory H1 of the
2026-11-03 plan is still module-level only); `go test -race -count=1 ./...`
passes in both.

How it was found:

- **Tools**: vet, staticcheck, deadcode, govulncheck, `dupl -t 70/80/120`.
  `deadcode` lists only what `docs/decisions.md` § Code and dependency
  hygiene keeps, plus test helpers; one entry there has gone stale (D1).
  `dupl` found no group worth acting on: the largest clusters are the
  declarative menu tables in `explorer_*.go`, already factored through
  `folderMenu`/`propertiesOnlyMenu`.
- **Pattern sweeps**: raw `go` statements outside `safego` (three, all
  justified in place); `context.Background()` in UI code (all detached on
  purpose); `runtime.GOOS` (only where `CLAUDE.md` says); hard-coded colours
  outside `tuikit/theme` (B1); package-level style captures (B1); every
  `--confirm` comparison in `internal/cli/cmd`.
- **Driven offline**: the built binary's `query classify` over about 70
  edge-case statements (B3), every output format with wide, combining,
  pipe, tab, quote and newline characters, `--max-col-width`,
  `--max-output-bytes`, bad flags, BOM-only and invalid-UTF-8 input, SIGPIPE.
  Only B2 and B3 came out of it; the rest behaved as `docs/cli.md` says.
- **Read in full**: gosmo `retry.go`, `transaction.go`,
  `database_maintenance.go`, `deadlock_report.go`, the version-sweep list;
  gossms `internal/query/session.go`, `internal/config/config.go` and
  `config_save.go`, `internal/cli/cmd/common.go`, `internal/cli/connect`,
  the `--confirm` paths of `maint_restore.go`/`data_import.go`,
  `tuikit/theme`, `options_dialog.go`'s apply, `dialog_stack.go`. Other
  files were sampled, not read whole.
- **No live drive this pass**: it is plan-only. B1's contrast numbers are
  computed from the palette RGB values; confirm them in tmux before fixing.

This pass found **no data-loss, crash or security bug**. It found one visible
theme defect, two small command-line defects, one gosmo error-shape bug
already on record, four open threads that earlier work has made cheap to
close, two architecture questions, lost leftovers from the last plan, and
a version-sweep gap.

`docs/decisions.md` § No top-level plan document still holds. This file exists
only because it was asked for. Delete it when the work is done, and move
anything left over into `docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
`TestNoDanglingDocReference` checks every `.md` path named here, so cite only
paths that exist.

Every gosmo change follows `~/go/gosmo/CLAUDE.md`:

- build and test in gosmo first;
- run `go vet -tags livedb ./...` after any rename;
- every new or changed write gets an offline test of its exact statement,
  mutation-checked;
- a new or changed read runs in `TestLiveVersionSweep` on the oldest instance
  (win10cli\SQL2016).

Every gossms change follows `docs/ui-rules.md`, `docs/db-rules.md` and
`docs/testing.md`. Drive it in the built binary before calling it done.

IDs: **B** bug or hazard, **G** gap closable now, **S** simplification or
robustness, **A** architecture, **V** verification gap, **D** docs,
**Q** question for the author.

---

## Bugs and hazards

### B1 — tuikit: four styles force a white foreground onto the palette's accent grounds

`theme.Palette` has `SelectedText` for "text on an accent ground", and the mc
skins set it to black because their accent is cyan. Four styles ignore it
and hard-code `color.White`:

- `StyleStatusBar` (`styles.go`): white on `StatusBar`.
- `StyleGridSelected`: white on `GridSelected`.
- `StyleButtonActive`: white on `ButtonActive`.
- `dialogs/modal.go`'s button-row `activeStyle`: white on `ButtonActive`.

Under both mc themes those grounds are cyan, so the result is white on cyan.
That gives about 2.9:1 contrast on VGA cyan (`MCRetro`) and 3.6:1 on Tango
cyan (`MC`), against about 7:1 for the black the palette asks for. Midnight
Commander itself draws black on cyan there. Three uses are white on a dark
`MenuSelected` and read fine: `StyleMenuSelected`, the toolbar hover and the
Activity Monitor selected tab. They should still take a palette role so a
future palette can't repeat this.

Two related slips:

- `StyleGridStatus` is a fixed light yellow and black, outside the palette.
- `query_panel.go` captures it once in the package variable
  `resultsStatusStyle`, at init, before `applyColorTheme` runs. That is
  harmless only while the style ignores the palette. Once it follows the
  palette, every grid built before a theme switch (and every grid built at
  all, since init runs before the configured theme is applied) keeps the
  Default palette's status bar.

**Fix:**

1. Add foreground roles where one ground needs its own text colour, such as
   `StatusBarText` and `ButtonActiveText`. Use `SelectedText` for
   `GridSelected`. Set every role in all four palettes;
   `TestPalettesSetEveryColour` will enforce that.
2. Make `StyleGridStatus` palette-backed (`GridStatusBg`/`GridStatusText`,
   with the current yellow and black in Default and Light).
3. Replace the `resultsStatusStyle` variable with a call at draw time. Either
   `DataGrid` reads `theme.StyleGridStatus()` itself when no override is set,
   or the five `SetStatusStyle(resultsStatusStyle)` call sites go away.
4. Add a palette test that computes WCAG contrast for every (foreground,
   ground) pair the `Style*` functions build, for every palette. Fail below a
   floor such as 4.5:1 for text, and list any deliberate exceptions by name.
   This is the test that would have caught B1.

Drive each theme in tmux: status bar, a selected grid row, the active button
in a dialog, and the results status line before and after a live switch
through Tools ▸ Options.

### B2 — CLI: `--max-col-width` can make a cell longer than it was

`output.clip` (`internal/cli/output/cells.go`) cuts at `width` runes and
appends `…[+N chars]`. When fewer characters are cut than the marker adds,
the marker makes the cell longer. Driven: `--max-col-width 11` on the
12-character `[abcdefghij]` prints `[abcdefghij…[+1 chars]`, which is 22
characters. That defeats the option, whose purpose is to bound line width
for an agent's context. Also, "+1 chars" should be "+1 char".

**Fix:** clip only when the cut saves room. Keep the cell whole when
`n - width` is no more than the marker's own length, or count the marker
within `width`. `docs/cli.md` § Output limits must match; it says "cuts a
cell to `n` characters". Pin it with a table test at the boundary
(n = width+1 … width+marker length). Get the singular right.

### B3 — CLI safety: `NEXT VALUE FOR` is classified `read`

`query classify --sql "SELECT NEXT VALUE FOR dbo.seq"` answers `read`, so
`--allow read` runs it. It advances the sequence permanently, and a
sequence advance is not undone by a rollback, so
`--transaction rollback` does not protect against it either. That is the
same class of hole as D3's ("reported, not refused").

**Fix:** add the phrase `NEXT VALUE FOR`, outside literals, comments and
quoted identifiers, as a write floor in `internal/cli/safety`. Report it
with the rollback-hole warning D3 already gives. Add rows to
`classify_test.go` and a line to `docs/cli.md` § Safety's class table. A
column named `next` stays read because the floor is the three-word phrase.

### B4 — gosmo: a database-scoped read wraps the `gosmo: USE` error twice (on record)

`~/go/gosmo/OPEN-THREADS.md` § A database-scoped read wraps the `gosmo: USE`
error a second time. It surfaces in the command line, `db info` as a login
with no user in the database:
`gosmo: space used: gosmo: USE HealthClinic: …`. Every `d.query`/`d.queryRow`
caller that wraps does the same, and the CLI is now the main surface for it.

**Fix:** in the helpers. Have `procRead`/`recheckEntry` return the can't-enter
failure as a typed error (`*EntryError{Database, Err}`, `Unwrap`) without the
`gosmo:` prefix, and let the caller's wrap add the only prefix. The message
then reads `gosmo: space used: cannot enter database HealthClinic: …`. This
changes error text, which the waiver allows. Extend
`TestNoWrapOfAnAlreadyPrefixedError` to call one database-scoped read
against a fake that refuses entry and assert exactly one `gosmo:` in the
result. Then fix the gossms tests that match the old text.

---

## Gaps closable now (open threads earlier work made cheap)

### G1 — CLI: an application role as a grant/deny/revoke or role-member principal

`docs/open-threads.md` § Command line: `resolvePrincipal`
(`internal/cli/cmd/security.go`) looks up a user, then a database role. An
application role is therefore `NOT_FOUND`, exit 8, though SQL Server accepts
it. gosmo has had `Database.ApplicationRoleByName` since 2026-11-03, so the
fix is a third lookup, a `Catalog` method and a test. `security permissions`
shares the gap and the fix. Drive it on 17 against a throwaway application
role. Close the open-thread item.

### G2 — CLI: `object describe`/`object indexes` show a hidden filter as no filter

`docs/open-threads.md` § Command line. gosmo reports `Index.IsFilterHidden`,
but both commands print an empty `filter`, which looks like an unfiltered
index. **Fix:** print `filter` as NULL plus a warning naming the index, the
way a hidden default or check is handled. Use a fake-catalog test, and drive
it on 17 as a login without VIEW DEFINITION.

### G3 — CLI: a `--confirm` refusal's audit line has no target

`docs/open-threads.md` § Command line. The open thread already names the
fix: `logCall` fills an empty target through `connect.Describe`. That one
change covers `db drop`, `db set --rollback-immediate`, `monitor kill` and
`data import --truncate-first`. Add a test per command asserting the
`BLOCKED` line's `server`/`database`/`login`.

### G4 — gosmo test hygiene: two known test defects with known fixes

From `~/go/gosmo/OPEN-THREADS.md`:

- **Live tests leave their backup files behind on 13 and 14.** Replace
  `xp_delete_files` (15+) with `master.sys.xp_delete_file 0, N'<file>'`
  through one shared helper in the live test files, and stop ignoring its
  error.
- **`TestLiveWrongValuesAgentJob` orders History by the second, flakily.**
  Compare by `instance_id`, or accept either order when the run times
  differ.

Both are test-only, so no API changes. Run the ten affected files on 13 and
check the default data folder is clean afterwards.

---

## Simplifications and robustness

### S1 — gossms config: make the settings baseline structurally independent

`Config.settingsSnapshot` copies the struct shallowly, so `base` and the
`SaveJob` snapshot share `XEventHiddenColumns`/`XEventViewSettings` with the
live config. The rule that keeps saves correct is a convention: those maps
are "replaced, never written into", followed by the two writers in
`xevent_viewer_toolbar.go` and `xevent_viewer_settings.go`. A future writer
that changes a map in place makes `mergeSettings` see no change, and the
setting is silently not saved.

**Fix:** deep-clone map and slice fields in `settingsSnapshot`, by reflection
like `mergeSettings` so a new field is covered. Then drop the convention
from the two field comments. Add a test that changes a map in place, saves,
reloads and finds the change.

### S2 — gossms: three identifier unquoters where gosmo has one

- `safety.unquote` (`internal/cli/safety/classify.go`)
- `sqlparse.unquoteIdent` (`internal/tui/sqlparse/token.go`)
- gosmo's `tsql.UnquoteName`, which gossms already imports elsewhere

The first two handle an *unterminated* identifier (an editor mid-typing),
which `UnquoteName` deliberately returns unchanged, so they can't simply be
swapped. **Fix:** add `tsql.UnquoteNameLenient` (or a `Span.Unescape` call
on a scanned span) for the lexer-fed case, and route both through it. This
only pays if it is done alongside another change in those files.
Otherwise, record it in `docs/decisions.md` as reviewed and left.

### S3 — both: files over 900 lines

The 900-line rule in `CLAUDE.md` is a prompt to split, not a defect:

- `internal/cli/cmd/common.go` (940): most of it is the `serverCatalog`
  delegation, which A1 would remove. Otherwise move it to
  `common_catalog.go` by exact line range.
- `internal/cli/cmd/db_storage.go` (934)
- `internal/cli/cmd/query.go` (916)
- `internal/tui/explorer_databases.go` (930)
- gosmo `database.go` (929)

Split each one only when a change lands there anyway, along its own section
comments. A new `internal/tui` file needs its `ARCHITECTURE.md` row.

---

## Architecture

### A1 — CLI: the 160-method `Catalog`/`Writer` seam (question for the author)

`internal/cli/cmd/common.go` declares `Catalog` with 122 methods, and
`write.go` declares `Writer` with 34. Each `serverCatalog` method is a
one-line delegation to gosmo, about 600 lines in all, which exists so tests
can supply fakes (`objectCatalog`, `dbCatalog`, `restoreCatalog`, … each
embedding the interface). Every new command adds to three places: the
interface, the delegation and a fake. Two costs follow:

- What the fakes return is the test author's idea of gosmo's answer, never
  gosmo's code path. A gosmo change in scanning, `Ref` semantics or errors
  passes every CLI test.
- A fake that doesn't override a method panics on a nil embedded interface,
  which reads as a test bug rather than an unexpected call.

**Option A (recommended): a `gosmotest` package in gosmo.** gosmo already
has a row-serving fake driver for its own tests (`fakeQueryConn`, used by
`login_map_test.go`). Exporting it, as `gosmotest.NewServer(t, gosmotest.Rows{…})`
with query matching by substring or regexp, lets CLI tests run real gosmo
over canned rows. `Catalog` then shrinks to nothing, or to the handful of
calls that genuinely need a seam (time, stdin). It is a library feature,
not a shim, and other gosmo users get it too. The cost is high: each CLI
test's fake is rewritten as canned rows, which is a lot of mechanical
churn, best done one command group per change.

**Option B: split `Catalog` per command group** (`objectReads`,
`storageReads`, `securityReads`, `agentReads`, …), each command taking only
its slice. This is cheaper and shrinks each fake, but keeps the delegation
and the unverified-fake problem.

**Q1:** A, B, or leave it? The plan recommends A, started with one group
(`object`) as a pilot to measure the churn before committing to the rest.

### A2 — gosmo: `BulkInsert` should own value checking and encoding (question for the author)

`docs/open-threads.md` § Command line: `BulkInsert` passes values to
go-mssqldb unchecked. An int too large for its column wraps (300 into a
tinyint loads 44), and a Go string bound for a `varchar` column is read in
the database's default code page. gossms avoids this only because
`data import` first runs every cell through `query.NewCellParser`
(`internal/query/cellparse.go`, 618 lines), and every other `BulkInsert`
caller is exposed.

**Option:** move the type-aware conversion into gosmo. `BulkCopy` resolves
each target column's type, precision, scale and collation code page once,
then `BulkInsert` checks ranges and encodes `varchar` through that code page,
refusing with `ErrInvalidRequest` and naming the row and column.
`NewCellParser` then becomes a thin text-to-Go layer over gosmo's checks,
or gosmo exports a `ParseCell` for text sources. This breaks `BulkInsert`'s
contract for callers that relied on driver coercion, which the waiver
allows. It closes the open-thread item instead of documenting it.

**Q2:** move it, or only document the contract on `BulkInsert` and keep the
check in gossms? The plan recommends moving it, since silent wrong data is
the worst failure a bulk loader has.

The previous plan's Q1 (the `App` struct's dialog fields) is not re-raised.

---

## Verification gaps

### V1 — gosmo: the discovery reads are not in `TestLiveVersionSweep`

The command line's discovery reads have their own live tests
(`live_discovery_reads_test.go`, `live_collation_code_page_test.go`) but are
missing from the sweep's hand-driven list in `live_versionsweep_test.go`:

- `Database.Search`
- `Database.SearchDefinitions`
- `Database.SearchColumns`
- `Database.ObjectByName`
- `Database.ObjectsNamed`
- `Server.CollationCodePage`

The sweep is the one test run on the oldest instance after every query
change, so a 2017+ column slipped into one of these fails only when someone
remembers the specific test. **Fix:** add a `sw.call` for each against the
sweep's scratch schema, and list them in the hand-driven set. Run the sweep
on 13 and record the call and failure totals here.

---

## Docs

### D1 — `docs/decisions.md` § Code and dependency hygiene: `theme.SetPalette` is no longer test-only

The entry says `theme.SetPalette` and `widgets.SpinnerByName` stay in
production files "though only tests reach them". Since the colour-theme
work, `applyColorTheme` (`options_dialog.go`) calls `SetPalette` at startup
and on every Options apply, and `deadcode` no longer lists it. Narrow the
entry to `SpinnerByName` and `UseTrackedQueries`.

### D2 — the 2026-11-03 plan's leftovers were deleted with it

That plan's steps 7 and 8 each ended with a "Left over" list. None of it
reached `docs/open-threads.md` when the plan was retired:

- Application roles have no Script menu, and gosmo has no
  `ScriptApplicationRole` (Script Database Security emits the one-role form
  privately).
- The owner pickers never offer an application role, though one can own a
  schema. `principalNames(users, roles)` feeds Owned Schemas' transfer
  picker, New Schema, Schema Properties, the key owner pickers and Full-Text.
- Database Role Properties renames without `showReloading`, and it is
  missing from `TestPropertiesApplyReloadsTheFolderListingTheObject`, which
  is a `docs/ui-rules.md` miss. This pass confirmed that the test has no
  Database Role row. It did not drive the rename, so check whether the tree
  really keeps the old label before fixing it.
- A login with no right on an application role sees the folder empty
  (catalog visibility).
- Sequences: values are int64, so a `decimal(38,0)` sequence's range past
  bigint can't be written. There is no Last Used Value row (gosmo's
  `noLastUsed` is unexported).

**Fix:** add them to `docs/open-threads.md` under a new § Security and
sequences: what is left. The Database Role rename is a real `ui-rules` miss
and small enough to fix outright, with a row in that test.

---

## Recommended implementation order

Cheapest and most certain first, then the items that need a live server,
then the questions. Each step builds, tests (`-race`) and is driven before
the next starts.

1. ~~**D1 + D2**: doc corrections, plus the Database Role rename reload from
   D2 with its test row. Mechanical, and it stops the leftovers being lost
   a second time.~~ **Done 2026-11-04.** D1 narrowed in `docs/decisions.md`;
   D2's other leftovers are `docs/open-threads.md` § Security and sequences:
   what is left. Server Role Properties shared the rename miss (same
   `roleGeneralPage` apply) and got the same fix; both role dialogs and
   Application Role now have rows in the reload test. Driven on 17: the
   pre-fix binary kept the old label, the fixed one relabels both folders.
2. ~~**B2**: `clip`'s boundary and the singular. One function, a table test
   and a `docs/cli.md` sentence.~~ **Done 2026-11-04.** `clip` cuts only when
   the cut saves more characters than its marker adds (so 1–12 cut
   characters keep the cell whole), and `clipMarker` says "1 char". The
   boundary loop in `TestCells` is mutation-checked; `docs/cli.md` § Output
   limits says so. Driven: `--max-col-width 11` on `[abcdefghij]` prints it
   whole in text and jsonl; a 28-character name cuts to
   `[abcdefghij…[+17 chars]`.
3. ~~**B3**: the `NEXT VALUE FOR` floor. A table entry, test rows and a
   `docs/cli.md` row.~~ **Done 2026-11-04.** `floor` raises the three-word
   phrase (word tokens only, so `[next] value for` and a column `next` stay
   read) to write and sets a new flag, `sequence-advance`. D3's after-run
   check cannot see a sequence advance (the transaction survives it), so
   `query run`/`plan actual` instead warn after any wrapped run that rolled
   back, naming the lines. `docs/cli.md` § Safety's write row and
   § Transactions say so; the agent-schema golden lists the flag. Classifier
   rows and `TestQueryRunWarnsSequenceRollback` are mutation-checked. Driven
   on 17: `--allow read` is BLOCKED; two `--transaction rollback` runs of
   `SELECT NEXT VALUE FOR` on a throwaway sequence returned 1 then 2, each
   with the warning.
4. ~~**G3**: the audit line's target. One change in `logCall` and four
   tests.~~ **Done 2026-11-04.** Fixed in `cmd`, not `logCall` (which has no
   config to read a profile from): `Deps.confirmRefusal` notes the target
   when a pre-gate `--confirm` check returns BLOCKED, at all four sites, and
   `data import --truncate-first`'s refusal logs class `ddl` as the gate
   would. `TestAuditConfirmRefusalNamesTarget` covers the four commands and
   is mutation-checked (no target; class `write`). The open-thread item is
   closed. Driven: each refusal in the built binary writes its `server`,
   `database` and `login`.
5. ~~**G1**: application roles as principals. One lookup and a `Catalog`
   method; drive it on 17.~~ **Done 2026-11-04.** `resolvePrincipal` tries
   `Catalog.ApplicationRoleByName` after the database role, as a role (so
   `--effective` is exit 2, its hint naming no member to ask for instead),
   and the server-scope NOT_FOUND hint names an application role of the
   database too. Help, `docs/cli.md` and the agent-schema golden say so; the
   open-thread item is closed. Tests cover `permissions` (explicit,
   `--effective`, the hint), `grant` and `role add-member`, and are
   mutation-checked (both lookups). Driven on 17 against a throwaway
   database: grant, deny, revoke, add-member and remove-member on an
   application role, `permissions` listing its entries and `memberOf`.
6. ~~**G2**: hidden index filters in `object describe`/`object indexes`;
   drive it on 17 as a login without VIEW DEFINITION.~~ **Done 2026-11-04.**
   The cell was already NULL (`nonEmpty`), so the fix is the warning:
   `hiddenFilters` names each index whose `IsFilterHidden` is set, in
   `describe` (table and view) and `object indexes`. `object stats` had the
   same defect, worse — a hidden filter printed as `""` — and now reads NULL
   with the same warning (gosmo's `ISNULL` leaves `HasFilter` with an empty
   `FilterDef`, which no real filter has). `docs/cli.md`'s three rows say so;
   the open-thread item is closed. The fake's `hidden` mode covers all three
   commands, mutation-checked. Driven on 17 as a login with SELECT only:
   `IX_t_v` and `st_v` read NULL, each named in a warning; as sa the
   predicates print and no warning is given.
7. **B1**: palette roles, the contrast test, the status-style capture. Do
   the test first so it fails on today's palettes, then fix until it passes;
   drive all four themes in tmux.
8. **B4** (gosmo first): the single-prefix entry error, then gossms's test
   text. It needs a live check that a 916 still reads well in `db info`.
9. **V1** (gosmo): the six reads into the sweep, run on 13.
10. **G4** (gosmo): test hygiene on 13 and 14. It can share a live session
    with step 9.
11. **S1**: deep-cloned settings snapshot plus the in-place regression test.
12. **Q2/A2**: only on the author's say-so; gosmo first, then
    `internal/query/cellparse.go`.
13. **Q1/A1**: only on the author's say-so; pilot on the `object` group.
14. **S2, S3**: only alongside another change in those files. Otherwise S2
    goes to `docs/decisions.md` as reviewed and left.

When done: delete this file. Move anything unfinished to
`docs/open-threads.md` or `~/go/gosmo/OPEN-THREADS.md`.
