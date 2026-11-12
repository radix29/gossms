# tuikit review plan — second pass (2026-11-12)

A second whole-library review of tuikit (`~/go/tuikit`, 10 packages, ~24k
non-test lines) for bugs, inconsistencies, optimizations, simplifications,
architecture and refactoring. The first review (same day, its plan since
deleted) prepared the extraction and is done; this pass covers what that one
read only by outline, plus the code it added. Nothing settled in
`docs/decisions.md` is re-raised (tab expansion, the uncapped redo stack and
the unguarded `applyStep` slice stay as they are).

**Progress:** steps 1–7 of the implementation order are done (BG4, BG5, BG8;
BG7; BG6; BG1 + IN2; AR2's first half; BG2, BG3, AR1; BG9, BG10, BG11,
BG13); the rest is plan only. Breaking changes are allowed and
marked **(breaking)**; tuikit has no tag yet (`docs/open-threads.md` § tuikit:
what is left), so this is the cheapest moment for them. Each item names what
gossms must change with it. Nobody commits but the user.

**Baseline:** tuikit `HEAD` 9021b9b, gossms branch `refactor/tuikit` at
0d48880. `go build`, `go vet`, `gofmt -l`, `staticcheck` and `go test ./...`
are clean in tuikit.

**How it was found**

- **Read in full:** `controls` (DataGrid, TreeView, ListBox, MenuBar,
  ContextMenu, cascade, Toolbar, ToolRow, ValueViewer); `controls/editor`
  except the XML/JSON highlighters; `propsheet` sheet, form, `rows.go`,
  GridRow, EditorRow, ButtonsRow; `dialogs` modal, stack, FieldGesture,
  FileDialog; the `core` files added by the first review (Buffer, ClipScreen,
  Gesture, PressLatch, ClickTracker, control interfaces).
- **Not re-read:** `widgets`, `layout`, `theme`, the rest of `core` and
  `dialogs` (read in full by the first review); `charts` (only the head of
  `common.go`); propsheet's choice rows and ToggleGridRow; the highlighters.
  Findings there are still possible.
- **Probes:** throwaway tests, deleted afterwards, reproduced every item marked
  *(confirmed)*, with the numbers quoted. Unit level only: per
  `docs/testing.md`, drive each in the built gossms binary before fixing it
  and again after.

Delete this file when the work is done; move leftovers to
`docs/open-threads.md`. `TestNoDanglingDocReference` checks every `.md` path
named here.

---

## BG — Bugs

### BG1. Moving the mouse over a property sheet's page list steals keyboard focus *(confirmed)*

`ListBox.HandleMouse` returns `true` for any event inside its rect, including
plain motion with no button. `PropertySheet.HandleMouse` treats `true` as
"claimed" and calls `setZone(zonePages)`. Probe: focus in the form, one
no-button motion event over the list → the zone is the page list. Typing then
goes to the list, not the field. `Form.HandleMouse` already returns `false`
for hover for exactly this reason; ListBox was missed.

**Fix:** ListBox returns `false` for `ButtonNone` unless it released a held
latch. The sheet changes zone only on a press. Add the contract test in AR2 so
no other control consumes hover.
**Drive:** any Properties dialog → Tab into a text field → move the pointer
across the page list → type.
**Done:** ListBox claims a release only when it ended its own press or thumb
drag inside the list; the sheet moves focus only on a button press
(`focusOnPress`), so neither a hover nor a wheel tick over the page list or
the form's grid moves it. `TestSheetFocusZoneMovesOnlyOnAPress` (fails on the
old code). Driven on win10cli: Server Properties → Memory, Tab into Minimum
server memory, hover across the page list, typed `5` → `05` in the field; a
click on the list still takes focus. The before-fix drive was not repeated:
the review's probe and the new test's failure on the old code stand for it.

### BG2. A held drag inside a DataGrid acts on whatever it drifts onto *(confirmed)*

The grid has seven independent latches (`mouseDragging`, `rowHeaderDragging`,
`sbDragging`, `sbDraggingH`, `colResizing`, `headerPressed`, `rowPressed`) and
each branch tests only some of them. A cell-selection drag that reaches:

