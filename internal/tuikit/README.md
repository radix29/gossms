# tuikit

`tuikit` is the embeddable, application-agnostic TUI library that powers
goSSMS. It knows nothing about SQL Server, gosmo, or goSSMS — it only knows
how to draw and drive terminal widgets on a `tcell.Screen`. It could be
vendored into any other tcell-based console application.

## Package map

```
tuikit/
├── theme/      Colour palette + derived tcell.Style helpers
├── core/       Geometry (Rect), drawing primitives, string/int helpers
├── widgets/    InputField, DropDown, CheckBox, Button
├── layout/     Panel interface, PanelManager (tabs), Splitter (resizable)
├── dialogs/    ModalDialog base (focus trap), PropertiesDialog, AlertDialog, ConfirmDialog
└── controls/   MenuBar, ContextMenu, TreeView, DataGrid, Editor (+ SQL highlighter)
```

## Dependency direction

Strict, one-way dependency graph — no package below ever imports a package
above it, and nothing in `tuikit` imports the `tui` (application) package:

```
theme  ◄── core ◄── widgets ◄── layout ◄── dialogs
                       ▲                      ▲
                       └──────── controls ─────┘
```

- **theme** has zero internal dependencies (only `tcell`).
- **core** depends on `theme` for `Init()`'s default style.
- **widgets**, **layout**, **dialogs**, and **controls** depend on `core` and `theme`.
- **dialogs** and **controls** are the only packages aware of higher-level
  composition (a `DataGrid` inside a `Panel`, a `ModalDialog` hosting buttons).

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

This is exactly the pattern `ConnectDialog`, `ConnStrDialog`, and
`HelpDialog` follow in `internal/tui`.
