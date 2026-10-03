# UI rules

Rules for `internal/tui` and `internal/tuikit` that the code doesn't make
obvious — each is a bug that shipped. `ARCHITECTURE.md` has the reasoning
behind the mouse and async rules.

## Editor, widgets and grids

- **Seed `QueryPanel.savedText` through `qp.markSaved()`, never from the string
  passed to `SetText` and never by assigning it** — `markSaved` reads
  `editor.Text()` and drops `Dirty`'s per-version cache, which a direct
  assignment leaves answering for the old baseline. `SetText` normalizes (tabs, CRLF, invalid UTF-8 →
  U+FFFD), so seeding from the source marks the panel dirty on open and the
  prompted save rewrites the file — `File > Open` once turned a UTF-16 script
  into U+FFFD-laden UTF-8. Text files go through `decodeTextFile`/
  `encodeTextFile` (`text_encoding.go`): encoding **from a BOM only** (never
  guessed), and the *majority* line ending preserved (`majorityCRLF`) — the
  editor folds CRLF on load, and testing mere CRLF presence rewrote a mostly-LF
  file over one stray CR.
- **Auto-indent lives in the `KeyEnter` branch of `editor_input.go`, never in
  `insertNewline`** — `Paste`/`blockPaste` call `insertNewline` per line, so
  indenting there staircases a paste. The branch reads `blockEditing()`
  *before* `deleteSelection` (which clears it), and the spaces go under the
  existing `pushUndoLocal` so one Ctrl+Z undoes the Enter. `SetSmartIndent`
  adds a level when the text left of the cursor ends with `(` or has
  `select`/`from`/`where` as its **last token** (not "line starts with", which
  drifts right per clause); on only for T-SQL editors.
- **Call `Editor.SetIndentWidth` at construction, before any `SetText`** —
  `SetText` expands tabs at the current width and `savedText` is seeded after,
  so a later width change reads as an edit. A runtime change (Options > OK)
  doesn't re-expand text; it pushes the width to every open `QueryPanel`, every
  property sheet's `EditorRow`s (`PropertySheet.SetEditorIndentWidth` — the
  Agent job-step Command box is the one writable non-`QueryPanel` editor), and
  `controls.SetDefaultIndentWidth` (for read-only T-SQL rows built without
  config). A writable editor outside a `QueryPanel` takes `cfg.IndentWidth` at
  construction, from its caller.
- **`HandleKey`/`HandleMouse` return `true` only for events acted on** — never
  "focused, so consumed". `propsheet.Form` falls back to its own Tab/Up/Down
  only on `false`, so an always-`true` widget is a keyboard trap. Check Up/Down
  at list boundaries and Escape with nothing open. **A widget fine standalone
  can trap as a form row; the wrapper translates.** `DataGrid` answers `true`
  to every arrow, which made all 21 `NewGridRow` pages swallow Down/Up at the
  edges and Left; `propsheet.GridRow` snapshots `SelectedCell`/`ScrollCol`
  around the key and reports what moved (changing `DataGrid` would break
  QueryPanel, DetailBrowser, Activity Monitor). Detect movement, never predict
  it — a cursor-less grid scrolls without changing `SelectedCell`.
- **Never `DataGrid.SetData` on a grid being navigated — use `redrawGrid`
  (`prop_grid_helpers.go`), or `resetGrid(grid, headers, rows, row)` when the
  row set changed.** `SetData` drops cursor, scroll and dragged column widths.
  Inside the grid's own `OnSelectRow` it undoes the move, `GridRow` reports
  "not handled" and focus leaves on the first arrow (six sites shipped, rows
  unselectable). Restoring only the cursor isn't enough: `ensureVisible`
  scrolls from zero and pins the row to the bottom. `redrawGrid` wraps
  `SetDataPreservingView` (widths, scroll, selection); outside `internal/tui`
  call that directly (`propsheet.ToggleGridRow`) — never hand-roll the pair.
  After Add/Remove/Revert use `resetGrid`, not `SetData` + `SetSelectedRow`
  (which silently drops widths). Packaged idioms: `wireGridEditor`
  (`ag_props.go`) for grid + detail editor; `wireCellToggle`/
  `newCellToggleGrid` for "activate a cell to change the row"; `staticBlock`
  for a read-only detail block — its `set()` clears every row not given, so a
  missed row can't keep describing the previous selection.
