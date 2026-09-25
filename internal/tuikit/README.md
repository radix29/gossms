# tuikit

The embeddable, application-agnostic TUI library behind goSSMS. It knows
nothing about SQL Server, gosmo or goSSMS — only how to draw and drive widgets
on a `tcell.Screen` — and could be vendored into any tcell application.

## Package map

```
tuikit/
├── theme/      Colour palette + derived tcell.Style helpers — palette.go, styles.go
├── core/       Rect geometry, drawing primitives, string/int helpers
│             — geometry.go, screen.go, drawing.go, strutil.go, mathutil.go,
│               runecol.go (rune index ↔ display column), wordutil.go (word
│               boundaries for Editor and InputField)
├── widgets/    InputField, DropDown, CheckBox, Button, RadioBox, Spinner — one file each
├── layout/     Panel interface, PanelManager (tabs), Splitter — panel.go,
│               panel_manager.go, splitter.go
├── dialogs/    ModalDialog (focus trap), Properties/Alert/Confirm/TypedConfirm
│             (retype-to-confirm)/Prompt (one-line input)/Progress/FileDialog
│             — FileDialog (every file-path prompt) is file_dialog.go (state),
│               _draw, _input, _complete (path completion), and file_system.go:
│               the FileSystem it browses (LocalFileSystem by default; Windows/
│               Posix path rules for a remote one — see ShowOpenOn/ShowSaveOn,
│               and BlockingFileSystem for the "Listing ..." repaint)
├── controls/   MenuBar+ContextMenu, Toolbar, TreeView, DataGrid, ListBox, TabStrip, Editor
│             — menu_bar.go, context_menu.go, menu_item.go (shared types),
│               menu_cascade.go (submenu chain draw/hit-test/keys for both hosts),
│               toolbar.go, treeview.go, listbox.go, tabstrip.go;
│               DataGrid: datagrid.go (state/source/widths), _draw, _input,
│               _overlay (right-click menu, "Show Value" popup);
│               Editor: document.go (the one text-mutation chokepoint; its
│               version counter keys the syntax/wrap/width caches),
│               line_buffer.go (builds a document off the UI goroutine for an
│               O(1) SetLineBuffer), editor.go (state/options), editor_undo.go
│               (span deltas, capped by steps and bytes), editor_search.go (the
│               one regexp engine for Find/Replace/Replace All), editor_block.go
│               (column selection + rectangular clipboard), editor_selection.go,
│               _draw, _wrap, _input, _actions, editor_completion.go (popup);
│               sql_highlighter.go, sql_statement.go (statement/batch bounds),
│               xml_highlighter.go (planview XML tab), json_highlighter.go (JSON
│               cell panel; stateless per line)
├── charts/     Terminal charts from generic series data
│             — canvas.go (off-screen buffer satisfying tcell.Screen's drawing
│               half, rendered once then blitted/scrolled), scale.go (nice-number
│               scales, ticks, formatting), glyph.go (eighth-block ramps),
│               axis.go, legend.go, common.go (Series, stacked-run composition),
│               history.go, stacked_history.go, barchart.go, stacked_bar.go,
│               vbar.go, kpi.go
├── sqltext/    T-SQL text rules — the "GO" separator line rule and SplitBatches;
│             stdlib only, so internal/query and sqlparse can use it
│             — doc.go, go_separator.go, split.go
└── propsheet/  PropertySheet — multi-page editable properties dialog framework
              — doc.go, common.go, rows.go, gridrow.go, editorrow.go,
                togglegrid.go, form.go; sheet.go (state/pages), sheet_draw.go,
                sheet_input.go, sheet_clipboard.go
```

Every sub-package: one file per type or tight group, plus `doc.go`.

## Dependency direction

One-way; nothing imports upward, nothing imports `tui`:

```
theme  ◄── core ◄── widgets ◄── layout ◄── dialogs ◄── propsheet
                       ▲                      ▲            ▲
                       └──────── controls ─────┴────────────┘
```

- **theme** depends only on `tcell`; **core** on `theme` (for `Init()`'s
  default style); **widgets**, **layout**, **dialogs**, **controls** on `core`
  and `theme`.
- **propsheet** is the top, composing `dialogs.ModalDialog`,
  `controls.DataGrid`/`ListBox` and `widgets`.
- **sqltext** is outside the graph: stdlib only, not even `tcell`. `controls`,
  `internal/query` and `internal/tui/sqlparse` use it, so it must never draw.

## Design principles

**No upward calls.** Controls talk outward only via callbacks (`OnExpand`,
`OnSelect`, `OnClick`, `OnConfirm`, …) that `tui` wires.

**Geometry via `core.Rect`.** Widgets store a `core.Rect` and expose
`SetBounds(x, y, w, h)`; containers (`PanelManager`, `Splitter`) compute child
rects and hand them down.

**Self-contained state**, read through getters (`Value()`, `Checked()`,
`Selected()`); the app never pushes into private fields.

