# Comment drift: findings and fix plan

Survey date: 2026-08-29. Scope: all 605 `.go` files (141,659 lines; 26,989
comment lines). Method: mechanical detectors across the whole tree — stale
doc-comment names, comment references to identifiers/files that no longer
exist, doc-vs-signature mismatches, numeric claims vs nearby code, file-header
content vs declarations, `doc.go` file lists vs directory contents — then
hand-verification of every hit against the code, including cross-checks into
`~/go/gosmo`.

Status: **Phases 1-4 done** (items 1-9), 2026-08-29. **Phase 5 in progress** —
the semantic pass caveat 1 always named as the open remainder. Every edit was
comment-only; `gofmt`, `go build ./...`, `go vet ./...` and `go test ./...` are
green across the tree. Written up in `docs/journal.md`
(§ 2026-08-29 — the comment-drift survey, worked through).

## What was checked and found clean

Worth recording so a resumed session doesn't re-run these:

- Zero TODO / FIXME / XXX / HACK markers in the tree.
- No first-person or editorial comments (`// I `, `// We `, `// Obviously`, …).
- No bug-narrative prose in non-test code.
- Numeric claims sampled all held: `amTabCount = 5` vs "five tabs";
  `queryStoreQueryColumns` 8 entries vs "eight columns"; `qsPlanColumns` 10 vs
  "ten"; `queryStoreReports` 7 entries vs "seven reports"; 5 menu selectors +
  Refresh vs "the five selectors and Refresh".
- gosmo cross-references all resolve: `Server.EventAlerts`,
  `Certificate.Encoded`, `CertificateSpec.FromBinary`, `Index.SetLockOptions`,
  `Index.SetIncludedColumns`, `Table.SpaceUsed`, `ServerPermissionEntry`,
  `DatabasePermissionEntry`.
- `internal/tuikit/widgets/doc.go` and `internal/tuikit/propsheet/doc.go` file
  lists are accurate.
- `xmlOpenBlock`'s "the only caller is TestXMLPrefixStatesMatchFullReplayAcrossEdits"
  is still true (one call site, `xml_highlighter_test.go:249`).

## Verbosity baseline

Non-test code is 22.1% comments (17,609 of 79,729 lines), across 41 blocks of
>=18 consecutive comment lines. Most are the load-bearing "why" comments
`CLAUDE.md` protects and are **not** a cleanup target. Only the two named in
Phase 4 carried material that `CLAUDE.md` says belongs in `docs/journal.md`.

---

# Plan

Ordered by risk of misleading the next reader. Comment-only throughout, so no
behavior change. Verify with `go build -o gossms ./cmd/gossms`,
`go test ./...`, `go vet ./...`, `gofmt -w .`.

## Phase 1 — dead pointers (send a reader to a file that doesn't exist) — DONE

### 1. `internal/tuikit/controls/sql_statement.go` — 4 stale references — DONE
The tui-side analogue moved to `internal/tui/sqlparse/` and was exported.

- `:26`, `:36`, `:97` — referenced `dmlStatementStarts` in
  `internal/tui/completion_provider.go`. Now `DMLStatementStarts` in
  `internal/tui/sqlparse/scope.go:123`. (Five sites, not four: `:97` is a
  line-comment inside `sqlStatementAt` the survey folded into this item.)
- `:57` (`dmlStatementLeaders` doc) — "mirrors
  internal/tui/completion_provider.go's map of the same name". The map is at
  `internal/tui/sqlparse/scope.go:118`.
- `:69` — cited `completion_tokenizer.go`'s `sqlKeywords` table. **No such
  file.** The tokenizer is `internal/tui/sqlparse/token.go`.

**Not a plain path swap.** The `:69` comment drew a contrast between this
file's small keyword set and "completion_tokenizer.go's much larger
`sqlKeywords` table, which also drives clause detection and FROM-scope
parsing". The contrast held, against a differently named table: the package's
keyword table is `sqlKeywordList` (`internal/tui/sqlparse/token.go:520`), whose
own doc says it exists "for clause detection, FROM-scope parsing, and deciding
when a candidate name needs bracket-quoting". The comment now names that, and
the paragraph was rewrapped.

### 2. `internal/tui/planview/graph_layout.go:39` — DONE
"children extending rightward, matching real SSMS (exec.png)". No `exec.png`
anywhere in the tree — `todo/mockups/` holds only `.sql` files. Parenthetical
deleted.

## Phase 2 — factually wrong claims — DONE

### 3. `internal/tui/dialog_common.go:5-14` — header wrong on three counts — DONE
- Named "Connect, Backup, Restore, Tasks and Query List" as the flat-`focusable`
  dialogs. `tasks_dialog.go` and `query_list_dialog.go` never reference
  `focusable`; `find_replace_dialog.go` and `log_search_dialog.go` do and went
  unnamed. Correct list: Connect, Backup, Restore, Find/Replace, Log Search.
- "roughly forty direct `d.focusable[d.focusIdx]` accesses" — actual count is
  **9**.