- **A read-only page's grids browse, so their `OnSelectRow` runs there too.**
  `Form.SetReadOnly` leaves grids focusable and browse-only; everything else is
  inert, and the form reports clean whatever its rows say. An `OnSelectRow`
  may fill detail rows (with `SetValue`, never `Edit`) and commit the previous
  row's unchanged values back — but must never open, write or dirty anything
  else on the strength of a selection.
- **Don't rely on a mouse modifier — Shift especially.** VTE terminals
  (xfce4-terminal, GNOME Terminal) take Shift+mouse for their own selection and
  forward nothing. So a Shift+mouse gesture needs a second modifier
  (`extendSelectionMods` in `datagrid_input.go` is `ModShift | ModAlt`) and a
  keyboard route. Key Diagnostics logs mouse events and tells a swallowed
  modifier from a bad binding; tmux cannot (injected SGR bypasses the terminal).
- **`Ctrl+Shift+<letter>` never arrives as `KeyCtrlX`** — tcell folds to
  `KeyCtrlA..Z` only when Ctrl is the sole modifier, so it arrives as `KeyRune`
  with `ModCtrl|ModShift` (Kitty/xterm modifyOtherKeys) or a plain control
  byte (legacy). `normalizeCtrlRune` (top of `handleKey`, `app_events.go`)
  folds it back, on modern protocols only — anything that must work everywhere
  also needs a function key or Ctrl+non-letter (Connect has F9). Verify by
  injecting encodings with `tmux send-keys -H` and reading Key Diagnostics.
- **Every menu item and toolbar button is context-gated** — never a no-op,
  wrong action or crash on an unmet precondition. Use `Enabled func() bool`;
  the reactive fallback is a guard plus `setStatus(...)` in existing wording
  ("Not connected — use File > Connect", "No active query panel"). A writing
  context-menu item (Delete, Enable, Start, Stop, …) also needs a permission
  `gate`; `TestEveryWriteMenuItemIsGated` enforces it, and its exemption list
  says why for deliberate exceptions.
- **A disabled control keeps its place in the focus ring.** `Button`,
  `CheckBox`, `RadioBox`, `InputField` share `SetEnabled`/`Enabled`: still
  drawn greyed (`theme.StyleControlDisabled`; `StyleButtonDisabled` for
  buttons), refusing input, but not removed — removal renumbers focus indexes.
- **A busy indicator is a function of elapsed time.** `widgets.Spinner` has no
  start time or goroutine: `Frame(elapsed)`/`FrameSince(start)`, redrawn from
  the host's clock. Don't add a goroutine.
- **`SetBounds` does nothing when the rect hasn't changed** — hosts lay out in
  `Draw`, every frame. A `ListBox` re-running `ensureVisible` there snapped the
  wheel back to the selection. Compare first; re-clamp only on real change.
  `DataGrid` included: a host that changes cells in place calls
  `RefreshColumnWidths`, and a setter that changes widths (`SetMaxCellWidth`,
  `SetRowNumbers`) defers its recompute to `Draw` — never rely on a relayout.
- **Draw a cell with `core.PutRune` or `Screen.Put`, never `SetContent`.**
  tcell's `SetContent` re-packs its rune into a string per call: two
  allocations per visible cell per frame before W24 (a full-screen grid went
  from 25,028 to 393 allocations a frame). `FillArea` has the same cost
  per cell, so `core.FillRect` puts one prebuilt string. A test screen fake
  that embeds a nil `tcell.Screen` needs a `Put` that forwards to its
  `SetContent`.
- **Setting a value programmatically drops the selection** — an anchor past a
  shorter new value paints blanks as selected. `SetValue` clears `selecting`
  and re-anchors on the caret; a new `SetXxx` does the same.
