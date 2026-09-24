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
- **Editor auto-indent lives in the `KeyEnter` branch of `editor_input.go`, never
  in `insertNewline`.** `Paste` and `blockPaste` drive `insertNewline` once per
  pasted line, so indenting there re-indents each line on top of the indentation
  it already carries and a pasted script arrives as a staircase. The branch reads
  `blockEditing()` *before* `deleteSelection`, which drops the block along with
  the selection — asking afterwards always answers `false` — and the spaces go in
  under the existing `pushUndoLocal`, so one Ctrl+Z undoes the whole Enter.
  `SetSmartIndent` adds one level on top when the text *left of the cursor*
  ends with `(` or with `select`/`from`/`where` as its **last token** — last
  token, not "the line starts with", which is what stops the indentation
  drifting right one level per clause down a query. It is off by default and
  on only for the T-SQL editors: those keywords mean nothing in the plain
  multi-line text boxes that also use `Editor`. Being in the key handler, it
  is typing-only by construction — a pasted script keeps its own indentation.
- **Call `Editor.SetIndentWidth` at construction, before any `SetText`.**
  `SetText` expands tabs at the editor's *current* width, and `QueryPanel.savedText`
  is seeded from `Text()` afterwards (see the bullet above), so a width set later
  would make an already-open file read as edited. Changing the width at runtime
  (Options > OK) deliberately does not re-expand existing text; it pushes the new
  width into every open `QueryPanel`, into every open property sheet's
  `EditorRow`s (`PropertySheet.SetEditorIndentWidth` — the Agent job-step Command
  box is the one *writable* editor that is not a `QueryPanel`), and into
  `controls.SetDefaultIndentWidth`, which is how editors built where the config
  is out of reach (the read-only property-sheet T-SQL rows) pick it up.
  A writable editor built outside a `QueryPanel` takes the width from the config
  at construction — the job-step panel is handed `cfg.IndentWidth` by its two
  callers — so one editor never reads its two indent settings from two places.
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
  grid-plus-detail-editor idiom, and `wireCellToggle`/`newCellToggleGrid`
  (`prop_grid_helpers.go`) package the "activate a cell in one column to change
  that row" idiom — the bounds check, the mutation and the `redrawGrid` — that
  nine Properties pages had hand-rolled. A page pairing such a grid with a
  read-only detail block uses `staticBlock`, whose `set()` clears every row it
  is not given a value for: clearing a "Selected X" block one `SetValue("")` per
  row is where a row gets missed, and a missed row goes on describing the object
  the selection just left.
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
  Connect", "No active query panel"). A context-menu item that writes (label
  starts Delete, Enable, Start, Stop, …) also needs a permission `gate`;
  `TestEveryWriteMenuItemIsGated` enforces it, and its exemption list is where a
  deliberately ungated write says why.

- **A switched-off control keeps its place in the focus ring.** `Button`,
  `CheckBox`, `RadioBox` and `InputField` all take `SetEnabled(bool)`/`Enabled()
  bool` and hold one contract: a disabled control still draws — greyed, via
  `theme.StyleControlDisabled` (`StyleButtonDisabled` for a button, which has no
  border to lose, so dropping its ground would leave nothing to click) — and
  refuses keys and clicks, but it does not vanish from the caller's focus order.
  A control that disappears when it is switched off renumbers every focus index
  recorded against it.

- **A busy indicator is a function of elapsed time, not a timer.**
  `widgets.Spinner` holds no start time and starts no goroutine:
  `Frame(elapsed)`/`FrameSince(start)` answer which frame shows, and the host
  redraws from a clock it already has. Do not add a goroutine to animate one.

- **`SetBounds` must do nothing when the bounds have not moved.** Hosts lay out
  from `Draw`, so a control is handed the same rect on every frame; work done
  unconditionally there runs 60 times a second against the user's input. A
  `ListBox` that re-ran `ensureVisible` on each call snapped its scroll back
  onto the selected row between one frame and the next, and the wheel looked
  unable to scroll past the selection at all. Compare the rect first, and keep
  the re-clamp for the call that really changes it (first real layout, resize).

- **Setting a widget's value programmatically drops its selection.** The
  selection was anchored in the text being replaced, and an anchor past the end
  of a shorter new value paints the blanks beyond it as selected —
  `widgets.InputField` refilled from the Connect dialog's History pane drew a
  highlight wider than the server name it had just been given. `SetValue`
  clears `selecting` and re-anchors on the cursor; a widget that grows its own
  `SetXxx` does the same.

