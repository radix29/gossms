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

## Architecture and refactoring

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
`Splitter.SetCollapsed` from A1, A3's title-row rule (the buttons win, the title truncates with `…`), plus A6 if taken) needs an entry; gosmo is past `v0.3.0`. Unchanged from the last
plan's R1 except for the counts. Only on request (CLAUDE.md § Repo hygiene).
