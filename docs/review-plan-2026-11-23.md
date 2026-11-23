# Review plan, 2026-11-23

A review of gossms, `~/go/gosmo` and `~/go/tuikit` after the 2026-11-22
fourth pass closed, covering what landed since: the hide-Object-Explorer
feature (`0974549`, with tuikit's `TreeView.SetCloseButton`), the securables
matrix refactor (`f336265`), gosmo's `BackupRefusal` collation fix and
catalog-types work, and tuikit's `SetSourceReordered` speed-up. Nothing is
fixed yet. Close an item by deleting it; delete this file with the last one.
gosmo and tuikit may break back-compat where an item says so (CHANGELOG
entry when the user asks for one).

**Method.** `go vet`, `staticcheck` (default gate and `-checks all` without
ST1000/3/20-22), `deadcode -test`, `govulncheck`, `go list -m -u` and
`go test ./...` in all three repos; `dupl -t 120` over all three; a ranking
of every non-test function by length; a read of the commits since
2026-11-22 in each repo; and greps across gossms for the rules CLAUDE.md
and `docs/db-rules.md` state (bare goroutines, `context.Background()`,
version comparisons, case-blind name comparisons). No server was contacted
and the binary was not driven: every fix still needs the `docs/testing.md`
drive.

**What came out clean.** Build, vet, both staticcheck gates, govulncheck and
every test in all three repos. No direct dependency has a newer version.
`deadcode` lists gosmo's `QSStatistics` and 28 tuikit exports with no
in-repo caller — library surface, kept deliberately (CLAUDE.md). `dupl -t
120` re-finds only pairs `docs/decisions.md` § Code and dependency hygiene
already settles. No file is over 900 non-test lines in any repo. The one
bare `go` in `internal/tui` (`app_saves.go`) and every `context.Background()`
root carry their documented reason. The securables refactor is behaviour-
preserving (the Properties target still dereferences the loaded principal at
apply time). Format Document is guarded at run time by `verify`, so no probe
was spent there.

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W3 — B3 + A2**: version gates through gosmo's `ServerInfo.AtLeast`, the
  Details pane's tempdb rows on Managed Instance with them, and a gossms test
  that keeps hand comparisons out.
- **W4 — A3** (tuikit): one title-row layout for `TreeView`, ending the two
  rules for "title vs. buttons"; split `TreeView.HandleMouse` by zone in the
  same change (the open-thread's "when a change lands there" has arrived).
- **W5 — B4 + A4**: name comparisons under the scope's collation — the
  CLI's `--on DATABASE::`, the AG membership lookup and `--schema` — and the
  rule written down in `docs/db-rules.md`.
- **W6 — A5**: split `analyzeCompletionContext` into phases, as W3 of the
  last plan did for `baseCompletionCandidates`.
- **W7 — A6** (tuikit): `DataGrid.SetSourceReordered` reads a nil `newPerm`
  as the identity, as it already reads a nil `oldPerm`.
- **W8 — N1**: nits.
- **W9 — R1**: release notes for gossms, gosmo and tuikit (only if the user
  asks: CLAUDE.md keeps CHANGELOG/RELEASE edits out of fixes).
- **W10**: move what stays undone to `docs/open-threads.md`, then delete
  this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Bugs

### B3 — the Details pane drops Resource Governor's tempdb rows on Managed Instance

**Where:** `internal/tui/detail_browser_resource_governor.go` (the
`serverMajor(sc) >= int(gosmo.SQLServer2025)` test).

**What:** `serverMajor` returns 0 for every Azure edition so that callers
can treat 0 as "newest" — and every other gate in gossms writes
`major == 0 || major >= …`. This one doesn't, so on a Managed Instance
(VersionMajor 12, engine 18) with both limits NULL the two "Max tempdb data"
rows vanish, while Resource Governor Properties' Workload Groups page
(`resource_governor_props_groups.go`, `tempdb := major == 0 || …`) shows the
fields. It is exactly the bug class gosmo's `majorAtLeast` comment records
(the 2026-10-21 AG-lag blanking on MI) and `TestVersionGatesGoThroughMajorAtLeast`
forbids there; gossms has no such guard. MI is unavailable (memory), so
this ships offline-tested only; record it under open-threads V4 for the MI
run.

**Fix:** with A2.

### B4 — CLI and AG name checks compare SQL names case-blind

**Where:** `internal/cli/cmd/security_permissions.go` `resolveSecurable`
(`--on DATABASE::name` vs. the connection's database);
`internal/cli/cmd/db_create.go` (the AG that already holds a database);
`internal/cli/cmd/db_table_sizes.go` (`--schema` filter).

**What:** all three use `strings.EqualFold` where gossms elsewhere compares
names with `gosmo.SameName` under the scope's collation (`name_set.go`,
`pending_edits.go`, completion). On a case-sensitive server,
`security grant … --on DATABASE::sales` while connected to `Sales` passes the
check and the GRANT (emitted with no database name) lands on `Sales` — the
wrong database. The AG lookup can name the wrong group for `sales` vs.
`Sales`; `--schema sales` lists `Sales` too. Database and AG names compare
under the server collation, schema names under the database's. CS-only, and
no CS server exists here (open-threads § Environment), so low — but the first
is a write to the wrong place.

**Fix:** `gosmo.SameName(info.Collation, …)` for the database and AG names,
the database's collation for the schema (what `connect`'s database already
carries; check `gosmo.Database.CatalogCollation`/`Collation` and use the one
`databaseCollation` in the TUI uses). Offline tests with a CS collation, as
`TestDatabaseBackupRefusal`'s CS cases. Add the three to the open-threads CS
list. The rest of the 124 `EqualFold`s were read: system type names, XE
object names, DNS and replica host names and profile names are
case-insensitive by definition and stay.

---

## Architecture and refactoring

### A2 — version gates through `ServerInfo.AtLeast`, enforced by a test

**Where:** `internal/tui/prop_grid_helpers.go` `serverMajor` and its
callers: `database_props_query_store.go`, `new_column_master_key_dialog.go`
(calls `serverMajor` twice), `explorer_objects.go`, `explorer_external.go`,
`new_ag_pages.go`, `resource_governor_props_groups.go`,
`detail_browser_resource_governor.go`.

**What:** gosmo exports `ServerInfo.AtLeast(v)` precisely so a caller does
not compare `VersionMajor` by hand ("comparing VersionMajor by hand is how a
gate ends up blanking a Managed Instance", `version_gate.go`). gossms instead
re-derives the rule nine times as `major == 0 || major >= int(gosmo.X)`, and
B3 is the copy that dropped the `== 0`.

**Fix:** `sc.Server.Info().AtLeast(gosmo.SQLServer2019)` (nil-safe: a nil
`*ServerInfo` counts as newest) at each gate; a small
`serverAtLeast(sc, v)` wrapper if the nil `sc`/`Server` guard is wanted.
`serverMajor` stays only where an int is genuinely consumed
(`compatItemsFor`, `agClusterTypeChoosable`, the XEvent dialogs' `major`,
`newAGPrefetch.major`) — and each of those consumers should take
`*gosmo.ServerInfo` or a bool if all it does is compare. Add
`TestVersionGatesGoThroughAtLeast` in `internal/tui` mirroring gosmo's
(go/ast over non-test files: no `>=`/`<` against `int(gosmo.SQLServer…)`
outside an allow-list). `xevent_session_advanced.go`'s
`major >= 17 && !azure` is the deliberate "known on-prem" form (V3) — allow-
list it with the reason.

### A3 — tuikit `TreeView` title row: one layout, one rule; split `HandleMouse`

**Where:** `~/go/tuikit/controls/treeview.go` (`titleButtonRect`,
`closeButtonRect`, `Draw`, `DrawOverlay`, `HandleMouse`).

**What:** the close button brought a second title-row layout. Without it,
`titleButtonRect` hides the title button when the centred title would reach
it ("hides rather than truncate the title", `SetTitleButton`'s doc). With it,
the title button always shows and `Draw` truncates the title, re-implementing
`core.DrawBoxTitle`'s centring by hand (no ellipsis). So adding `[x]` flips
which of title and button wins at narrow widths, and `SetTitleButton`'s doc
is wrong whenever a close button is set. `HandleMouse` grew from 147 to 154
lines, the longest function in tuikit; `docs/open-threads.md` § tuikit asked
for the split "when a change lands there anyway" — this is it.

**Fix (author's call on the rule):** one `titleLayout()` returning the title
span and both button rects, used by `Draw`, `DrawOverlay` and `HandleMouse`.
Recommended rule: the buttons always win, the title truncates with `…` (the
explorer's "Object Explorer" title matters less than its actions), and
`SetTitleButton`'s doc says so; the alternative is to hide the title button
first, then the close button, as the old rule did. Then split `HandleMouse`
by zone (scrollbars, title row, row hit) as `PanelManager.HandleMouse` was,
and delete the open-threads item. Tests: the existing title-button and
close-button tests at widths where title and buttons collide. Drive every
tree after (OE, dialogs' trees), as the open-thread asks.

### A4 — write down the name-comparison rule

**Where:** `docs/db-rules.md`.

**What:** the rule B4 breaks lives only in code (`name_set.go`'s comment,
`ARCHITECTURE.md`'s package-map row for it). A contributor reaches for
`strings.EqualFold` — 124 uses in gossms, most legitimately — and nothing
says which names need the collation.

**Fix:** a `docs/db-rules.md` bullet: compare SQL Server object, schema,
database and principal names with `gosmo.SameName` (or `nameSet`/`nameMap`)
under the scope's collation — server for databases, logins, AGs; the
database's for what lives in it; msdb's for Agent; `EqualFold` only for
names that are case-insensitive by definition (system type names, XEvent
package/object names, host and DNS names, gossms profile names).

### A5 — split `analyzeCompletionContext`

**Where:** `internal/tui/completion_expressions.go`
`QueryPanel.analyzeCompletionContext` (234 lines, the longest function in
the completion code).

**What:** one function does five things in sequence: the DDL/data-type early
outs, the parenthesis frame walk (with aggregate/analytic/window flags per
frame), the parenthesized-position classification (JSON path, DATEPART,
scalar), the CASE-arm and clause scan over the innermost frame, and the
final per-clause scalar/aggregate decision. Recent fixes (#57, the
user-type work) each landed in a different phase; the function's opening
comment describes none of them. It also calls `p.ddlTypes()` twice per
keystroke (cheap — no win on its own).

**Fix:** as the last plan's W3 did for `baseCompletionCandidates`:
`completionFrames(buf, tokens, start, from) []completionFrame`,
`parenthesizedPosition(…)`, `clauseScan(f) (clause, start, cases…)`,
`classifyPosition(…)`, each with a doc comment naming what it decides; one
`ddlTypes()` call. Pure moves, so the 1,754-line keyword test and the
expression tests are the safety net; drive the same completion positions as
W3 against the pre-split binary and compare popups.

### A6 — tuikit `SetSourceReordered`: a nil `newPerm` means "every row is gone"

**Where:** `~/go/tuikit/controls/datagrid_sort.go` `SetSourceReordered`.

**What:** a nil `oldPerm` is documented as the identity, but a nil `newPerm`
silently means the opposite: `slices.Index(nil, orig)` and an empty `at`
drop the cursor's row and every mark. Both gossms callers
(`detail_browser_sort.go`, `query_panel_sort.go`) build an explicit identity
today, so nothing is broken, but a host that clears its sort by passing nil
(the natural spelling, and the one `oldPerm` invites) loses the selection.
tuikit has users besides gossms (CLAUDE.md).

**Fix:** treat nil `newPerm` as the identity over `rows.Len()` (build `at`
from it, or short-circuit `pos` to `r < rows.Len()`), say so in the doc
comment, and add the nil case to `datagrid_sort_test.go`. The bench should
not move.

---

## Nits

### N1 — small ones

- `internal/tui/securables_matrix.go`: the column grid's height is still a
  bare `8` beside the two new named constants; name it
  (`securablesColumnsHeight`) and drop "the column grid below is a fixed 8"
  from their comment.
- `internal/tui/completion_expressions.go` `completionEndsSource` builds an
  eleven-element slice on every call inside a token loop; a package-level
  set (or a `switch`) does the same with no allocation.
- `internal/tui/help_dialog.go`: the new "[x] Hide pane" line is the only
  F1 entry describing a mouse target rather than a key; phrase it as the
  others ("View > Object Explorer  Show/focus; [x] on its border hides it").
- Long property-page builders (`pageRGGroups` 324 lines, `pageMailProfiles`
  264, `pageMailAccounts` 218, `buildSymmetricKeyEncryptionForm` 218,
  `pageLoginUserMapping` 217): not worth a change on their own; add one
  `docs/open-threads.md` line so the next change in any of them splits it
  (grid, detail form, apply), as CLAUDE.md does for 900-line files.

---

## Release

### R1 — three repos carry unreleased work with no release notes

gossms is 110 commits past `v0.2.1` and `CHANGELOG.md`'s newest section is
0.2.1 (the command line, completion, safety, hide-explorer work among them);
tuikit is past `v0.0.3` with no `CHANGELOG.md` at all, though CLAUDE.md says
it is released like gosmo, and its new API (`SetCloseButton`,
`Splitter.SetCollapsed` from A1, plus A3/A6 if taken) needs an entry; gosmo is past `v0.3.0`. Unchanged from the last
plan's R1 except for the counts. Only on request (CLAUDE.md § Repo hygiene).
