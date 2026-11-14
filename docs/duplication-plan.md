# Duplication review and fix plan

Reviewed 2026-11-14: gossms (145k non-test lines), gosmo (70k), tuikit (24k).
Method: `dupl -t 90` over non-test files of each repo, then reading every hit,
plus a name sweep for same-named helpers across packages and repos.

**Verdict: low duplication.** Few clones, mostly 10–20 lines. Nothing here is a
bug source today; the value is in consolidating the few patterns that grow with
every new feature (new object dialogs, explorer folders, gosmo `Create*`).
Settled items in `docs/decisions.md` are **not** re-raised (see § Not
duplication).

Backwards compatibility in gosmo and tuikit may be broken where noted. Per
CLAUDE.md, no gosmo/tuikit capability is removed merely because gossms does not
call it; every change below *consolidates* and keeps the capability.

## Findings

### A. gossms — worth fixing

| # | Where | What | Fix |
|---|---|---|---|
| A1 | `tui/log_viewer.go` / `tui/xevent_viewer.go` (+ `_draw`, `_input`, `_rows`) | Two viewer panels share a toolbar-over-splitter shell: identical `SetBounds` (toolRect + splitter + `layoutTools` + `layoutChildren`), same method set (all but `filterVisible`), `rect/toolRect/splitter/active` fields. | Extract a small embedded `viewerPanel` (rect, toolRect, active, splitter, `SetBounds`, `Bounds`, focus plumbing). Verify the rest by diffing method bodies first; stop at the shell if the content diverges. |
| A2 | `tui/explorer_resource_governor.go:391`, `explorer_database_mail.go:87`, `app_explorer_data.go:102` | `refresh…Label`: find node → `safego` → timeout ctx → fetch state → `postAndWake` { retired check, `applyTo`, `rebuild`, `Retitle` } copied per folder. | One `refreshStateLabel(sc, nodeOf, fetch func(ctx, sc) stateApplier)` helper; the three become 3-line callers. Obey `ARCHITECTURE.md` § postAndWake. |
| A3 | `tui/explorer_resource_governor.go:121` / `:177` | `loadResourcePoolsChildren` ≡ `loadExternalResourcePoolsChildren` except the fetch and node kind. | `loadPoolChildren(l, fetch, kind)` generic over the pool type (both expose `Name`/`IsSystem()`). |
| A4 | `tui/xevent_session_events.go:415`, `xevent_session_storage.go:356`, and `:528` / `:399` | Same column-fetch callback (cache by lowercased key, re-show if still current) in the Events and Storage pages. | Method on the shared `h` helper (`h.columns` already exists): pass a `show func(cols)` and the hint widget. |
| A5 | `tui/new_sequence_dialog.go:143`, `new_synonym_dialog.go:29` (and likely other `schemaObjectPrefetch` dialogs) | Whole constructor identical except title/noun/accessor. | `newSchemaObjectDialog` builder taking title, noun, and the `schemaObjectKeys` accessor; grep `schemaObjectKeys(` for the rest. |
| A6 | `tui/attach_database_dialog.go:51`, `new_snapshot_dialog.go:60` | Same `newObjectConfig` literal incl. `refresh: ReloadFolders(Databases)` and the Abandon-then-`onClose` hook. | `refreshDatabasesFolder` named func (the shared `refresh`); leave the rest, it is configuration. |
| A7 | `tui/role_props.go:222` / `server_role_props.go:202`; `fulltext_props.go:413` / `:650`; `rule_default_props.go:34/53`; `detail_browser_external.go:29/44`; `detail_browser_fulltext.go:63/95`; `key_signatures_page.go:98/111`; `explorer_security.go:149/170` | Paired near-twins: one spec/page builder per object kind differing in noun, accessor and gate. | Per pair, parameterise only where the pair has ≥3 callers or the twin already diverged once. Role owner-transfer: shared `roleOwnerTransferSpec(kind string, …)`. Full-text lists: `fullTextListPropPages(kind, find, page, gate)`. Skip a pair when the helper needs more than ~4 params (decisions.md § user-mapping precedent). |
| A8 | `tui/explorer_objects.go:565` / `:655` | View and stored-procedure menus differ in one item. | Keep unless a third kind lands (`viewMenuItems` etc. already take `newQuery`/`refresh`). **Skip.** |
| A9 | `tui/dashboard/history.go:75` / `tempdb.go:59` | Three-column chart section, same series literals. | `chartTriple(s, body, hits, v View, []panel{title, series, stacked})` using a shared `View` header/interval. Small. |
| A10 | `allDigits` ×3 (`tui/query_panel_sort.go:390`, `fileutil/atomic.go:220`, `query/cells.go:165`); `oneLine` ×3 (`planview_draw.go:180`, `cli/output/text.go:315`, and inline `tui/log_viewer_rows.go:174`); `nullable` ×2 (`cli/output/json.go:201`, `cli/agent/schema.go:197`); `yesNo` ×2 (`tui/backup_common.go:234`, `showplan/compare.go:256`); `truncateForError` (`query/cellparse.go:222` ≈ `gosmo/bulkvalue.go:739`) | Tiny helpers re-written per package. `oneLine` variants differ subtly (tab/NEL/` ` handled by only one). | Add `internal/textutil` (stdlib-only leaf, like `sqltext`) with `OneLine` (superset of the three behaviours), `AllDigits`, `Nullable`. `yesNo` only if a third caller shows up. `truncateForError`: leave (cross-module; 6 lines). Decide the `oneLine` semantics deliberately and test them. |
| A11 | `cli/cmd/db_files.go:388` vs `tui/database_props_files.go:213` (`maxSizeText`) | Same concept, different shapes (unit-aware vs KB). | Move the KB→text core to gosmo (`DatabaseFileInfo.MaxSizeText`?) **only if** the formatting is identical; otherwise skip. |

