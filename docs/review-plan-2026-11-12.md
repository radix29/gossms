# Review plan — 2026-11-12: tuikit, before it becomes its own module

A whole-package review of `internal/tuikit` (9 packages, ~24k non-test
lines) for bugs, inconsistencies, optimizations, simplifications and
architecture. It prepares for moving tuikit into its own module.
Nothing settled in `docs/decisions.md` is re-raised. § T-SQL lexing
("one lexer") still holds: A1 moves the lexer, it does not add a second one.

**Baseline (2026-11-12, gossms `HEAD` 07effa9, branch `refactor/tuikit`):**
`go vet` and `staticcheck` are clean on `./internal/tuikit/...`, and
`go test ./internal/tuikit/...` passes. tuikit imports nothing from gossms
outside tuikit, and nothing from gosmo. Its only third-party imports are
tcell/v3 and displaywidth.

How it was found:

- **Read in full:** `core`, `theme`, `widgets`, `layout`, `dialogs` (except
  the FileDialog draw/input files), `controls/listbox.go`, `tabstrip.go`,
  `menu_item.go`, `datagrid_overlay.go`, `document.go` (head), and the
  TreeView/MenuBar input paths. The rest of `controls`, plus `propsheet` and
  `charts`, were read by outline and targeted sections, not line by line.
- **Pattern sweeps:** hard-coded UI strings, double-click detectors, inline
  style construction, package-level mutable state, references to `internal/tui`
  and `docs/` from tuikit comments, test-only fake screens, the Focus/Bounds/
  Overlay method sets across controls.
- **Probes:** throwaway unit tests (deleted afterwards) reproduced B1–B5.
  These are unit-level only. Per `docs/testing.md`, drive each one in the built
  binary before fixing it, and again after.

This file exists because it was asked for. Delete it when the work is done,
and move anything left over into `docs/open-threads.md`.
`TestNoDanglingDocReference` checks every `.md` path named here, so cite only
paths that exist.

---

## B — Bugs

### B1. A password InputField hands its plaintext to the clipboard *(security, confirmed by probe)* — **DONE 2026-11-12**

`widgets.InputField` masks the drawing only. `SelectedText()` and `Cut()`
return the real value (the probe got `"hunter2 secret"` back), and
`App.copySelection` (`internal/tui/clipboard.go`) writes it to the clipboard:
through the OS clipboard, or through OSC 52 into the terminal over SSH.
Ctrl+Left/Right and Ctrl+Backspace/Delete use the hidden value's word
boundaries. Ctrl+Right from 0 stopped at 7, which reveals where its spaces and
punctuation are.

**Fix (tuikit):** in password mode, `SelectedText()` returns `""`. `Cut()`
returns `""` and deletes nothing (desktop password boxes refuse both).
Word moves and word deletes treat the whole value as one word.
**Fix (app):** `copySelection`/`cutSelection` must not overwrite the clipboard
with `""`.
**Tests:** `TestPasswordFieldRefusesCopy`, `TestPasswordFieldWordJumpIsWholeField`.
**Drive:** Connect dialog → password → Ctrl+A, Ctrl+C → paste into an editor.

### B2. A drag that starts inside a dialog fires a button when it reaches the button row *(confirmed)* — **DONE 2026-11-12**

`ModalDialog.ButtonClickedAt`'s `mouseDragging` latch only stops repeat
events from the press that armed it. A press in an AlertDialog's message area,
dragged onto `[ OK ]`, closed the dialog (probe). The same thing happens on
every dialog without its own gesture router: a FileDialog list drag, a
PropertiesDialog scrollbar drag onto `[ Close ]`, a Confirm checkbox press.
`PropertySheet` is safe (`dragZone`), and so are fields
(`FieldGesture.Replay`). ARCHITECTURE.md § The mouseDragging idiom says a
router must "tell fresh from continuation". The dialog is a router for its
children, but it never does.

**Fix:** `ConsumeOutsideClick`, which every dialog calls first, records whether
a press is held. `ButtonClickedAt` fires only on the *first* Button1 of a
gesture. `Show` already clears the latches, and the new one is cleared with
them. Done this way, it also covers S9 for dialogs.
**Tests:** a drag-in test for each dialog type in `dialogs`.
**Drive:** Alert (drag from text to OK), FileDialog (drag from list to Open).
**Done as:** `ModalDialog.TrackPress` (called by `ConsumeOutsideClick` and
`ButtonClicked`, a no-op on a repeat call for one event) runs the dialog's
`PressLatch` over every event. Backup, Restore, Filter and Options route an open
drop-down ahead of `ConsumeOutsideClick`, so they call `TrackPress` first (else
a list pick's resends still pressed a button). Driven: Connect (History → Cancel)
and FileDialog (list → Open). **Left over:** FileDialog's list still opens on a
second *separate* click on the selected entry, which is B4's bug (no
`ClickTracker`).

### B3. Word boundaries split grapheme clusters *(confirmed)* — **DONE 2026-11-12**

