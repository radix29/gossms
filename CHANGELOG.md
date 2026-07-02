# Changelog

## Unreleased — menu dropdown not rendering (z-order bug) + Close Query rename

### Bug: opening a top menu didn't show its items

Root cause: `MenuBar.Draw()` drew the bar row *and* the open dropdown in a
single call, invoked early in `App.draw()` (right after `Clear()`). The
dropdown extends downward into the same rows `explorer.Draw()` and
`panels.Draw()` render into immediately afterward — so every frame, the
dropdown was painted into the back buffer and then immediately overwritten
before `Show()` ever displayed it. The menu bar row itself worked (it's on
its own row, row 0), but the item list never became visible.

Fixed by splitting `MenuBar.Draw()` into two methods: `Draw()` (bar row
only, unchanged call site) and a new `DrawOverlay()` (the dropdown, if
open). `App.draw()` now calls `menuBar.DrawOverlay()` *after* every panel
and the status bar, alongside `contextMenu.Draw()` — which already worked
correctly because it was already called last, following the same
must-draw-last rule overlay content needs.

The same bug existed in `widgets.DropDown` (used for the Auth-method
selector in the Connect dialog): its open item list was drawn inline
within `Draw()`, before several other input fields in `ConnectDialog.Draw()`
that sit lower in the dialog and would paint over the open list. Fixed with
the identical split — `Draw()` / `DrawOverlay()` — with `ConnectDialog.Draw()`
now calling `ddAuth.DrawOverlay(s)` last, after every other field.

**General rule going forward**: any widget whose open/expanded state can
extend into space other sibling widgets or panels also draw into must
separate that overlay content into its own draw call, invoked after
everything else in the same frame — the same pattern `ContextMenu` and
modal dialogs already followed correctly.

### Rename: "Close Panel" → "Close Query"

Renamed the File menu's `Ctrl+W` item, plus the matching references in the
in-app Help dialog and `README.md`'s keyboard table, for consistency.

## Unreleased — `EventKey.Modifiers()` confirmed against uploaded tcell source

The user uploaded the actual pinned tcell source (`v3.4.0`), resolving the
uncertainty from the previous entry. Confirmed directly from `key.go`:

```go
func (ev *EventKey) Modifiers() ModMask {
	return ev.mod
}
```

The `Modifiers()` fix applied previously was correct. While this source was
available, every other tcell symbol gossms uses was also cross-checked
against it (not just spot-checked): the full `Screen` interface
(`EventQ`, `SetContent`, `ShowCursor`, `EnableMouse`, `Size`, `SetStyle`,
`Init`, `Fini`), `NewScreen`, `NewEventInterrupt(data any)`, `ModCtrl`/
`ModAlt`, every `Key*` constant referenced (including confirming
`KeyBacktab` exists and `KeyCtrlTab` genuinely does not), `EventMouse`'s
`Buttons()`/`Position()`, mouse button/wheel constants, `ColorWhite`,
`NewRGBColor`, and `Style`'s `Background`/`Foreground`/`Bold` methods —
including verifying `tcell.Color` is a genuine Go type alias
(`type Color = color.Color`) for the `color` sub-package type those style
methods take, so no conversion is needed anywhere `theme.go` builds a style
from `tcell.NewRGBColor(...)`. No further changes were required — this was
a confirmation pass with the same code already delivered previously.

## Unreleased — `EventKey` modifier accessor: `Mod()` → `Modifiers()`

Real compiler diagnostics: `ev.Mod undefined (type *tcell.EventKey has no
field or method Mod, but does have unexported field mod)`. The earlier fix
that introduced `ev.Mod()` (in the tcell v3 `PollEvent`/`PostEvent`
reconciliation) was itself wrong — likely sourced from `tcell`'s `main`
branch on GitHub, which can be ahead of whatever is actually tagged as the
pinned `v3.4.0`. Search results for tcell's modifier accessor kept mixing
v1, v2, v3, and `main`-branch documentation with no reliable way to isolate
which applies to `v3.4.0` specifically.