### B. gosmo — worth fixing

| # | Where | What | Fix |
|---|---|---|---|
| B1 | 35 files, 47 `createdObject(` call sites; clones at `asymmetric_key.go:207`/`certificate.go:249`, `audit.go:302`/`audit_specification.go:202`, `availability_group.go:138`/`service_broker_queue.go:149`, `external_resource.go:197`/`user_defined_type.go:88` | Every `Create*` repeats: build statement → wrap error → exec → wrap error → `createdObject(Ref, ByName)`. | A generic `createAndRead[T](ctx, "asymmetric key", name, stmt, exec func, ref T, byName func)`; error wording stays identical (`TestCreate*` / golden messages pin it). Do it **only** for the plain-exec cases, not password-redacting `execPasswords` paths unless parameterised by the exec closure. Public API unchanged. |
| B2 | `search.go:28` / `:65` | Same row scanner for `SearchResult`. | `scanSearchResult` func var shared by both. |
| B3 | `resource_governor.go:358/528`, `service_broker.go:368/558`, `server_activity.go:182` / `server_config.go:423` | Struct-scan boilerplate around `scanRows`. | **Skip** — each lists distinct columns; a reflection-based scanner would cost safety. Report as "intentional". |
| B4 | `symmetric_key.go:675/693`, `database_mail_service.go:212/231`, `fulltext_write.go:195/322`, `scripter_module_clr.go:356/382`, `index_management.go:479` / `statistics.go:222` | Add/Drop twins differing in a verb. | Verb-parameterised private helper per pair (`alterEncryption(ctx, "ADD"\|"DROP", …)`; the statement builder already takes the verb in `symmetric_key.go`). Low risk, low value; batch with B1. |
| B5 | `helpers.go:183` `quoteIdent` | One-line alias for `QuoteName`. | Delete the alias and call `QuoteName` (private; mechanical `sed`). |
| B6 | Test helpers: `liveDB`/`liveConn`/`liveExec` redefined in `live_execproc_script_test.go`, `live_monitor_reads_test.go`, `live_login_refusal_test.go`, `live_table_kinds_test.go`, `live_add_schedule_test.go`, `live_initial_database_test.go` with different signatures (3 declare ≥1 each, `liveDB` returns different tuples). `gosmotest` already exists. | ~180 live test files, each with a private connection helper. | One `live_helpers_test.go` in package gosmo (`liveDB(t)`, `liveConn(t, db, ctx)`, `liveExec`), signature normalised; migrate files with `gofmt -r`/sed. Keep behaviour: skip when no `GOSMO_TEST_DSN`. Stage per file, run `go vet` between. |

