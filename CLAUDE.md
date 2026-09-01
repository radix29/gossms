# CLAUDE.md

Context for Claude Code sessions on **goSSMS**. Read this first; it points to
the rest of the docs rather than repeating them.

## What this is

goSSMS is a portable, cross-platform terminal TUI reimplementation of SQL
Server Management Studio, written in Go 1.27. One build runs on
Linux/macOS/Windows — no CGO, no build-tag-split files. `runtime.GOOS`
branching exists in exactly two places: `internal/tui/os_clipboard.go`
(shells out to `xclip`/`wl-copy`/`pbcopy`/`clip`, OSC-52 fallback) and
`internal/version/version.go`.

- Module: `github.com/radix29/gossms` — https://github.com/radix29/gossms
- Depends on `github.com/radix29/gosmo`, the author's own companion library for
  SQL Server management objects (https://github.com/radix29/gosmo), and
  `github.com/gdamore/tcell/v3` (`v3.4.2`) for the TUI backend.
- `go.mod`'s `require` pins a gosmo tag, but the `replace github.com/radix29/gosmo
  => ../gosmo` directive is deliberately **active** during development, so builds
  use the sibling checkout and `HEAD` may depend on untagged gosmo code. The
  author's layout has both repos as siblings: `~/go/gossms`, `~/go/gosmo`.
- Version resolves automatically from the pushed git tag — see
  `internal/version/version.go` — never hand-edited.

## Required reading by task

Read what the task actually touches — a one-file fix needs none of this. Each
entry names a section, not a whole document; read the section.

| If touching… | Read first |
|---|---|
| User-facing behavior, features, or keys | `README.md` |
| Anything spanning packages | `ARCHITECTURE.md` § Package map, § Which document owns what |
| Anything under `internal/tuikit/**` | `internal/tuikit/README.md` § Design principles |
| A new tuikit control or dialog | `internal/tuikit/README.md` § Adding a new control |
| `HandleMouse`, overlays, focus, drag | `ARCHITECTURE.md` § The mouseDragging idiom |
| A goroutine delivering a result to the UI | `ARCHITECTURE.md` § Async result delivery: postAndWake |
| gosmo changes / the `replace` directive | the `dev-with-local-gosmo` skill; `ARCHITECTURE.md` § Developing against a local gosmo checkout |
| Known bugs, deferred scope, release blockers | `docs/open-threads.md` — check before reporting something as newly found |

## The one rule that matters most: verify against real source, don't guess

Guessing at `tcell`/`gosmo` API shapes from training data produces code that
*looks* plausible but doesn't match the real API. Before writing any code that
calls into either, check the real source — `go doc`, grep the module cache
(`go env GOMODCACHE`), or read `~/go/gosmo` directly. Don't rely on memory of
"similar" APIs from other versions or packages.

Standing gotchas:

- `Screen.PollEvent`/`PostEvent` don't exist in tcell v3 — replaced by a
  channel, `EventQ()`.
- The modifier accessor is `Modifiers()`, not `Mod()`.
- `gosmo.Database.Name` / `.State` / `.RecoveryModel` etc. are *methods*, not
  fields.
- `gosmo.Server` has **both** `Database(name)` and `DatabaseByName(name)`, and
  they are not interchangeable — picking the wrong one fails quietly.
  `Database` is a lightweight handle with no query and zero-valued metadata,
  and the only one that works under a `WithScript`-derived context;
  `DatabaseByName` queries `sys.databases`. Both are documented on the methods
  themselves — `go doc gosmo.Server.Database`.

## Build & verify

```sh
go build -o gossms ./cmd/gossms   # build
go run ./cmd/gossms                # run without building a binary
go test ./...                      # test
gofmt -w .                         # format in place
go vet ./...                       # vet
```

No Makefile — plain `go` toolchain only. Use these directly rather than
eyeballing correctness; a real shell is available here.

Version/Commit/Date (`internal/version`) resolve automatically, in priority
order: `-ldflags -X` (set by `.github/workflows/release.yml` from the pushed
tag) → `debug.BuildInfo.Main.Version` (from `go install …@<tag>`) → the literal
`"(devel)"` default. Nothing here is hand-edited before a release.

### Green tests are not verification

`go test ./...` passing means the unit tests pass, not that the change works.
Nearly every real bug in this project's history was caught by driving the built
binary. For anything touching the TUI or the database layer, verify by running it:

- **TUI behavior** — drive the binary headlessly under tmux:
  `tmux new-session -d -s t -x 100 -y 30 <binary>`, `tmux send-keys -t t ...`,
  then `tmux capture-pane -t t -p` (plain) or `-p -e` (SGR codes, to tell focused
  from merely selected). Send `Escape` in its **own** `send-keys` call — batched
  with a following key, tcell reads `\x1b<key>` as Alt+key. Verify focus against a
  capture after each keystroke instead of counting `Tab`s, and capture the full
  pane height before concluding a dialog closed.
- **Database behavior** — a real SQL Server instance, not a mock. Connection
  details are deliberately not in the repo; ask for them. Create throwaway
  databases/logins, exercise the real write path, then drop them — never mutate
  pre-existing ones.
- When a fix is subtle, A/B it: keep a pre-fix binary, show the old behavior
  reproducing and the new one not.

Tests must assert an outcome, not that nothing panicked. Check a new test by
mutating the code it covers and confirming it fails.

**A round-trip test proves two functions are inverses, never that either is
right.** Where a load half and a write half share parallel label/code tables
(schedule dropdowns, permission-state tables, any `items[]`/`values[]` pair), a
fault in the shared table cancels out: swap two entries in `weekdayBits` and the
checkbox labelled Monday sets Tuesday's bit while `populate`/`readFrequency` still
agree. Pin such a pair by *naming* it (`"Monday"` -> `gosmo.WeekdayMonday`) and by
asserting both slices are the same length; add a page test that pins the value
reaching the server (`agent_schedule_props_page_test.go` pins `@freq_interval`).

**A Properties page can be driven end to end from a test — use
`internal/tui/fakedb_test.go`.** `gosmo.NewServer` takes a caller-supplied
`*sql.DB`; the harness gives `newFakeConn` (a `*db.ServerConn` over a scripted
driver that records every statement), `loadPage`, and `textRow` for addressing a
form by label. `database_props_files_page_test.go` is the worked example. The
`gosmo.WithScript` harness the New-X dialogs use does *not* work here — a
Properties page's `load` and `apply` both open with a by-name read, and
`WithScript` intercepts writes only. Rules, each learned from a test that passed
for the wrong reason:

- **Drive a row with `Edit`, never `SetValue`/`SetSelected`** — the latter move the
  dirty baseline with the value, so apply skips the row and a "nothing was written"
  assertion passes wrongly. Use `editText`/`editSelect`/`editRadio`/`toggleByName`.
- **Address rows and grid cells by name, never by index** — these pages read their
  grid back positionally, so an index-based test agrees with a misaligned page.
- Where a page keeps a filtered or pending-removal subset alongside the full list,
  exercise *two* edits: the lists only diverge after the first.
- **Act on an object that is not first in its list** — a page that ignores the
  selection still passes when the test picks row 0.
- **Scope a `fakeResponse` with `db:`** when the page reads the same query in
  several databases, or every database returns the identical answer and the
  misalignment the test exists to catch is unreachable.
- **Scope one with `arg:` for a by-name read, placed *before* the list read** —
  responses match by substring in order, and `DatabaseByName`'s query also contains
  `FROM sys.databases`, so otherwise every object resolves to whichever row sorts
  first.
- **Assert with `StatementsIn(db)`, not `Statements()`, for anything
  database-scoped** — the bare `USE` is stripped as plumbing, and with it the only
  record of where the write landed; pair it with `assertNoStatementsIn`.
- `eachDatabase` (`db_scan.go`) and gosmo's `userMappingsIn` drop a database whose
  read fails rather than failing the page, so an under-scripted fake yields an
  empty grid and an apply that writes nothing. Assert the rows loaded first.

The harness is bounded: queries match by substring and are answered with whatever
the test scripted, so it shows the page asked for the right things and built the
right request — never that the T-SQL is valid. Statement text is gosmo's own
tests; acceptance is a live run.

## Scope discipline

Avoid unrequested tidying. A bug fix does not need surrounding cleanup, new
abstractions, compatibility shims, feature flags, or speculative validation. Do
the simplest behavior-preserving change that satisfies the task. Trust internal
code and framework guarantees; validate at system boundaries.

## Changing gosmo

`gosmo` is the author's own library, not a third-party dependency — add or change
functionality in it when gossms needs something it doesn't have yet, rather than
working around a missing capability inside gossms. Build and test inside `gosmo`
before relying on a change from gossms.

**gosmo is a general-purpose library with users beyond gossms. Never remove or
narrow a gosmo capability because gossms doesn't call it.** "No callers in
gossms" is not evidence of dead code — an unused method is one some other
application (or a future gossms page) depends on. This covers whole files,
exported methods, exported types and their fields, and struct fields only some
paths populate. The `*Seq` iterators in `iter.go` are the standing example: 91
exported methods, zero gossms callers, all deliberately kept.

When an audit turns up something unused, the allowed moves are: make it faster,
make its doc comment accurate, or add a test that pins it. Removing it, or
replacing a general form with the narrow one gossms happens to need, is not one
of them — bring it up instead. Optimisation must be behaviour-preserving at the
API surface: same signature, same results, same errors. Report unused gosmo
surface as "unused, kept deliberately", never as a deletion candidate. Stated
from gosmo's side in `~/go/gosmo/CLAUDE.md` § This is a library, not gossms's
back end.

Inside **gossms**, dead code is ordinary cleanup. The no-removal rule is about
gosmo only.

## Coding conventions

- **Comments: describe what the code does. Add the "why" only when getting it
  wrong reintroduces a bug, and then say so concretely.** The bar is whether the
  next person would break something without the note.
  - Load-bearing here: the invariant a `mouseDragging` latch protects, why
    `postAndWake`'s two halves can't be called by hand, why `SelectRow.orig` is
    read back from the widget, why `escapeSingle` goes on top of bracket-quoting.
    Each is a shipped bug a plausible "simplification" would bring back.
  - Not worth writing: rejected alternatives, restatements of the line, and
    narration of how the code came to look this way.
  - Prefer one sharp sentence naming the failure to a paragraph, and keep an
    earned long comment on the declaration it protects.
  - Existing long comments are not a cleanup target — `app.go`, `datagrid.go`,
    `secret.go`, `propsheet/common.go` are 33-50% comments, and that rationale
    has repeatedly stopped a regression. Judge only *new* comments by the bar above.
- Go 1.26+ features in active use: `new(T{...})` composite-literal syntax, the
  `slices` package, `errors.AsType`.
- `core.DisplayWidth(s)`, never `len(s)`, for any column-position math.
- **A property-sheet label must fit `propsheet.LabelWidth` (30 columns).**
  Text/Password/Int/Select pad with `core.PadRight` and Static clips itself; both
  hard-clip with no ellipsis, so an over-long label silently renders as a
  shorter, different one ("Auto update statistics asynchronously" became "Auto
  update statistics asynchr", directly under "Auto update statistics").
  `TestNoPropertySheetLabelIsTruncated` fails on any literal over the limit;
  shorten the label rather than widening `LabelWidth`, which moves the value
  column on every page. Check/Radio/Section/Note draw at full width and are exempt.
- `tuikit` is a strictly one-way dependency graph and knows nothing about `tui`.
- Every `tuikit` sub-package is one file per type or tightly related group, plus
  `common.go` for cross-file helpers and `doc.go` for the package doc.
  `internal/tui` files are one-per-purpose (`app.go`, `menu.go`, …).

## Application rules (not derivable from the code)

- **Seed `QueryPanel.savedText` from `qp.editor.Text()`, never from the string you
  passed to `SetText`.** `SetText` normalizes (tabs expanded, CRLF folded, invalid
  UTF-8 to U+FFFD), so seeding from the source marks the panel dirty on open; a
  falsely dirty panel prompts to save on close and the save rewrites the file
  normalized — `File > Open` shipped that way and converted a UTF-16 script to
  U+FFFD-laden UTF-8 on disk. Anything reading a text file goes through
  `decodeTextFile`/`encodeTextFile` (`text_encoding.go`), which detect encoding
  **from a BOM only** — a guessed encoding rewrites the user's script in one they
  never chose — and preserve the file's line endings.
- **A widget's `HandleKey`/`HandleMouse` returns `true` only for events it actually
  acted on** — never "I'm focused, so I consumed it." `propsheet.Form` gives the
  focused row first refusal and falls back to its own Tab/Up/Down cycling only on
  `false`, so a widget that always returns `true` is a keyboard trap escapable only
  by mouse. Check what a new widget returns for Up/Down at a list boundary and
  Escape with nothing open.
  **A widget correct standalone can still trap once wrapped as a form row, and then
  it is the wrapper's job to translate.** `DataGrid` answers `true` to every arrow
  key — right when nothing else wants them, and it made all 21 `NewGridRow` pages
  swallow Down at the last row, Up at the first, and `Left` back to the page list.
  The fix lives in `propsheet.GridRow`, which snapshots `SelectedCell`/`ScrollCol`
  around the key and reports what actually moved; changing `DataGrid` would have
  reached QueryPanel, DetailBrowser and Activity Monitor, which rely on the blanket
  answer. Detect movement, never predict it; a grid with no cell cursor scrolls
  without changing `SelectedCell`.
- **Never call `DataGrid.SetData` on a grid the user is navigating without
  restoring the view — use `redrawGrid` (`prop_grid_helpers.go`).** `SetData`
  discards three things: the cell cursor (reset to 0,0), the scroll position, and
  any dragged column width. From inside the grid's own `OnSelectRow` it is worst,
  because the callback runs *after* the grid moved: the move is undone, `GridRow`
  reports "not handled", and `Form` moves focus out on the first arrow key — six
  sites shipped that way and every row but the first was unselectable by mouse
  *and* keyboard. Restoring only the cursor is not enough either:
  `SetSelectedCell` ends in `ensureVisible`, which scrolls from zero and lands the
  user's row against the bottom edge (Server Properties > Permissions moved the
  whole list on every toggle). `redrawGrid` wraps
  `controls.DataGrid.SetDataPreservingView`, which restores widths, then scroll,
  then selection, in that order. Outside `internal/tui` call
  `SetDataPreservingView` directly (`propsheet.ToggleGridRow` cannot reach
  `redrawGrid`). Never hand-roll the pair — six pages did, all dropped the widths.
  `wireGridEditor` (`ag_props.go`) packages the commit/load/redraw wiring for the
  grid-plus-detail-editor idiom.
  **A redraw after the row *set* changes is `resetGrid`, not bare `SetData`.**
  An Add/Remove/Revert does want the cursor placed deliberately, but `SetData`
  discards the dragged widths on the way and setting the cursor afterwards hides
  that there was anything to notice — no cursor jump, no keyboard trap, just a
  column snapping back. Seventeen sites shipped as `SetData` + `SetSelectedRow`
  for that reason. `resetGrid(grid, headers, rows, row)`
  (`prop_grid_helpers.go`) is `SetDataPreservingView` followed by
  `SetSelectedRow`, and is the only correct form: `redrawGrid` keeps the old
  cursor, which a changed row set has invalidated.
- **A mouse gesture's modifier is not yours to rely on — Shift+click especially.**
  A VTE terminal (xfce4-terminal, GNOME Terminal) handles Shift+mouse as its own
  text selection whenever an application has mouse reporting on, and forwards
  nothing: the app sees no event at all, which reads exactly like a broken
  binding. Ctrl and Alt are delivered. So a Shift+mouse gesture needs a second
  modifier that means the same thing — `extendSelectionMods` in
  `datagrid_input.go` is `ModShift | ModAlt` for this reason — and a keyboard
  route as well. Key Diagnostics logs mouse events (button, modifiers, position,
  drags collapsed), which is what tells a swallowed modifier from a wrong
  binding; a tmux harness cannot, since injecting the SGR sequence bypasses the
  terminal that would have eaten it.
- **A `Ctrl+Shift+<letter>` chord never arrives as `KeyCtrlX`.** tcell folds a
  Ctrl-modified rune into `KeyCtrlA..KeyCtrlZ` only when Ctrl is the *sole*
  modifier, so Ctrl+Shift+O arrives as `KeyRune "o"` (Kitty) or `"O"` (xterm
  modifyOtherKeys) with `ModCtrl|ModShift`, and legacy terminals send plain `0x0F`.
  `normalizeCtrlRune` (`app_events.go`, called once atop `handleKey`) folds it back
  app-wide — a binding testing `KeyCtrlX` *and* `ModShift` works only because of it,
  and only on modern keyboard protocols. Anything that must work everywhere needs a
  function key or Ctrl+non-letter too, the way Connect has F9. Verify a new chord by
  injecting its encodings with `tmux send-keys -H` and reading Key Diagnostics; a
  unit test proves nothing here.
- **Every menu item and toolbar button must be context-gated** — never let a click
  or keypress do nothing, do the wrong thing, or crash on an unmet precondition (a
  connection, an active query panel, an Object Explorer selection).
  `MenuItem`/`ToolbarButton` have an `Enabled func() bool`; the reactive fallback is
  a guard plus `setStatus(...)` in existing wording ("Not connected — use File >
  Connect", "No active query panel").
- **Every Properties page that can write declares the rights its writes need, and
  the rights are the *page's*, not the dialog's.** `withRequires` at the
  `[]propPage` constructor, or `withRequiresOn` when any right is schema- or
  object-scoped — an object-scoped right asked without a securable answers for
  nobody, so a page on `objectWriteRights()` wrapped in plain `withRequires`
  compiles, looks right, and shows a read-only banner to the one principal who
  could actually write it. The securable for an index, a statistic or a key is the
  **table**: that is what SQL Server checks and what gosmo's probe records.
  Login General takes ALTER ANY LOGIN, Login Server Roles takes ALTER ANY SERVER
  ROLE and Login Securables takes CONTROL SERVER — one list per dialog would have
  been wrong on two of the three. `prop_page_requires_test.go` fails on a page
  that declares nothing (unless it is named in `pagesThatOnlyRead`, which is only
  for pages with no `apply`), on a stale exemption, on an object-scoped page with
  no securable, and on a new `[]propPage` constructor absent from its list.
  **The banner's check and the menus' gate are one function, `rightsAllow`
  (`permission_gate.go`) — never add a second copy.** They had diverged: the
  banner's half knew only server and database scope, so it would have shown a
  false read-only for every object-scoped right and never fired at all for SQL
  Agent's msdb memberships, which fail open when read as server permissions. The
  callers differ in one thing only, how a database's capabilities are reached —
  cached for the UI goroutine, probing for a page's `load`.
  **Inside it, the object-scope DENY is asked first and separately.** Every right
  in a set is an alternative that can only *add* permission, but SQL Server
  resolves a DENY on the object over all of them, so `objectDenial` runs before
  the loop rather than as another case in it — and it exempts sysadmin, because
  the probe reads permissions through `public` and a DENY to public is recorded
  for the one login the server never applies it to.
- **A dialog-level scrollbar goes through `ModalDialog.DrawContentScrollbar`, not
  `core.DrawScrollbar` at `Rect().Right()-1`.** On a terminal too small for its
  requested size the content is clipped to `InnerRect` (`App.drawDialogs` wraps the
  screen in a `core.ClipScreen`; `DrawBase` narrows it when `clamped()`), and the
  border column the bar sits on is outside that clip — correct at full size,
  silently gone when clamped. A scrollbar inside a child widget is on the widget's
  own rect and needs nothing. `DrawBase` sets the clip only when clamped because an
  overlay a dialog opens may extend past the box.
- **Key Diagnostics (`internal/tui/key_diagnostics_dialog.go`, Help menu) is a
  permanent shipped feature**, not debug scaffolding, and out of scope for any
  pre-release trimming — it decodes tcell's exact Key/Modifiers/rune per keypress,
  which separates a real app bug from a terminal limitation.
- **An open dialog owns the clipboard outright — `activeClipboardTarget` must never
  fall past `topDialog()` to a panel underneath.** Ctrl+C/X/V are consumed centrally
  in `App.handleKey` before any dialog sees them (the Screen clipboard methods exist
  only in the application layer), so the target is resolved by asking the frontmost
  dialog for its focused field via `core.ClipboardHost`. The old form was a switch
  naming three of thirty dialogs with a fall-through, and Ctrl+X in the Find dialog
  silently cut the query editor's selection behind it. A dialog with no text entry
  deliberately isn't a host, and an inert clipboard is correct there. A new dialog
  with a text field implements `FocusedClipboardTarget`, returning an **explicit**
  nil on every miss (a typed nil widget in an interface is not a nil interface).
  `TestEveryDialogWithTextEntryIsAClipboardHost` catches a missing host but cannot
  catch a reintroduced fall-through — which is why this is written here. A dialog
  that must *react* to an edit (re-filter a list, refresh a preview) implements
  `core.ClipboardEditHandler` and checks the target it is handed rather than
  assuming the edit landed in the field it watches. `App.pasteInto` drops the text
  unless the widget it was aimed at is still the target: every clipboard read is
  asynchronous, so the dialog may be gone by the time it returns.
- **Never give a procedure you install outside `master` an `sp_` prefix.** An
  `sp_` name falls back to `master` when the current database has no such
  procedure, corrupting DDL on the very path that installs one: `CREATE OR ALTER
  dbo.sp_x` in tempdb finds master's copy and fails "Invalid object name", and
  `DROP PROCEDURE IF EXISTS dbo.sp_x` in tempdb **deletes master's copy** — it did,
  live. `internal/activity/block.go` is the worked example: master's copy is
  `sp_block`, tempdb's is `usp_block`, and every `EXEC` names its database.
- **A folder's Object Explorer filter has to be applied twice — once for the tree,
  once for the Detail Browser.** The tree's half is `filterChildren` in
  `fetchChildren`; the pane's loaders (`detail_browser_*.go`) query gosmo
  independently and hold gosmo objects, so they use `filterObjects` on the
  collection *before* rows are built — a progressive loader backfills by index, so
  filtering rows afterwards writes each count and size into the wrong row. A loader
  that skips this leaves the pane listing objects the tree has filtered away.
  **Both halves also push the filter into gosmo's `...FilteredContext` listing — that
  push-down is an optimisation, never the meaning of the filter.**
  `filterChildren`/`filterObjects` still run over whatever comes back and stay the
  authority, so `nodeFilter.pushdown` must either reproduce their comparison exactly
  (values trimmed as `matchText` trims them, whole calendar days as `matchDate`
  compares them) or refuse the filter, which `serverFilter` turns into "read the
  whole folder". Two rules in gosmo's clause builder are the ones a plausible
  simplification removes: compare `LOWER(col) LIKE LOWER(@p)`, because a bare LIKE
  follows the database collation and drops rows on a case-sensitive one; and escape
  the pattern with `likeEscape` plus `ESCAPE`, because `%`, `_` and `[` are legal in
  an identifier — unescaped, a filter for `pct_1` also matches `pct1100`.
- **A panel toolbar cell that does not fit its row is not drawn at all.**
  `layoutToolButtons` gives an overflowing cell a zero rect, and a zero rect is
  neither painted nor clickable — so adding a cell can silently delete the last
  one. The Query Store panel's two new filters took `Refresh` off the toolbar of
  every pane narrower than ~150 columns, and every unit test still passed. Check
  a new cell against the real pane width (Object Explorer takes ~60 of the
  terminal), not against the terminal's; the Query Store panel's filters live on
  the action row for exactly this reason.
- **A host acting on whole objects reads `DataGrid.SelectedRows()`, never
  `SelectionBounds()`.** The bounds are a rectangle, which is the wrong shape for
  a Ctrl+click selection: rows 1 and 3 come back as 1..3, so a Details pane
  reading them deletes the row the user deliberately left out, with the
  confirmation naming only the two they picked. `selectedRowObjects`
  (`detail_browser_ops.go`) is the worked example. The bounds are still right for
  a *cell* range — the block copy is what they exist for.
- **A panel that draws a `DataGrid` must also call `grid.DrawOverlay(s)`, after
  every grid it draws.** The cell context menu and the value popup are drawn
  outside the grid's own rect, so `Draw` alone paints neither — and the menu
  still opens and still swallows every key until Escape, which reads as a dead
  right-click rather than a missing draw call. The Query Store panel shipped
  that way: its "Copy" / "Show Value" menu had never once been visible.
- **A grid cell that flattens a multi-line value is a rendering, never the value
  itself — "Show Value" must be handed the original.** `queryStoreOneLine` joins a
  Query Store statement onto one line because a raw newline breaks the grid row,
  and `DataGrid.OnShowValue` opens that cell in a *runnable* query panel: a
  statement whose first line ends in `-- comment` arrives as
  `SELECT 1 -- pick one FROM dbo.t`, with the whole FROM clause inside the
  comment. `OnShowValue`'s first parameter is the **column** index, not the row,
  so the row comes from `grid.SelectedRow()` (`DataGrid.openViewer` reads the cell
  at `selRow`/`selCol`). `QueryStorePanel.showValue` resolves it from
  `qsResultRow.queryText` held in memory; `DetailBrowser.showQueryStoreValue`
  cannot — its grid is `[][]string` shared with every other node type — so it
  re-reads the statement by the row's `Query ID` through
  `gosmo.QueryStoreQueryTextContext`. A test asserting only that *a* read happened
  cannot catch a hook that addressed the wrong row: the fake answers every id
  alike, so assert the bound id with `fakeInstance.ReadArgs`.
- **Never call `rows.Next()` speculatively inside `internal/query/executor.go`'s
  `sqlexp.ReturnMessage` loop.** One extra `Next()` on an exhausted result set makes
  the driver consume the protocol message `retmsg.Message(ctx)` is waiting for: the
  grid comes up empty, with no error and no Messages tab. Gate any drain on having
  actually abandoned the set mid-scan (`scanNext` returns a bool for exactly this).
  Unit tests do not catch it; only a live query does.

## Mouse, overlays, and async UI

**Read `ARCHITECTURE.md` § The mouseDragging idiom before touching any
`HandleMouse`** — it has the reasoning and the shipped bug behind each rule. Five
invariants, and where each is implemented:

1. A gesture belongs to whatever claimed its first press, until the release.
   Routers: `App.gestureOwner` (`app_events.go`), `QueryPanel.dragZone`
   (`query_panel.go`), `propsheet.PropertySheet.dragZone` (`sheet_input.go`) — each
   an `armGesture`/`armDrag` at every branch that claims a press, plus a
   `routeGesture`/`routeDrag` that replays to the owner.
2. `App` also snapshots the modal layer (`gestureOverlay`/`overlaySnapshot`) and
   drops held events across a change.
3. A widget that acts on `Button1` needs a per-widget `mouseDragging` latch, set on
   the press and cleared on the matching `ButtonNone`.
4. A latch must not survive into the widget's next showing — `ModalDialog.Show()`
   clears both.
5. A host that returns early from `HandleMouse` must still forward `ButtonNone` to a
   latch-bearing child.

**A dialog with a text field uses `dialogs.FieldGesture` — never a hand-rolled
`dragField`.** Points 1, 4 and 5 all land on the same three calls
(`Release`/`Replay`/`Clear`), and each has a placement that is not local to it:
`Release` above `ConsumeOutsideClick` *and* above any mode switch, `Replay` after
`ConsumeOutsideClick` and before any hit-test, `Clear` in `Show`. Seven dialogs had
their own copy. `ARCHITECTURE.md` § dialogs.FieldGesture has the failure each
placement prevents; the dialog keeps only its own hit-testing and focus handling.
Two meta-tests enforce it, and both are needed: `TestEveryDialogWithATextFieldOwns
AFieldGesture` walks the built dialogs for one that owns a loose
`*widgets.InputField` and no gesture, and
`TestFieldGestureCallsAreOrderedCorrectly` reads the source of both dialog
packages for the order — `Release` above `ConsumeOutsideClick`, `Replay` below it
and above the last `ButtonClicked`, `Claim` used at all, `Clear` on the path that
shows the dialog. Options, Prompt and TypedConfirm each hand-rolled the protocol
and got it wrong long after the other seven were converted, with every test
passing: a drag from the field to the button row pressed the button under the
pointer, which on Prompt *accepted* the rename and on TypedConfirm answered the
confirmation the retyping exists to slow down.

An overlay drawn last gets **first refusal** of every key/mouse event while open —
`DataGrid.OverlayActive()` at the top of `QueryPanel.HandleKey`/`HandleMouse`; the
focused row before positional routing in `propsheet.Form.HandleMouse`.

A background goroutine reports its result with **`App.postAndWake(fn)`**, never its
two halves (`postEvent` then `wakeEventLoop`) by hand — getting the ordering wrong
leaves the result queued and invisible until an unrelated keypress drains it
(shipped bug: tree nodes stuck on "Loading..."). See `ARCHITECTURE.md` § Async
result delivery: postAndWake. `QueryPanel`'s elapsed-timer tick is the one
legitimate bare `wakeEventLoop()` caller: it has no callback to post, only a redraw.

**A background operation that latches UI state before it starts must use
`App.safegoRepair`, not `App.safego`** — a busy flag, a "loading" placeholder, a
toolbar the flag dims. The latch is released by the callback the goroutine posts
when it finishes, and a panic unwinds straight past that callback, so the latch
survives for the object's lifetime: the Log File Viewer's whole toolbar stays
inert, an Activity Monitor tab sits at "Running..." forever, a Properties page's
button refuses every later click. `safego` alone reports the panic and leaves the
UI stuck; the repair step is the way out. Cover it the way
`TestPageActionLatchClearsWhenTheActionPanics` does — panic the action, then assert
the *next* click still runs.

## Splitting a file that's grown too large

One file per type/group, plus `common.go` and `doc.go`. Then: extract each section
by exact line range; never retype by hand; diff the extracted text byte-for-byte
against the original; only then delete the source section/file.

## Repo hygiene

- `todo/` is tracked in git but is not source — scratch notes, SSMS mockups, scratch
  SQL. Don't build from it or act on it unless asked, and leave it out of cleanups.
- `CHANGELOG.md` and `RELEASE.md` cover the release process. Don't edit either as
  part of a feature or fix unless asked.

## Self learning

Turn mistakes into rules, and never repeat them — but write the rule down where it
will be loaded again:

- A rule about the code or the workflow → this file, or `ARCHITECTURE.md` if it
  needs more than a paragraph.
- Work knowingly left undone, or a bug found and not fixed → `docs/open-threads.md`.
