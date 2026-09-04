# UI rules

Rules for `internal/tui` and `internal/tuikit` that are not derivable from the
code — each one is a bug that shipped. `ARCHITECTURE.md` has the reasoning
behind the mouse and async sections; this file has the rules themselves.

## Editor, widgets and grids

- **Seed `QueryPanel.savedText` from `qp.editor.Text()`, never from the string you
  passed to `SetText`.** `SetText` normalizes (tabs expanded, CRLF folded, invalid
  UTF-8 to U+FFFD), so seeding from the source marks the panel dirty on open; a
  falsely dirty panel prompts to save on close and the save rewrites the file
  normalized — `File > Open` shipped that way and converted a UTF-16 script to
  U+FFFD-laden UTF-8 on disk. Anything reading a text file goes through
  `decodeTextFile`/`encodeTextFile` (`text_encoding.go`), which detect encoding
  **from a BOM only** — a guessed encoding rewrites the user's script in one they
  never chose — and preserve the file's line endings. "Preserve" means the
  *majority* ending (`majorityCRLF`): the editor folds CRLF to LF when the text
  is set, so which lines carried a CR is gone before Save can ask, and testing
  for CRLF's mere presence turned one stray CRLF into a whole-file rewrite of a
  mostly-LF script.
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
- **Never call `DataGrid.SetData` on a grid the user is navigating — use
  `redrawGrid` (`prop_grid_helpers.go`), or `resetGrid` when the row *set*
  changed.** `SetData` discards three things: the cell cursor (reset to 0,0), the
  scroll position, and any dragged column width. From inside the grid's own
  `OnSelectRow` it is worst — the callback runs *after* the grid moved, so the move
  is undone, `GridRow` reports "not handled", and `Form` moves focus out on the
  first arrow key; six sites shipped that way, every row but the first unselectable
  by mouse *and* keyboard. Restoring only the cursor is not enough either:
  `SetSelectedCell` ends in `ensureVisible`, which scrolls from zero and lands the
  row against the bottom edge (Server Properties > Permissions moved the whole list
  on every toggle). `redrawGrid` wraps `controls.DataGrid.SetDataPreservingView`
  (widths, then scroll, then selection); outside `internal/tui` call
  `SetDataPreservingView` directly (`propsheet.ToggleGridRow` cannot reach
  `redrawGrid`), and never hand-roll the pair — six pages did, all dropped the
  widths. After an Add/Remove/Revert the cursor *is* placed deliberately, but
  `SetData` + `SetSelectedRow` (seventeen sites) still drops the dragged widths
  with no visible symptom: use `resetGrid(grid, headers, rows, row)`.
  `wireGridEditor` (`ag_props.go`) packages the commit/load/redraw wiring for the
  grid-plus-detail-editor idiom.
- **A mouse gesture's modifier is not yours to rely on — Shift+click especially.**
  A VTE terminal (xfce4-terminal, GNOME Terminal) handles Shift+mouse as its own
  text selection whenever an application has mouse reporting on, and forwards
  nothing: the app sees no event at all, which reads exactly like a broken
  binding. Ctrl and Alt are delivered. So a Shift+mouse gesture needs a second
  modifier meaning the same thing — `extendSelectionMods` (`datagrid_input.go`) is
  `ModShift | ModAlt` for this reason — and a keyboard route as well. Key
  Diagnostics logs mouse events (button, modifiers, position, drags collapsed),
  which is what tells a swallowed modifier from a wrong binding; a tmux harness
  cannot, since injecting the SGR sequence bypasses the terminal that would have
  eaten it.