Changed all 6 call sites (`app.go`, `controls.go` ×2, `layout.go` ×4) from
`ev.Mod()` to `ev.Modifiers()` — the original v1/v2 accessor name, which a
doc comment directly in `key.go` (`// Modifiers returns the modifiers that
were present with the key press.`) suggests still exists in v3. This is the
best-supported guess available, but **not independently confirmed** the
way the `gosmo` fixes were (those were checked against your actual
uploaded source). If your next compile still fails on this, run:

```
go doc github.com/gdamore/tcell/v3 EventKey
```

That will show the exact real method name for your exact pinned version
directly, with zero ambiguity — please paste the output (or the resulting
compiler error) if `Modifiers()` isn't it either.

## Unreleased — real gosmo and tcell v3.4.0 API reconciliation

Real compiler diagnostics from the actual local build (VS Code + gopls
against the real `../gosmo` checkout and pinned `tcell v3.4.0`) surfaced
every place earlier code had guessed at an API shape instead of reading it.
The user then uploaded the actual `gosmo` source, which made it possible to
fix these precisely instead of guessing again. All fixes below are verified
against real source, not inferred.

### tcell v3.4.0: `Screen` no longer has `PollEvent`/`PostEvent`

Confirmed against the real `tcell/screen.go`: v3 replaced both methods with
a single channel, `EventQ() chan Event`. Reading and posting events are now
plain channel operations, and the channel closes when `Fini()` is called.