### C. tuikit — worth fixing

| # | Where | What | Fix |
|---|---|---|---|
| C1 | `core/drawing.go:412` / `:428` | `HandleScrollbarDrag` / `…H` identical but axis. | Private `handleScrollbarDragAxis(ev, along, across, …)`; both exported funcs stay as wrappers (no break). |
| C2 | `theme/palette.go:156` vs `:417` (Default vs Light) | Same field list, different RGBs. | **Skip.** Palette literals are data; a builder would only obscure the table. |
| C3 | `theme/palette.go:262` / `:284` | Two near-identical 18-line literals. | Read: if one is Default-derived, express as `p := Default; p.X = …`; otherwise skip. |
| C4 | `controls/frame_bench_test.go:24` / `controls/editor/editor_grapheme_test.go:20` (`mockScreen`), `tuikit/widgets/dropdown_test.go` | Test screens re-declared. | Move into `tuikittest` (already exists) as `NewMockScreen(tb, w, h)`; **breaking** only for test-only code. |
| C5 | `gossms/tui/sqledit/statement_test.go:11` vs `tuikit/controls/editor/editor_test.go:14` (`newTestEditor`) | Same helper in two repos. | Export from `tuikit/tuikittest` (`NewEditor(text)`), gossms test imports it. |

### Not duplication (do not re-raise)

- `gosmo/quoting.go` forwarding to `gosmo/tsql` — intentional so `tsql` stays a
  stdlib-only leaf (`tsql/doc.go`, `TestImportsOnlyStdlib`).
- `sqltext` lexer vs `tsql.Spans`: decisions.md § "T-SQL lexing: one lexer, two
  statement splitters" — settled.
- Two search dialogs' key-routing, and the two user-mapping page builders:
  decisions.md already declines both.
- `config.ResolveServer` vs `gosmo.ParseServerAddress`: documented as the same
  fold on purpose.
- The `*_test.go` repetition inside a single package — table-driven consolidation
  was not found to be warranted.

## Recommended implementation order

Lowest risk and widest benefit first; each step is independently shippable and
ends with `gofmt`, `go vet`, `staticcheck`, `deadcode -test`, then tests in the
touched module (docs/testing.md), then driving the built binary for UI steps.

1. ✅ **B5** — delete `quoteIdent` alias (gosmo, mechanical). *Done
   2026-11-14: 630 call sites (110 files, incl. comments and
   `diagram/07-connection-internals.mmd`) now call `QuoteName`.*
2. ✅ **B2, C1** — one-function dedups in gosmo/tuikit; no API change. *Done
   2026-11-14: gosmo `scanSearchResult` shared by `Search` and
   `SearchDefinitions`; tuikit `handleScrollbarDragAxis` behind both
   exported `HandleScrollbarDrag`/`…H` wrappers.*
3. ✅ **A10** — `internal/textutil` (`OneLine`, `AllDigits`, `Nullable`). Decide
   `oneLine` semantics first; add tests pinning tab/NEL/` ` handling. *Done
   2026-11-14: two semantics kept on purpose — `OneLine` (each of `Breaks`,
   CR/LF/VT/FF/NEL/LS/PS/tab, → one space, nothing else touched; the CLI text
   renderer, where a value's spacing is data) and `Collapse` (whitespace runs
   → one space, trimmed; planview statement line, Compare Showplan pickers,
   `flattenLogText`, which now also flattens on NEL/LS/PS/VT/FF). `AllDigits`
   is generic over string/[]byte and false for "" (the decimal sort-key
   caller adjusted); `Nullable` serves cli/output and cli/agent. Package-map
   row added.*
