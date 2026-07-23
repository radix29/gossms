# tuikit

`tuikit` is the embeddable, application-agnostic TUI library that powers
goSSMS. It knows nothing about SQL Server, gosmo, or goSSMS — it only knows
how to draw and drive terminal widgets on a `tcell.Screen`. It could be
vendored into any other tcell-based console application.

## Package map

```
tuikit/
├── theme/      Colour palette + derived tcell.Style helpers
│             — palette.go, styles.go
├── core/       Geometry (Rect), drawing primitives, string/int helpers
│             — geometry.go, screen.go, drawing.go, strutil.go, mathutil.go
├── widgets/    InputField, DropDown, CheckBox, Button, RadioBox — one file per widget
├── layout/     Panel interface, PanelManager (tabs), Splitter (resizable)
│             — panel.go, panel_manager.go, splitter.go
├── dialogs/    ModalDialog base (focus trap), PropertiesDialog, AlertDialog, ConfirmDialog, TypedConfirmDialog, FileDialog
│             — modal.go, properties_dialog.go, alert_dialog.go, confirm_dialog.go,
│               typed_confirm_dialog.go (retype-to-confirm); FileDialog (browse/
│               save-as, list+path entry, used for every file path prompt in
│               internal/tui) is split across file_dialog.go (state), file_dialog_draw.go,
│               file_dialog_input.go, and file_dialog_complete.go (path completion)
├── controls/   MenuBar+ContextMenu, Toolbar, TreeView, DataGrid, ListBox, TabStrip, Editor (+ SQL highlighter/statement select)
│             — one file per group: menu_bar.go/context_menu.go (+ shared MenuItem/Menu
│               types in menu_item.go), toolbar.go, treeview.go, listbox.go, tabstrip.go;
│               DataGrid is split across datagrid.go (state/data source/column
│               widths), datagrid_draw.go, datagrid_input.go, and
│               datagrid_overlay.go (right-click menu, "Show Value" popup);
│               Editor is split across editor.go (state/options/undo),
│               editor_selection.go, editor_draw.go, editor_wrap.go,
│               editor_input.go, editor_actions.go, editor_completion.go
│               (generic completion/IntelliSense popup), sql_highlighter.go,
│               sql_statement.go (T-SQL statement/batch boundary detection)
└── propsheet/  PropertySheet — multi-page editable properties dialog framework
              — doc.go, common.go, rows.go, gridrow.go, togglegrid.go, form.go;
                PropertySheet itself is split across sheet.go (state/page list),
                sheet_draw.go, sheet_input.go, and sheet_clipboard.go
```

Every tuikit sub-package follows the same convention: one file per type or
tightly-related group of types, plus a `doc.go` holding the package doc
comment.

## Dependency direction

Strict, one-way dependency graph — no package below ever imports a package
above it, and nothing in `tuikit` imports the `tui` (application) package:

```
theme  ◄── core ◄── widgets ◄── layout ◄── dialogs ◄── propsheet
                       ▲                      ▲            ▲
                       └──────── controls ─────┴────────────┘
```

- **theme** has zero internal dependencies (only `tcell`).
- **core** depends on `theme` for `Init()`'s default style.
- **widgets**, **layout**, **dialogs**, and **controls** depend on `core` and `theme`.
- **dialogs** and **controls** are the only packages aware of higher-level
  composition (a `DataGrid` inside a `Panel`, a `ModalDialog` hosting buttons).
- **propsheet** sits at the top: it composes `dialogs.ModalDialog`,
  `controls.DataGrid`/`ListBox`, and `widgets` into `PropertySheet`, the
  multi-page editable-properties framework. Nothing below it imports it.

## Design principles

**No upward calls.** Every control communicates outward via plain Go
callbacks (`OnExpand`, `OnSelect`, `OnClick`, `OnConfirm`, …), never by
importing or calling into application code. The `tui` package wires these
callbacks; `tuikit` never imports `tui`.

**Geometry via `core.Rect`.** Every widget stores its position/size as a
`core.Rect` and exposes `SetBounds(x, y, w, h)`. Layout containers
(`PanelManager`, `Splitter`) compute child rectangles and hand them down —
children never reach upward to ask "how much space do I have?".

**Self-contained state.** Widgets hold their own value/selection/scroll
state and expose it through getters (`Value()`, `Checked()`, `Selected()`).
The application reads state when it needs it; it does not push state into
private fields.

**Optional capability interfaces.** `layout.Panel` only requires the five
methods every panel needs (`SetBounds`, `Draw`, `HandleKey`, `HandleMouse`,
`Title`). Panels that care about gaining/losing focus implement the optional
`layout.Activatable` interface (`SetActive(bool)`); `PanelManager` detects
this via a type assertion and calls it automatically on every switch — no
panel is forced to implement focus tracking it doesn't need.

**Row data behind an interface, not a concrete slice.** `controls.DataGrid`
takes any `RowSource` (`Len() int`, `Row(i int) []string`) rather than
requiring a `[][]string` in hand. `SetData` is a thin convenience wrapper
over `SetSource` for the common already-in-memory case (`SliceRowSource`);
a future paged or streamed result set can implement `RowSource` directly
without `DataGrid` itself changing. Column widths are sized by sampling
only the first `colWidthSampleRows` rows, not every row, so this scales to
sources holding far more than that.