- a header separator starts a column resize (width 6 → 11);
- the right-edge column grabs the vertical scrollbar (scroll jumped 0 → 68);
- the status row grabs the horizontal scrollbar (scrollCol 0 → 2);
- the header, on a row-mode grid (no cell cursor), fires `OnHeaderClick` — the
  Details pane re-sorts mid-drag.

This is tuikit's own "a press claims the gesture" rule
(`~/go/tuikit/ARCHITECTURE.md` § Mouse gestures) broken inside one widget.

**Fix:** one owner per press: a `core.Gesture[gridZone]` (cells, row header,
vertical bar, horizontal bar, resize, header, none) replaces the seven bools.
Zones are hit-tested only on the fresh press; every later `Button1` goes to
the owner. `SetSource`/`SetError`/`ShowValue`/`SetBrowseOnly` release it.
**Drive:** results grid — drag a selection up through the header, right onto
the scrollbar, down onto the status row.
**Done:** `DataGrid.gesture` (`core.Gesture[gridZone]`: inert, cells, row
header, Ctrl+mark, both bars, resize) replaces the seven bools; `press`
hit-tests a fresh press only, `routeDrag` takes every later `Button1`.
`OnRowDoubleClick` now fires after `Arm`, so a host's `ShowValue` from it still
ends the gesture. New core helpers `OnScrollbar`/`OnScrollbarH` (the press
hit-test; `HandleScrollbarDrag` keeps its bool for ListBox, DropDown,
ModalDialog, PanelManager). Tests `datagrid_gesture_test.go`. Driven on
win10cli (results grid, 200 rows × 9 columns): a selection dragged across the
`name|object_id` separator left the widths alone (a fresh press there still
resized); onto the vertical bar column it selected the cell under it, no
scroll jump; onto the status-row bar it scrolled one row, no thumb jump;
Details pane (Databases), a row drag over the header did not re-sort, a fresh
header click did. The before-fix drive was not repeated: the review's probes
stand for it.

### BG3. A latched drag stops at the widget's edge *(confirmed)*

`DataGrid` and `Editor` test `rect.Contains` before their vertical scrollbar
and selection drags. A thumb dragged below the grid stops (probe: scroll
stayed at 11, event returned `false`); a text or cell selection cannot be
extended past the visible rows by dragging. The first review fixed the same
thing in ListBox and TreeView.

**Fix:** with BG2's owner in place, route a held gesture before the bounds
test. For a selection drag past an edge, clamp the pointer into the content
and scroll one row/column per event toward it (no timer: tuikit has none).
Same in Editor (both wrap modes).
**Drive:** editor — drag a selection below the last visible line; grid — drag
the thumb off the bottom.
**Done:** both widgets route a held gesture ahead of the bounds test.
`DataGrid.dragCells`/`dragRowHeader` and `Editor.dragText` clamp the pointer
to the row/column just beyond the view and scroll one per motion event; a
toggle grid paints only cells under the pointer and never scrolls. Tests in
`datagrid_gesture_test.go`, `editor/editor_gesture_test.go`. gossms's
`TestDetailGridDragEndingOverTheStripReleasesTheGrid` now checks the clicked
row absolutely (its drag onto the strip extends the selection). Driven:
results grid, 5 motions below the grid → scrolled 5, selection followed;
query editor (40 lines), 6 motions below → scrolled 6, selection from line 1
to 22; editor thumb dragged far below reached line 41 with the results
splitter untouched.

### BG4. PgDn on an empty TreeView panics on the next Draw *(confirmed)*

`KeyPgDn` sets `sel = min(len(nodes)-1, …)` = -1; `ensureVisible` copies it to
`scroll`; `Draw` indexes `nodes[-1]`. DataGrid guards the same four keys for
this reason. **Fix:** return early on an empty tree; clamp in `ensureVisible`.
**Done:** `TestTreeViewNavKeysOnEmptyTreeDoNotBreakDraw`; driven (the old
binary panicked on PgDn in an empty explorer, the new one does not).

### BG5. A TreeView populated before its first SetBounds starts scrolled *(confirmed)*