- "across five files" — actual is **4** (`connect_dialog.go`,
  `backup_dialog_input.go`, `restore_dialog_input.go`, and this file).
- The count was dropped entirely rather than corrected: it re-drifts on the
  next refactor and the argument ("embedded structs owning the fields buys no
  behavioural gain") holds without a number.
- The header described only focus/scroll helpers; the file also holds
  `runProgressButton` and `confirmDiscardChanges`. The "only the operations
  over that state live here" sentence now covers them.
- One correction to the finding: `filter_dialog.go` also uses the `focusable`
  *type* (`widgetAt`), and `restore_dialog_files.go` builds its own slices, but
  neither calls the shared helpers — so the header names the five helper users.
  Tasks and Query List are not dropped silently; they use `scrollToShow` only,
  and the header now says so.

### 4. `internal/tuikit/controls/xml_highlighter.go:221` — doc contradicted signature — DONE
`xmlOpenBlock` "reports whether line idx begins already inside an unterminated
<!-- --> comment or <![CDATA[ ]]> section" — but it returns `xmlBlockState`, a
three-valued enum (`xmlNone`/`xmlComment`/`xmlCDATA`), not a bool. The yes/no
phrasing was inherited from `sql_highlighter.go:314` `startsInBlockComment`,
which genuinely does return bool.

First clause rewritten to "returns which block, if any, line idx begins
inside". The `startsInBlockComment` parallel is kept — it is accurate about
role — but now reads "the same role startsInBlockComment plays … over three
states rather than two", so it no longer implies the same return shape.

### 5. `internal/tui/query_store_reports.go:364` — inconsistent with itself — DONE
"the six ranking reports read the whole database". There are 7 reports, of which
**5** rank — `query_store_panel_draw.go:69` says so explicitly ("tallest first
for the five that rank, chronologically for Overall Resource Consumption, and
most recently executed first for Tracked Queries"). The set meant at `:364` is
the six *non-tracked* reports. Changed to "the other six reports".

## Phase 3 — stale identifiers in tests — DONE

### 6. `internal/config/config_test.go:353` — DONE
Doc opened `TestSaveCarriesFieldsItDoesNotNameExplicitly`; declaration is
`TestSaveCarriesUnnamedFields`.

### 7. `internal/tui/server_permissions_page_test.go:23` — DONE
Doc named `gridRowFor` and `permRow`. It covers two helpers, both renamed:
`permGrid` (`:27`) and `permRowIndex` (`:38`, which has no doc of its own).

## Phase 4 — trim, only where the wording is history rather than state — DONE

Both are sanctioned long comments; only the named paragraphs were trimmed.

### 8. `internal/fileutil/atomic.go:102` — 36 lines guarding a 6-line function — DONE
Cut (7 lines, now 29):
- the "The cost is narrow and accepted: a script at 0664 …" paragraph —
  rejected-alternative reasoning, belongs in `docs/journal.md`.
- "Recorded here so the next reader doesn't assume ownership is handled
  alongside the mode." — meta-narration about the comment itself.

Kept: both "load-bearing in opposite directions" halves (preserve-the-mode and
cap-at-perm, each naming a real failure), the symlink note, and the ownership
*fact* minus its narration.

### 9. `internal/tui/system_principals.go:9` — 36 lines — DONE
Cut: "Verified on win10cli (SQL Server 17.0) by attempting each one in a
throwaway database" — provenance/history.

Kept verbatim: the refusal table, "Do not 'tidy' them into the general rule",
and "Do not simplify those two predicates down to the flag" (the `public` /
`is_fixed_role` trap). These are exactly the comments `CLAUDE.md` protects.

## Phase 5 — semantic drift: prose that misdescribes the logic

Caveat 1's class: a comment that names nothing stale, so no mechanical detector
can see it, but describes behaviour the code does not have. Only a per-file
read finds these, so this phase is a sweep in batches, highest-risk packages
first — "risk" meaning a comment `CLAUDE.md` treats as load-bearing, where a
reader who believes it reintroduces a shipped bug.

Batch order (by comment volume and by how much a wrong claim costs):

1. `internal/query/executor.go`, `internal/tui/app_events.go`,
   `internal/tui/permission_gate.go`, `internal/tui/prop_grid_helpers.go`,
   `internal/tuikit/propsheet/form.go`,
   `internal/tuikit/controls/datagrid_input.go` — the idiom files, each the
   subject of a `CLAUDE.md` rule.
2. `internal/tui/query_store_panel.go`, `activity_monitor.go`, `log_viewer.go`,
   `explorer_object_ops.go`, `app_panel_actions.go`, `detail_browser.go`.
3. `internal/tuikit/controls/datagrid.go`, `editor*.go`, `treeview.go`,
   `internal/tuikit/propsheet/rows.go`, `internal/tuikit/layout/panel_manager.go`.
4. Everything else, thinner and lower-risk.

Findings and their disposition are recorded per batch below.

### Batch 1 — read, no drift found

`internal/query/executor.go`, `internal/tui/app_events.go`,
`internal/tui/permission_gate.go`, `internal/tui/prop_grid_helpers.go`,
`internal/tuikit/propsheet/form.go`,
`internal/tuikit/controls/datagrid_input.go`, read comment against code.
Every load-bearing claim checked out, including the ones a reader would act on:

- `scanNext`'s drain prohibition matches the code both ways round — the working
  tree had just inverted it to `abandoned`, and the `MsgNext` caller drains only
  on true. `streamResultSet`'s `exhausted` really is not derivable from `err`
  (the deferred `EndSet` can fail on a set read right through), and `RowSink`'s
  "EndSet is called for every set BeginSet was called for, including one whose
  own BeginSet failed" is what the defer's placement gives.
- `permission_gate.go`'s Permits/Allows and Has/Permits distinctions were
  checked against gosmo's `capabilities.go`: `Allows`/`AllowsOnSchema` do fail
  open on unknown, `Permits`/`PermitsOnSchema` add `Accessible`, and
  `HasOnObject` is `== CapabilityGranted`. Each comment names the right one.
- `redrawGrid`/`resetGrid` against `DataGrid`: `SetSource` really does clear
  `colWidthOverride` with the cursor, `SetSelectedRow` really does end in
  `ensureVisible`, and `SetDataPreservingView` restores widths, then scroll,
  then selection, in that order.
- `Form.HandleKey`/`HandleMouse`, the `sbDragging` latch ordering, `app_events`'
  F5/F10/Tab/Ctrl+0-9 routing and `routeRelease`'s broadcast all match.

One imprecision judged not worth an edit: `redrawGrid` says `SetSelectedCell`
"ends in ensureVisible", where `ensureVisibleCol` is actually last. The claim
it supports — that restoring the cursor re-scrolls vertically from zero — is
exactly right.

### Batch 2 — three fixes

`query_store_panel.go`, `activity_monitor.go`, `log_viewer.go`,
`explorer_object_ops.go`, `app_panel_actions.go`, `detail_browser.go`. Three
claims were wrong, each of the kind no mechanical detector sees — the number is
right there in the prose but the thing it counts is a table two files away.

1. **`defaultStat` is 3 Total / 4 Avg, not "two … five".** Said in both
   `query_store_reports.go:198` and `query_store_panel.go:114`. Counted off
   `queryStoreReports`: Total for Overall Resource Consumption, Top Resource
   Consuming Queries and Query Wait Statistics; Avg for the other four. The
   wording also conflated this split with the unrelated "five that rank" one in
   `query_store_panel_draw.go:69` — two of the three Total reports do not rank
   at all. Both sites now name the three by title.
2. **`qsMenuItems` said "which of six windows"; `qsWindows` has eight.**
   (5 m, 15 m, 1 h, 4 h, 12 h, 24 h, 7 d, 30 d.)
3. **`objectOpRights` said "any one of the four is enough"; `objectWriteRights`
   returns five.** Stale since `rightAlterOnObject` joined the set — and the
   comment beside it in `permission_gate.go` is what dates it, since that one
   says "the four *wider* rights all deny … rightAlterOnObject is the one that
   speaks for them". Rewritten to name both the schema- and object-scoped
   rights and to say "any one of the set", so it does not re-drift on the next
   addition.

Checked and correct: `amTabCount`/five tabs and "all eleven charts" (11
`drawChart`/`drawStackedChart` calls in `dashboard/history.go`);
`maxRowFetchConcurrency = 8`, "two loaders fan out to 16", pool
`maxOpenConns = 20`; `qsDefaultWindowIdx = 5` really is 24 h; "seven reports";
`applyResult`'s redrawGrid/SetData split; `LogViewer.Refresh` dropping rather
than refreshing the enumeration; `detail_browser.go`'s cache/pending/seq
guards.

---

# Explicitly out of scope

- The other 39 long comment blocks.
- The failure-naming comments in `query_store_panel.go`, `prop_grid_helpers.go`
  and similar ("which took Refresh off the toolbar of every pane narrower than
  150", "six sites shipped that way"). `CLAUDE.md` § Coding conventions names
  this class as earned — each stops a regression a plausible simplification
  would reintroduce. Trimming them is the opposite of what that section asks.

# Caveats for whoever resumes this

1. **Coverage is mechanical.** The detectors find drift that names a stale
   symbol, number, or file. Drift in a comment whose *prose* misdescribes the
   logic without naming anything stale would not surface — catching that class
   needs a semantic read of each file, which this survey did not do. **This is
   the only part of the survey still open.**
2. **Reconcile against the working tree first.** At survey time ~50 files were
   modified and uncommitted (`+629/-247`), overwhelmingly comment changes —
   apparently a comment pass already in progress. Items **3, 8 and 9** touch
   files in that set (`dialog_common.go`, `fileutil/atomic.go`, and
   `system_principals.go` was clean but neighbours were not). Checked with
   `git diff` before each edit; none of the in-flight changes touched a target
   line, so nothing was undone or duplicated. Items 1, 2, 4, 5 also touch
   modified files (`sql_statement.go` was clean; `xml_highlighter.go` and
   `query_store_reports.go` were modified).