- **A pre-filled field reads from its start; a field being typed into follows
  its caret.** `SetValue` leaves the caret at the end, so a value longer than
  the box shows its *tail* — right for a Destination path, where the file name
  is what matters (`TestInputFieldSetValueLongValueShowsItsTail`), wrong for a
  value the user picked rather than typed. A caller filling a field from a
  selection calls `InputField.ShowFromStart()` afterwards, as the Connect
  dialog's `PreFill` does for every field; the caret stays at the end and the
  first key scrolls the view back to it.

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

- **A tabbed dialog pane rebuilds its focus ring per tab; it does not just skip
  the hidden tab's controls while drawing.** The Connect dialog holds two tabs'
  worth of controls in one dialog, and `rebuildFocusable` puts only the visible
  tab's in the ring — a control the hidden tab owns that stays in the ring is
  reachable by Tab and takes keystrokes nobody can see the effect of. The same
  function rebuilds the ring when the dialog switches between its two-pane and
  one-pane layouts, so a pane that is not drawn is not focusable either. Focus
  is carried across the rebuild when the same widget is in the new ring (the
  Custom Properties editor is on both tabs) and falls back to the first entry
  when it is not. `TestConnectDialogTabRingHoldsOnlyTheVisibleTab` pins it.
- **A dialog whose layout depends on the terminal size re-decides it in `Show`
  *and* in `Relayout`, not only in the constructor.** `ModalDialog.Relayout`
  recentres at the size last requested, so a dialog that picks its own width
  from `screen.Size()` must override `Relayout` to recompute that width and
  call `SetSize` — otherwise a terminal widened across an open dialog keeps the
  narrow layout until it is closed and reopened. The hit-testing geometry then
  comes from the same `layoutFields` the drawing does, once per frame.

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
  `gosmo.QueryStoreQueryText`. A test asserting only that *a* read happened
  cannot catch a hook that addressed the wrong row: the fake answers every id
  alike, so assert the bound id with `fakeInstance.ReadArgs`.

## Mouse, overlays, and async UI

- **A mouse release goes to every open dialog, not just the front one**
  (`App.routeRelease`). `ModalDialog`'s button latch is cleared by the release
  its own `HandleMouse` sees, and a dialog that opens a nested one *from a
  button press* never sees that release — so its next press is refused as a
  continuation of the click that opened the child, and the dialog appears to
  ignore the first click after the child closes. The Connect dialog's Delete
  confirmation shipped that way. Acting on a click needs `Button1`, so the
  dialogs underneath only reset latches; a new dialog's `HandleMouse` must keep
  it that way.

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
   clears both, and `FieldGesture.Clear` drops the gesture *and* the field's own
   `mouseDragging`, for the dialogs that hand the same field back on the reshow.
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

**The release forwarded to the *focused* field is
`forwardReleaseToFocusedField` (`dialog_common.go`), not a hand-written type
switch.** `FieldGesture.Release` covers the field that claimed a press;
invariant 5 also wants the focused `*controls.Editor`, which the gesture never
tracks and which keeps its own latch. Its `InputField` arm is belt-and-braces
and has no reachable caller: in Backup, Restore and Connect the only route a
press takes to an `InputField` is `FieldGesture.Claim`, so a latched field
always has the gesture too. The one way the two halves came apart — a dialog
dismissed mid-drag and reopened, in the dialogs that build their fields once —
is closed in `FieldGesture.Clear`, which now drops the field's own
`mouseDragging` as well (`InputField.CancelMouseDrag`), pinned by
`TestConnectDialogShowClearsTheFieldLatchTooNotJustTheGesture`. Backup, Restore and Connect
each wrote that switch out and Connect's was the only one with the Editor arm.
It answers only "was this the release, and has it been delivered" — the
`!= Button1` early return stays at the call site, because Connect reads the
wheel between the release and its hit-tests.
`TestFieldGestureCallsAreOrderedCorrectly` fails on a gesture-owning `tui`
dialog whose `HandleMouse` type-asserts to an `InputField` or an `Editor`.

**Not every mouse event is followed by a draw.** `App.Run` skips the frame after a
motion-only event (button state unchanged, no wheel) while more events are queued:
a drag over a results grid drew a 6 ms frame per cell crossed and fell 2.2 s
behind the pointer. Presses, releases and wheel notches still draw each. So state
that a *motion* handler depends on must be set in the handler, never in `Draw` —
geometry laid out in `Draw` is current as of the last press, not the last motion.