`SetNodes` → `ensureVisible(inner.H)` with `H = -2` sets `scroll = 3` (probe:
first drawn row is node 3). `SetBounds` never re-clamps, so it stays, and a
tree that grows taller after scrolling to the end shows blank rows.
**Fix:** `ensureVisible` does nothing for `h <= 0`; `SetBounds` re-clamps
scroll on a real change (IN3).
**Drive:** check Object Explorer's first frame and after enlarging the window.
**Done:** TreeView's `SetBounds` follows IN3's rule (unchanged rect: nothing;
changed: clamp both scroll offsets, selection untouched), so IN3 is left for
Editor and Form. `TestTreeViewPopulatedBeforeSetBoundsStartsAtTheTop`,
`TestTreeViewSetBoundsTallerReclampsScroll`; driven on win10cli.

### BG6. Editor.Paste is quadratic *(confirmed)*

`Paste` calls `insertRune` per rune (a fresh copy of the line each time) and
`insertNewline` per line (a splice of the document each time).

| Paste | Time |
|---|---|
| one line, 20,000 chars | 0.3 s |
| one line, 100,000 chars | 8.7 s |
| one line, 300,000 chars | 82 s |
| 5,000 lines at the top of a 50,000-line script | 1.9 s |
| (`SetText`, 50,000 lines, for scale) | 0.04 s |

The UI goroutine is frozen for all of it. A minified JSON/XML value or a
generated script is enough.
**Fix:** build the pasted lines once and install them with one
`replaceRange` (first line joined to the text left of the cursor, last to the
text right of it), under one `pushUndoSpan`. `blockPaste` is per line already.
**Tests:** a benchmark beside `editor_bench_test.go`; the existing undo
round-trip test covers the new span.
**Done:** `Paste` builds the lines once and splices them with one
`replaceRange` (`insertText`) under the existing `pushUndoLocal` span.
`BenchmarkEditorPasteLongLine` (100,000 chars: 8.7 s → 2.5 ms),
`BenchmarkEditorPasteManyLines` (5,000 lines atop 50,000: 1.9 s → 14 ms),
`TestEditorPasteTextAndCaret` (joins, caret, CRLF, undo). Driven: bracketed
paste of a 100,000-char line and of 5,000 lines twice, Ctrl+Z restored.

### BG7. One line action on a large script evicts the undo history *(confirmed)*

`Cut`, `Paste`, `DuplicateLines`, `DeleteLines`, `MoveLinesUp/Down`,
`IndentLines`, `DedentLines`, `ToggleLineComments`, the case conversions and
`ReplaceAll` call `pushUndo()`, which snapshots the whole document. On a
20,000-line script one step is 5 MB, and `maxUndoBytes` (64 MB) then drops the
oldest steps: 50 typed characters followed by 30 one-line indents left 13 undo
steps — every typed step gone. Holding Ctrl+Shift+Down does the same.
**Fix:** each of these knows its rows: `pushUndoSpan` over
`affectedLineRange` (± the neighbour a move swaps with; min..max match row for
`ReplaceAll`). `DuplicateLines`/`DeleteLines`/`MoveLines*` also switch from
`doc.edit` (drops every cache) to `replaceRange`.
`TestEditorUndoRestoresEveryEditPath` is the guard — extend it per path.
**Done:** every listed path pushes `pushUndoSpan` over its own rows
(`DuplicateLines` an empty span below the block; block paste through
`blockPasteSpan`; `Cut`/plain `Paste` through `pushUndoLocal`), and
`DuplicateLines`/`DeleteLines`/`MoveLines*` splice through `replaceRange`.
`pushUndo` (whole document) had no caller left and is gone; only
`ReplaceText` still records the whole document, by design. 14 new cases in
`TestEditorUndoRestoresEveryEditPath`,
`TestEditorLineActionsKeepUndoHistoryOnALargeScript`; the two byte-cap tests
now use `ReplaceText`. Driven: 20,000-line script, typed text + 40 line
actions, Ctrl+Z restored the original.

### BG8. Replacing a Toolbar's buttons or a MenuBar's menus can panic *(confirmed)*

- `Toolbar.SetButtons` keeps the old `widths`/`starts`; `Draw` and
  `HandleMouse` index them by the new button count (index out of range until
  the next `SetBounds`).
- `MenuBar.SetMenus` with fewer menus while a dropdown is open leaves
  `openMenu` past the end; the next key or mouse event indexes `menus[openMenu]`.