**Display width, not byte length or rune count.** Terminal columns are not
the same as Go string length. `core.DisplayWidth(s string) int` (backed by
`github.com/clipperhouse/displaywidth`, already a transitive dependency of
`tcell` v3.4+) is the single source of truth for "how many columns does
this text occupy" — it accounts for wide CJK glyphs and multi-rune
grapheme clusters. `core.DrawText`, `core.DrawTextClipped`,
`core.DrawTextRight`, `core.Truncate`, and `core.PadRight` are all built on
it. Any new code that positions something *after* a label (a button after
its text, an input box after its label, a hit-test region after a tab) must
use `core.DisplayWidth(label)`, never `len(label)` — `len()` returns bytes,
which is wrong for any non-ASCII text and will desync drawing from mouse
hit-testing.

**Async state as data, not goroutines — `propsheet.PropertySheet`.**
`PropertySheet` is a multi-page dialog (page list on the left, a `Form` of
`Row`s on the right, OK/Cancel/Apply/Script Changes below) where each
page's data loads
lazily and asynchronously. Like every other tuikit control it never spawns
a goroutine or imports the application layer: when a page needs loading it
calls `OnLoadPage(page, seq)` and waits. The caller (`internal/tui`) does
the actual fetch — typically on a background goroutine — and reports the
result via `SetPageForm(page, seq, form)` or `SetPageError(page, seq,
err)`. `seq` is a per-page monotonic counter; a call with a stale `seq`
(the page was refreshed again, or the sheet was hidden, before the result
arrived) is silently ignored. **`SetPageForm`/`SetPageError` must only be
called from the UI goroutine** — `PropertySheet` does no locking of its
own, the same contract `App.postEvent` already provides for every other
background-to-UI handoff in `internal/tui`. A `Form`'s rows unify under a
small `Row` interface plus optional capability interfaces (`Editable`,
`Copyable`, `KeyHandler`, …) — see `propsheet/common.go` — so adding a new
row kind never requires touching `Form` itself.

**Overlays are drawn last and get first refusal of input.** A widget whose
open state floats independently of its own `SetBounds` rect — `DropDown`'s
open list, `DataGrid`'s right-click menu/"Show Value" popup, `Editor`'s
completion popup (`CompletionActive()`/`DrawOverlay`, `editor_completion.go`)
— exposes a `DrawOverlay(s tcell.Screen)` that the host must call *after*
every other widget sharing that screen space has drawn, so nothing paints
over it. The same widget must also get exclusive first refusal of every
key/mouse event while that state is open, checked at the top of the host's
own `HandleKey`/`HandleMouse` (see `DataGrid.OverlayActive()` and
`QueryPanel.HandleKey`/`HandleMouse` in `internal/tui`) — otherwise a click
or keypress meant for the floating overlay gets routed by position/focus to
whatever widget normally owns those screen coordinates instead.

**Theming is global but swappable.** `theme.Active()` returns the live
palette; call `theme.SetPalette(p)` once at start-up to reskin every widget
in the library without touching draw code.

## How the application layer uses it

`internal/tui` (the goSSMS application) is a thin layer that:

1. Defines SQL-Server-specific domain types (`NodeType`, `explorerNode`, `nodeData`).
2. Wires `tuikit` controls together with callbacks that load data via `gosmo`.
3. Embeds `dialogs.ModalDialog` in its own dialogs (`ConnectDialog`,
   `HelpDialog`) to add domain-specific fields and behaviour on top of the
   generic focus-trap/overlay/button-row machinery.
4. Implements `layout.Panel` for `QueryPanel` and `DetailBrowser` so they can
   be hosted side-by-side in a `layout.PanelManager`.

Example — the Object Explorer is `internal/tui/object_explorer.go`'s
`ObjectExplorer`, which owns the SQL Server tree model (`explorerNode`) and
projects it into a flat `[]controls.TreeNode` for `controls.TreeView` to
render and navigate. `ObjectExplorer` never duplicates tree-walking,
scrolling, or expand/collapse logic — all of that lives once, in
`controls.TreeView`.

## Adding a new control

1. Decide which package it belongs in (a leaf input → `widgets`; a layout
   primitive → `layout`; a self-contained modal → `dialogs`; anything bigger
   with internal state/scrolling → `controls`).
2. Take only `core` and `theme` as dependencies unless you're building on
   top of another `tuikit` package.
3. Expose `SetBounds`, `Draw(tcell.Screen)`, `HandleKey`, `HandleMouse` for
   anything interactive; expose plain getters for any value state.
4. Never import `internal/tui`.

## Adding a new dialog in the application

```go
type MyDialog struct {
    dialogs.ModalDialog
    // your fields...
}

func NewMyDialog(app *App) *MyDialog {
    d := &MyDialog{}
    d.InitModal(app.screen, "My Dialog", 50, 12)
    return d
}

func (d *MyDialog) Draw(s tcell.Screen) {
    if !d.Visible() { return }
    d.DrawBase(s)              // overlay + box + title
    // ...draw your content inside d.InnerRect()...
    d.DrawSeparator(s)
    d.DrawButtons(s, []string{"OK", "Cancel"}, activeIdx)
}

func (d *MyDialog) HandleMouse(ev *tcell.EventMouse) bool {
    if !d.Visible() { return false }
    if d.ConsumeOutsideClick(ev) { return true }  // focus trap
    if i := d.ButtonClicked(ev, []string{"OK", "Cancel"}); i >= 0 {
        // handle button i
    }
    return true
}
```

This is exactly the pattern `ConnectDialog` and `HelpDialog` follow in
`internal/tui`; `dialogs.FileDialog` (any file path prompt — Open, Save,
Save As, Results To File) is the same idea one level down, built directly
in `tuikit/dialogs` rather than `internal/tui` since it needs no SQL Server
domain knowledge at all.