`core.WordBoundaryLeft/Right` and `WordBoundsAt` classify one rune at a time.
A combining mark (Mn/Mc/Me), ZWJ or variation selector is neither a letter nor
space, so it counts as punctuation. On `e`+U+0301, Ctrl+Right stopped at 1,
which is *inside* the cluster. That breaks the runecol rule (tuikit
README.md: the cursor "never lands there"). The Editor and InputField then
draw the caret mid-cluster, and Ctrl+Backspace leaves an orphaned mark.

**Fix:** treat marks, ZWJ and VS as word-continuing. Also snap each result to a
cluster boundary with `NextGrapheme`/`PrevGrapheme`, which also covers emoji
ZWJ sequences between punctuation.
**Tests:** table cases in `wordutil_test.go` (combining, ZWJ family, flag).
**Done as:** the three helpers step by cluster (`NextGrapheme`) and classify a
cluster by its first rune, so every result is a boundary without a separate
snap. A backward walk starts at the nearest boundary after whitespace
(`spaceAnchor`) rather than the line's start, so Ctrl+Left on a long non-ASCII
line does not segment the whole prefix. Tests: `TestWordBoundaryRightGraphemes`,
`TestWordBoundaryLeftGraphemes`, `TestWordBoundsAt` (combining, ZWJ, VS, emoji
modifier, family, flag, Cyrillic). Driven: Editor Ctrl+Right/Ctrl+Delete over
`cafe`+U+0301 and `e`+U+0301, Connect Server field Ctrl+Right.

### B4. ListBox activates on any second click on the selected row, however late *(confirmed)* — **DONE 2026-11-12**

`ListBox.HandleMouse` calls `OnActivate` whenever the clicked row is already
selected, with no timing check (two separate clicks → activate). In the
Connect dialog the history list starts with row 0 selected, so **one click on
the first history entry connects**. In PropertySheet a click on the current
page moves focus into the form. TreeView, Editor and DataGrid all require a
timed double-click.

**Fix:** use S1's click tracker. `OnActivate` fires only on a real
double-click.
**Drive:** Connect dialog: click the selected history row once, and it must
only select.

### B5. A right-click on a TreeView row moves the selection without `OnSelect` *(confirmed)* — **DONE 2026-11-12**

`TreeView.HandleMouse`'s Button2 branch sets `tv.sel = idx` and does not call
`fireSelect` (probe: selection moved to node 2, 0 callbacks). Object Explorer's
`handleSelect`, the breadcrumb and anything else that follows the selection
keep describing the previous node while the highlight shows the new one.

**Fix:** when the row changes, call `fireSelect` before `OnRightClick`. First
check that `handleSelect` loads nothing a right-click must not trigger.
**Drive:** select A, right-click B, Escape. The status/breadcrumb must name B.
**Done as:** the Button2 branch fires `OnSelect` only when the row changes (a
held right button resends Button2). `handleSelect` → `onNodeSelected` sets the
status, refreshes Details and primes the capability cache, which the menu's
gates want anyway. Test: `TestTreeViewRightClickFiresSelectBeforeMenu`.
Driven on win10cli: Databases selected, right-click HealthClinic → status and
Details name HealthClinic with the menu open, and still after Escape.

### B6. `LocalFileSystem.Exists` drops errors that its interface says to report — **DONE 2026-11-12**

`FileSystem.Exists` says `err` means "couldn't ask", and the save-overwrite
guard treats that as "assume it exists" and prompts. `LocalFileSystem.Exists`
returns `(false, false, nil)` for *every* `os.Stat` error, so on EACCES or EIO
the overwrite prompt is skipped. **Fix:** return `err` unless it is
`errors.Is(err, fs.ErrNotExist)`. Low severity.
**Done as:** ENOTDIR (a file where a directory component should be) also
counts as "not there". Tests: `TestLocalFileSystemExistsMissingIsNotAnError`,
`TestLocalFileSystemExistsReportsPermissionError`. Driven: Save Query into a
mode-000 directory → the overwrite prompt appears.

### B7. A ListBox scrollbar drag stops at the list's edge — **DONE 2026-11-12**