4. ✅ **C4, C5, B6** — test-helper consolidation (`tuikittest`, gosmo
   `live_helpers_test.go`). Tests-only, so zero production risk; do tuikit
   first because gossms tests depend on it. *Done 2026-11-14: **C4** —
   `tuikittest.NewMockScreen(tb, w, h)` (tcell's screen on a `vt` mock
   terminal) replaces `mockScreen` ×2, `dropTestScreen` and `nullTestScreen`
   (17 call sites); `core/imports_test.go` gains `testKitExternal` so only
   tuikittest may import `tcell/v3/vt`; README/ARCHITECTURE/doc.go updated.
   **C5 skipped** — `controls/editor`'s in-package tests import tuikittest,
   so tuikittest importing editor is a cycle; a 4-line helper is not worth a
   new package. **B6** — the premise was off: gosmo's live helpers were
   already defined once each (one package), only scattered. Now
   `live_helpers_test.go` holds `liveDSN`, `liveDB`, `liveDBTimeout`,
   `skipSlowInShort`, `liveRestrictedDSN`, `liveSecondDB` and a new
   `liveOpenAs(t, login, pass)`, which replaces 34 inline "open a pool as
   another login" copies across 30 files. Callers still close the pool
   themselves (a `defer DROP LOGIN` needs the pool closed first). The
   `*liveDSN` second pools in restore/contention tests keep their own
   `defer Close` for the same ordering reason. Verified with the 69 live tests
   in the touched files against win10cli (`-short`): all pass or skip.*
5. ✅ **B1 (+B4)** — gosmo generic `Create*` helper. Land it on two types
   (certificate, asymmetric key), confirm error text is byte-identical via the
   existing tests, then roll out file by file. Tag/release gosmo (or keep the
   `replace` active) before gossms steps depend on it. *Done 2026-11-14,
   narrower than planned: the duplicated part was the read-back tail, not the
   exec. gosmo `createdByName(ctx, ref, byName, name)` (`helpers.go`) replaces
   the `createdObject(ctx, XRef(n), func(){ return XByName(ctx, n) })` closure
   at 29 sites in 24 files, and `createOwned` uses it. Exec + error wrap stay
   at each call site: six exec forms (`exec`, `execPasswords`, `execSecret`,
   `execAtomic`, `execSecretAtomic`, `Table.exec`) and ~6 error shapes
   (`in %q`, `qualifiedName` `%s`, class suffix, `on %s`) meant a helper
   folding them would take the error text pre-formatted and save nothing — and
   leaving them put keeps the messages byte-identical by construction. Sites
   keyed by more than a name (schema, class, schedule id, AG/snapshot/job-step
   literals, full-text index, event session, pool kind) keep `createdObject`
   with their closure. **B4 skipped**: every pair differs in statement or
   procedure *and* error wording, and the one pair sharing a statement
   (`SymmetricKey` Add/DropEncryption) already has its verb-taking builder.
   No API change, so no gossms change or gosmo tag needed. Verified: gosmo
   build/vet (incl. `-tags livedb`)/staticcheck/`go test ./...` clean; 238
   live Create/Certificate/Key/Credential/Audit/… tests pass on win10cli
   (`-short`).*
6. **A2, A3, A4, A6** — gossms explorer/XEvent helpers (same area, same review).
7. **A5, A7** — dialog/prop-page builders, pair by pair, skipping any pair whose
   helper would exceed ~4 parameters.
8. **A9, A11, C3** — small, optional; do only if touching those files anyway.
9. **A1** — LogViewer/XEventViewer shell. Last: highest churn in event/mouse
   code (`ARCHITECTURE.md` § mouseDragging); verify by driving both panels
   (resize, splitter drag, toolbar clicks) per docs/testing.md, with
   `go test -race` on `internal/tui`.
10. Update `docs/open-threads.md` with anything left deliberately skipped
    (A8, B3, C2) so a later review does not re-flag it; add a line to
    `ARCHITECTURE.md` § Package map for `internal/textutil` (and any new
    `internal/tui` file, `TestPackageMapListsEveryTUIFile`).

Cross-cutting rules: don't commit (user commits); no `CHANGELOG.md` edits; keep
long "why" comments when moving code; check target paths exist before writing.