**Fix:** `SetButtons` remembers the last `SetBounds` arguments and lays out
again (which also lets `SetBounds` skip unchanged input, OP6); `SetMenus`
closes a dropdown whose menu is gone and clamps `selectedItem`.
gossms calls these from 9 sites; none changes.
**Done:** OP6's skip-when-unchanged is not in (step 12). `SetMenus` also keeps
a surviving dropdown's submenus unless its highlight had to move, and the two
`OnBeforeOpen` call sites return when the hook removed the menu being opened.
Tests `TestToolbarSetButtons*`, `TestMenuBarSetMenus*`,
`TestMenuBarOnBeforeOpenRemovingTheClickedMenuDoesNotPanic`.

### BG9. FileDialog opens an entry on a second, separate click *(done)*

Left over from the first review: `HandleMouse` activates when the clicked row
is already selected, with no timing. The list opens with row 0 selected, so
one click on `..` navigates up. **Fix:** `core.ClickTracker`, as ListBox.
**Done:** a click selects, a double-click opens; `loadDir` resets the tracker
so a click, Enter, click on the same row does not pair across listings.
`TestFileDialogClickSelectsDoubleClickOpens`,
`TestFileDialogClickDoesNotPairAcrossListings`. Driven (Open Query File).

### BG10. The completion popup commits on one click or on two, depending on the row *(done)*

A click on the selected row commits; a click on any other row only selects.
The first row is preselected, so one click on it commits and one click on the
second does not. **Fix (author's call):** recommended — a click selects, a
double-click (`ClickTracker`) commits, as SSMS does; the alternative is that
any click commits.
**Done** with the recommended option: `completionClicks` (reset when the items
change, the popup closes, or its bar is pressed).
`TestEditorCompletionClickSelectsDoubleClickCommits`. Driven (`sys.obj`
popup: one click on the preselected row stays open, a double-click commits).

### BG11. core.Buffer leaves half a wide glyph behind *(done)*

Writing a narrow cell over either half of a wide grapheme leaves the other
half: over the lead, the trailing cell stays width 0 and `Blit` skips it
(stale screen content shows through); over the trail, the lead keeps width 2
and `Blit` steps over the new cell. **Fix:** `write` blanks the orphaned half.
Matters to charts with CJK labels and to every test reading a Buffer back.
**Done:** `write` blanks the orphaned lead or trailing cells, keeping their
style. `TestBufferWriteOverHalfAWideGraphemeBlanksTheOtherHalf`. Unit only.

### BG12. A clamped dialog's left and right button groups overlap

Already in `docs/open-threads.md`. `DrawButtonsAtGated` and
`ButtonClickedAt` take a caller-computed x, so nothing knows both groups.
**Fix:** `ModalDialog` lays the two groups out together (one layout shared by
draw and hit-test); when they do not fit, the gap shrinks, then the left group
moves to a second row or drops, never overlaps. **(breaking)** for the app's
split rows (Connect, …): they pass both groups instead of an x.

### BG13. FileDialog's busy repaint bypasses the clip *(done)*

`showBusy` calls `d.Draw(d.screen)` with the raw screen, so a clamped dialog
spills its content for that one frame. **Fix:** draw through a
`core.ClipScreen`.
**Done.** `TestFileDialogBusyFrameStaysInsideAClampedDialog` (unclipped, the
content overwrote the right and bottom borders). Not driven: needs a remote
file browser in a clamped dialog.

---

## IN — Inconsistencies

- **IN1. `HandleKey` says "handled" for keys that did nothing. (breaking)**
  The rule in `~/go/tuikit/ARCHITECTURE.md` § Widgets and grids is "true only
  for events acted on". DataGrid answers `true` to every arrow, to Enter on a
  read-only grid, and to PgUp/PgDn on an empty one; Editor to Up on the first
  line and Down on the last; TreeView to every navigation key. `propsheet`
  works around it twice (`GridRow.HandleKey`, `EditorRow.HandleKey` snapshot
  cursor and scroll and compare). ListBox alone refuses keys while unfocused.
  → Each control returns whether anything changed (selection, cursor, scroll,
  text, a callback fired). No control tests its own focus: the host routes.
  The two propsheet adapters shrink to a forward (SI5).
  **gossms:** 13 call sites read the return of a grid/editor/tree
  `HandleKey`; audit each — a host that wants an arrow swallowed at an edge
  must now say so itself.
- **IN2. `HandleMouse` return for an event that did nothing.** ListBox `true`
  for hover (BG1); Editor's wheel at a limit `true` unwrapped, `false`
  wrapped; ContextMenu lets a right-click or wheel outside fall through with
  the menu still open. → One rule, stated in `core.MouseHandler`'s doc: plain
  motion is never consumed unless a latch or an overlay is open; a wheel over
  the control is consumed even at its limit; any press outside an open menu
  dismisses it (a right-click still falls through, so a new menu can open).
  **Done:** the rule is in `core.MouseHandler`'s doc and tuikit's
  `ARCHITECTURE.md` § Widgets and grids. Hover is no longer claimed by
  ListBox, DataGrid, Toolbar or a closed MenuBar (each still claims the
  release of its own gesture); the wheel is claimed at a limit by TreeView
  (blank rows and border too) and Editor (gutter, and wrapped). ContextMenu
  and MenuBar share `outsideMenu`: any press outside dismisses, a right-click
  then falls through, a wheel is swallowed with the menu left open. DataGrid
  lets a right-click that closed its own menu fall through, so it reopens on
  the clicked cell; Editor's wheel moved into one `handleWheel` for both wrap
  modes. Tests in `controls/mouse_rules_test.go`. **gossms:**
  `App.handleMouse` lets a dismissing right-click fall through past an open
  context menu or menu-bar dropdown. Driven: one right-click moves from the
  tree's menu to the Details grid's, and from an open File menu to the
  grid's. Found while driving, pre-existing, now in `docs/open-threads.md`
  § tuikit: the Details grid's own menu stays open when a click lands in the
  tree.
- **IN3. `SetBounds` does three different things.** DataGrid: no-op when
  unchanged, re-clamps scroll. ListBox: no-op when unchanged, re-runs
  `ensureVisible`. TreeView, Editor, Form: store the rect only (BG5; a wrapped
  Editor keeps a `scrollRow` past the end after a width change). → Unchanged
  rect: nothing. Changed rect: clamp scroll to the content, never move the
  selection.
- **IN4. Wheel step.** One row in DataGrid/TreeView/ListBox/Editor, three in
  `propsheet.Form`; horizontally one column in DataGrid, four in
  TreeView/Editor. → Named constants in `core` (rows, columns), used by all.
- **IN5. Text still hard-coded after the first review's Strings pass.**
  propsheet: "Nothing to revert — no unsaved changes on this page.", the
  `Int` validator's two messages. dialogs: default titles (Alert, Working,
  Prompt, Confirm, Properties, Open File), "Enter a value.", FileDialog's
  "Path:", "File:", "Name", "Size", "Modified", "DIR", "Listing … ...",
  PropertiesDialog's "Property"/"Value". controls: `ToolRowMoreLabel` is a
  const. → Into each package's `Strings`. `ToolRowMoreLabel` becoming a field
  is **(breaking)** for gossms code that reads the const.