`ListBox.HandleMouse` returns on `!l.rect.Contains` *before*
`core.HandleScrollbarDrag`. That breaks the latch contract (the drag keeps
control "even once the mouse drifts off"), so a fast drag past the bottom stops
short of the end. The same early return lets a held press's motion events fall
through to the host. ListBox text also runs under its own scrollbar column,
which DropDown reserves. **Fix:** handle latched gestures before the bounds
check, as TreeView does, and narrow the text by one column when the bar shows.
**Done as:** TreeView had the same early return, so it got the same fix (its
held *row* press still falls through: Object Explorer's node drag needs it).
The Connect dialog's `ConsumeOutsideClick` swallowed the drag past its box
before the list saw it, so `ConnectDialog.HandleMouse` now offers events
outside the box to the history list first (it refuses any it did not latch).
Tests: `TestListBoxScrollbarDragContinuesPastTheEdge`,
`TestListBoxHeldPressConsumesMotionOffTheList`,
`TestListBoxTextStopsShortOfTheScrollbar`,
`TestTreeViewScrollbarDragContinuesPastTheEdge`. Driven: Connect history with
40 entries, thumb dragged below the dialog → list reaches the last entry.

### B8. Splitter ratio can become NaN — **DONE 2026-11-12**

A drag divides by `rect.H`/`rect.W`. A resize to 0 during a drag, or
`SetRatio(NaN)` from a corrupt config, produces NaN, and `core.Clamp` returns
NaN because both of its comparisons are false. **Fix:** skip the drag math on
a zero dimension, and make `SetRatio` reject NaN/Inf. Low severity.
**Done as:** `SetRatio` ignores NaN/±Inf (keeps the current ratio).
Tests: `TestSplitterSetRatioIgnoresNaNAndInf`, `TestSplitterDragSurvivesZeroSize`.
Unit-level only: a zero-size resize mid-drag cannot be produced in a terminal.

### B9. `ClipScreen` does not clip `ShowCursor` — **DONE 2026-11-12**

In a clamped dialog, an Editor or InputField whose caret falls outside the clip
puts the terminal cursor outside the box. **Fix:** hide the cursor when it
falls outside the clip. Low severity.
**Done as:** `ClipScreen.ShowCursor` calls `HideCursor` outside the clip.
Test: `TestClipScreenHidesCursorOutsideTheClip`. Unit-level only.

---

## C — Inconsistencies

- **C1. Focus API.** *(DONE 2026-11-12, step 11: `Focus(bool)`/`Focused() bool`
  on every focusable control; TreeView, Editor and Splitter lost `SetActive`;
  `PanelManager.SetActiveIndex`; `propsheet.Form.Focused` is now the form's
  bool and `FocusedRow` the row. App-side `SetActive(bool)` remains only on
  Activatable panels and the app's own `planview.PlanView`.)* Widgets, ListBox, DataGrid and Editor use `Focus(bool)`.
  TreeView uses `SetActive(bool)`, and Editor has both. `PanelManager.SetActive(i int)`
  has the same name as `layout.Activatable.SetActive(bool)`, which means a
  PanelManager can never be an Activatable panel. Nothing has a `Focused()`
  getter. → One spelling everywhere, `Focus(bool)`/`Focused() bool`.
  `SetActive(bool)` stays only as Activatable. Rename
  `PanelManager.SetActive` → `SetActiveIndex` (it already has `ActiveIndex`).
- **C2. Change notification.** *(DONE 2026-11-12: an `OnChange` field on
  InputField (key, Cut, Paste), CheckBox, RadioBox, DropDown, never fired by a
  setter. propsheet's Text/Check/Select/Radio rows hear user edits through it
  (`notifyChanged` stays for Edit/Revert); LogViewer's filter re-filters
  through it, so a paste into the box now re-filters too. Connect and Profile
  keep their per-widget key routing: each runs a different follow-up.)* Button, ListBox and TreeView have callbacks.
  InputField, CheckBox, RadioBox and DropDown have none, so every host compares
  values after each key. → Add `OnChange`, fired by user edits only, never by
  setters.
- **C3.** *(DONE 2026-11-12: `core.Overlay` = `DrawOverlay` + `OverlayActive`
  on DataGrid, Editor, DropDown, MenuBar, ContextMenu (`Draw` →
  `DrawOverlay`) and propsheet.Form (`DrawOverlays` → `DrawOverlay`); Toolbar
  and TreeView's hover hints implement it with `OverlayActive` always false.
  propsheet's `OverlayDrawer`/`OverlayActiver`/`KeyHandler`/`MouseHandler`
  are gone in favour of core's.)* **The overlay contract is spelled five ways:** `DataGrid.OverlayActive`,
  `Editor.CompletionActive`, `DropDown.IsOpen`, `MenuBar.IsOpen`,
  `ContextMenu.Visible`. propsheet declares its own
  `OverlayDrawer`/`OverlayActiver` on top. → One `core` interface
  (`DrawOverlay` + `OverlayActive`), implemented by every control that owns an
  overlay. propsheet reuses it (A3).
- **C4. Modal mouse return values.** *(DONE 2026-11-12: Prompt and
  TypedConfirm now return `true` for every non-Button1 event; the contract is
  in `dialogs/doc.go`. Test: `TestAVisibleModalConsumesEveryMouseEvent`, over
  all seven tuikit dialogs. No visible change today: App ignores the return.)*
  Alert and Confirm return `true` for every
  event while visible. `PromptDialog` returns `false` for non-Button1 events
  (wheel, right-click), which leaves its focus trap leaky. → A visible modal
  always returns `true`.
- **C5. Four double-click detectors** *(DONE 2026-11-12, via S1)* (Editor, TreeView, DataGrid separator,
  DataGrid row) with two constants (`doubleClickInterval`,
  `resizeDoubleClickInterval`, both 500 ms), each reading the wall clock
  directly. ListBox has none (B4). → S1.
- **C6.** *(DONE 2026-11-12, step 12: buttons are `sheetButton` IDs;
  `activateButton` and the applying greying dispatch on the ID, the text comes
  from `propsheet.Strings`. Test: `TestRenamedButtonsKeepTheirActions`.)*
  **PropertySheet buttons dispatch on label text.** `activateButton` and
  `sheet_draw.go` switch on `"Cancel"`, `"Close"`, `"Script Changes"` and
  `"Script"`, so renaming a label changes behavior, and localization is
  impossible. → Give buttons IDs, and keep the labels for display only.
- **C7.** *(DONE 2026-11-12: `Bounds() core.Rect` on every control, the drawn
  extent for one-row widgets (label through closing bracket; RadioBox all its
  rows); `RectX`/`RectY` dropped, the app reads `Bounds()`. `InputField.InputX`
  stays: it is where the editable box starts, not a position. CheckBox gained
  `Label`. Test: `TestWidgetBoundsMatchWhatIsDrawn`.)* **Accessor sets differ.** CheckBox has `RectX/RectY` but no `Label`.
  InputField has `RectX/RectY/InputX`. DropDown has `RectX` only. Button has
  `Label` only. `Bounds()` exists only on DataGrid and Editor. → `Bounds() core.Rect`
  on every control. Keep `RectX/RectY` until the app no longer uses them, then
  drop them.
- **C8.** *(DONE 2026-11-12, step 12: `PanelManager.EmptyText` (default
  "(no panels open)") and `ProgressDialog.ElapsedText`/`CancellingText`
  (default "…the operation to stop"); the app sets its Ctrl+N and "server"
  wording. Generic text is an exported `Strings` struct in `controls` (DataGrid
  status, Copy menu, viewer hint/Close), `dialogs` (every button label) and
  `propsheet` (buttons, hints, Loading/Error/Retry, Applying/Cancelling,
  Reverted). `defaultHints` stayed generic: F5 and Ctrl+Z are the sheet's own
  bindings. Dialog titles and FileDialog column headers remain hard-coded.)*
  **Application text inside the library.** PanelManager's empty bar shows
  `"(no panels open — Ctrl+N for a new query)"`. ProgressDialog shows
  `"Cancelling — waiting for the server to stop"`. propsheet's `defaultHints`
  names F5/Ctrl+Z bindings. The rest is generic but hard-coded English:
  DataGrid `"Ready"`, `"Error"`, the Copy menu labels and the viewer hint, and
  the dialogs' button labels. → The app-specific strings become
  fields/setters (`PanelManager.EmptyText`, `ProgressDialog.CancellingText`,
  `PropertySheet.SetHints` already exists). The generic ones become exported
  per-package defaults that a host can override.
- **C9. Clipboard on read-only targets.** *(DONE 2026-11-12: `DataGrid.Cut`,
  `PropertySheet.Cut` (a non-field row, or a form locked for Apply) and the
  app's `PlanView.Cut` return `""`; `core.ClipboardTarget`'s doc states the
  rule. `DeviceCodeDialog.Cut` still copies its code, deliberately (its comment
  says so). Tests: `TestDataGridViewerCutReturnsNothing`,
  `TestSheetCutOfAStaticRowCutsNothing`, planview's XML-tab test. Driven:
  results Show Value → Ctrl+A, Ctrl+X → no status, text kept; Ctrl+C copies.)*
  `DataGrid.Cut` returns the text
  without removing it. The app then reports "Cut to clipboard" and fires
  `ClipboardEdited`. → `Cut()` on a read-only target returns `""`, and the app
  treats `""` as "nothing cut" (this is the same app change as B1).
- **C10. Comment drift that the move would leave dangling.** *(Comment-only
  part DONE 2026-11-12: the app/`docs/`/ARCHITECTURE.md citations in tuikit
  comments, tests included, are rewritten in library terms; `PollEvent` →
  `EventQ`; `layout/doc.go` lists `Disposable`; `DrawText` says the mark is
  dropped; the completion header no longer claims Editor knows no SQL. Old
  review-plan labels (K1, K10, X6) dropped from test comments. Left for A1:
  `sqltext` and `sql_statement_test.go`, which cite `internal/tui/sqlparse`
  and move into gossms with it.)* About 25 tuikit
  comments cite `internal/tui/*.go`, `App.*`, `QueryPanel`, `ObjectExplorer`,
  `docs/ui-rules.md`, `ARCHITECTURE.md` or "review plan K1". The Canvas doc
  names `PollEvent` (tcell v3 has none). `layout/doc.go` omits `Disposable`.
  The header of `editor_completion.go` says Editor "knows nothing about SQL"
  while it calls `sqltext.WordStart` and hard-codes T-SQL's `@`/`#` sigils
  (A1). `core.DrawText`'s comment says a zero-width grapheme is attached to the
  previous cell "when possible", but the code always drops it. → Rewrite in
  library terms, and keep the bug-history *why* by citing tuikit tests
  instead of app files (CLAUDE.md: those comments name shipped bugs, so don't
  strip them).
- **C11.** *(DONE 2026-11-12, step 12: `layout.NewSplitter(dir, ratio)`
  (limits 0.1–0.9), `SetLimits` (re-clamps; nonsense ignored), `SetLabel`. The
  two old constructors moved to the app as `newHorizontalSplitter`/
  `newVerticalSplitter` in `internal/tui/app.go`; planview calls `NewSplitter`
  directly. Test: `TestSplitterLimits`.)* **Splitter limits are hard-coded per app layout** (0.5 within 0.1–0.9
  for "editor over results", 0.3 within 0.1–0.8 for "explorer | panels"), and
  there is no setter. → `NewSplitter(dir, ratio)` + `SetLimits(min, max)`.
  Keep the two constructors as thin wrappers in the app.
- **C12.** *(DONE 2026-11-12, step 12, docs only: `Active()` keeps its
  pointer (~130 per-frame readers, a large struct) and its doc says read-only,
  change it with `SetPalette`. Every package-level setting —
  `SetPalette`, `SetDefaultIndentWidth`, `ApplyingSpinner`, the `Strings`
  vars — documents "UI goroutine", listed in tuikit's `README.md`
  § Design principles. "Before the first Draw" is the advice, not a rule:
  Options changes the palette and indent width at runtime, between frames.)*
  **Global state.** `theme.Active()` returns a pointer to the live
  global, so any caller can modify it. Other globals: `controls.SetDefaultIndentWidth`,
  `propsheet.ApplyingSpinner` (an exported var). → `Active()` returns a
  value, or its docs say read-only. Document all of them as "UI goroutine,
  before the first Draw".

## O — Optimizations

No hot-path finding is worth a change. The draw path is already tuned
(`RuneString`, `DrawTextLine`, Document version caches, `FillRect`'s single
`FillArea`), and `frame_bench_test.go`/`editor_bench_test.go` cover it.
Optional, and only with a benchmark showing a gain:

- **O1.** A password `InputField.displayRunes` builds
  `[]rune(strings.Repeat("*", n))` 3–4 times per keystroke (Draw, the two
  grapheme steps, `adjustScroll`). Caching it by length would remove that.
- **O2.** `PanelManager.tabSegments` allocates `[][]int` and `[][]TabSegment`
  on every Draw *and* every mouse event. It could cache by panel
  count/titles/width.

## S — Simplifications

- **S1. `core.ClickTracker`** *(DONE 2026-11-12: times come from `ev.When()`, which tests stamp with `SetEventTime`, instead of an injected `Now`)*, one double-click recognizer: interval, position
  key, and an injectable `Now func() time.Time` for tests. It replaces C5's
  four detectors and the two constants, and fixes B4.
- **S2. One test screen instead of ~18.** *(DONE 2026-11-12: `charts.Canvas`
  is `core.Buffer` (`core/buffer.go`), now with `PutStr`/`PutStrStyled`, a
  recorded cursor (`ShowCursor`/`HideCursor`/`Cursor`), `RuneAt`/`StyleAt`
  and `Text`. New `tuikittest` package: `Screen` (a Buffer plus
  `Find`/`RowOf`/`Snapshot`/`SetSize`) and `Key`/`Rune`/`Mouse`/`Press`/
  `Release`. Every cell-recording fake in tuikit and the app's
  `fakeSizedScreen`/`recordingScreen`/`rowRecordingScreen`/`overlayScreen`
  are gone, as are the per-package key/mouse helpers in tuikit; core's tests
  use a Buffer (they cannot import tuikittest). `FillAreaByPut` is deleted.
  Kept on purpose: `putLog`/`quietScreen` (core: call-sequence and
  allocation spies), `discardScreen` (editor benchmarks: a sink),
  `osc52Screen`/`eventQScreen` (app: clipboard and EventQ, not drawing),
  tcell's vt mock in `editor_grapheme_test.go`. Two expectations changed
  because the Buffer behaves like tcell: a wide glyph's continuation cell
  carries its style, and a combining mark reads back with its base.)*
  tuikit tests declare about 18
  bespoke fake/recording `tcell.Screen` types (internal/tui has more), and
  `core.FillAreaByPut` is a production export that exists only to serve them.
  → Move `charts.Canvas` to `core` (name it e.g. `core.Buffer`). Complete it:
  `PutStr`, `PutStrStyled`, and a recorded `ShowCursor`, which also stops
  Editor from panicking on Canvas's nil embedded Screen. Add a `tuikittest`
  package with a Buffer-backed screen, key/mouse event builders and row
  snapshots. Migrate one package at a time, then delete `FillAreaByPut`.
- **S3. DataGrid's exported/unexported twins** *(DONE 2026-11-12: the
  exported form of each is the only one; doc comments merged.)* (`SelectionBounds`/`selectionBounds`,
  `SelectedRows`, `SelectedRowCount`, `SelectedCellsText`). Keep one of each.
- **S4.** *(DONE 2026-11-12: `ClipboardRow` is `Copyable` +
  `core.ClipboardTarget`.)* `propsheet.ClipboardRow` re-declares `core.ClipboardTarget`'s five
  methods. → Embed `core.ClipboardTarget` and `Copyable`.
- **S5. Style helpers.** *(DONE 2026-11-12: `theme.On(bg, fg)` plus
  `StyleDialogDim/Line/Accent/Error`, `StyleMenuDim/Line`, `StyleActiveBlock`
  (the caret, active tab, dragged splitter) and `StyleScrollThumb`. Every
  inline pair in tuikit production code is now a named helper where an exact
  one exists (Bold-carrying helpers only where the source was bold), else
  `On`. `StyleInputDisabled` is gone. The new helpers are in `styleFuncs`;
  nine pairs below the contrast floor, drawn the same before, are pinned in
  `knownLowContrast` and logged in `docs/open-threads.md` § Theme. Rule in
  `docs/ui-rules.md`. `internal/tui` not migrated. Driven: Connect dialog and
  query editor colours match the palette.)* There are 99 inline
  `tcell.StyleDefault.Background(p.X).Foreground(p.Y)` constructions. 14 of
  them are exactly `theme.StyleDialog()`, and most of the rest repeat about 8
  pairs (DialogTextDim, DialogLine, DialogAccent, MenuText/Dim/Line, the
  BorderActive caret and thumb). → Add named helpers for the repeated pairs and
  `theme.On(bg, fg)` for the rest. Fold `StyleInputDisabled` into
  `StyleControlDisabled`.
- **S6.** *(DONE 2026-11-12, step 13: `controls.ValueViewer`
  (`value_viewer.go`), exported, zero value ready: `Show(title, text)`,
  `Close`, `Title`, `Text`, `core.Overlay`/`KeyHandler`/`MouseHandler`/
  `ClipboardTarget`; the dismiss latch is a `core.PressLatch`. DataGrid holds
  one (`viewer`) and forwards draw, input and clipboard to it. Test:
  `TestValueViewerStandalone`; the grid's viewer tests are unchanged in
  substance. Driven: Show Value on a long cell, Shift+Down + Ctrl+C copies,
  Ctrl+X reports nothing, Escape returns to the grid. The Close-button click
  was not driven: unit tests only.)*
  **DataGrid's "Show Value" popup re-implements a modal** (dim, box, close
  button, hint, latch) inside `datagrid_overlay.go`. → Extract it into a
  `ValueViewer` overlay type with its own file, and have DataGrid hold one.
- **S7. One fresh-press latch.** *(DONE 2026-11-12: `core.PressLatch` with
  `Release`/`Fresh`/`Held`/`Clear`, in all of these plus ContextMenu and
  FileDialog's list. Editor, DataGrid and InputField keep `mouseDragging`:
  theirs also drives a selection drag. `internal/tui` latches are not migrated.)* The pattern "clear on ButtonNone; act only if
  not latched; latch" is hand-written in Button, CheckBox, RadioBox, DropDown,
  ListBox, TreeView, MenuBar, Toolbar, PanelManager, ModalDialog and Splitter.
  → `core.PressLatch` (`Fresh(ev) bool`, `Release(ev)`). Do it with B2.
- **S8 (consider).** `dialogs.PropertiesDialog`, a read-only key/value list,
  overlaps with a single-page read-only `propsheet` Form. Merge them only if
  the app's PropertiesDialog uses survive the change. Otherwise leave it.

Considered and left alone: `Truncate`/`TruncateLine`/`DrawTextLine` share an
ellipsis rule in three bodies, but each is tuned for its caller (allocation-free
for the grid), and they are pinned by tests.

## A — Architecture: what blocks extraction

- **A1.** *(DONE 2026-11-12, step 15: `sqltext` is `internal/sqltext`;
  `TestNoTUIImports` has no exception left. `controls` gained
  `LineStateAt` (a per-Document line-state cache keyed by owner),
  `Document.Lines`, `Editor.SelectRange`, `SetIndentRule` (replaces
  `SetSmartIndent`), `SetWordStart`/`DefaultWordStart` and
  `SetCompletionTrigger` (default: letter or '_'). The highlighter,
  `LineState`, `SelectStatementAtCursor`, `IndentRule`, `CompletionTrigger`
  and `Configure` are `internal/tui/sqledit`; their tests moved with them, and
  `controls`' cache tests and benchmarks run on a toy lexer
  (`testlang_test.go`). Driven: highlighting across an open comment, smart
  indent, Execute at Cursor, the `[` and word completion triggers (same as a
  HEAD build), Format Document refusing a selection inside a comment. Left in
  tuikit: `ToggleLineComments` still hard-codes the `--` prefix.)*
  **Pull T-SQL out of tuikit.** It is the largest blocker.
  - `sqltext` moves to gossms as `internal/sqltext`. It remains the one lexer
    (decisions § T-SQL lexing). ARCHITECTURE.md's CLI invariant becomes
    "no tuikit at all", and `TestNoTUIImports` drops its sqltext exception.
  - In `controls`, the SQL-specific parts are `SQLHighlighter`,
    `sql_statement.go`, `Editor.SQLLineState`, `Document.sqlStarts`,
    `sqlIndentKeywords`/`SetSmartIndent`, and completion's `sqltext.WordStart`
    + `@`/`#` sigils. Replace them with generic hooks:
    - export the generic `prefixStates` as a per-Document line-state cache,
      keyed by owner, so a highlighter outside `controls` gets the same
      once-per-edit lexing (XMLHighlighter already uses it);
    - add Editor options `SetWordStart(func(line []rune, col int) int)`,
      `SetCompletionTrigger(func(line []rune, col int) bool)` (default:
      letter/underscore), and `SetIndentRule(func(left string) int)` in place
      of `SetSmartIndent`;
    - add a read-only line accessor, so StatementAt can run against an Editor
      from outside the package.
  - The SQL pieces go to gossms (for example `internal/tui/sqledit`, next to
    `sqlparse`). XML/JSON highlighters are language-neutral and stay in tuikit.
- **A2.** *(DONE 2026-11-12, step 14: `dialogs.Stack` (`dialogs/stack.go`)
  registers dialogs and hosts the open ones: Sync, Top, Draw (one ClipScreen,
  reset per dialog), Relayout, HandleKey/HandleMouse (release to every open
  dialog), ClipboardTarget/ClipboardEdited. App holds one as `modals`; its
  `allDialogs`/`dialogStack` and the sync/top/draw/relayout helpers are gone,
  and it keeps only the gesture-overlay snapshot. `DrawBase` now resets a
  ClipScreen's clip before narrowing it; README states the contract. Tests:
  `dialogs/stack_test.go`, incl. `TestADialogDoesNotInheritAnotherDialogsClip`
  (fails without the reset). Driven: nested Connection Error over Connect,
  both clamped at 60x16.)* **The dialog clip protocol is half in the app.** `ModalDialog.DrawBase`
  narrows a `ClipScreen`, but only `internal/tui/dialog_stack.go` creates that
  ClipScreen and resets it per dialog. A library user who draws a clamped
  dialog without that wrapper gets unclipped content. Two dialogs drawn on one
  ClipScreen inherit each other's clip. → At minimum, `DrawBase` resets the
  clip when the dialog is not clamped, and the README states the contract.
  Better: a `dialogs.Stack` (draw/relayout/route/clipboard for the frontmost
  dialog) that App's stack wraps. App keeps its gesture-overlay snapshot.
- **A3.** *(DONE 2026-11-12: `core/control.go` — `Focusable`, `Enabler`,
  `Bounded`, `KeyHandler`, `MouseHandler`, `Overlay`; each package's
  `control_test.go` pins which control implements which. The app's
  `focusable` ring type is `core.Focusable`; Connect's enabled check and
  Find/Replace's key dispatch assert on `core.Enabler`/`core.KeyHandler`.
  `forwardReleaseToFocusedField` keeps its type switch on purpose: it must
  skip checkboxes and drop-downs.)* **Shared control interfaces in `core`:** `Focusable` (C1), `Overlay`
  (C3), `Enabler`, plus key/mouse handler interfaces. propsheet's capability
  interfaces and any focus ring are built on them, not on per-type switches.
- **A4.** *(DONE 2026-11-12, step 16: `tabstrip.go` is in `core`, so
  `layout` is a leaf over core/theme. The editor split is done too:
  `controls/editor` holds Editor, Document, LineBuffer, undo, search,
  completion and the XML/JSON highlighters (with their tests and the editor
  frame benchmark); it imports only core/theme, and `controls` reaches it only
  through ValueViewer. App call sites now say `editor.Editor` etc.
  `TestTuikitLayering` (`core/imports_test.go`) pins the whole intra-tuikit
  graph, README § Dependency direction; probed with a planted
  layout → controls import. Driven: SQL highlighting across a multi-line
  comment, completion popup, Connect's tab strip (Ctrl+PgUp/PgDn), Results/
  Messages tabs, Show Value from the grid's context menu, PanelManager tab
  clicks.)* **Layering inside the new module.** `layout` imports `controls` only for
  `TabStripSegments`/`TabLabelWidth`. Moving `tabstrip.go` to `core` makes
  `layout` a leaf over core/theme. Optional, and done before the first tag:
  split `controls` (10k lines) into `controls` (grid, tree, list, menus,
  toolbar) and `controls/editor` (Editor, Document, undo, search, completion,
  highlighters).
- **A5. Enforce the dependency invariant with a test.** *(DONE 2026-11-12:
  `TestTuikitImportsOnlyItsDeclaredDependencies` in
  `internal/tuikit/core/imports_test.go` lists `../...`, so it runs from
  whichever module tuikit lives in. Production imports: stdlib, tuikit,
  tcell/v3, tcell/v3/color, displaywidth only. Test imports: anything but
  gosmo or the host module. Probed with a planted import of each kind.)*
  ARCHITECTURE.md
  states it ("must print exactly tcell/v3, tcell/v3/color, displaywidth"), but
  no test runs it. Add one inside tuikit, so it moves with the module, and
  include "no tuikit package imports `internal/…`".
- **A6. Docs that must travel.** *(DONE 2026-11-12, step 17: the README is
  the module README, in library terms (install, imports, Development). The
  library half of `docs/ui-rules.md` and of ARCHITECTURE.md § The
  mouseDragging idiom/§ dialogs.FieldGesture is tuikit's own
  `ARCHITECTURE.md` (Threading, Mouse gestures, Overlays, Dialogs, Widgets and
  grids, Editor), which owns it; gossms's copies now say how goSSMS applies
  it and point there. tuikit also has a short `CLAUDE.md`. The overlay
  citations in plan_panel/live_plan_panel/query_panel_draw/planview point at
  tuikit's § Overlays.)* The tuikit README becomes the
  module README. Bring along the tuikit-only parts of `docs/ui-rules.md` and
  ARCHITECTURE.md (§ The mouseDragging idiom, overlays drawn last,
  postAndWake-free async state) so that C10's rewritten comments have
  something to cite.

## X — The move itself (after A1–A6)

*(DONE 2026-11-12, step 17: module `github.com/radix29/tuikit`, fresh
history, in the `~/go/tuikit` checkout the user had created (its placeholder
README replaced; LICENSE and .gitignore kept). gossms `require`s it at the
`v0.0.0-…` placeholder with `replace … => ../tuikit`; `internal/tuikit` is
deleted (a tarball of it is in the session scratchpad). The
`dev-with-local-gosmo` skill gained a tuikit section rather than a sibling
skill. Source-reading tests (`gestureSourceDirs`, `gridDrawPackages`) resolve
tuikit through `go list`; `TestNoTUIImports` names `radix29/tuikit` (the
`/internal/tui` match no longer covers it); `TestNoDanglingDocReference`
resolves `~/go/tuikit/…` against the sibling, and its suffix-match case still
earns its place (`docs/cli.md`'s `guide/*.md`). Also fixed: FuzzFormatSQL's
seed corpus glob still pointed at the pre-A1 `tuikit/sqltext` path and seeded
nothing. About shows tuikit's module version (`version.Dependency`). Nothing
committed, tagged or pushed: tuikit needs a first commit and tag before a
gossms release can drop the `replace`.)*

The user decides the module path, the repo, and whether to keep history
(subtree split vs. fresh). Claude does not commit, tag or push.

1. Create the sibling repo (like `~/go/gosmo`, `~/go/gotuipic`) with a
   `go.mod` pinned to gossms's tcell/v3 and displaywidth versions. Copy the
   packages and their `testdata/`.
2. In gossms: `require` the module, add `replace … => ../tuikit` (the
   convention gosmo uses; consider a sibling of the `dev-with-local-gosmo`
   skill), rewrite imports, then delete `internal/tuikit`.
3. Update CLAUDE.md (the `tuikit` rows and conventions), ARCHITECTURE.md
   (package map, invariants, "Which document owns what"), and
   `docs/testing.md`. In `doc_links_test.go`, check the README suffix-match
   case still means something. `go build`, `go test -race ./...`,
   `staticcheck`, `deadcode -test`, then drive the binary per `docs/testing.md`.

---

## Recommended implementation order

Fix bugs on the current layout first. Each fix is small, tests stay where they
are, and any regression is easy to bisect. Then add the shared primitives the
refactors build on. Then the API changes, which touch the app's call sites.
The structural moves come last.

| # | Item(s) | Why here | Size |
|---|---|---|---|
| 1 | ~~**B1** password copy + word jump~~ **DONE** | security; tuikit + 2 app lines | S |
| 2 | ~~**S1 → B4, C5** ClickTracker, ListBox double-click~~ **DONE** | B4 is user-visible (one click connects) | S |
| 3 | ~~**S7 → B2** PressLatch, dialog fresh-press~~ **DONE** | one primitive, many dialogs; drive each | M |
| 4 | ~~**B5** tree right-click selection~~ **DONE** | check `handleSelect` first | S |
| 5 | ~~**B3** grapheme-safe word boundaries~~ **DONE** | core only, table tests | S |
| 6 | ~~**B7, B8, B9, B6** small correctness fixes~~ **DONE** | independent, low risk | S |
| 7 | ~~**C4, C9, C10 (comment-only part)**~~ **DONE** | no API change | S |
| 8 | ~~**A5** dependency-invariant test~~ **DONE** | guards everything after it | S |
| 9 | ~~**S2** core.Buffer + tuikittest, migrate tests per package~~ **DONE** | later refactors get cheap tests | M–L |
| 10 | ~~**S3, S4, S5** mechanical cleanups~~ **DONE** | no behavior change; diff-review | M |
| 11 | ~~**C1, C2, C3, C7 + A3** interfaces and API normalization~~ **DONE** | touches app call sites; one control family per step | L |
| 12 | ~~**C6, C8, C11, C12** IDs, strings, splitter limits, globals~~ **DONE** | API-visible; app wires defaults | M |
| 13 | ~~**S6** ValueViewer extraction~~ **DONE** | after C3 (it is an Overlay) | M |
| 14 | ~~**A2** dialog clip/stack~~ **DONE** | after C4/B2 settle dialog routing | M |
| 15 | ~~**A1** T-SQL out of tuikit~~ **DONE** | largest; after 9 for test cheapness | L |
| 16 | ~~**A4** tabstrip to core; optional editor split~~ **DONE** | import churn, so do it last before the move | M |
| 17 | ~~**A6, X** docs and the move~~ **DONE** | only when 1–16 are green and driven | M |

O1/O2 and S8 are optional, and none of the steps above depends on them.