- **A `Ctrl+Shift+<letter>` chord never arrives as `KeyCtrlX`.** tcell folds a
  Ctrl-modified rune into `KeyCtrlA..KeyCtrlZ` only when Ctrl is the *sole*
  modifier, so Ctrl+Shift+O arrives as `KeyRune "o"` (Kitty) or `"O"` (xterm
  modifyOtherKeys) with `ModCtrl|ModShift`, and legacy terminals send plain `0x0F`.
  `normalizeCtrlRune` (`app_events.go`, called once atop `handleKey`) folds it back
  app-wide — a binding testing `KeyCtrlX` *and* `ModShift` works only because of it,
  and only on modern keyboard protocols, so anything that must work everywhere needs
  a function key or Ctrl+non-letter too, the way Connect has F9. Verify a new chord
  by injecting its encodings with `tmux send-keys -H` and reading Key Diagnostics;
  a unit test proves nothing here.
- **Every menu item and toolbar button must be context-gated** — never let a click
  or keypress do nothing, do the wrong thing, or crash on an unmet precondition (a
  connection, an active query panel, an Object Explorer selection).
  `MenuItem`/`ToolbarButton` have an `Enabled func() bool`; the reactive fallback is
  a guard plus `setStatus(...)` in existing wording ("Not connected — use File >
  Connect", "No active query panel").

## Dialogs and the clipboard

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
  deliberately isn't a host; an inert clipboard is correct there. A new dialog with
  a text field implements `FocusedClipboardTarget`, returning an **explicit** nil on
  every miss (a typed nil widget in an interface is not a nil interface).
  `TestEveryDialogWithTextEntryIsAClipboardHost` catches a missing host but cannot
  catch a reintroduced fall-through — which is why this is written here. A dialog
  that must *react* to an edit (re-filter a list, refresh a preview) implements
  `core.ClipboardEditHandler` and checks the target it is handed rather than
  assuming the edit landed in the field it watches. `App.pasteInto` drops the text
  unless the widget it was aimed at is still the target — every clipboard read is
  asynchronous, so the dialog may be gone by the time it returns.

## Panels, toolbars and grid hosts

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
  `SELECT 1 -- pick one FROM dbo.t`, the whole FROM clause inside the comment.
  `OnShowValue`'s first parameter is the **column** index, not the row, so the row
  comes from `grid.SelectedRow()` (`DataGrid.openViewer` reads the cell at
  `selRow`/`selCol`). `QueryStorePanel.showValue` resolves it from
  `qsResultRow.queryText` held in memory; `DetailBrowser.showQueryStoreValue`
  cannot — its grid is `[][]string` shared with every other node type — so it
  re-reads the statement by the row's `Query ID` through
  `gosmo.QueryStoreQueryTextContext`. A test asserting only that *a* read happened
  cannot catch a hook that addressed the wrong row: the fake answers every id
  alike, so assert the bound id with `fakeInstance.ReadArgs`.

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
`dragField`.** Points 1, 4 and 5 all land on the same three calls, each with a
placement that is not local to it: `Release` above `ConsumeOutsideClick` *and*
above any mode switch, `Replay` after `ConsumeOutsideClick` and before any
hit-test, `Clear` in `Show`. The dialog keeps only its own hit-testing and focus
handling; `ARCHITECTURE.md` § dialogs.FieldGesture has the failure each placement
prevents. Two meta-tests enforce it and both are needed —
`TestEveryDialogWithATextFieldOwnsAFieldGesture` walks the built dialogs for one
owning a loose `*widgets.InputField` and no gesture;
`TestFieldGestureCallsAreOrderedCorrectly` reads both dialog packages' source for
the order plus `Claim` used at all. Options, Prompt and TypedConfirm hand-rolled
it and got it wrong long after the other seven were converted, every test passing:
a drag from the field to the button row pressed the button under the pointer,
which on Prompt *accepted* the rename and on TypedConfirm answered the
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
when it finishes, and a panic unwinds straight past that callback, so it survives
for the object's lifetime: the Log File Viewer's toolbar stays inert, an Activity
Monitor tab sits at "Running..." forever, a Properties page's button refuses every
later click. Cover it the way `TestPageActionLatchClearsWhenTheActionPanics` does
— panic the action, then assert the *next* click still runs.