- `App.Run()`'s event loop rewritten from `for { ev := s.PollEvent(); if ev
  == nil { break } ... }` to `for ev := range s.EventQ() { ... }` — the
  range loop now exits automatically when `Fini()` closes the channel,
  instead of relying on a nil-event sentinel that no longer exists.
- Every `a.screen.PostEvent(tcell.NewEventInterrupt())` (used to wake the
  main loop after a background goroutine queues UI work) became
  `a.screen.EventQ() <- tcell.NewEventInterrupt(nil)` — three call sites in
  `app.go`, one in `query_panel.go` (the latter wasn't yet flagged by the
  compiler but had the identical bug).
- `tcell.NewEventInterrupt` now requires a `data any` argument; `nil` is
  passed since gossms doesn't use the payload.

### `tcell.KeyCtrlTab` was never a real constant

Removed. tcell has no distinct key constant for Ctrl+Tab (unlike KeyCtrlA
through KeyCtrlZ, which map to real ASCII control codes — Tab isn't in that
range). Ctrl+Tab is now detected the same way Ctrl+Left/Right already were:
checking `ev.Mod()&tcell.ModCtrl` on the plain `KeyTab` case.

### `gosmo` API corrections (verified against uploaded source)

| What the code assumed | What gosmo actually has |
|---|---|
| `ConnectionOptions.Encrypt bool` | `Encrypt string` (`"true"/"false"/"disable"/"strict"`, mirroring the go-mssqldb DSN parameter) |
| `Server.Database(name)` | `Server.DatabaseByName(name)` |
| `Database.Table(schema, name)` | `Database.TableByName(schema, name)` |
| `Database.Name` (field) | `Database.Name()` (method) |
| `Database.Status` (field) | `Database.State()` (method, different name too) |
| `Database.RecoveryModel` (field) | `Database.RecoveryModel()` (method, returns named type `RecoveryModel`, needs `string(...)` to store in a plain string) |
| `Database.CompatibilityLevel` (field) | `Database.CompatibilityLevel()` (method) |
| `Database.Collation` (field) | `Database.Collation()` (method) |
| `Database.CreateDate` (assumed string field) | `Database.CreateDate()` (method, returns `time.Time`) |
| `Database.Owner` | **doesn't exist** — removed from both properties views |
| `Database.SizeMB` | **doesn't exist** — real equivalent is `Database.SpaceUsed() (SpaceInfo, error)`, a separate query. Added to the single-database properties view (one extra round-trip is fine); intentionally *not* added to the all-databases list view, which would otherwise mean N extra queries for N databases |
| `ServerInfo.RootDirectory` | **doesn't exist** — replaced with the real fields `DefaultDataPath` / `DefaultLogPath` / `DefaultBackupPath` |
| `Table.CreateDate`, `View.CreateDate`, `StoredProcedure.CreateDate`/`ModifyDate` (assumed strings) | all real fields, but typed `time.Time`, not `string` — every place one was put directly into a `[]string` grid row now goes through a new `formatSQLDate(time.Time) string` helper in `tree_node.go` |

`Table.Name`, `Table.Schema`, `View.Name`/`Schema`, `StoredProcedure.Name`/`Schema`,
`Login.Name`, `Job.Name`, `Sequence.Name`/`Schema`, `Synonym.Name`/`Schema`,
`User.Name`, `Schema.Name`, `Column.Name`/`DataType`/`IsNullable` were all
**confirmed correct as originally written** (genuine plain fields) — these
were not touched. `Scripter`, `NewScripter`, `DefaultScriptOptions`,
`ScriptTable`/`ScriptView`/`ScriptStoredProcedure`/`ScriptFunction` were
also confirmed correct as originally written.

### Silent (non-compiling) bug: `AuthMethod` enum value mismatch

`config.AuthMethod`'s constants were hand-guessed with values that don't
match `gosmo.AuthMethod`'s real `iota` sequence (e.g. gossms's
`AuthEntraInteractive = 9` vs. gosmo's real `AuthEntraInteractive = 8`,
which is actually gosmo's `AuthEntraDeviceCode` value). Because
`internal/db/connection.go` did a raw `gosmo.AuthMethod(opts.AuthMethod)`
numeric cast, this **compiled without error** but would have silently
connected using the wrong authentication method at runtime — a much worse
class of bug than a compile error, since it would only surface as a
confusing runtime authentication failure or, worse, a successful connection
using an unintended credential path.

Fixed by adding `toGosmoAuth(config.AuthMethod) gosmo.AuthMethod` in
`internal/db/connection.go`, an explicit switch-based mapping. The two
enums are no longer required to share numeric values, and
`config.AuthMethod`'s doc comment was updated to state this explicitly so
a future contributor doesn't reintroduce the raw-cast version.
`fedauthForMethod`'s string-based mapping (already explicit, not numeric)
was checked against gosmo's real `fedauthValue` map and confirmed correct
as originally written.

## Unreleased — go.mod reconciliation and display-width correctness

The placeholder `go.mod` (hand-written, guessing at versions) was replaced
with the project's actual `go.mod`, which revealed real dependency
versions and one significant architectural fact: `github.com/radix29/gosmo`
is resolved via `replace github.com/radix29/gosmo => ../gosmo` — a local
sibling checkout, not a tagged remote module. `tcell/v3` is pinned at
`v3.4.0`.

### What the real go.mod exposed

`tcell v3.4.0` pulls in `github.com/clipperhouse/displaywidth` and
`github.com/clipperhouse/uax29/v2` as transitive dependencies — tcell
itself now does grapheme-cluster-aware width measurement internally for
its own higher-level `Put`/`PutStr` convenience methods. tuikit's drawing
primitives don't use those methods (everything goes through the lower-level
`Screen.SetContent`), so nothing failed to compile — but it surfaced a
real, pre-existing **rendering correctness bug**: every text-measurement
and text-drawing helper in tuikit assumed 1 rune = 1 terminal column. That
assumption silently mis-renders for wide CJK characters and multi-rune
grapheme clusters (combining marks, flags, etc.) — a realistic scenario
given SQL Server table/column/database names can be arbitrary Unicode.

### Fix: `core.DisplayWidth` and width-aware primitives throughout

`displaywidth` was promoted from an indirect to a direct dependency, and
`internal/tuikit/core` now exposes `core.DisplayWidth(s string) int` as the
single, package-confined entry point other tuikit packages use instead of
importing `displaywidth` themselves (preserving the
`theme ← core ← {widgets, layout, dialogs, controls}` dependency rule).

`core.DrawText`, `core.DrawTextClipped`, `core.DrawTextRight`,
`core.Truncate`, and `core.PadRight` were rewritten to iterate grapheme
clusters via `displaywidth.StringGraphemes` and advance the screen column
by true display width rather than by 1 per rune.

Every caller across the library that duplicated `len(label)`-style byte-length
arithmetic for column positioning or hit-testing was fixed to use
`core.DisplayWidth` instead, so drawing and mouse hit-testing always agree
on where a label actually ends:

- `layout.PanelManager` — tab bar drawing and tab-click hit-test
- `controls.MenuBar` — bar tab drawing/hit-test, dropdown width calculation
  (also fixed a separate pre-existing bug where `dropdownContains`'s
  hard-coded `w := 28` didn't match the dynamically-computed width used by
  `drawDropdown`, via a new shared `dropdownGeometry` helper used by both)
- `controls.ContextMenu` — popup width calculation
- `controls.DataGrid` — query-result column width calculation
  (`computeColWidths`) — the most realistic place to hit actual Unicode
  data
- `controls.TreeView` — Object Explorer label rendering, previously a
  manual rune-by-rune loop, now delegates to `core.DrawTextClipped`
- `widgets.InputField`, `widgets.DropDown`, `widgets.Button` — label-to-input
  offset and button width calculations
- `dialogs.ModalDialog` — `DrawButtons`/`ButtonClicked` button-row layout
  and hit-testing

`controls.Editor` was deliberately left rune-indexed (documented inline as
an intentional scope limit) — its cursor/selection/click-to-position model
is rune-indexed throughout, and reworking that for grapheme-awareness is a
materially larger change than fixing read-only label/grid rendering. SQL
text is overwhelmingly ASCII, so this only affects alignment inside
wide-character string literals.

### Other go.mod-driven fixes

- `README.md` installation instructions corrected: `go install
  .../gossms@latest` does not work while the `replace` directive points at
  a local `../gosmo` checkout; both repos must be cloned side by side.
- `Makefile` gained a `check-gosmo` target that fails fast with a clear
  message if `../gosmo` is missing, run automatically before `tidy`.

## Unreleased — tuikit library extraction

The flat `internal/tui` package (17 files, ~5,400 lines, mixing low-level
drawing primitives with SQL-Server-specific application logic) was split
into:

- **`internal/tuikit`** — a new embeddable, application-agnostic TUI library
  with six sub-packages: `theme`, `core`, `widgets`, `layout`, `dialogs`,
  `controls`. ~3,000 lines. Zero knowledge of SQL Server, gosmo, or any
  goSSMS-specific type.
- **`internal/tui`** — slimmed to the application layer: SQL Server domain
  types, the object-tree fetch logic, and dialogs/panels composed from
  `tuikit` building blocks. ~2,300 lines.

### What moved where

| Old file (`internal/tui`) | New home |
|---|---|
| `draw.go` | `tuikit/core` (geometry + drawing primitives) |
| `theme.go` | `tuikit/theme` (palette is now swappable via `theme.SetPalette`) |
| `widgets.go` | `tuikit/widgets` (`InputField`, `DropDown`, `CheckBox`, + new `Button`) |
| `modal_dialog.go` | `tuikit/dialogs` (`ModalDialog` base, + new generic `PropertiesDialog`, `AlertDialog`, `ConfirmDialog`) |
| `panel_manager.go` | `tuikit/layout` (`Panel` interface, `PanelManager`, + new generic `Splitter`) |
| `menu_bar.go`, `context_menu.go` | `tuikit/controls` |
| `query_editor.go`, `results_grid.go` | Generalised into `tuikit/controls.Editor` and `tuikit/controls.DataGrid` (no SQL Server knowledge — SQL highlighting is a pluggable `Highlighter` function, `controls.SQLHighlighter`) |
| `object_explorer.go`'s tree rendering/navigation | Generalised into `tuikit/controls.TreeView` (generic `TreeNode` with an `any` `Tag`); `internal/tui/object_explorer.go` now only owns the SQL Server tree model and projects it into `[]controls.TreeNode` |

### New reusable capability introduced during the split

- **`layout.Splitter`** — a single, generic draggable/keyboard-resizable
  divider type used for *both* the explorer/panels split (vertical) and the
  editor/results split inside `QueryPanel` (horizontal). Previously each
  panel hand-rolled its own splitter drag math.
- **`controls.Highlighter`** — syntax highlighting is now a plain function
  type (`func(line []rune) []ColorRun`) that `controls.Editor` calls per
  line. `controls.SQLHighlighter(palette)` is the built-in implementation;
  any other language's highlighter can be dropped in without touching the
  editor.
- **`dialogs.ModalDialog`** gained shared button-row helpers
  (`DrawButtons`, `ButtonClicked`, `DrawSeparator`) so every dialog no
  longer reimplements button-row layout and hit-testing.
- **`dialogs.AlertDialog`** and **`dialogs.ConfirmDialog`** — generic
  single/two-button dialogs, available for future use, built on the same
  `ModalDialog` base as the SQL-Server-specific dialogs.

### Behavioural fixes made during the split

- **Thread safety**: `ObjectExplorer`'s node-ID allocation (`allocID`,
  mutating a counter and a map) previously ran partly on a background
  goroutine via `fetchChildren`. IDs are now assigned exclusively in
  `SetChildren`, which only ever runs on the UI goroutine via `postEvent`.
- **Splitter drag termination**: both `PanelManager.HandleMouse` and
  `QueryPanel.HandleMouse` now always forward button-release events to the
  active panel/splitter, even if the mouse has moved outside the panel's
  column bounds, so a drag can never get stuck mid-resize.
- **Lost focus-highlight regression caught during the split**: extracting
  `QueryPanel`'s "is this the active panel" title-bar highlight and cursor
  visibility out of the old monolithic `app.go` (which toggled it manually
  inline) initially dropped the call that flips it when switching tabs.
  Fixed by adding `layout.Activatable`, an optional interface
  (`SetActive(bool)`) that `PanelManager` now detects via type assertion and
  invokes automatically on every panel switch, add, and removal — so any
  `Panel` that cares about focus gets notified without `layout.Panel` itself
  having to carry that requirement.

### Dependency rule enforced

`tuikit/*` packages never import `internal/tui`, and within `tuikit` the
dependency graph is strictly one-directional: `theme ← core ← {widgets,
layout, dialogs, controls}`. See `internal/tuikit/README.md`.

## v1.0.0 — Go 1.26 (initial release)

### Go 1.26 language and runtime optimisations applied

This release targets **Go 1.26** and makes deliberate use of every relevant
new feature and runtime improvement introduced in that version.

---

#### Language: `new(expr)` — pointer initialisation with value

Go 1.26 extends the `new` builtin to accept an expression, not just a type.
`new(expr)` allocates a variable of the expression's type, initialises it to
the value of `expr`, and returns `*T`. This eliminates the two-step
`p := &T{}` / `*p = v` pattern throughout the codebase.

Applied in:
- `NewApp()` — `new(App{explorerW: 30, ...})`
- `NewQueryPanel()` — `new(QueryPanel{splitRatio: 0.5, ...})`
- `NewQueryEditor()` — `new(QueryEditor{lines: [][]rune{{}}})`
- `NewResultsGrid()` — `new(ResultsGrid{status: "Ready"})`
- All widget constructors: `NewInputField`, `NewDropDown`, `NewCheckBox`,
  `NewDetailBrowser`, `NewPanelManager`, `NewMenuBar`, `NewHelpDialog`,
  `NewPropertiesDialog`
- `addServerNode()` — `new(TreeNode{Label: ..., Type: NodeServer, ...})`
- `fetchChildren()` — every `child(...)` helper uses `new(TreeNode{...})`
- `TreeNode.visibleChildren()` — loading placeholder
- `config.Load()` — `new(Config)` on empty/error path

---

#### Standard library: `errors.AsType[E]` — type-safe error checking

`errors.AsType[E error](err error) (E, bool)` is a generic replacement for
`errors.As` introduced in Go 1.26. It is:
- **Type-safe** — compile-time error if `E` doesn't implement `error`
- **No reflection** — 3× faster than `errors.As`, one fewer allocation
- **Scoped** — the typed value is scoped to the `if` block

Applied in:
- `connectServer()` — `errors.AsType[*db.ConnectionError](err)` to surface
  the new typed `ConnectionError` struct with `Server` and `Cause` fields

The `db.ConnectionError` struct was added specifically to make this idiomatic.

---

#### Standard library: `slices` package (Go 1.21+, pervasive in Go 1.26)

The `slices` package is the idiomatic replacement for hand-rolled slice
manipulation. Go 1.26's `go fix` modernisers recommend migrating to it.

Applied in:
- `disconnectActive()` — `slices.Delete(a.connections, i, i+1)` replaces
  manual `append(s[:i], s[i+1:]...)` splice
- `RemoveRoot()` — `slices.Delete(oe.roots, i, i+1)`
- `HandleKey()` left-arrow — `slices.Index(oe.flat, node.Parent)` replaces
  a hand-written linear search loop
- `FormatNodePath()` — `slices.Reverse(parts)` replaces manual reversal loop

---

#### Runtime: Green Tea garbage collector (default in Go 1.26)

The Green Tea GC, promoted from experiment to default in Go 1.26, improves
marking and scanning of **small objects** through better memory locality and
CPU scalability. Expected 10–40% reduction in GC overhead for
allocation-heavy programs.

Code changes made to maximise Green Tea GC benefit:

1. **Pre-allocated slices throughout `fetchChildren()`** — every `make([]T,
   0, len(source))` before a loop avoids repeated `append` growth and reduces
   the number of short-lived backing arrays the GC must track.

2. **`rebuild()` reuses its backing array** — `oe.flat = oe.flat[:0]` resets
   the length without releasing the backing array. Subsequent `append` calls
   reuse the allocation, producing no garbage between tree refreshes.

3. **`tokenize()` pre-allocates** — `make([]token, 0, 8)` for the token
   slice; most SQL lines have fewer than 8 tokens so this is allocation-free
   in the common case.

4. **`execQuery()` reuses scan buffers** — the `vals`/`ptrs` slices are
   allocated once before the `rows.Next()` loop, not on every row.

5. **`InputField.HandleKey()` uses a pre-sized rune slice** for insertion
   instead of `append(append([]rune{}, ...))` double-copy.

---

#### Compiler: faster small object allocation (Go 1.26)

Go 1.26's compiler generates calls to size-specialised allocation routines,
reducing small object allocation cost by up to 30% (19% measured on M1).

The widespread use of `new(TreeNode{...})` in `fetchChildren()` creates many
small, short-lived structs. These now hit the fast allocation path directly.

---

#### Standard library: `strings.Builder` (Go 1.10+, idiomatic in 1.26)

`QueryEditor.Text()` uses `strings.Builder` for linear-time string
concatenation from the editor's line slice, avoiding quadratic `+=` growth.

---

#### Other idioms updated

- `any` alias used instead of `interface{}` in `execQuery()` scan buffers
  (Go 1.18+, standard style in Go 1.26 codebases)
- `switch` with `case a.dialog.Visible():` — replaced `if/else if` chains
  with `switch { case ...: }` for cleaner modal dialog dispatch in `draw()`
  and `handleKey()`
- Parallel assignment `a.focus, a.explorer.active = "panels", false`
  replaces two separate statements where both change together
- `clampF()` helper for `float64` split-ratio clamping — avoids repetition
  and is inlineable by the Go 1.26 compiler