- **IN6. `ToggleLineComments` hard-codes `--`. (breaking)** The open thread.
  → `Editor.SetLineComment(prefix string)`; empty (the default) makes
  Ctrl+/ a no-op. **gossms:** `sqledit.Configure` sets `"--"`.
- **IN7. Latches still hand-rolled.** Editor's `completionMouseDown` and
  `completionSbDragging`; DataGrid's seven (BG2). `dialogs.FieldGesture`
  protects only an `InputField`; a ListBox or Editor inside a dialog gets no
  equivalent. → `PressLatch`/`Gesture` everywhere; consider FieldGesture over
  `core.MouseHandler` (optional).
- **IN8. Small ones.** `TreeNode.onText` and `TreeView.lineWidth` measure the
  icon differently (`max(1, …)` in one). `TextRow.SetEnabled` says a disabled
  row is skipped by focus cycling, while § Widgets and grids says a disabled
  control keeps its place: state the Form exception there or align.
  `PropertySheet.RevertPage`'s comment says it leaves a message; the caller
  does.

## OP — Optimizations

- **OP1. A stale search makes every Enter cost a full regexp sweep
  *(confirmed)*.** The search stays active after the Find dialog closes (by
  design). A one-line edit rescans one line (0.4 ms); any edit that changes
  the line count rescans the document: 132 ms per Enter on 20,000 lines.
  → Splice the match list through `Document.changedSince`, as `rewrapSpan`
  does for wrap segments.