**An animation is a function of elapsed time, not a timer.** `widgets.Spinner`
(catalogue: `SpinnerBraille`, `SpinnerCodex`, …; `widgets.Spinners`,
`SpinnerByName`) has no start time or goroutine: `Frame(elapsed)`/
`FrameSince(start)`, redrawn from the host's clock. All frames of a Spinner
share one display width, or a narrower frame leaves the previous one's tail
(`TestSpinnerFramesAreUniformWidth`). `cmd/spindemo` shows them all.

**Optional capability interfaces.** `layout.Panel` requires only `SetBounds`,
`Draw`, `HandleKey`, `HandleMouse`, `Title`. `layout.Activatable`
(`SetActive(bool)`) is called by `PanelManager` on every switch if
implemented; `layout.Disposable` (`Close()`) is called by the host before
`RemovePanel` for panels owning a read or connection.

**Row data behind an interface.** `controls.DataGrid` takes a `RowSource`
(`Len()`, `Row(i)`); `SetData` wraps `SetSource` with a `SliceRowSource`.
Column widths sample only the first `colWidthSampleRows`, so large or streamed
sources scale.

**Display width, not bytes or runes.** `core.DisplayWidth(s)` (via
`clipperhouse/displaywidth`) is the one answer to "how many columns", handling
wide CJK and grapheme clusters; `core.DrawText`, `DrawTextClipped`,
`DrawTextRight`, `Truncate`, `PadRight` build on it. Anything positioned after
a label uses `core.DisplayWidth(label)`, never `len(label)`, or drawing and
hit-testing desync. Rune-indexed text (`Editor`, `InputField` cursors,
selections, wrap segments) converts via `core/runecol.go` only — `RuneWidth`,
`RunesWidth`, `ColumnOfRune`, `RuneIndexAtColumn`. Until 2026-08-02 both
widgets treated rune index as column, and a CJK/emoji character shifted the
rest of its line.

**Async state as data, not goroutines — `propsheet.PropertySheet`.** A
multi-page dialog (page list, a `Form` of `Row`s, OK/Cancel/Apply/Script
Changes) whose pages load lazily. It never spawns a goroutine: it calls
`OnLoadPage(page, seq)`; the host fetches and replies with
`SetPageForm(page, seq, form)`/`SetPageError(page, seq, err)`. `seq` is
sheet-wide, monotonic, never reset by `SetPages`; a stale `seq` is ignored
(`SetPageForm` returns false). **Both must be called on the UI goroutine** —
no locking. Rows share a small `Row` interface plus optional capabilities
(`Editable`, `Copyable`, `KeyHandler`, … — `propsheet/common.go`), so a new
row kind never touches `Form`. **A row that draws a control also implements
`ReadOnlyDrawer`**: under `Form.SetReadOnly`, a row still drawing `[value]` or
`[ ]` looks like a field refusing input.

**Overlays are drawn last and get first refusal of input.** A widget whose open
state floats outside its rect — `DropDown`'s list, `DataGrid`'s menu/"Show
Value" popup, `Editor`'s completion popup (`CompletionActive()`/`DrawOverlay`)
— exposes `DrawOverlay(s)`, which the host calls after everything sharing that
space, and checks it first in its own `HandleKey`/`HandleMouse` (see
`DataGrid.OverlayActive()` and `QueryPanel`), or input goes to whatever sits
underneath.

**Theming is global but swappable**: `theme.Active()`; `theme.SetPalette(p)`
once at startup reskins everything.

## How the application layer uses it

`internal/tui` defines domain types (`NodeType`, `explorerNode`, `nodeData`),
wires controls with callbacks that load via `gosmo`, embeds
`dialogs.ModalDialog` in its dialogs, and implements `layout.Panel` for
`QueryPanel`, `DetailBrowser`, etc. Example: `ObjectExplorer`
(`object_explorer.go`) owns the tree model and projects it into a flat
`[]controls.TreeNode`; all walking, scrolling and expand/collapse lives once in
`controls.TreeView`.

## Adding a new control

1. Pick the package: leaf input → `widgets`; layout primitive → `layout`;
   self-contained modal → `dialogs`; bigger stateful/scrolling → `controls`.
2. Depend only on `core` and `theme` unless building on another tuikit package.
3. Interactive controls expose `SetBounds`, `Draw(tcell.Screen)`, `HandleKey`,
   `HandleMouse`, plus plain getters.
4. If it can be switched off: `SetEnabled(bool)`/`Enabled() bool` with
   `InputField`'s contract (shared by `Button`, `CheckBox`, `RadioBox`) —
   draws greyed (`theme.StyleControlDisabled`; `StyleButtonDisabled` for
   buttons) **and** refuses input, keeps its focus-ring slot, and its own
   setters still work (pinning a value the server requires is the usual reason
   to disable).
5. Never import `internal/tui`.

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

`ConnectDialog` and `HelpDialog` follow this. `dialogs.FileDialog` is the same
pattern inside tuikit (it needs no domain knowledge); it reaches disk only via
its `FileSystem` interface, which lets Backup/Restore's Browse show the
*server's* disks (`serverFS`, over gosmo) while other callers stay local.