- **Pre-filled fields read from their start.** `SetValue` leaves the caret at
  the end, showing the tail — right for a typed path
  (`TestInputFieldSetValueLongValueShowsItsTail`), wrong for a picked value:
  call `InputField.ShowFromStart()` after (Connect's `PreFill` does, per field).

## Dialogs and the clipboard

- **A dialog's button row is reachable with Tab**, never only through a
  function key or the mouse. A propsheet dialog has `zoneButtons`; a dialog
  driving a flat `focusable` slice makes the row the stop past either end of
  its ring and hands keys to `buttonRowKey` while it is there (Connect, Back Up,
  Restore). F1 is Help; it once cycled these rows, and was the only way to
  reach Restore's OK from the keyboard.
- **A dialog-level scrollbar uses `ModalDialog.DrawContentScrollbar`, not
  `core.DrawScrollbar` at `Rect().Right()-1`.** On a too-small terminal content
  is clipped to `InnerRect` (`App.drawDialogs`' `core.ClipScreen`, narrowed by
  `DrawBase` when `clamped()`), and the border column is outside the clip. A
  child widget's own scrollbar needs nothing.
- **Key Diagnostics (`key_diagnostics_dialog.go`, Help menu) is a permanent
  feature**, not debug scaffolding — never trim it. It separates app bugs from
  terminal limits.
- **An open dialog owns the clipboard — `activeClipboardTarget` never falls past
  `topDialog()`.** Ctrl+C/X/V are handled centrally in `App.handleKey`; the
  target is the frontmost dialog's focused field via `core.ClipboardHost`. The
  old switch named 3 of 30 dialogs and fell through, so Ctrl+X in Find cut the
  editor behind it. Dialogs without text entry are deliberately not hosts. A
  new dialog with a text field implements `FocusedClipboardTarget`, returning an
  **explicit** nil on a miss (a typed nil is not a nil interface).
  `TestEveryDialogWithTextEntryIsAClipboardHost` catches a missing host, not a
  reintroduced fall-through. A dialog reacting to edits implements
  `core.ClipboardEditHandler` and checks the target it's handed. `App.pasteInto`
  drops text unless its target is still current (reads are async).
- **Every dialog is modal: a query window opened from one is unreachable
  until it closes.** `topDialog()` takes all input, so a button that hands the
  user T-SQL to edit and run (not Script Changes, which needs no edit) closes
  the dialog first, confirming when it is `Dirty()`. Resource Governor's New
  classifier... shipped leaving it open with "create it, then F5 here" —
  impossible (`rgNewClassifier`).
- **A tabbed dialog rebuilds its focus ring per tab** rather than skipping
  hidden controls in draw — hidden controls left in the ring take invisible
  keystrokes. Connect's `rebuildFocusable` does this per tab and per
  one/two-pane layout, carrying focus over when the widget is in both rings.
  `TestConnectDialogTabRingHoldsOnlyTheVisibleTab`.
- **A dialog sized from the terminal re-decides its layout in `Show` and
  `Relayout`**, not only the constructor — `ModalDialog.Relayout` recentres at
  the last requested size, so override it to recompute and `SetSize`.
  Hit-testing uses the same `layoutFields` as drawing, once per frame.

## Panels, toolbars and grid hosts

- **A toolbar cell that doesn't fit isn't drawn at all** — `layoutToolButtons`
  gives it a zero rect, neither painted nor clickable, so adding a cell can
  delete the last one (Query Store's filters removed `Refresh` below ~150
  columns, tests green). Check against the real pane width (Object Explorer
  takes ~60 columns).
- **Hosts acting on whole objects read `DataGrid.SelectedRows()`, never
  `SelectionBounds()`** — bounds are a rectangle, so Ctrl+click rows 1 and 3
  deletes row 2 too. `selectedRowObjects` (`detail_browser_ops.go`) is the
  example; bounds are for cell ranges (block copy).
- **A panel drawing a `DataGrid` also calls `grid.DrawOverlay(s)` after every
  grid** — the cell menu and value popup draw outside the grid rect. Without it
  the menu opens invisibly and swallows keys until Escape (shipped in Query
  Store).
- **A flattened cell is a rendering; "Show Value" gets the original.**
  `queryStoreOneLine` joins lines for the grid, and `OnShowValue` opens a
  *runnable* panel — a flattened `-- comment` swallows the rest of the query.
  `OnShowValue`'s first parameter is the **column**; the row is
  `grid.SelectedRow()`. `QueryStorePanel.showValue` uses `qsResultRow.queryText`;
  `DetailBrowser.showQueryStoreValue` (shared `[][]string` grid) re-reads by
  `Query ID` via `gosmo.QueryStoreQueryText`. Test the bound id with
  `fakeInstance.ReadArgs` — the fake answers every id alike.

## Mouse, overlays, and async UI

**Read `ARCHITECTURE.md` § The mouseDragging idiom before touching any
`HandleMouse`.** Invariants:

1. A gesture belongs to what claimed its first press until release —
   `App.gestureOwner`, `QueryPanel.dragZone`, `PropertySheet.dragZone`, each
   `armGesture`/`armDrag` + `routeGesture`/`routeDrag`.
2. `App` snapshots the modal layer (`gestureOverlay`/`overlaySnapshot`) and
   drops held events across a change.
3. A widget acting on `Button1` has a `mouseDragging` latch, set on press,
   cleared on `ButtonNone`.
4. A latch never survives into the next showing — `ModalDialog.Show()` clears
   both; `FieldGesture.Clear` also drops the field's own latch.
5. A host returning early from `HandleMouse` still forwards `ButtonNone` to
   latch-bearing children.

- **A release goes to every open dialog, not just the front one**
  (`App.routeRelease`). A dialog that opened a child from a button press never
  sees that release, so its next press is refused as a continuation (Connect's
  Delete confirmation shipped that way). Underlying dialogs only reset latches;
  acting needs `Button1`.
- **A dialog with a text field uses `dialogs.FieldGesture`, never a hand-rolled
  `dragField`**: `Release` above `ConsumeOutsideClick` and any mode switch,
  `Replay` after `ConsumeOutsideClick` and before hit-tests, `Clear` in `Show`
  (`ARCHITECTURE.md` § dialogs.FieldGesture has why). Enforced by
  `TestEveryDialogWithATextFieldOwnsAFieldGesture` and
  `TestFieldGestureCallsAreOrderedCorrectly` — both needed. Options, Prompt and
  TypedConfirm hand-rolled it wrong with all tests passing: a drag to the
  button row pressed the button, *accepting* a rename or a typed confirmation.
- **The release to the *focused* field goes through
  `forwardReleaseToFocusedField` (`dialog_common.go`)**, not a type switch — it
  covers the focused `*controls.Editor`, which `FieldGesture` doesn't track.
  (Its `InputField` arm is belt-and-braces; `FieldGesture.Clear` +
  `InputField.CancelMouseDrag` closed the one gap,
  `TestConnectDialogShowClearsTheFieldLatchTooNotJustTheGesture`.) The `!=
  Button1` early return stays at the call site — Connect reads the wheel in
  between. `TestFieldGestureCallsAreOrderedCorrectly` fails a gesture-owning
  dialog whose `HandleMouse` type-asserts to `InputField` or `Editor`.
- **Not every mouse event draws.** `App.Run` skips the frame after motion-only
  events while more are queued (a grid drag fell 2.2 s behind). State a motion
  handler depends on is set in the handler, never in `Draw`.
- **An overlay drawn last gets first refusal** of every event while open —
  `DataGrid.OverlayActive()` atop `QueryPanel.HandleKey`/`HandleMouse`; the
  focused row before positional routing in `propsheet.Form.HandleMouse`.
- **Report with `App.postAndWake(fn)`**, never `postEvent` + `wakeEventLoop` by
  hand (tree nodes stuck on "Loading..."). `ARCHITECTURE.md` § Async result delivery:
  postAndWake. The elapsed-timer tick is the one legit bare `wakeEventLoop()`.
- **An apply closure never writes page state.** `propApply` runs on the
  pipeline goroutine while page callbacks read the same variables on the UI
  goroutine, and it also runs under Script Changes. It only issues statements;
  a real Apply reloads the page (`InvalidateAll`), and a failed one reloads
  every page that reached the server, which is what puts baselines right — an
  apply never moves them. (AG Listener Properties cleared pending addresses in
  its apply; after Script Changes the next Apply sent nothing.)
  - A grid-plus-detail page copies its editor into the model with
    `form.SetCommit(commitCurrent)`, never by calling it in the apply: the
    host runs every form's hook on the UI goroutine before reading
    dirtiness (`PropDialog.runApply`/`runScript`, `newObjectDialog.runPipeline`).
  - A New-object dialog builds its request in `preflight` and the step
    captures it (`var req …; d.applyFns[0] = func(ctx) error { return
    d.create(ctx, req) }`), never `d.applyFns[0] = d.create` reading widgets.
    A result the run hands back (New Endpoint's per-instance script) goes
    through a destination the run puts in its context.
  - `TestApplyClosuresDoNotWritePageState` checks every
    `func(ctx context.Context) error` literal and method value, following
    captured closures, func fields, methods of captured values and package
    functions handed captured state. `runPageAction`/`runPageActionOnce` work
    closures and `commitRename` are exempt. Blind spot: a local aliasing page
    state (a range variable, `r := d.rows`) — without types a copy can't be
    told from a pointer.
- **An apply closure writes on the context it's handed, only through gosmo.**
  `runApplySteps` wraps it in `gosmo.WithStatementObserver`, which is how a
  failed Apply knows which pages reached the server (those reload; others keep
  edits; a New-object dialog whose first page completed counts as created).
  Writes on `d.ctx`, `sc.Server.Context()`, `context.Background()` or raw
  `database/sql` are invisible and get re-sent. Derived contexts (`WithTimeout`,
  `WithoutCancel`) keep the observer. Unseeable case: a gosmo disable window
  whose re-enable is refused — re-read and mark `applyCommitted` (Audit
  Properties).
- **A Properties dialog that can rename its object, or change a state its tree
  label or icon carries, opens with `showReloading`** and the matcher of the
  folder listing it (`folderOf`). The folder, not the node: its loader builds
  the label, and a renamed node keeps the old name every later menu action
  uses. `TestPropertiesApplyReloadsTheFolderListingTheObject` lists them; a new
  one joins it. Details staleness is `staleDetails`, already generic.
- **A load a newer one replaces uses `latest` (`latest.go`)**, never a
  hand-rolled token or cancel. `Begin`/`BeginTimeout` supersede and cancel;
  `Done` checks and releases; `Cancel` stops without superseding; `Abandon`
  does both. Token-only copies were bugs. Wrap for extra bookkeeping
  (`detailRuns`). `ARCHITECTURE.md` § Latest-only loads: latest.
- **No file save on the UI goroutine — config goes through `App.saveConfig`,
  tracked queries through `App.saveTracked` (`app_saves.go`).** `Config.Save`
  waits up to two seconds on another instance's lock, reads the key file and
  fsyncs; on the UI goroutine every one of those froze the screen (T58).
  Change `a.cfg` first, then ask; report in the callback. `Config.Save` itself
  stays for tests and `FlushSaves`.
- **A confirmed write runs through `App.runWithProgress` (`progress_job.go`),
  never a bare `safego`** — otherwise nothing shows a DROP waiting on a lock and
  it can't be stopped. The job owns context, spinner clock and dialog release
  (panic included); `done` gets `cancelled` only if Cancel was pressed *and*
  the work failed, and a cancelled write still re-reads its folder (the cancel
  may land after commit). `uninterruptible` for statements worse half-stopped
  (Restore from Snapshot, failovers). Batches check `ctx.Err()` per item and
  report via `progressReport`.
- **A node label re-read in place also re-titles the Details pane** —
  `DetailBrowser.Retitle(node)` after the label lands. The Refresh that
  re-fetched the grid titled it first, with the old label (Resource Governor
  and Database Mail both shipped a "(Disabled)" title over a started view).
- **An operation that latches UI state before starting uses
  `App.safegoRepair`**, not `safego` — a panic skips the posted release and the
  latch lives forever (inert Log Viewer toolbar, Activity Monitor tab stuck at
  "Running...", dead page button). Test like
  `TestPageActionLatchClearsWhenTheActionPanics`.