- **OP2. Menu `Enabled` predicates run far more than once per item
  *(confirmed)*.** Three calls per item per frame, and once more per item per
  mouse move (`ContextMenu.drawnRect` → `menuContentWidth` → `showsNote`).
  gossms has 61 `Enabled` funcs. → Evaluate once per item per draw into the
  box's cached state; cache the drawn rect whole (MenuBar already does). Falls
  out of SI2.
- **OP3. DataGrid.** A separator drag re-samples up to 200 rows on every
  motion event (4,000 `Row()` calls for a 20-step drag; a streamed source
  builds each). → Keep the sampled natural widths; a drag only reapplies
  overrides. Smaller: a selected row is drawn twice (`drawRow`, then
  `drawCellSelection`); `gutterWidth` formats a number on every call.
- **OP4. `Editor.Text()`** converts each line to a string before appending
  it. → Append runes into one pre-sized buffer. Same shape in `SelectedText`
  and the search's `fullScan`. Only with a benchmark.
- **OP5. Completion popup** measures every item's label and detail three
  times per frame. → Once, when the items arrive.
- **OP6. `Toolbar.SetBounds`** allocates four slices per call, and hosts call
  it per frame. → Skip when bounds and buttons are unchanged (with BG8).
- **OP7. Carried from the first review, still open:** the password
  `InputField.displayRunes` rebuild, `PanelManager.tabSegments` per event.
  Benchmark first.

## SI — Simplifications

- **SI1.** `DataGrid.SetSource` and `SetError` repeat a 12-line view reset.
  → one `resetView`.
- **SI2. One menu box.** MenuBar's dropdown and ContextMenu duplicate draw,
  hit-test, key and click handling line for line, and only the dropdown
  scrolls: a context menu or any submenu taller than the terminal silently
  loses its rows (the clamp is "defensive" only). → A `menuBox` (items, hover,
  scroll, rect) used for the root and for every cascade level; MenuBar becomes
  a header strip over it. Fixes OP2 and gives every level scrolling.
  **Drive:** Object Explorer's largest context menu on a 24-row terminal.
- **SI3.** The dim-behind-a-popup constants exist twice (ModalDialog,
  ValueViewer). → one `core` helper.
- **SI4.** `PropertySheet` keeps its own `screen` beside the embedded
  ModalDialog's. → `ModalDialog.Screen()`.
- **SI5.** After IN1, `GridRow.HandleKey` and `EditorRow.HandleKey` lose
  their snapshot-and-compare code. `CursorPos`/`ScrollPos`/`ScrollCol` stay:
  library surface, unused by tuikit, kept deliberately.
- **SI6 (optional).** FileDialog re-implements ListBox (keys, ensureVisible,
  wheel, scrollbar, press latch) because its rows have three columns. → an
  owner-draw hook on ListBox (`DrawItem`), then FileDialog holds a ListBox and
  BG9 disappears with it.
- **Carried:** the first review's S8 (PropertiesDialog vs a read-only
  one-page sheet) is still "consider".

## AR — Architecture

- **AR1. Make `core.Gesture` the one drag mechanism inside composite
  widgets.** BG2 and BG3 are the same defect; Editor (`mouseDragging`, two
  scrollbar latches, two completion latches), TreeView (`press`, two scrollbar
  latches) and Form (`sbDragging`) have the same shape and are one missed
  branch from the same bugs. Do DataGrid first (BG2), then give the others the
  same structure. `core.HandleScrollbarDrag` then takes the gesture's
  "is this the fresh press" instead of owning a bool.
  **Done:** Editor (`editorZone`: text, both bars, completion item and bar —
  IN7's two completion latches went with it, and a click that commits an item
  no longer drops its resends onto the text), TreeView (`treeZone`: acted row
  or title button, both bars), Form (`Gesture[int]`: a row's index, `formBar`,
  `formInert`; a release goes to the focused row and the owner row). Rule in
  tuikit's `ARCHITECTURE.md` § One owner per press inside a widget.
  `TestFormDragStaysWithTheRowItBeganIn`. Not driven in the binary: TreeView
  and Form (no tree on the test server overflows; no property-sheet drag was
  tried); their unit tests and AR2's contract tests cover them.