An overlay drawn last gets **first refusal** of every key/mouse event while open —
`DataGrid.OverlayActive()` at the top of `QueryPanel.HandleKey`/`HandleMouse`; the
focused row before positional routing in `propsheet.Form.HandleMouse`.

A background goroutine reports its result with **`App.postAndWake(fn)`**, never its
two halves (`postEvent` then `wakeEventLoop`) by hand — getting the ordering wrong
leaves the result queued and invisible until an unrelated keypress drains it
(shipped bug: tree nodes stuck on "Loading..."). See `ARCHITECTURE.md` § Async
result delivery: postAndWake. `QueryPanel`'s elapsed-timer tick is the one
legitimate bare `wakeEventLoop()` caller: it has no callback to post, only a redraw.

**An apply closure never writes page state.** A page's `propApply` runs on the
pipeline's goroutine while the page's own callbacks (`DirtyFn`, a grid's rows,
a button) read the same variables on the UI goroutine — and it runs under
Script Changes too, where nothing reached the server. It only issues
statements; a real Apply reloads the page afterwards (`InvalidateAll`), so there
is nothing to clean up. AG Listener Properties cleared its pending addresses at
the end of its apply: after Script Changes the grid still listed them "To be
added" on a page no longer dirty, and the next Apply sent nothing.
`TestApplyClosuresDoNotWritePageState` fails on any `func(ctx context.Context)
error` literal that assigns a captured variable; the work closure passed to
`runPageAction`/`runPageActionOnce` is exempt, since handing its result to the
UI-goroutine completion through a variable declared beside the call is the
point of it.

**An apply closure writes on the context it is handed, and only through
gosmo.** `runApplySteps` wraps that context in `gosmo.WithStatementObserver`, and
the observer is how a failed Apply knows which pages reached the server: those
reload, the rest keep their edits, and a New-object dialog whose first page ran
to the end counts as created. A write issued on `d.ctx`, `sc.Context()` or a
fresh `context.Background()` — or straight through `database/sql` — is invisible
to it, so a page that fails after such a write keeps its edits and re-sends the
statement on the next Apply. Deriving from the handed context (`WithTimeout`,
`WithoutCancel`) keeps the observer. The one case the observer cannot see is a
gosmo disable window whose closing re-enable is refused; a page that can reach
that re-reads and marks the failure `applyCommitted` (Audit Properties).

**A load that a newer one replaces uses `latest` (`internal/tui/latest.go`),
never a hand-rolled token or cancel.** `Begin`/`BeginTimeout` supersede the run
in flight *and* cancel it; `Done` reports whether the result is still wanted and
releases the context; `Cancel` stops a run without superseding it, `Abandon`
does both. Every copy of this that shipped with only the token half was a bug —
Refresh leaked replaced nodes' loads, and a Properties dialog's previous showing
reached the next. A site needing more bookkeeping wraps it (`detailRuns`) rather
than growing it. See `ARCHITECTURE.md` § Latest-only loads: latest.

**A write the user confirmed runs through `App.runWithProgress`
(`progress_job.go`), never a bare `safego`.** The confirmation closes on Yes,
and without the progress dialog nothing on screen says a DROP is still waiting
on a lock — the tree stays live under a write that has not landed, and there is
no way to stop it. The job owns the context, the spinner's clock and the
dialog's release (panic included); `done` gets `cancelled` only when Cancel was
pressed *and* the work failed, and a cancelled write re-reads its folder anyway,
because the cancel can reach the server after the statement committed. A
statement that stopping halfway leaves worse off than waiting sets
`uninterruptible` (Restore from Snapshot, both failovers). A batch checks
`ctx.Err()` before each item as well as handing ctx to it, and reports each item
through `progressReport`.

**A background operation that latches UI state before it starts must use
`App.safegoRepair`, not `App.safego`** — a busy flag, a "loading" placeholder, a
toolbar the flag dims. The latch is released by the callback the goroutine posts
when it finishes, and a panic unwinds straight past that callback, so it survives
for the object's lifetime: the Log File Viewer's toolbar stays inert, an Activity
Monitor tab sits at "Running..." forever, a Properties page's button refuses every
later click. Cover it the way `TestPageActionLatchClearsWhenTheActionPanics` does
— panic the action, then assert the *next* click still runs.