- **AR2. Contract tests that run every control through the rules.** Most of
  this review's bugs are a rule from `~/go/tuikit/ARCHITECTURE.md` broken in
  one more widget. A table over every control in each package's
  `control_test.go`: hover is not consumed; a release anywhere clears every
  latch; a drag that began elsewhere does not act; `HandleKey` is `false` when
  nothing changed; `SetBounds` with the same rect changes nothing; Home, End,
  PgUp, PgDn on empty content neither panic nor break the next `Draw`.
  **First half done** (hover, release, empty-content keys): three
  `TestContract*` tests in each of `controls`, `widgets`, `layout` and
  `propsheet`'s `control_test.go`. Hover is tried on every cell of each
  control (Form: with each focusable row focused in turn); the release test
  presses each gesture zone (cells, row/column header, separator, both
  scrollbars, gutter, toolbar button, splitter bar, tab row, every Form band
  and its bar), drags, releases in place and far outside, then expects
  neither motion nor a press outside claimed; the key test runs
  Home/End/PgUp/PgDn/arrows/Enter (plain, Shift, Ctrl) on empty content,
  sized and unsized, drawing after each. All pass with no library change.
  Checked to bite: the pre-step-4 ListBox, Toolbar, MenuBar and DataGrid and
  pre-step-1 TreeView fail them, and so does dropping any release-cleared
  latch tried (DataGrid's two, ListBox's, Editor's two, InputField's,
  Splitter's, Form's). `mouse_rules_test.go` keeps only the wheel and menu
  rules. Left for step 8: the "drag that began elsewhere" and
  `HandleKey`-false rows; the `SetBounds` row goes with IN3 (step 9).
- **AR3. Land the breaking changes before tuikit's first tag** (IN1, IN5's
  const, IN6, BG12), and update tuikit's `README.md`/`ARCHITECTURE.md` in the
  same change as each rule they state.

---

## gossms changes, by item

| Item | gossms change |
|---|---|
| BG1, BG4–BG11, BG13, OP*, SI1–SI4 | none (behavior only) — drive the named screens |
| BG2, BG3, AR1 | none in code; re-drive every grid/editor host's own gesture router |
| BG12 | dialogs with a left button group (Connect, …) pass both groups |
| IN1, SI5 | audit 13 `HandleKey`-return call sites |
| IN2 | done: `App.handleMouse` lets a dismissing right-click fall through |
| IN5 | `ToolRowMoreLabel` readers; optional: app sets its own strings |
| IN6 | `sqledit.Configure` calls `SetLineComment("--")` |
| SI6 | none (FileDialog is internal to tuikit) |

After each step: tuikit `go build ./... && go vet ./... && go test ./...`,
`staticcheck ./...`; then gossms `go build`, `go test ./...`, `staticcheck`,
`deadcode -test`, and the drive.

## Recommended implementation order

Crashes and data-loss first, then the gesture work that several fixes share,
then API changes while there is no tag, then cleanup.

| # | Item(s) | Why here | Size |
|---|---|---|---|
| 1 ✅ | **BG4, BG8, BG5** — done | panics and a wrong first frame; each a few lines | S |
| 2 ✅ | **BG7** — done | undo history loss; tests already exist | S–M |
| 3 ✅ | **BG6** — done | UI freeze; reuses BG7's span | S–M |
| 4 ✅ | **BG1 + IN2** — done | visible focus theft; fixes the return-value rule it came from | S |
| 5 ✅ | **AR2** (first half: hover, release, empty-content keys) — done | pins 1 and 4 for every control before the refactors | M |
| 6 ✅ | **BG2 → BG3** (DataGrid), then **AR1** (Editor, TreeView, Form) — done | one mechanism; drive every grid host | L |
| 7 ✅ | **BG9, BG10, BG11, BG13** — done | independent, small | S |
| 8 | **IN1 + SI5** | breaking; needs the gossms audit; extend AR2 with the `HandleKey` rule | M |
| 9 | **IN6, IN5, IN3, IN4, IN8** | API-visible normalization before the tag | M |
| 10 | **SI2 (+ OP2)** | menu unification; after 5 so the contract tests cover it | M–L |
| 11 | **BG12** | needs a layout decision for the too-narrow case | M |
| 12 | **OP1, OP3, OP5, OP6, SI1, SI3, SI4, IN7** | no behavior change; benchmark where noted | M |
| 13 | **OP4, OP7, SI6**, carried S8 | optional | — |
| 14 | **AR3** | docs with each step; tag tuikit when 1–11 are driven | S |
